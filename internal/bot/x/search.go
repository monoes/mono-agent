//go:build !nosocial

package x

import (
	"context"
	"errors"
	"fmt"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// tweetsJS reads every post (article[data-testid=tweet]) currently rendered
// in the main column. X virtualises its timelines — posts scrolled far away
// leave the DOM — so callers accumulate rows by id across scrolls.
//
// Promoted posts are flagged, not guessed from substrings: a post is an ad
// when it has no timestamp link of its own (ads carry none) or when a leaf
// element's whole text is exactly "Ad"/"Promoted" (so "Add", "Admin" or a
// post that merely mentions ads are kept).
const tweetsJS = `() => {
	const txt = (el) => el ? (el.innerText || el.textContent || '').trim() : '';
	const root = document.querySelector("[data-testid='primaryColumn']") || document;
	const num = (el) => {
		if (!el) return '';
		const label = el.getAttribute('aria-label') || '';
		const m = label.match(/^\s*([0-9][0-9.,\s]*[KkMmBb]?)/);
		return m ? m[1].trim() : '';
	};
	const out = [];
	for (const a of root.querySelectorAll("article[data-testid='tweet']")) {
		const timeLink = [...a.querySelectorAll("a[href*='/status/']")].find((l) => l.querySelector('time') && l.closest('article') === a && !l.closest("div[role='link']"));
		let ad = !timeLink;
		if (!ad) {
			for (const el of a.querySelectorAll('span, div')) {
				if (el.children.length) continue;
				const t = (el.textContent || '').trim();
				if (t === 'Ad' || t === 'Promoted') { ad = true; break; }
			}
		}
		const row = { ad };
		if (timeLink) {
			const href = timeLink.getAttribute('href') || '';
			const m = href.match(/\/status\/(\d+)/);
			row.id = m ? m[1] : '';
			row.url = new URL(href.split('?')[0], location.origin).href.replace(/\/(analytics|photo\/\d+|video\/\d+)$/, '');
			const t = timeLink.querySelector('time');
			row.created_at = t ? (t.getAttribute('datetime') || '') : '';
		}
		const user = a.querySelector("[data-testid='User-Name']");
		if (user) {
			const lines = txt(user).split('\n').map((s) => s.trim()).filter(Boolean);
			row.author_name = lines[0] || '';
			const h = lines.find((l) => /^@[A-Za-z0-9_]{1,15}$/.test(l));
			row.author_username = h ? h.slice(1) : '';
		}
		if (!row.author_username && row.url) {
			const m = row.url.match(/x\.com\/([A-Za-z0-9_]{1,15})\/status\//);
			if (m) row.author_username = m[1];
		}
		const text = [...a.querySelectorAll("[data-testid='tweetText']")].find((el) => !el.closest("div[role='link']")) || a.querySelector("[data-testid='tweetText']");
		row.text = txt(text);
		row.reply_count = num(a.querySelector("[data-testid='reply']"));
		row.repost_count = num(a.querySelector("[data-testid='retweet'], [data-testid='unretweet']"));
		const likeBtn = a.querySelector("[data-testid='like'], [data-testid='unlike']");
		row.like_count = num(likeBtn);
		row.liked = !!a.querySelector("[data-testid='unlike']");
		out.push(row);
	}
	const empty = !!root.querySelector("[data-testid='emptyState']");
	return { rows: out, empty };
}`

type rawTweet struct {
	Ad             bool   `json:"ad"`
	ID             string `json:"id"`
	URL            string `json:"url"`
	CreatedAt      string `json:"created_at"`
	AuthorName     string `json:"author_name"`
	AuthorUsername string `json:"author_username"`
	Text           string `json:"text"`
	ReplyCount     string `json:"reply_count"`
	RepostCount    string `json:"repost_count"`
	LikeCount      string `json:"like_count"`
	Liked          bool   `json:"liked"`
}

type tweetsSnapshot struct {
	Rows  []rawTweet `json:"rows"`
	Empty bool       `json:"empty"`
}

// scrollJS scrolls the window by most of a viewport.
const scrollJS = `() => { window.scrollBy(0, Math.round(window.innerHeight * 0.9)); return true; }`

// SearchPosts searches X posts ("Top" results) for query and returns up to
// max posts (promoted posts excluded). query may hold several searches, one
// per line; rows carry the search they came from in "keyword".
func (b *XBot) SearchPosts(ctx context.Context, p browser.PageInterface, query string, max int) ([]map[string]interface{}, error) {
	var queries []string
	for _, q := range strings.Split(query, "\n") {
		if q = strings.TrimSpace(q); q != "" {
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return nil, errors.New("x: search query is required")
	}
	if max <= 0 {
		max = 20
	}
	var out []map[string]interface{}
	seen := map[string]bool{}
	for _, q := range queries {
		rows, err := b.searchOne(ctx, p, q, max, seen)
		if err != nil {
			return out, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (b *XBot) searchOne(ctx context.Context, p browser.PageInterface, q string, max int, seen map[string]bool) ([]map[string]interface{}, error) {
	if err := navigate(p, b.SearchURL(q)); err != nil {
		return nil, err
	}
	var snap tweetsSnapshot
	err := poll(ctx, loadTimeout, func() (bool, error) {
		if err := botpkg.EvalJSON(p, tweetsJS, &snap); err != nil {
			return false, nil
		}
		return len(snap.Rows) > 0 || snap.Empty, nil
	})
	if err != nil {
		if lerr := checkLoginRedirect(p); lerr != nil {
			return nil, lerr
		}
		return nil, fmt.Errorf("x: search %q: no results rendered: %w", q, err)
	}
	var out []map[string]interface{}
	stale := 0
	for len(out) < max {
		added := 0
		for _, t := range snap.Rows {
			if t.Ad || t.ID == "" || seen[t.ID] {
				continue
			}
			seen[t.ID] = true
			added++
			out = append(out, tweetRow(t, q))
			if len(out) >= max {
				break
			}
		}
		if len(out) >= max || snap.Empty && len(snap.Rows) == 0 {
			break
		}
		if added == 0 {
			stale++
			if stale >= 3 {
				break
			}
		} else {
			stale = 0
		}
		var ok bool
		if err := botpkg.EvalJSON(p, scrollJS, &ok); err != nil {
			return out, fmt.Errorf("x: scroll search results: %w", err)
		}
		if err := sleep(ctx, scrollSettle); err != nil {
			return out, err
		}
		if err := botpkg.EvalJSON(p, tweetsJS, &snap); err != nil {
			return out, fmt.Errorf("x: read search results: %w", err)
		}
	}
	return out, nil
}

func tweetRow(t rawTweet, keyword string) map[string]interface{} {
	row := map[string]interface{}{
		"url":             t.URL,
		"id":              t.ID,
		"author_username": t.AuthorUsername,
		"author_name":     t.AuthorName,
		"post_text":       t.Text,
		"created_at":      t.CreatedAt,
		"liked":           t.Liked,
		"keyword":         keyword,
	}
	if t.AuthorUsername != "" {
		row["author_url"] = "https://x.com/" + t.AuthorUsername
	}
	for k, v := range map[string]string{"reply_count": t.ReplyCount, "repost_count": t.RepostCount, "like_count": t.LikeCount} {
		if n, ok := parseCount(v); ok {
			row[k] = n
		}
	}
	return row
}
