package action

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Fakes: a non-Rod page with a tiny DOM, optionally exposing EvalCDP.
// ---------------------------------------------------------------------------

type domNode struct {
	text  string
	attrs map[string]string
	html  string
}

type domElement struct {
	browser.ElementHandle
	n *domNode
}

func (e *domElement) Text() (string, error) { return e.n.text, nil }
func (e *domElement) Attribute(name string) (*string, error) {
	if v, ok := e.n.attrs[name]; ok {
		return &v, nil
	}
	return nil, nil
}
func (e *domElement) HTML() (string, error) { return e.n.html, nil }

// domPage is a non-Rod page WITHOUT EvalCDP. Elements/Element/Race resolve
// CSS selectors of the form "#id" or `[attr="value"]` against the DOM.
type domPage struct {
	browser.PageInterface
	nodes      []*domNode       // document order
	xpaths     map[string][]int // xpath -> matching node indices (document order)
	byID       map[string]int   // "#id" -> node index
	raceCalls  [][]string       // selectors passed to Race
	elemCalls  []string         // selectors passed to Element/ElementX
	appearAt   map[string]int   // selector -> Element call count at which it appears
	elemCount  map[string]int
	outcomeSel map[string]bool // selectors Race treats as present
}

var attrSelRe = regexp.MustCompile(`^\[([\w-]+)="([^"]*)"\]$`)

func (p *domPage) lookup(sel string) *domNode {
	if i, ok := p.byID[sel]; ok {
		if at, gated := p.appearAt[sel]; gated && p.elemCount[sel] < at {
			return nil
		}
		return p.nodes[i]
	}
	if idx, ok := p.xpaths[sel]; ok && len(idx) > 0 {
		if at, gated := p.appearAt[sel]; gated && p.elemCount[sel] < at {
			return nil
		}
		return p.nodes[idx[0]]
	}
	return nil
}

func (p *domPage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	p.elemCalls = append(p.elemCalls, sel)
	if p.elemCount == nil {
		p.elemCount = map[string]int{}
	}
	p.elemCount[sel]++
	if n := p.lookup(sel); n != nil {
		return &domElement{n: n}, nil
	}
	return nil, fmt.Errorf("element not found: %s", sel)
}

func (p *domPage) ElementX(xp string, t time.Duration) (browser.ElementHandle, error) {
	return p.Element(xp, t)
}

func (p *domPage) Elements(sel string) ([]browser.ElementHandle, error) {
	m := attrSelRe.FindStringSubmatch(sel)
	if m == nil {
		return nil, nil
	}
	var out []browser.ElementHandle
	for _, n := range p.nodes {
		if n.attrs[m[1]] == m[2] {
			out = append(out, &domElement{n: n})
		}
	}
	return out, nil
}

func (p *domPage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	p.raceCalls = append(p.raceCalls, sels)
	for i, s := range sels {
		if p.outcomeSel[s] {
			return i, &domElement{n: &domNode{text: s}}, nil
		}
		if n := p.lookup(s); n != nil {
			return i, &domElement{n: n}, nil
		}
	}
	return -1, nil, errors.New("None of the selectors matched")
}

func (p *domPage) WaitLoad() error { return nil }

// cdpDomPage adds EvalCDP, interpreting the two scripts elementsByXPath sends.
type cdpDomPage struct {
	domPage
	evals    []string
	cleanups int
	evalErr  error
}

var (
	markXPathRe  = regexp.MustCompile(`document\.evaluate\(("(?:[^"\\]|\\.)*"),`)
	markTokenRe  = regexp.MustCompile(`setAttribute\("data-monoagent-xpath", "([0-9a-f]+)"\)`)
	cleanTokenRe = regexp.MustCompile(`querySelectorAll\("\[data-monoagent-xpath=\\"([0-9a-f]+)\\"\]"\)`)
)

func (p *cdpDomPage) EvalCDP(js string) (interface{}, error) {
	p.evals = append(p.evals, js)
	if p.evalErr != nil {
		return nil, p.evalErr
	}
	if m := cleanTokenRe.FindStringSubmatch(js); m != nil {
		p.cleanups++
		for _, n := range p.nodes {
			if n.attrs[xpathMarkAttr] == m[1] {
				delete(n.attrs, xpathMarkAttr)
			}
		}
		return true, nil
	}
	xm := markXPathRe.FindStringSubmatch(js)
	tm := markTokenRe.FindStringSubmatch(js)
	if xm == nil || tm == nil {
		return nil, fmt.Errorf("unexpected script: %s", js)
	}
	xp := strings.Trim(xm[1], `"`)
	idx := p.xpaths[xp]
	for _, i := range idx {
		p.nodes[i].attrs[xpathMarkAttr] = tm[1]
	}
	return float64(len(idx)), nil
}

func newDOM() domPage {
	nodes := []*domNode{
		{text: "Alice", attrs: map[string]string{"href": "/alice"}},
		{text: "noise", attrs: map[string]string{}},
		{text: "Bob card", attrs: map[string]string{}, html: `<div class="card"><span>Bob</span><a href="/bob">Bob</a><a href="/other">x</a></div>`},
		{text: "Carol", attrs: map[string]string{"href": "https://x.example/carol"}},
	}
	return domPage{
		nodes:  nodes,
		xpaths: map[string][]int{"//div[@data-testid='UserCell']": {0, 2, 3}},
		byID:   map[string]int{"#alt": 3},
	}
}

func newLookupExecutor(page browser.PageInterface) *ActionExecutor {
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "lookup-test"}
	ae.execCtx.CurrentURL = "https://x.example/home"
	return ae
}

// ---------------------------------------------------------------------------
// 1 + 2: extract_multiple with an XPath on a non-Rod page
// ---------------------------------------------------------------------------

func TestExtractMultipleXPathOnCDPPage(t *testing.T) {
	page := &cdpDomPage{domPage: newDOM()}
	ae := newLookupExecutor(page)

	res, err := ae.stepExtractMultiple(context.Background(), StepDef{
		ID: "extract_users", Type: "extract_multiple", XPath: "//div[@data-testid='UserCell']",
	})
	if err != nil || !res.Success {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	items := res.Data.([]map[string]interface{})
	var got []string
	for _, it := range items {
		got = append(got, fmt.Sprintf("%v|%v", it["text"], it["href"]))
	}
	want := []string{
		"Alice|https://x.example/alice",
		"Bob card|https://x.example/bob", // child <a href> fallback, generic path
		"Carol|https://x.example/carol",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("items = %v, want %v", got, want)
	}
	if page.cleanups != 1 {
		t.Errorf("cleanups = %d, want 1", page.cleanups)
	}
	for i, n := range page.nodes {
		if _, ok := n.attrs[xpathMarkAttr]; ok {
			t.Errorf("node %d still carries the xpath marker", i)
		}
	}
}

func TestExtractMultipleXPathNoMatchIsEmptySuccess(t *testing.T) {
	page := &cdpDomPage{domPage: newDOM()}
	ae := newLookupExecutor(page)
	res, _ := ae.stepExtractMultiple(context.Background(), StepDef{ID: "x", Type: "extract_multiple", XPath: "//nothing"})
	if !res.Success || len(res.Data.([]map[string]interface{})) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestExtractMultipleXPathEvalErrorFails(t *testing.T) {
	page := &cdpDomPage{domPage: newDOM(), evalErr: errors.New("extension error: eval_cdp: bad xpath")}
	ae := newLookupExecutor(page)
	res, _ := ae.stepExtractMultiple(context.Background(), StepDef{ID: "x", Type: "extract_multiple", XPath: "//div["})
	if res.Success || !strings.Contains(res.Error.Error(), "bad xpath") {
		t.Fatalf("res = %+v", res)
	}
}

func TestExtractMultipleXPathUnsupportedPageErrors(t *testing.T) {
	dom := newDOM()
	page := &dom // no EvalCDP
	ae := newLookupExecutor(page)
	res, err := ae.stepExtractMultiple(context.Background(), StepDef{
		ID: "extract_users", Type: "extract_multiple", XPath: "//div[@data-testid='UserCell']",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !errors.Is(res.Error, errXPathMultiUnsupported) {
		t.Fatalf("res = %+v, want an unsupported-driver failure", res)
	}

	// A CSS alternative that matches nothing must not mask it either.
	res, _ = ae.stepExtractMultiple(context.Background(), StepDef{
		ID: "extract_users", Type: "extract_multiple", XPath: "//div[@data-testid='UserCell']",
		Alternatives: []string{".none"},
	})
	if res.Success || !errors.Is(res.Error, errXPathMultiUnsupported) {
		t.Fatalf("res = %+v, want an unsupported-driver failure", res)
	}
}

// ---------------------------------------------------------------------------
// 3: alternatives honoured on a non-Rod page
// ---------------------------------------------------------------------------

func TestResolveElementAlternativesOnNonRodPage(t *testing.T) {
	dom := newDOM()
	ae := newLookupExecutor(&dom)
	el, err := ae.resolveElement(StepDef{ID: "c", Type: "click", Selector: "#missing", Alternatives: []string{"#alt"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := el.Text(); txt != "Carol" {
		t.Fatalf("got %q, want the alternative (Carol)", txt)
	}
	if len(dom.raceCalls) != 1 || strings.Join(dom.raceCalls[0], " ") != "#missing #alt" {
		t.Fatalf("race calls = %v", dom.raceCalls)
	}
}

func TestResolveElementPrimaryWinsOverAlternative(t *testing.T) {
	dom := newDOM()
	dom.byID["#primary"] = 0
	ae := newLookupExecutor(&dom)
	el, err := ae.resolveElement(StepDef{ID: "c", Type: "click", Selector: "#primary", Alternatives: []string{"#alt"}})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := el.Text(); txt != "Alice" {
		t.Fatalf("got %q, want the primary (Alice)", txt)
	}
}

func TestResolveElementXPathAlternativeIsPolled(t *testing.T) {
	dom := newDOM()
	dom.xpaths["//a[@id='late']"] = []int{3}
	dom.appearAt = map[string]int{"//a[@id='late']": 2} // appears on the 2nd probe
	ae := newLookupExecutor(&dom)
	el, err := ae.resolveElement(StepDef{ID: "c", Type: "click", Selector: "#missing",
		Alternatives: []string{"//a[@id='late']"}, Timeout: 3})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := el.Text(); txt != "Carol" {
		t.Fatalf("got %q", txt)
	}
}

func TestResolveElementAlternativesAllMissing(t *testing.T) {
	dom := newDOM()
	ae := newLookupExecutor(&dom)
	el, err := ae.resolveElement(StepDef{ID: "c", Type: "click", Selector: "#missing", Alternatives: []string{"#gone"}})
	if el != nil || err == nil || !strings.Contains(err.Error(), "1 alternatives") {
		t.Fatalf("el = %v err = %v", el, err)
	}
}

func TestFindElementStepAlternativesOnNonRodPage(t *testing.T) {
	dom := newDOM()
	ae := newLookupExecutor(&dom)
	res, _ := ae.stepFindElement(context.Background(), StepDef{ID: "f", Type: "find_element", Selector: "#missing", Alternatives: []string{"#alt"}})
	if !res.Success {
		t.Fatalf("res = %+v", res)
	}
	if ae.execCtx.GetElement("f") == nil {
		t.Fatal("element not stored")
	}
}

// ---------------------------------------------------------------------------
// 4: race wait after click on a non-Rod page
// ---------------------------------------------------------------------------

func TestWaitAfterClickRaceOnNonRodPage(t *testing.T) {
	dom := newDOM()
	dom.outcomeSel = map[string]bool{"[data-testid='toast']": true}
	ae := newLookupExecutor(&dom)
	ae.waitAfterClick(StepDef{ID: "click_post", WaitAfter: "race", RaceSelectors: map[string]string{
		"success": "[data-testid='toast']",
		"error":   "[role='alert']",
	}})
	if len(dom.raceCalls) != 1 {
		t.Fatalf("race calls = %v, want one race", dom.raceCalls)
	}
	// Ordered by label: error, success.
	if strings.Join(dom.raceCalls[0], " ") != "[role='alert'] [data-testid='toast']" {
		t.Fatalf("race selectors = %v", dom.raceCalls[0])
	}

	label, err := waitForOutcomes(&dom, map[string]string{"success": "[data-testid='toast']", "error": "[role='alert']"}, time.Second)
	if err != nil || label != "success" {
		t.Fatalf("label = %q err = %v", label, err)
	}
}

// ---------------------------------------------------------------------------
// 6: call_bot_method list results become extracted items (once)
// ---------------------------------------------------------------------------

type listBotAdapter struct{ result interface{} }

func (l *listBotAdapter) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	return func(ctx context.Context, args ...interface{}) (interface{}, error) { return l.result, nil }, true
}

func TestCallBotMethodSliceResultBecomesItems(t *testing.T) {
	for name, result := range map[string]interface{}{
		"maps":       []map[string]interface{}{{"url": "a"}, {"url": "b"}},
		"interfaces": []interface{}{map[string]interface{}{"url": "a"}, "skip", map[string]interface{}{"url": "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			ae := NewActionExecutor(context.Background(), nil, nil, nil, nil, &listBotAdapter{result: result}, zerolog.Nop())
			ae.action = &StorageAction{ID: "bot-list"}
			db := &fakeDB{}
			ae.db = db

			res, _ := ae.stepCallBotMethod(context.Background(), StepDef{ID: "t1", Type: "call_bot_method", MethodName: "list_user_posts", VariableName: "posts"})
			if !res.Success {
				t.Fatalf("res = %+v", res)
			}
			if got := len(ae.execCtx.ExtractedItems); got != 2 {
				t.Fatalf("extracted = %d, want 2", got)
			}
			// save_data over the same variable must not add them again.
			if res, _ := ae.stepSaveData(context.Background(), StepDef{ID: "save", Type: "save_data", DataSource: "posts"}); !res.Success {
				t.Fatalf("save res = %+v", res)
			}
			if got := len(ae.execCtx.ExtractedItems); got != 2 || db.savedRows != 2 {
				t.Fatalf("extracted = %d saved = %d, want 2 and 2", got, db.savedRows)
			}
		})
	}
}

func TestWaitStepHonoursAlternatives(t *testing.T) {
	dom := newDOM()
	ae := newLookupExecutor(&dom)
	res, _ := ae.stepWait(context.Background(), StepDef{ID: "w", Type: "wait", Selector: "#missing", Alternatives: []string{"#alt"}})
	if !res.Success || res.Element == nil {
		t.Fatalf("res = %+v", res)
	}
}
