package recordanalyze

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/recording"
)

// Step kinds after normalization. They mirror the recording event types,
// minus the ones folded away (navigated, param).
const (
	KindNavigate = "navigate"
	KindClick    = "click"
	KindType     = "type"
	KindSelect   = "select_option"
	KindCheck    = "check"
	KindSubmit   = "submit"
	KindPressKey = "press_key"
	KindUpload   = "upload"
	KindScroll   = "scroll"
	KindExtract  = "extract"
)

// Step is one normalized user action. Detect fills the fields below the
// marker; Normalize fills the rest.
type Step struct {
	EventID     string                     `json:"eventId"`
	Kind        string                     `json:"kind"`
	URL         string                     `json:"url"` // page URL (navigate: the target)
	Value       string                     `json:"value,omitempty"`
	Masked      bool                       `json:"masked,omitempty"`
	SecretAs    string                     `json:"secretAs,omitempty"`
	Key         string                     `json:"key,omitempty"`
	Checked     *bool                      `json:"checked,omitempty"`
	Target      *recording.Fingerprint     `json:"target,omitempty"`
	Candidates  []action.SelectorCandidate `json:"candidates,omitempty"`
	Extract     *recording.ExtractMark     `json:"extract,omitempty"`
	Submits     bool                       `json:"submits,omitempty"`     // a folded form submit
	Double      bool                       `json:"double,omitempty"`      // a double click
	NavigatedTo string                     `json:"navigatedTo,omitempty"` // URL this step caused
	Param       string                     `json:"param,omitempty"`       // user-marked input name
	Marked      bool                       `json:"marked,omitempty"`      // user marked the value as an input
	Note        string                     `json:"note,omitempty"`
	Segment     int                        `json:"segment"`
	// Merged lists the event ids folded into this step (itself first).
	Merged []string `json:"-"`

	// --- filled by Detect ---
	Input       string           `json:"input,omitempty"`       // bound input name ({{input}})
	URLTemplate string           `json:"urlTemplate,omitempty"` // navigate URL with ID inputs
	SideEffect  bool             `json:"sideEffect,omitempty"`
	Until       *action.WaitSpec `json:"until,omitempty"`
	Login       bool             `json:"login,omitempty"` // part of the detected login flow
}

// Segment is a run of steps on one URL pattern.
type Segment struct {
	Index      int    `json:"index"`
	From       string `json:"from"` // first event id
	To         string `json:"to"`   // last event id
	Host       string `json:"host"`
	URLPattern string `json:"urlPattern"`
	First      int    `json:"first"` // step index range [First, Last]
	Last       int    `json:"last"`
}

// Normalized is the clean, small input the detector and the LLM work on.
type Normalized struct {
	RecordingID string               `json:"recordingId"`
	Goal        string               `json:"goal,omitempty"`
	StartURL    string               `json:"startUrl"`
	Domains     []string             `json:"domains"`
	Steps       []Step               `json:"steps"`
	Segments    []Segment            `json:"segments"`
	Net         []recording.NetEntry `json:"net,omitempty"`
}

// Normalize turns raw recorded events into steps: keystrokes merged into
// final values, click+submit folded, focus clicks, printable keys, stray
// scrolls and duplicate navigations dropped, navigated events attached to
// the step that caused them, candidates ranked, and segments split on URL
// pattern changes.
func Normalize(sum *recording.Summary, events []recording.Event) *Normalized {
	evs := append([]recording.Event(nil), events...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Seq < evs[j].Seq })

	n := &Normalized{}
	if sum != nil {
		n.RecordingID, n.Goal, n.StartURL = sum.ID, sum.Goal, sum.URL
	}
	params := map[string]string{} // ref event id → input name
	var steps []Step
	cur := n.StartURL
	last := func() *Step {
		if len(steps) == 0 {
			return nil
		}
		return &steps[len(steps)-1]
	}
	add := func(ev recording.Event, kind string) {
		s := Step{EventID: ev.ID, Kind: kind, URL: ev.URL, Value: ev.Value, Masked: ev.Masked,
			SecretAs: ev.SecretAs, Key: ev.Key, Checked: ev.Checked, Target: ev.Target,
			Extract: ev.Extract, Note: ev.Note, Merged: []string{ev.ID}}
		if ev.Target != nil {
			s.Candidates = rankCandidates(ev.Target)
		}
		steps = append(steps, s)
	}
	merge := func(ev recording.Event) {
		l := last()
		l.Merged = append(l.Merged, ev.ID)
	}

	var prevT int64
	for _, ev := range evs {
		if ev.Type != recording.EvClick {
			prevT = 0
		}
		if cur == "" && ev.URL != "" {
			cur = ev.URL
		}
		l := last()
		switch ev.Type {
		case recording.EvParam:
			// The latest mark per event wins; note "unset" removes it.
			if ev.Param != nil && ev.Param.RefEvent != "" {
				if ev.Note == "unset" {
					delete(params, ev.Param.RefEvent)
				} else {
					params[ev.Param.RefEvent] = ev.Param.Name
				}
			}
		case recording.EvNavigated:
			if l != nil && l.NavigatedTo == "" && l.Kind != KindNavigate {
				l.NavigatedTo = ev.URL
			} else if l == nil && n.StartURL == "" {
				n.StartURL = ev.URL
			}
			cur = ev.URL
		case recording.EvNavigate:
			if sameURL(ev.URL, cur) && l != nil {
				continue // reload / duplicate
			}
			if l != nil && l.Kind == KindNavigate {
				// Nothing happened in between: the later URL wins.
				l.URL, l.EventID = ev.URL, ev.ID
				merge(ev)
			} else {
				add(ev, KindNavigate)
			}
			cur = ev.URL
		case recording.EvType:
			switch {
			case l != nil && l.Kind == KindType && sameTarget(l.Target, ev.Target):
				l.Value, l.Masked, l.SecretAs = ev.Value, ev.Masked, ev.SecretAs
				merge(ev)
			case l != nil && l.Kind == KindClick && sameTarget(l.Target, ev.Target) && isTextField(ev.Target):
				prior := append([]string(nil), l.Merged...)
				steps = steps[:len(steps)-1] // focus click before typing
				add(ev, KindType)
				steps[len(steps)-1].Merged = append(steps[len(steps)-1].Merged, prior...)
			default:
				add(ev, KindType)
			}
		case recording.EvSubmit:
			if l != nil && (l.Kind == KindClick || (l.Kind == KindPressKey && l.Key == "Enter")) {
				l.Submits = true
				merge(ev)
			} else {
				add(ev, KindSubmit)
			}
		case recording.EvPressKey:
			if isPrintableKey(ev.Key) || ev.Key == "Tab" || ev.Key == "Shift+Tab" {
				continue
			}
			add(ev, KindPressKey)
		case recording.EvScroll:
			if l != nil && l.Kind == KindScroll {
				merge(ev)
				continue
			}
			add(ev, KindScroll)
		case recording.EvClick:
			// A dblclick arrives as two clicks plus one with note "double".
			if l != nil && l.Kind == KindClick && sameTarget(l.Target, ev.Target) && (ev.Note == "double" || ev.T-prevT < 400) {
				merge(ev)
				if ev.Note == "double" {
					l.Double = true
				}
				prevT = ev.T
				continue
			}
			add(ev, KindClick)
			prevT = ev.T
			steps[len(steps)-1].Note = strings.TrimPrefix(ev.Note, "double")
		case recording.EvExtract:
			// "supersedes eN" replaces an earlier pick (list upgrade, rename).
			if ids, ok := strings.CutPrefix(ev.Note, "supersedes "); ok {
				steps = dropSteps(steps, strings.FieldsFunc(ids, func(r rune) bool { return r == ' ' || r == ',' }))
				ev.Note = ""
			}
			add(ev, KindExtract)
		case recording.EvSelect, recording.EvCheck, recording.EvUpload:
			kind := map[string]string{recording.EvSelect: KindSelect, recording.EvCheck: KindCheck,
				recording.EvUpload: KindUpload}[ev.Type]
			add(ev, kind)
		}
	}

	// A scroll only matters when lazily loaded content is extracted next.
	kept := steps[:0]
	for i, s := range steps {
		if s.Kind == KindScroll && (i+1 >= len(steps) || steps[i+1].Kind != KindExtract) {
			continue
		}
		kept = append(kept, s)
	}
	steps = kept

	if n.StartURL == "" && len(steps) > 0 {
		n.StartURL = steps[0].URL
	}
	if len(steps) > 0 && steps[0].Kind != KindNavigate && n.StartURL != "" {
		steps = append([]Step{{EventID: "start", Kind: KindNavigate, URL: n.StartURL}}, steps...)
	}
	for i := range steps {
		for _, id := range steps[i].Merged {
			if name, ok := params[id]; ok {
				steps[i].Marked, steps[i].Param = true, name
			}
		}
	}
	n.Steps = steps
	n.Domains = collectDomains(steps, n.StartURL)
	n.Segments = splitSegments(steps)
	for _, seg := range n.Segments {
		for i := seg.First; i <= seg.Last; i++ {
			n.Steps[i].Segment = seg.Index
		}
	}
	return n
}

// dropSteps removes the steps that own any of the event ids.
func dropSteps(steps []Step, ids []string) []Step {
	drop := map[string]bool{}
	for _, id := range ids {
		drop[id] = true
	}
	kept := steps[:0]
	for _, s := range steps {
		if !drop[s.EventID] {
			kept = append(kept, s)
		}
	}
	return kept
}

func targetKey(fp *recording.Fingerprint) string {
	if fp == nil {
		return ""
	}
	for _, k := range []string{fp.CSS, fp.XPath, fp.TestID, fp.ID, fp.Name} {
		if k != "" {
			return k
		}
	}
	return fp.Tag + "|" + fp.Text
}

func sameTarget(a, b *recording.Fingerprint) bool {
	return a != nil && b != nil && targetKey(a) == targetKey(b)
}

func isTextField(fp *recording.Fingerprint) bool {
	if fp == nil {
		return false
	}
	switch strings.ToLower(fp.Tag) {
	case "textarea":
		return true
	case "input":
		switch strings.ToLower(fp.InputType) {
		case "checkbox", "radio", "submit", "button", "file", "reset", "image":
			return false
		}
		return true
	}
	return fp.Role == "textbox" || fp.Role == "searchbox"
}

func isPrintableKey(k string) bool {
	return len([]rune(k)) == 1 || k == "Space" || k == "Backspace" || k == "Delete"
}

func sameURL(a, b string) bool {
	strip := func(s string) string {
		if i := strings.IndexByte(s, '#'); i >= 0 {
			s = s[:i]
		}
		return strings.TrimSuffix(s, "/")
	}
	return a != "" && strip(a) == strip(b)
}

// rankCandidates converts recorded candidates to selector candidates,
// best first; a candidate that matched several elements at record time is
// scored down. With no recorded candidates the fingerprint's own
// attributes are used.
func rankCandidates(fp *recording.Fingerprint) []action.SelectorCandidate {
	var out []action.SelectorCandidate
	for _, c := range fp.Candidates {
		sc := action.SelectorCandidate{Score: c.Score}
		switch c.Kind {
		case "css":
			sc.CSS = c.Value
		case "xpath":
			sc.XPath = c.Value
		case "aria":
			sc.Aria = &action.AriaSelector{Role: c.Role, Name: c.Name}
		case "text":
			sc.Text = c.Value
		default:
			continue
		}
		if !c.Unique && c.Count > 1 {
			sc.Score *= 0.5
		}
		out = append(out, sc)
	}
	if len(out) == 0 {
		switch {
		case fp.TestID != "":
			out = append(out, action.SelectorCandidate{CSS: `[data-testid="` + fp.TestID + `"]`, Score: 0.9})
		case fp.ID != "":
			out = append(out, action.SelectorCandidate{CSS: "#" + fp.ID, Score: 0.85})
		}
		if fp.Name != "" && fp.Tag != "" {
			out = append(out, action.SelectorCandidate{CSS: fp.Tag + `[name="` + fp.Name + `"]`, Score: 0.8})
		}
		if fp.Role != "" && fp.AriaName != "" {
			out = append(out, action.SelectorCandidate{Aria: &action.AriaSelector{Role: fp.Role, Name: fp.AriaName}, Score: 0.75})
		}
		if fp.CSS != "" {
			out = append(out, action.SelectorCandidate{CSS: fp.CSS, Score: 0.5})
		}
		if fp.XPath != "" {
			out = append(out, action.SelectorCandidate{XPath: fp.XPath, Score: 0.4})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func collectDomains(steps []Step, start string) []string {
	seen := map[string]bool{}
	var out []string
	addHost := func(raw string) {
		h := hostOf(raw)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	addHost(start)
	for _, s := range steps {
		addHost(s.URL)
		addHost(s.NavigatedTo)
	}
	sort.Strings(out)
	return out
}

var (
	uuidRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	digitRe = regexp.MustCompile(`^\d+$`)
	hexRe   = regexp.MustCompile(`^[0-9a-fA-F]{12,}$`)
	mixedRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,}$`)
)

// isIDSegment reports a URL path segment that looks like a record id.
func isIDSegment(seg string) bool {
	switch {
	case digitRe.MatchString(seg), uuidRe.MatchString(seg), hexRe.MatchString(seg):
		return true
	case mixedRe.MatchString(seg):
		return strings.ContainsAny(seg, "0123456789") && strings.IndexFunc(seg, func(r rune) bool {
			return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		}) >= 0 && !strings.ContainsAny(seg, "-_")
	}
	return false
}

// urlPattern is host + path with id-like segments replaced by ":id".
func urlPattern(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if isIDSegment(p) {
			parts[i] = ":id"
		}
	}
	return strings.ToLower(u.Hostname()) + "/" + strings.Join(parts, "/")
}

func splitSegments(steps []Step) []Segment {
	var segs []Segment
	for i, s := range steps {
		pat := urlPattern(s.URL)
		if len(segs) == 0 || segs[len(segs)-1].URLPattern != pat {
			segs = append(segs, Segment{Index: len(segs), From: s.EventID, Host: hostOf(s.URL), URLPattern: pat, First: i})
		}
		seg := &segs[len(segs)-1]
		seg.To, seg.Last = s.EventID, i
	}
	return segs
}
