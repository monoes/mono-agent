package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/workflow"
)

// The refresh the daemon runs before every heartbeat write carries the account
// verdict: locked and enforced is a real lock, locked and not enforced is the
// warn period (nothing is refused), and a dormant gate says nothing at all.
func TestDaemonHeartbeatRefreshCarriesTheAccount(t *testing.T) {
	engine := workflow.NewWorkflowEngineWithStore(nil, nil, nil, workflow.NewNodeTypeRegistry(),
		workflow.EngineConfig{WebhookAddr: "127.0.0.1:0"}, zerolog.Nop())
	refresh := daemonHeartbeatRefresh(engine)
	var hb daemonhb.Heartbeat

	accounttest.Install(t, accounttest.LockedNoLogin)
	refresh(&hb)
	if hb.Account == nil || hb.Account.State != "locked" || hb.Account.Reason != "not_logged_in" || !hb.Account.Enforced {
		t.Fatalf("locked: Account = %+v, want locked / not_logged_in / enforced", hb.Account)
	}

	account.InstallForTest(t, nil)
	account.SetEnforceFromForTest(t, time.Now().Add(48*time.Hour))
	refresh(&hb)
	if hb.Account == nil || hb.Account.State != "locked" || hb.Account.Reason != "not_logged_in" || hb.Account.Enforced {
		t.Fatalf("warn period: Account = %+v, want locked / not_logged_in / not enforced", hb.Account)
	}

	accounttest.Install(t, accounttest.Dormant)
	refresh(&hb)
	if hb.Account != nil {
		t.Fatalf("dormant: Account = %+v, want nil", hb.Account)
	}
}

// busyOrLocked: a locked account is busy, with a reason that names it; every
// other state (dormant included) leaves the usual check, which an empty
// database passes.
func TestAutoRevalidationWaitsWhileTheAccountIsLocked(t *testing.T) {
	db, err := storage.NewDatabase(t.TempDir() + "/auto.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		mode accounttest.Mode
		busy bool
	}{"locked": {accounttest.LockedNoLogin, true}, "signed in": {accounttest.SignedIn, false}, "dormant": {accounttest.Dormant, false}} {
		accounttest.Install(t, c.mode)
		busy, why := busyOrLocked(context.Background(), db.DB)
		if busy != c.busy || (busy && !strings.Contains(why, "monoes.me account is locked")) {
			t.Errorf("%s: busy = %v (%q), want %v", name, busy, why, c.busy)
		}
	}
}
