package recordanalyze

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadInputsFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	m, err := ReadInputsFile(write("ok.json", `{"account_password":"hunter2","n":3}`, 0o600))
	if err != nil || m["account_password"] != "hunter2" || m["n"] != float64(3) {
		t.Fatalf("m=%v err=%v", m, err)
	}
	for name, c := range map[string]struct {
		body string
		mode os.FileMode
	}{
		"open.json":  {`{"account_password":"hunter2"}`, 0o644},
		"group.json": {`{"account_password":"hunter2"}`, 0o640},
		"bad.json":   {`{"account_password": hunter2}`, 0o600},
		"arr.json":   {`["hunter2"]`, 0o600},
		"name.json":  {`{"bad name":"hunter2"}`, 0o600},
	} {
		_, err := ReadInputsFile(write(name, c.body, c.mode))
		if err == nil {
			t.Errorf("%s accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: error leaks the value: %v", name, err)
		}
	}
	if _, err := ReadInputsFile(dir); err == nil {
		t.Error("directory accepted")
	}
	link := filepath.Join(dir, "link.json")
	_ = os.Symlink(filepath.Join(dir, "ok.json"), link)
	if _, err := ReadInputsFile(link); err == nil {
		t.Error("symlink accepted")
	}
}

func TestDraftViewNeedsValue(t *testing.T) {
	dir := formDraft(t)
	v, err := LoadDraftView(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]bool{}
	for _, in := range v.Inputs {
		got[in.Name] = [2]bool{in.Secret, in.NeedsValue}
	}
	// email/full_name have recorded values; the password is a secret.
	want := map[string][2]bool{"email": {false, false}, "full_name": {false, false}, "account_password": {true, true}}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: secret,needsValue = %v, want %v", k, got[k], w)
		}
	}
	// A required input with neither a recorded value nor a default needs one.
	in := flatInputs(v.ActionDef, map[string]any{})
	if !in[0].NeedsValue {
		t.Errorf("email without recorded value: %+v", in[0])
	}
}
