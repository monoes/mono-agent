package health

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// monomind's own checks (`monomind doctor --json`, capability doctor-json)
// imported as rows: once for the active profile's folder, and on demand for
// every monomind project inside it.
const (
	CheckMonomindDoctor   = "monomind.doctor"
	CheckMonomindProjects = "monomind.projects"

	// Parameterized by "<component>" (profile folder) or
	// "<component>@<project path relative to the profile folder>".
	FixMonomindDoctor        = "monomind.doctor.fix"     // auto: `monomind doctor -c <component> --fix`
	FixMonomindDoctorInstall = "monomind.doctor.install" // confirm: `... --install`
)

const monomindDoctorTimeout = 150 * time.Second

func monomindDoctorChecks() []Check {
	return []Check{
		{ID: CheckMonomindDoctor, Group: GroupMonomind, Title: "monomind checks",
			DependsOn: []string{CheckMonomindProfileInit}, Timeout: monomindDoctorTimeout, Run: checkMonomindDoctor},
		{ID: CheckMonomindProjects, Group: GroupMonomind, Title: "monomind projects", OnDemand: true,
			DependsOn: []string{CheckMonomindHandshake, CheckProfile}, Timeout: 10 * time.Minute, Run: checkMonomindProjects},
	}
}

func monomindDoctorFixes() []Fix {
	apply := func(install bool) func(context.Context, *Env, string, func(string)) error {
		return func(ctx context.Context, env *Env, arg string, progress func(string)) error {
			return fixMonomindComponent(ctx, env, arg, install, progress)
		}
	}
	return []Fix{
		{FixInfo: FixInfo{ID: FixMonomindDoctor, Label: "Fix {arg} (monomind)", Safety: SafetyAuto,
			Command: "monomind doctor -c {arg} --fix"}, ApplyArg: apply(false)},
		{FixInfo: FixInfo{ID: FixMonomindDoctorInstall, Label: "Install for {arg} (monomind)", Safety: SafetyConfirm,
			Command: "monomind doctor -c {arg} --install", Optional: true}, ApplyArg: apply(true)},
	}
}

func hasDoctorJSON(ctx context.Context, env *Env) (bool, string) {
	if env.MonomindHandshake == nil || env.MonomindDoctor == nil || env.ProfileRoot == nil {
		return false, ""
	}
	vi, err := env.MonomindHandshake(ctx)
	if err != nil {
		return false, ""
	}
	return vi.HasCapability(monomind.CapDoctorJSON), vi.Version
}

func checkMonomindDoctor(ctx context.Context, env *Env) Result {
	ok, version := hasDoctorJSON(ctx, env)
	if !ok {
		if version == "" {
			return Result{Status: StatusSkip, Summary: "not available"}
		}
		return Result{Status: StatusInfo, Summary: fmt.Sprintf("monomind %s can't report its checks here — update it to see them", version),
			Detail: "needs monomind with capability " + monomind.CapDoctorJSON + ": npm install -g " + MonomindPackage}
	}
	root := env.ProfileRoot(env.profileID())
	rep, err := env.MonomindDoctor(ctx, monomind.DoctorOptions{Dir: root})
	if err != nil {
		return Result{Status: StatusWarn, Summary: "monomind doctor did not run", Detail: err.Error()}
	}
	res := summarizeDoctor(rep)
	res.Source = "monomind"
	for i, r := range rep.Results {
		res.Children = append(res.Children, doctorRow(r, "monomind.doctor."+rowKey(r, i), ""))
	}
	return res
}

func checkMonomindProjects(ctx context.Context, env *Env) Result {
	ok, version := hasDoctorJSON(ctx, env)
	if !ok {
		return Result{Status: StatusInfo, Summary: fmt.Sprintf("monomind %s can't report its checks here — update it", version)}
	}
	if env.MonomindProjects == nil {
		return Result{Status: StatusSkip, Summary: "not available"}
	}
	projects := env.MonomindProjects()
	if len(projects) == 0 {
		return Result{Status: StatusInfo, Summary: "no monomind projects inside this profile's folder"}
	}
	root := env.ProfileRoot(env.profileID())
	res := Result{Source: "monomind"}
	worst := StatusOK
	for _, rel := range projects {
		rep, err := env.MonomindDoctor(ctx, monomind.DoctorOptions{Dir: filepath.Join(root, rel)})
		key := "monomind.project." + filepath.ToSlash(rel)
		if err != nil {
			res.Children = append(res.Children, Result{ID: key, Title: rel, Status: StatusWarn, Summary: "monomind doctor did not run", Detail: err.Error()})
			worst = worse(worst, StatusWarn)
			continue
		}
		summary := summarizeDoctor(rep)
		summary.ID, summary.Title = key, rel
		res.Children = append(res.Children, summary)
		worst = worse(worst, summary.Status)
		// Only the rows that need attention, per project.
		for i, r := range rep.Results {
			if r.Status == "pass" || r.Status == "info" {
				continue
			}
			row := doctorRow(r, key+"."+rowKey(r, i), rel)
			row.Title = rel + ": " + row.Title
			res.Children = append(res.Children, row)
		}
	}
	res.Status = worst
	res.Summary = fmt.Sprintf("%d project(s) checked", len(projects))
	return res
}

// summarizeDoctor turns a report's counts into a row (fail stays fail;
// monomind's checks never count as required for monoagent).
func summarizeDoctor(rep *monomind.DoctorReport) Result {
	s := rep.Summary
	res := Result{Summary: fmt.Sprintf("%d passed · %d warnings · %d failed", s.Passed, s.Warnings, s.Failed)}
	switch {
	case s.Failed > 0:
		res.Status = StatusFail
	case s.Warnings > 0:
		res.Status = StatusWarn
	default:
		res.Status = StatusOK
	}
	return res
}

// doctorRow maps one monomind result. project is "" for the profile folder.
func doctorRow(r monomind.DoctorResult, id, project string) Result {
	row := Result{ID: id, Title: r.Name, Source: "monomind"}
	switch r.Status {
	case "pass":
		row.Status = StatusOK
	case "warn":
		row.Status = StatusWarn
	case "fail":
		row.Status = StatusFail
	default:
		row.Status = StatusInfo
	}
	row.Summary, row.Detail = splitMessage(r.Message)
	if r.Fix == nil || row.Status == StatusOK {
		return row
	}
	arg := r.Component
	if project != "" {
		arg += "@" + filepath.ToSlash(project)
	}
	switch deref(r.FixSafety, "manual") {
	case "auto":
		row.FixID = FixMonomindDoctor + ":" + arg
	case "confirm":
		row.FixID = FixMonomindDoctorInstall + ":" + arg
	default:
		row.Detail = strings.TrimSpace(row.Detail + "\nto fix: " + *r.Fix)
	}
	return row
}

// splitMessage keeps the row one line; the rest goes to the detail.
func splitMessage(msg string) (summary, detail string) {
	msg = strings.TrimSpace(msg)
	first, rest, _ := strings.Cut(msg, "\n")
	if len(first) > 110 {
		return first[:107] + "…", msg
	}
	return first, strings.TrimSpace(rest)
}

func rowKey(r monomind.DoctorResult, i int) string {
	if r.Component == "" {
		return fmt.Sprintf("%d", i)
	}
	return r.Component
}

func worse(a, b Status) Status {
	rank := map[Status]int{StatusOK: 0, StatusInfo: 0, StatusSkip: 0, StatusWarn: 1, StatusFail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// fixMonomindComponent runs one monomind component's fix and checks that
// monomind reports it applied.
func fixMonomindComponent(ctx context.Context, env *Env, arg string, install bool, progress func(string)) error {
	if env.MonomindDoctor == nil || env.ProfileRoot == nil {
		return fmt.Errorf("monomind doctor is not available here")
	}
	component, project, _ := strings.Cut(arg, "@")
	root := env.ProfileRoot(env.profileID())
	dir := root
	if project != "" {
		rel := filepath.Clean(filepath.FromSlash(project))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("project %q is not inside the profile folder", project)
		}
		dir = filepath.Join(root, rel)
		if !monomind.IsInitializedAt(dir) {
			return fmt.Errorf("%s is not a monomind project", dir)
		}
	}
	flag := "--fix"
	if install {
		flag = "--install"
	}
	progress(fmt.Sprintf("monomind doctor -c %s %s (in %s)", component, flag, dir))
	rep, err := env.MonomindDoctor(ctx, monomind.DoctorOptions{Dir: dir, Component: component, Fix: !install, Install: install})
	if err != nil {
		return err
	}
	applied := false
	for _, f := range rep.Fixes {
		if f.Component == component {
			if f.Outcome != "applied" {
				return fmt.Errorf("monomind could not fix %s", component)
			}
			applied = true
		}
	}
	for _, r := range rep.Results {
		progress(fmt.Sprintf("%s: %s — %s", r.Name, r.Status, firstLine(r.Message)))
	}
	if !applied {
		return fmt.Errorf("monomind has no automatic fix for %s", component)
	}
	return nil
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return first
}
