package recordanalyze

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

var secretTokenRe = regexp.MustCompile(`(sk-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,})`)

// Lint runs the deterministic checks on a draft (spec §8.4 step 4):
// action.Validate against the draft package, every configKey resolving
// against the recorded DOM snippets to exactly one element, no literal
// secrets, and no navigation outside the domains. snippets maps event id
// → DOM snippet HTML.
func Lint(out *Output, env *Env, m *automation.Manifest, snippets map[string]string) []automation.IssueJSON {
	file := "actions/" + out.Action.ActionType + ".json"
	var issues []automation.IssueJSON
	add := func(sev, step, code, f string, a ...any) {
		issues = append(issues, automation.IssueJSON{File: file, Severity: sev, StepID: step, Code: code, Message: fmt.Sprintf(f, a...)})
	}
	ctx := newDraftContext(out, env, m)
	seen := map[string]bool{}
	for _, is := range action.Validate(&out.Action, ctx) {
		seen[is.Code+"|"+is.StepID] = true
		add(is.Severity, is.StepID, is.Code, "%s", is.Message)
	}

	// Selector resolution against the recorded DOM.
	docs := parseSnippets(snippets)
	listKeys, keys := map[string]bool{}, map[string]string{} // key → first step id
	allSteps(out, func(s *action.StepDef) {
		if s.ConfigKey == "" {
			return
		}
		if _, ok := keys[s.ConfigKey]; !ok {
			keys[s.ConfigKey] = s.ID
		}
		if s.Type == "extract_multiple" { // matches every item; extract_table's key is the container
			listKeys[s.ConfigKey] = true
		}
	})
	if len(docs) == 0 && len(keys) > 0 {
		add("warning", "", "no_dom_snippets", "the recording has no DOM snippets; selectors were not checked")
	} else {
		for _, key := range sortedKeys(keys) {
			e, ok := ctx.Selector(key)
			if !ok {
				continue // CheckOutput reports it
			}
			code, msg := resolveEntry(e, docs, listKeys[key])
			switch code {
			case "":
			case "selector_unverified", "selector_unverifiable":
				add("warning", keys[key], code, "configKey %q: %s", key, msg)
			default:
				add("error", keys[key], code, "configKey %q: %s", key, msg)
			}
		}
	}

	// Literal secrets.
	masked := maskedTargets(env)
	allSteps(out, func(s *action.StepDef) {
		for _, v := range []string{fmt.Sprint(valueOr(s.Value)), s.Text, s.URL} {
			if secretTokenRe.MatchString(v) {
				add("error", s.ID, "literal_secret", "step contains what looks like a credential; use a {{secret:<name>}} input")
				return
			}
		}
		if s.Type != "type" || s.ConfigKey == "" {
			return
		}
		v := fmt.Sprint(valueOr(s.Value))
		if strings.Contains(v, "{{") {
			return
		}
		if e, ok := ctx.Selector(s.ConfigKey); ok && hitsMasked(e, masked) {
			add("error", s.ID, "literal_secret", "a masked field is typed with a literal value; use {{secret:<name>}}")
		}
	})

	// Navigation outside the domains.
	allSteps(out, func(s *action.StepDef) {
		if s.Type != "navigate" || s.URL == "" || strings.HasPrefix(strings.TrimSpace(s.URL), "{{") {
			return
		}
		h := hostOf(s.URL)
		if h != "" && len(m.Site.Domains) > 0 && !domainAllowed(h, m.Site.Domains) {
			add("error", s.ID, "off_domain", "navigate to %s is outside the recorded domains %v", h, m.Site.Domains)
		}
	})
	allSteps(out, func(s *action.StepDef) {
		if AdvancedSteps[s.Type] && !env.AllowAdvanced {
			add("error", s.ID, "advanced_step", "%s needs --allow-advanced", s.Type)
		}
		if s.Type == "navigate" && !strings.HasPrefix(strings.TrimSpace(s.URL), "{{") && urlCarriesToken(s.URL) {
			add("error", s.ID, "token_in_url", "navigate URL carries a token or credential parameter; make it an input or drop it")
		}
	})
	allSteps(out, func(s *action.StepDef) {
		if s.Type == "page_script" && !seen["script_used|"+s.ID] {
			add("warning", s.ID, "script_used", "uses page_script %q; prefer declarative steps", s.Script)
		}
	})
	return issues
}

func valueOr(v any) any {
	if v == nil {
		return ""
	}
	return v
}

// domainAllowed: exact host, or "*.x.com" matching x.com and its subdomains.
func domainAllowed(host string, domains []string) bool {
	for _, d := range domains {
		d = strings.ToLower(d)
		if suf, ok := strings.CutPrefix(d, "*."); ok {
			if host == suf || strings.HasSuffix(host, "."+suf) {
				return true
			}
		} else if host == d {
			return true
		}
	}
	return false
}

func parseSnippets(snippets map[string]string) []*goquery.Document {
	var docs []*goquery.Document
	for _, id := range sortedKeys(snippets) {
		if strings.TrimSpace(snippets[id]) == "" {
			continue
		}
		if d, err := goquery.NewDocumentFromReader(strings.NewReader(snippets[id])); err == nil {
			docs = append(docs, d)
		}
	}
	return docs
}

// resolveEntry checks an entry's candidates against the snippets. A
// candidate resolves when it matches exactly one element in some snippet
// (at least one for list selectors) and never several in one snippet.
// Returns "" when some candidate resolves, else an issue code.
func resolveEntry(e *action.SelectorEntry, docs []*goquery.Document, list bool) (string, string) {
	checkable, ambiguous := false, false
	for _, c := range e.Candidates {
		counts, ok := candidateCounts(c, docs)
		if !ok {
			continue
		}
		checkable = true
		maxN, hit := 0, false
		for _, n := range counts {
			maxN = max(maxN, n)
			hit = hit || n >= 1
		}
		if list && hit {
			return "", ""
		}
		if hit && maxN == 1 {
			return "", ""
		}
		if maxN > 1 {
			ambiguous = true
		}
	}
	switch {
	case !checkable:
		return "selector_unverified", "only xpath candidates; not checked against the recorded DOM"
	case ambiguous:
		return "selector_ambiguous", "matches more than one element in the recorded DOM"
	}
	// Snippets are fragments around recorded elements: an element outside
	// every snippet is not evidence the selector is wrong (e2e D7).
	return "selector_unverifiable", "no recorded DOM snippet contains a match; not verifiable offline"
}

// candidateCounts returns the match count per snippet; ok is false for a
// candidate kind that cannot be checked here (xpath).
func candidateCounts(c action.SelectorCandidate, docs []*goquery.Document) (counts []int, ok bool) {
	for _, d := range docs {
		var n int
		switch {
		case c.CSS != "":
			sel, err := safeFind(d, c.CSS)
			if err != nil {
				return nil, true // invalid CSS never resolves
			}
			n = sel
		case c.Text != "":
			d.Find("body *").Each(func(_ int, s *goquery.Selection) {
				if s.Children().Length() == 0 && strings.TrimSpace(s.Text()) == c.Text {
					n++
				}
			})
		case c.Aria != nil:
			d.Find("body *").Each(func(_ int, s *goquery.Selection) {
				if ariaMatches(s, c.Aria) {
					n++
				}
			})
		default:
			return nil, false
		}
		counts = append(counts, n)
	}
	return counts, true
}

func safeFind(d *goquery.Document, css string) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("invalid selector %q", css)
		}
	}()
	return d.Find(css).Length(), nil
}

var implicitRoles = map[string]string{"button": "button", "a": "link", "textarea": "textbox", "select": "combobox", "h1": "heading", "h2": "heading", "h3": "heading"}

func ariaMatches(s *goquery.Selection, a *action.AriaSelector) bool {
	tag := goquery.NodeName(s)
	role, _ := s.Attr("role")
	if role == "" {
		role = implicitRoles[tag]
		if tag == "input" {
			switch t, _ := s.Attr("type"); t {
			case "submit", "button":
				role = "button"
			case "checkbox":
				role = "checkbox"
			default:
				role = "textbox"
			}
		}
	}
	if a.Role != "" && role != a.Role {
		return false
	}
	name, _ := s.Attr("aria-label")
	if name == "" {
		if tag == "input" {
			if v, ok := s.Attr("placeholder"); ok {
				name = v
			} else {
				name, _ = s.Attr("value")
			}
		} else {
			name = strings.TrimSpace(s.Text())
		}
	}
	return strings.TrimSpace(name) == a.Name
}

type maskedSet struct{ css, aria map[string]bool }

func maskedTargets(env *Env) maskedSet {
	ms := maskedSet{css: map[string]bool{}, aria: map[string]bool{}}
	if env.Analysis == nil {
		return ms
	}
	for _, s := range env.Analysis.Steps {
		if !s.Masked {
			continue
		}
		for _, c := range s.Candidates {
			if c.CSS != "" {
				ms.css[c.CSS] = true
			}
			if c.Aria != nil {
				ms.aria[c.Aria.Name] = true
			}
		}
		if s.Target != nil && s.Target.CSS != "" {
			ms.css[s.Target.CSS] = true
		}
	}
	return ms
}

func hitsMasked(e *action.SelectorEntry, ms maskedSet) bool {
	for _, c := range e.Candidates {
		if (c.CSS != "" && ms.css[c.CSS]) || (c.Aria != nil && ms.aria[c.Aria.Name]) {
			return true
		}
	}
	return false
}
