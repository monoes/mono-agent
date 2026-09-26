package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// CheckForUpdate asks the CLI about the app's own version instead of
// calling GitHub itself.
func TestCheckForUpdateAsksTheCLI(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, `echo "$*" >> '`+log+`'
echo '{"current_version":"`+version+`","latest_version":"v9.9.9","update_available":true,"release_url":"https://example.test/r"}'
`))
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	info := a.CheckForUpdate()
	if !info.UpdateAvailable || info.LatestVersion != "v9.9.9" || info.ReleaseURL != "https://example.test/r" || info.Error != "" {
		t.Fatalf("info = %+v", info)
	}
	if got := strings.Join(loggedArgs(t, log), "\n"); got != "--profile work --json update --check --current "+version {
		t.Fatalf("argv = %q", got)
	}
}

func TestCheckForUpdateReportsCLIFailure(t *testing.T) {
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t, "echo 'no network' >&2; exit 4\n"))
	a := newTestApp(t)
	a.ctx = context.Background()
	info := a.CheckForUpdate()
	if info.UpdateAvailable || !strings.Contains(info.Error, "no network") || info.CurrentVersion != version {
		t.Fatalf("info = %+v", info)
	}
}
