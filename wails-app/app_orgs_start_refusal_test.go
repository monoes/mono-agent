package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeOrgFile(t *testing.T, root, name, doc string) {
	t.Helper()
	dir := filepath.Join(root, ".monomind", "orgs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A start monomind refuses (R6 host preflight) reaches the toast as
// monomind's line plus the hint, not as the startup log lines before it.
func TestOrgRunFailureMessageIsMonomindsRefusal(t *testing.T) {
	tail := "2026/10/06 07:38:18 applied migration 061\n" +
		"Could not start org t: org \"t\" cannot start on this host: no usable sandbox (R6)\n" +
		"monomind checks the host before every start, resume and role replacement (R6): fix what the message names"
	got := orgRunFailureMessage(tail, errors.New("exit status 1"))
	if !strings.Contains(got, `org "t" cannot start on this host: no usable sandbox (R6)`) || !strings.Contains(got, "fix what the message names") {
		t.Errorf("message = %q", got)
	}
	if strings.Contains(got, "applied migration") {
		t.Errorf("message keeps unrelated log lines: %q", got)
	}
	if got := orgRunFailureMessage("boom", errors.New("exit status 1")); got != "boom" {
		t.Errorf("plain failure = %q", got)
	}
	if got := orgRunFailureMessage("", errors.New("exit status 1")); got != "exit status 1" {
		t.Errorf("empty tail = %q", got)
	}
}

func TestGetOrgDesignSaysWhetherItIsASectionsOrg(t *testing.T) {
	a, root := newOrgDesignApp(t)
	fakeOrgCLI(t, `{"valid":true,"warnings":[]}`, `{"ok":true}`)
	for name, doc := range map[string]string{
		"plain": `{"name":"plain","goal":"g","roles":[{"id":"a","title":"A","reports_to":null}]}`,
		"secs":  `{"name":"secs","goal":"g","roles":[{"id":"a","title":"A","reports_to":null},{"id":"b","title":"B","reports_to":"a"}],"sections":{"s":{"lead":"b","members":["b"]}}}`,
	} {
		writeOrgFile(t, root, name, doc)
	}
	if out := a.GetOrgDesign("plain"); !strings.Contains(out, `"sections_enabled":false`) {
		t.Errorf("plain: %s", out)
	}
	if out := a.GetOrgDesign("secs"); !strings.Contains(out, `"sections_enabled":true`) {
		t.Errorf("secs: %s", out)
	}
}
