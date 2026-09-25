package recordanalyze

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/browser"
)

// fakePage answers the core step handlers from a fixed element set.
type fakePage struct {
	browser.PageInterface
	mu    sync.Mutex
	url   string
	elems map[string]*fakeElem
	typed []string
}

func (p *fakePage) Navigate(u string) error                     { p.url = u; return nil }
func (p *fakePage) WaitLoad() error                             { return nil }
func (p *fakePage) WaitDOMStable(time.Duration) error           { return nil }
func (p *fakePage) WaitIdle(time.Duration) error                { return nil }
func (p *fakePage) GetURL() (string, error)                     { return p.url, nil }
func (p *fakePage) Timeout(time.Duration) browser.PageInterface { return p }
func (p *fakePage) KeyboardType(keys ...rune) error             { p.record(string(keys)); return nil }
func (p *fakePage) InsertText(t string) error                   { p.record(t); return nil }
func (p *fakePage) Eval(string, ...interface{}) (*browser.EvalResult, error) {
	return browser.NewEvalResult(nil), nil
}
func (p *fakePage) Has(sel string) (bool, error) { _, ok := p.elems[sel]; return ok, nil }
func (p *fakePage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	if e, ok := p.elems[sel]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("element not found: %s", sel)
}
func (p *fakePage) ElementX(x string, _ time.Duration) (browser.ElementHandle, error) {
	return nil, fmt.Errorf("element not found: %s", x)
}
func (p *fakePage) Elements(sel string) ([]browser.ElementHandle, error) {
	if e, ok := p.elems[sel]; ok {
		return []browser.ElementHandle{e}, nil
	}
	return nil, nil
}

type fakeElem struct {
	browser.ElementHandle
	page    *fakePage
	name    string
	clicked bool
}

func (e *fakeElem) Click() error                         { e.clicked = true; return nil }
func (e *fakeElem) Focus() error                         { return nil }
func (e *fakeElem) ScrollIntoView() error                { return nil }
func (e *fakeElem) WaitStable(time.Duration) error       { return nil }
func (e *fakeElem) Text() (string, error)                { return e.name, nil }
func (e *fakeElem) Attribute(string) (*string, error)    { return nil, nil }
func (e *fakeElem) Property(string) (interface{}, error) { return "", nil }
func (e *fakeElem) Input(t string) error {
	e.page.mu.Lock()
	e.page.typed = append(e.page.typed, e.name+"="+t)
	e.page.mu.Unlock()
	return nil
}

// TestPageExecSafeModeRealExecutor replays the form draft through the real
// ActionExecutor on a fake page: everything before Save runs, Save does not.
func TestPageExecSafeModeRealExecutor(t *testing.T) {
	dir := formDraft(t)
	page := &fakePage{elems: map[string]*fakeElem{}}
	for sel, name := range map[string]string{
		`[data-testid="contact-email"]`: "email", `input[name="full_name"]`: "name",
		`input[name="password"]`: "password", `[data-testid="contact-save"]`: "save",
	} {
		page.elems[sel] = &fakeElem{page: page, name: name}
	}
	rep, err := Verify(context.Background(), dir, VerifyOptions{Exec: PageExec(page, zerolog.Nop()), Inputs: map[string]any{"account_password": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %+v", rep)
	if page.elems[`[data-testid="contact-save"]`].clicked {
		t.Error("safe mode clicked Save")
	}
	if rep.StoppedAt == nil || rep.StoppedAt.StepID != "save" {
		t.Errorf("stoppedAt = %+v", rep.StoppedAt)
	}
	for _, s := range rep.Steps[:3] {
		if s.Status != StatusPass {
			t.Errorf("step %s = %s (%s)", s.ID, s.Status, s.Message)
		}
	}
	// The masked password reaches the page through {{secret:account_password}}.
	all := strings.Join(page.typed, "|")
	if !strings.Contains(all, "s3cret") || !strings.Contains(all, "jane@example.com") {
		t.Errorf("expected values were not typed (%d entries)", len(page.typed))
	}
	if page.url != "https://app.acme-crm.test/contacts/new" {
		t.Errorf("url = %s", page.url)
	}
}

func (p *fakePage) record(t string) {
	p.mu.Lock()
	p.typed = append(p.typed, t)
	p.mu.Unlock()
}
