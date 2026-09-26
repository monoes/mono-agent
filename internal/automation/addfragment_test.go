package automation

import (
	"errors"
	"strings"
	"testing"
)

func fragmentDraft(t *testing.T, loginSteps string) *Package {
	t.Helper()
	dir := writeTree(t, t.TempDir(), map[string]string{
		"automation.json": `{"schema":"monoagent.automation/v1","id":"draft","name":"Draft","version":"0.1.0",
  "site":{"startUrl":"https://app.acme.com/","domains":["app.acme.com"]},
  "permissions":{"steps":["click","call_fragment","page_script"],"scripts":["helper.js"]},
  "actions":[],"policy":{"tier":"standard"}}`,
		"fragments/login_flow.json": `{"name":"login_flow","steps":` + loginSteps + `}`,
		"fragments/nested.json":     `{"name":"nested","steps":[{"id":"n","type":"click","configKey":"login.submit"}]}`,
		"fragments/unrelated.json":  `{"name":"unrelated","steps":[{"id":"u","type":"click","selector":"#u"}]}`,
		"selectors.json":            `{"login.button":{"candidates":[{"css":"#login"}]},"login.submit":{"candidates":[{"css":"#go"}]},"other.key":{"candidates":[{"css":"#o"}]}}`,
		"scripts/helper.js":         "return 1;\n",
	})
	p, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const loginSteps = `[{"id":"a","type":"click","configKey":"login.button"},{"id":"b","type":"call_fragment","fragment":"nested"},{"id":"c","type":"page_script","script":"helper.js"}]`

func TestAddFragmentIntoBuiltin(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	res, err := r.AddFragment("demo", fragmentDraft(t, loginSteps), "login_flow", InstallOptions{Trust: TrustRecorded})
	if err != nil {
		t.Fatalf("AddFragment: %v %+v", err, res)
	}
	info, _ := r.Info("demo")
	if info.Version != "1.0.0+local.1" || info.Source != SourceBuiltin || info.Trust != TrustRecorded || !info.Modified {
		t.Fatalf("after AddFragment: %+v", info)
	}
	p, _ := r.Get("demo")
	if got := strings.Join(p.FragmentNames(), ","); got != "login_flow,nested" {
		t.Errorf("fragments %s (closure only, no unrelated)", got)
	}
	sel, _ := p.Selectors()
	if _, ok := sel["login.button"]; !ok || len(sel) != 2 {
		t.Errorf("selectors %v", sel)
	}
	if _, err := p.Script("helper.js"); err != nil || !contains(p.Manifest.Permissions.Scripts, "helper.js") {
		t.Errorf("script not merged/declared: %v %v", err, p.Manifest.Permissions.Scripts)
	}
	if strings.Join(p.Manifest.Actions, ",") != "hello" {
		t.Errorf("actions changed: %v", p.Manifest.Actions)
	}

	// Same content again: no conflict. Different content: conflict.
	if res, err := r.AddFragment("demo", fragmentDraft(t, loginSteps), "login_flow", InstallOptions{Trust: TrustRecorded}); err != nil || res.Version != "1.0.0+local.1" || !res.Installed {
		t.Errorf("identical fragment: %v %+v", err, res)
	}
	if info, _ := r.Info("demo"); info.Version != "1.0.0+local.1" {
		t.Errorf("no-op re-add bumped the version: %s", info.Version)
	}
	changed := `[{"id":"a","type":"click","configKey":"login.button"}]`
	if _, err := r.AddFragment("demo", fragmentDraft(t, changed), "login_flow", InstallOptions{Trust: TrustRecorded}); !errors.Is(err, ErrConflict) {
		t.Errorf("changed same-named fragment: %v", err)
	}

	// Lineage: the next release is pending, restore works.
	if rep, _ := r.SeedWithReport(seedFS("1.0.1", "b")); len(rep.Pending) != 1 {
		t.Errorf("newer seed not pending: %+v", rep)
	}
	if err := r.Restore("demo", seedFS("1.0.1", "b")); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("demo"); info.Version != "1.0.1" || info.Trust != TrustBuiltin {
		t.Errorf("after restore: %+v", info)
	}
}

func TestAddFragmentNewPackageAndErrors(t *testing.T) {
	r := newReg(t)
	res, err := r.AddFragment("frags", fragmentDraft(t, loginSteps), "login_flow", InstallOptions{Trust: TrustRecorded})
	if err != nil {
		t.Fatalf("new package: %v %+v", err, errorsOnly(res.Issues))
	}
	info, _ := r.Info("frags")
	if info.Source != SourceLocal || info.Trust != TrustRecorded || info.Version != "0.1.0" || info.Actions != 0 {
		t.Fatalf("new package: %+v", info)
	}
	p, _ := r.Get("frags")
	if _, err := p.Fragment("unrelated"); err == nil {
		t.Error("fragment outside the closure copied")
	}
	for _, name := range []string{"missing", "../x", ""} {
		if _, err := r.AddFragment("frags", fragmentDraft(t, loginSteps), name, InstallOptions{}); err == nil {
			t.Errorf("fragment %q accepted", name)
		}
	}
}
