package main

import (
	"strings"
	"testing"
)

// A saved value that holds a control character (a row planted by hand, or by a version that did not refuse
// it) is shown by `api config show` as text, and must not drive the terminal that prints it: no escape sequence
// that sets the title or clears the screen (S3 of the security review of phase 6). `set` refuses such a value now;
// this is the other side of it, for a row that is already there.
func TestAPIConfigShowTextWritesControlCharactersAsEscapes(t *testing.T) {
	db := configTest(t)
	saveAt(t, db, "tls_cert_file=/x\x1b]0;PWNED\x07", "tls_key_file=/k\u009b2J", "max_concurrent=3\nforged")
	text, _, err := runAPI(t, db, "default", false, "config", "show")
	if err != nil {
		t.Fatalf("a saved value that fails its rule must not stop show: %v", err)
	}
	if strings.ContainsAny(text, "\x1b\x07\u009b\x00") || strings.Contains(text, "3\nforged") {
		t.Errorf("a control character reached the table:\n%q", text)
	}
	for _, want := range []string{`/x\x1b]0;PWNED\x07`, `/k\x9b2J`, `3\x0aforged`} {
		if !strings.Contains(text, want) {
			t.Errorf("the table should show %q, with the control characters written out:\n%s", want, text)
		}
	}
	// A path of the person's own, in any script, is shown as it is.
	db = configTest(t)
	saveAt(t, db, "tls_cert_file=/home/مرتضی/my certs/c.pem", "tls_key_file=/home/مرتضی/my certs/k.pem")
	text, _, err = runAPI(t, db, "default", false, "config", "show")
	if err != nil || !strings.Contains(text, "/home/مرتضی/my certs/c.pem") {
		t.Errorf("a path with letters of another script and a space: %v\n%s", err, text)
	}
}

// JSON writes a control character as an escape of its own, so the document stays one document that holds the
// value as it is stored.
func TestAPIConfigShowJSONKeepsTheStoredValue(t *testing.T) {
	db := configTest(t)
	saveAt(t, db, "tls_cert_file=/x\x1b[2J", "tls_key_file=/k.pem")
	out, _, err := runAPI(t, db, "default", true, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out, "\x1b") {
		t.Errorf("a raw escape in the JSON: %q", out)
	}
	for _, row := range decodeConfig(t, out).Settings {
		if row.Key == "tls_cert_file" && row.Saved != "/x\x1b[2J" {
			t.Errorf("saved = %q, want the stored value", row.Saved)
		}
	}
}
