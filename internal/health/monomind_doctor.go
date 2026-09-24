package health

import (
	"context"
	"errors"
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
	FixMonomindDoctorConfirm = "monomind.doctor.confirm" // confirm: the same, asked first
	FixMonomindDoctorInstall = "monomind.doctor.install" // confirm: `... --install`
)

// autoComponents are the monomind fixes monoagent applies without asking,
// and only in the profile folder: they write the profile's own files and
// can be run again. monomind labels more of its fixes "auto" (appledouble
// deletes files; monoes-tools runs sudo on macOS), and fixes inside a
// user's project touch their repository, so those are asked about or left
// to the person — monomind's own label is not trusted on its own.
var autoComponents = map[string]bool{"helpers": true, "gitignore": true}

// manualComponents are never run from monoagent (monoes-tools installs
// with sudo).
var manualComponents = map[string]bool{"monoes-tools": true}

const monomindDoctorTimeout = 150 * time.Second

func monomindDoctorChecks() []Check {
	return []Check{
		// Network: `monomind doctor` itself writes .monomind/registry.json
		// and asks npm for the latest version, so it runs only with --deep
		// (or --check), keeping plain `doctor` free of writes and traffic.
		{ID: CheckMonomindDoctor, Group: GroupMonomind, Title: "monomind checks", Network: true,
			DependsOn: []string{CheckMonomindProfileInit}, Timeout: monomindDoctorTimeout, Run: checkMonomindDoctor},
		{ID: CheckMonomindProjects, Group: GroupMonomind, Title: "monomind projects", OnDemand: true, Network: true,
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
			Command: "monomind doctor -c {arg} --fix"}, ApplyArg: func(ctx context.Context, env *Env, arg string, progress func(string)) error {
			component, project, _ := strings.Cut(arg, "@")
			if project != "" || !autoComponents[component] {
				return fmt.Errorf("%s is not a fix monoagent applies without asking; use %s:%s", arg, FixMonomindDoctorConfirm, arg)
			}
			return fixMonomindComponent(ctx, env, arg, false, progress)
		}},
		{FixInfo: FixInfo{ID: FixMonomindDoctorConfirm, Label: "Fix {arg} (monomind)", Safety: SafetyConfirm,
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
	// Its checks are about a folder set up for monomind; in one that is
	// not, every row is "not set up yet" and its automatic fixes would
	// write monomind's files there although setting it up was declined.
	if !monomind.IsInitializedAt(root) {
		return Result{Status: StatusSkip, Summary: "this profile's folder is not set up for monomind yet"}
	}
	rep, err := env.MonomindDoctor(ctx, monomind.DoctorOptions{Dir: root})
	if errors.Is(err, monomind.ErrDoctorFormat) {
		return Result{Status: StatusInfo, Summary: "monomind reports its checks in a newer format — update monoagent to see them", Detail: err.Error()}
	}
	if err != nil {
		return Result{Status: StatusWarn, Summary: "monomind doctor did not run", Detail: err.Error()}
	}
	res := summarizeDoctor(rep)
	res.Source = "monomind"
	keys := rowKeys(rep.Results)
	for i, r := range rep.Results {
		res.Children = append(res.Children, doctorRow(r, "monomind.doctor."+keys[i], ""))
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
		keys := rowKeys(rep.Results)
		for i, r := range rep.Results {
			if r.Status == "pass" || r.Status == "info" {
				continue
			}
			row := doctorRow(r, key+"."+keys[i], rel)
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
	manual := func() Result {
		row.Detail = strings.TrimSpace(row.Detail + "\nto fix: " + *r.Fix)
		return row
	}
	switch {
	case r.Component == "" || !validComponent(r.Component) || manualComponents[r.Component]:
		return manual()
	case r.Component == "claude" && deref(r.FixSafety, "") == "confirm":
		// Installing Claude Code is a runtime install: the path that has
		// no terminal, a deadline and the real command in its confirmation.
		row.FixID = FixRuntimeInstall + ":claude"
	case deref(r.FixSafety, "manual") == "auto" && project == "" && autoComponents[r.Component]:
		row.FixID = FixMonomindDoctor + ":" + arg
	case deref(r.FixSafety, "manual") == "auto":
		row.FixID = FixMonomindDoctorConfirm + ":" + arg
	case deref(r.FixSafety, "manual") == "confirm":
		row.FixID = FixMonomindDoctorInstall + ":" + arg
	default:
		return manual()
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

// rowKeys gives each result a stable, unique key: its component, with the
// index added when one component reports several results (monomind's
// protocol allows it) or none.
func rowKeys(results []monomind.DoctorResult) []string {
	count := map[string]int{}
	for _, r := range results {
		count[r.Component]++
	}
	keys := make([]string, len(results))
	for i, r := range results {
		switch {
		case r.Component == "":
			keys[i] = fmt.Sprintf("%d", i)
		case count[r.Component] > 1:
			keys[i] = fmt.Sprintf("%s.%d", r.Component, i)
		default:
			keys[i] = r.Component
		}
	}
	return keys
}

// validComponent is a monomind component name as it may appear in a fix
// id and on monomind's command line.
func validComponent(c string) bool {
	if c == "" || len(c) > 64 {
		return false
	}
	for _, r := range c {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
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
	// Without a component `monomind doctor --fix` applies every fix it has.
	if !validComponent(component) || manualComponents[component] {
		return fmt.Errorf("%q is not a monomind fix monoagent runs", component)
	}
	root := env.ProfileRoot(env.profileID())
	dir := root
	if project != "" {
		rel := filepath.Clean(filepath.FromSlash(project))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("project %q is not inside the profile folder", project)
		}
		dir = filepath.Join(root, rel)
		// The text check above is not enough: a symlink inside the folder
		// can point anywhere.
		realRoot, err1 := filepath.EvalSymlinks(root)
		realDir, err2 := filepath.EvalSymlinks(dir)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("project %q is not inside the profile folder", project)
		}
		if r, err := filepath.Rel(realRoot, realDir); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
			return fmt.Errorf("project %q is not inside the profile folder", project)
		}
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
