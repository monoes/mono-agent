package orgdecide

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// #281: the display copy carries paused_until while a pause is active, and
// the next reconcile after it ends drops it.
func TestDisplayCopyPausedUntil(t *testing.T) {
	s := NewStore(newDB(t))
	ctx := context.Background()
	a := Defaults("p", "growth")
	a.Level = orgdesign.LevelMid
	until := time.Now().Add(time.Hour).Truncate(time.Second)
	a.PausedUntil = &until
	if err := s.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}
	doc := &orgdesign.Doc{Name: "growth", Autonomy: &orgdesign.Autonomy{Level: "mid"}}
	res, err := ReconcileAutonomy(ctx, s, "p", doc)
	if err != nil {
		t.Fatal(err)
	}
	if want := until.UTC().Format(time.RFC3339); doc.Autonomy.PausedUntil != want || !res.DocChanged {
		t.Fatalf("paused_until = %q (changed %v), want %q", doc.Autonomy.PausedUntil, res.DocChanged, want)
	}

	expired := time.Now().Add(-time.Minute)
	a.PausedUntil = &expired
	if err := s.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}
	res, err = ReconcileAutonomy(ctx, s, "p", doc)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Autonomy.PausedUntil != "" || !res.DocChanged {
		t.Fatalf("expired pause kept: %q (changed %v)", doc.Autonomy.PausedUntil, res.DocChanged)
	}
	if res, _ = ReconcileAutonomy(ctx, s, "p", doc); res.DocChanged {
		t.Fatal("reconcile of an unpaused org not idempotent")
	}
}

// A paused_until written into the org file by hand pauses nothing and is
// removed on the next reconcile: the row alone decides.
func TestDisplayCopyPauseNeverFeedsBack(t *testing.T) {
	s := NewStore(newDB(t))
	ctx := context.Background()
	a := Defaults("p", "growth")
	a.Level = orgdesign.LevelFull
	if err := s.Put(ctx, a, "cli"); err != nil {
		t.Fatal(err)
	}
	doc := &orgdesign.Doc{Name: "growth", Autonomy: &orgdesign.Autonomy{Level: "full", PausedUntil: "2099-01-01T00:00:00Z"}}
	if _, err := ReconcileAutonomy(ctx, s, "p", doc); err != nil {
		t.Fatal(err)
	}
	row, _ := s.Get(ctx, "p", "growth")
	if row.PausedUntil != nil || row.EffectiveLevel(time.Now()) != orgdesign.LevelFull {
		t.Fatalf("file pause reached the row: %+v", row)
	}
	if doc.Autonomy.PausedUntil != "" {
		t.Fatalf("file pause kept in the display copy: %q", doc.Autonomy.PausedUntil)
	}
}

// A grant's tier in the org file is display only: routing reads the row.
func TestGrantTierDisplayCopyNeverFeedsBack(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	if _, err := orggrant.NewStore(db).UpsertGrant(ctx, orggrant.GrantInput{ProfileID: "p", OrgName: "growth", RoleID: "lead",
		Tool: orggrant.Tool{Alias: "summarize", WorkflowID: "wf", Tier: orgdesign.TierConsequential}}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(orgdesign.OrgsDir(root), "growth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"name":"growth","roles":[{"id":"lead","automations":[` +
		`{"alias":"summarize","tier":"routine"},{"alias":"ghost","tier":"routine"}]}]}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	facts := NewService(db, nil).tierFacts(ctx, "p", root, "growth")
	if got := TierFor("grant:summarize", "lead", nil, facts); got != orgdesign.TierConsequential {
		t.Fatalf("grant:summarize tier = %q, want the row's consequential", got)
	}
	if got := TierFor("grant:ghost", "lead", nil, facts); got != orgdesign.TierIrreversible {
		t.Fatalf("grant:ghost (no row) tier = %q, want irreversible", got)
	}
}
