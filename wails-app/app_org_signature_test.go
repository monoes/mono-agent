package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/orgsign/orgsigntest"
)

// signedDesignOrg saves the two-role org and signs it the way monomind
// 2.21 would, in a scratch operator dir.
func signedDesignOrg(t *testing.T, root string) {
	t.Helper()
	t.Setenv("MONOMIND_ORGRT_OPERATOR_DIR", filepath.Join(t.TempDir(), "operator"))
	t.Setenv("MONOMIND_AGENT_EXEC", "")
	if _, err := orgdesign.Save(root, twoRoleDoc()); err != nil {
		t.Fatal(err)
	}
	orgsigntest.Sign(t, root, "growth")
}

func signCalls(t *testing.T, log string) []string {
	t.Helper()
	var out []string
	if _, err := os.Stat(log); os.IsNotExist(err) {
		return nil // the stub CLI never ran
	}
	for _, c := range loggedArgs(t, log) {
		if strings.Contains(c, " sign ") {
			out = append(out, c)
		}
	}
	return out
}

// #288: a designer save of a signed org is re-signed through the CLI, for
// exactly the bytes it wrote.
func TestSaveOrgDoc_ResignsASignedOrg(t *testing.T) {
	reconciled := `{"v":1,"org":{"name":"growth","goal":"grow","status":"stopped","schedule":null,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":["lead"],"policy":{"git":"read"}}]},"reconcile":[]}`
	log, _ := fakeOrgCLI(t, `{"v":1,"org":"growth","valid":true}`, reconciled)
	a, root := newOrgDesignApp(t)
	signedDesignOrg(t, root)

	d, err := orgdesign.Load(root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	sha, err := a.saveOrgDoc(root, d)
	if err != nil {
		t.Fatalf("saveOrgDoc: %v", err)
	}
	want := "--profile gui --json org --project " + root + " sign growth --yes --expect-sha256 " + sha
	if calls := signCalls(t, log); len(calls) != 1 || calls[0] != want {
		t.Fatalf("sign calls = %q, want %q", calls, want)
	}
}

// An org changed outside the app is never signed by a designer save.
func TestSaveOrgDoc_NeverSignsAnOutsideEdit(t *testing.T) {
	reconciled := `{"v":1,"org":{"name":"growth","goal":"grow","status":"stopped","schedule":null,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":["lead"],"policy":{"git":"push"}}]},"reconcile":[]}`
	log, _ := fakeOrgCLI(t, `{"v":1,"org":"growth","valid":true}`, reconciled)
	a, root := newOrgDesignApp(t)
	signedDesignOrg(t, root)
	outside := twoRoleDoc()
	outside.Runtime = "codex"
	if _, err := orgdesign.Save(root, outside); err != nil {
		t.Fatal(err)
	}

	d, err := orgdesign.Load(root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveOrgDoc(root, d); err != nil {
		t.Fatalf("saveOrgDoc: %v", err)
	}
	if calls := signCalls(t, log); len(calls) != 0 {
		t.Fatalf("signed an outside edit: %q", calls)
	}
}

// A layout-only write (role ui is not signed) still verifies: no CLI call.
func TestKeepOrgSignature_LayoutNeedsNoSignature(t *testing.T) {
	log, _ := fakeOrgCLI(t, `{}`, `{}`)
	a, root := newOrgDesignApp(t)
	signedDesignOrg(t, root)
	d, err := orgdesign.Load(root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	sig := orgsign.Before(root, "growth", d.LoadedSHA(), false)
	if !sig.Eligible() {
		t.Fatal("a signed org loaded as is must be eligible")
	}
	_ = d.SetLayout(map[string]orgdesign.RoleUI{"lead": {X: 10, Y: 20}})
	sha, err := orgdesign.Save(root, d)
	if err != nil {
		t.Fatal(err)
	}
	a.keepOrgSignature(root, sig, "growth", sha)
	if calls := signCalls(t, log); len(calls) != 0 {
		t.Fatalf("sign calls for a layout change: %q", calls)
	}
	if st, _, _ := orgsign.VerifyFile(root, "growth"); !st.OK() {
		t.Fatalf("layout change broke the signature: %+v", st)
	}
}

func TestOrgSignArgs(t *testing.T) {
	if got := strings.Join(orgSignArgs("/p", "growth", "--status"), " "); got != "org --project /p sign growth --status" {
		t.Fatalf("args = %q", got)
	}
	if got := strings.Join(orgSignArgs("", "growth"), " "); got != "org sign growth" {
		t.Fatalf("args = %q", got)
	}
}
