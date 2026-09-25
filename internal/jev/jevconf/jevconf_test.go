package jevconf

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/storage"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	return db.DB
}

func TestResolveKeyOrder(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()
	t.Setenv("TYPESAFE_API_KEY", "")

	if k, src, err := ResolveKey(ctx, db, "p1", "explicit"); err != nil || k != "explicit" || src != SourceConfig {
		t.Fatalf("explicit: %q %q %v", k, src, err)
	}
	// No vault entry, no env: error names the vault miss, never returns the literal ref.
	k, _, err := ResolveKey(ctx, db, "p1", "@secret:typesafe")
	if !errors.Is(err, jev.ErrNoAPIKey) || k != "" || !strings.Contains(err.Error(), "vault") {
		t.Fatalf("missing: %q %v", k, err)
	}
	t.Setenv("TYPESAFE_API_KEY", "env-key")
	for _, explicit := range []string{"", "@secret:typesafe", "@secret:other"} {
		if k, src, err := ResolveKey(ctx, db, "p1", explicit); err != nil || k != "env-key" || src != SourceEnv {
			t.Fatalf("%q: %q %q %v", explicit, k, src, err)
		}
	}
	if k, src, err := ResolveKey(ctx, nil, "p1", ""); err != nil || k != "env-key" || src != SourceEnv {
		t.Fatalf("nil db: %q %q %v", k, src, err)
	}
}

func TestEnabledAndThreshold(t *testing.T) {
	db := newDB(t)
	if Enabled(db, "p1", Capture) || Enabled(nil, "p1", Capture) {
		t.Fatal("surfaces must default to off")
	}
	if err := SetEnabled(db, "p1", Capture, true); err != nil {
		t.Fatal(err)
	}
	if !Enabled(db, "p1", Capture) || Enabled(db, "p2", Capture) || Enabled(db, "p1", Inbox) {
		t.Fatal("enable must be per profile and per surface")
	}
	if err := SetEnabled(db, "p1", Surface("bogus"), true); err == nil {
		t.Fatal("unknown surface accepted")
	}
	if got := Threshold(db, "p1", Capture, 0.75); got != 0.75 {
		t.Fatalf("default threshold = %v", got)
	}
	if err := SetThreshold(db, "p1", Capture, 1.5); err == nil {
		t.Fatal("threshold 1.5 accepted")
	}
	if err := SetThreshold(db, "p1", Capture, 0.6); err != nil || Threshold(db, "p1", Capture, 0.75) != 0.6 {
		t.Fatalf("threshold = %v, %v", Threshold(db, "p1", Capture, 0.75), err)
	}
	for _, s := range Surfaces {
		if len(Egress[s]) == 0 {
			t.Errorf("surface %s has no egress description", s)
		}
		if _, ok := DefaultThreshold[s]; !ok {
			t.Errorf("surface %s has no default threshold", s)
		}
	}
}

func TestNewClientRecordsUsage(t *testing.T) {
	db := newDB(t)
	srv := jevtest.NewServer(t, nil)
	c, err := NewClient(context.Background(), db, "p1", "k", "", Capture)
	if err != nil {
		t.Fatal(err)
	}
	q := map[string]jev.Question{"q": {Type: jev.TypeChoice, Criteria: map[string]any{"a": nil, "b": nil}}}
	if _, err := c.Ask(context.Background(), "s", q); err != nil {
		t.Fatal(err)
	}
	srv.SetStatus(400)
	_, _ = c.Ask(context.Background(), "s", q)
	u, err := UsageSince(db, "p1", time.Now().Add(-time.Hour))
	if err != nil || len(u) != 1 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	if u[0].Surface != "capture" || u[0].Calls != 2 || u[0].Failures != 1 || u[0].InputTokens != 100 || u[0].EstimatedUSD <= 0 {
		t.Fatalf("usage = %+v", u[0])
	}
}

func TestSuggestionCache(t *testing.T) {
	db := newDB(t)
	var got map[string]any
	if ok, err := LoadSuggestion(db, "p1", PeopleReview, "person-1", &got); ok || err != nil {
		t.Fatalf("empty cache: %v %v", ok, err)
	}
	for _, v := range []string{"approve", "reject"} {
		if err := SaveSuggestion(db, "p1", PeopleReview, "person-1", map[string]any{"choice": v}); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := LoadSuggestion(db, "p1", PeopleReview, "person-1", &got); !ok || err != nil || got["choice"] != "reject" {
		t.Fatalf("cache = %v %v %v", got, ok, err)
	}
}

func TestKeySourceNeverNeedsTheVaultKey(t *testing.T) {
	db := newDB(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, err := KeySource(context.Background(), db, "p1"); !errors.Is(err, jev.ErrNoAPIKey) {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("TYPESAFE_API_KEY", "env-key")
	if src, err := KeySource(context.Background(), nil, "p1"); err != nil || src != SourceEnv {
		t.Fatalf("src = %q %v", src, err)
	}
}
