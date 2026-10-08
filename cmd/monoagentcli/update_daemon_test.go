package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// runningDaemon writes the heartbeat of a daemon that runs version: this test process, so it is alive.
func runningDaemon(t *testing.T, version string) {
	t.Helper()
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), Version: version}); err != nil {
		t.Fatal(err)
	}
}

// fakeBlockers replaces what the update reads from the database, for one test.
func fakeBlockers(t *testing.T, inFlight int, settingsErr, err error) {
	t.Helper()
	was := restartBlockers
	restartBlockers = func(context.Context, *globalConfig, int) (int, error, error) { return inFlight, settingsErr, err }
	t.Cleanup(func() { restartBlockers = was })
}

func TestDaemonAfterUpdate(t *testing.T) {
	damaged := errors.New("the saved settings are damaged")
	for _, c := range []struct {
		name       string
		daemon     string // the version in the heartbeat; "-" means no daemon runs
		inFlight   int
		settings   error
		count      error
		registered bool
		restartErr error
		action     string // "" means nothing to do
		restarted  int
		says       []string
	}{
		{"no daemon runs", "-", 0, nil, nil, true, nil, "", 0, nil},
		{"it already runs the new version", "v0.108.0", 0, nil, nil, true, nil, "", 0, nil},
		{"an old daemon with nothing in flight", "v0.105.0", 0, nil, nil, true, nil, "restarted", 1, []string{"Restarted the daemon", "v0.108.0", "v0.105.0"}},
		{"a heartbeat with no version", "", 0, nil, nil, true, nil, "restarted", 1, nil},
		{"an execution in flight", "v0.105.0", 2, nil, nil, true, nil, "busy", 0, []string{"2 workflow execution(s)", "never interrupts a run", "monoagentcli daemon restart"}},
		{"saved API settings that cannot be used", "v0.105.0", 0, damaged, nil, true, nil, "settings", 0, []string{"OpenAI-compatible API", "the saved settings are damaged"}},
		{"what cannot be told", "v0.105.0", 0, nil, errors.New("database is locked"), true, nil, "unknown", 0, []string{"database is locked"}},
		{"not registered for auto-start", "v0.105.0", 0, nil, nil, false, nil, "not_registered", 0, []string{"daemon install"}},
		{"a service manager that fails", "v0.105.0", 0, nil, nil, true, errors.New("launchctl: exit status 113"), "failed", 1, []string{"exit status 113", "daemon restart"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.daemon == "-" {
				t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "none.json"))
			} else {
				runningDaemon(t, c.daemon)
			}
			fakeBlockers(t, c.inFlight, c.settings, c.count)
			fake := &fakeAutostart{installed: c.registered, restartErr: c.restartErr}
			useInstaller(t, fake)
			d := daemonAfterUpdate(context.Background(), &globalConfig{}, "v0.108.0")
			if c.action == "" {
				if d != nil || fake.restarted != 0 {
					t.Fatalf("d = %+v, restarted %d: nothing to do", d, fake.restarted)
				}
				return
			}
			if d == nil || d.Action != c.action || fake.restarted != c.restarted || d.InFlight != c.inFlight {
				t.Fatalf("d = %+v, restarted %d: want %s (an update never interrupts a run)", d, fake.restarted, c.action)
			}
			for _, want := range c.says {
				if !strings.Contains(d.Message, want) {
					t.Errorf("message %q should say %q", d.Message, want)
				}
			}
		})
	}
}

// What the update reads is the daemon's own: RUNNING and claimed QUEUED rows of its pid (not another
// process's, not WAITING, not finished, not unclaimed), and the saved settings `daemon restart` checks.
func TestRestartBlockersReadTheDaemonsRowsAndTheSavedSettings(t *testing.T) {
	path := testdb.Path(t)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.DB.Exec(`INSERT INTO workflows (id, name) VALUES ('w', 'w')`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		id, status string
		pid        int
	}{{"a", "RUNNING", 4242}, {"b", "QUEUED", 4242}, {"c", "RUNNING", 999}, {"d", "WAITING", 4242}, {"e", "SUCCESS", 4242}, {"f", "QUEUED", 0}} {
		if _, err := db.DB.Exec(`INSERT INTO workflow_executions (id, workflow_id, status, pid) VALUES (?, 'w', ?, ?)`, r.id, r.status, r.pid); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &globalConfig{DBPath: path}
	if n, settings, err := restartBlockers(context.Background(), cfg, 4242); err != nil || settings != nil || n != 2 {
		t.Fatalf("count = %d, settings %v, %v; want 2 (a and b) and usable settings", n, settings, err)
	}
	plantRow(t, db.DB, "not json")
	if _, settings, err := restartBlockers(context.Background(), cfg, 4242); err != nil || settings == nil {
		t.Fatalf("a damaged row: settings %v, %v", settings, err)
	}
	if _, _, err := restartBlockers(context.Background(), &globalConfig{DBPath: filepath.Join(t.TempDir(), "absent.db")}, 1); err == nil {
		t.Fatal("no database is not 'nothing in flight'")
	}
}

// `update` on a binary that is already current still brings a stale daemon onto it: the first update
// to a release is made by the binary that predates it, which cannot restart anything.
func TestUpdateWhenAlreadyCurrentRestartsAStaleDaemon(t *testing.T) {
	_ = getVersion() // resolve it once, so that the cleanup restores a value
	was := version
	version = "v9.9.9"
	t.Cleanup(func() { version = was })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9","assets":[]}`)
	}))
	t.Cleanup(srv.Close)
	old := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = old })
	runningDaemon(t, "v9.8.0")
	fakeBlockers(t, 0, nil, nil)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)
	if err := runUpdate(newUpdateCmd(&globalConfig{}), &globalConfig{}); err != nil || fake.restarted != 1 {
		t.Fatalf("err %v, the stale daemon was restarted %d times, want 1", err, fake.restarted)
	}
}

// update --app --json reports what it did about the daemon, and the progress line carries it to the
// desktop app (the install path is linux/amd64's, as in the other update --app tests).
func TestUpdateAppRestartsTheDaemonAndReportsIt(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("linux/amd64 install path")
	}
	fakeAppRelease(t, tgz(t, map[string]string{"MonoAgent-linux-amd64": "new-app"}), false)
	app, _ := appInstallDir(t)
	runningDaemon(t, "v1.0.0")
	fakeBlockers(t, 0, nil, nil)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)
	res, progress := runUpdateAppCmd(t, "--app", app, "--current", "v1.0.0")
	if !res.Success || res.Daemon == nil || res.Daemon.Action != "restarted" || fake.restarted != 1 || !strings.Contains(progress, "Restarted the daemon") {
		t.Fatalf("res = %+v, daemon %+v, restarted %d, progress %q", res, res.Daemon, fake.restarted, progress)
	}
}
