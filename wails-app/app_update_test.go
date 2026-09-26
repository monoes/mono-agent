//go:build !windows

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// AppSelfUpdate runs `update --app <exe> --current <version>` and forwards
// the CLI's NDJSON progress; it downloads nothing itself.
func TestAppSelfUpdateGoesThroughTheCLI(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
echo '{"kind":"line","message":"Downloading MonoAgent-linux-amd64.tar.gz..."}' >&2
echo '{"kind":"line","message":"Checksum verified. Installing..."}' >&2
echo '{"success":true,"new_version":"v9.9.9","restart":"quit"}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	var steps []string
	res, err := a.runAppUpdate("/opt/MonoAgent/MonoAgent-linux-amd64", func(m string) { steps = append(steps, m) })
	if err != nil || !res.Success || res.NewVersion != "v9.9.9" || res.Restart != "quit" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(steps) != 2 || !strings.Contains(steps[1], "Checksum verified") {
		t.Fatalf("progress = %v", steps)
	}
	if got := strings.Join(loggedArgs(t, log), "\n"); got != "--json update --app /opt/MonoAgent/MonoAgent-linux-amd64 --current "+version {
		t.Fatalf("argv = %q", got)
	}
}

func TestAppSelfUpdateReportsCLIRefusal(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo '{"success":false,"error":"sha256 mismatch for MonoAgent-linux-amd64.tar.gz"}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	res, err := a.runAppUpdate("/x", func(string) {})
	if err != nil || res.Success || !strings.Contains(res.Error, "sha256") {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestAppSelfUpdateCLICrash(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "echo 'panic: boom' >&2; exit 2\n"))
	a := newTestApp(t)
	a.ctx = context.Background()
	if _, err := a.runAppUpdate("/x", func(string) {}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}
