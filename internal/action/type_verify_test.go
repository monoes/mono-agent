package action

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// fieldElem is an input whose value is read through Property("value").
type fieldElem struct {
	browser.ElementHandle
	value      string
	inputWorks bool
	unreadable bool
	focused    bool
}

func (e *fieldElem) ElementID() string { return "el-1" }
func (e *fieldElem) Click() error      { return nil }
func (e *fieldElem) Focus() error      { e.focused = true; return nil }
func (e *fieldElem) Input(t string) error {
	if e.inputWorks {
		e.value += t
	}
	return nil
}
func (e *fieldElem) Property(name string) (interface{}, error) {
	if e.unreadable {
		return nil, errors.New("no property access")
	}
	return e.value, nil
}
func (e *fieldElem) Text() (string, error) {
	if e.unreadable {
		return "", errors.New("detached")
	}
	return "", nil
}

// typerPage is a CDP-typing page. cdpType decides what TypeCDPOnElement
// does to the field (nil: silently nothing); insertWorks makes InsertText
// type into the focused field.
type typerPage struct {
	browser.PageInterface
	el          *fieldElem
	cdpType     func(el *fieldElem, text string)
	insertWorks bool
	inserts     int
}

func (p *typerPage) Element(string, time.Duration) (browser.ElementHandle, error) { return p.el, nil }
func (p *typerPage) TypeCDP(string) error                                         { return nil }
func (p *typerPage) TypeCDPOnElement(text, _ string) error {
	if p.cdpType != nil {
		p.cdpType(p.el, text)
	}
	return nil
}
func (p *typerPage) InsertText(text string) error {
	p.inserts++
	if p.insertWorks && p.el.focused {
		p.el.value += text
	}
	return nil
}

func runType(t *testing.T, page *typerPage, text string) *StepResult {
	t.Helper()
	ae := newPkgExecutor(page, nil)
	res, err := ae.stepType(context.Background(), StepDef{ID: "t", Type: "type", Selector: "#f", Value: text})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestTypeVerifiesTextLanded(t *testing.T) {
	// Typer works: passes, no fallback.
	p := &typerPage{el: &fieldElem{}, cdpType: func(e *fieldElem, s string) { e.value += s }}
	if res := runType(t, p, "hello"); !res.Success || p.inserts != 0 {
		t.Fatalf("working typer: %+v inserts=%d", res, p.inserts)
	}

	// Typer silently does nothing; focus+InsertText lands it.
	p = &typerPage{el: &fieldElem{}, insertWorks: true}
	if res := runType(t, p, "hello"); !res.Success || p.el.value != "hello" {
		t.Fatalf("insert fallback: %+v value=%q", res, p.el.value)
	}

	// InsertText does nothing either; element Input lands it.
	p = &typerPage{el: &fieldElem{inputWorks: true}}
	if res := runType(t, p, "hello"); !res.Success || p.el.value != "hello" {
		t.Fatalf("input fallback: %+v value=%q", res, p.el.value)
	}

	// Nothing works: the step fails, without the text in the error.
	p = &typerPage{el: &fieldElem{}}
	res := runType(t, p, "s3cret-pass")
	if res.Success || !errors.Is(res.Error, errTypedTextMissing) || strings.Contains(res.Error.Error(), "s3cret") {
		t.Fatalf("silent typer: %+v", res)
	}
}

func TestTypeVerifyLenient(t *testing.T) {
	// A phone mask reformats the digits.
	p := &typerPage{el: &fieldElem{}, cdpType: func(e *fieldElem, s string) { e.value = "(555) 123-4567" }}
	if res := runType(t, p, "5551234567"); !res.Success {
		t.Fatalf("phone mask: %+v", res)
	}
	// An autocomplete replaces the text with something else, non-empty.
	p = &typerPage{el: &fieldElem{}, cdpType: func(e *fieldElem, s string) { e.value = "Berlin, Germany" }}
	if res := runType(t, p, "berl"); !res.Success {
		t.Fatalf("autocomplete: %+v", res)
	}
	// An unreadable field (detached after submit) is not a failure.
	p = &typerPage{el: &fieldElem{unreadable: true}}
	if res := runType(t, p, "query"); !res.Success {
		t.Fatalf("unreadable: %+v", res)
	}
	// Pre-filled and unchanged is a failure.
	p = &typerPage{el: &fieldElem{value: "old"}}
	if res := runType(t, p, "new"); res.Success {
		t.Fatal("unchanged pre-filled field passed")
	}
}

func TestTypedTextLandedRule(t *testing.T) {
	cases := []struct {
		before, after, text string
		want                bool
	}{
		{"", "hello", "hello", true},
		{"", "HELLO", "hello", true},
		{"", "4111 1111 1111 1111", "4111111111111111", true},
		{"", "", "hello", false},
		{"x", "x", "hello", false},
		{"", "", "", true},
		{"", "else", "hello", true},
	}
	for _, c := range cases {
		if got := typedTextLanded(c.before, c.after, c.text); got != c.want {
			t.Errorf("typedTextLanded(%q,%q,%q) = %v", c.before, c.after, c.text, got)
		}
	}
}
