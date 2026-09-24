package main

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// A signed trace that keeps coming back in through a webhook climbs a hop
// each time and is refused past the default limit: a workflow calling its
// own webhook is a loop like any other (U10).
func TestAdmitWebhookTraceStopsALoop(t *testing.T) {
	f := newOrgCLIFixture(t)
	db, err := storage.NewDatabase(f.cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &orgServices{db: db, logf: t.Logf}
	ctx := context.Background()

	hop := 0
	for i := 1; i <= 8; i++ {
		got, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", hop)
		if err != nil || refusal != "" {
			t.Fatalf("crossing %d: hop %d, refusal %q, err %v", i, got, refusal, err)
		}
		if got != i {
			t.Fatalf("crossing %d admitted at hop %d, want %d", i, got, i)
		}
		hop = got
	}
	if _, refusal, err := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", hop); err != nil || !strings.Contains(refusal, "hop 9") {
		t.Fatalf("9th crossing: refusal %q, err %v — want refused at hop 9", refusal, err)
	}
	// A replayed older token cannot restart the count.
	if _, refusal, _ := s.admitWebhookTrace(ctx, f.plainWF, "chn_selfloop", 0); refusal == "" {
		t.Fatal("a replayed hop-0 token was admitted on a chain at its limit")
	}
}
