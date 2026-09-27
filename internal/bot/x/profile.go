//go:build !nosocial

package x

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// profileReadyJS reports whether a profile header (or an empty state such as
// "This account doesn't exist") has rendered. It knows the signed-in app's
// data-testids and the newer logged-out layout (an <h1> name and
// /following + /verified_followers links).
const profileReadyJS = `() => {
	const col = document.querySelector("[data-testid='primaryColumn']") || document;
	if (col.querySelector("[data-testid='UserName']")) return 'profile';
	if (col.querySelector("[data-testid='emptyState']")) return 'empty';
	const main = document.querySelector('main') || document;
	if (main.querySelector('h1') && main.querySelector("a[href$='/following']")) return 'profile';
	return '';
}`

// scrapeProfileJS reads the rendered profile header. Values are raw page
// text; counts are parsed in Go.
const scrapeProfileJS = `() => {
	const txt = (el) => el ? (el.innerText || el.textContent || '').trim() : '';
	const col = document.querySelector("[data-testid='primaryColumn']") || document.querySelector('main') || document;
	const q = (s) => col.querySelector(s);
	const out = {};
	const empty = q("[data-testid='emptyState']");
	const nameBox = q("[data-testid='UserName']");
	if (!nameBox && empty) { out.empty_state = txt(empty); return out; }
	const handleOf = (root) => {
		for (const el of root.querySelectorAll('span, div')) {
			if (el.children.length) continue;
			const t = (el.textContent || '').trim();
			if (/^@[A-Za-z0-9_]{1,15}$/.test(t)) return t;
		}
		return '';
	};
	if (nameBox) {
		out.handle = handleOf(nameBox);
		const lines = txt(nameBox).split('\n').map((s) => s.trim()).filter(Boolean);
		out.full_name = lines.find((l) => l !== out.handle && !l.startsWith('@')) || '';
		out.is_verified = !!nameBox.querySelector("[data-testid='icon-verified'], svg[aria-label*='erified']");
		out.is_protected = !!nameBox.querySelector("[data-testid='icon-lock'], svg[aria-label*='rotected']");
	} else {
		const h1 = q('h1');
		out.full_name = txt(h1);
		const box = (h1 && h1.parentElement && h1.parentElement.parentElement) || col;
		out.handle = handleOf(box) || handleOf(col);
		out.is_verified = !!(h1 && h1.parentElement && h1.parentElement.querySelector("[aria-label*='erified']"));
		out.is_protected = false;
	}
	out.bio = txt(q("[data-testid='UserDescription']"));
	out.location = txt(q("[data-testid='UserLocation']"));
	let site = q("[data-testid='UserUrl']");
	out.join_date = txt(q("[data-testid='UserJoinDate']"));
	if (!nameBox) {
		// Logged-out layout: no data-testids. The header is the smallest
		// ancestor of the <h1> that also holds the /following link; its bio is
		// a dir=auto div and its meta items are marked only by svg[data-icon].
		const h1 = q('h1');
		let header = null;
		for (let n = h1 && h1.parentElement; n && n !== document.body; n = n.parentElement) {
			if (n.querySelector("a[href$='/following']")) { header = n; break; }
		}
		if (header) {
			const bioEl = [...header.querySelectorAll("div[dir='auto']")].find((d) =>
				!d.contains(h1) && !d.closest('a, button, h1') && txt(d) && txt(d) !== out.handle);
			if (!out.bio) out.bio = txt(bioEl);
			const iconItem = (prefix) => {
				const svg = header.querySelector("svg[data-icon^='" + prefix + "']");
				return svg ? svg.parentElement : null;
			};
			if (!out.location) out.location = txt(iconItem('icon-location'));
			if (!site) {
				const item = iconItem('icon-link');
				site = item ? (item.querySelector('a[href]') || item) : null;
			}
			if (!out.join_date) {
				out.join_date = txt(iconItem('icon-calendar')) ||
					txt([...header.querySelectorAll("a[href$='/about'], span, div")].find((el) => !el.children.length && /^Joined\s/.test(txt(el))));
			}
		}
	}
	out.website = txt(site);
	out.website_href = site ? (site.getAttribute('href') || '') : '';
	const countText = (a) => {
		if (!a) return '';
		for (const el of a.querySelectorAll('span, div')) {
			if (el.children.length) continue;
			const t = (el.textContent || '').trim();
			if (/\d/.test(t)) return t;
		}
		return txt(a).split(/\s/)[0] || '';
	};
	const h = (out.handle || '').replace(/^@/, '').toLowerCase();
	const link = (suffixes) => {
		const links = [...col.querySelectorAll('a[href]')];
		for (const suf of suffixes) {
			const exact = links.find((a) => (a.getAttribute('href') || '').toLowerCase() === '/' + h + suf);
			if (exact) return exact;
		}
		for (const suf of suffixes) {
			const any = links.find((a) => (a.getAttribute('href') || '').toLowerCase().endsWith(suf));
			if (any) return any;
		}
		return null;
	};
	out.followers_text = countText(link(['/verified_followers', '/followers']));
	out.following_text = countText(link(['/following']));
	const avatar = q("a[href$='/photo'] img") || q("[data-testid^='UserAvatar-Container'] img");
	out.profile_picture_url = avatar ? (avatar.getAttribute('src') || '') : '';
	const banner = q("a[href$='/header_photo'] img");
	out.banner_url = banner ? (banner.getAttribute('src') || '') : '';
	out.can_dm = !!q("[data-testid='sendDMFromProfile']");
	out.professional_category = txt(q("[data-testid='UserProfessionalCategory']"));
	out.birth_date = txt(q("[data-testid='UserBirthdate']"));
	// Posts count: the column's top bar, under the name ("74.3K posts").
	for (const h of col.querySelectorAll('h2')) {
		const m = txt(h.parentElement).match(/([\d.,]+\s*[KMB]?)\s+(posts?|tweets?)\b/i);
		if (m) { out.posts_text = m[1]; break; }
	}
	// Badge kind: gold (a gradient) is a business, grey a government or
	// multilateral organisation, anything else a paid (blue) check.
	const badge = nameBox && nameBox.querySelector("[data-testid='icon-verified']");
	if (badge) {
		const html = badge.outerHTML;
		out.verification_type = /lineargradient|url\(#/i.test(html) ? 'business' : /#829aab/i.test(html) ? 'government' : 'blue';
	}
	// Pinned post: the timeline post marked Pinned.
	for (const art of col.querySelectorAll("article[data-testid='tweet']")) {
		const sc = art.querySelector("[data-testid='socialContext']");
		if (!sc || !/pinned|angeheftet|épinglé|fijado|fissato/i.test(txt(sc))) continue;
		const links = [...art.querySelectorAll("a[href*='/status/']")];
		const a = links.find((x) => x.querySelector('time')) || links[0];
		out.pinned_url = a ? new URL(a.getAttribute('href'), location.href).href.split('?')[0] : '';
		out.pinned_text = txt(art.querySelector("[data-testid='tweetText']"));
		break;
	}
	// The profile's own data, as the header's components hold it: exact
	// counts, the expanded website, dates, badge type, category.
	const fk = nameBox && Object.keys(nameBox).find((k) => k.startsWith('__reactFiber$'));
	for (let f = fk ? nameBox[fk] : null, i = 0; f && i < 40; f = f.return, i++) {
		const u = f.memoizedProps && f.memoizedProps.user;
		if (!u || typeof u !== 'object' || !u.screen_name) continue;
		const pick = {};
		for (const k of ['id_str', 'name', 'screen_name', 'description', 'location', 'entities', 'followers_count',
			'friends_count', 'statuses_count', 'verified', 'verified_type', 'is_blue_verified', 'protected',
			'profile_image_url_https', 'profile_banner_url', 'created_at', 'birthdate', 'professional',
			'pinned_tweet_ids_str', 'business_account']) if (u[k] !== undefined) pick[k] = u[k];
		try { out.user = JSON.parse(JSON.stringify(pick)); } catch (e) {}
		break;
	}
	return out;
}`

type rawProfile struct {
	EmptyState        string `json:"empty_state"`
	Handle            string `json:"handle"`
	FullName          string `json:"full_name"`
	IsVerified        bool   `json:"is_verified"`
	IsProtected       bool   `json:"is_protected"`
	Bio               string `json:"bio"`
	Location          string `json:"location"`
	Website           string `json:"website"`
	WebsiteHref       string `json:"website_href"`
	JoinDate          string `json:"join_date"`
	FollowersText     string `json:"followers_text"`
	FollowingText     string `json:"following_text"`
	ProfilePictureURL string `json:"profile_picture_url"`
	BannerURL         string `json:"banner_url"`
	CanDM             bool   `json:"can_dm"`
	ProfCategory      string `json:"professional_category"`
	BirthDate         string `json:"birth_date"`
	PostsText         string `json:"posts_text"`
	VerificationType  string `json:"verification_type"`
	PinnedURL         string `json:"pinned_url"`
	PinnedText        string `json:"pinned_text"`
	User              *xUser `json:"user"`
}

// xUser is the part of the user object X's profile header renders from.
type xUser struct {
	IDStr       string `json:"id_str"`
	Name        string `json:"name"`
	ScreenName  string `json:"screen_name"`
	Description string `json:"description"`
	Location    string `json:"location"`
	Entities    struct {
		URL struct {
			URLs []xURL `json:"urls"`
		} `json:"url"`
		Description struct {
			URLs []xURL `json:"urls"`
		} `json:"description"`
	} `json:"entities"`
	FollowersCount    *int64   `json:"followers_count"`
	FriendsCount      *int64   `json:"friends_count"`
	StatusesCount     *int64   `json:"statuses_count"`
	Verified          bool     `json:"verified"`
	VerifiedType      string   `json:"verified_type"`
	IsBlueVerified    bool     `json:"is_blue_verified"`
	Protected         bool     `json:"protected"`
	ProfileImageURL   string   `json:"profile_image_url_https"`
	ProfileBannerURL  string   `json:"profile_banner_url"`
	CreatedAt         string   `json:"created_at"`
	PinnedTweetIDsStr []string `json:"pinned_tweet_ids_str"`
	Birthdate         *struct {
		Day   int `json:"day"`
		Month int `json:"month"`
		Year  int `json:"year"`
	} `json:"birthdate"`
	Professional *struct {
		ProfessionalType string `json:"professional_type"`
		Category         []struct {
			Name string `json:"name"`
		} `json:"category"`
	} `json:"professional"`
	BusinessAccount *struct {
		AffiliatesCount *int64 `json:"affiliates_count"`
	} `json:"business_account"`
}

type xURL struct {
	URL         string `json:"url"`
	ExpandedURL string `json:"expanded_url"`
	DisplayURL  string `json:"display_url"`
}

// GetProfile opens a profile (URL or handle) and returns its header data.
func (b *XBot) GetProfile(ctx context.Context, p browser.PageInterface, target string) (map[string]interface{}, error) {
	u, _, err := b.profileURL(target)
	if err != nil {
		return nil, err
	}
	if err := navigate(p, u); err != nil {
		return nil, err
	}
	return b.scrapeProfile(ctx, p, u)
}

// scrapeProfile waits for the loaded profile header and reads it.
func (b *XBot) scrapeProfile(ctx context.Context, p browser.PageInterface, pageURL string) (map[string]interface{}, error) {
	var state string
	err := poll(ctx, loadTimeout, func() (bool, error) {
		if err := botpkg.EvalJSON(p, profileReadyJS, &state); err != nil {
			return false, nil // page may be mid-navigation
		}
		return state != "", nil
	})
	if err != nil {
		if lerr := checkLoginRedirect(p); lerr != nil {
			return nil, lerr
		}
		return nil, fmt.Errorf("x: profile %s did not render: %w", pageURL, err)
	}
	// The header renders in pieces (counts after the name); let it settle.
	if err := sleep(ctx, actionPause); err != nil {
		return nil, err
	}
	var r rawProfile
	if err := botpkg.EvalJSON(p, scrapeProfileJS, &r); err != nil {
		return nil, fmt.Errorf("x: read profile %s: %w", pageURL, err)
	}
	if r.EmptyState != "" {
		return nil, fmt.Errorf("x: profile %s is unavailable: %s", pageURL, truncateForError(r.EmptyState, 120))
	}
	username := strings.TrimPrefix(r.Handle, "@")
	if username == "" {
		username = b.ExtractUsername(pageURL)
	}
	if username == "" && r.FullName == "" {
		return nil, errors.New("x: profile header has no name or username")
	}
	data := map[string]interface{}{
		"profile_url":         "https://x.com/" + username,
		"username":            username,
		"full_name":           r.FullName,
		"bio":                 r.Bio,
		"location":            r.Location,
		"website":             r.Website,
		"join_date":           r.JoinDate,
		"is_verified":         r.IsVerified,
		"is_protected":        r.IsProtected,
		"profile_picture_url": r.ProfilePictureURL,
		"banner_url":          r.BannerURL,
		"can_dm":              r.CanDM,
		"followers_text":      r.FollowersText,
		"following_text":      r.FollowingText,
	}
	if r.WebsiteHref != "" {
		data["website_href"] = r.WebsiteHref
	}
	if n, ok := parseCount(r.FollowersText); ok {
		data["followers_count"] = n
	}
	if n, ok := parseCount(r.FollowingText); ok {
		data["following_count"] = n
	}
	if n, ok := parseCount(r.PostsText); ok {
		data["content_count"] = n
	}
	data["join_date"] = joinDate(r.JoinDate)
	data["birth_date"] = strings.TrimSpace(strings.TrimPrefix(r.BirthDate, "Born "))
	data["profile_category"] = r.ProfCategory
	if r.IsVerified {
		data["verification_type"] = r.VerificationType
	}
	if r.PinnedURL != "" {
		data["pinned_post"] = map[string]interface{}{"url": r.PinnedURL, "text": r.PinnedText}
	}
	if r.WebsiteHref != "" || r.Website != "" {
		data["links"] = []map[string]interface{}{{"url": firstNonEmptyStr(r.WebsiteHref, r.Website), "title": r.Website}}
	}
	if u := r.User; u != nil && strings.EqualFold(u.ScreenName, username) {
		u.apply(data)
		if pin, _ := data["pinned_post"].(map[string]interface{}); pin != nil && pin["text"] == nil && len(u.PinnedTweetIDsStr) > 0 {
			// The pinned post's text is in the timeline, which renders after
			// the header.
			var text string
			_ = poll(ctx, loadTimeout/2, func() (bool, error) {
				if err := botpkg.EvalJSON(p, pinnedTextJS, &text, u.PinnedTweetIDsStr[0]); err != nil {
					return false, nil
				}
				return text != "", nil
			})
			if text != "" {
				pin["text"] = text
			}
		}
	}
	return data, nil
}

// pinnedTextJS returns the text of the timeline post with the given id.
const pinnedTextJS = `(id) => {
	const a = document.querySelector("[data-testid='primaryColumn'] article a[href*='/status/" + id + "']");
	const t = a && a.closest('article') && a.closest('article').querySelector("[data-testid='tweetText']");
	return t ? (t.innerText || t.textContent || '').trim() : '';
}`

// apply overlays the header's user object onto the page read: it has exact
// counts, the website behind t.co, exact dates and the badge's kind.
func (u *xUser) apply(data map[string]interface{}) {
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			data[k] = v
		}
	}
	set("full_name", u.Name)
	set("location", u.Location)
	set("platform_id", u.IDStr)
	if data["bio"] == "" {
		bio := u.Description
		for _, l := range u.Entities.Description.URLs {
			if l.URL != "" && l.ExpandedURL != "" {
				bio = strings.ReplaceAll(bio, l.URL, l.ExpandedURL)
			}
		}
		set("bio", bio)
	}
	var links []map[string]interface{}
	for _, l := range append(append([]xURL{}, u.Entities.URL.URLs...), u.Entities.Description.URLs...) {
		if l.ExpandedURL != "" {
			links = append(links, map[string]interface{}{"url": l.ExpandedURL, "title": l.DisplayURL})
		}
	}
	if len(links) > 0 {
		data["links"] = links
	}
	if len(u.Entities.URL.URLs) > 0 {
		set("website", u.Entities.URL.URLs[0].ExpandedURL)
	}
	if u.FollowersCount != nil {
		data["followers_count"] = *u.FollowersCount
	}
	if u.FriendsCount != nil {
		data["following_count"] = *u.FriendsCount
	}
	if u.StatusesCount != nil {
		data["content_count"] = *u.StatusesCount
	}
	if t, err := time.Parse(time.RFC3339, u.CreatedAt); err == nil {
		data["join_date"] = t.UTC().Format("2006-01-02")
	} else if t, err := time.Parse(time.RubyDate, u.CreatedAt); err == nil {
		data["join_date"] = t.UTC().Format("2006-01-02")
	}
	if b := u.Birthdate; b != nil && b.Month >= 1 && b.Month <= 12 && b.Day > 0 {
		d := fmt.Sprintf("%s %d", time.Month(b.Month), b.Day)
		if b.Year > 0 {
			d += fmt.Sprintf(", %d", b.Year)
		}
		data["birth_date"] = d
	}
	if p := u.Professional; p != nil {
		if len(p.Category) > 0 {
			set("profile_category", p.Category[0].Name)
		}
		set("account_type", strings.ToLower(p.ProfessionalType))
	}
	switch {
	case u.VerifiedType != "":
		data["verification_type"] = strings.ToLower(u.VerifiedType)
	case u.IsBlueVerified:
		data["verification_type"] = "blue"
	case u.Verified:
		data["verification_type"] = "verified"
	}
	if data["verification_type"] != nil && data["verification_type"] != "" {
		data["is_verified"] = true
	}
	data["is_protected"] = u.Protected || data["is_protected"] == true
	if img := u.ProfileImageURL; img != "" {
		data["profile_picture_url"] = largeAvatar(img)
	}
	if u.ProfileBannerURL != "" {
		data["banner_url"] = u.ProfileBannerURL + "/1500x500"
	}
	if _, ok := data["pinned_post"]; !ok && len(u.PinnedTweetIDsStr) > 0 {
		data["pinned_post"] = map[string]interface{}{"url": "https://x.com/" + u.ScreenName + "/status/" + u.PinnedTweetIDsStr[0]}
	}
	if b := u.BusinessAccount; b != nil && b.AffiliatesCount != nil && *b.AffiliatesCount > 0 {
		data["affiliates_count"] = *b.AffiliatesCount
	}
}

// avatarSizeRe matches the size suffix of an X avatar URL (_normal.jpg,
// _bigger.png, _200x200.jpg).
var avatarSizeRe = regexp.MustCompile(`_(normal|bigger|mini|\d+x\d+)(\.[a-z]+)$`)

// largeAvatar returns the 400x400 version of an X avatar URL.
func largeAvatar(u string) string {
	return avatarSizeRe.ReplaceAllString(u, "_400x400$2")
}

// joinDateRe reads the header's "Joined March 2011".
var joinDateRe = regexp.MustCompile(`(?i)([A-Z][a-z]+)\s+(\d{4})`)

// joinDate turns "Joined March 2011" into "2011-03"; text it can't read is
// returned as is.
func joinDate(s string) string {
	s = strings.TrimSpace(s)
	if m := joinDateRe.FindStringSubmatch(s); m != nil {
		if t, err := time.Parse("January 2006", m[1]+" "+m[2]); err == nil {
			return t.Format("2006-01")
		}
	}
	return s
}

func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
