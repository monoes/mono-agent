package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func sp(s string) *string { return &s }

func doctorEnv(t *testing.T, caps []string, rep *monomind.DoctorReport) (*Env, *[]monomind.DoctorOptions) {
	t.Helper()
	root := t.TempDir()
	// A profile folder set up for monomind (the checks skip one that isn't).
	os.MkdirAll(filepath.Join(root, ".monomind"), 0o755)
	os.WriteFile(filepath.Join(root, ".monomind", "config.yaml"), []byte("x"), 0o644)
	var calls []monomind.DoctorOptions
	env := &Env{
		ProfileRoot: func(string) string { return root },
		MonomindHandshake: func(context.Context) (*monomind.VersionInfo, error) {
			return &monomind.VersionInfo{V: 1, Version: "2.16.1", Capabilities: caps}, nil
		},
		MonomindDoctor: func(_ context.Context, o monomind.DoctorOptions) (*monomind.DoctorReport, error) {
			calls = append(calls, o)
			return rep, nil
		},
	}
	return env, &calls
}

func sampleDoctor() *monomind.DoctorReport {
	rep := &monomind.DoctorReport{V: 1, Results: []monomind.DoctorResult{
		{Component: "node", Name: "Node.js Version", Status: "pass", Message: "v24"},
		{Component: "helpers", Name: "Helper Files", Status: "warn", Message: "3 stale", Fix: sp("monomind init upgrade"), FixSafety: sp("auto"), FixFlag: sp("--fix")},
		{Component: "claude", Name: "Claude Code CLI", Status: "fail", Message: "missing", Fix: sp("npm i -g …"), FixSafety: sp("confirm"), FixFlag: sp("--install")},
		{Component: "kg", Name: "Knowledge Graph", Status: "warn", Message: "empty", Fix: sp("run org_learn"), FixSafety: sp("manual")},
	}}
	rep.Summary.Passed, rep.Summary.Warnings, rep.Summary.Failed = 1, 2, 1
	return rep
}

func TestMonomindDoctorRows(t *testing.T) {
	env, calls := doctorEnv(t, []string{monomind.CapDoctorJSON}, sampleDoctor())
	res := checkMonomindDoctor(context.Background(), env)
	if res.Status != StatusFail || res.Source != "monomind" || len(res.Children) != 4 {
		t.Fatalf("parent: %+v", res)
	}
	byKey := map[string]Result{}
	for _, c := range res.Children {
		byKey[c.ID] = c
	}
	if byKey["monomind.doctor.helpers"].FixID != FixMonomindDoctor+":helpers" {
		t.Errorf("auto fix: %+v", byKey["monomind.doctor.helpers"])
	}
	// Installing Claude Code goes through monoagent's runtime install.
	if byKey["monomind.doctor.claude"].FixID != FixRuntimeInstall+":claude" {
		t.Errorf("claude install fix: %+v", byKey["monomind.doctor.claude"])
	}
	if kg := byKey["monomind.doctor.kg"]; kg.FixID != "" || kg.Detail != "to fix: run org_learn" {
		t.Errorf("manual hint: %+v", kg)
	}
	if (*calls)[0].Dir != env.ProfileRoot("") {
		t.Errorf("ran in %q", (*calls)[0].Dir)
	}
}

func TestMonomindDoctorNeedsCapability(t *testing.T) {
	env, calls := doctorEnv(t, nil, sampleDoctor())
	if res := checkMonomindDoctor(context.Background(), env); res.Status != StatusInfo || len(*calls) != 0 {
		t.Fatalf("old monomind: %+v, calls %d", res, len(*calls))
	}
}

func TestMonomindProjectsAndFix(t *testing.T) {
	rep := sampleDoctor()
	env, calls := doctorEnv(t, []string{monomind.CapDoctorJSON}, rep)
	root := env.ProfileRoot("")
	proj := filepath.Join(root, "codes", "app", ".monomind")
	os.MkdirAll(proj, 0o755)
	os.WriteFile(filepath.Join(proj, "config.yaml"), []byte("x"), 0o644)
	env.MonomindProjects = func() []string { return []string{filepath.Join("codes", "app")} }

	res := checkMonomindProjects(context.Background(), env)
	// project summary + helpers, claude, kg (non-pass rows only)
	if len(res.Children) != 4 || res.Children[0].ID != "monomind.project.codes/app" {
		t.Fatalf("children: %+v", res.Children)
	}
	// Inside a user's project a monomind fix is asked about first.
	if f := res.Children[1].FixID; f != FixMonomindDoctorConfirm+":helpers@codes/app" {
		t.Fatalf("project fix id %q", f)
	}

	rep.Fixes = append(rep.Fixes, struct {
		Component string `json:"component"`
		Outcome   string `json:"outcome"`
	}{"helpers", "applied"})
	if err := fixMonomindComponent(context.Background(), env, "helpers@codes/app", false, noop); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last.Dir != filepath.Join(root, "codes", "app") || last.Component != "helpers" || !last.Fix || last.Install {
		t.Fatalf("fix ran with %+v", last)
	}
	for _, bad := range []string{"helpers@../x", "helpers@/etc", "helpers@missing"} {
		if err := fixMonomindComponent(context.Background(), env, bad, false, noop); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	// A component monomind has no automatic fix for is reported, not faked.
	if err := fixMonomindComponent(context.Background(), env, "kg", false, noop); err == nil {
		t.Error("kg has no fix outcome: want an error")
	}
}

func TestProjectsCheckIsOnDemand(t *testing.T) {
	reg := Default()
	env := &Env{}
	if _, ok := byID(reg.Run(context.Background(), env, Options{Groups: []string{GroupMonomind}}))[CheckMonomindProjects]; ok {
		t.Error("monomind.projects must not run without OnDemand")
	}
	if _, ok := byID(reg.Run(context.Background(), env, Options{Groups: []string{GroupMonomind}, OnDemand: true, Deep: true}))[CheckMonomindProjects]; !ok {
		t.Error("monomind.projects must run with OnDemand")
	}
}

// A folder not set up for monomind is skipped: its rows would all be "not
// set up", and its automatic fixes would write monomind's files there.
func TestMonomindDoctorSkipsAFolderNotSetUp(t *testing.T) {
	env, calls := doctorEnv(t, []string{monomind.CapDoctorJSON}, sampleDoctor())
	bare := t.TempDir()
	env.ProfileRoot = func(string) string { return bare }
	if res := checkMonomindDoctor(context.Background(), env); res.Status != StatusSkip || len(*calls) != 0 {
		t.Fatalf("not set up: %+v, monomind ran %d times", res, len(*calls))
	}
}

// Plain `doctor` doesn't run monomind's doctor (it writes and uses the
// network); --deep does.
func TestMonomindDoctorOnlyWithDeep(t *testing.T) {
	for _, c := range monomindDoctorChecks() {
		if !c.Network {
			t.Errorf("%s must be a --deep check", c.ID)
		}
	}
}

// monomind's own "auto" label is not trusted on its own: only listed
// components in the profile folder are applied without asking; sudo
// installers are never run; the auto fix refuses anything else by id.
func TestMonomindFixSafety(t *testing.T) {
	row := func(component, safety, project string) Result {
		return doctorRow(monomind.DoctorResult{Component: component, Name: component, Status: "warn", Message: "x",
			Fix: sp("do it"), FixSafety: sp(safety)}, "k", project)
	}
	if id := row("appledouble", "auto", "").FixID; id != FixMonomindDoctorConfirm+":appledouble" {
		t.Errorf("appledouble (deletes files): %q, want a confirm fix", id)
	}
	if r := row("monoes-tools", "auto", ""); r.FixID != "" || !strings.Contains(r.Detail, "do it") {
		t.Errorf("monoes-tools (sudo): %+v, want manual only", r)
	}
	if id := row("", "auto", "").FixID; id != "" {
		t.Errorf("empty component got fix %q; it would run every monomind fix", id)
	}
	env, _ := doctorEnv(t, []string{monomind.CapDoctorJSON}, sampleDoctor())
	reg := Default()
	for _, bad := range []string{"appledouble", "monoes-tools", "helpers@codes/app"} {
		f, ok := reg.Fix(FixMonomindDoctor + ":" + bad)
		if !ok {
			t.Fatalf("fix %s not registered", bad)
		}
		if err := f.Apply(context.Background(), env, noop); err == nil {
			t.Errorf("auto fix ran %s without asking", bad)
		}
	}
	if err := fixMonomindComponent(context.Background(), env, "", false, noop); err == nil {
		t.Error("an empty component ran a full monomind doctor --fix")
	}
}

// One component reporting several results gives each its own row id.
func TestMonomindRowIDsAreUnique(t *testing.T) {
	keys := rowKeys([]monomind.DoctorResult{{Component: "docs"}, {Component: "docs"}, {Component: "node"}, {}})
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("duplicate key %q in %v", k, keys)
		}
		seen[k] = true
	}
	if keys[2] != "node" {
		t.Errorf("a unique component keeps its plain key: %v", keys)
	}
}

// A symlink inside the profile folder cannot take a fix outside it.
func TestMonomindFixStaysInsideThroughSymlinks(t *testing.T) {
	env, calls := doctorEnv(t, []string{monomind.CapDoctorJSON}, sampleDoctor())
	root := env.ProfileRoot("")
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(outside, ".monomind"), 0o755)
	os.WriteFile(filepath.Join(outside, ".monomind", "config.yaml"), []byte("x"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	before := len(*calls)
	if err := fixMonomindComponent(context.Background(), env, "gitignore@link", false, noop); err == nil {
		t.Fatal("a fix ran through a symlink out of the profile folder")
	}
	if len(*calls) != before {
		t.Fatal("monomind ran outside the profile folder")
	}
}

// --projects (Options.Monomind) runs monomind's checks without --deep, and
// nothing else that needs --deep.
func TestMonomindOptionRunsOnlyMonomindNetworkChecks(t *testing.T) {
	opts := Options{OnDemand: true, Monomind: true}
	for _, c := range Default().checks {
		if !c.Network {
			continue
		}
		if got, want := opts.selects(c), c.Group == GroupMonomind; got != want {
			t.Errorf("%s (group %s): selected %v, want %v", c.ID, c.Group, got, want)
		}
	}
}
