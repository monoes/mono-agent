//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The installer and its tests live in the CLI (internal/appupdate,
// cmd/monoagentcli/update_app_test.go); these cover the binding: the argv,
// progress forwarding and when the app quits.

type updateRun struct {
	progress []string
	quits    int
}

func (r *updateRun) run(cli, exe string) UpdateResult {
	return runAppUpdate(context.Background(), cli, exe, "v1.2.3",
		func(m string) { r.progress = append(r.progress, m) }, func() { r.quits++ })
}

func TestAppSelfUpdateShellsOutAndForwardsProgress(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	cli := fakeCLI(t, `echo "$*" >> '`+log+`'
echo '{"kind":"line","message":"Downloading app update..."}' >&2
echo 'a stray warning' >&2
echo '{"kind":"line","message":"Update installed — restarting"}' >&2
echo '{"success":true,"new_version":"v9.9.9","restart":"quit"}'
`)
	var r updateRun
	res := r.run(cli, "/opt/Mono Agent/MonoAgent-linux-amd64")
	if !res.Success || res.NewVersion != "v9.9.9" || res.Error != "" {
		t.Fatalf("res = %+v", res)
	}
	if got := strings.Join(loggedArgs(t, log), "\n"); got != "--json update --app /opt/Mono Agent/MonoAgent-linux-amd64 --current v1.2.3" {
		t.Fatalf("argv = %q", got)
	}
	if got := strings.Join(r.progress, "|"); got != "Downloading app update...|Update installed — restarting" {
		t.Fatalf("progress = %q", got)
	}
	if r.quits != 1 {
		t.Fatalf("quit called %d times, want 1", r.quits)
	}
}

// A refused update (bad checksum, missing asset…) is a result with an
// error: the app shows it and keeps running.
func TestAppSelfUpdateReportsARefusal(t *testing.T) {
	cli := fakeCLI(t, `echo '{"kind":"line","message":"Downloading app update..."}' >&2
echo '{"success":false,"error":"MonoAgent-linux-amd64.tar.gz: SHA-256 mismatch"}'
`)
	var r updateRun
	res := r.run(cli, "/opt/MonoAgent/MonoAgent-linux-amd64")
	if res.Success || !strings.Contains(res.Error, "SHA-256 mismatch") {
		t.Fatalf("res = %+v", res)
	}
	if r.quits != 0 || len(r.progress) != 1 {
		t.Fatalf("quits = %d, progress = %v", r.quits, r.progress)
	}
}

func TestAppSelfUpdateReportsACLICrash(t *testing.T) {
	cli := fakeCLI(t, "echo 'panic: boom' >&2; exit 2\n")
	var r updateRun
	res := r.run(cli, "/opt/MonoAgent/MonoAgent-linux-amd64")
	if res.Success || !strings.Contains(res.Error, "panic: boom") {
		t.Fatalf("res = %+v", res)
	}
	if r.quits != 0 {
		t.Fatal("quit after a crash")
	}

	// A result followed by a non-zero exit is still a failure.
	cli = fakeCLI(t, `echo '{"success":true,"new_version":"v9.9.9"}'; exit 1`+"\n")
	if res := r.run(cli, "/x"); res.Success || r.quits != 0 {
		t.Fatalf("res = %+v, quits = %d", res, r.quits)
	}
}

func TestAppSelfUpdateUpToDateKeepsRunning(t *testing.T) {
	cli := fakeCLI(t, `echo '{"success":true,"up_to_date":true}'`+"\n")
	var r updateRun
	res := r.run(cli, "/x")
	if !res.Success || !res.UpToDate || r.quits != 0 {
		t.Fatalf("res = %+v, quits = %d", res, r.quits)
	}
}

// AppSelfUpdate picks the CLI with findMonoAgentCLI and names the running
// executable. (The refusal path emits no event and does not quit, so it
// runs without the Wails runtime.)
func TestAppSelfUpdateUsesTheAppsCLIAndExecutable(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
echo '{"success":false,"error":"no app asset"}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	res := a.AppSelfUpdate()
	if res.Success || res.Error != "no app asset" {
		t.Fatalf("res = %+v", res)
	}
	exe, _ := os.Executable()
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if got := strings.Join(loggedArgs(t, log), "\n"); got != "--json update --app "+exe+" --current "+version {
		t.Fatalf("argv = %q", got)
	}
}
