// internal/apply/browser_test.go
package apply_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/browser"
)

// fakeBridge is a minimal browser.ExtensionBridge test double — no real
// extension/Chrome connection involved.
type fakeBridge struct {
	connected   bool
	createErr   error
	createdURLs []string
}

func (f *fakeBridge) IsConnected() bool { return f.connected }
func (f *fakeBridge) CreateTab(url string) (int, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.createdURLs = append(f.createdURLs, url)
	return len(f.createdURLs), nil
}
func (f *fakeBridge) CloseTab(tabID int) error                { return nil }
func (f *fakeBridge) NewPage(tabID int) browser.PageInterface { return nil }

// TestOpenForApplicationRequiresConnectedExtension is a regression test:
// OpenForApplication must never fall back to launching a local
// Rod/Chromium browser when the extension isn't connected — see
// internal/browser.HybridSessionProvider for the same invariant elsewhere.
func TestOpenForApplicationRequiresConnectedExtension(t *testing.T) {
	if err := apply.OpenForApplication(context.Background(), "https://example.com/jobs/1", nil); err == nil {
		t.Fatal("expected an error with a nil bridge, got nil")
	}
	disconnected := &fakeBridge{connected: false}
	err := apply.OpenForApplication(context.Background(), "https://example.com/jobs/1", disconnected)
	if err == nil {
		t.Fatal("expected an error with a disconnected bridge, got nil")
	}
	if !strings.Contains(err.Error(), "extension") {
		t.Fatalf("expected the error to mention the extension, got: %v", err)
	}
	if len(disconnected.createdURLs) != 0 {
		t.Fatal("CreateTab must not be called when the bridge isn't connected")
	}
}

// TestOpenForApplicationUsesExtensionBridge verifies the happy path opens
// jobURL as a new tab through the bridge — no browser process spawned.
func TestOpenForApplicationUsesExtensionBridge(t *testing.T) {
	connected := &fakeBridge{connected: true}
	if err := apply.OpenForApplication(context.Background(), "https://example.com/jobs/1", connected); err != nil {
		t.Fatalf("OpenForApplication: %v", err)
	}
	if len(connected.createdURLs) != 1 || connected.createdURLs[0] != "https://example.com/jobs/1" {
		t.Fatalf("expected CreateTab called once with the job URL, got: %v", connected.createdURLs)
	}
}

func TestOpenForApplicationPropagatesCreateTabError(t *testing.T) {
	connected := &fakeBridge{connected: true, createErr: errors.New("boom")}
	err := apply.OpenForApplication(context.Background(), "https://example.com/jobs/1", connected)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the CreateTab error to propagate, got: %v", err)
	}
}

// TestBrowserFileNeverClicksAnything is a literal source-grep, not a
// behavioral test: it asserts that no non-test .go file in this package
// contains any of the substrings a click/submit/DOM-mutation call would
// use. This is the mechanical enforcement of this phase's core safety
// invariant — see
// docs/mastermind/specs/2026-09-05-apply-automation-design.md. It
// deliberately scans every production file in the package (not just
// browser.go by name) so a forbidden call added to apply.go, or to any
// new file later added to this package, is caught too. If this test ever
// needs to change, that is a deliberate, reviewed decision to weaken the
// invariant, not a test to "fix" in passing.
func TestBrowserFileNeverClicksAnything(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/apply directory: %v", err)
	}

	// Trailing "(" on each substring targets the actual method call (not
	// just the word appearing in a doc comment) and avoids matching
	// "Keyboard.Type" against "Keyboard.MustType" style near-misses —
	// verified against the vendored go-rod API's actual DOM-interaction
	// surface (github.com/go-rod/rod: Element.Input/MustInput,
	// Element.MustSelect, Page.Eval/MustEval, Keyboard.MustType/Press,
	// etc.) rather than guessing at method names.
	forbidden := []string{
		"Click(", "MustClick(",
		"Submit(", "MustSubmit(",
		"Input(", "MustInput(",
		"Type(", "MustType(",
		"Select(", "MustSelect(",
		"Eval(", "MustEval(",
		"Press(", "MustPress(",
		"SetValue(", "MustSetValue(",
		"SetFiles(", "MustSetFiles(",
		"InsertText(", "MustInsertText(",
		// go-rod's Mouse.Click is LITERALLY implemented as Down(button,
		// clickCount) followed by Up(button, clickCount) — a Down/Up
		// pair performs a real click even though neither half is spelled
		// "Click(", so both halves must be forbidden too. Scoped to the
		// dotted method-call form (".Down("/".Up(") rather than the bare
		// word so it doesn't false-positive on unrelated identifiers
		// that merely end in "Down"/"Up" (e.g. a hypothetical
		// "shutDown(" or "backoffCountUp(" helper) with no dot before
		// them.
		".Down(", ".Up(",
		// go-rod's convenience methods above are themselves thin
		// wrappers over raw CDP proto commands invoked as
		// `proto.SomeCommand{...}.Call(page)` (e.g.
		// proto.InputDispatchMouseEvent, proto.RuntimeCallFunctionOn for
		// arbitrary JS eval, proto.DOMSetAttributeValue for setting a
		// form field directly) — bypassing the wrapper and driving the
		// protocol directly would dodge every substring above, so both
		// the "proto." package prefix and the ".Call(" invocation
		// suffix are forbidden outright.
		"proto.",
		".Call(",
		// Other real go-rod interaction methods that are literal-prefix
		// supersets the checks above would otherwise miss entirely.
		"Tap(", "MustTap(",
		"InputColor(", "MustInputColor(",
		"InputTime(", "MustInputTime(",
		"SelectText(", "MustSelectText(",
		"SelectAllText(", "MustSelectAllText(",
	}

	// browser.go now opens the tab the human will act in via the
	// MonoAgent extension bridge's CreateTab(url) — a plain HTTP/JSON call,
	// not a raw CDP "proto." invocation — so no exemption is needed here
	// the way the old direct-Rod implementation required one for
	// proto.TargetCreateTarget. Every "proto."/".Call(" occurrence below is
	// now a real hit.
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		text := string(src)
		for _, f := range forbidden {
			if strings.Contains(text, f) {
				t.Fatalf("internal/apply/%s must never call anything resembling %q — found it in the source. This package's browser code must only navigate to a URL and leave the window open for a human.", name, f)
			}
		}
	}
}
