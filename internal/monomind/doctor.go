package monomind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CapDoctorJSON: `monomind doctor --json` (protocol rev 9, §10).
const CapDoctorJSON = "doctor-json"

// DoctorTimeout bounds one `monomind doctor` run (some checks shell out to
// git, npm and the network).
const DoctorTimeout = 2 * time.Minute

// DoctorResult is one row of `monomind doctor --json`.
type DoctorResult struct {
	Component string  `json:"component"`
	Name      string  `json:"name"`
	Status    string  `json:"status"` // pass | warn | fail | info
	Message   string  `json:"message"`
	Fix       *string `json:"fix"`
	FixSafety *string `json:"fix_safety"` // auto | confirm | manual
	FixFlag   *string `json:"fix_flag"`   // --fix | --install
}

// DoctorReport is the `monomind doctor --json` payload (v1).
type DoctorReport struct {
	V       int  `json:"v"`
	Success bool `json:"success"`
	Summary struct {
		Passed   int `json:"passed"`
		Warnings int `json:"warnings"`
		Failed   int `json:"failed"`
		Info     int `json:"info"`
	} `json:"summary"`
	Results []DoctorResult `json:"results"`
	Fixes   []struct {
		Component string `json:"component"`
		Outcome   string `json:"outcome"`
	} `json:"fixes"`
}

// DoctorOptions selects what `monomind doctor` does.
type DoctorOptions struct {
	Dir       string // project folder to check (the doctor's cwd)
	Component string // "" = all checks
	Fix       bool   // --fix (auto fixes)
	Install   bool   // --install (confirm fixes)
}

// Doctor runs `monomind doctor --json` in opts.Dir. A failing check makes
// monomind exit 1 but still print the report, so the report is parsed
// whatever the exit status.
func Doctor(ctx context.Context, bin string, opts DoctorOptions) (*DoctorReport, error) {
	args := []string{"doctor", "--json"}
	if opts.Component != "" {
		args = append(args, "-c", opts.Component)
	}
	if opts.Fix {
		args = append(args, "--fix")
	}
	if opts.Install {
		args = append(args, "--install")
	}
	ctx, cancel := context.WithTimeout(ctx, DoctorTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = opts.Dir
	cmd.Env = append(os.Environ(), "CI=true") // never prompt
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var rep DoctorReport
	if err := json.Unmarshal(out, &rep); err != nil || rep.V == 0 {
		if runErr == nil {
			runErr = errors.New("output is not a doctor report")
		}
		return nil, fmt.Errorf("monomind doctor in %s: %w: %s", opts.Dir, runErr, lastLines(stderr.String(), 3))
	}
	return &rep, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// skipDirs are never descended into when looking for projects.
var skipDirs = map[string]bool{"node_modules": true, ".git": true, ".monomind": true, ".monoagent": true,
	".claude": true, "dist": true, "build": true, "vendor": true, ".venv": true}

// ProjectsUnder lists the monomind projects inside root (not root itself):
// folders up to three levels down that `monomind init` set up, plus
// monomind's own registered projects (~/.monomind-projects.json) under
// root. Sorted, de-duplicated, absolute.
func ProjectsUnder(root string) []string {
	root = filepath.Clean(root)
	found := map[string]bool{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || skipDirs[e.Name()] {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if IsInitializedAt(p) {
				found[p] = true
			}
			walk(p, depth+1)
		}
	}
	walk(root, 1)
	for _, p := range registeredProjects() {
		p = filepath.Clean(p)
		if strings.HasPrefix(p, root+string(filepath.Separator)) && IsInitializedAt(p) {
			found[p] = true
		}
	}
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// registeredProjects reads ~/.monomind-projects.json (written by monomind's
// `init upgrade --all`); nil when absent or unreadable.
func registeredProjects() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".monomind-projects.json"))
	if err != nil {
		return nil
	}
	var f struct {
		Projects []string `json:"projects"`
	}
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return f.Projects
}
