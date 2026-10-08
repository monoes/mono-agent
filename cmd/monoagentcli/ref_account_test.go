package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRefAccountDocumentsDormantBuild(t *testing.T) {
	cmd := refAccountCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dormant", "Production signing keys are not pinned", "24 hours", "login_required", "keyring_unavailable", "unconfirmed", "MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE", "Never copy or restore", "current MIT license"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("ref account omits %q", want)
		}
	}
	assertNoEnforcementClaims(t, "ref account", out.String())
}

// The examples must remain usable if command flags or aliases change.
func TestRefAccountFlagsExistOnCommands(t *testing.T) {
	root := newRootCmd()
	for _, tc := range []struct {
		path  []string
		flags []string
	}{
		{[]string{"account", "login"}, []string{"email", "send", "code", "code-stdin", "no-browser", "timeout"}},
		{[]string{"account", "status"}, []string{"offline"}},
		{[]string{"library", "login"}, []string{"email", "send", "code", "code-stdin", "no-browser", "timeout"}},
		{[]string{"library", "status"}, []string{"offline"}},
	} {
		cmd, _, err := root.Find(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range tc.flags {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%v no longer has --%s", tc.path, flag)
			}
		}
	}
	cmd, _, err := root.Find([]string{"ref", "account"})
	if err != nil || cmd.Name() != "account" {
		t.Fatalf("ref account missing: %v", err)
	}
	applyClassification(root)
	if commandClass(cmd) != classOpen {
		t.Fatal("offline account reference requires sign-in")
	}
}
