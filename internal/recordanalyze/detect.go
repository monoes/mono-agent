package recordanalyze

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// InputSpec is a detected run-time input of the action.
type InputSpec struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // string | number | secret | file
	Format    string `json:"format,omitempty"`
	Label     string `json:"label,omitempty"`
	Default   string `json:"default,omitempty"` // recorded value (never for secrets)
	Required  bool   `json:"required"`
	Marked    bool   `json:"marked,omitempty"` // the user marked it as an input
	FromEvent string `json:"fromEvent"`
}

// ExtractField is one field of an extract group.
type ExtractField struct {
	Name      string   `json:"name"`
	Selector  string   `json:"selector"` // relative to the item for lists
	Attribute string   `json:"attribute,omitempty"`
	Samples   []string `json:"samples,omitempty"`
	EventID   string   `json:"eventId"`
}

// ExtractGroup is the data the user marked: one list (container + item
// pattern) or a set of single values.
type ExtractGroup struct {
	List      bool           `json:"list"`
	Container string         `json:"container,omitempty"`
	Item      string         `json:"item,omitempty"`
	Fields    []ExtractField `json:"fields"`
}

// Repetition is the same step sequence repeated on sibling elements: a
// for_each over the items matched by ItemSelector.
type Repetition struct {
	First        int    `json:"first"` // step index of the first occurrence
	Period       int    `json:"period"`
	Count        int    `json:"count"`
	ItemSelector string `json:"itemSelector"`
	Relative     string `json:"relative,omitempty"` // target within one item
}

// LoginInfo is a login form detected at the start of the recording.
type LoginInfo struct {
	URL      string `json:"url"`      // page with the login form
	AfterURL string `json:"afterUrl"` // where the submit led
	Steps    []int  `json:"steps"`    // step indexes of the login flow
	Username string `json:"usernameSelector,omitempty"`
}

// Analysis is the normalized recording plus everything Detect found.
type Analysis struct {
	*Normalized
	Inputs     []InputSpec    `json:"inputs"`
	Extracts   []ExtractGroup `json:"extracts,omitempty"`
	Repeats    []Repetition   `json:"repeats,omitempty"`
	Login      *LoginInfo     `json:"login,omitempty"`
	ActionFrom string         `json:"actionStartUrl"` // where the action starts (after login)
}

var (
	emailRe      = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-zA-Z]{2,}$`)
	phoneRe      = regexp.MustCompile(`^\+?[\d\s().-]{7,}$`)
	numberRe     = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	sideEffectRe = regexp.MustCompile(`(?i)\b(save|submit|send|post|publish|create|add|delete|remove|confirm|update|pay|buy|order|apply|sign ?up|register|invite|share|reply|comment|like|follow|upload)\b`)
	nthRe        = regexp.MustCompile(`:nth-(child|of-type)\(\d+\)`)
)

// Detect finds structure in a normalized recording: the login flow,
// inputs (typed/picked values, marked params, masked secrets, id-like URL
// segments), extract groups, repetitions and outcome waits of
// side-effecting steps. It annotates n.Steps in place.
func Detect(n *Normalized) *Analysis {
	a := &Analysis{Normalized: n, ActionFrom: n.StartURL}
	a.Login = detectLogin(n)
	if a.Login != nil {
		for _, i := range a.Login.Steps {
			n.Steps[i].Login = true
		}
		if a.Login.AfterURL != "" {
			a.ActionFrom = a.Login.AfterURL
		}
	}
	taken := map[string]bool{}
	isTaken := func(s string) bool { return taken[s] }
	for i := range n.Steps {
		s := &n.Steps[i]
		if s.Login {
			continue
		}
		switch s.Kind {
		case KindType, KindSelect, KindUpload:
			in := inputFor(s)
			in.Name = Unique(in.Name, "_", isTaken)
			taken[in.Name] = true
			s.Input = in.Name
			a.Inputs = append(a.Inputs, in)
		case KindNavigate:
			s.URLTemplate = templateURL(s, &a.Inputs, taken)
		}
		if isSideEffect(s) {
			s.SideEffect = true
		}
		if s.NavigatedTo != "" && urlPattern(s.NavigatedTo) != urlPattern(s.URL) &&
			(s.Kind == KindClick || s.Kind == KindSubmit || s.Kind == KindPressKey) {
			s.Until = &action.WaitSpec{URLMatches: urlRegex(s.NavigatedTo)}
		}
	}
	a.Extracts = detectExtracts(n.Steps)
	a.Repeats = detectRepeats(n.Steps)
	return a
}

func inputFor(s *Step) InputSpec {
	in := InputSpec{Type: "string", FromEvent: s.EventID, Marked: s.Marked, Required: s.Marked}
	fp := s.Target
	base := s.Param
	if base == "" && s.SecretAs != "" && s.Masked {
		base = s.SecretAs
	}
	if fp != nil {
		in.Label = firstNonEmpty(fp.Label, fp.AriaName, fp.Placeholder)
		if base == "" {
			base = firstNonEmpty(fp.Name, fp.Label, fp.Placeholder, fp.AriaName, fp.ID, fp.TestID)
		}
	}
	in.Name = InputSlug(base)
	switch {
	case s.Masked:
		in.Type, in.Required = "secret", true
	case s.Kind == KindUpload:
		in.Type, in.Required = "file", true
	case emailRe.MatchString(s.Value):
		in.Format, in.Required = "email", true
	case numberRe.MatchString(s.Value):
		in.Type = "number"
	case phoneRe.MatchString(s.Value):
		in.Format, in.Required = "phone", true
	}
	if !s.Masked {
		in.Default = s.Value
	}
	return in
}

// templateURL turns id-like path segments of a navigate URL into inputs.
func templateURL(s *Step, inputs *[]InputSpec, taken map[string]bool) string {
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	changed := false
	for i, p := range parts {
		if p == "" || !isIDSegment(p) {
			continue
		}
		prev := "record"
		if i > 0 && parts[i-1] != "" && !isIDSegment(parts[i-1]) {
			prev = strings.TrimSuffix(parts[i-1], "s")
		}
		name := Unique(InputSlug(prev+"_id"), "_", func(n string) bool { return taken[n] })
		taken[name] = true
		*inputs = append(*inputs, InputSpec{Name: name, Type: "string", Default: p, Required: true, FromEvent: s.EventID})
		parts[i] = "{{" + name + "}}"
		changed = true
	}
	if !changed {
		return ""
	}
	return u.Scheme + "://" + u.Host + strings.Join(parts, "/") + queryPart(u)
}

func queryPart(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

func isSideEffect(s *Step) bool {
	if s.Kind == KindSubmit {
		return !looksLikeSearch(s)
	}
	if s.Kind != KindClick && !(s.Kind == KindPressKey && s.Submits) {
		return false
	}
	if looksLikeSearch(s) {
		return false
	}
	if s.Submits {
		return true
	}
	fp := s.Target
	if fp == nil {
		return false
	}
	if strings.EqualFold(fp.InputType, "submit") {
		return true
	}
	return sideEffectRe.MatchString(fp.Text + " " + fp.AriaName + " " + fp.Name)
}

func looksLikeSearch(s *Step) bool {
	fp := s.Target
	if fp == nil {
		return false
	}
	t := strings.ToLower(fp.Text + " " + fp.AriaName + " " + fp.Name + " " + fp.Role + " " + fp.InputType)
	return strings.Contains(t, "search")
}

// urlRegex matches the path of raw with id-like segments generalized.
func urlRegex(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return regexp.QuoteMeta(raw)
	}
	parts := strings.Split(u.Path, "/")
	for i, p := range parts {
		switch {
		case digitRe.MatchString(p):
			parts[i] = `\d+`
		case p != "" && isIDSegment(p):
			parts[i] = `[^/]+`
		default:
			parts[i] = regexp.QuoteMeta(p)
		}
	}
	return strings.Join(parts, "/") + `(?:[?#]|$)`
}

// detectLogin finds a login form at the start: a masked password field,
// at most one other text field before it in the same segment, and a
// submit that navigates to a different page, with more steps after it.
func detectLogin(n *Normalized) *LoginInfo {
	if len(n.Segments) < 2 {
		return nil
	}
	for _, seg := range n.Segments[:min(2, len(n.Segments))] {
		pw, texts := -1, 0
		var user string
		for i := seg.First; i <= seg.Last; i++ {
			s := n.Steps[i]
			if s.Kind == KindExtract || (s.Kind == KindClick && !s.Submits && s.NavigatedTo == "") {
				continue
			}
			if s.Kind == KindType && s.Masked && s.Target != nil && strings.EqualFold(s.Target.InputType, "password") {
				pw = i
				continue
			}
			if s.Kind == KindType {
				texts++
				if len(s.Candidates) > 0 {
					user = s.Candidates[0].CSS
				}
			}
			if pw >= 0 && s.NavigatedTo != "" && (s.Kind == KindClick || s.Kind == KindSubmit || s.Kind == KindPressKey) {
				if texts > 1 || i == len(n.Steps)-1 {
					return nil
				}
				li := &LoginInfo{URL: n.Steps[pw].URL, AfterURL: s.NavigatedTo, Username: user}
				for j := seg.First; j <= i; j++ {
					li.Steps = append(li.Steps, j)
				}
				return li
			}
		}
	}
	return nil
}

func detectExtracts(steps []Step) []ExtractGroup {
	var groups []ExtractGroup
	index := map[string]int{}
	for _, s := range steps {
		if s.Kind != KindExtract || s.Extract == nil {
			continue
		}
		m := s.Extract
		key := "single"
		if m.List {
			key = "list|" + m.ContainerSelector + "|" + m.ItemSelector
		}
		gi, ok := index[key]
		if !ok {
			groups = append(groups, ExtractGroup{List: m.List, Container: m.ContainerSelector, Item: m.ItemSelector})
			gi = len(groups) - 1
			index[key] = gi
		}
		sel := m.FieldSelector
		if sel == "" && len(s.Candidates) > 0 {
			sel = firstNonEmpty(s.Candidates[0].CSS, s.Candidates[0].XPath)
		}
		name := m.Field
		if name == "" && s.Target != nil {
			name = firstNonEmpty(s.Target.Label, s.Target.AriaName, s.Target.Name, s.Target.TestID)
		}
		if name == "" {
			name = "field"
		}
		g := &groups[gi]
		name = Unique(InputSlug(name), "_", func(n string) bool {
			for _, f := range g.Fields {
				if f.Name == n {
					return true
				}
			}
			return false
		})
		g.Fields = append(g.Fields, ExtractField{Name: name, Selector: sel, Attribute: m.Attribute, Samples: m.Samples, EventID: s.EventID})
	}
	return groups
}

func stepCSS(s Step) string {
	for _, c := range s.Candidates {
		if c.CSS != "" && nthRe.MatchString(c.CSS) {
			return c.CSS
		}
	}
	if s.Target != nil {
		return s.Target.CSS
	}
	return ""
}

func shape(s Step) string { return s.Kind + "|" + nthRe.ReplaceAllString(stepCSS(s), ":nth($1)") }

// detectRepeats finds a step sequence (period 1–4) repeated at least three
// times on sibling elements (same structural CSS path, different
// :nth-child index).
func detectRepeats(steps []Step) []Repetition {
	var reps []Repetition
	for i := 0; i < len(steps); {
		found := false
		for p := 1; p <= 4 && !found; p++ {
			count := 1
			for i+(count+1)*p <= len(steps) && sameShapes(steps, i, i+count*p, p) {
				count++
			}
			if count < 3 {
				continue
			}
			item, rel := itemSelector(steps, i, p, count)
			if item == "" {
				continue
			}
			reps = append(reps, Repetition{First: i, Period: p, Count: count, ItemSelector: item, Relative: rel})
			i += count * p
			found = true
		}
		if !found {
			i++
		}
	}
	return reps
}

func sameShapes(steps []Step, a, b, p int) bool {
	for k := 0; k < p; k++ {
		if steps[a+k].Kind == KindNavigate || shape(steps[a+k]) != shape(steps[b+k]) {
			return false
		}
	}
	return true
}

// itemSelector finds the first :nth-child compound whose index varies
// across occurrences and splits the CSS path there.
func itemSelector(steps []Step, first, p, count int) (string, string) {
	for k := 0; k < p; k++ {
		css := stepCSS(steps[first+k])
		locs := nthRe.FindAllStringIndex(css, -1)
		for li, loc := range locs {
			varies := false
			for c := 1; c < count; c++ {
				other := stepCSS(steps[first+k+c*p])
				ol := nthRe.FindAllStringIndex(other, -1)
				if len(ol) != len(locs) || other[ol[li][0]:ol[li][1]] != css[loc[0]:loc[1]] {
					varies = true
					break
				}
			}
			if !varies {
				continue
			}
			item := css[:loc[0]]
			rest := strings.TrimSpace(css[loc[1]:])
			rest = strings.TrimSpace(strings.TrimPrefix(rest, ">"))
			return nthRe.ReplaceAllString(item, ""), nthRe.ReplaceAllString(rest, "")
		}
	}
	return "", ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
