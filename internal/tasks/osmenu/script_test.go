package osmenu

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The stubs stand in for monoagentcli and osascript and record what they get.
// The CLI stub fails (exit 3, the text of the file "fail" on stderr) without
// reading its input when that file exists; when the file "short" exists it
// reads only the first 1 MiB and succeeds, as task add --stdin does.
const stubCLI = `#!/bin/sh
d=$(dirname "$0")
for a in "$@"; do printf '%s\n' "$a"; done > "$d/args"
env > "$d/env"
if [ -f "$d/fail" ]; then cat "$d/fail" >&2; exit 3; fi
if [ -f "$d/short" ]; then head -c 1048576 > "$d/stdin"; exit 0; fi
cat > "$d/stdin"
`

const stubOsascript = `#!/bin/sh
d=$(dirname "$0")
if [ "$2" = 'POSIX path of (path to frontmost application)' ]; then
	cat "$d/frontmost"
	exit 0
fi
for a in "$@"; do printf '%s\n' "$a"; done > "$d/notified"
`

// rig runs the menu's script as Automator does, sh -c with the selected text
// on standard input, against the stubs, in a folder whose name needs quoting.
type rig struct {
	dir  string
	view scriptView
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the menu's script runs under /bin/sh")
	}
	dir := filepath.Join(t.TempDir(), "my 'odd' dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &rig{dir: dir, view: scriptView{
		CLI:       filepath.Join(dir, "monoagentcli"),
		DBPath:    filepath.Join(dir, "my db.sqlite"),
		ProfileID: "work-id",
		Name:      "Work",
		Osascript: filepath.Join(dir, "osascript"),
	}}
	r.write(t, "monoagentcli", stubCLI, 0o755)
	r.write(t, "osascript", stubOsascript, 0o755)
	r.write(t, "frontmost", "/Applications/Safari.app/\n", 0o644)
	return r
}

func (r *rig) write(t *testing.T, name, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// read returns what a stub recorded, or "" when it recorded nothing.
func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

// command is the rendered script under sh -c, run in the rig's folder with a
// minimal environment plus env.
func (r *rig) command(t *testing.T, stdin io.Reader, env ...string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	script, err := renderScript(r.view)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Dir = r.dir
	cmd.Stdin = stdin
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + r.dir}, env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	return cmd, &stderr
}

// run runs the script to the end and returns its stderr and exit status.
func (r *rig) run(t *testing.T, stdin io.Reader, env ...string) (string, int) {
	t.Helper()
	cmd, stderr := r.command(t, stdin, env...)
	err := cmd.Run()
	return stderr.String(), exitStatus(t, err)
}

func exitStatus(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	t.Fatalf("running the script: %v", err)
	return 0
}

// noCanary fails when "pwned" exists in the rig's folder, where the script
// runs: some text ran as a command.
func (r *rig) noCanary(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(r.dir, "pwned")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("text from the selection, the profile name or the app name ran as a command")
	}
}

// lines is one record line per item, as the stubs write them.
func lines(items ...string) string { return strings.Join(items, "\n") + "\n" }

// hostileText runs commands if it ever reaches a shell command line, and
// carries bytes a careless pipe would mangle.
const hostileText = "Pay the invoice $(touch pwned) `touch pwned` ; touch pwned | cat\n" +
	"'quoted' \"double\" back\\slash \x00 \xff \U0000202E end\n"

func TestTheScriptHandsTheTextOverOnStandardInputOnly(t *testing.T) {
	r := newRig(t)
	r.view.Name = `O'Brien "Q" $(touch pwned) team`
	stderr, code := r.run(t, strings.NewReader(hostileText))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("stdin"); got != hostileText {
		t.Errorf("monoagentcli read %q on standard input, want the selection byte for byte", got)
	}
	want := lines("--db-path="+r.view.DBPath, "--profile=work-id", "task", "add", "--stdin", "--source", "os", "--app=Safari")
	if got := r.read("args"); got != want {
		t.Errorf("arguments:\n%swant:\n%s", got, want)
	}
	note := lines("-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run",
		"MonoAgent Tasks", `Added to Inbox in O'Brien "Q" $(touch pwned) team`)
	if got := r.read("notified"); got != note {
		t.Errorf("notification:\n%swant:\n%s", got, note)
	}
	r.noCanary(t)
}

func TestTheAppNameIsDataToo(t *testing.T) {
	r := newRig(t)
	r.write(t, "frontmost", "/Applications/Evil $(touch pwned) `touch pwned`.app/\n", 0o644)
	if stderr, code := r.run(t, strings.NewReader("x")); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("args"); !strings.HasSuffix(got, "\n--app=Evil $(touch pwned) `touch pwned`\n") {
		t.Errorf("arguments:\n%s", got)
	}
	r.noCanary(t)
}

func TestWithoutOsascriptTheTaskIsStillAdded(t *testing.T) {
	r := newRig(t)
	r.view.Osascript = filepath.Join(r.dir, "no-osascript")
	if stderr, code := r.run(t, strings.NewReader("x")); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := r.read("args"); !strings.HasSuffix(got, "\n--app=\n") {
		t.Errorf("arguments:\n%s", got)
	}
	if got := r.read("notified"); got != "" {
		t.Errorf("a notification without osascript: %q", got)
	}
}

// The script passes no --as and keeps its environment: under an agent's
// marker monoagentcli sees the marker and refuses --source os, so the menu is
// no way round the agent limit.
func TestTheScriptLeavesTheEnvironmentAlone(t *testing.T) {
	r := newRig(t)
	if stderr, code := r.run(t, strings.NewReader("x"), "CLAUDECODE=1", "MONOAGENT_ACTOR=bot"); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	env := "\n" + r.read("env")
	for _, want := range []string{"\nCLAUDECODE=1\n", "\nMONOAGENT_ACTOR=bot\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("monoagentcli did not get %q:%s", strings.TrimSpace(want), env)
		}
	}
	if strings.Contains(r.read("args"), "--as") {
		t.Error("the script names an agent")
	}
}

func TestARefusalIsShownAndPassedOn(t *testing.T) {
	r := newRig(t)
	r.write(t, "fail", "warning: an unrelated note\ninitializing database: profile \"work-id\" not found (checked both id and name)\n", 0o644)
	stderr, code := r.run(t, strings.NewReader("x"))
	if code != 3 {
		t.Fatalf("exit %d, want monoagentcli's 3: %s", code, stderr)
	}
	reason := `initializing database: profile "work-id" not found (checked both id and name)`
	if got := r.read("notified"); !strings.HasSuffix(got, "\nNot added: "+reason+"\n") || strings.Contains(got, "warning") {
		t.Errorf("notification (the last line of the error only):\n%s", got)
	}
	if stderr != "Not added to MonoAgent Tasks: "+reason+"\n" {
		t.Errorf("stderr %q", stderr)
	}
}

func TestAMissingCLIIsShownNotRun(t *testing.T) {
	r := newRig(t)
	r.view.CLI = filepath.Join(r.dir, "moved", "monoagentcli")
	stderr, code := r.run(t, strings.NewReader("x"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, stderr)
	}
	want := "Not added: monoagentcli is not at " + r.view.CLI +
		" any more. Run monoagentcli task os install again, or remove this menu with monoagentcli task os uninstall."
	if got := r.read("notified"); !strings.HasSuffix(got, "\n"+want+"\n") {
		t.Errorf("notification:\n%s", got)
	}
}

// The script reads the whole selection in every case, so the program writing
// it (Automator) never meets a closed pipe: when nothing is added, and when
// monoagentcli adds the task after reading only the first 1 MiB of it.
func TestTheScriptReadsTheWholeSelection(t *testing.T) {
	for name, c := range map[string]struct {
		setup func(*testing.T, *rig)
		code  int
	}{
		"monoagentcli moved":            {func(_ *testing.T, r *rig) { r.view.CLI = filepath.Join(r.dir, "moved") }, 1},
		"monoagentcli refuses":          {func(t *testing.T, r *rig) { r.write(t, "fail", "refused\n", 0o644) }, 3},
		"monoagentcli adds after 1 MiB": {func(t *testing.T, r *rig) { r.write(t, "short", "", 0o644) }, 0},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c.setup(t, r)
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd, stderr := r.command(t, pr)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			pr.Close() // only the script holds the reading end now
			wrote := make(chan error, 1)
			go func() {
				_, err := pw.Write(bytes.Repeat([]byte("selection "), 300_000)) // 3 MB: well past the 1 MiB monoagentcli reads plus a pipe's buffer
				pw.Close()
				wrote <- err
			}()
			if code := exitStatus(t, cmd.Wait()); code != c.code {
				t.Fatalf("exit %d, want %d: %s", code, c.code, stderr)
			}
			if err := <-wrote; err != nil {
				t.Errorf("writing the selection failed: %v (the script stopped reading)", err)
			}
		})
	}
}
