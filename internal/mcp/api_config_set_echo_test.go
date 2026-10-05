package mcp

// Nothing a caller sends comes back in an error of api_config_set, as for the key tools: a caller may
// have pasted an API key into any of its arguments (a model that has just created one has it at
// hand), and an argument of 1 MiB must not come back as an error of 1 MiB. The checks are made in
// lower case too, because a runtime list is lower-cased by its parser and its ids are echoed by the
// reasons of the exposure gate.

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/apikeys"
)

// echoes says whether a message holds what a test sent, in any case: the key, a cut of it, or 16
// of the 1 MiB of Q.
func echoes(msg, short string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, strings.ToLower(short)) || strings.Contains(m, strings.Repeat("q", 16))
}

func TestNoErrorOfAPIConfigSetRepeatsAnArgument(t *testing.T) {
	pasted, err := apikeys.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	short := pasted[:30] // a cut of the key is shorter than a runtime id may be
	values := []struct{ what, value string }{
		{"a key", pasted},
		{"a key in upper case", strings.ToUpper(pasted)},
		{"a key cut short", short},
		{"a key cut short, in upper case", strings.ToUpper(short)},
		{"1 MiB", strings.Repeat("Q", 1<<20)},
	}

	// Every place a string can go: the value of each setting, a name that is no setting (as a key of
	// set and as an entry of unset), and unset given as a string.
	type place struct {
		name string
		args func(v string) map[string]any
	}
	places := []place{
		{"the name of a setting", func(v string) map[string]any { return set(map[string]any{v: "x"}) }},
		{"an entry of unset", func(v string) map[string]any { return unset([]string{v}) }},
		{"unset as a string", func(v string) map[string]any { return unset(v) }},
		{"a key named as a setting and an entry that is not", func(v string) map[string]any {
			return map[string]any{"set": map[string]any{"max_concurrent": "8"}, "unset": []string{v}}
		}},
	}
	for _, key := range apiconfig.Keys() {
		key := key
		places = append(places, place{"the value of " + key, func(v string) map[string]any { return set(map[string]any{key: v}) }})
	}

	f := newConfigFixture(t, configSetup{allowExposure: true}) // with the gate open: a value that is no key reaches Apply
	for _, p := range places {
		for _, v := range values {
			text, err := f.call("api_config_set", p.args(v.value))
			if err == nil {
				t.Errorf("%s holding %s was accepted: %q", p.name, v.what, scrubbed(text))
				continue
			}
			if echoes(err.Error(), short) {
				t.Errorf("%s holding %s: the error repeats it", p.name, v.what)
			}
			if len(err.Error()) > 1024 {
				t.Errorf("%s holding %s: an error of %d bytes", p.name, v.what, len(err.Error()))
			}
		}
	}
	if !f.saved().IsEmpty() {
		t.Errorf("a refused call saved something: %+v", f.saved())
	}
}

// A value that holds the start of an API key is refused whatever the setting: nothing here takes one,
// and what is saved is shown to whoever reads api_config_get. The tool says so without repeating it.
func TestAPIConfigSetRefusesAValueThatHoldsAnAPIKey(t *testing.T) {
	pasted, err := apikeys.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	f := newConfigFixture(t, configSetup{allowExposure: true})
	for _, key := range apiconfig.Keys() {
		_, err := f.call("api_config_set", set(map[string]any{key: "/etc/ssl/" + pasted + ".pem"}))
		if err == nil || !strings.Contains(err.Error(), key+" must not hold an API key") {
			t.Errorf("%s: %v, want a refusal that says it must not hold an API key", key, err)
		}
	}
	if !f.saved().IsEmpty() {
		t.Errorf("a key reached the saved settings: %+v", f.saved())
	}
}

func TestAPIConfigSetRefusesAValueThatIsTooLongNamingOnlyTheSetting(t *testing.T) {
	f := newConfigFixture(t, configSetup{allowExposure: true})
	for _, c := range []struct {
		key  string
		long int // the shortest value that is refused
	}{
		{"v1_addr", 261}, {"tls_cert_file", 4097}, {"tls_key_file", 4097}, {"confinement", 4097}, {"max_concurrent", 4097},
		{"turn_timeout", 4097}, {"image_runtimes", 4097}, {"tool_runtimes", 4097},
	} {
		_, err := f.call("api_config_set", set(map[string]any{c.key: strings.Repeat("a", c.long)}))
		if err == nil || !strings.Contains(err.Error(), c.key+" is too long") {
			t.Errorf("%s of %d characters: %v", c.key, c.long, err)
		}
	}
	// The longest address there can be, a host name of 253 characters and a port, is not too long.
	host := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	if len(host+":65535") > 260 {
		t.Fatalf("test data: %d", len(host+":65535"))
	}
	if _, err := f.call("api_config_set", set(map[string]any{"v1_addr": host + ":65535"})); err != nil {
		t.Errorf("the longest address: %v", err)
	}
}

// An address that is not a host and a port is refused in the words of the command, without the
// address: the command quotes it, which suits a command line.
func TestAPIConfigSetSaysWhatAnAddressMustBeWithoutRepeatingIt(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	for _, bad := range []string{"9443", "no-port-here-QQQQQQQQQQQQQQQQQ", "host:abc", "host:99999", "1:2:3", "host:"} {
		_, err := f.call("api_config_set", set(map[string]any{"v1_addr": bad}))
		if err == nil || !strings.Contains(err.Error(), "v1_addr must be host:port, such as 127.0.0.1:9443 or :9443") {
			t.Errorf("%q: %v", bad, err)
			continue
		}
		if strings.Contains(err.Error(), bad) && len(bad) > 8 {
			t.Errorf("%q: the error repeats the address: %v", bad, err)
		}
		if strings.Contains(err.Error(), "address ") || strings.Contains(err.Error(), "port number") {
			t.Errorf("%q: the error carries the detail the command adds: %v", bad, err)
		}
	}
}

// The reasons of the gate name the address or the runtimes of the change, which is what makes them
// useful to a person and what makes them an echo of the arguments: the refusal of a tool says which
// setting and why without them.
func TestTheRefusalOfAWideningChangeDoesNotRepeatTheValuesOfTheCall(t *testing.T) {
	for _, c := range []struct {
		name    string
		args    map[string]any
		secret  string // what the call holds and the refusal must not
		mention string // what it must still say: the setting, and why
	}{
		{"an address", set(map[string]any{"v1_addr": "203.0.113.7:9443"}), "203.0.113.7", "beyond this machine"},
		{"a long host name", set(map[string]any{"v1_addr": strings.Repeat("Q", 200) + ":9443"}), strings.Repeat("q", 16), "beyond this machine"},
		{"a bind to every interface", set(map[string]any{"v1_addr": ":9443"}), ":9443", "beyond this machine"},
		{"a runtime", set(map[string]any{"tool_runtimes": "claude,codex,zzz-extra-runtime"}), "zzz-extra-runtime", "default list"},
		{"a runtime in capitals", set(map[string]any{"image_runtimes": "CODEX," + strings.Repeat("Q", 32)}), strings.Repeat("q", 16), "default list"},
		{"two runtimes", set(map[string]any{"tool_runtimes": "alpha-one,beta-two"}), "alpha-one", "default list"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newConfigFixture(t, configSetup{})
			_, err := f.call("api_config_set", c.args)
			if err == nil {
				t.Fatal("the change was not refused")
			}
			if echoes(err.Error(), c.secret) {
				t.Errorf("the refusal repeats %q: %s", c.secret, err)
			}
			if !strings.Contains(err.Error(), c.mention) {
				t.Errorf("the refusal must still say why (%q): %s", c.mention, err)
			}
			// What the caller asked is not lost: when the operator allows it, the document shows it.
			open := newConfigFixture(t, configSetup{allowExposure: true})
			text := open.mustCall("api_config_set", c.args)
			if !strings.Contains(strings.ToLower(text), strings.ToLower(c.secret)) {
				t.Errorf("the document of an allowed change must show what was saved (%q)", c.secret)
			}
		})
	}
}

// The default list is public, and the reason says it: a runtime of the call that is a part of a word
// of it must not take that apart.
func TestTheRefusalKeepsTheDefaultListWholeWhenARuntimeIsPartOfOne(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	_, err := f.call("api_config_set", set(map[string]any{"tool_runtimes": "claude,codex,code"}))
	if err == nil || !strings.Contains(err.Error(), "beyond the default list (claude, codex)") || !strings.Contains(err.Error(), "<runtime>") {
		t.Errorf("a runtime named code, next to codex in the default list: %v", err)
	}
}
