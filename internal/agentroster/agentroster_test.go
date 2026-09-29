package agentroster

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/testhome"
)

func TestMain(m *testing.M) { testhome.Main(m) }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.NewDatabase(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func done(text string) *monomind.TurnResult {
	return &monomind.TurnResult{SawDone: true, ResultText: text, StopReason: monomind.StopEndTurn}
}

func failed(code, msg string) *monomind.TurnResult {
	return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: code, Message: msg}}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		res    *monomind.TurnResult
		err    error
		status string
	}{
		{"plain ok", done("ok"), nil, StatusOK},
		{"ok with punctuation", done("**OK.**\n"), nil, StatusOK},
		{"chatty reply", done("Sure! ok, here you go"), nil, StatusOKUnexpected},
		{"auth code", failed(monomind.ErrAuth, "run codex login"), nil, StatusAuth},
		{"claude not logged in", failed(monomind.ErrRunnerError, "Not logged in · Please run /login"), nil, StatusAuth},
		// Real messages from runtimes with no login (2026-09-29, scratch HOME).
		{"grok not signed in", failed(monomind.ErrRunnerError, "GrokAgentRunner: grok failed (exit 1): Not signed in. To authenticate without a browser, run:\n  grok login --device-code"), nil, StatusAuth},
		{"crush no provider", failed(monomind.ErrRunnerError, "CrushAgentRunner: crush run failed (exit 1)\nstderr: ERROR No providers configured - please run 'crush' to set up a provider interactively."), nil, StatusAuth},
		{"pi no api key", failed(monomind.ErrRunnerError, "PiAgentRunner: pi failed (exit 1)\nstderr: No API key found for the selected model.\n\nUse /login to log into a provider"), nil, StatusAuth},
		{"quota code", failed(monomind.ErrQuota, "x"), nil, StatusQuota},
		{"rate limit text", failed(monomind.ErrRunnerError, "429 Too Many Requests"), nil, StatusQuota},
		{"unknown model", failed(monomind.ErrRunnerError, "The model `gpt-9` does not exist or you do not have access to it"), nil, StatusModelUnavailable},
		{"model not found", failed(monomind.ErrRunnerError, "model_not_found"), nil, StatusModelUnavailable},
		{"missing binary", failed(monomind.ErrMissingBinary, "grok not on PATH"), nil, StatusMissingBinary},
		{"timeout code", failed(monomind.ErrTimeout, "timed out"), nil, StatusTimeout},
		{"timeout stop", &monomind.TurnResult{SawDone: true, StopReason: monomind.StopTimeout}, nil, StatusTimeout},
		{"cancelled", failed(monomind.ErrCancelled, ""), nil, StatusCancelled},
		{"other runner error", failed(monomind.ErrRunnerError, "segfault"), nil, StatusError},
		{"never finished", &monomind.TurnResult{ResultText: "ok"}, nil, StatusError},
		{"empty reply", done(""), nil, StatusError},
		{"error in the reply text", done("Not logged in · Please run /login"), nil, StatusAuth},
		{"exec failed to start", nil, errors.New("fork failed"), StatusError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := Classify(c.res, c.err)
			if got != c.status {
				t.Fatalf("status = %q, want %q", got, c.status)
			}
		})
	}
}

func TestStoreRoundTripManualAndOutcome(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)

	if err := AddManual(ctx, db, "codex", "gpt-x"); err != nil {
		t.Fatal(err)
	}
	r := Result{Runtime: "codex", Model: "gpt-x", Label: "GPT X", EffortLevels: []string{"low", "high"},
		Status: StatusOK, LatencyMs: 1400, CostUSD: 0.002, HasCost: true, Source: SourceListed, ValidatedAt: at}
	if err := Save(ctx, db, r); err != nil {
		t.Fatal(err)
	}
	got, err := List(ctx, db)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v", got, err)
	}
	g := got[0]
	if g.Source != SourceManual {
		t.Errorf("a listed save must keep the manual source, got %q", g.Source)
	}
	if g.Status != StatusOK || g.LatencyMs != 1400 || !g.HasCost || len(g.EffortLevels) != 2 || !g.ValidatedAt.Equal(at) {
		t.Errorf("round trip lost fields: %+v", g)
	}

	if err := RecordOutcome(ctx, db, "codex", "gpt-x", StatusQuota, "usage limit", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ = List(ctx, db)
	if got[0].Status != StatusQuota {
		t.Errorf("a real quota failure must mark the row failed, got %q", got[0].Status)
	}
	if err := RecordOutcome(ctx, db, "codex", "gpt-x", StatusOK, "", at); err != nil {
		t.Fatal(err)
	}
	var s, f int
	if err := db.QueryRow(`SELECT successes, failures FROM agent_model_outcomes WHERE runtime='codex'`).Scan(&s, &f); err != nil {
		t.Fatal(err)
	}
	if s != 1 || f != 1 {
		t.Errorf("outcomes = %d/%d, want 1/1", s, f)
	}
}

func strp(s string) *string { return &s }

func scanOf(entries ...monomind.ScanEntry) *monomind.ScanResult {
	return &monomind.ScanResult{V: 1, Agents: entries}
}

func TestBuildStates(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	scan := scanOf(
		monomind.ScanEntry{ID: "claude", Installed: true, Version: strp("2.0"), LoginHint: strp("claude /login")},
		monomind.ScanEntry{ID: "codex", Installed: true, Version: strp("0.60")},
		monomind.ScanEntry{ID: "grok", Installed: false},
		monomind.ScanEntry{ID: "qwen", Installed: true},
	)
	results := []Result{
		{Runtime: "claude", Model: "fresh", Status: StatusOK, RuntimeVersion: "2.0", ValidatedAt: now.Add(-time.Hour)},
		{Runtime: "claude", Model: "old", Status: StatusOK, RuntimeVersion: "2.0", ValidatedAt: now.Add(-8 * 24 * time.Hour)},
		{Runtime: "claude", Model: "bad", Status: StatusAuth, ValidatedAt: now},
		{Runtime: "codex", Model: "upgraded", Status: StatusOKUnexpected, RuntimeVersion: "0.59", ValidatedAt: now},
		{Runtime: "codex", Model: "typed", Status: StatusUntested, Source: SourceManual},
		{Runtime: "grok", Model: "gone", Status: StatusOK, ValidatedAt: now},
	}
	rs := Build(results, scan, now, 0)
	want := map[string]string{
		"claude/fresh": StateReady, "claude/old": StateStale, "claude/bad": StateFailed,
		"codex/upgraded": StateStale, "codex/typed": StateUntested, "grok/gone": StateFailed,
	}
	seen := map[string]bool{}
	for _, rr := range rs {
		seen[rr.Runtime] = true
		for _, e := range rr.Models {
			if w := want[rr.Runtime+"/"+e.Model]; e.State != w {
				t.Errorf("%s/%s state = %q, want %q", rr.Runtime, e.Model, e.State, w)
			}
		}
		if rr.Runtime == "claude" && (rr.Ready != 1 || rr.LoginHint == "") {
			t.Errorf("claude roster = %+v", rr)
		}
	}
	if !seen["qwen"] {
		t.Error("an installed runtime with no results must still be listed")
	}
}

func TestBuildPlan(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	scan := scanOf(
		monomind.ScanEntry{ID: "claude", Installed: true, Version: strp("2.0")},
		monomind.ScanEntry{ID: "crush", Installed: true},
		monomind.ScanEntry{ID: "codex", Installed: true},
		monomind.ScanEntry{ID: "grok", Installed: false},
	)
	list := func(_ context.Context, id, _ string) ([]monomind.RuntimeModel, error) {
		switch id {
		case "claude":
			return []monomind.RuntimeModel{{ID: "opus", Label: "Opus", EffortLevels: []string{"high"}}, {ID: "haiku", Label: "Haiku"}}, nil
		case "codex":
			return nil, errors.New("not logged in")
		}
		return nil, nil
	}
	previous := []Result{
		{Runtime: "claude", Model: "opus", Status: StatusOK, RuntimeVersion: "2.0", CostUSD: 0.01, HasCost: true, ValidatedAt: now},
		{Runtime: "crush", Model: "my-model", Status: StatusUntested, Source: SourceManual},
	}

	p := BuildPlan(context.Background(), scan, list, previous, PlanFilter{Now: now})
	got := map[string]bool{}
	for _, tg := range p.Targets {
		got[tg.Runtime+"/"+tg.Model] = true
	}
	for _, w := range []string{"claude/opus", "claude/haiku", "crush/default", "crush/my-model", "codex/default"} {
		if !got[w] {
			t.Errorf("plan is missing %s (targets %v)", w, got)
		}
	}
	if p.Calls != 5 || p.EstCostUSD != 0.01 || p.UnknownCost != 4 {
		t.Errorf("cost plan = calls %d, $%v, unknown %d", p.Calls, p.EstCostUSD, p.UnknownCost)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Runtime != "codex" {
		t.Errorf("skipped = %+v", p.Skipped)
	}

	p = BuildPlan(context.Background(), scan, list, previous, PlanFilter{Now: now, StaleOnly: true, Runtimes: []string{"claude", "grok"}})
	if len(p.Targets) != 1 || p.Targets[0].Model != "haiku" {
		t.Errorf("stale-only plan = %+v", p.Targets)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Runtime != "grok" {
		t.Errorf("an uninstalled runtime asked for must be reported, got %+v", p.Skipped)
	}

	p = BuildPlan(context.Background(), scan, list, previous, PlanFilter{Now: now, Runtimes: []string{"claude"}, Models: []string{"haiku", "typed-id"}})
	if len(p.Targets) != 2 || p.Targets[1].Source != SourceManual {
		t.Errorf("model filter plan = %+v", p.Targets)
	}
}

func TestRunSerialPerRuntimeAndSaves(t *testing.T) {
	var running sync.Map // runtime -> *int32
	var overlap atomic.Bool
	exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		v, _ := running.LoadOrStore(o.Runtime, new(int32))
		n := v.(*int32)
		if atomic.AddInt32(n, 1) > 1 {
			overlap.Store(true)
		}
		defer atomic.AddInt32(n, -1)
		// Validated the way chat runs it: sandboxed (when monomind can).
		if o.MaxTurns != 1 || o.Prompt != TestPrompt || o.Cwd == "" || len(o.Tools) != 0 || o.Sandbox != monomind.SandboxWorkspace {
			t.Errorf("unexpected exec options %+v", o)
		}
		if o.Runtime == "b" && o.Model == "" {
			t.Error("a listed model must be passed as --model")
		}
		if o.Runtime == "a" && o.Model != "" {
			t.Error(`the "default" model must run without --model`)
		}
		time.Sleep(5 * time.Millisecond)
		on(monomind.Event{Type: monomind.EventAssistant, Text: "ok"})
		if o.Model == "broken" {
			return failed(monomind.ErrAuth, "login"), nil
		}
		return done("ok"), nil
	}
	targets := []Target{
		{Runtime: "a", Model: DefaultModel},
		{Runtime: "b", Model: "m1"}, {Runtime: "b", Model: "m2"}, {Runtime: "b", Model: "broken"},
	}
	var saved []Result
	var lines []Line
	sum := Run(context.Background(), targets, RunOptions{RunID: "r1", Exec: exec, Concurrency: 2,
		Save: func(r Result) error { saved = append(saved, r); return nil }}, func(l Line) { lines = append(lines, l) })

	if overlap.Load() {
		t.Error("two tests of one runtime ran at the same time")
	}
	if sum.OK != 3 || sum.Failed != 1 || sum.Planned != 4 {
		t.Errorf("summary = %+v", sum)
	}
	if len(saved) != 4 {
		t.Errorf("saved %d results, want 4", len(saved))
	}
	if last := lines[len(lines)-1]; last.Type != "validate.done" || last.Summary == nil {
		t.Errorf("last line = %+v", last)
	}
	for _, r := range saved {
		if r.RunID != "r1" || r.ValidatedAt.IsZero() {
			t.Errorf("result missing run id or time: %+v", r)
		}
	}
}

func TestRunCancelStoresNothingForUnrun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	exec := func(ctx context.Context, o monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		cancel()
		<-ctx.Done()
		return failed(monomind.ErrCancelled, "cancelled"), nil
	}
	var saved int
	sum := Run(ctx, []Target{{Runtime: "a", Model: "x"}, {Runtime: "a", Model: "y"}},
		RunOptions{Exec: exec, Save: func(Result) error { saved++; return nil }}, func(Line) {})
	if sum.Cancelled != 2 || saved != 0 {
		t.Errorf("summary %+v, saved %d; want 2 cancelled, 0 saved", sum, saved)
	}
}
