package apiconfig

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The saved layer is stricter than the environment in two ways that the environment has no need of: whoever
// can change a saved value is not always whoever started the server, and a service reads it from another folder.

// A saved value is printed by `api config show` and handed to every model that calls api_config_get, and a path
// or an address is free text: a control character in it would drive a terminal (an escape sequence that sets the
// title or clears the screen) or forge a line of a log (S3 of the security review).
func TestAControlCharacterIsRefusedInEverySavedValue(t *testing.T) {
	texts := map[string]string{
		"an escape sequence": "x\x1b]0;PWNED\x07\x1b[2Jy", "a new line": "a\nb", "a carriage return": "a\rb",
		"a NUL": "a\x00b", "a delete": "a\x7fb", "a C1 control": "a\u009bb", "a tab": "a\tb",
	}
	for _, key := range Keys() {
		for name, text := range texts {
			if _, err := Canonical(key, text); err == nil || !strings.Contains(err.Error(), key+" must not contain control characters") {
				t.Errorf("Canonical(%s, %s) = %v, want a refusal that names the setting and the rule", key, name, err)
				continue
			} else if strings.ContainsAny(err.Error(), "\x1b\x07\n\r\x00\x7f\u009b\t") || strings.Contains(err.Error(), "PWNED") {
				t.Errorf("the refusal for %s (%s) repeats what was typed: %q", key, name, err.Error())
			}
			var s Settings
			if err := s.Set(key, text); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range Validate(s) {
				found = found || (p.Key == key && strings.Contains(p.Message, "control characters"))
			}
			if !found {
				t.Errorf("Validate does not report %s (%s)", key, name)
			}
		}
	}
	// What is not a control character stays fine: a space, and letters of any script (a path of the user's own).
	for _, c := range []struct{ key, text string }{
		{KeyTLSCertFile, filepath.Join(t.TempDir(), "my certs", "مرتضی.pem")},
		{KeyV1Addr, "مرتضی.example:9443"}, {KeyV1Addr, " 127.0.0.1:9443"},
	} {
		if _, err := Canonical(c.key, c.text); err != nil {
			t.Errorf("Canonical(%s, %q): %v", c.key, c.text, err)
		}
	}
	// An unknown key is still refused without echoing it, whatever it holds.
	if _, err := Canonical("nonsense\x1b", "x"); err == nil || strings.Contains(err.Error(), "nonsense") {
		t.Errorf("an unknown key: %v", err)
	}
}

// A saved file path is read by a service that starts in another folder and does not expand a ~: a relative path
// would name nothing there, and `set` would have taken it without a word (the review of phase 6 found a listener
// that never started for that reason while `show` said applied).
func TestASavedTLSFileMustBeAnAbsolutePath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "cert.pem")
	for _, key := range []string{KeyTLSCertFile, KeyTLSKeyFile} {
		if got, err := Canonical(key, abs); err != nil || got != abs {
			t.Errorf("Canonical(%s, %q) = %q, %v: an absolute path is fine", key, abs, got, err)
		}
		for _, bad := range []string{"cert.pem", "~/cert.pem", "./cert.pem", "../cert.pem", "certs/cert.pem", " " + abs, "~"} {
			_, err := Canonical(key, bad)
			if err == nil || !strings.Contains(err.Error(), key+" must be an absolute path") {
				t.Errorf("Canonical(%s, %q) = %v, want a refusal that says the path must be absolute", key, bad, err)
				continue
			}
			if strings.Contains(err.Error(), strings.TrimSpace(bad)) && len(strings.TrimSpace(bad)) > 3 {
				t.Errorf("the refusal repeats the path: %q", err.Error())
			}
		}
	}
}

// Through the one entry that every surface uses, a refused value is a *ValidationError that names the setting,
// and nothing is saved.
func TestApplyRefusesAControlCharacterAndARelativePath(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	abs := filepath.Join(t.TempDir(), "k.pem")
	for name, change := range map[string]Change{
		"a control character in an address": {Set: map[string]string{"v1_addr": "evil\x1b[2J:9443"}, Confirm: true},
		"a control character in a path":     {Set: map[string]string{"tls_cert_file": abs + "\x1b[2J", "tls_key_file": abs}},
		"a relative path":                   {Set: map[string]string{"tls_cert_file": "cert.pem", "tls_key_file": abs}},
		"a tilde":                           {Set: map[string]string{"tls_cert_file": abs, "tls_key_file": "~/key.pem"}},
	} {
		_, err := Apply(ctx, db, shellEnv(nil, nil, &fakeInstaller{}), change)
		var v *ValidationError
		if !errors.As(err, &v) || len(v.Problems) == 0 {
			t.Errorf("%s: %v, want a *ValidationError", name, err)
			continue
		}
		if saved, err := Load(ctx, db); err != nil || !saved.IsEmpty() {
			t.Errorf("%s: something was saved: %+v, %v", name, saved, err)
		}
	}
}
