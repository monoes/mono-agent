//go:build social

package instagram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// ---------------------------------------------------------------------------
// Profile data (READ)
// ---------------------------------------------------------------------------

// GetUserInfo returns profile data for username. It first asks Instagram's
// web_profile_info endpoint from inside the page (same origin, the user's
// own session — exactly what Instagram's frontend does); when that fails it
// opens the profile and parses the rendered page.
func (b *InstagramBot) GetUserInfo(ctx context.Context, p browser.PageInterface, username string) (map[string]interface{}, error) {
	if username == "" {
		return nil, fmt.Errorf("instagram: username is required for get_user_info")
	}
	cur, _ := p.GetURL()
	onSite := strings.Contains(hostOf(cur), "instagram.com")
	if !onSite {
		if err := b.open(ctx, p, profileURL(username)); err != nil {
			return nil, err
		}
	}
	if res, err := b.fetchProfileViaJS(p, username); err == nil {
		return res, nil
	}
	if cur, _ = p.GetURL(); b.ExtractUsername(cur) != username || !onSite {
		if err := b.open(ctx, p, profileURL(username)); err != nil {
			return nil, err
		}
	}
	var res map[string]interface{}
	err := poll(ctx, findTimeout, func() (bool, error) {
		r, err := b.profileFromDOM(p, username)
		if err != nil {
			return false, err
		}
		res = r
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("instagram: could not read profile of %s: %w", username, err)
	}
	return res, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// fetchProfileViaJS calls /api/v1/users/web_profile_info/ from the page.
func (b *InstagramBot) fetchProfileViaJS(p browser.PageInterface, username string) (map[string]interface{}, error) {
	var out struct {
		Error string                 `json:"error"`
		Data  map[string]interface{} `json:"data"`
	}
	err := evalJS(p, `async (u) => {
		const csrf = (document.cookie.match(/csrftoken=([^;]+)/) || [])[1] || '';
		try {
			const r = await fetch('/api/v1/users/web_profile_info/?username=' + encodeURIComponent(u), {
				headers: { 'x-ig-app-id': '936619743392459', 'x-requested-with': 'XMLHttpRequest', 'x-csrftoken': csrf },
				credentials: 'include',
			});
			if (!r.ok) return { error: 'HTTP ' + r.status };
			return { data: await r.json() };
		} catch (e) { return { error: String((e && e.message) || e) }; }
	}`, &out, username)
	if err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("web_profile_info: %s", out.Error)
	}
	user := extractUserFromResponse(out.Data)
	if user == nil {
		return nil, errors.New("web_profile_info: no user in response")
	}
	if u := getString(user, "username"); u != "" && !strings.EqualFold(u, username) {
		return nil, fmt.Errorf("web_profile_info: asked for %q, got %q", username, u)
	}
	return buildProfileResult(username, user), nil
}

// extractUserFromResponse navigates the JSON response structure to find the
// user object. Instagram uses different response formats:
//   - web_profile_info: {"data": {"user": {...}}}
//   - graphql: {"data": {"user": {...}}} or {"graphql": {"user": {...}}}
func extractUserFromResponse(raw map[string]interface{}) map[string]interface{} {
	if data, ok := raw["data"].(map[string]interface{}); ok {
		if user, ok := data["user"].(map[string]interface{}); ok {
			if _, hasID := user["id"]; hasID {
				return user
			}
			if _, hasUsername := user["username"]; hasUsername {
				return user
			}
		}
	}
	if gql, ok := raw["graphql"].(map[string]interface{}); ok {
		if user, ok := gql["user"].(map[string]interface{}); ok {
			return user
		}
	}
	return nil
}

// buildProfileResult converts an Instagram user object to our standard format.
func buildProfileResult(username string, user map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		"platform":     "INSTAGRAM",
		"username":     username,
		"url":          profileURL(username),
		"full_name":    getString(user, "full_name"),
		"introduction": getString(user, "biography"),
		"is_verified":  getBool(user, "is_verified"),
		"is_private":   getBool(user, "is_private"),
		"image_url":    firstNonEmpty(getString(user, "profile_pic_url_hd"), getString(user, "profile_pic_url")),
		"website":      getString(user, "external_url"),
	}
	count := func(key string) (string, bool) {
		if edge, ok := user[key].(map[string]interface{}); ok {
			if c, ok := edge["count"].(float64); ok {
				return strconv.FormatInt(int64(c), 10), true
			}
		}
		return "", false
	}
	if c, ok := count("edge_followed_by"); ok {
		result["follower_count"] = c
	}
	if c, ok := count("edge_follow"); ok {
		result["following_count"] = c
	}
	if c, ok := count("edge_owner_to_timeline_media"); ok {
		result["content_count"] = c
	}
	return result
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// domProfile is what profileScript reads off a rendered profile page.
type domProfile struct {
	State       string   `json:"state"`
	Username    string   `json:"username"`
	FullName    string   `json:"full_name"`
	Bio         string   `json:"bio"`
	Followers   string   `json:"followers"`
	Following   string   `json:"following"`
	Posts       string   `json:"posts"`
	Image       string   `json:"image"`
	Website     string   `json:"website"`
	Verified    bool     `json:"verified"`
	Private     bool     `json:"private"`
	OGDesc      string   `json:"og_description"`
	OGTitle     string   `json:"og_title"`
	LDJSON      []string `json:"ldjson"`
	Category    string   `json:"category"`
	Unavailable bool     `json:"unavailable"`
}

const profileScript = `() => {
	const meta = (p) => { const m = document.querySelector('meta[property="' + p + '"]'); return m ? m.getAttribute('content') || '' : ''; };
	const out = { og_description: meta('og:description'), og_title: meta('og:title'),
		ldjson: Array.from(document.querySelectorAll('script[type="application/ld+json"]')).map((s) => s.textContent) };
	const body = VT();
	out.unavailable = /sorry, this page isn.t available/i.test(body);
	out.private = /this account is private/i.test(body);
	const h = hdr();
	if (!h) { out.state = 'no_header'; return out; }
	out.state = 'ok';
	const h2 = h.querySelector('h2, h1');
	out.username = h2 ? T(h2) : '';
	out.verified = !!h.querySelector('svg[aria-label="Verified"], [title="Verified"]');
	const img = h.querySelector('img[alt*="profile picture"]') || h.querySelector('img');
	out.image = img ? img.getAttribute('src') || '' : '';
	// Counts: the stats list items ("12 posts", "1,234 followers", "56 following").
	const exact = (el) => { const t = el && el.querySelector('[title]'); return t ? t.getAttribute('title') : ''; };
	for (const li of h.querySelectorAll('ul li')) {
		const t = T(li);
		const num = exact(li) || ((t.match(/^([\d.,]+\s*[kmb]?)/i) || [])[1] || '');
		if (/follower/i.test(t)) out.followers = num;
		else if (/following/i.test(t)) out.following = num;
		else if (/post/i.test(t)) out.posts = num;
	}
	for (const a of h.querySelectorAll('a[href]')) {
		const href = a.getAttribute('href') || '';
		if (/\/followers\/?$/.test(href) && !out.followers) out.followers = exact(a) || ((T(a).match(/^([\d.,]+\s*[kmb]?)/i) || [])[1] || '');
		if (/\/following\/?$/.test(href) && !out.following) out.following = exact(a) || ((T(a).match(/^([\d.,]+\s*[kmb]?)/i) || [])[1] || '');
		if (!out.website && (a.target === '_blank' || /l\.instagram\.com/.test(href)) && !userOf(href)) out.website = href;
	}
	// Name and bio live in the section after the stats.
	const secs = Array.from(h.querySelectorAll('section'));
	const statsSec = secs.find((s) => s.querySelector('ul li'));
	const infoSec = statsSec ? secs[secs.indexOf(statsSec) + 1] : null;
	if (infoSec) {
		const spans = Array.from(infoSec.querySelectorAll('span[dir="auto"]')).filter((s) => !s.closest('button, a, [role="button"]'));
		const texts = spans.map(T).filter(Boolean);
		if (texts.length) out.full_name = texts[0];
		const bio = spans.find((s) => T(s) && T(s) !== out.full_name && !s.querySelector('span[dir="auto"]'));
		const bioBtn = infoSec.querySelector('[role="button"] span[dir="auto"]');
		out.bio = bio ? T(bio) : (bioBtn ? T(bioBtn) : '');
		const cat = infoSec.querySelector('[data-category], div[class*="category"]');
		if (cat) out.category = T(cat);
		const wb = infoSec.querySelector('button span[dir="auto"], a[target="_blank"]');
		if (!out.website && wb) out.website = T(wb);
	}
	return out;
}`

var (
	listKindRe  = regexp.MustCompile(`(?i)^(followers?|following)(_fetch)?$`)
	followRe    = regexp.MustCompile(`(?i)^follow( back)?$`)
	followingRe = regexp.MustCompile(`(?i)^(following|requested)$`)
	unfollowRe  = regexp.MustCompile(`(?i)^unfollow$`)
)

var ogCountsRe = regexp.MustCompile(`(?i)([\d.,]+\s*[kmb]?)\s+followers?,\s*([\d.,]+\s*[kmb]?)\s+following,\s*([\d.,]+\s*[kmb]?)\s+posts?`)
var ogTitleRe = regexp.MustCompile(`^(.*?)\s*\(@([A-Za-z0-9._]+)\)`)

// profileFromDOM parses the rendered profile page for username.
func (b *InstagramBot) profileFromDOM(p browser.PageInterface, username string) (map[string]interface{}, error) {
	var d domProfile
	if err := evalJS(p, profileScript, &d); err != nil {
		return nil, err
	}
	if d.Unavailable {
		return nil, stopErr{fmt.Errorf("instagram: profile %q is not available", username)}
	}
	if d.State != "ok" && d.OGDesc == "" {
		return nil, fmt.Errorf("profile header not rendered (%s)", d.State)
	}
	if username == "" {
		username = d.Username
	}
	res := map[string]interface{}{
		"platform":     "INSTAGRAM",
		"username":     username,
		"url":          profileURL(username),
		"full_name":    d.FullName,
		"introduction": d.Bio,
		"is_verified":  d.Verified,
		"is_private":   d.Private,
		"image_url":    d.Image,
		"website":      unwrapLinkShim(d.Website),
	}
	if d.Category != "" {
		res["category"] = d.Category
	}
	followers, following, posts := d.Followers, d.Following, d.Posts
	if m := ogCountsRe.FindStringSubmatch(d.OGDesc); m != nil {
		followers, following, posts = firstNonEmpty(followers, m[1]), firstNonEmpty(following, m[2]), firstNonEmpty(posts, m[3])
	}
	if m := ogTitleRe.FindStringSubmatch(d.OGTitle); m != nil && d.FullName == "" {
		res["full_name"] = strings.TrimSpace(m[1])
	}
	for _, s := range d.LDJSON {
		var ld map[string]interface{}
		if jsonUnmarshal([]byte(s), &ld) != nil {
			continue
		}
		if name := getString(ld, "name"); name != "" && res["full_name"] == "" {
			res["full_name"] = name
		}
		if desc := getString(ld, "description"); desc != "" && res["introduction"] == "" {
			res["introduction"] = desc
		}
	}
	if c := parseCount(followers); c != "" {
		res["follower_count"] = c
	}
	if c := parseCount(following); c != "" {
		res["following_count"] = c
	}
	if c := parseCount(posts); c != "" {
		res["content_count"] = c
	}
	return res, nil
}

// unwrapLinkShim turns Instagram's l.instagram.com/?u=<url> redirect into
// the destination URL.
func unwrapLinkShim(link string) string {
	u, err := url.Parse(link)
	if err != nil || !strings.HasSuffix(u.Hostname(), "l.instagram.com") {
		return link
	}
	if dest := u.Query().Get("u"); dest != "" {
		return dest
	}
	return link
}

// parseCount normalises "1,234" → "1234" and "12.5K" / "686m" → an integer
// string; anything unparseable is returned trimmed.
func parseCount(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSpace(strings.TrimSuffix(s, "k"))
	case strings.HasSuffix(s, "m"):
		mult, s = 1e6, strings.TrimSpace(strings.TrimSuffix(s, "m"))
	case strings.HasSuffix(s, "b"):
		mult, s = 1e9, strings.TrimSpace(strings.TrimSuffix(s, "b"))
	}
	if mult == 1 {
		digits := strings.NewReplacer(",", "", ".", "", " ", "").Replace(s)
		if _, err := strconv.ParseInt(digits, 10, 64); err == nil {
			return digits
		}
		return s
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
	if err != nil {
		return s
	}
	return strconv.FormatInt(int64(f*mult+0.5), 10)
}

// ExtractUsernameFromMetadata returns the username of the profile — or the
// author of the post — shown on the current page, from its JSON-LD, Open
// Graph, canonical link or post header.
func (b *InstagramBot) ExtractUsernameFromMetadata(ctx context.Context, p browser.PageInterface) (string, error) {
	var u string
	err := poll(ctx, 3*time.Second, func() (bool, error) {
		if err := evalJS(p, `() => {
			for (const s of document.querySelectorAll('script[type="application/ld+json"]')) {
				try {
					const ld = JSON.parse(s.textContent);
					const items = Array.isArray(ld) ? ld : [ld];
					for (const it of items) {
						const alt = it.alternateName || (it.author && it.author.alternateName) || '';
						if (typeof alt === 'string' && alt.startsWith('@')) return alt.slice(1);
					}
				} catch (e) {}
			}
			const urls = [];
			const og = document.querySelector('meta[property="og:url"]');
			if (og) urls.push(og.getAttribute('content') || '');
			const can = document.querySelector('link[rel="canonical"]');
			if (can) urls.push(can.getAttribute('href') || '');
			urls.push(location.href);
			for (const u of urls) {
				const ref = postRef(u);
				if (ref) { if (ref.author) return ref.author; continue; }
				const who = userOf(u);
				if (who) return who;
			}
			// A post page: the author link in the post header.
			for (const h of document.querySelectorAll('main header, article header')) {
				const a = Array.from(h.querySelectorAll('a[href]')).find((x) => userOf(x.getAttribute('href')));
				if (a) return userOf(a.getAttribute('href'));
			}
			const t = document.querySelector('meta[property="og:title"]');
			const m = t && (t.getAttribute('content') || '').match(/@([A-Za-z0-9._]{1,30})/);
			return m ? m[1] : '';
		}`, &u); err != nil {
			return false, err
		}
		return u != "", nil
	})
	if err != nil || u == "" {
		return "", fmt.Errorf("could not extract username from page metadata")
	}
	return u, nil
}

// ---------------------------------------------------------------------------
// Follow / unfollow (WRITE)
// ---------------------------------------------------------------------------

// openProfile resolves a username or profile URL and opens the profile.
func (b *InstagramBot) openProfile(ctx context.Context, p browser.PageInterface, target string) (string, error) {
	username := b.usernameFrom(target)
	if username == "" {
		return "", fmt.Errorf("instagram: could not determine a username from %q", target)
	}
	if err := b.open(ctx, p, profileURL(username)); err != nil {
		return "", err
	}
	return username, nil
}

// waitFollowState waits for the profile header to render its follow button
// and returns followState(): following | requested | not_following, or an
// error when the header never shows one.
func waitFollowState(ctx context.Context, p browser.PageInterface) (string, error) {
	var state string
	err := poll(ctx, findTimeout, func() (bool, error) {
		var unavailable bool
		if err := evalJS(p, `() => followState()`, &state); err != nil {
			return false, err
		}
		if err := evalJS(p, `() => /sorry, this page isn.t available/i.test(VT())`, &unavailable); err == nil && unavailable {
			return false, stopErr{errors.New("instagram: this profile is not available")}
		}
		return state == "following" || state == "requested" || state == "not_following", nil
	})
	return state, err
}

// FollowUser follows the profile's owner. A profile already followed (or
// with a pending request) is reported as such and left alone; the click is
// verified by the header switching to Following / Requested. Only the
// profile header's own Follow button is ever clicked — never one in
// "Suggested for you".
func (b *InstagramBot) FollowUser(ctx context.Context, p browser.PageInterface, target string) (map[string]interface{}, error) {
	username, err := b.openProfile(ctx, p, target)
	if err != nil {
		return nil, err
	}
	state, err := waitFollowState(ctx, p)
	m := newMark()
	switch {
	case err == nil && (state == "following" || state == "requested"):
		return ok("url", profileURL(username), "username", username, "status", "already_"+state), nil
	case err == nil:
		var marked bool
		if err := evalJS(p, `(m) => { const b = profileButton(/^follow( back)?$/i); if (b) mark(b, m); return !!b; }`, &marked, m); err != nil || !marked {
			return nil, fmt.Errorf("instagram: Follow button vanished on %s", username)
		}
	case !errors.Is(err, errTimeout):
		return nil, err
	default:
		// No recognisable button: Jev may find it; accept only a real
		// Follow button, outside any list (suggestions) or dialog.
		d, jerr := b.jevPick(ctx, p, "click",
			fmt.Sprintf("the Follow button in the profile header of @%s (not a Follow button under 'Suggested for you')", username),
			"its text is exactly 'Follow' or 'Follow Back'; 'Following' or 'Requested' means already followed", m)
		if jerr != nil {
			return nil, fmt.Errorf("instagram: no Follow button on %s's profile: %v (jev: %v)", username, err, jerr)
		}
		switch {
		case followingRe.MatchString(d.Text):
			return ok("url", profileURL(username), "username", username, "status", "already_"+strings.ToLower(d.Text)), nil
		case !followRe.MatchString(d.Text) || d.InList || d.InDialog:
			return nil, fmt.Errorf("instagram: no Follow button on %s's profile (jev picked %q)", username, d.Text)
		}
	}
	if err := clickMarked(p, m); err != nil {
		return nil, fmt.Errorf("instagram: click Follow: %w", err)
	}
	var after string
	err = poll(ctx, verifyTimeout, func() (bool, error) {
		if err := evalJS(p, `(m) => {
			const s = followState();
			if (s === 'following' || s === 'requested') return s;
			const el = document.querySelector('['+MARK+'="'+m+'"]');
			const t = el ? T(el).toLowerCase() : '';
			return t === 'following' || t === 'requested' ? t : s;
		}`, &after, m); err != nil {
			return false, err
		}
		return after == "following" || after == "requested", nil
	})
	if err != nil {
		return nil, fmt.Errorf("instagram: follow of %s not confirmed (header still shows %q)", username, after)
	}
	status := "followed"
	if after == "requested" {
		status = "requested"
	}
	return ok("url", profileURL(username), "username", username, "status", status), nil
}

// UnfollowUser unfollows the profile's owner (or withdraws a pending
// request): header Following/Requested → the dialog's Unfollow → verified by
// the header showing Follow again. A profile not followed is left alone.
func (b *InstagramBot) UnfollowUser(ctx context.Context, p browser.PageInterface, target string) (map[string]interface{}, error) {
	username, err := b.openProfile(ctx, p, target)
	if err != nil {
		return nil, err
	}
	state, err := waitFollowState(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("instagram: no follow button on %s's profile: %w", username, err)
	}
	if state == "not_following" {
		return ok("url", profileURL(username), "username", username, "status", "not_following"), nil
	}
	m := newMark()
	var marked bool
	if err := evalJS(p, `(m) => { const b = profileButton(/^(following|requested)$/i); if (b) mark(b, m); return !!b; }`, &marked, m); err != nil || !marked {
		return nil, fmt.Errorf("instagram: Following button vanished on %s", username)
	}
	if err := clickMarked(p, m); err != nil {
		return nil, fmt.Errorf("instagram: click Following: %w", err)
	}
	// The dialog lists Close friends / Favorites / Mute / Restrict / Unfollow.
	c := newMark()
	err = poll(ctx, findTimeout, func() (bool, error) {
		var found bool
		err := evalJS(p, `(m) => {
			for (const d of dialogs()) {
				const b = byText(d, /^unfollow$/i)[0];
				if (b) { mark(b, m); return true; }
			}
			return false;
		}`, &found, c)
		return found, err
	})
	if err != nil {
		d, jerr := b.jevPick(ctx, p, "click",
			fmt.Sprintf("the Unfollow option in the dialog that opened after clicking Following on @%s's profile", username), "", c)
		if jerr != nil || !unfollowRe.MatchString(d.Text) {
			return nil, fmt.Errorf("instagram: Unfollow confirmation not found for %s: %v (jev: %v)", username, err, jerr)
		}
	}
	if err := pause(ctx, uiPause/2); err != nil {
		return nil, err
	}
	if err := clickMarked(p, c); err != nil {
		return nil, fmt.Errorf("instagram: click Unfollow: %w", err)
	}
	var after string
	err = poll(ctx, verifyTimeout, func() (bool, error) {
		if err := evalJS(p, `() => followState()`, &after); err != nil {
			return false, err
		}
		return after == "not_following", nil
	})
	if err != nil {
		return nil, fmt.Errorf("instagram: unfollow of %s not confirmed (header shows %q)", username, after)
	}
	return ok("url", profileURL(username), "username", username, "status", "unfollowed"), nil
}

// ---------------------------------------------------------------------------
// Stories (WRITE: viewing adds the account to the story's viewer list)
// ---------------------------------------------------------------------------

// ViewStories opens the profile's active story from the avatar ring (never a
// highlight) and watches up to maxStories of them (0 = all, capped at 50),
// stopping when the viewer moves on to another account. A profile without an
// active story is reported with stories_viewed 0.
func (b *InstagramBot) ViewStories(ctx context.Context, p browser.PageInterface, target string, maxStories int) (map[string]interface{}, error) {
	username, err := b.openProfile(ctx, p, target)
	if err != nil {
		return nil, err
	}
	if maxStories <= 0 || maxStories > 50 {
		maxStories = 50
	}
	if _, err := waitProfileHeader(ctx, p); err != nil {
		return nil, fmt.Errorf("instagram: %s's profile did not render: %w", username, err)
	}
	m := newMark()
	var ring string
	if err := evalJS(p, `(m) => {
		const h = hdr();
		// The avatar is in the header's first section; highlights (also
		// canvas rings) link to /stories/highlights/ and are never used.
		const cands = Array.from(h.querySelectorAll('[aria-label="View profile story"], [aria-label$="profile story"]'));
		const first = h.querySelector('section');
		if (first) for (const c of first.querySelectorAll('canvas')) { const b = c.closest('[role="button"], a'); if (b) cands.push(b); }
		const ok = cands.find((b) => !b.closest('a[href*="/highlights/"]') && !/highlight/i.test(lbl(b)));
		if (!ok) return 'none';
		mark(ok, m);
		return 'marked';
	}`, &ring, m); err != nil {
		return nil, err
	}
	if ring != "marked" {
		return ok("url", profileURL(username), "username", username, "stories_viewed", 0, "status", "no_active_story"), nil
	}
	if err := clickMarked(p, m); err != nil {
		return nil, fmt.Errorf("instagram: open story: %w", err)
	}
	// Some sessions get a "View as <you>?" interstitial first.
	viewerOpen := func() (bool, error) {
		var st struct {
			Open  bool   `json:"open"`
			Owner string `json:"owner"`
		}
		if err := evalJS(p, `() => {
			for (const d of dialogs().concat([document.body])) {
				const v = byText(d, /^view story$/i)[0];
				if (v && /view as/i.test(T(d))) { v.click(); return { open: false }; }
			}
			const m = location.pathname.match(/^\/stories\/([^/]+)\//);
			return { open: !!m, owner: m ? m[1] : '' };
		}`, &st); err != nil {
			return false, err
		}
		return st.Open && strings.EqualFold(st.Owner, username), nil
	}
	if err := poll(ctx, findTimeout, viewerOpen); err != nil {
		return nil, fmt.Errorf("instagram: story viewer did not open for %s: %w", username, err)
	}
	viewed := 0
	for viewed < maxStories {
		viewed++
		if err := pause(ctx, storyDwell); err != nil {
			return nil, err
		}
		if viewed >= maxStories {
			break
		}
		var before string
		_ = evalJS(p, `() => location.pathname`, &before)
		n := newMark()
		var hasNext bool
		_ = evalJS(p, `(m) => { const b = document.querySelector('button[aria-label="Next"], [role="button"][aria-label="Next"]'); if (b) mark(b, m); return !!b; }`, &hasNext, n)
		if hasNext {
			_ = clickMarked(p, n)
		} else if err := pressArrowRight(p); err != nil {
			break
		}
		// Wait for the next story (path changes) or the viewer leaving.
		var still bool
		_ = poll(ctx, 3*time.Second, func() (bool, error) {
			var path string
			if err := evalJS(p, `() => location.pathname`, &path); err != nil {
				return false, err
			}
			return path != before, nil
		})
		if s, err := viewerOpen(); err == nil {
			still = s
		}
		if !still {
			break
		}
	}
	// Close the viewer if it is still on this account's stories.
	if s, _ := viewerOpen(); s {
		c := newMark()
		var hasClose bool
		_ = evalJS(p, `(m) => { const s = document.querySelector('svg[aria-label="Close"]'); const b = s && btnOf(s); if (b) mark(b, m); return !!b; }`, &hasClose, c)
		if hasClose {
			_ = clickMarked(p, c)
		}
	}
	return ok("url", profileURL(username), "username", username, "stories_viewed", viewed, "status", "viewed"), nil
}

// waitProfileHeader waits for the profile header.
func waitProfileHeader(ctx context.Context, p browser.PageInterface) (bool, error) {
	err := poll(ctx, findTimeout, func() (bool, error) {
		var has bool
		if err := evalJS(p, `() => !!hdr()`, &has); err != nil {
			return false, err
		}
		return has, nil
	})
	return err == nil, err
}

// ---------------------------------------------------------------------------
// Followers / following (READ)
// ---------------------------------------------------------------------------

// scrollSettle is how long list readers wait for lazy content after a scroll.
var scrollSettle = 1500 * time.Millisecond

// FetchFollowersList opens profile's followers (or following) dialog and
// reads up to maxCount accounts from it, scrolling the dialog for more.
// sourceType is "followers"/"FOLLOWERS_FETCH" or "following"/
// "FOLLOWING_FETCH"; when profile is empty, a URL or username in sourceType
// is taken as the profile (followers).
func (b *InstagramBot) FetchFollowersList(ctx context.Context, p browser.PageInterface, profile, sourceType string, maxCount int) ([]map[string]interface{}, error) {
	kind := "followers"
	st := strings.ToLower(strings.TrimSpace(sourceType))
	switch {
	case strings.Contains(st, "following"):
		kind = "following"
	case st == "" || strings.Contains(st, "follower"):
	default:
		if strings.TrimSpace(profile) == "" {
			profile = sourceType
		}
	}
	if listKindRe.MatchString(strings.TrimSpace(profile)) {
		profile = "" // a template fallback handed the list kind as the profile
	}
	if strings.TrimSpace(profile) == "" {
		return nil, errors.New("instagram: no profile given for the followers list (target_url, profileUrl, or a profile URL in sourceType)")
	}
	if maxCount <= 0 {
		maxCount = 100
	}
	username, err := b.openProfile(ctx, p, profile)
	if err != nil {
		return nil, err
	}
	if _, err := waitProfileHeader(ctx, p); err != nil {
		return nil, fmt.Errorf("instagram: %s's profile did not render: %w", username, err)
	}
	var private bool
	_ = evalJS(p, `() => /this account is private/i.test(VT())`, &private)
	if private {
		return nil, fmt.Errorf("instagram: %s is private — its %s list is not visible", username, kind)
	}
	dialogOpen := func() (bool, error) {
		var n int
		err := evalJS(p, `() => { const d = dialogs().find((x) => x.querySelector('a[href]')); return d ? d.querySelectorAll('a[href]').length : 0; }`, &n)
		return n > 0, err
	}
	m := newMark()
	var clicked bool
	if err := evalJS(p, `(kind, m) => {
		const h = hdr();
		const re = new RegExp('/' + kind + '/?$');
		let a = Array.from(h.querySelectorAll('a[href]')).find((x) => re.test(x.getAttribute('href') || ''));
		if (!a) {
			const li = Array.from(h.querySelectorAll('ul li')).find((x) => new RegExp('\\b' + kind + '\\b', 'i').test(T(x)));
			a = li && (li.querySelector('a, [role="button"], button') || null);
		}
		if (a) mark(a, m);
		return !!a;
	}`, &clicked, kind, m); err == nil && clicked {
		_ = clickMarked(p, m)
	}
	if !clicked || poll(ctx, findTimeout, dialogOpen) != nil {
		// The /<user>/followers/ route opens the same dialog.
		if err := b.open(ctx, p, profileURL(username)+kind+"/"); err != nil {
			return nil, err
		}
		if err := poll(ctx, findTimeout, dialogOpen); err != nil {
			return nil, fmt.Errorf("instagram: %s dialog did not open on %s's profile: %w", kind, username, err)
		}
	}
	var results []map[string]interface{}
	seen := map[string]bool{}
	prev, stale := 0, 0
	for round := 0; round < 200 && len(results) < maxCount; round++ {
		var batch []struct {
			Username string `json:"username"`
			FullName string `json:"full_name"`
			Image    string `json:"image"`
			Verified bool   `json:"verified"`
		}
		if err := evalJS(p, `(owner) => {
			const d = dialogs().find((x) => x.querySelector('a[href]'));
			if (!d) return [];
			const out = [], seen = new Set();
			for (const a of d.querySelectorAll('a[href]')) {
				const u = userOf(a.getAttribute('href'));
				if (!u || u.toLowerCase() === owner.toLowerCase() || seen.has(u)) continue;
				seen.add(u);
				// The row: the nearest ancestor holding a single account.
				let row = a;
				while (row.parentElement && row.parentElement !== d &&
					new Set(Array.from(row.parentElement.querySelectorAll('a[href]')).map((x) => userOf(x.getAttribute('href'))).filter(Boolean)).size <= 1) row = row.parentElement;
				const names = Array.from(row.querySelectorAll('span[dir="auto"], span')).map(T).filter((t) => t && t !== u && !/^(follow|following|remove|requested|·)$/i.test(t));
				const img = row.querySelector('img');
				out.push({ username: u, full_name: names.find((t) => !t.includes(u)) || '', image: img ? img.getAttribute('src') || '' : '',
					verified: !!row.querySelector('svg[aria-label="Verified"]') });
			}
			return out;
		}`, &batch, username); err != nil {
			return nil, fmt.Errorf("instagram: read %s dialog: %w", kind, err)
		}
		for _, u := range batch {
			if seen[u.Username] {
				continue
			}
			seen[u.Username] = true
			results = append(results, map[string]interface{}{
				"platform": "INSTAGRAM", "username": u.Username, "url": profileURL(u.Username),
				"profile_url": profileURL(u.Username), "full_name": u.FullName, "image_url": u.Image,
				"is_verified": u.Verified, "source": kind, "source_profile": username,
			})
		}
		if len(results) >= maxCount {
			break
		}
		prev, stale = trackStagnation(len(results), prev, stale)
		if stale >= 3 {
			break
		}
		var scrolled bool
		_ = evalJS(p, `() => {
			const d = dialogs().find((x) => x.querySelector('a[href]'));
			if (!d) return false;
			const els = [d].concat(Array.from(d.querySelectorAll('div')));
			const sc = els.filter((e) => e.scrollHeight > e.clientHeight + 4 && /(auto|scroll)/.test(getComputedStyle(e).overflowY));
			const el = sc.sort((a, b) => b.scrollHeight - a.scrollHeight)[0] || d;
			el.scrollTop = el.scrollHeight;
			el.dispatchEvent(new Event('scroll'));
			return true;
		}`, &scrolled)
		if err := pause(ctx, scrollSettle); err != nil {
			return results, err
		}
	}
	if len(results) > maxCount {
		results = results[:maxCount]
	}
	return results, nil
}

// trackStagnation updates the scroll-progress counters used to detect when a
// scrollable list has stopped loading new items. It returns the new prevCount
// and noChangeRounds to carry into the next iteration.
func trackStagnation(currentCount, prevCount, noChangeRounds int) (newPrevCount, newNoChangeRounds int) {
	if currentCount == prevCount {
		return prevCount, noChangeRounds + 1
	}
	return currentCount, 0
}

// ---------------------------------------------------------------------------
// Post grids: a profile's posts, search results (READ)
// ---------------------------------------------------------------------------

type postLink struct {
	URL       string `json:"url"`
	Shortcode string `json:"shortcode"`
	Kind      string `json:"kind"`
	Author    string `json:"author"`
	Thumbnail string `json:"thumbnail_src"`
	Alt       string `json:"alt_text"`
}

// collectPostLinks reads post/reel links from the page's main content
// (never the header's highlights), scrolling for more until maxCount or the
// grid stops growing.
func collectPostLinks(ctx context.Context, p browser.PageInterface, maxCount int, defaultAuthor string) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	seen := map[string]bool{}
	prev, stale := 0, 0
	for round := 0; round < 100 && len(out) < maxCount; round++ {
		var links []postLink
		if err := evalJS(p, `() => {
			const root = document.querySelector('main') || document.body;
			const out = [];
			for (const a of root.querySelectorAll('a[href]')) {
				if (a.closest('header') || a.closest('[role="dialog"]') || !vis(a)) continue;
				const ref = postRef(a.getAttribute('href'));
				if (!ref) continue;
				const img = a.querySelector('img');
				out.push({ url: ref.url, shortcode: ref.shortcode, kind: ref.kind, author: ref.author,
					thumbnail_src: img ? img.getAttribute('src') || '' : '', alt_text: img ? img.getAttribute('alt') || '' : '' });
			}
			return out;
		}`, &links); err != nil {
			return out, err
		}
		for _, l := range links {
			if seen[l.Shortcode] || len(out) >= maxCount {
				continue
			}
			seen[l.Shortcode] = true
			author := firstNonEmpty(l.Author, defaultAuthor)
			row := map[string]interface{}{
				"platform": "INSTAGRAM", "url": l.URL, "shortcode": l.Shortcode, "type": l.Kind,
				"thumbnail_src": l.Thumbnail, "alt_text": l.Alt,
			}
			if author != "" {
				row["author"] = author
				row["author_url"] = profileURL(author)
			}
			out = append(out, row)
		}
		if len(out) >= maxCount {
			break
		}
		prev, stale = trackStagnation(len(out), prev, stale)
		if stale >= 3 {
			break
		}
		var ok bool
		_ = evalJS(p, `() => { window.scrollBy(0, Math.max(800, innerHeight)); window.dispatchEvent(new Event('scroll')); return true; }`, &ok)
		if err := pause(ctx, scrollSettle); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ListUserPosts returns up to maxCount posts from a profile's grid, each with
// url, shortcode, type, author, thumbnail_src and alt_text.
func (b *InstagramBot) ListUserPosts(ctx context.Context, p browser.PageInterface, target string, maxCount int) ([]map[string]interface{}, error) {
	if maxCount <= 0 {
		maxCount = 20
	}
	username, err := b.openProfile(ctx, p, target)
	if err != nil {
		return nil, err
	}
	if _, err := waitProfileHeader(ctx, p); err != nil {
		return nil, fmt.Errorf("instagram: %s's profile did not render: %w", username, err)
	}
	var st struct {
		Private bool `json:"private"`
		Posts   int  `json:"posts"`
	}
	_ = poll(ctx, findTimeout, func() (bool, error) {
		err := evalJS(p, `() => ({ private: /this account is private/i.test(VT()),
			posts: Array.from((document.querySelector('main') || document.body).querySelectorAll('a[href]')).filter((a) => !a.closest('header') && vis(a) && postRef(a.getAttribute('href'))).length })`, &st)
		return st.Private || st.Posts > 0, err
	})
	if st.Private && st.Posts == 0 {
		return nil, fmt.Errorf("instagram: %s is private — its posts are not visible", username)
	}
	return collectPostLinks(ctx, p, maxCount, username)
}

var tagRe = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

// SearchPosts returns up to maxCount posts for a keyword: the hashtag page
// for a single-word keyword, else (or when that page is empty) Instagram's
// keyword search.
func (b *InstagramBot) SearchPosts(ctx context.Context, p browser.PageInterface, keyword string, maxCount int) ([]map[string]interface{}, error) {
	kw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(keyword), "#"))
	if kw == "" {
		return nil, errors.New("instagram: keyword is required")
	}
	if maxCount <= 0 {
		maxCount = 20
	}
	var pages []string
	if tagRe.MatchString(kw) {
		pages = append(pages, b.SearchURL(kw))
	}
	pages = append(pages, "https://www.instagram.com/explore/search/keyword/?q="+url.QueryEscape(keyword))
	var lastErr error
	for _, u := range pages {
		if err := b.open(ctx, p, u); err != nil {
			if errors.Is(err, ErrNotLoggedIn) {
				return nil, err
			}
			lastErr = err
			continue
		}
		_ = poll(ctx, findTimeout, func() (bool, error) {
			var n int
			err := evalJS(p, `() => Array.from(document.querySelectorAll('main a[href], a[href]')).filter((a) => postRef(a.getAttribute('href'))).length`, &n)
			return n > 0, err
		})
		posts, err := collectPostLinks(ctx, p, maxCount, "")
		if err != nil {
			lastErr = err
			continue
		}
		if len(posts) > 0 {
			for _, row := range posts {
				row["keyword"] = keyword
			}
			return posts, nil
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("instagram: search %q: %w", keyword, lastErr)
	}
	return []map[string]interface{}{}, nil
}

// FindPostAuthors searches keyword and returns the profiles of up to
// maxCount distinct post authors (each with the post it was found through).
// A post whose author cannot be read is skipped; a profile that cannot be
// read is returned with just its username, url and profile_error.
func (b *InstagramBot) FindPostAuthors(ctx context.Context, p browser.PageInterface, keyword string, maxCount int) ([]map[string]interface{}, error) {
	if maxCount <= 0 {
		maxCount = 20
	}
	posts, err := b.SearchPosts(ctx, p, keyword, min(maxCount*3, 150))
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	seen := map[string]bool{}
	for _, post := range posts {
		if len(out) >= maxCount {
			break
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		postURL := getString(post, "url")
		author := getString(post, "author")
		if author == "" {
			data, err := b.ScrapePostData(ctx, p, postURL, 0)
			if err != nil {
				continue
			}
			author = getString(data, "author_username")
		}
		if author == "" || seen[strings.ToLower(author)] {
			continue
		}
		seen[strings.ToLower(author)] = true
		prof, err := b.GetUserInfo(ctx, p, author)
		if err != nil {
			if errors.Is(err, ErrNotLoggedIn) {
				return out, err
			}
			prof = map[string]interface{}{"platform": "INSTAGRAM", "username": author, "url": profileURL(author), "profile_error": err.Error()}
		}
		prof["source_post"] = postURL
		prof["keyword"] = keyword
		out = append(out, prof)
	}
	return out, nil
}
