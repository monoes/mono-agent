//go:build !nosocial

package linkedin

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// profileSectionJS reads the Experience or Education list (kind) of a
// profile page: the /details/experience/ or /details/education/ page, or
// the section on the profile itself. It reports each entry's text lines,
// its description and its company/school link; an entry that groups
// several roles at one company also reports each role's lines. Both the
// classic markup (section#experience / pvs-list items with aria-hidden
// spans) and the server-driven one (componentkey cards, <p> lines) are
// read the same way: entries are found by their company/school logo, lines
// by walking the rendered block layout (so hidden a11y duplicates, buttons,
// media attachments and descriptions stay out of them).
const profileSectionJS = `(kind) => {
	const main = document.querySelector('main') || document.body;
	const cap = kind === 'education' ? 'Education' : 'Experience';
	let root = main.querySelector('[componentkey*="' + cap + 'DetailsSection"], [data-testid^="profile_' + cap + 'DetailsSection"]')
		|| main.querySelector('[componentkey$="' + cap + 'TopLevelSection"]');
	if (!root) {
		const anchor = main.querySelector('#' + kind);
		if (anchor) root = anchor.closest('section') || anchor.parentElement;
	}
	if (!root) {
		const hd = Array.from(main.querySelectorAll('h1, h2')).find((x) => (L.lines(x)[0] || '') === cap);
		if (hd) root = hd.closest('section');
	}
	if (!root) return { found: false, entries: [] };

	const descSel = '[data-testid="expandable-text-box"], .inline-show-more-text, .pv-shared-text-with-see-more';
	const skipSel = '.visually-hidden, .a11y-text, button, figure, img, svg, [role="img"], a[href*="/overlay/"], ' + descSel;
	const lines = (el, skip) => {
		const out = [];
		let cur = '';
		const flush = () => {
			const t = cur.replace(/\s+/g, ' ').trim();
			if (t && out[out.length - 1] !== t) out.push(t);
			cur = '';
		};
		const walk = (n) => {
			if (n.nodeType === 3) { cur += n.nodeValue; return; }
			if (n.nodeType !== 1 || skip(n)) return;
			if (n.tagName === 'BR') { flush(); return; }
			const d = getComputedStyle(n).display;
			if (d === 'none') return;
			const block = d !== 'inline' && d !== 'contents';
			if (block) flush();
			for (const c of n.childNodes) walk(c);
			if (block) flush();
		};
		walk(el);
		flush();
		return out;
	};
	const descOf = (el, except) => {
		const box = Array.from(el.querySelectorAll(descSel)).find((b) => !except.some((x) => x.contains(b)));
		if (!box) return '';
		return lines(box, (n) => n.matches('.visually-hidden, .a11y-text, button')).join('\n');
	};
	const linkOf = (el, except) => {
		const a = Array.from(el.querySelectorAll('a[href*="/company/"], a[href*="/school/"]')).find((x) => !except.some((r) => r.contains(x)));
		return a ? a.href.split('?')[0] : '';
	};

	// Entries: the largest block around each company/school logo that
	// holds no other logo, nor the section's heading or its "Show all"
	// link. Without logos (classic lists), the outermost list items.
	const fences = Array.from(root.querySelectorAll('h1, h2, h3, p, span, a[href*="/details/"]')).filter((x) =>
		x.matches('a') ? !/\/edit\//.test(x.getAttribute('href') || '') : L.text(x) === cap);
	const holders = [];
	root.querySelectorAll('img[alt$="logo" i], svg[aria-label$="logo" i], [data-field$="_logo"]').forEach((x) => {
		const h = x.closest('[data-field$="_logo"]') || x.closest('a') || x.closest('figure') || x;
		if (!holders.includes(h)) holders.push(h);
	});
	let entries = [];
	for (const h of holders) {
		let e = h;
		const fenced = (el) => fences.some((f) => !e.contains(f) && el.contains(f));
		while (e.parentElement && e.parentElement !== root && holders.filter((o) => e.parentElement.contains(o)).length === 1 && !fenced(e.parentElement)) e = e.parentElement;
		if (!entries.includes(e)) entries.push(e);
	}
	if (!entries.length) {
		const lis = Array.from(root.querySelectorAll('li.artdeco-list__item, li.pvs-list__paged-list-item'));
		entries = lis.filter((li) => !lis.some((o) => o !== li && o.contains(li)));
	}

	const plain = (n) => n.matches(skipSel);
	const year = /\b(?:19|20)\d{2}\b/;
	const out = entries.map((e) => {
		// Roles grouped under one company: the outermost list items inside
		// the entry that carry a date line of their own.
		const dated = Array.from(e.querySelectorAll('li')).filter((li) => lines(li, plain).some((t) => year.test(t)));
		const roles = dated.filter((li) => !dated.some((o) => o !== li && o.contains(li)));
		return {
			lines: lines(e, (n) => plain(n) || roles.includes(n)),
			description: descOf(e, roles),
			url: linkOf(e, roles),
			roles: roles.map((r) => ({ lines: lines(r, plain), description: descOf(r, []) })),
		};
	}).filter((x) => x.lines.length);
	return { found: true, entries: out };
}`

type rawSection struct {
	Found   bool       `json:"found"`
	Entries []rawEntry `json:"entries"`
}

type rawEntry struct {
	Lines       []string  `json:"lines"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	Roles       []rawRole `json:"roles"`
}

type rawRole struct {
	Lines       []string `json:"lines"`
	Description string   `json:"description"`
}

// Position is one role in a member's Experience.
type Position struct {
	Title          string `json:"title"`
	Company        string `json:"company,omitempty"`
	CompanyURL     string `json:"company_url,omitempty"`
	EmploymentType string `json:"employment_type,omitempty"`
	DateRange      string `json:"date_range,omitempty"`
	Start          string `json:"start,omitempty"`
	End            string `json:"end,omitempty"`
	Duration       string `json:"duration,omitempty"`
	Location       string `json:"location,omitempty"`
	LocationType   string `json:"location_type,omitempty"`
	Description    string `json:"description,omitempty"`
	Skills         string `json:"skills,omitempty"`
}

// School is one entry in a member's Education.
type School struct {
	School       string `json:"school"`
	SchoolURL    string `json:"school_url,omitempty"`
	Degree       string `json:"degree,omitempty"`
	FieldOfStudy string `json:"field_of_study,omitempty"`
	DateRange    string `json:"date_range,omitempty"`
	Start        string `json:"start,omitempty"`
	End          string `json:"end,omitempty"`
	Grade        string `json:"grade,omitempty"`
	Activities   string `json:"activities,omitempty"`
	Description  string `json:"description,omitempty"`
	Skills       string `json:"skills,omitempty"`
}

var (
	// dateLineRe: "Jun 2025 - Present · 1 yr 4 mos", "2011 – 2013",
	// "Sep 1988 – Jun 1992", "2007 – Oct 2024", "2019".
	dateLineRe   = regexp.MustCompile(`^((?:[^\s\d·]+\.?\s+)?\d{4})(?:\s*[-–—]\s*((?:[^\s\d·]+\.?\s+)?\d{4}|[^\d·]+?))?(?:\s*·\s*(.+))?$`)
	durationRe   = regexp.MustCompile(`(?i)^(?:less than a year|(?:\d+\s+(?:yrs?|years?|mos?|months?)\s*){1,2})$`)
	employmentRe = regexp.MustCompile(`(?i)^(?:full-time|part-time|self-employed|freelance|contract|contractor|internship|apprenticeship|seasonal|permanent|temporary|volunteer|trainee)$`)
	workplaceRe  = regexp.MustCompile(`(?i)^(?:remote|on-site|onsite|hybrid)$`)
	skillsRe     = regexp.MustCompile(`(?i)^skills:\s*(.+)$|^(.+\s+and\s+\+\d+\s+skills?)$`)
	gradeRe      = regexp.MustCompile(`(?i)^grade:\s*(.+)$`)
	activitiesRe = regexp.MustCompile(`(?i)^activities and societies:\s*(.+)$`)
)

// dates splits a date line into its range, start, end and duration.
func dates(line string) (rng, start, end, dur string, ok bool) {
	m := dateLineRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", "", "", false
	}
	start, end, dur = strings.TrimSpace(m[1]), strings.TrimSpace(m[2]), strings.TrimSpace(m[3])
	rng = start
	if end != "" {
		rng = start + " - " + end
	}
	return rng, start, end, dur, true
}

func isDateLine(t string) bool { _, _, _, _, ok := dates(t); return ok }

func skillsOf(t string) string {
	m := skillsRe.FindStringSubmatch(t)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(m[2])
}

// looksLikeLocation: a short line of a few words (a description sentence
// is longer).
func looksLikeLocation(t string) bool {
	return len(t) <= 80 && len(strings.Fields(t)) <= 10
}

// splitDot splits "a · b · c" into its parts.
func splitDot(t string) []string {
	parts := strings.Split(t, "·")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func (p *Position) setLocation(t string) {
	parts := splitDot(t)
	if last := parts[len(parts)-1]; workplaceRe.MatchString(last) {
		p.LocationType = last
		parts = parts[:len(parts)-1]
	}
	p.Location = strings.Join(parts, " · ")
}

// companyLine reads the line under a title: "Company · Full-time",
// "Full-time" or "Company".
func (p *Position) companyLine(t string) {
	parts := splitDot(t)
	switch last := parts[len(parts)-1]; {
	case len(parts) > 1 && employmentRe.MatchString(last):
		p.Company = strings.Join(parts[:len(parts)-1], " · ")
		p.EmploymentType = last
	case employmentRe.MatchString(t):
		p.EmploymentType = t
	case p.Company == "":
		p.Company = t
	}
}

// parsePosition reads one role: title, then the company line, the date
// line, then location and skills; anything else is description when the
// page gave none apart.
func parsePosition(lines []string, desc string) Position {
	var p Position
	di := -1
	for i, t := range lines {
		if i > 0 && isDateLine(t) {
			di = i
			break
		}
	}
	head, rest := lines, []string(nil)
	if di >= 0 {
		head, rest = lines[:di], lines[di+1:]
		p.DateRange, p.Start, p.End, p.Duration, _ = dates(lines[di])
	} else if len(lines) > 2 {
		head, rest = lines[:2], lines[2:]
	}
	if len(head) > 0 {
		p.Title = head[0]
	}
	for _, t := range head[min(1, len(head)):] {
		p.companyLine(t)
	}
	var extra []string
	for _, t := range rest {
		switch {
		case skillsOf(t) != "":
			p.Skills = skillsOf(t)
		case p.Location == "" && p.LocationType == "" && len(extra) == 0 && looksLikeLocation(t):
			p.setLocation(t)
		default:
			extra = append(extra, t)
		}
	}
	p.Description = strings.TrimSpace(desc)
	if p.Description == "" && len(extra) > 0 {
		p.Description = strings.Join(extra, "\n")
	}
	return p
}

// parseExperience turns the entries of an Experience list into positions,
// one per role: an entry that groups roles at one company gives each role
// the company (and its employment type and location when the role has
// none).
func parseExperience(entries []rawEntry) []Position {
	out := []Position{}
	for _, e := range entries {
		if len(e.Lines) == 0 {
			continue
		}
		if len(e.Roles) == 0 {
			p := parsePosition(e.Lines, e.Description)
			p.CompanyURL = e.URL
			out = append(out, p)
			continue
		}
		company := e.Lines[0]
		var group Position
		for _, t := range e.Lines[1:] {
			parts := splitDot(t)
			switch {
			case durationRe.MatchString(t):
			case len(parts) == 2 && employmentRe.MatchString(parts[0]) && durationRe.MatchString(parts[1]):
				group.EmploymentType = parts[0]
			case employmentRe.MatchString(t):
				group.EmploymentType = t
			case group.Location == "" && group.LocationType == "" && looksLikeLocation(t):
				group.setLocation(t)
			}
		}
		for _, r := range e.Roles {
			p := parsePosition(r.Lines, r.Description)
			if p.Title == "" {
				continue
			}
			p.Company, p.CompanyURL = company, e.URL
			if p.EmploymentType == "" {
				p.EmploymentType = group.EmploymentType
			}
			if p.Location == "" && p.LocationType == "" {
				p.Location, p.LocationType = group.Location, group.LocationType
			}
			out = append(out, p)
		}
	}
	return out
}

// parseEducation turns the entries of an Education list into schools:
// school, then "Degree, Field of study", the dates, grade, activities.
func parseEducation(entries []rawEntry) []School {
	out := []School{}
	for _, e := range entries {
		if len(e.Lines) == 0 {
			continue
		}
		s := School{School: e.Lines[0], SchoolURL: e.URL}
		seenDate := false
		var extra []string
		for _, t := range e.Lines[1:] {
			switch {
			case !seenDate && isDateLine(t):
				s.DateRange, s.Start, s.End, _, _ = dates(t)
				seenDate = true
			case gradeRe.MatchString(t):
				s.Grade = gradeRe.FindStringSubmatch(t)[1]
			case activitiesRe.MatchString(t):
				s.Activities = activitiesRe.FindStringSubmatch(t)[1]
			case skillsOf(t) != "":
				s.Skills = skillsOf(t)
			case !seenDate && s.Degree == "" && s.FieldOfStudy == "":
				if i := strings.Index(t, ", "); i > 0 {
					s.Degree, s.FieldOfStudy = t[:i], strings.TrimSpace(t[i+2:])
				} else {
					s.Degree = t
				}
			default:
				extra = append(extra, t)
			}
		}
		s.Description = strings.TrimSpace(e.Description)
		if s.Description == "" && len(extra) > 0 {
			s.Description = strings.Join(extra, "\n")
		}
		out = append(out, s)
	}
	return out
}

// currentPosition is the first position still held (no end year), if any.
func currentPosition(ps []Position) (Position, bool) {
	for _, p := range ps {
		if p.DateRange != "" && !strings.ContainsAny(p.End, "0123456789") && p.End != "" {
			return p, true
		}
	}
	return Position{}, false
}

// readSection reads one Experience/Education list from the loaded page.
func readSection(p browser.PageInterface, kind string) (rawSection, error) {
	var raw rawSection
	err := run(p, profileSectionJS, &raw, kind)
	return raw, err
}

// sectionScrolls bounds how often a details page is scrolled for more
// entries.
const sectionScrolls = 6

// emptySectionWait is how long a details section may render without
// entries before it counts as empty.
var emptySectionWait = 3 * time.Second

// readDetails opens a profile's /details/<kind>/ page and reads the whole
// list, scrolling until no more entries render. ok is false when the page
// never showed the list.
func (b *LinkedInBot) readDetails(ctx context.Context, p browser.PageInterface, profileURL, kind string) ([]rawEntry, bool) {
	base := strings.TrimSuffix(profileURL, "/") + "/"
	if err := navigate(ctx, p, base+"details/"+kind+"/"); err != nil {
		return nil, false
	}
	// Wait for the list: entries, or the section rendered and still empty
	// a moment later (a member without education).
	var raw rawSection
	var foundAt time.Time
	if err := poll(ctx, findTimeout, func() (bool, error) {
		r, err := readSection(p, kind)
		if err != nil || !r.Found {
			return false, nil
		}
		raw = r
		if foundAt.IsZero() {
			foundAt = time.Now()
		}
		return len(r.Entries) > 0 || time.Since(foundAt) >= emptySectionWait, nil
	}); err != nil && !raw.Found {
		return nil, false
	}
	for i := 0; i < sectionScrolls; i++ {
		scrollPage(p)
		if sleepCtx(ctx, scrollSettle) != nil {
			break
		}
		again, err := readSection(p, kind)
		if err != nil || len(again.Entries) <= len(raw.Entries) {
			break
		}
		raw = again
	}
	return raw.Entries, true
}

// asJSONList converts typed entries to the []interface{} of maps a node
// item carries.
func asJSONList(v interface{}) []interface{} {
	out := []interface{}{}
	b, err := json.Marshal(v)
	if err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// setDetails puts experience and education onto a profile item, with the
// current position's title as job_title (the headline when no position is
// current) and its company as current_company.
func setDetails(m map[string]interface{}, exp []Position, edu []School) {
	m["experience"] = asJSONList(exp)
	m["education"] = asJSONList(edu)
	m["job_title"], m["current_company"] = m["headline"], ""
	if cur, ok := currentPosition(exp); ok {
		m["job_title"], m["current_company"] = cur.Title, cur.Company
	}
}

// fromJSONList is asJSONList's inverse.
func fromJSONList[T any](v []interface{}) []T {
	var out []T
	if b, err := json.Marshal(v); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}
