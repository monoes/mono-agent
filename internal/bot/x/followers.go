//go:build social

package x

import (
	"context"
	"fmt"
	"strings"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// userCellsJS reads the UserCell rows of the main column's user list (the
// sidebar's "Who to follow" cells are outside primaryColumn and ignored).
const userCellsJS = `() => {
	const txt = (el) => el ? (el.innerText || el.textContent || '').trim() : '';
	const root = document.querySelector("[data-testid='primaryColumn']");
	if (!root) return { rows: [], empty: false, ready: false };
	const rows = [];
	for (const cell of root.querySelectorAll("[data-testid='UserCell']")) {
		let handle = '';
		for (const el of cell.querySelectorAll('span, div')) {
			if (el.children.length) continue;
			const t = (el.textContent || '').trim();
			if (/^@[A-Za-z0-9_]{1,15}$/.test(t)) { handle = t.slice(1); break; }
		}
		if (!handle) {
			const a = [...cell.querySelectorAll('a[href]')].map((l) => l.getAttribute('href')).find((h) => /^\/[A-Za-z0-9_]{1,15}$/.test(h || ''));
			if (a) handle = a.slice(1);
		}
		if (!handle) continue;
		const nameLink = [...cell.querySelectorAll('a[href]')].find((l) => (l.getAttribute('href') || '').toLowerCase() === '/' + handle.toLowerCase() && txt(l) && !txt(l).startsWith('@'));
		const name = nameLink ? txt(nameLink).split('\n')[0].trim() : '';
		// Accessible descriptions/labels of controls in the cell (X's follow
		// button is aria-describedby a hidden "Click to Follow <user>" div).
		const described = new Set();
		for (const el of cell.querySelectorAll('[aria-describedby], [aria-labelledby]')) {
			for (const id of ((el.getAttribute('aria-describedby') || '') + ' ' + (el.getAttribute('aria-labelledby') || '')).split(/\s+/)) {
				if (id) described.add(id);
			}
		}
		const hidden = (el) => {
			for (let n = el; n && n !== cell; n = n.parentElement) {
				if (n.id && described.has(n.id)) return true;
				const cs = getComputedStyle(n);
				if (cs.display === 'none' || cs.visibility === 'hidden') return true;
				if (cs.position === 'absolute' && (n.offsetWidth <= 1 || n.offsetHeight <= 1)) return true;
			}
			return false;
		};
		const bio = [...cell.querySelectorAll("div[dir='auto']")]
			.filter((d) => {
				// The cell itself is a button in X's markup; controls inside it are not bio.
				const ctl = d.closest("a, button, [role='button'], [role='link']");
				return (!ctl || ctl === cell) && !d.closest("[data-testid='userFollowIndicator']") && !d.parentElement.closest("div[dir='auto']") && !hidden(d);
			})
			.map(txt).filter((t) => t && !/^Click to (Un)?follow /i.test(t)).join('\n');
		const btn = cell.querySelector("[data-testid$='-follow'], [data-testid$='-unfollow']");
		const tid = btn ? (btn.getAttribute('data-testid') || '') : '';
		const img = cell.querySelector('img');
		rows.push({
			username: handle,
			full_name: name,
			bio,
			is_following: tid.endsWith('-unfollow'),
			follows_you: !!cell.querySelector("[data-testid='userFollowIndicator']"),
			is_verified: !!cell.querySelector("[data-testid='icon-verified'], svg[aria-label*='erified']"),
			profile_picture_url: img ? (img.getAttribute('src') || '') : '',
		});
	}
	return { rows, empty: !!root.querySelector("[data-testid='emptyState']"), ready: true };
}`

type rawUserCell struct {
	Username          string `json:"username"`
	FullName          string `json:"full_name"`
	Bio               string `json:"bio"`
	IsFollowing       bool   `json:"is_following"`
	FollowsYou        bool   `json:"follows_you"`
	IsVerified        bool   `json:"is_verified"`
	ProfilePictureURL string `json:"profile_picture_url"`
}

type userCellsSnapshot struct {
	Rows  []rawUserCell `json:"rows"`
	Empty bool          `json:"empty"`
	Ready bool          `json:"ready"`
}

// followListPath maps an export source type to the profile sub-page.
func followListPath(sourceType string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(sourceType)) {
	case "", "FOLLOWERS", "FOLLOWERS_FETCH":
		return "followers", nil
	case "VERIFIED_FOLLOWERS", "VERIFIED_FOLLOWERS_FETCH":
		return "verified_followers", nil
	case "FOLLOWING", "FOLLOWING_FETCH":
		return "following", nil
	}
	return "", fmt.Errorf("x: unknown sourceType %q (want FOLLOWERS_FETCH, VERIFIED_FOLLOWERS_FETCH or FOLLOWING_FETCH)", sourceType)
}

// ListFollowers opens target's followers (or verified followers, or
// following) list directly and collects up to max users, scrolling the
// virtualised list.
func (b *XBot) ListFollowers(ctx context.Context, p browser.PageInterface, target, sourceType string, max int) ([]map[string]interface{}, error) {
	_, handle, err := b.profileURL(target)
	if err != nil {
		return nil, err
	}
	sub, err := followListPath(sourceType)
	if err != nil {
		return nil, err
	}
	if max <= 0 {
		max = 100
	}
	listURL := "https://x.com/" + handle + "/" + sub
	if err := navigate(p, listURL); err != nil {
		return nil, err
	}
	var snap userCellsSnapshot
	err = poll(ctx, loadTimeout, func() (bool, error) {
		if err := botpkg.EvalJSON(p, userCellsJS, &snap); err != nil {
			return false, nil
		}
		return len(snap.Rows) > 0 || snap.Empty, nil
	})
	if err != nil {
		if lerr := checkLoginRedirect(p); lerr != nil {
			return nil, lerr
		}
		return nil, fmt.Errorf("x: %s list of @%s did not render: %w", sub, handle, err)
	}
	var out []map[string]interface{}
	seen := map[string]bool{}
	stale := 0
	for len(out) < max {
		added := 0
		for _, u := range snap.Rows {
			key := strings.ToLower(u.Username)
			if seen[key] {
				continue
			}
			seen[key] = true
			added++
			out = append(out, map[string]interface{}{
				"username":            u.Username,
				"profile_url":         "https://x.com/" + u.Username,
				"full_name":           u.FullName,
				"bio":                 u.Bio,
				"is_following":        u.IsFollowing,
				"follows_you":         u.FollowsYou,
				"is_verified":         u.IsVerified,
				"profile_picture_url": u.ProfilePictureURL,
				"source":              sub,
				"source_username":     handle,
			})
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
			return out, fmt.Errorf("x: scroll %s list: %w", sub, err)
		}
		if err := sleep(ctx, scrollSettle); err != nil {
			return out, err
		}
		if err := botpkg.EvalJSON(p, userCellsJS, &snap); err != nil {
			return out, fmt.Errorf("x: read %s list: %w", sub, err)
		}
	}
	return out, nil
}
