package nodes

import (
	"context"
	"database/sql"
	"testing"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
	_ "modernc.org/sqlite"
)

// ctxOnly is a PackageContext with just an id.
type ctxOnly struct{ action.PackageContext }

func (ctxOnly) ID() string { return "acme-crm" }

type pkgOnlySource struct{ fakeSource }

func (pkgOnlySource) Package(string) action.PackageContext { return ctxOnly{} }

func healthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl, err := data.MigrationsFS.ReadFile("migrations/047_automation_selector_health.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestAttachPackage_SharesHealthObserver: every run reuses one observer per
// database (each observer owns a batching goroutine), and flushing after a
// run persists what it saw without waiting for the batch timer.
func TestAttachPackage_SharesHealthObserver(t *testing.T) {
	db := healthDB(t)
	action.SetDefSource(pkgOnlySource{})
	t.Cleanup(func() { action.SetDefSource(nil); CloseHealthObservers() })

	for i := 0; i < 2; i++ {
		ex := action.NewActionExecutor(context.Background(), nil, nil, nil, nil, nil, newNopLogger())
		attachPackage(ex, "acme-crm", db)
	}
	healthMu.Lock()
	n := len(healthObs)
	obs := healthObs[db]
	healthMu.Unlock()
	if n != 1 || obs == nil {
		t.Fatalf("want one shared observer, have %d", n)
	}
	if healthObserver(db) != obs {
		t.Fatal("healthObserver returned a new observer for the same db")
	}

	obs.ObserveSelector("acme-crm", "contact.save_button", 0, true, false)
	flushHealth(db)
	var okCount int
	if err := db.QueryRow(`SELECT ok_count FROM automation_selector_health WHERE automation_id='acme-crm' AND selector_key='contact.save_button'`).Scan(&okCount); err != nil {
		t.Fatalf("observation not persisted by flush: %v", err)
	}
	if okCount != 1 {
		t.Fatalf("ok_count = %d, want 1", okCount)
	}

	CloseHealthObservers()
	healthMu.Lock()
	n = len(healthObs)
	healthMu.Unlock()
	if n != 0 {
		t.Fatalf("CloseHealthObservers left %d observers", n)
	}
}
