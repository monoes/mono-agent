package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/library"
	"github.com/monoes/mono-agent/internal/secrets"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testdb"
)

// adoptRig is a migrated database and a counter in place of the library's adoption: what the wiring
// decides is whether to call it, and internal/library proves what the adoption does.
type adoptRig struct {
	dbPath string
	calls  atomic.Int32
}

func newAdoptRig(t *testing.T, mode accounttest.Mode, afterTheDate bool) *adoptRig {
	t.Helper()
	accounttest.Install(t, mode)
	if mode == accounttest.LockedNoLogin && !afterTheDate {
		account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
	}
	r := &adoptRig{dbPath: testdb.Path(t)}
	was := adoptOlderLogin
	adoptOlderLogin = func(context.Context, *sql.DB, *account.Guard) (bool, error) { r.calls.Add(1); return false, nil }
	t.Cleanup(func() { adoptOlderLogin = was })
	return r
}

func adoptionCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := newRootCmd()
	applyClassification(root)
	c, _, err := root.Find(args)
	if err != nil || c == root {
		t.Fatalf("no command %v: %v", args, err)
	}
	return c
}

func TestAdoptFirstRun(t *testing.T) {
	for _, c := range []struct {
		name    string
		mode    accounttest.Mode
		after   bool
		args    []string
		noDB    bool
		noGuard bool
		want    int32
	}{
		{"a gated command on a machine with no session", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, false, false, 1},
		{"a serving command after the date", accounttest.LockedNoLogin, true, []string{"daemon"}, false, false, 1},
		{"an open command", accounttest.LockedNoLogin, false, []string{"version"}, false, false, 0},
		{"the gate is dormant", accounttest.Dormant, false, []string{"workflow", "list"}, false, false, 0},
		{"a session exists", accounttest.SignedIn, false, []string{"workflow", "list"}, false, false, 0},
		{"a session that monoes.me refused", accounttest.LockedRefused, false, []string{"workflow", "list"}, false, false, 0},
		{"no database yet", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, true, false, 0},
		{"no guard installed", accounttest.LockedNoLogin, false, []string{"workflow", "list"}, false, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newAdoptRig(t, c.mode, c.after)
			if c.noDB {
				r.dbPath = filepath.Join(t.TempDir(), "absent.db")
			}
			if c.noGuard {
				account.Install(nil)
			}
			adoptFirstRun(adoptionCommand(t, c.args...), &globalConfig{DBPath: r.dbPath})
			if got := r.calls.Load(); got != c.want {
				t.Fatalf("the adoption was called %d times, want %d", got, c.want)
			}
			if _, err := os.Stat(r.dbPath); c.noDB && !os.IsNotExist(err) {
				t.Fatalf("the wiring created a database for nothing: %v", err)
			}
		})
	}
}

// A try is made once per database, ever, and two processes that start together make one between them.
func TestAdoptFirstRunTriesOnce(t *testing.T) {
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	cfg, cmd := &globalConfig{DBPath: r.dbPath}, adoptionCommand(t, "workflow", "list")
	for i := 0; i < 3; i++ {
		adoptFirstRun(cmd, cfg)
	}
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("three runs made %d tries, want 1", got)
	}

	r = newAdoptRig(t, accounttest.LockedNoLogin, false)
	cfg = &globalConfig{DBPath: r.dbPath}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); adoptFirstRun(cmd, cfg) }()
	}
	wg.Wait()
	if got := r.calls.Load(); got != 1 {
		t.Fatalf("eight processes starting together made %d tries, want 1", got)
	}
}

// An adoption that fails is not repeated, does not stop the command and prints nothing.
func TestAdoptFirstRunFailureIsClaimedAndSwallowed(t *testing.T) {
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	var calls atomic.Int32
	adoptOlderLogin = func(context.Context, *sql.DB, *account.Guard) (bool, error) {
		calls.Add(1)
		return false, errors.New("boom")
	}
	cfg, cmd := &globalConfig{DBPath: r.dbPath}, adoptionCommand(t, "workflow", "list")
	adoptFirstRun(cmd, cfg)
	adoptFirstRun(cmd, cfg)
	if got := calls.Load(); got != 1 {
		t.Fatalf("a failed try was made %d times, want 1", got)
	}
}

// The wiring itself: the root command makes the try before a gated command runs and not before an
// open one. Fails when the call in root.go is removed.
func TestTheRootCommandAdoptsBeforeAGatedCommandRuns(t *testing.T) {
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	for _, c := range []struct {
		args []string
		want int32
	}{{[]string{"version"}, 0}, {[]string{"workflow", "list"}, 1}, {[]string{"workflow", "list"}, 1}} {
		root := newRootCmd()
		applyClassification(root)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--db-path", r.dbPath}, c.args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := r.calls.Load(); got != c.want {
			t.Fatalf("after %v the adoption was called %d times, want %d", c.args, got, c.want)
		}
	}
}

// realAdoptRig runs the library's real adoption against a home with no monoes.me behind it: any call
// to the network fails, so a test that passes proved the wiring and the adoption sent nothing.
func realAdoptRig(t *testing.T) (*adoptRig, string) {
	t.Helper()
	r := newAdoptRig(t, accounttest.LockedNoLogin, false)
	adoptOlderLogin = library.AdoptIntoAccount
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	account.SetHostForTest(t, "http://127.0.0.1:1")
	account.SetEnforceFromForTest(t, time.Now().Add(-time.Hour))
	return r, home
}

func noAccountFolder(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, ".monoagent", "account")); !os.IsNotExist(err) {
		t.Fatalf("the adoption made an account folder: %v", err)
	}
}

// An unreadable older login is nobody's login: nothing is adopted or stored, and the failed look is
// not repeated.
func TestAdoptFirstRunWithACorruptOlderLoginIsInert(t *testing.T) {
	r, home := realAdoptRig(t)
	db, err := storage.NewDatabase(r.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var pid string
	if err := db.DB.QueryRow(`SELECT id FROM profiles LIMIT 1`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Add(context.Background(), db.DB, pid, "secret", library.VaultSecretName,
		map[string]string{"token": "{not json"}, "u", "http://127.0.0.1:1", ""); err != nil {
		t.Fatal(err)
	}
	db.Close()

	cfg, cmd := &globalConfig{DBPath: r.dbPath}, adoptionCommand(t, "workflow", "list")
	adoptFirstRun(cmd, cfg)
	adoptFirstRun(cmd, cfg)
	noAccountFolder(t, home)
}

// With no older login at all the wiring writes only its claim row.
func TestAdoptFirstRunIsInertWithoutAnOlderLogin(t *testing.T) {
	r, home := realAdoptRig(t)
	adoptFirstRun(adoptionCommand(t, "workflow", "list"), &globalConfig{DBPath: r.dbPath})
	noAccountFolder(t, home)
	db, err := storage.NewDatabase(r.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if v, _ := db.GetSetting(adoptionSetting); v == "" {
		t.Fatal("the try was not claimed")
	}
}
