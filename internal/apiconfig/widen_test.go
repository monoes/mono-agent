package apiconfig

import (
	"strings"
	"testing"
)

// doc builds Settings from "key=value" words.
func doc(t *testing.T, words ...string) Settings {
	t.Helper()
	var s Settings
	for _, w := range words {
		key, value, _ := strings.Cut(w, "=")
		if err := s.Set(key, value); err != nil {
			t.Fatalf("%q: %v", w, err)
		}
	}
	return s
}

func keysOf(w []Widening) string {
	keys := make([]string, len(w))
	for i, x := range w {
		keys[i] = x.Key
	}
	return strings.Join(keys, ",")
}

type wideningRow struct {
	name          string
	before, after []string
	want          string // the keys, in order, comma-separated
}

// The rules of D39 as the plan reads them (P1: the effective policy of the two documents is
// compared, so an unset below the default counts; P1b: both kinds of listener are judged in
// every document, because the daemon's own environment may name a dedicated listener).
var wideningRows = []wideningRow{
	// (a) the dedicated listener's address
	{"a network address where there was none", nil, []string{"v1_addr=0.0.0.0:9443"}, "v1_addr"},
	{"an address with no host binds every interface", nil, []string{"v1_addr=:9443"}, "v1_addr"},
	{"a host name is not loopback", nil, []string{"v1_addr=example.com:9443"}, "v1_addr"},
	{"a private address is not loopback", nil, []string{"v1_addr=192.168.1.5:9443"}, "v1_addr"},
	{"loopback where there was none", nil, []string{"v1_addr=127.0.0.1:9443"}, ""},
	{"localhost", nil, []string{"v1_addr=localhost:9443"}, ""},
	{"ipv6 loopback", nil, []string{"v1_addr=[::1]:9443"}, ""},
	{"the rest of 127.0.0.0/8", nil, []string{"v1_addr=127.5.5.5:9443"}, ""},
	{"loopback to a network address", []string{"v1_addr=127.0.0.1:9443"}, []string{"v1_addr=0.0.0.0:9443"}, "v1_addr"},
	{"a network address to another (P9: one interface to all of them is not seen)", []string{"v1_addr=10.0.0.5:9443"}, []string{"v1_addr=0.0.0.0:9443"}, ""},
	{"a network address to the same", []string{"v1_addr=0.0.0.0:9443"}, []string{"v1_addr=0.0.0.0:9443"}, ""},
	{"a network address to loopback", []string{"v1_addr=0.0.0.0:9443"}, []string{"v1_addr=127.0.0.1:9443"}, ""},
	{"unsetting the address", []string{"v1_addr=0.0.0.0:9443"}, nil, ""},
	{"nothing at all", nil, nil, ""},

	// (b) confinement: no default value, and a network listener starts at chat-only
	{"chat-only never widens", nil, []string{"confinement=chat-only"}, ""},
	{"sandboxed raises what a network listener would serve", nil, []string{"confinement=sandboxed"}, "confinement.network"},
	{"any raises what a network listener would serve (the loopback default is already any)", nil, []string{"confinement=any"}, "confinement.network"},
	{"sandboxed to any raises both kinds", []string{"confinement=sandboxed"}, []string{"confinement=any"}, "confinement.loopback,confinement.network"},
	{"any to sandboxed narrows both", []string{"confinement=any"}, []string{"confinement=sandboxed"}, ""},
	{"chat-only to any raises both", []string{"confinement=chat-only"}, []string{"confinement=any"}, "confinement.loopback,confinement.network"},
	{"unsetting chat-only gives loopback back its any (P1)", []string{"confinement=chat-only"}, nil, "confinement.loopback"},
	{"unsetting sandboxed raises loopback and lowers a network listener", []string{"confinement=sandboxed"}, nil, "confinement.loopback"},
	{"unsetting any narrows a network listener and changes loopback not at all", []string{"confinement=any"}, nil, ""},
	{"a named network listener does not change the rule", []string{"v1_addr=0.0.0.0:9443"}, []string{"v1_addr=0.0.0.0:9443", "confinement=any"}, "confinement.network"},

	// (b) context_confinement, after the cap that confinement puts on each kind
	{"context sandboxed raises loopback only: a network listener is capped by its chat-only", nil, []string{"context_confinement=sandboxed"}, "context_confinement.loopback"},
	{"context chat-only is its default", nil, []string{"context_confinement=chat-only"}, ""},
	{"unsetting context narrows", []string{"context_confinement=any"}, nil, ""},
	{"context any under confinement sandboxed is sandboxed on both kinds", []string{"confinement=sandboxed"}, []string{"confinement=sandboxed", "context_confinement=any"}, "context_confinement.loopback,context_confinement.network"},
	{"context any under confinement chat-only is capped at chat-only", []string{"confinement=chat-only"}, []string{"confinement=chat-only", "context_confinement=any"}, ""},
	{"capping context with confinement narrows", []string{"context_confinement=any"}, []string{"context_confinement=any", "confinement=chat-only"}, ""},
	{"confinement sandboxed lifts the cap of a network listener", []string{"context_confinement=any"}, []string{"context_confinement=any", "confinement=sandboxed"}, "confinement.network,context_confinement.network"},

	// (b) auto_confinement
	{"auto any raises loopback only", nil, []string{"auto_confinement=any"}, "auto_confinement.loopback"},
	{"auto sandboxed raises loopback only", nil, []string{"auto_confinement=sandboxed"}, "auto_confinement.loopback"},
	{"auto any under confinement sandboxed is sandboxed on both kinds", []string{"confinement=sandboxed"}, []string{"confinement=sandboxed", "auto_confinement=any"}, "auto_confinement.loopback,auto_confinement.network"},
	{"auto chat-only is its default", nil, []string{"auto_confinement=chat-only"}, ""},
	{"unsetting auto narrows", []string{"auto_confinement=any"}, nil, ""},

	// (c) the runtime lists
	{"a subset of the default list", nil, []string{"image_runtimes=codex"}, ""},
	{"a runtime that is not in the default list", nil, []string{"image_runtimes=codex,claude"}, "image_runtimes"},
	{"an alias of a default runtime", nil, []string{"image_runtimes=agy"}, ""},
	{"spelling does not matter", nil, []string{"image_runtimes= CODEX , claude"}, "image_runtimes"},
	{"none narrows", nil, []string{"image_runtimes=none"}, ""},
	{"leaving none by unsetting (P1)", []string{"image_runtimes=none"}, nil, "image_runtimes"},
	{"leaving none for a list", []string{"image_runtimes=none"}, []string{"image_runtimes=codex"}, "image_runtimes"},
	{"none to none", []string{"image_runtimes=none"}, []string{"image_runtimes=none"}, ""},
	{"gaining a runtime beside one already saved", []string{"image_runtimes=opencode"}, []string{"image_runtimes=opencode,pi"}, "image_runtimes"},
	{"the same custom list", []string{"image_runtimes=opencode"}, []string{"image_runtimes=opencode"}, ""},
	{"a custom list back to the default", []string{"image_runtimes=opencode"}, nil, ""},
	{"tools: a subset of the default list", nil, []string{"tool_runtimes=codex"}, ""},
	{"tools: a runtime that is not in the default list", nil, []string{"tool_runtimes=claude,codex,antigravity"}, "tool_runtimes"},
	{"tools: leaving none by unsetting", []string{"tool_runtimes=none"}, nil, "tool_runtimes"},
	{"tools: leaving none for a list", []string{"tool_runtimes=none"}, []string{"tool_runtimes=claude"}, "tool_runtimes"},
	{"tools: none narrows", nil, []string{"tool_runtimes=none"}, ""},
	{"both lists, images first", nil, []string{"tool_runtimes=pi", "image_runtimes=pi"}, "image_runtimes,tool_runtimes"},

	// what is never exposure
	{"a bigger maximum", nil, []string{"max_concurrent=64"}, ""},
	{"a longer timeout", nil, []string{"turn_timeout=24h"}, ""},
	{"the TLS files", nil, []string{"tls_cert_file=/c.pem", "tls_key_file=/k.pem"}, ""},
	{"values that spell the defaults", nil, []string{"max_concurrent=4", "turn_timeout=10m", "context_confinement=chat-only", "auto_confinement=chat-only", "image_runtimes=codex,antigravity", "tool_runtimes=claude,codex"}, ""},

	// several at once, in the order of the settings
	{"everything at once", nil, []string{"v1_addr=0.0.0.0:9443", "confinement=any", "context_confinement=any", "image_runtimes=codex,claude"},
		"v1_addr,confinement.network,context_confinement.loopback,context_confinement.network,image_runtimes"},
	{"unsetting all of it, which was below the defaults", []string{"confinement=chat-only", "image_runtimes=none", "tool_runtimes=none"}, nil, "confinement.loopback,image_runtimes,tool_runtimes"},
	{"unsetting all of what was above them", []string{"v1_addr=0.0.0.0:9443", "confinement=any", "context_confinement=any", "auto_confinement=any", "image_runtimes=codex,claude"}, nil, ""},
}

func TestWidens(t *testing.T) {
	for _, r := range wideningRows {
		t.Run(r.name, func(t *testing.T) {
			before, after := doc(t, r.before...), doc(t, r.after...)
			got := Widens(before, after)
			if keysOf(got) != r.want {
				t.Fatalf("Widens = [%s], want [%s]\n%+v", keysOf(got), r.want, got)
			}
			// Nothing changes, nothing widens; and the same document read twice says the same.
			if same := Widens(after, after); len(same) != 0 {
				t.Errorf("a document widens itself: %+v", same)
			}
			if again := Widens(before, after); keysOf(again) != keysOf(got) {
				t.Errorf("not deterministic: %v then %v", keysOf(got), keysOf(again))
			}
		})
	}
}

// A reason is a sentence for a person: one line, and the same set of keys everywhere.
func TestWideningReasonsAreSentences(t *testing.T) {
	known := map[string]bool{
		"v1_addr": true, "confinement.loopback": true, "confinement.network": true,
		"context_confinement.loopback": true, "context_confinement.network": true,
		"auto_confinement.loopback": true, "auto_confinement.network": true, "image_runtimes": true, "tool_runtimes": true,
	}
	seen := map[string]bool{}
	for _, r := range wideningRows {
		for _, w := range Widens(doc(t, r.before...), doc(t, r.after...)) {
			seen[w.Key] = true
			if !known[w.Key] {
				t.Errorf("%s: a key the contract does not list: %q", r.name, w.Key)
			}
			if len(w.Reason) < 30 || !strings.HasSuffix(w.Reason, ".") || strings.ContainsAny(w.Reason, "\n\t") || w.Reason != strings.TrimSpace(w.Reason) ||
				w.Reason[0] < 'A' || w.Reason[0] > 'Z' {
				t.Errorf("%s: %s: reason %q is not one sentence", r.name, w.Key, w.Reason)
			}
		}
	}
	for k := range known {
		if !seen[k] {
			t.Errorf("no row of the table produces %s", k)
		}
	}
}

func TestWideningReasonsSayWhatChanges(t *testing.T) {
	has := func(w []Widening, key string, parts ...string) {
		t.Helper()
		for _, x := range w {
			if x.Key != key {
				continue
			}
			for _, p := range parts {
				if !strings.Contains(x.Reason, p) {
					t.Errorf("%s: %q does not say %q", key, x.Reason, p)
				}
			}
			return
		}
		t.Errorf("no %s in %+v", key, w)
	}
	w := Widens(Settings{}, doc(t, "v1_addr=0.0.0.0:9443", "confinement=sandboxed"))
	has(w, "v1_addr", "0.0.0.0:9443", "beyond this machine", "serve runtimes up to sandboxed")
	has(w, "confinement.network", "beyond this machine", "serve runtimes up to sandboxed, where it served up to chat-only")
	w = Widens(doc(t, "confinement=chat-only"), Settings{})
	has(w, "confinement.loopback", "on this machine", "serve runtimes up to any, where it served up to chat-only")
	w = Widens(Settings{}, doc(t, "context_confinement=sandboxed", "auto_confinement=any"))
	has(w, "context_confinement.loopback", "--context", "up to sandboxed on the /v1 listener on this machine, where it could use up to chat-only")
	has(w, "auto_confinement.loopback", "auto model", "up to any on the /v1 listener on this machine, where it could pick up to chat-only")
	w = Widens(doc(t, "confinement=sandboxed"), doc(t, "confinement=sandboxed", "context_confinement=any"))
	has(w, "context_confinement.network", "up to sandboxed on a /v1 listener beyond this machine")
	w = Widens(Settings{}, doc(t, "image_runtimes=codex,claude"))
	has(w, "image_runtimes", "claude", "default")
	w = Widens(doc(t, "tool_runtimes=none"), Settings{})
	has(w, "tool_runtimes", "switched off", "claude")
}

// Invalid text (a document edited by hand) is read as not saved: the gate is not the place
// that refuses it, and it must not panic.
func TestWidensReadsInvalidTextAsNotSaved(t *testing.T) {
	bad := doc(t, "confinement=everything", "context_confinement=x", "auto_confinement=y", "image_runtimes=co dex", "tool_runtimes=a,,b", "v1_addr=nonsense")
	if got := Widens(bad, Settings{}); len(got) != 0 {
		t.Errorf("invalid to nothing: %+v", got)
	}
	if got := Widens(Settings{}, bad); len(got) != 0 {
		t.Errorf("nothing to invalid: %+v", got)
	}
}
