package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

func TestTrackRecordCell(t *testing.T) {
	rates := []agentroster.Rate{
		{Category: "engineering", Rate: 0.821, Results: 11, Succeeded: 9, Ratings: 2, RatedGood: 2, Known: true},
		{Category: "research", Rate: 0.2, Results: 2},
		{Category: "testing", Rate: 0.4, Results: 5, Succeeded: 1, Ratings: 1, Known: true},
	}
	want := "engineering score 82% (9 of 11 succeeded, 2 rated good), testing score 40% (1 of 5 succeeded, 1 rated bad) low"
	if got := trackRecordCell(rates); got != want {
		t.Errorf("cell = %q", got)
	}
	if got := trackRecordCell(rates[1:2]); got != "—" {
		t.Errorf("below the minimum sample = %q", got)
	}
}

func runAgentRoster(t *testing.T, dbPath string, jsonOut bool) string {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default", JSONOutput: jsonOut}
	var err error
	out := captureStdout(t, func() {
		cmd := newAgentRosterCmd(cfg)
		cmd.SetArgs([]string{"--no-scan"})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		err = cmd.Execute()
	})
	if err != nil {
		t.Fatalf("agent roster: %v\n%s", err, out)
	}
	return out
}

func TestAgentRosterShowsTrackRecord(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	if err := agentroster.Save(ctx, db.DB, agentroster.Result{Runtime: "claude", Model: "opus", Status: agentroster.StatusOK, ValidatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := agentroster.RecordQuality(ctx, db.DB, agentroster.QualityEvent{Runtime: "claude", Model: "opus", Category: "engineering", Kind: agentroster.KindOutcome, At: now}); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	var got struct {
		Runtimes []agentroster.RuntimeRoster `json:"runtimes"`
	}
	decodeChatJSON(t, runAgentRoster(t, dbPath, true), &got)
	if len(got.Runtimes) != 1 || len(got.Runtimes[0].Models) != 1 {
		t.Fatalf("roster = %+v", got)
	}
	if tr := got.Runtimes[0].Models[0].TrackRecord; len(tr) != 1 || tr[0].Category != "engineering" || tr[0].Results != 3 || tr[0].Succeeded != 0 || !tr[0].Known || !tr[0].BadFit() {
		t.Errorf("track record = %+v", tr)
	}
	if out := runAgentRoster(t, dbPath, false); !strings.Contains(strings.ToLower(out), "track record") || !strings.Contains(out, "engineering score 43% (0 of 3 succeeded) low") {
		t.Errorf("table = %s", out)
	}
}

// A dynamic-org turn's worker result lands in the track record under the
// worker's role category.
func TestDynamicOrgTurnRecordsWorkerQuality(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	dbPath := newChatCLITestDB(t)
	bin, _ := writeOrgMonomind(t)
	withCoderCaps(t, append(monomind.CoderCapabilities, monomind.CapAgentExecFullAccessTools, monomind.CapAgentExecAccessRead)...)
	withCoderScan(t, orgScan(true))
	setCoderSettings(t, dbPath, coderSettings{Enabled: true})
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "dynamic"); code != 0 {
		t.Fatalf("set-org: %s", out)
	}
	if out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "where is the cache?"); err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events, err := agentroster.ListQuality(context.Background(), db.DB, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Runtime != "claude" || events[0].Model != "opus" || events[0].Category != "research" ||
		events[0].Kind != agentroster.KindOutcome || !events[0].Success {
		t.Errorf("quality events = %+v", events)
	}
}
