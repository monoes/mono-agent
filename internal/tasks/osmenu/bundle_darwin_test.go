//go:build darwin

package osmenu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Apple's plutil validates both files and reads back the menu item and the
// script exactly as rendered, a hostile profile name included.
func TestPlutilReadsWhatWasRendered(t *testing.T) {
	const plutil = "/usr/bin/plutil"
	if _, err := os.Stat(plutil); err != nil {
		t.Skip("no plutil")
	}
	spec := testSpec()
	spec.ProfileName = hostileName
	b, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(t.TempDir(), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(res.Path, filepath.FromSlash(InfoPath))
	doc := filepath.Join(res.Path, filepath.FromSlash(DocumentPath))
	if out, err := exec.Command(plutil, "-lint", info, doc).CombinedOutput(); err != nil || strings.Count(string(out), ": OK") != 2 {
		t.Fatalf("plutil -lint: %v\n%s", err, out)
	}
	menu, err := exec.Command(plutil, "-extract", "NSServices.0.NSMenuItem.default", "raw", "-o", "-", info).Output()
	if want := MenuPrefix + ": " + hostileClean; err != nil || strings.TrimRight(string(menu), "\n") != want {
		t.Errorf("plutil reads the menu item as %q (%v), want %q", menu, err, want)
	}
	want, err := renderScript(scriptView{CLI: spec.CLI, DBPath: spec.DBPath, ProfileID: spec.ProfileID, Name: hostileClean, Osascript: osascript})
	if err != nil {
		t.Fatal(err)
	}
	script, err := exec.Command(plutil, "-extract", "actions.0.action.ActionParameters.COMMAND_STRING", "raw", "-o", "-", doc).Output()
	if err != nil || strings.TrimRight(string(script), "\n") != strings.TrimRight(want, "\n") {
		t.Errorf("plutil reads the script as:\n%s\n(%v), want:\n%s", script, err, want)
	}
}

// pbs, the Services agent, prints the service it would list. -read_bundle
// only reads: it does not update the Services cache.
func TestTheServicesAgentReadsTheBundle(t *testing.T) {
	const pbs = "/System/Library/CoreServices/pbs"
	if _, err := os.Stat(pbs); err != nil {
		t.Skip("no pbs")
	}
	b, err := Render(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Install(t.TempDir(), b, false, noneGone)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(pbs, "-read_bundle", res.Path).CombinedOutput()
	if err != nil {
		t.Fatalf("pbs -read_bundle: %v\n%s", err, out)
	}
	for _, want := range []string{`default = "Add to MonoAgent Tasks: Work";`, "NSMessage = runWorkflowAsService;", "NSRequiredContext =", `"public.utf8-plain-text"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("pbs does not print %q:\n%s", want, out)
		}
	}
}
