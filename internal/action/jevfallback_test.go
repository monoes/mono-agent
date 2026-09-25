package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/jevpick"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Fixtures: a CDP-capable fake tab (answers jevpick's snapshot, freshness,
// mark and unmark evaluations — adapted from internal/jevpick/pick_test.go)
// whose Element() only finds the element jevpick marked.
// ---------------------------------------------------------------------------

var errSelectorMiss = errors.New("element not found: #gone")

// markedElement is the handle the fake returns for a marker selector.
type markedElement struct {
	browser.ElementHandle
	node int
}

type jevFakePage struct {
	fakePage
	state    jevpick.PageState
	marked   map[string]int // marker → node
	unmarks  int
	lookups  []string
	cdpCalls int
}

func newJevFakePage() *jevFakePage {
	return &jevFakePage{
		state: jevpick.PageState{
			URL: "https://gemini.example/app", Title: "Chat", Text: "Ask anything",
			Marker: json.RawMessage(`["m"]`), PageKey: json.RawMessage(`["k"]`),
			Guards: map[string]json.RawMessage{"10": json.RawMessage(`["g10"]`), "20": json.RawMessage(`["g20"]`), "30": json.RawMessage(`["g30"]`)},
			Actions: []jevpick.Action{
				{ID: "e1", Kind: "fill", Label: "Enter a prompt here", Role: "textbox", Node: 10},
				{ID: "e2", Kind: "click", Label: "Open Enter a prompt here", Role: "textbox", Node: 10},
				{ID: "e3", Kind: "click", Label: "Send message", Role: "button", Node: 20},
				{ID: "e4", Kind: "click", Label: "Settings", Role: "link", Node: 30},
				{ID: "wait", Kind: "wait", Label: "Wait for the page to update"},
			},
		},
		marked: map[string]int{},
	}
}

var (
	fakeMarkRe   = regexp.MustCompile(`nodes\.get\((\d+)\); if \(!e\?\.isConnected\) return false; e\.setAttribute\("data-monoagent-jev","([0-9a-f]+)"\)`)
	fakeUnmarkRe = regexp.MustCompile(`querySelectorAll\('\[data-monoagent-jev="([0-9a-f]+)"\]'\)`)
	fakeGuardRe  = regexp.MustCompile(`nodes\.get\((\d+)\)`)
	markerSelRe  = regexp.MustCompile(`^\[data-monoagent-jev='([0-9a-f]{16})'\]$`)
)

func (f *jevFakePage) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	f.cdpCalls++
	if method != "Runtime.evaluate" {
		return map[string]interface{}{}, nil
	}
	expr := params["expression"].(string)
	var value interface{}
	switch {
	case strings.Contains(expr, "state?.marker"):
		value = []interface{}{"m"}
	case strings.Contains(expr, "c.pageKey(),c.guard"):
		node := fakeGuardRe.FindStringSubmatch(expr)[1]
		var g interface{}
		_ = json.Unmarshal(f.state.Guards[node], &g)
		value = []interface{}{[]interface{}{"k"}, g}
	case fakeMarkRe.MatchString(expr):
		m := fakeMarkRe.FindStringSubmatch(expr)
		var node int
		_ = json.Unmarshal([]byte(m[1]), &node)
		f.marked[m[2]] = node
		value = true
	case fakeUnmarkRe.MatchString(expr):
		f.unmarks++
		delete(f.marked, fakeUnmarkRe.FindStringSubmatch(expr)[1])
		value = true
	default: // the snapshot
		raw, _ := json.Marshal(f.state)
		_ = json.Unmarshal(raw, &value)
	}
	return map[string]interface{}{"result": map[string]interface{}{"value": value}}, nil
}

// Element finds only marked elements; every real selector misses.
func (f *jevFakePage) Element(selector string, timeout time.Duration) (browser.ElementHandle, error) {
	f.lookups = append(f.lookups, selector)
	if m := markerSelRe.FindStringSubmatch(selector); m != nil {
		if node, ok := f.marked[m[1]]; ok {
			return &markedElement{node: node}, nil
		}
	}
	return nil, errSelectorMiss
}

func (f *jevFakePage) ElementX(xpath string, timeout time.Duration) (browser.ElementHandle, error) {
	f.lookups = append(f.lookups, xpath)
	return nil, fmt.Errorf("element not found: %s", xpath)
}

func jevTestClient(t *testing.T) *jev.Client {
	t.Helper()
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newJevExecutor(t *testing.T, page browser.PageInterface, picker bool, minP float64) *ActionExecutor {
	t.Helper()
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "a1", TargetPlatform: "gemini", Type: t.Name()}
	if picker {
		ae.SetJevPicker(jevTestClient(t), minP)
	}
	return ae
}

// Kind click: 1 = prompt box (node 10, click row), 2 = Send, 3 = Settings.
// Kind any adds nothing new (node 10 is deduplicated).
func sendStep(typ string) StepDef {
	return StepDef{ID: "t2_find_send_button", Type: typ, Selector: "#gone", Intent: "the chat's Send message button"}
}

// ---------------------------------------------------------------------------
// resolveElement
// ---------------------------------------------------------------------------

func TestJevFallbackConfidentPickReturnsMarkedElement(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)

	el, err := ae.resolveElement(sendStep("click"))
	if err != nil {
		t.Fatal(err)
	}
	me, ok := el.(*markedElement)
	if !ok || me.node != 20 {
		t.Fatalf("element = %#v, want the marked Send button (node 20)", el)
	}
	if srv.Calls() != 1 {
		t.Errorf("calls = %d, want 1", srv.Calls())
	}
	if last := page.lookups[len(page.lookups)-1]; !markerSelRe.MatchString(last) || page.lookups[0] != "#gone" {
		t.Errorf("lookups = %v, want #gone then the marker selector", page.lookups)
	}
	// The marker stays until the step finishes.
	if len(page.marked) != 1 || ae.jevMarker == "" {
		t.Errorf("marker released too early: %v", page.marked)
	}
	ae.releaseJevMarker()
	if len(page.marked) != 0 || page.unmarks != 1 {
		t.Errorf("release left %v (unmarks %d)", page.marked, page.unmarks)
	}
	// The request carries the intent and a click-only element table.
	q := srv.Requests()[0].Questions["target"]
	if q.Instructions.(map[string]any)["intent"] != "the chat's Send message button" {
		t.Errorf("instructions = %v", q.Instructions)
	}
	if crit := q.Criteria.(map[string]any); len(crit) != 4 {
		t.Errorf("criteria = %v, want 3 clickable nodes + NONE", crit)
	}
}

func TestJevFallbackXPathBranch(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)
	step := StepDef{ID: "s", Type: "click", XPath: "//button[@id='gone']", Intent: "the Send button"}
	el, err := ae.resolveElement(step)
	if err != nil || el.(*markedElement).node != 20 || srv.Calls() != 1 {
		t.Fatalf("el = %#v err = %v calls = %d", el, err, srv.Calls())
	}
}

func TestJevFallbackNoneKeepsOriginalError(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "NONE"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)
	el, err := ae.resolveElement(sendStep("click"))
	if el != nil || !errors.Is(err, errSelectorMiss) || !strings.Contains(err.Error(), "jev fallback") {
		t.Fatalf("el = %v err = %v, want the original error wrapped", el, err)
	}
	if srv.Calls() != 1 || len(page.marked) != 0 {
		t.Errorf("calls = %d marked = %v", srv.Calls(), page.marked)
	}
	// The step's own error still reaches the step result unchanged in kind.
	res, _ := ae.stepClick(context.Background(), sendStep("click"))
	if res.Success || !errors.Is(res.Error, errSelectorMiss) {
		t.Errorf("stepClick result = %+v", res)
	}
}

func TestJevFallbackLowProbabilityKeepsOriginalError(t *testing.T) {
	jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"})) // answers at p=0.94
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.95)
	el, err := ae.resolveElement(sendStep("click"))
	if el != nil || !errors.Is(err, errSelectorMiss) || !strings.Contains(err.Error(), jevpick.ErrNoMatch.Error()) {
		t.Fatalf("el = %v err = %v", el, err)
	}
	if len(page.marked) != 0 {
		t.Errorf("low-p pick marked %v", page.marked)
	}
}

func TestJevFallbackTransportErrorKeepsOriginalError(t *testing.T) {
	srv := jevtest.NewServer(t, nil)
	srv.SetStatus(401)
	ae := newJevExecutor(t, newJevFakePage(), true, 0.5)
	if _, err := ae.resolveElement(sendStep("click")); !errors.Is(err, errSelectorMiss) {
		t.Fatalf("err = %v", err)
	}
}

func TestJevFallbackNoIntentNeverCallsJev(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)
	step := sendStep("click")
	step.Intent = "  "
	el, err := ae.resolveElement(step)
	if el != nil || err != errSelectorMiss {
		t.Fatalf("el = %v err = %v, want the unwrapped original error", el, err)
	}
	if srv.Calls() != 0 || page.cdpCalls != 0 {
		t.Errorf("Jev called %d times, CDP %d times", srv.Calls(), page.cdpCalls)
	}
}

func TestJevFallbackDisabledNeverCallsJev(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, false, 0.5)
	el, err := ae.resolveElement(sendStep("click"))
	if el != nil || err != errSelectorMiss {
		t.Fatalf("el = %v err = %v", el, err)
	}
	res, _ := ae.stepFindElement(context.Background(), sendStep("find_element"))
	if res.Success || res.Error.Error() != "find_element t2_find_send_button: "+errSelectorMiss.Error() {
		t.Errorf("find_element error changed: %v", res.Error)
	}
	if srv.Calls() != 0 || page.cdpCalls != 0 {
		t.Errorf("Jev called %d times, CDP %d times", srv.Calls(), page.cdpCalls)
	}
}

func TestJevFallbackPageWithoutCDPReturnsCause(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	page := &fakePage{elementFn: func(string) (browser.ElementHandle, error) { return nil, errSelectorMiss }}
	ae := newJevExecutor(t, page, true, 0.5)
	if _, err := ae.resolveElement(sendStep("click")); err != errSelectorMiss || srv.Calls() != 0 {
		t.Fatalf("err = %v calls = %d", err, srv.Calls())
	}
}

func TestJevFallbackHintCache(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "2"}))
	ae := newJevExecutor(t, newJevFakePage(), true, 0.5)
	for i := 0; i < 2; i++ {
		if _, err := ae.resolveElement(sendStep("click")); err != nil {
			t.Fatal(err)
		}
		ae.releaseJevMarker()
	}
	reqs := srv.Requests()
	first := reqs[0].Questions["target"].Instructions.(map[string]any)
	second := reqs[1].Questions["target"].Instructions.(map[string]any)
	if first["hint"] != nil {
		t.Errorf("first pick had a hint: %v", first["hint"])
	}
	if h, _ := second["hint"].(string); !strings.Contains(h, `"Send message"`) {
		t.Errorf("second hint = %v, want the last label", second["hint"])
	}
}

func TestJevKind(t *testing.T) {
	for typ, want := range map[string]string{"click": "click", "type": "fill", "input": "fill", "find_element": "any", "hover": "any"} {
		if got := jevKind(typ); got != want {
			t.Errorf("jevKind(%q) = %q, want %q", typ, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// stepFindElement
// ---------------------------------------------------------------------------

func TestJevFallbackFindElementSelectorBranch(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "3"})) // any: 1 prompt, 2 Send, 3 Settings
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)
	step := sendStep("find_element")
	step.VariableName = "sendButton"
	res, err := ae.stepFindElement(context.Background(), step)
	if err != nil || !res.Success {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if res.Element.(*markedElement).node != 30 || ae.execCtx.GetElement(step.ID) != res.Element || ae.execCtx.GetElement("sendButton") != res.Element {
		t.Errorf("element not stored: %+v", res)
	}
	if srv.Calls() != 1 {
		t.Errorf("calls = %d", srv.Calls())
	}
	// The step finished: its marker is gone.
	if len(page.marked) != 0 || ae.jevMarker != "" {
		t.Errorf("marker left after the step: %v", page.marked)
	}

	// NONE: the step result carries the original error, wrapped.
	srv.SetHandler(jevtest.Fixed(map[string]string{"target": "NONE"}))
	res, _ = ae.stepFindElement(context.Background(), sendStep("find_element"))
	if res.Success || !errors.Is(res.Error, errSelectorMiss) || !strings.HasPrefix(res.Error.Error(), "find_element t2_find_send_button: ") ||
		!strings.Contains(res.Error.Error(), "jev fallback") {
		t.Errorf("res = %+v", res)
	}
}

func TestJevFallbackFindElementListExhaustedPicksOnce(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"target": "1"}))
	page := newJevFakePage()
	ae := newJevExecutor(t, page, true, 0.5)
	step := StepDef{ID: "t2_find_input", Type: "find_element", XPath: "//div[@id='a']",
		Alternatives: []string{"#b", "//div[@id='c']"}, Intent: "the chat prompt input box"}
	res, err := ae.stepFindElement(context.Background(), step)
	if err != nil || !res.Success || res.Element.(*markedElement).node != 10 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if srv.Calls() != 1 {
		t.Errorf("calls = %d, want exactly one pick after the whole list failed", srv.Calls())
	}
	if n := len(page.lookups); n != 4 || !markerSelRe.MatchString(page.lookups[3]) {
		t.Errorf("lookups = %v, want 3 selectors then the marker", page.lookups)
	}

	// Exhausted + NONE → today's error, wrapped; still one pick.
	srv.SetHandler(jevtest.Fixed(map[string]string{"target": "NONE"}))
	res, _ = ae.stepFindElement(context.Background(), step)
	if res.Success || !strings.Contains(res.Error.Error(), "no matching element found (tried 3 selectors)") ||
		!strings.Contains(res.Error.Error(), "jev fallback") || srv.Calls() != 2 {
		t.Errorf("res = %+v calls = %d", res, srv.Calls())
	}
}

// ---------------------------------------------------------------------------
// Shipped action JSON: intents
// ---------------------------------------------------------------------------

func TestActionIntentsAreValid(t *testing.T) {
	elementStep := func(s map[string]any) bool {
		typ, _ := s["type"].(string)
		sel, _ := s["selector"].(string)
		xp, _ := s["xpath"].(string)
		return (typ == "find_element" || typ == "click" || typ == "type") && (sel != "" || xp != "")
	}
	count := 0
	err := fs.WalkDir(data.ActionsFS, "actions", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		raw, err := data.ActionsFS.ReadFile(path)
		if err != nil {
			return err
		}
		var def struct {
			Steps []map[string]any `json:"steps"`
		}
		if err := json.Unmarshal(raw, &def); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		hasIntent := false
		for _, s := range def.Steps {
			v, ok := s["intent"]
			if !ok {
				if strings.HasPrefix(path, "actions/gemini/") && elementStep(s) {
					t.Errorf("%s step %v: gemini element step without an intent", path, s["id"])
				}
				continue
			}
			hasIntent = true
			count++
			intent, isStr := v.(string)
			switch {
			case !isStr || strings.TrimSpace(intent) == "":
				t.Errorf("%s step %v: empty intent", path, s["id"])
			case len(intent) > 200:
				t.Errorf("%s step %v: intent is %d chars (max 200)", path, s["id"], len(intent))
			case strings.Contains(intent, "{{"):
				t.Errorf("%s step %v: intents are not templated", path, s["id"])
			case !elementStep(s):
				t.Errorf("%s step %v: intent on a step that does not look up an element", path, s["id"])
			}
		}
		if hasIntent {
			parts := strings.Split(strings.TrimSuffix(path, ".json"), "/") // actions/<platform>/<type>
			def2, err := GetLoader().Load(parts[1], parts[2])
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			n := 0
			for _, s := range def2.Steps {
				if s.Intent != "" {
					n++
				}
			}
			if n == 0 {
				t.Errorf("%s: loader dropped the intents", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("no shipped action declares an intent")
	}
}
