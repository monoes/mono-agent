package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// extPage is a scriptable page for the extended steps: EvalCDP answers
// through eval (the evalJS path), Has through has, GetURL through url.
type extPage struct {
	browser.PageInterface
	mu      sync.Mutex
	url     string
	urlFn   func() string
	has     func(sel string) bool
	eval    func(js string) (interface{}, error)
	evals   []string
	elems   map[string]*extElem
	pressed []rune
}

func (p *extPage) EvalCDP(js string) (interface{}, error) {
	p.mu.Lock()
	p.evals = append(p.evals, js)
	fn := p.eval
	p.mu.Unlock()
	if fn == nil {
		return nil, errors.New("no eval handler")
	}
	return fn(js)
}

func (p *extPage) GetURL() (string, error) {
	if p.urlFn != nil {
		return p.urlFn(), nil
	}
	return p.url, nil
}

func (p *extPage) Has(sel string) (bool, error) {
	if p.has == nil {
		return false, nil
	}
	return p.has(sel), nil
}

func (p *extPage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	if e, ok := p.elems[sel]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("element not found: %s", sel)
}

func (p *extPage) ElementX(sel string, t time.Duration) (browser.ElementHandle, error) {
	return p.Element(sel, t)
}

func (p *extPage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	for i, s := range sels {
		if e, ok := p.elems[s]; ok {
			return i, e, nil
		}
	}
	return -1, nil, errors.New("none matched")
}

func (p *extPage) KeyboardPress(r rune) error {
	p.pressed = append(p.pressed, r)
	return nil
}

func (p *extPage) Timeout(time.Duration) browser.PageInterface { return p }

func (p *extPage) lastEval() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.evals) == 0 {
		return ""
	}
	return p.evals[len(p.evals)-1]
}

// cdpPage adds the raw CDP relay (the extension page's key path).
type cdpPage struct {
	extPage
	calls []map[string]interface{}
}

func (p *cdpPage) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	c := map[string]interface{}{"method": method}
	for k, v := range params {
		c[k] = v
	}
	p.calls = append(p.calls, c)
	return map[string]interface{}{}, nil
}

type extElem struct {
	browser.ElementHandle
	focused  int
	focusErr error
}

func (e *extElem) Focus() error {
	e.focused++
	return e.focusErr
}

// js returns v the way the page hands back evalJS results: a JSON string.
func jsOut(v interface{}) interface{} {
	b, _ := json.Marshal(v)
	return string(b)
}

// extPkg is an in-memory PackageContext.
type extPkg struct {
	id        string
	domains   []string
	fragments map[string]*FragmentDef
	scripts   map[string]string
	actions   map[string]*ActionDef
	other     map[string]*extPkg // "<automation>" → package, for "x.y" refs
}

func (p *extPkg) ID() string                             { return p.id }
func (p *extPkg) StartURL() string                       { return "" }
func (p *extPkg) Domains() []string                      { return p.domains }
func (p *extPkg) PermittedSteps() []string               { return nil }
func (p *extPkg) Selector(string) (*SelectorEntry, bool) { return nil, false }
func (p *extPkg) Fragment(name string) (*FragmentDef, error) {
	if f, ok := p.fragments[name]; ok {
		return f, nil
	}
	return nil, fmt.Errorf("fragment %q not found", name)
}
func (p *extPkg) Script(name string) (string, error) {
	if s, ok := p.scripts[name]; ok {
		return s, nil
	}
	return "", fmt.Errorf("script %q not found", name)
}
func (p *extPkg) ResolveAction(ref string) (*ActionDef, PackageContext, error) {
	if auto, name, ok := strings.Cut(ref, "."); ok {
		o, found := p.other[auto]
		if !found {
			return nil, nil, fmt.Errorf("automation %q not installed", auto)
		}
		return o.ResolveAction(name)
	}
	if a, ok := p.actions[ref]; ok {
		return a, p, nil
	}
	return nil, nil, fmt.Errorf("action %q not found", ref)
}

func newExtExecutor(t *testing.T, page browser.PageInterface) *ActionExecutor {
	t.Helper()
	ae := NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	ae.action = &StorageAction{ID: "act-1", Type: "test", TargetPlatform: "testsite"}
	ae.actionDef = &ActionDef{}
	return ae
}

// run executes one step the way executeSteps does (templates resolved).
func runExt(t *testing.T, ae *ActionExecutor, step StepDef) *StepResult {
	t.Helper()
	h, ok := ae.handlers[step.Type]
	if !ok {
		t.Fatalf("no handler for %q", step.Type)
	}
	res, err := h(context.Background(), ae.resolver.ResolveStepDef(step))
	if err != nil {
		t.Fatalf("handler returned error (want errors in the result): %v", err)
	}
	return res
}

func wantFail(t *testing.T, res *StepResult, substr string) {
	t.Helper()
	if res.Success {
		t.Fatalf("want failure containing %q, got success (data %v)", substr, res.Data)
	}
	if res.Error == nil || !strings.Contains(res.Error.Error(), substr) {
		t.Fatalf("want error containing %q, got %v", substr, res.Error)
	}
}

func wantOK(t *testing.T, res *StepResult) {
	t.Helper()
	if !res.Success {
		t.Fatalf("want success, got error %v", res.Error)
	}
}

func getVar(ae *ActionExecutor, name string) interface{} {
	v, _ := ae.execCtx.GetVariable(name)
	return v
}
