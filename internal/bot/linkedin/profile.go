//go:build !nosocial

package linkedin

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/monoes/mono-agent/internal/browser"
)

// profileJS reads the member profile rendered on the page, in either the
// classic markup (h1.text-heading-xlarge …) or the server-driven one (the
// top card [componentkey*="Topcard"] with an h2 name and <p> lines).
const profileJS = `() => {
	const main = document.querySelector('main') || document.body;
	const card = main.querySelector('[componentkey*="Topcard"], [componentkey*="topcard"], section.pv-top-card, .pv-top-card, .ph5') || main.querySelector('section') || main;
	const nameEl = card.querySelector('h1.text-heading-xlarge, h1, h2');
	const name = nameEl ? L.text(nameEl) : '';
	// Lines that are never the headline: the connection degree ("· 3rd",
	// "2nd degree connection"), any "·"-led fragment, pronouns, counts.
	const noise = (t) => !t || t === name || /^[·•]/.test(t) ||
		/^(?:1st|2nd|3rd\+?)(?:\s+degree connection)?$/i.test(t) ||
		/^(?:he|she|they|ze)\s*\/\s*\w+$/i.test(t) ||
		/connections?$|followers?$|^contact info$/i.test(t);
	let headline = '', place = '', about = '';
	const h = card.querySelector('.text-body-medium.break-words, [data-generated-suggestion-target]');
	if (h) headline = L.text(h);
	// Server-driven card: the headline is the first real line after the
	// name block, found by walking the siblings that follow it outward.
	if (!headline && nameEl) {
		for (let blk = nameEl; blk && blk !== card && !headline; blk = blk.parentElement) {
			for (let sib = blk.nextElementSibling; sib && !headline; sib = sib.nextElementSibling) {
				if (sib.matches('button, [role="button"]')) continue;
				const t = L.lines(sib).find((x) => !noise(x));
				if (t) headline = t;
			}
		}
	}
	const loc = card.querySelector('span.text-body-small.inline.t-black--light.break-words, .pv-text-details__left-panel span.text-body-small');
	if (loc) place = L.text(loc);
	const contact = card.querySelector('a[href*="/overlay/contact-info"], a#top-card-text-details-contact-info');
	if (!place && contact) {
		const holder = contact.closest('p, span') || contact;
		const row = holder.parentElement;
		if (row) {
			const first = Array.from(row.querySelectorAll('p, span')).map((x) => L.text(x)).find((t) => t && t !== '·' && !/contact info/i.test(t));
			if (first) place = first;
		}
	}
	if (headline === place) headline = '';
	if (!headline) {
		const ps = Array.from(card.querySelectorAll('p')).map((p) => L.text(p)).filter((t) => !noise(t) && t !== place);
		if (ps.length) headline = ps[0];
	}
	const all = L.text(card);
	const conn = all.match(/([\d,.]+\+?)\s+connections?/i);
	const fol = L.text(main).match(/([\d,.]+[KkMm]?\+?)\s+followers?/i);
	const deg = all.match(/(?:·|•)\s*(1st|2nd|3rd\+?)/);
	// The profile photo: selectors in priority order, one lookup each (a
	// comma list would return the first match in document order — the
	// banner), never the background image.
	let picture = '';
	for (const sel of ['img.pv-top-card-profile-picture__image', 'img.pv-top-card-profile-picture__image--show', '[aria-label="Profile photo"] img', '.pv-top-card__photo-wrapper img', 'figure img']) {
		for (const img of card.querySelectorAll(sel)) {
			const u = img.currentSrc || img.src || '';
			if (u && !/displaybackgroundimage/i.test(u)) { picture = u; break; }
		}
		if (picture) break;
	}
	// The cover (banner) image, when the member set one.
	let banner = '';
	for (const img of main.querySelectorAll('img')) {
		const u = img.currentSrc || img.src || '';
		if (/displaybackgroundimage/i.test(u)) { banner = u; break; }
	}
	// About: the section holding the #about anchor, or the one headed
	// "About" (a <section> or a server-driven card keyed by componentkey).
	let aboutSec = null;
	const aboutAnchor = main.querySelector('#about');
	if (aboutAnchor) aboutSec = aboutAnchor.closest('section') || aboutAnchor.parentElement;
	if (!aboutSec) {
		const hd = Array.from(main.querySelectorAll('h2, h3')).find((x) => /^About$/i.test(L.lines(x)[0] || L.text(x)));
		if (hd) aboutSec = hd.closest('section, [componentkey]') || hd.parentElement;
	}
	if (aboutSec) {
		const box = aboutSec.querySelector('.inline-show-more-text span[aria-hidden="true"], .inline-show-more-text, [data-testid="expandable-text-box"]');
		about = box ? L.text(box) : L.text(aboutSec).replace(/^About\s*/i, '');
	}
	return {
		name, headline, location: place, about, aboutSection: !!aboutSec,
		connections: conn ? conn[1] : '',
		followers: fol ? fol[1] : '',
		degree: deg ? deg[1] : '',
		picture, banner,
		isSelf: !!card.querySelector('a[href*="/edit/intro/"], a[href*="/edit/forms/intro"]'),
		url: window.location.href,
	};
}`

type rawProfile struct {
	Name        string `json:"name"`
	Headline    string `json:"headline"`
	Location    string `json:"location"`
	About       string `json:"about"`
	AboutSec    bool   `json:"aboutSection"`
	Connections string `json:"connections"`
	Followers   string `json:"followers"`
	Degree      string `json:"degree"`
	Picture     string `json:"picture"`
	Banner      string `json:"banner"`
	IsSelf      bool   `json:"isSelf"`
	URL         string `json:"url"`
}

// GetProfileData reads the profile currently loaded in page. It implements
// botpkg.BotAdapter; use GetProfile to navigate first.
func (b *LinkedInBot) GetProfileData(ctx context.Context, p browser.PageInterface) (map[string]interface{}, error) {
	var raw rawProfile
	err := poll(ctx, findTimeout, func() (bool, error) {
		if err := run(p, profileJS, &raw); err != nil {
			return false, nil
		}
		return raw.Name != "", nil
	})
	if err != nil {
		return nil, fmt.Errorf("linkedin: no profile name rendered on the page: %w", err)
	}
	if raw.About == "" {
		b.loadAbout(ctx, p, &raw)
	}
	pageURL := raw.URL
	if pageURL == "" {
		pageURL, _ = p.GetURL()
	}
	clean := pageURL
	if u, err := url.Parse(pageURL); err == nil {
		u.RawQuery, u.Fragment = "", ""
		clean = u.String()
	}
	m := map[string]interface{}{
		"username":            b.ExtractUsername(clean),
		"profile_url":         clean,
		"full_name":           raw.Name,
		"headline":            raw.Headline,
		"location":            raw.Location,
		"about":               raw.About,
		"connection_count":    raw.Connections,
		"follower_count":      raw.Followers,
		"connection_degree":   raw.Degree,
		"profile_picture_url": raw.Picture,
		"cover_image_url":     raw.Banner,
		"is_self":             raw.IsSelf,
	}
	// Experience and education as far as the profile page itself shows
	// them; GetProfile replaces them with the full /details/ lists.
	var exp []Position
	var edu []School
	if sec, err := readSection(p, "experience"); err == nil && sec.Found {
		exp = parseExperience(sec.Entries)
	}
	if sec, err := readSection(p, "education"); err == nil && sec.Found {
		edu = parseEducation(sec.Entries)
	}
	setDetails(m, exp, edu)
	return m, nil
}

// aboutScrolls bounds how often loadAbout scrolls looking for the About
// section.
const aboutScrolls = 4

// scrollAboutJS brings the About section into view when it is on the page,
// else scrolls a screen further down (LinkedIn renders the section only
// once it nears the viewport).
const scrollAboutJS = `() => {
	const main = document.querySelector('main') || document.body;
	const a = main.querySelector('#about');
	const hd = Array.from(main.querySelectorAll('h2, h3')).find((x) => /^About$/i.test(L.lines(x)[0] || L.text(x)));
	const el = (a && (a.closest('section') || a.parentElement)) || (hd && (hd.closest('section, [componentkey]') || hd.parentElement));
	if (el) el.scrollIntoView({ block: 'center' });
	else window.scrollBy(0, window.innerHeight);
	return !!el;
}`

// loadAbout scrolls the About section in (or down to where it renders) and
// reads the profile again, a few times, until the About text shows.
func (b *LinkedInBot) loadAbout(ctx context.Context, p browser.PageInterface, raw *rawProfile) {
	for i := 0; i < aboutScrolls && raw.About == ""; i++ {
		var found bool
		if err := run(p, scrollAboutJS, &found); err != nil {
			return
		}
		if sleepCtx(ctx, scrollSettle) != nil {
			return
		}
		var again rawProfile
		if err := run(p, profileJS, &again); err == nil && again.Name != "" {
			raw.About, raw.AboutSec = again.About, again.AboutSec
		}
		if found && raw.About == "" && i > 0 {
			return // the section is there and empty
		}
	}
}

// GetProfile navigates to a profile (URL or slug) and reads it, including
// the full Experience and Education lists from the profile's /details/
// pages (the profile page itself shows only the first few entries).
func (b *LinkedInBot) GetProfile(ctx context.Context, page browser.PageInterface, target string) (map[string]interface{}, error) {
	return b.getProfile(ctx, page, target, true)
}

// profileTargetURL turns a profile target — a URL, a site-relative path, or
// a bare member slug such as "jane-doe" or "@jane-doe" — into a URL.
func profileTargetURL(target string) string {
	target = strings.TrimSpace(target)
	if target == "" || strings.Contains(target, "linkedin.com/") || strings.HasPrefix(target, "/") {
		return target
	}
	return "https://www.linkedin.com/in/" + url.PathEscape(strings.TrimPrefix(strings.Trim(target, "/"), "@")) + "/"
}

func (b *LinkedInBot) getProfile(ctx context.Context, page browser.PageInterface, target string, details bool) (map[string]interface{}, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("linkedin: profile URL is required")
	}
	u := profileTargetURL(target)
	if err := navigate(ctx, page, b.ResolveURL(u)); err != nil {
		return nil, err
	}
	m, err := b.GetProfileData(ctx, page)
	if err != nil || !details {
		return m, err
	}
	profileURL, _ := m["profile_url"].(string)
	if b.ExtractUsername(profileURL) == "" {
		return m, nil
	}
	exp, _ := m["experience"].([]interface{})
	edu, _ := m["education"].([]interface{})
	var positions []Position
	var schools []School
	if entries, ok := b.readDetails(ctx, page, profileURL, "experience"); ok {
		positions = parseExperience(entries)
	} else {
		positions = fromJSONList[Position](exp)
	}
	if entries, ok := b.readDetails(ctx, page, profileURL, "education"); ok {
		schools = parseEducation(entries)
	} else {
		schools = fromJSONList[School](edu)
	}
	setDetails(m, positions, schools)
	return m, nil
}

// peopleCardsJS reads person cards from a search-results or network list
// page: {url, name, headline, location, degree, followsBack}.
const peopleCardsJS = `() => {
	const cards = [];
	const push = (el) => { if (el && !cards.includes(el)) cards.push(el); };
	document.querySelectorAll('[data-view-name="search-entity-result-universal-template"], li.reusable-search__result-container, [role="list"] [role="listitem"], main ul[role="list"] > li').forEach(push);
	const out = [];
	const seen = new Set();
	for (const c of cards) {
		if (cards.some((o) => o !== c && o.contains(c))) continue;
		const links = Array.from(c.querySelectorAll('a[href*="/in/"]'));
		if (!links.length) continue;
		const href = links[0].href.split('?')[0];
		if (seen.has(href)) continue;
		seen.add(href);
		// The name link: a link to the same profile that wraps no other
		// profile link (in the server-driven markup the whole card is one
		// link wrapping the name link), else the first visible one.
		const same = (a) => a.href.split('?')[0] === href;
		const named = (a) => a.getAttribute('aria-hidden') !== 'true' && L.text(a);
		const nameLink = links.find((a) => same(a) && named(a) && !a.querySelector('a[href*="/in/"]')) || links.find(named) || links[0];
		let name = '';
		const hidden = nameLink.querySelector('span[aria-hidden="true"]');
		name = L.lines(hidden || nameLink)[0] || '';
		if (!name) { const img = c.querySelector('img[alt]'); name = img ? img.getAttribute('alt') : ''; }
		name = L.bareName(name.replace(/^View\s+/, '').replace(/[’']s\s+profile$/, ''));
		// Screen-reader-only texts (presence "Status is online", "2nd degree
		// connection") render as lines of their own; they are not content.
		const a11y = L.a11yOnly(c);
		const lines = L.lines(c).filter((t) => t !== name && !a11y.has(t) && !/^Status is \w+$/i.test(t) && !/^(Premium|Verified|LinkedIn Member)$/i.test(t));
		const degLine = lines.find((t) => /^(?:•|·)?\s*(1st|2nd|3rd\+?)(\s+degree connection)?$/i.test(t));
		const rest = lines.filter((t) => t !== degLine && !/^(?:•|·)\s*(1st|2nd|3rd\+?)$/.test(t) && !/mutual connection|^Followed by|^(Connect|Follow|Following|Message|Pending)$/i.test(t) && !t.startsWith(name + ' '));
		const deg = (L.lines(c).join(' ').match(/(?:•|·)\s*(1st|2nd|3rd\+?)/) || [])[1] || '';
		const stop = Array.from(c.querySelectorAll('button')).find((x) => /^Click to stop following/i.test(x.getAttribute('aria-label') || ''));
		out.push({ url: href, name, headline: rest[0] || '', location: rest[1] || '', degree: deg || '', followsBack: !!stop });
	}
	return out;
}`

type rawPerson struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	Headline    string `json:"headline"`
	Location    string `json:"location"`
	Degree      string `json:"degree"`
	FollowsBack bool   `json:"followsBack"`
}

func (b *LinkedInBot) personItem(r rawPerson) map[string]interface{} {
	return map[string]interface{}{
		"url":               r.URL,
		"profile_url":       r.URL,
		"username":          b.ExtractUsername(r.URL),
		"full_name":         r.Name,
		"headline":          r.Headline,
		"job_title":         r.Headline,
		"location":          r.Location,
		"connection_degree": r.Degree,
		"platform":          "LINKEDIN",
	}
}

// SearchPeople runs a people search for keyword (or uses keyword as the
// search URL when it is one) and returns up to max people, following result
// pages.
func (b *LinkedInBot) SearchPeople(ctx context.Context, page browser.PageInterface, keyword string, max int) ([]map[string]interface{}, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("linkedin: keyword is required")
	}
	if max <= 0 {
		max = 10
	}
	base := keyword
	if strings.Contains(keyword, "linkedin.com/search/results/") {
		if !strings.Contains(keyword, "/search/results/people") {
			return nil, fmt.Errorf("linkedin: %q is not a people search URL", keyword)
		}
	} else {
		base = b.SearchURL(keyword) + "&origin=" + searchOrigin
	}
	out := []map[string]interface{}{}
	seen := map[string]bool{}
	for pageNo := 1; len(out) < max && pageNo <= max/5+2; pageNo++ {
		u := base
		if pageNo > 1 {
			pu, err := url.Parse(base)
			if err != nil {
				break
			}
			q := pu.Query()
			q.Set("page", fmt.Sprint(pageNo))
			pu.RawQuery = q.Encode()
			u = pu.String()
		}
		if err := navigate(ctx, page, u); err != nil {
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		var raw []rawPerson
		_ = poll(ctx, findTimeout, func() (bool, error) {
			if err := run(page, peopleCardsJS, &raw); err != nil {
				return false, nil
			}
			return len(raw) > 0, nil
		})
		added := 0
		for _, r := range raw {
			if r.URL == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			out = append(out, b.personItem(r))
			added++
			if len(out) >= max {
				break
			}
		}
		if added == 0 {
			break
		}
	}
	return out, nil
}

// listEmptyJS reports whether a network list shows LinkedIn's empty state
// ("You're not following anyone yet", "No followers yet", …).
const listEmptyJS = `() => {
	const main = document.querySelector('main') || document.body;
	if (main.querySelector('.artdeco-empty-state, [class*="empty-state"], [data-test-empty-state], [data-testid*="empty-state"]')) return true;
	return /(not following anyone|don[’']t have any followers|no followers yet|no one is following|you have no followers|no results found)/i.test(L.text(main));
}`

func listKind(target string) string {
	if strings.Contains(target, "/following/") {
		return "following"
	}
	return "followers"
}

// ListFollowers lists the signed-in member's followers (sourceType
// "followers"/FOLLOWERS_FETCH) or the people they follow
// ("following"/FOLLOWING_FETCH), up to max. LinkedIn only shows these
// lists for the viewer's own account. It never clicks Follow/Unfollow.
func (b *LinkedInBot) ListFollowers(ctx context.Context, page browser.PageInterface, sourceType string, max int) ([]map[string]interface{}, error) {
	if max <= 0 {
		max = 50
	}
	var target string
	switch st := strings.ToLower(strings.TrimSpace(sourceType)); {
	case st == "" || strings.Contains(st, "follower"):
		target = "https://www.linkedin.com/mynetwork/network-manager/people-follow/followers/"
	case strings.Contains(st, "following"):
		target = "https://www.linkedin.com/mynetwork/network-manager/people-follow/following/"
	default:
		return nil, fmt.Errorf("linkedin: unknown sourceType %q (want FOLLOWERS_FETCH or FOLLOWING_FETCH)", sourceType)
	}
	if err := navigate(ctx, page, target); err != nil {
		return nil, err
	}
	out := []map[string]interface{}{}
	// Wait for the list to render: person cards, or LinkedIn's empty state.
	// Neither within findTimeout is an error, never an empty success.
	var empty bool
	err := poll(ctx, findTimeout, func() (bool, error) {
		var raw []rawPerson
		if run(page, peopleCardsJS, &raw) == nil && len(raw) > 0 {
			return true, nil
		}
		if run(page, listEmptyJS, &empty) == nil && empty {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("linkedin: the %s list didn't load (no people and no empty-state message within %s)", listKind(target), findTimeout)
	}
	if empty {
		return out, nil
	}
	seen := map[string]bool{}
	stale := 0
	for len(out) < max && stale < 3 {
		var raw []rawPerson
		if err := run(page, peopleCardsJS, &raw); err != nil && len(out) == 0 && stale == 2 {
			return nil, fmt.Errorf("linkedin: reading the list: %w", err)
		}
		added := 0
		for _, r := range raw {
			if r.URL == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			it := b.personItem(r)
			it["you_follow"] = r.FollowsBack
			out = append(out, it)
			added++
			if len(out) >= max {
				break
			}
		}
		if added == 0 {
			stale++
		} else {
			stale = 0
		}
		if len(out) >= max {
			break
		}
		clickShowMore(page)
		scrollPage(page)
		if err := sleepCtx(ctx, scrollSettle); err != nil {
			return out, err
		}
	}
	return out, nil
}
