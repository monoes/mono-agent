//go:build social

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
	let headline = '', place = '', about = '';
	const h = card.querySelector('.text-body-medium.break-words, [data-generated-suggestion-target]');
	if (h) headline = L.text(h);
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
	const ps = Array.from(card.querySelectorAll('p')).map((p) => L.text(p)).filter((t) => t && t !== '·' && t !== name && !/connections?$|followers?$|^contact info$/i.test(t));
	if (!headline && ps.length) headline = ps[0];
	const all = L.text(card);
	const conn = all.match(/([\d,.]+\+?)\s+connections?/i);
	const fol = L.text(main).match(/([\d,.]+[KkMm]?\+?)\s+followers?/i);
	const deg = all.match(/(?:·|•)\s*(1st|2nd|3rd\+?)/);
	const img = card.querySelector('img.pv-top-card-profile-picture__image, img.pv-top-card-profile-picture__image--show, [aria-label="Profile photo"] img, .pv-top-card__photo-wrapper img, figure img');
	const aboutAnchor = main.querySelector('#about');
	if (aboutAnchor) {
		const sec = aboutAnchor.closest('section');
		const span = sec && sec.querySelector('.inline-show-more-text span[aria-hidden="true"], .inline-show-more-text, [data-testid="expandable-text-box"]');
		if (span) about = L.text(span);
	}
	if (!about) {
		const hd = Array.from(main.querySelectorAll('h2')).find((x) => /^About$/i.test(L.text(x)));
		const sec = hd && hd.closest('section');
		if (sec) {
			const box = sec.querySelector('[data-testid="expandable-text-box"], .inline-show-more-text');
			about = box ? L.text(box) : L.text(sec).replace(/^About\s*/i, '');
		}
	}
	return {
		name, headline, location: place, about,
		connections: conn ? conn[1] : '',
		followers: fol ? fol[1] : '',
		degree: deg ? deg[1] : '',
		picture: img ? (img.currentSrc || img.src || '') : '',
		isSelf: !!card.querySelector('a[href*="/edit/intro/"], a[href*="/edit/forms/intro"]'),
		url: window.location.href,
	};
}`

type rawProfile struct {
	Name        string `json:"name"`
	Headline    string `json:"headline"`
	Location    string `json:"location"`
	About       string `json:"about"`
	Connections string `json:"connections"`
	Followers   string `json:"followers"`
	Degree      string `json:"degree"`
	Picture     string `json:"picture"`
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
	pageURL := raw.URL
	if pageURL == "" {
		pageURL, _ = p.GetURL()
	}
	clean := pageURL
	if u, err := url.Parse(pageURL); err == nil {
		u.RawQuery, u.Fragment = "", ""
		clean = u.String()
	}
	return map[string]interface{}{
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
		"is_self":             raw.IsSelf,
	}, nil
}

// GetProfile navigates to a profile (URL or slug) and reads it.
func (b *LinkedInBot) GetProfile(ctx context.Context, page browser.PageInterface, target string) (map[string]interface{}, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("linkedin: profile URL is required")
	}
	u := target
	if !strings.Contains(target, "linkedin.com/") && !strings.HasPrefix(target, "/") {
		u = "https://www.linkedin.com/in/" + url.PathEscape(strings.Trim(target, "/")) + "/"
	}
	if err := navigate(ctx, page, b.ResolveURL(u)); err != nil {
		return nil, err
	}
	return b.GetProfileData(ctx, page)
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
		base = b.SearchURL(keyword) + "&origin=GLOBAL_SEARCH_HEADER"
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
