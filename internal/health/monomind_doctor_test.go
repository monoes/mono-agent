package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func sp(s string) *string { return &s }

func doctorEnv(t *testing.T, caps []string, rep *monomind.DoctorReport) (*Env, *[]monomind.DoctorOptions) {
	t.Helper()
	root := t.TempDir()
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
	if byKey["monomind.doctor.claude"].FixID != FixMonomindDoctorInstall+":claude" {
		t.Errorf("confirm fix: %+v", byKey["monomind.doctor.claude"])
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
	if f := res.Children[1].FixID; f != FixMonomindDoctor+":helpers@codes/app" {
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
	if _, ok := byID(reg.Run(context.Background(), env, Options{Groups: []string{GroupMonomind}, OnDemand: true}))[CheckMonomindProjects]; !ok {
		t.Error("monomind.projects must run with OnDemand")
	}
}
