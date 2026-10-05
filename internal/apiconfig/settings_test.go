package apiconfig

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/openaiapi"
)

func TestKeysAreTheTenSettingsInDocumentOrder(t *testing.T) {
	want := []string{"v1_addr", "tls_cert_file", "tls_key_file", "confinement", "context_confinement",
		"auto_confinement", "max_concurrent", "turn_timeout", "image_runtimes", "tool_runtimes"}
	got := Keys()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Keys() = %v, want %v", got, want)
	}
	// A caller that edits the slice must not change what the next one gets.
	got[0] = "x"
	if Keys()[0] != "v1_addr" {
		t.Error("Keys() hands out its own storage")
	}
}

// The environment variable and the flag of each setting are what the documents and the
// help print, so they are the names the code reads: cmd/monoagentcli checks the flags
// and the readers against this table.
func TestSpecsNameTheEnvironmentVariableAndTheServerFlag(t *testing.T) {
	want := map[string][2]string{
		"v1_addr":             {"--v1-addr", "MONOAGENT_API_V1_ADDR"},
		"tls_cert_file":       {"", "MONOAGENT_API_TLS_CERT"},
		"tls_key_file":        {"", "MONOAGENT_API_TLS_KEY"},
		"confinement":         {"--confinement", "MONOAGENT_API_CONFINEMENT"},
		"context_confinement": {"--context-confinement", "MONOAGENT_API_CONTEXT_CONFINEMENT"},
		"auto_confinement":    {"--auto-confinement", "MONOAGENT_API_AUTO_CONFINEMENT"},
		"max_concurrent":      {"--max-concurrent", "MONOAGENT_API_MAX_CONCURRENT"},
		"turn_timeout":        {"", "MONOAGENT_API_TURN_TIMEOUT"},
		"image_runtimes":      {"", "MONOAGENT_API_IMAGE_RUNTIMES"},
		"tool_runtimes":       {"", "MONOAGENT_API_TOOL_RUNTIMES"},
	}
	specs := Specs()
	if len(specs) != len(want) {
		t.Fatalf("%d specs, want %d", len(specs), len(want))
	}
	for i, sp := range specs {
		if sp.Key != Keys()[i] {
			t.Errorf("spec %d is %q, Keys()[%d] is %q", i, sp.Key, i, Keys()[i])
		}
		if w := want[sp.Key]; sp.ServerFlag != w[0] || sp.Env != w[1] {
			t.Errorf("%s: flag %q and variable %q, want %q and %q", sp.Key, sp.ServerFlag, sp.Env, w[0], w[1])
		}
	}
}

func TestDefaultsAreWhatTheServerDoesWithNothingSet(t *testing.T) {
	d := Defaults()
	for key, want := range map[string]string{
		"v1_addr": "", "tls_cert_file": "", "tls_key_file": "", "confinement": "",
		"context_confinement": "chat-only", "auto_confinement": "chat-only",
		"max_concurrent": "4", "turn_timeout": "10m",
		"image_runtimes": "codex,antigravity", "tool_runtimes": "claude,codex",
	} {
		if got := d.Get(key); got != want {
			t.Errorf("default of %s = %q, want %q", key, got, want)
		}
	}
	// ...which is what the parsers make of nothing.
	img, _ := openaiapi.ParseImageRuntimes("")
	if d.ImageRuntimes != strings.Join(img, ",") {
		t.Errorf("image default %q is not the parser's %v", d.ImageRuntimes, img)
	}
	tools, _ := openaiapi.ParseToolRuntimes("")
	if d.ToolRuntimes != strings.Join(tools, ",") {
		t.Errorf("tool default %q is not the parser's %v", d.ToolRuntimes, tools)
	}
	if n, _ := openaiapi.ParseMaxConcurrent(d.MaxConcurrent); n != openaiapi.DefaultMaxConcurrent {
		t.Errorf("max default %q is not %d", d.MaxConcurrent, openaiapi.DefaultMaxConcurrent)
	}
	if got, _ := openaiapi.ParseTurnTimeout(d.TurnTimeout); got != openaiapi.DefaultTurnTimeout {
		t.Errorf("timeout default %q is not %v", d.TurnTimeout, openaiapi.DefaultTurnTimeout)
	}
	if got := Specs()[6].Default; got != "4" {
		t.Errorf("the spec of max_concurrent says its default is %q", got)
	}
}

func TestLookupKeyTakesTheDashedSpellingOfAFlag(t *testing.T) {
	for in, want := range map[string]string{"v1_addr": "v1_addr", "v1-addr": "v1_addr", "tls-cert-file": "tls_cert_file", "tool_runtimes": "tool_runtimes"} {
		if got, ok := LookupKey(in); !ok || got != want {
			t.Errorf("LookupKey(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "V1_ADDR", "--v1-addr", "v1addr", "all", " v1_addr", "sk-ma-secret"} {
		if got, ok := LookupKey(in); ok {
			t.Errorf("LookupKey(%q) = %q, want it refused", in, got)
		}
	}
}

func TestGetSetUnset(t *testing.T) {
	var s Settings
	if !s.IsEmpty() {
		t.Fatal("a zero Settings holds nothing")
	}
	for i, key := range Keys() {
		if err := s.Set(key, "v"+key); err != nil {
			t.Fatalf("Set(%s): %v", key, err)
		}
		if got := s.Get(key); got != "v"+key {
			t.Errorf("Get(%s) = %q after Set", key, got)
		}
		for j, other := range Keys() {
			if (j <= i) != (s.Get(other) != "") {
				t.Errorf("after setting %s, %s holds %q: Set must change its own field and no other", key, other, s.Get(other))
			}
		}
	}
	if s.IsEmpty() {
		t.Error("ten values set, and it says empty")
	}
	s.Unset("max_concurrent")
	if s.Get("max_concurrent") != "" || s.Get("turn_timeout") == "" {
		t.Errorf("Unset removed %q and left %q", s.Get("max_concurrent"), s.Get("turn_timeout"))
	}

	err := s.Set("nonsense", "x")
	if err == nil || strings.Contains(err.Error(), "nonsense") || !strings.Contains(err.Error(), "v1_addr") {
		t.Errorf("an unknown key is refused by listing the settings and not by echoing the key: %v", err)
	}
	s.Unset("nonsense") // nothing to do
	if s.Get("nonsense") != "" {
		t.Error("an unknown key holds nothing")
	}
}

// What Validate accepts and refuses is the parity table of cmd/monoagentcli (the flags,
// the environment, and this); here are its own rules, the spelling it stores and what its
// messages say.
func TestCanonicalSpellings(t *testing.T) {
	for _, c := range []struct{ key, in, want string }{
		{"v1_addr", "0.0.0.0:9443", "0.0.0.0:9443"},
		{"v1_addr", ":9443", ":9443"},
		{"tls_cert_file", "/etc/ssl/a b.pem", "/etc/ssl/a b.pem"},
		{"confinement", "sandboxed", "sandboxed"},
		{"max_concurrent", "8", "8"},
		{"max_concurrent", "+5", "5"},
		{"max_concurrent", "007", "7"},
		{"turn_timeout", "15m", "15m"},
		{"turn_timeout", "900s", "15m"},
		{"turn_timeout", "1h", "1h"},
		{"turn_timeout", "90m", "1h30m"},
		{"turn_timeout", "90s", "1m30s"},
		{"turn_timeout", "10s", "10s"},
		{"turn_timeout", "10.5s", "10.5s"},
		{"image_runtimes", "agy, Codex", "antigravity,codex"},
		{"image_runtimes", "codex,codex,claude", "codex,claude"},
		{"image_runtimes", "none", "none"},
		{"image_runtimes", "NONE", "none"},
		{"tool_runtimes", " claude ,codex ", "claude,codex"},
		{"tool_runtimes", "none", "none"},
		// A blank list is the default list, as the environment reads it.
		{"tool_runtimes", " ", "claude,codex"},
	} {
		got, err := Canonical(c.key, c.in)
		if err != nil || got != c.want {
			t.Errorf("Canonical(%s, %q) = %q, %v, want %q", c.key, c.in, got, err, c.want)
		}
		// The stored spelling is itself accepted and stays the same.
		if again, err := Canonical(c.key, got); err != nil || again != got {
			t.Errorf("Canonical(%s, %q) = %q, %v: the stored spelling must be stable", c.key, got, again, err)
		}
	}
	if _, err := Canonical("nonsense", "x"); err == nil {
		t.Error("an unknown key has no canonical spelling")
	}
}

// A message names the setting and the rule. It echoes what was typed only for an address
// or a path: a person may paste a key anywhere, and nothing here is worth a leak.
func TestValidateMessagesNameTheSettingAndKeepTheValueOut(t *testing.T) {
	for _, c := range []struct{ key, in, contains string }{
		{"confinement", "supersecret-everything", "confinement must be chat-only, sandboxed or any"},
		{"context_confinement", "supersecret-everything", "context_confinement must be chat-only, sandboxed or any"},
		{"auto_confinement", "supersecret-everything", "auto_confinement must be chat-only, sandboxed or any"},
		{"max_concurrent", "supersecret-many", "max_concurrent must be an integer from 1 to 64"},
		{"max_concurrent", "65", "max_concurrent must be an integer from 1 to 64"},
		{"turn_timeout", "supersecret-soon", "turn_timeout must be a duration of at least 10s, such as 15m"},
		{"image_runtimes", "supersecret runtime", "image_runtimes must be a comma-separated list of runtime ids"},
		{"image_runtimes", "none,codex", "image_runtimes"},
		{"tool_runtimes", "supersecret runtime", "tool_runtimes must be a comma-separated list of runtime ids"},
		{"v1_addr", "9443", "v1_addr must be host:port, such as 127.0.0.1:9443 or :9443"},
	} {
		var s Settings
		if err := s.Set(c.key, c.in); err != nil {
			t.Fatal(err)
		}
		problems := Validate(s)
		if len(problems) != 1 || problems[0].Key != c.key {
			t.Errorf("%s=%q: problems %+v, want one for the setting", c.key, c.in, problems)
			continue
		}
		msg := problems[0].Message
		if !strings.Contains(msg, c.contains) {
			t.Errorf("%s=%q: message %q does not say %q", c.key, c.in, msg, c.contains)
		}
		if c.key != "v1_addr" && strings.Contains(msg, c.in) {
			t.Errorf("%s: the message echoes the value %q: %q", c.key, c.in, msg)
		}
	}
	// An address is echoed: it is what the person typed for that setting, and it is what they must fix.
	var s Settings
	s.V1Addr = "0.0.0.0:99999"
	if p := Validate(s); len(p) != 1 || !strings.Contains(p[0].Message, "99999") {
		t.Errorf("the message for a bad port should show the address: %+v", p)
	}
}

func TestValidateSkipsWhatIsNotSavedAndAcceptsTheDefaults(t *testing.T) {
	if p := Validate(Settings{}); len(p) != 0 {
		t.Errorf("nothing saved, yet problems: %+v", p)
	}
	if p := Validate(Defaults()); len(p) != 0 {
		t.Errorf("the defaults are not valid settings: %+v", p)
	}
}

// The two files are one setting in two keys: the environment's pair is both or an error,
// and a saved half is never taken for a certificate.
func TestValidateWantsTheTLSFilesTogether(t *testing.T) {
	both := Settings{TLSCertFile: "/c.pem", TLSKeyFile: "/k.pem"}
	if p := Validate(both); len(p) != 0 {
		t.Errorf("a pair is fine: %+v", p)
	}
	for name, s := range map[string]Settings{"cert only": {TLSCertFile: "/c.pem"}, "key only": {TLSKeyFile: "/k.pem"}} {
		p := Validate(s)
		if len(p) != 1 || !strings.Contains(p[0].Message, "tls_cert_file and tls_key_file must be set together") {
			t.Errorf("%s: %+v", name, p)
			continue
		}
		want := "tls_cert_file"
		if name == "key only" {
			want = "tls_key_file"
		}
		if p[0].Key != want {
			t.Errorf("%s: the problem is filed under %q, want the file that is there, %q", name, p[0].Key, want)
		}
	}
}

func TestValidateReportsEverySettingThatFailsInOrder(t *testing.T) {
	s := Settings{V1Addr: "nope", Confinement: "x", MaxConcurrent: "0", ImageRuntimes: "co dex"}
	p := Validate(s)
	var keys []string
	for _, pr := range p {
		keys = append(keys, pr.Key)
	}
	if strings.Join(keys, ",") != "v1_addr,confinement,max_concurrent,image_runtimes" {
		t.Errorf("problems for %v, want them in the order of the settings", keys)
	}
}

func TestValidListenAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:9443", ":9443", "0.0.0.0:0", "[::1]:9443", "localhost:80", "example.com:65535"} {
		if err := ValidListenAddr(ok); err != nil {
			t.Errorf("ValidListenAddr(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "9443", "host", "0.0.0.0:abc", "0.0.0.0:65536", "0.0.0.0:99999", "0.0.0.0:-1", "0.0.0.0:", "1:2:3"} {
		if err := ValidListenAddr(bad); err == nil {
			t.Errorf("ValidListenAddr(%q) accepted", bad)
		}
	}
}
