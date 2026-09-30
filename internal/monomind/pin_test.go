package monomind

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// shimFixture is the #301 layout: monomind reached through a mise shim,
// and a project whose .tool-versions points node at a planted .cache/n.
type shimFixture struct {
	home, data, shims, project, log string
	mono, node                      string // the real installs' bin dirs
}

func testHandshake(t *testing.T) string {
	b, err := json.Marshal(VersionInfo{V: ProtocolVersion, Version: "9.0.0", MinCaller: "1.0.0", Capabilities: RequiredCapabilities})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newShimFixture builds it with monomind installed under
// installs/<monoInstall>/bin (node's own dir when monoInstall is
// "node/22.0.0"), node under installs/node/22.0.0/bin, and PATH = the shims
// dir first. The mise shim run in the project picks the planted binaries:
// the fixture checks that before returning, so the tests test the attack.
func newShimFixture(t *testing.T, monoInstall string) *shimFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell shims are unix-only")
	}
	f := &shimFixture{home: t.TempDir(), project: t.TempDir()}
	f.data = filepath.Join(f.home, ".local", "share", "mise")
	f.shims = filepath.Join(f.data, "shims")
	f.mono = filepath.Join(f.data, "installs", filepath.FromSlash(monoInstall), "bin")
	f.node = filepath.Join(f.data, "installs", "node", "22.0.0", "bin")
	f.log = filepath.Join(t.TempDir(), "pin.log")
	for k, v := range map[string]string{
		"HOME": f.home, "MISE_DATA_DIR": f.data, "PIN_LOG": f.log, EnvOverride: "",
		"XDG_DATA_HOME": "", "RTX_DATA_DIR": "", "ASDF_DATA_DIR": "", "VOLTA_HOME": "", "MISE_NODE_VERSION": "",
	} {
		t.Setenv(k, v)
	}
	tools := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "fake-mise.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "mise"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.shims, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"monomind", "node"} {
		if err := os.Symlink(filepath.Join(tools, "mise"), filepath.Join(f.shims, name)); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command("sh", filepath.Join("testdata", "make-shim-fixture.sh"), f.mono, f.node, f.project, testHandshake(t))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	t.Setenv("PATH", f.shims+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)

	attack := exec.Command(filepath.Join(f.shims, "monomind"), "--version", "--json")
	attack.Dir = f.project
	if out, err := attack.CombinedOutput(); err != nil {
		t.Fatalf("shim in project: %v: %s", err, out)
	}
	if !strings.Contains(f.readLog(t), "PLANTED") {
		t.Fatalf("fixture: the shim run in the project did not pick the planted binary; log:\n%s", f.readLog(t))
	}
	if err := os.Remove(f.log); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *shimFixture) readLog(t *testing.T) string {
	b, err := os.ReadFile(f.log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// assertRanReal fails when anything planted ran, and unless the real
// monomind ran on the real node.
func (f *shimFixture) assertRanReal(t *testing.T) {
	t.Helper()
	log := f.readLog(t)
	if strings.Contains(log, "PLANTED") {
		t.Fatalf("a planted binary ran:\n%s", log)
	}
	for _, want := range []string{"node " + filepath.Join(f.node, "node"), "monomind " + filepath.Join(f.mono, "monomind")} {
		if !strings.Contains(log, want) {
			t.Fatalf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestFindResolvesMiseShimFromNeutralDir(t *testing.T) {
	f := newShimFixture(t, "node/22.0.0")
	// The process itself runs in the project, as `monoagentcli org …`
	// started there would.
	t.Chdir(f.project)
	bin, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.mono, "monomind"); bin != want {
		t.Fatalf("Find() = %s, want the installed %s", bin, want)
	}
}

func TestOrgCommandsNeverRunPlantedBinary(t *testing.T) {
	for _, install := range []string{
		"node/22.0.0",           // node next to monomind (a mise node's npm -g)
		"npm-monomindcli/1.0.0", // node only through the shim on PATH
	} {
		t.Run(install, func(t *testing.T) {
			f := newShimFixture(t, install)
			ctx := context.Background()
			if _, err := OrgStatus(ctx, f.project, "growth"); err != nil {
				t.Fatalf("OrgStatus: %v", err)
			}
			f.assertRanReal(t)
			os.Remove(f.log)
			if _, err := OrgRun(ctx, f.project, "growth", "", false); err != nil {
				t.Fatalf("OrgRun: %v", err)
			}
			f.assertRanReal(t)
			os.Remove(f.log)
			if _, err := runOrgText(ctx, f.project, "validate", "growth"); err != nil {
				t.Fatalf("runOrgText: %v", err)
			}
			f.assertRanReal(t)
		})
	}
}

// An activated shell's per-project mise setting in this process's own
// environment doesn't reach the resolution either.
func TestShimResolutionDropsProjectManagerEnv(t *testing.T) {
	f := newShimFixture(t, "node/22.0.0")
	t.Setenv("MISE_NODE_VERSION", "path:"+filepath.Join(f.project, ".cache", "n"))
	bin, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(f.mono, "monomind"); bin != want {
		t.Fatalf("Find() = %s, want %s", bin, want)
	}
}

// A shim whose global pick lies outside the manager's installs dir fails
// closed instead of running it.
func TestShimResolvingOutsideInstallsFailsClosed(t *testing.T) {
	f := newShimFixture(t, "node/22.0.0")
	if err := os.WriteFile(filepath.Join(f.home, ".tool-versions"), []byte("node path:"+filepath.Join(f.project, ".cache", "n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Find()
	if err == nil || !strings.Contains(err.Error(), "outside mise's installs dir") {
		t.Fatalf("Find() err = %v, want an outside-installs refusal", err)
	}
	if _, err := OrgStatus(context.Background(), f.project, "growth"); err == nil {
		t.Fatal("OrgStatus ran with an unpinnable monomind")
	}
	if strings.Contains(f.readLog(t), "PLANTED") {
		t.Fatalf("a planted binary ran:\n%s", f.readLog(t))
	}
}

func TestProjectLocalBinaryFailsClosed(t *testing.T) {
	f := newShimFixture(t, "node/22.0.0")
	planted := filepath.Join(f.project, ".cache", "n", "bin", "monomind")
	outside := filepath.Join(f.mono, "monomind")
	link := filepath.Join(f.project, "monomind-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{planted, link} {
		t.Setenv(EnvOverride, bin)
		ResetCapabilityCache()
		_, err := OrgStatus(context.Background(), f.project, "growth")
		if err == nil || !strings.Contains(err.Error(), "project-local binary") {
			t.Fatalf("%s: OrgStatus err = %v, want a project-local refusal", bin, err)
		}
		if err := CheckOutside(bin, f.project); err == nil {
			t.Fatalf("CheckOutside(%s) passed", bin)
		}
	}
	if strings.Contains(f.readLog(t), "PLANTED") {
		t.Fatalf("a planted binary ran:\n%s", f.readLog(t))
	}
	if _, err := Exec(context.Background(), ExecOptions{Bin: planted, Runtime: "claude", Prompt: "p", Cwd: f.project}, nil); err == nil ||
		!strings.Contains(err.Error(), "project-local binary") {
		t.Fatalf("Exec err = %v, want a project-local refusal", err)
	}
}

// A plain install (npm -g, Homebrew, /usr/bin) is returned as found, runs
// on the node next to it, and passes the project check.
func TestNormalInstallUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes are unix-only")
	}
	prefix := filepath.Join(t.TempDir(), "npm-global", "bin")
	project := t.TempDir()
	log := filepath.Join(t.TempDir(), "pin.log")
	t.Setenv("PIN_LOG", log)
	t.Setenv("HOME", t.TempDir())
	build := exec.Command("sh", filepath.Join("testdata", "make-shim-fixture.sh"), prefix, prefix, project, testHandshake(t))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+"/bin")
	t.Setenv(EnvOverride, filepath.Join(prefix, "monomind"))
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)

	bin, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	if bin != filepath.Join(prefix, "monomind") {
		t.Fatalf("Find() = %s, want it unchanged", bin)
	}
	if _, err := OrgStatus(context.Background(), project, "growth"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "node "+filepath.Join(prefix, "node")) || strings.Contains(string(b), "PLANTED") {
		t.Fatalf("log:\n%s", b)
	}
	env := PinEnv([]string{"A=1", "PATH=/x" + string(os.PathListSeparator) + prefix}, bin)
	if got := env[len(env)-1]; got != "PATH="+prefix+string(os.PathListSeparator)+"/x" {
		t.Fatalf("PinEnv PATH = %s", got)
	}
}

// Volta: monomind in ~/.volta/bin links to volta-shim, which picks per
// project; `volta which` from the neutral dir gives the image install.
func TestFindResolvesVoltaShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes are unix-only")
	}
	home, project := t.TempDir(), t.TempDir()
	volta := filepath.Join(home, ".volta")
	image := filepath.Join(volta, "tools", "image", "node", "22.0.0", "bin")
	log := filepath.Join(t.TempDir(), "pin.log")
	for k, v := range map[string]string{"HOME": home, "VOLTA_HOME": volta, "PIN_LOG": log, EnvOverride: "", "MISE_DATA_DIR": "", "ASDF_DATA_DIR": ""} {
		t.Setenv(k, v)
	}
	build := exec.Command("sh", filepath.Join("testdata", "make-shim-fixture.sh"), image, image, project, testHandshake(t))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	bin := filepath.Join(volta, "bin")
	// volta-shim runs the planted binary whenever the cwd has a
	// .tool-versions (standing in for package.json's "volta" pin).
	shim := "#!/bin/sh\nif [ -f .tool-versions ]; then exec ./.cache/n/bin/$(basename \"$0\") \"$@\"; fi\nexec " + image + "/$(basename \"$0\") \"$@\"\n"
	voltaCLI := "#!/bin/sh\n[ \"$1\" = which ] || exit 2\nif [ -f .tool-versions ]; then echo \"$PWD/.cache/n/bin/$2\"; else echo " + image + "/$2; fi\n"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"volta-shim": shim, "volta": voltaCLI} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"monomind", "node"} {
		if err := os.Symlink(filepath.Join(bin, "volta-shim"), filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
	t.Chdir(project)

	got, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(image, "monomind"); got != want {
		t.Fatalf("Find() = %s, want %s", got, want)
	}
	if _, err := OrgStatus(context.Background(), project, "growth"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "PLANTED") || !strings.Contains(string(b), "node "+filepath.Join(image, "node")) {
		t.Fatalf("log:\n%s", b)
	}
}

func TestShimManagerDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "asdf-data")
	t.Setenv("HOME", home)
	t.Setenv("ASDF_DATA_DIR", custom)
	t.Setenv("MISE_DATA_DIR", "")
	t.Setenv("VOLTA_HOME", "")
	write := func(path, body string) string {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	other := t.TempDir()
	for path, want := range map[string]string{
		write(filepath.Join(other, "a", "monomind"), "#!/usr/bin/env bash\n# asdf-plugin: nodejs 22.0.0\nexec asdf exec \"monomind\" \"$@\"\n"): managerAsdf,
		write(filepath.Join(other, "m", "monomind"), "#!/bin/sh\nexec mise x -- monomind \"$@\"\n"):                                             managerMise,
		write(filepath.Join(custom, "shims", "monomind"), "#!/bin/sh\n"):                                                                        managerAsdf,
		write(filepath.Join(home, ".local", "share", "mise", "shims", "monomind"), "#!/bin/sh\n"):                                               managerMise,
		write(filepath.Join(home, ".volta", "bin", "monomind"), "#!/bin/sh\n"):                                                                  managerVolta,
		write(filepath.Join(other, "plain", "monomind"), "#!/usr/bin/env node\nconsole.log(1)\n"):                                               "",
	} {
		if got := shimManager(path); got != want {
			t.Errorf("shimManager(%s) = %q, want %q", path, got, want)
		}
	}
}

func TestScrubManagerEnv(t *testing.T) {
	in := []string{"PATH=/bin", "MISE_DATA_DIR=/d", "MISE_NODE_VERSION=path:/p", "__MISE_DIFF=x", "ASDF_NODEJS_VERSION=1", "ASDF_DATA_DIR=/a", "MISE_OVERRIDE_CONFIG_FILENAMES=x.toml", "VOLTA_HOME=/v"}
	got := strings.Join(scrubManagerEnv(in), " ")
	if want := "PATH=/bin MISE_DATA_DIR=/d ASDF_DATA_DIR=/a VOLTA_HOME=/v"; got != want {
		t.Fatalf("scrubManagerEnv = %q, want %q", got, want)
	}
}
