//go:build social

package x

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	const site = q("[data-testid='UserUrl']");
	out.website = txt(site);
	out.website_href = site ? (site.getAttribute('href') || '') : '';
	out.join_date = txt(q("[data-testid='UserJoinDate']"));
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
	return data, nil
}
