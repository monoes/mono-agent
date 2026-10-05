package mcp

// The exposure gate. A change that makes the API's server reach further than it did is refused by
// api_config_set unless the operator started this MCP server with --allow-api-exposure. The gate is
// apiconfig.Widens (which decides what widens, in the transaction that replaces the row); the tool
// only says whether the operator opened it. A model sets the arguments of a call and nothing else,
// so nothing in them may open it.

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

// widening is a change that reaches further, from a state already saved, and the keys of the
// reasons that the gate gives for it.
type widening struct {
	name  string
	saved []string       // what is saved before (by the CLI, say)
	args  map[string]any // the call
	keys  []string       // the keys of the widening, as a prefix each: confinement. matches both kinds of listener
	via   string         // the command the refusal names: set or unset
}

var wideningChanges = []widening{
	{"a dedicated listener beyond this machine", nil, set(map[string]any{"v1_addr": "0.0.0.0:9443"}), []string{"v1_addr"}, "set"},
	{"a higher confinement class", nil, set(map[string]any{"confinement": "sandboxed"}), []string{"confinement."}, "set"},
	{"confinement any", nil, set(map[string]any{"confinement": "any"}), []string{"confinement."}, "set"},
	{"a key created with context may use a higher class", []string{"confinement=any"}, set(map[string]any{"context_confinement": "sandboxed"}), []string{"context_confinement."}, "set"},
	{"auto may pick a higher class", []string{"confinement=any"}, set(map[string]any{"auto_confinement": "any"}), []string{"auto_confinement."}, "set"},
	{"tool calling by a runtime outside the default list", nil, set(map[string]any{"tool_runtimes": "claude,codex,gemini"}), []string{"tool_runtimes"}, "set"},
	{"image generation switched on again", []string{"image_runtimes=none"}, set(map[string]any{"image_runtimes": "codex"}), []string{"image_runtimes"}, "set"},
	{"a confinement of chat-only removed", []string{"confinement=chat-only"}, unset([]string{"confinement"}), []string{"confinement.loopback"}, "unset"},
	{"image_runtimes none removed", []string{"image_runtimes=none"}, unset([]string{"image_runtimes"}), []string{"image_runtimes"}, "unset"},
	{"everything removed over a confinement of chat-only", []string{"confinement=chat-only", "tool_runtimes=none"}, unset("all"), []string{"confinement.loopback", "tool_runtimes"}, "unset"},
}

// hasKey says whether a key is among the keys of a widening: a want that ends in a dot (confinement.)
// stands for both kinds of listener.
func hasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want || (strings.HasSuffix(want, ".") && strings.HasPrefix(k, want)) {
			return true
		}
	}
	return false
}

func TestAPIConfigSetRefusesAWideningChangeWithoutTheOperatorsFlag(t *testing.T) {
	for _, c := range wideningChanges {
		t.Run(c.name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{})
			f.save(c.saved...)
			before := f.saved()

			_, err := f.call("api_config_set", c.args)
			if err == nil {
				t.Fatal("a widening change was saved without --allow-api-exposure")
			}
			msg := err.Error()
			// Which setting and why, who decides, and what the user can do instead.
			for _, key := range c.keys {
				if !strings.Contains(msg, key) {
					t.Errorf("the refusal does not name %q: %s", key, msg)
				}
			}
			for _, want := range []string{"reach further", "--allow-api-exposure", "MONOAGENT_MCP_ALLOW_API_EXPOSURE", "No argument", "monoagentcli api config " + c.via, "--yes", "desktop app", "nothing was saved"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not say %q: %s", want, msg)
				}
			}
			if !reflect.DeepEqual(f.saved(), before) {
				t.Errorf("a refused change changed what is saved: %+v", f.saved())
			}
			if f.Installer.everything() != "" {
				t.Errorf("a refusal asked the service manager: %q", f.Installer.everything())
			}
		})
	}
}

func TestAPIConfigSetSavesAWideningChangeWhenTheOperatorStartedTheServerWithTheFlag(t *testing.T) {
	for _, c := range wideningChanges {
		t.Run(c.name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{allowExposure: true})
			f.save(c.saved...)
			before := f.saved()

			text := f.mustCall("api_config_set", c.args)
			r := decodeChangeResult(t, text)
			if !r.Applied || len(r.Changed) == 0 {
				t.Fatalf("applied %v, changed %v", r.Applied, r.Changed)
			}
			// The reasons are in the result when the change is allowed too, as the command shows them.
			var got []string
			for _, w := range r.Widening {
				got = append(got, w.Key)
				if w.Reason == "" {
					t.Errorf("a widening without a reason: %+v", w)
				}
			}
			for _, key := range c.keys {
				if !hasKey(got, key) {
					t.Errorf("the result does not list %q among the widening: %v", key, got)
				}
			}
			if reflect.DeepEqual(f.saved(), before) {
				t.Error("an allowed change was not saved")
			}
		})
	}
}

// Narrowing, the limits and the TLS files never need the flag.
func TestAPIConfigSetNeverAsksTheFlagForWhatDoesNotReachFurther(t *testing.T) {
	for _, c := range []struct {
		name  string
		saved []string
		args  map[string]any
	}{
		{"confinement chat-only", nil, set(map[string]any{"confinement": "chat-only"})},
		{"a limit", nil, set(map[string]any{"max_concurrent": "8", "turn_timeout": "20m"})},
		{"the TLS files", nil, set(map[string]any{"tls_cert_file": "/c.pem", "tls_key_file": "/k.pem"})},
		{"a listener on this machine", nil, set(map[string]any{"v1_addr": "127.0.0.1:9443"})},
		{"tool calling switched off", nil, set(map[string]any{"tool_runtimes": "none"})},
		{"a runtime of the default list", nil, set(map[string]any{"image_runtimes": "codex"})},
		{"a value that spells the default", nil, set(map[string]any{"context_confinement": "chat-only"})},
		{"a higher class lowered", []string{"confinement=any", "context_confinement=sandboxed"}, set(map[string]any{"confinement": "sandboxed", "context_confinement": "chat-only"})},
		{"an ordinary value removed", []string{"max_concurrent=8", "context_confinement=sandboxed"}, unset([]string{"max_concurrent", "context_confinement"})},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{})
			f.save(c.saved...)
			r := decodeChangeResult(t, f.mustCall("api_config_set", c.args))
			if !r.Applied || len(r.Widening) != 0 {
				t.Errorf("applied %v, widening %+v", r.Applied, r.Widening)
			}
		})
	}
}

// A model sets the arguments of a call, so no argument may be a way to confirm: not at the top
// level of the call, and not among the settings. The refusal stands, and nothing is saved.
func TestNoArgumentOfAnAPIConfigSetCallCanOpenTheGate(t *testing.T) {
	widening := map[string]any{"v1_addr": "0.0.0.0:9443"}
	attempts := []map[string]any{}
	for _, name := range []string{"confirm", "yes", "force", "allow_api_exposure", "allow-api-exposure", "allowAPIExposure", "confirm_exposure", "dry_run", "override", "acknowledge_egress", "i_am_the_operator"} {
		for _, v := range []any{true, "1", "true", "yes", 1} {
			attempts = append(attempts, map[string]any{"set": widening, name: v})
		}
	}
	// Among the settings: not a setting, so the whole call is refused (the part that was right too).
	for _, name := range []string{"confirm", "yes", "allow_api_exposure"} {
		attempts = append(attempts, map[string]any{"set": map[string]any{"v1_addr": "0.0.0.0:9443", name: "true"}})
	}
	// Inside unset, or as the operator's own words.
	attempts = append(attempts,
		map[string]any{"set": widening, "unset": []string{"allow_api_exposure"}},
		map[string]any{"set": widening, "unset": "MONOAGENT_MCP_ALLOW_API_EXPOSURE=1"},
	)

	f := newConfigFixture(t, configSetup{})
	for _, args := range attempts {
		if text, err := f.call("api_config_set", args); err == nil {
			t.Fatalf("an argument opened the gate: %v answered %q", args, scrubbed(text))
		}
	}
	if !f.saved().IsEmpty() {
		t.Errorf("something was saved: %+v", f.saved())
	}
}

// The operator decides when the server starts: the variable read afterwards is too late, and a
// server that was started without it stays closed whatever its environment becomes.
func TestTheGateIsDecidedWhenTheServerStarts(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	t.Setenv("MONOAGENT_MCP_ALLOW_API_EXPOSURE", "1") // after NewServer
	if _, err := f.call("api_config_set", set(map[string]any{"v1_addr": "0.0.0.0:9443"})); err == nil {
		t.Error("a variable set after the server started opened its gate")
	}
	// And started with it, it is open.
	t.Setenv("MONOAGENT_MCP_ALLOW_API_EXPOSURE", "1")
	open := NewServer(Options{DBPath: f.DBPath, Profile: "default", WorkflowsDir: filepath.Join(t.TempDir(), "workflows"), Version: "test", AllowMutations: true, APIEnv: f.Server.opts.APIEnv})
	t.Cleanup(open.closeRuntime)
	if _, err := callAPITool(t, open, "api_config_set", set(map[string]any{"v1_addr": "0.0.0.0:9443"})); err != nil {
		t.Errorf("a server started with MONOAGENT_MCP_ALLOW_API_EXPOSURE=1: %v", err)
	}
}

// api_config_set is a mutating tool: omitted from tools/list and refused by name without
// --allow-mutations, which --allow-api-exposure does not stand in for.
func TestAPIConfigSetIsGatedLikeTheOtherMutatingTools(t *testing.T) {
	closed := newConfigFixture(t, configSetup{readOnly: true})
	if toolsListNames(t, closed.Server)["api_config_set"] {
		t.Error("api_config_set changes what the server exposes and must not be listed without --allow-mutations")
	}
	open := newConfigFixture(t, configSetup{})
	if !toolsListNames(t, open.Server)["api_config_set"] {
		t.Error("api_config_set must be listed with --allow-mutations")
	}

	// Without --allow-mutations it is refused by name, even for a server whose operator allowed exposure.
	for _, setup := range []configSetup{{readOnly: true}, {readOnly: true, allowExposure: true}} {
		f := newConfigFixture(t, setup)
		_, err := f.call("api_config_set", set(map[string]any{"max_concurrent": "8"}))
		if err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%+v: %v, want a refusal that names --allow-mutations", setup, err)
		}
		if !f.saved().IsEmpty() {
			t.Errorf("%+v: a refused call changed what is saved", setup)
		}
	}
}

// What a model is offered to send has no way to confirm anything: every name in the schema is a
// setting, set, or unset (and the words of a schema).
func TestTheSchemaOfAPIConfigSetOffersNoWayToConfirm(t *testing.T) {
	var def map[string]any
	for _, d := range toolDefinitions(true) {
		if d["name"] == "api_config_set" {
			def = d
		}
	}
	if def == nil {
		t.Fatal("api_config_set is not listed")
	}
	b, err := json.Marshal(def["inputSchema"])
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"set": true, "unset": true, "items": true, "properties": true}
	for _, k := range apiconfig.Keys() {
		allowed[k] = true
	}
	var names []string
	for _, m := range regexp.MustCompile(`"([A-Za-z0-9_]+)":\{`).FindAllStringSubmatch(string(b), -1) {
		names = append(names, m[1])
		if !allowed[m[1]] {
			t.Errorf("the schema offers %q, which is neither a setting nor set or unset", m[1])
		}
	}
	if !hasKey(names, "set") || !hasKey(names, "max_concurrent") {
		t.Errorf("the schema lists %v: it must offer the settings", names)
	}
}
