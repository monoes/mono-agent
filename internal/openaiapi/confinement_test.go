package openaiapi

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// loadScanFixture reads testdata/scan-2.22.0.json: four runtimes from a real
// `monomind agent scan --json` on monomind 2.22.0 (paths scrubbed).
func loadScanFixture(t *testing.T) *monomind.ScanResult {
	t.Helper()
	raw, err := os.ReadFile("testdata/scan-2.22.0.json")
	if err != nil {
		t.Fatal(err)
	}
	var res monomind.ScanResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return &res
}

func TestClassifyRuntimeAgainstTheCapturedScan(t *testing.T) {
	scan := loadScanFixture(t)
	caps := monomind.NewCapabilitySet("2.22.0", monomind.CapAgentExecSandbox)

	want := map[string]Class{
		"claude":      ChatOnly,   // monomind's allow-list gate is the only tool gate
		"codex":       Sandboxed,  // lists workspace-write, so Exec applies it
		"antigravity": Unconfined, // lists only restricted and full: no sandbox applies
		"hermes":      Unconfined,
	}
	for id, w := range want {
		e := scan.Find(id)
		if e == nil {
			t.Fatalf("fixture lacks %s", id)
		}
		if got := ClassifyRuntime(*e, caps); got != w {
			t.Errorf("%s: class %v, want %v", id, got, w)
		}
	}
}

func TestClassifyRuntimeFailsClosed(t *testing.T) {
	scan := loadScanFixture(t)
	codex, claude := *scan.Find("codex"), *scan.Find("claude")

	// A monomind that predates the sandbox capability: codex still gets its
	// sandbox from the env path.
	if got := ClassifyRuntime(codex, monomind.NewCapabilitySet("2.20.0")); got != Sandboxed {
		t.Errorf("codex on a 2.20.0 handshake: %v, want Sandboxed", got)
	}
	// No handshake at all: nothing can be vouched for.
	if got := ClassifyRuntime(codex, nil); got != Unconfined {
		t.Errorf("codex with no handshake: %v, want Unconfined", got)
	}
	// A scan that doesn't report native_sandbox can't prove claude is chat-only.
	claude.NativeSandbox = ""
	if got := ClassifyRuntime(claude, monomind.NewCapabilitySet("2.20.0")); got == ChatOnly {
		t.Errorf("claude without native_sandbox must not be trusted as chat-only")
	}
}

func TestClassFromNativeSandbox(t *testing.T) {
	for native, want := range map[string]Class{
		"monomind":        ChatOnly,
		"workspace-write": Sandboxed,
		"read-only":       Sandboxed,
		"restricted":      Unconfined, // the CLI's own approval rules, not a boundary
		"full":            Unconfined,
		"none":            Unconfined,
		"":                Unconfined,
		"something-new":   Unconfined,
	} {
		if got := ClassFromNativeSandbox(native); got != want {
			t.Errorf("ClassFromNativeSandbox(%q) = %v, want %v", native, got, want)
		}
	}
}

func TestPolicy(t *testing.T) {
	for in, want := range map[string]Class{"chat-only": ChatOnly, "sandboxed": Sandboxed, "any": Unconfined} {
		p, err := ParsePolicy(in)
		if err != nil || p.Max != want || p.String() != in {
			t.Errorf("ParsePolicy(%q) = %+v, %v", in, p, err)
		}
	}
	if _, err := ParsePolicy("everything"); err == nil {
		t.Error("an unknown confinement level must be rejected")
	}

	chat, sand, any := Policy{Max: ChatOnly}, Policy{Max: Sandboxed}, Policy{Max: Unconfined}
	for _, c := range []struct {
		p    Policy
		cls  Class
		want bool
	}{
		{chat, ChatOnly, true}, {chat, Sandboxed, false}, {chat, Unconfined, false},
		{sand, ChatOnly, true}, {sand, Sandboxed, true}, {sand, Unconfined, false},
		{any, ChatOnly, true}, {any, Sandboxed, true}, {any, Unconfined, true},
	} {
		if got := c.p.Allows(c.cls); got != c.want {
			t.Errorf("%s allows %v = %v, want %v", c.p, c.cls, got, c.want)
		}
	}

	// A class or a policy nobody set must fail closed, never open.
	var unset Class
	if any.Allows(unset) {
		t.Error("the zero Class must be allowed by no policy")
	}
	if (Policy{}).Allows(ChatOnly) || (Policy{}).String() != "none" {
		t.Errorf("the zero Policy must serve nothing: %q", Policy{})
	}
}

// A key created with --context is held to chat-only unless the operator raised
// the context maximum, and that maximum never raises what the listener serves.
func TestPolicyForContextKeyIsCappedByTheContextMax(t *testing.T) {
	for _, c := range []struct{ listener, contextMax, want Class }{
		{Unconfined, 0, ChatOnly}, // nothing set: chat-only
		{Sandboxed, 0, ChatOnly},
		{ChatOnly, 0, ChatOnly},
		{Unconfined, ChatOnly, ChatOnly},
		{Unconfined, Sandboxed, Sandboxed},
		{Unconfined, Unconfined, Unconfined},
		{Sandboxed, Unconfined, Sandboxed}, // never above the listener
		{ChatOnly, Unconfined, ChatOnly},
	} {
		p := Policy{Max: c.listener, ContextMax: c.contextMax}
		if got := p.ForContextKey().Max; got != c.want {
			t.Errorf("a %s listener with context maximum %d serves a context key up to %v, want %v", p, c.contextMax, got, c.want)
		}
	}
	if (Policy{Max: Unconfined}).ForContextKey().Allows(Sandboxed) {
		t.Error("by default a context key must not reach a sandboxed runtime")
	}
	if !(Policy{Max: Unconfined, ContextMax: Sandboxed}).ForContextKey().Allows(Sandboxed) {
		t.Error("with the context maximum raised, a context key may reach a sandboxed runtime")
	}
	if (Policy{}).ForContextKey().Allows(ChatOnly) {
		t.Error("the zero policy serves nothing, for a context key too")
	}
}

// The auto model picks among chat-only models unless the operator raised the auto
// maximum, because a prompt can steer the pick; the maximum never raises what the
// listener serves, nor what a context key may use.
func TestPolicyForAutoIsCappedByTheAutoMax(t *testing.T) {
	for _, c := range []struct{ listener, autoMax, want Class }{
		{Unconfined, 0, ChatOnly}, // nothing set: chat-only
		{Sandboxed, 0, ChatOnly},
		{ChatOnly, 0, ChatOnly},
		{Unconfined, ChatOnly, ChatOnly},
		{Unconfined, Sandboxed, Sandboxed},
		{Unconfined, Unconfined, Unconfined},
		{Sandboxed, Unconfined, Sandboxed}, // never above the listener
		{ChatOnly, Unconfined, ChatOnly},
	} {
		p := Policy{Max: c.listener, AutoMax: c.autoMax}
		if got := p.ForAuto().Max; got != c.want {
			t.Errorf("a %s listener with auto maximum %d lets auto pick up to %v, want %v", p, c.autoMax, got, c.want)
		}
	}
	// A context key keeps its own cap through auto, whatever the auto maximum.
	ctx := Policy{Max: Unconfined, ContextMax: ChatOnly, AutoMax: Unconfined}
	if got := ctx.ForContextKey().ForAuto().Max; got != ChatOnly {
		t.Errorf("a context key through auto: up to %v, want chat-only", got)
	}
	both := Policy{Max: Unconfined, ContextMax: Sandboxed, AutoMax: Sandboxed}
	if got := both.ForContextKey().ForAuto().Max; got != Sandboxed {
		t.Errorf("both raised to sandboxed: up to %v, want sandboxed", got)
	}
	if (Policy{}).ForAuto().Allows(ChatOnly) {
		t.Error("the zero policy serves nothing, for auto too")
	}
}

func TestDefaultPolicyByBindAddress(t *testing.T) {
	for addr, want := range map[string]Class{
		"127.0.0.1:9322": Unconfined,
		"localhost:9322": Unconfined,
		"[::1]:9322":     Unconfined,
		"0.0.0.0:9443":   ChatOnly,
		":9443":          ChatOnly,
		"10.1.2.3:9443":  ChatOnly,
	} {
		if got := DefaultPolicy(addr).Max; got != want {
			t.Errorf("DefaultPolicy(%q).Max = %v, want %v", addr, got, want)
		}
	}
}

func TestClassString(t *testing.T) {
	for c, want := range map[Class]string{ChatOnly: "chat-only", Sandboxed: "sandboxed", Unconfined: "unconfined"} {
		if c.String() != want {
			t.Errorf("%d.String() = %q, want %q", c, c.String(), want)
		}
	}
}
