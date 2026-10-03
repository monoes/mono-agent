package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/agentinstall"
	"github.com/monoes/mono-agent/internal/monomind"
)

func TestKiloInstallUsesScanRecipe(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "kilo")
	entry := monomind.ScanEntry{ID: "kilo", InstallHint: "npm install -g @kilocode/cli",
		Install: &monomind.InstallRecipe{Kind: "npm", Packages: []string{"@kilocode/cli"}}, LoginHint: sp("kilo auth login")}
	after := entry
	after.Installed, after.Binary = true, sp(bin)
	f := &fakeMachine{binDir: dir, before: monomind.ScanResult{Agents: []monomind.ScanEntry{entry}},
		after: monomind.ScanResult{Agents: []monomind.ScanEntry{after}}}
	var lines []string
	_, err := installRuntime(context.Background(), f.machine(), "kilo", false, yesScript, func(l string) { lines = append(lines, l) })
	if err != nil {
		t.Fatal(err)
	}
	if len(f.installed) != 1 || f.installed[0].Kind != agentinstall.KindNpm || len(f.installed[0].Packages) != 1 || f.installed[0].Packages[0] != "@kilocode/cli" {
		t.Fatalf("wrong recipe: %+v", f.installed)
	}
	if log := strings.Join(lines, "\n"); !strings.Contains(log, "kilo auth login") {
		t.Fatalf("missing scan login hint: %s", log)
	}
	old := &fakeMachine{before: monomind.ScanResult{Agents: []monomind.ScanEntry{npmEntry("codex", true, "", "")}}}
	_, err = installRuntime(context.Background(), old.machine(), "kilo", false, yesScript, func(string) {})
	if exitCodeFor(err) != 2 || len(old.installed) != 0 {
		t.Fatalf("unknown Kilo installed: %v, %+v", err, old.installed)
	}
}

func TestKiloCoderReadinessUsesCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		installed, full, modern, ready bool
	}{
		{"no full access", true, false, true, false},
		{"not installed", false, true, true, false},
		{"old monomind", true, true, false, false},
		{"future supported runner", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := newChatCLITestDB(t)
			caps := monomind.CoderCapabilities
			if tc.modern {
				caps = allRuntimeCaps
			}
			withCoderCaps(t, caps...)
			withCoderScan(t, monomind.ScanEntry{ID: "kilo", Installed: tc.installed, FullAccess: tc.full})
			out, code := runCoderCLI(t, dbPath, "status")
			if code != 0 {
				t.Fatalf("status: exit %d %s", code, out)
			}
			var got struct {
				Runtimes []struct {
					ID         string `json:"id"`
					Ready      bool   `json:"ready"`
					Resume     bool   `json:"resume"`
					InitTarget string `json:"initTarget"`
				} `json:"runtimes"`
			}
			decodeChatJSON(t, out, &got)
			if len(got.Runtimes) != 1 || got.Runtimes[0].ID != "kilo" || got.Runtimes[0].Ready != tc.ready || got.Runtimes[0].Resume || got.Runtimes[0].InitTarget != "" {
				t.Fatalf("capabilities invented or ignored: %+v", got)
			}
		})
	}
}
