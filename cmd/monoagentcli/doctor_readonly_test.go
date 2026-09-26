package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/health"
	"github.com/monoes/mono-agent/internal/monomind"
)

// mutatingHooks change the machine; a check must never call one (only
// fixes do).
var mutatingHooks = map[string]bool{
	"Migrate": true, "EnsureProfile": true, "InstallNode": true, "UpdateNode": true, "RemoveNode": true,
	"InstallMonomind": true, "InitMonomindProfile": true, "InstallRuntime": true, "InstallAutostart": true,
	"StartDaemon": true, "InstallClaudeSkills": true, "RegisterMCP": true, "RefreshConnection": true,
}

// readOnlyHooks only read files under HOME or the database; they stay real
// so the test sees what they do.
var readOnlyHooks = map[string]bool{
	"PendingMigrations": true, "QuickCheck": true, "VaultState": true, "ProfileRoot": true, "FreeBytes": true,
	"ManagedNode": true, "FindBrowser": true, "ExtensionInstalled": true, "ExtensionDir": true, "Daemon": true,
	"ClaudeSkills": true, "MCPRegistration": true, "Connections": true, "AIProviders": true, "LoginSessions": true,
	"MonomindProjects": true, "Automations": true,
}

// offlineEnv replaces every hook that starts a process or uses the network
// with a canned answer, and every other hook that isn't known to be
// read-only with one that records its call and returns zero values — so a
// hook added later is covered without editing this list.
// keep names hooks to leave real (the fixes that set a home up).
func offlineEnv(t *testing.T, called *sync.Map, keep ...string) func(*health.Env) {
	vi := &monomind.VersionInfo{V: monomind.ProtocolVersion, Version: "9.9.9", Capabilities: []string{monomind.CapDoctorJSON}}
	fakes := map[string]any{
		"LoginPath":          func(context.Context) (string, error) { return "/usr/bin:/bin", nil },
		"LatestVersion":      func(context.Context) (string, error) { return "v0.0.0", nil },
		"SystemNode":         func(context.Context) (string, string, bool) { return "/usr/bin/node", "24.1.0", true },
		"FindMonomind":       func() (string, error) { return "/fake/monomind", nil },
		"MonomindCandidates": func() []string { return []string{"/fake/monomind"} },
		"MonomindHandshake":  func(context.Context) (*monomind.VersionInfo, error) { return vi, nil },
		"ScanRuntimes": func(context.Context) (*monomind.ScanResult, error) {
			return &monomind.ScanResult{V: 1, Agents: []monomind.ScanEntry{{ID: "claude", InstallHint: "npm install -g @anthropic-ai/claude-code"}}}, nil
		},
		"MonomindDoctor": func(context.Context, monomind.DoctorOptions) (*monomind.DoctorReport, error) {
			fix, safety := "monomind doctor -c helpers --fix", "auto"
			return &monomind.DoctorReport{V: 1, Results: []monomind.DoctorResult{
				{Component: "helpers", Name: "Helpers", Status: "warn", Message: "missing", Fix: &fix, FixSafety: &safety}}}, nil
		},
		"Bridge": func(context.Context) (health.BridgeInfo, bool) {
			return health.BridgeInfo{Addr: "127.0.0.1:1", Status: "waiting", PID: 1}, true
		},
		"APIHealth":       func(context.Context, string) error { return nil },
		"AutostartStatus": func(context.Context) (bool, string) { return false, "" },
	}
	return func(env *health.Env) {
		v := reflect.ValueOf(env).Elem()
		for i := 0; i < v.NumField(); i++ {
			f, name := v.Field(i), v.Type().Field(i).Name
			if f.Kind() != reflect.Func || readOnlyHooks[name] || slices.Contains(keep, name) {
				continue
			}
			if fake, ok := fakes[name]; ok {
				f.Set(reflect.ValueOf(fake))
				continue
			}
			if f.IsNil() {
				continue
			}
			ft := f.Type()
			f.Set(reflect.MakeFunc(ft, func([]reflect.Value) []reflect.Value {
				called.Store(name, true)
				out := make([]reflect.Value, ft.NumOut())
				for j := range out {
					out[j] = reflect.Zero(ft.Out(j))
				}
				return out
			}))
		}
		if missing := unknownFakes(v, fakes); len(missing) > 0 {
			t.Fatalf("fakes for hooks that no longer exist: %v", missing)
		}
	}
}

func unknownFakes(v reflect.Value, fakes map[string]any) []string {
	var out []string
	for name := range fakes {
		if !v.FieldByName(name).IsValid() {
			out = append(out, name)
		}
	}
	return out
}

// snapshot lists every file under root with its size and modification
// time, and every folder. A folder's own mtime is left out: opening the
// database creates SQLite's -wal/-shm files and closing it removes them,
// which touches the folder without changing anything in it.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			files[rel] = fmt.Sprintf("%v dir", info.Mode())
			return nil
		}
		files[rel] = fmt.Sprintf("%v %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return files
}

func diffSnapshots(before, after map[string]string) []string {
	var diffs []string
	for p, a := range after {
		if b, ok := before[p]; !ok {
			diffs = append(diffs, "created "+p)
		} else if a != b {
			diffs = append(diffs, "changed "+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			diffs = append(diffs, "removed "+p)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// Every group's checks — with --deep and --projects, so the network and
// on-demand ones run too — change nothing under HOME and call no hook that
// changes the machine, both on a fresh home and on a set-up one. Hooks
// that start processes or use the network are faked, and PATH is empty.
func TestDoctorChangesNothingInAnyGroup(t *testing.T) {
	keyring.MockInit()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv(monomind.EnvOverride, "")
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", "")
	var called sync.Map
	healthEnvHook = offlineEnv(t, &called)
	t.Cleanup(func() { healthEnvHook = nil })

	groups := map[string]bool{}
	for _, c := range health.Default().Checks() {
		groups[c.Group] = true
	}
	runs := [][]string{{}}
	for g := range groups {
		runs = append(runs, []string{"--group", g})
	}
	check := func(stage string) {
		for _, extra := range runs {
			before := snapshot(t, home)
			args := append([]string{"doctor", "--json", "--deep", "--projects"}, extra...)
			out, _ := runDoctor(t, home, args...)
			decodeDoctor(t, out)
			if d := diffSnapshots(before, snapshot(t, home)); len(d) > 0 {
				t.Errorf("%s: doctor %s changed HOME:\n%s", stage, strings.Join(extra, " "), strings.Join(d, "\n"))
			}
		}
		called.Range(func(k, _ any) bool {
			if mutatingHooks[k.(string)] {
				t.Errorf("%s: a check called %s", stage, k)
			}
			return true
		})
	}
	check("fresh home")

	// Set up: data folder, database, profile folder (these are fixes).
	var setup sync.Map
	healthEnvHook = offlineEnv(t, &setup, "Migrate", "EnsureProfile")
	if out, err := runDoctor(t, home, "doctor", "--json", "--group", "core", "--fix"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	healthEnvHook = offlineEnv(t, &called)
	called = sync.Map{}
	check("set-up home")
}
