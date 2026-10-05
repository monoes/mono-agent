package apiconfig

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(name string) string { return m[name] }
}

func specOf(t *testing.T, key string) Spec {
	t.Helper()
	for _, sp := range Specs() {
		if sp.Key == key {
			return sp
		}
	}
	t.Fatalf("no spec for %s", key)
	return Spec{}
}

// Per setting: flag, then environment, then saved, then default; and which one it was.
func TestResolveKeyPrecedence(t *testing.T) {
	type layers struct{ flag, env, saved string }
	for _, c := range []struct {
		key        string
		l          layers
		text, from string
	}{
		{"confinement", layers{"chat-only", "sandboxed", "any"}, "chat-only", SourceFlag},
		{"confinement", layers{"", "sandboxed", "any"}, "sandboxed", SourceEnv},
		{"confinement", layers{"", "", "any"}, "any", SourceSaved},
		{"confinement", layers{"", "", ""}, "", SourceDefault}, // no value: the listener decides
		{"v1_addr", layers{":1", "127.0.0.1:2", "127.0.0.1:3"}, ":1", SourceFlag},
		{"v1_addr", layers{"", "127.0.0.1:2", "127.0.0.1:3"}, "127.0.0.1:2", SourceEnv},
		{"v1_addr", layers{"", "", "127.0.0.1:3"}, "127.0.0.1:3", SourceSaved},
		{"v1_addr", layers{}, "", SourceDefault},
		{"context_confinement", layers{"", "", ""}, "chat-only", SourceDefault},
		{"context_confinement", layers{"", "", "any"}, "any", SourceSaved},
		{"auto_confinement", layers{"", "sandboxed", ""}, "sandboxed", SourceEnv},
		{"max_concurrent", layers{"9", "8", "7"}, "9", SourceFlag},
		{"max_concurrent", layers{"", "8", "7"}, "8", SourceEnv},
		{"max_concurrent", layers{"", "", "7"}, "7", SourceSaved},
		{"max_concurrent", layers{}, "4", SourceDefault},
		{"turn_timeout", layers{"", "", ""}, "10m", SourceDefault},
		{"turn_timeout", layers{"", "", "15m"}, "15m", SourceSaved},
		{"turn_timeout", layers{"", "20m", "15m"}, "20m", SourceEnv},
		{"image_runtimes", layers{}, "codex,antigravity", SourceDefault},
		{"image_runtimes", layers{"", "", "none"}, "none", SourceSaved},
		{"tool_runtimes", layers{}, "claude,codex", SourceDefault},
		{"tool_runtimes", layers{"", "codex", "none"}, "codex", SourceEnv},
	} {
		sp := specOf(t, c.key)
		var saved Settings
		if c.l.saved != "" {
			_ = saved.Set(c.key, c.l.saved)
		}
		got := ResolveKey(c.key, c.l.flag, envOf(map[string]string{sp.Env: c.l.env}), saved)
		if got.Key != c.key || got.Text != c.text || got.Source != c.from {
			t.Errorf("%s %+v: %+v, want text %q from %s", c.key, c.l, got, c.text, c.from)
		}
	}
}

// An empty variable is an unset one: the code and the tests that clear a variable with
// t.Setenv(name, "") have always meant that.
func TestAnEmptyVariableIsNotSet(t *testing.T) {
	var saved Settings
	_ = saved.Set("max_concurrent", "7")
	got := ResolveKey("max_concurrent", "", envOf(map[string]string{"MONOAGENT_API_MAX_CONCURRENT": ""}), saved)
	if got.Text != "7" || got.Source != SourceSaved {
		t.Errorf("%+v", got)
	}
}

// The value a document reports is in the spelling it stores, wherever it came from, so
// that it can be compared with a saved value and with what the daemon reported.
func TestResolvedTextIsCanonical(t *testing.T) {
	got := ResolveKey("image_runtimes", "", envOf(map[string]string{"MONOAGENT_API_IMAGE_RUNTIMES": "agy, Codex"}), Settings{})
	if got.Text != "antigravity,codex" || got.Source != SourceEnv {
		t.Errorf("%+v", got)
	}
	got = ResolveKey("turn_timeout", "", envOf(map[string]string{"MONOAGENT_API_TURN_TIMEOUT": "900s"}), Settings{})
	if got.Text != "15m" {
		t.Errorf("%+v", got)
	}
	got = ResolveKey("max_concurrent", "+5", envOf(nil), Settings{})
	if got.Text != "5" || got.Source != SourceFlag {
		t.Errorf("%+v", got)
	}
	// A value that fails its rule is reported as it is, not hidden: the document says it.
	got = ResolveKey("turn_timeout", "", envOf(map[string]string{"MONOAGENT_API_TURN_TIMEOUT": "soon"}), Settings{})
	if got.Text != "soon" || got.Source != SourceEnv {
		t.Errorf("%+v", got)
	}
}

// The TLS files are a pair: the environment's pair wins as a pair, even with one variable.
func TestTheTLSPairIsResolvedAsAPair(t *testing.T) {
	const cert, key = "MONOAGENT_API_TLS_CERT", "MONOAGENT_API_TLS_KEY"
	saved := Settings{TLSCertFile: "/saved/c.pem", TLSKeyFile: "/saved/k.pem"}
	for _, c := range []struct {
		name              string
		env               map[string]string
		saved             Settings
		wantCert, wantKey string
		fromCert, fromKey string
	}{
		{"both in the environment", map[string]string{cert: "/e/c.pem", key: "/e/k.pem"}, saved, "/e/c.pem", "/e/k.pem", SourceEnv, SourceEnv},
		{"only the certificate in the environment", map[string]string{cert: "/e/c.pem"}, saved, "/e/c.pem", "", SourceEnv, SourceEnv},
		{"only the key in the environment", map[string]string{key: "/e/k.pem"}, saved, "", "/e/k.pem", SourceEnv, SourceEnv},
		{"saved only", nil, saved, "/saved/c.pem", "/saved/k.pem", SourceSaved, SourceSaved},
		// A half pair in a document edited by hand is shown as it is: Validate is what refuses it.
		{"only a certificate saved", nil, Settings{TLSCertFile: "/saved/c.pem"}, "/saved/c.pem", "", SourceSaved, SourceSaved},
		{"only a key saved", nil, Settings{TLSKeyFile: "/saved/k.pem"}, "", "/saved/k.pem", SourceSaved, SourceSaved},
		{"nothing", nil, Settings{}, "", "", SourceDefault, SourceDefault},
	} {
		env := envOf(c.env)
		gc := ResolveKey("tls_cert_file", "", env, c.saved)
		gk := ResolveKey("tls_key_file", "", env, c.saved)
		if gc.Text != c.wantCert || gc.Source != c.fromCert || gk.Text != c.wantKey || gk.Source != c.fromKey {
			t.Errorf("%s: cert %+v, key %+v", c.name, gc, gk)
		}
	}
}

func TestResolveAllIsTheTenSettingsInOrderWithTheFlagsOfTheServer(t *testing.T) {
	var saved Settings
	_ = saved.Set("v1_addr", "127.0.0.1:1")
	_ = saved.Set("confinement", "sandboxed")
	env := envOf(map[string]string{"MONOAGENT_API_CONFINEMENT": "any", "MONOAGENT_API_TURN_TIMEOUT": "20m"})
	all := ResolveAll(Flags{V1Addr: ":9", AutoConfinement: "sandboxed", MaxConcurrent: 12}, env, saved)
	if len(all) != 10 {
		t.Fatalf("%d settings", len(all))
	}
	want := map[string][2]string{
		"v1_addr": {":9", SourceFlag}, "tls_cert_file": {"", SourceDefault}, "tls_key_file": {"", SourceDefault},
		"confinement": {"any", SourceEnv}, "context_confinement": {"chat-only", SourceDefault}, "auto_confinement": {"sandboxed", SourceFlag},
		"max_concurrent": {"12", SourceFlag}, "turn_timeout": {"20m", SourceEnv},
		"image_runtimes": {"codex,antigravity", SourceDefault}, "tool_runtimes": {"claude,codex", SourceDefault},
	}
	for i, key := range Keys() {
		if all[i].Key != key {
			t.Errorf("setting %d is %s, want %s", i, all[i].Key, key)
		}
		if w := want[key]; all[i].Text != w[0] || all[i].Source != w[1] {
			t.Errorf("%s: %+v, want %q from %s", key, all[i], w[0], w[1])
		}
	}
	// No flag given: a zero MaxConcurrent is not a flag.
	if r := ResolveAll(Flags{}, envOf(nil), Settings{})[6]; r.Source != SourceDefault || r.Text != "4" {
		t.Errorf("max_concurrent with no flag: %+v", r)
	}
}

// The overlay hands the existing readers what they read from the environment, with the
// saved value in the places the environment leaves empty.
func TestOverlayFillsInWhatTheEnvironmentLeavesEmpty(t *testing.T) {
	var saved Settings
	_ = saved.Set("confinement", "sandboxed")
	_ = saved.Set("turn_timeout", "15m")
	_ = saved.Set("image_runtimes", "none")
	got := Overlay(saved, envOf(map[string]string{
		"MONOAGENT_API_CONFINEMENT":    "any",        // the environment wins, as it was typed
		"MONOAGENT_API_IMAGE_RUNTIMES": "agy, Codex", // ...not in the canonical spelling
		"MONOAGENT_API_V1_ADDR":        "",           // empty is not set
		"HOME":                         "/home/x",
	}))
	for name, want := range map[string]string{
		"MONOAGENT_API_CONFINEMENT":    "any",
		"MONOAGENT_API_TURN_TIMEOUT":   "15m",
		"MONOAGENT_API_IMAGE_RUNTIMES": "agy, Codex",
		"MONOAGENT_API_V1_ADDR":        "",
		"MONOAGENT_API_TOOL_RUNTIMES":  "", // the defaults are the readers' own
		"MONOAGENT_API_MAX_CONCURRENT": "",
		"HOME":                         "/home/x", // anything else passes through
		"MONOAGENT_API_SOMETHING_ELSE": "",
	} {
		if g := got(name); g != want {
			t.Errorf("%s = %q, want %q", name, g, want)
		}
	}
}

func TestOverlayResolvesTheTLSPairAsAPair(t *testing.T) {
	saved := Settings{TLSCertFile: "/saved/c.pem", TLSKeyFile: "/saved/k.pem"}
	got := Overlay(saved, envOf(nil))
	if got("MONOAGENT_API_TLS_CERT") != "/saved/c.pem" || got("MONOAGENT_API_TLS_KEY") != "/saved/k.pem" {
		t.Error("the saved pair should show when the environment names none")
	}
	got = Overlay(saved, envOf(map[string]string{"MONOAGENT_API_TLS_CERT": "/e/c.pem"}))
	if got("MONOAGENT_API_TLS_CERT") != "/e/c.pem" || got("MONOAGENT_API_TLS_KEY") != "" {
		t.Errorf("one variable in the environment takes the pair from it: %q %q", got("MONOAGENT_API_TLS_CERT"), got("MONOAGENT_API_TLS_KEY"))
	}
}

// ResolveKey and Overlay are one rule: wherever the value comes from the environment or the
// saved layer, the overlay gives the reader what ResolveKey calls the setting's value.
func TestOverlayAndResolveKeyAgree(t *testing.T) {
	values := map[string][2]string{ // the environment's value and the saved one
		"v1_addr": {":9443", "127.0.0.1:9443"}, "tls_cert_file": {"/e/c.pem", "/s/c.pem"}, "tls_key_file": {"/e/k.pem", "/s/k.pem"},
		"confinement": {"any", "sandboxed"}, "context_confinement": {"any", "sandboxed"}, "auto_confinement": {"any", "sandboxed"},
		"max_concurrent": {"20", "30"}, "turn_timeout": {"20m", "30m"}, "image_runtimes": {"none", "codex"}, "tool_runtimes": {"none", "claude"},
	}
	for _, key := range Keys() {
		sp := specOf(t, key)
		for _, withEnv := range []bool{false, true} {
			for _, withSaved := range []bool{false, true} {
				env := map[string]string{}
				var saved Settings
				if withEnv {
					env[sp.Env] = values[key][0]
				}
				if withSaved {
					_ = saved.Set(key, values[key][1])
				}
				r := ResolveKey(key, "", envOf(env), saved)
				overlay := Overlay(saved, envOf(env))(sp.Env)
				if r.Source == SourceDefault {
					if overlay != "" {
						t.Errorf("%s env=%v saved=%v: the overlay gives %q where the setting is at its default", key, withEnv, withSaved, overlay)
					}
					continue
				}
				if canon, err := Canonical(key, overlay); err != nil || canon != r.Text {
					t.Errorf("%s env=%v saved=%v: overlay %q, ResolveKey %+v", key, withEnv, withSaved, overlay, r)
				}
			}
		}
	}
}

func TestEnvWithSavedReadsTheStoredSettings(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	putRow(t, db, `{"v":1,"confinement":"sandboxed","max_concurrent":8}`)
	getenv, err := EnvWithSaved(ctx, db, envOf(map[string]string{"MONOAGENT_API_MAX_CONCURRENT": "3"}))
	if err != nil {
		t.Fatal(err)
	}
	if getenv("MONOAGENT_API_CONFINEMENT") != "sandboxed" || getenv("MONOAGENT_API_MAX_CONCURRENT") != "3" {
		t.Errorf("the saved confinement and the environment's maximum were expected: %q %q",
			getenv("MONOAGENT_API_CONFINEMENT"), getenv("MONOAGENT_API_MAX_CONCURRENT"))
	}

	// Nothing saved: the environment as it is.
	getenv, err = EnvWithSaved(ctx, openDB(t), envOf(map[string]string{"MONOAGENT_API_V1_ADDR": ":9"}))
	if err != nil || getenv("MONOAGENT_API_V1_ADDR") != ":9" || getenv("MONOAGENT_API_CONFINEMENT") != "" {
		t.Errorf("no row: %v %q", err, getenv("MONOAGENT_API_V1_ADDR"))
	}
}

// A server does not start on settings that fail their rules, and says which and how to fix it.
func TestEnvWithSavedRefusesInvalidSettingsByName(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":1,"confinement":"everything","max_concurrent":99}`)
	_, err := EnvWithSaved(context.Background(), db, envOf(nil))
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 2 {
		t.Fatalf("error %v, want a *ValidationError with two problems", err)
	}
	msg := err.Error()
	for _, want := range []string{"confinement must be", "max_concurrent must be", "api config unset", "api config set"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message %q does not say %q", msg, want)
		}
	}
	if strings.Contains(msg, "everything") || strings.Contains(msg, "99") {
		t.Errorf("the message repeats a value: %q", msg)
	}
}

func TestEnvWithSavedPassesOnWhatLoadRefuses(t *testing.T) {
	db := openDB(t)
	putRow(t, db, `{"v":2}`)
	if _, err := EnvWithSaved(context.Background(), db, envOf(nil)); !errors.Is(err, ErrTooNew) {
		t.Errorf("a newer format: %v", err)
	}
	putRow(t, db, `[`)
	if _, err := EnvWithSaved(context.Background(), db, envOf(nil)); err == nil || !strings.Contains(err.Error(), Row) {
		t.Errorf("a damaged document: %v", err)
	}
}

func TestValidationErrorListsEveryProblem(t *testing.T) {
	e := &ValidationError{Problems: []Problem{{Key: "a", Message: "a must be x"}, {Message: "nothing to change"}}}
	if got := e.Error(); got != "a must be x; nothing to change" {
		t.Errorf("Error() = %q", got)
	}
}
