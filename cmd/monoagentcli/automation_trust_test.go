package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

func TestAutomationTrustAndReplaceBuiltin(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "gemini.mpkg")
	var exp map[string]string
	mustJSON(t, home, &exp, "automation", "export", "gemini", "-o", file)

	// An imported package may not silently replace the built-in.
	out, _, err := runAutomationCLI(t, home, "automation", "install", file, "--yes", "--json")
	if err == nil || !strings.Contains(out, "--replace-builtin") {
		t.Fatalf("install over a built-in without --replace-builtin: err=%v out=%s", err, out)
	}

	var dry map[string]json.RawMessage
	mustJSON(t, home, &dry, "automation", "install", file, "--dry-run", "--replace-builtin")
	var review map[string]json.RawMessage
	if err := json.Unmarshal(dry["review"], &review); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"source", "trust", "replaces", "computedTier", "callActions", "scriptSources", "capabilities"} {
		if _, ok := review[k]; !ok {
			t.Errorf("review missing %q", k)
		}
	}

	var inst automation.InstallResult
	mustJSON(t, home, &inst, "automation", "install", file, "--yes", "--replace-builtin")
	if !inst.Installed {
		t.Fatalf("install = %+v", inst)
	}

	type infoOut struct {
		OK   bool                      `json:"ok"`
		Info *automation.InstalledInfo `json:"info"`
	}
	var got infoOut
	mustJSON(t, home, &got, "automation", "trust", "gemini")
	if got.Info == nil || got.Info.Trust != "imported" || got.Info.ScriptsAllowed || got.Info.LiveRunConfirmed {
		t.Fatalf("imported defaults: %+v", got.Info)
	}
	mustJSON(t, home, &got, "automation", "trust", "gemini", "--scripts", "--live")
	if !got.Info.ScriptsAllowed || !got.Info.LiveRunConfirmed {
		t.Fatalf("after --scripts --live: %+v", got.Info)
	}
	mustJSON(t, home, &got, "automation", "trust", "gemini", "--no-scripts")
	if got.Info.ScriptsAllowed || !got.Info.LiveRunConfirmed {
		t.Fatalf("after --no-scripts: %+v", got.Info)
	}

	// list and show carry the same flags.
	var list struct {
		Automations []map[string]json.RawMessage `json:"automations"`
	}
	mustJSON(t, home, &list, "automation", "list")
	for _, a := range list.Automations {
		for _, k := range []string{"trust", "scriptsAllowed", "liveRunConfirmed"} {
			if _, ok := a[k]; !ok {
				t.Errorf("list row missing %q", k)
			}
		}
	}
	var show struct {
		Info *automation.InstalledInfo `json:"info"`
	}
	mustJSON(t, home, &show, "automation", "show", "gemini")
	if show.Info == nil || show.Info.ScriptsAllowed || !show.Info.LiveRunConfirmed {
		t.Fatalf("show info: %+v", show.Info)
	}

	out, _, err = runAutomationCLI(t, home, "automation", "trust", "gemini", "--live", "--no-live", "--json")
	if err == nil || !strings.Contains(out, "mutually exclusive") {
		t.Fatalf("conflicting flags: err=%v out=%s", err, out)
	}
}

func TestAutomationReviewPrintsScriptSources(t *testing.T) {
	var b strings.Builder
	printScriptSources(&b, map[string]string{"grab.js": "return document.title"})
	s := b.String()
	if !strings.Contains(s, "WARNING") || !strings.Contains(s, "return document.title") || !strings.Contains(s, "scripts/grab.js") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestAutomationInstallLocalAndNewInstall(t *testing.T) {
	home := t.TempDir()
	// --local is refused for an archive.
	file := filepath.Join(home, "gemini.mpkg")
	var exp map[string]string
	mustJSON(t, home, &exp, "automation", "export", "gemini", "-o", file)
	out, _, err := runAutomationCLI(t, home, "automation", "install", file, "--local", "--yes", "--json")
	if err == nil || !strings.Contains(out, "only for a package directory") {
		t.Fatalf("--local on .mpkg: err=%v out=%s", err, out)
	}

	// new --install scaffolds and installs as local.
	dir := filepath.Join(home, "acme")
	var created struct {
		Dir     string                    `json:"dir"`
		Install *automation.InstallResult `json:"install"`
	}
	mustJSON(t, home, &created, "automation", "new", "acme", "--template", "basic", "--dir", dir,
		"--start-url", "https://acme.test/", "--install")
	if created.Dir == "" || created.Install == nil || !created.Install.Installed || created.Install.Review.Trust != "local" {
		t.Fatalf("new --install = %+v", created)
	}
	var info struct {
		Info *automation.InstalledInfo `json:"info"`
	}
	mustJSON(t, home, &info, "automation", "show", "acme")
	if info.Info.Trust != "local" || info.Info.Source != "local" || !info.Info.ScriptsAllowed {
		t.Fatalf("installed as %+v", info.Info)
	}

	// install <dir> --local updates it in place as local; without --local a
	// directory installs as imported and may not replace the local package.
	var res automation.InstallResult
	mustJSON(t, home, &res, "automation", "install", dir, "--local", "--yes")
	if !res.Installed || res.Review.Trust != "local" {
		t.Fatalf("install --local = %+v", res)
	}
	if _, _, err := runAutomationCLI(t, home, "automation", "install", dir, "--yes", "--json"); err == nil {
		t.Fatal("imported install over the local package succeeded without --replace-builtin")
	}
}
