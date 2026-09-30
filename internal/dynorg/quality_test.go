package dynorg

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// failures is a track record of n fresh failed results for a model in a
// category.
func failures(q []agentroster.QualityEvent, m Model, category string, n int) []agentroster.QualityEvent {
	for range n {
		q = append(q, agentroster.QualityEvent{Runtime: m.Runtime, Model: m.Model, Category: category, Kind: agentroster.KindOutcome, At: time.Now()})
	}
	return q
}

func track(events []agentroster.QualityEvent) agentroster.Quality {
	return agentroster.Aggregate(events, time.Now())
}

func TestRuleModelSkipsBadFits(t *testing.T) {
	ctx := context.Background()
	writer := SpawnRequest{Brief: "implement the cache", Role: "engineering-code-reviewer", Access: ProfileCoding}
	base := &Staffer{Roster: []Model{opus, haiku, gpt}, Lead: opus, Library: lib()}

	st, err := base.Staff(ctx, writer)
	if err != nil || st.Model.Key() != "claude/opus" {
		t.Fatalf("no track record: %v, %v; want the lead's model", st.Model.Key(), err)
	}

	// Two failures are below the minimum sample: nothing changes.
	s := *base
	s.Quality = track(failures(nil, opus, "engineering", 2))
	if st, _ := s.Staff(ctx, writer); st.Model.Key() != "claude/opus" {
		t.Errorf("below the minimum sample the model = %s, want claude/opus", st.Model.Key())
	}

	// Three: the lead's model is a bad fit for engineering work, so the
	// rule takes the priciest other model and opus is the last fallback.
	s.Quality = track(failures(nil, opus, "engineering", 3))
	st, _ = s.Staff(ctx, writer)
	if st.Model.Key() != "codex/gpt-5.5" || st.Fallbacks[len(st.Fallbacks)-1].Key() != "claude/opus" {
		t.Errorf("model = %s, fallbacks = %s; want codex/gpt-5.5 with opus last", st.Model.Key(), keys(st.Fallbacks))
	}
	if !strings.Contains(strings.Join(st.Why, "; "), "low success rate: claude/opus") {
		t.Errorf("why = %v", st.Why)
	}

	// The track record is per category: a testing worker still gets opus.
	tester := writer
	tester.Role = "qa-tester"
	if st, _ := s.Staff(ctx, tester); st.Model.Key() != "claude/opus" {
		t.Errorf("testing worker model = %s, want claude/opus", st.Model.Key())
	}

	// When every model is a bad fit the rule still picks one.
	all := failures(failures(failures(nil, opus, "engineering", 3), haiku, "engineering", 3), gpt, "engineering", 3)
	s.Quality = track(all)
	if st, err := s.Staff(ctx, writer); err != nil || st.Model.Key() != "claude/opus" {
		t.Errorf("all bad: %s, %v", st.Model.Key(), err)
	}

	// The lead's own choice still wins, but its fallbacks are ranked.
	s.Quality = track(failures(nil, haiku, "engineering", 3))
	chosen := writer
	chosen.Runtime, chosen.Model = "claude", "opus"
	st, _ = s.Staff(ctx, chosen)
	if st.Model.Key() != "claude/opus" || keys(st.Fallbacks) != "codex/gpt-5.5, claude/haiku" {
		t.Errorf("lead's choice: %s, fallbacks %s", st.Model.Key(), keys(st.Fallbacks))
	}
}

type stateChooser struct {
	answer string
	state  any
	opts   map[string]string
}

func (c *stateChooser) Choose(_ context.Context, state any, q string, opts map[string]string) (string, float64, error) {
	if strings.HasPrefix(q, "Which model") {
		c.state, c.opts = state, opts
		return c.answer, 0.8, nil
	}
	return "", 0, context.Canceled
}

func TestJevSeesTheTrackRecord(t *testing.T) {
	jev := &stateChooser{answer: "claude/haiku"}
	s := &Staffer{Roster: []Model{opus, haiku, gpt}, Lead: opus, ModelPicker: PickerLeadThenJev, Library: lib(), Chooser: jev,
		Quality: track(failures(nil, opus, "engineering", 4))}
	st, err := s.Staff(context.Background(), SpawnRequest{Brief: "review it", Role: "engineering-code-reviewer", Access: ProfileCoding})
	if err != nil || st.Model.Key() != "claude/haiku" {
		t.Fatalf("model = %s, %v", st.Model.Key(), err)
	}
	b, _ := json.Marshal(jev.state)
	if !strings.Contains(string(b), `"track_record":{"claude/opus":{"bad_fit":true,"samples":4,"success_rate":0.38}}`) {
		t.Errorf("Jev's state = %s", b)
	}
	if !strings.Contains(jev.opts["claude/opus"], "succeeded in 38% of 4 recent engineering jobs") || strings.Contains(jev.opts["claude/haiku"], "succeeded") {
		t.Errorf("options = %v", jev.opts)
	}
}

func TestCountsAsQuality(t *testing.T) {
	cases := []struct {
		outcome, errText string
		want             bool
	}{
		{chatevents.AgentDone, "", true},
		{chatevents.AgentFailed, "error: the tests still fail", true},
		{chatevents.AgentFailed, "timeout: timed out", true},
		{chatevents.AgentFailed, "budget: the workers' budget of $1.00 for this turn is spent", false}, // #265's form
		{chatevents.AgentFailed, "error: budget: the workers' budget of $1.00 for this turn is spent", false},
		{chatevents.AgentFailed, "error: budget exceeded", false}, // monomind's own budget stop
		{chatevents.AgentFailed, "auth: not logged in", false},
		{chatevents.AgentFailed, "quota: rate limited", false},
		{chatevents.AgentFailed, "no model could run this worker", false},
		{chatevents.AgentCancelled, "cancelled", false},
	}
	for _, c := range cases {
		if got := countsAsQuality(c.outcome, c.errText); got != c.want {
			t.Errorf("countsAsQuality(%q, %q) = %v, want %v", c.outcome, c.errText, got, c.want)
		}
	}
}

type qualityLog struct {
	mu     sync.Mutex
	events []agentroster.QualityEvent
}

func (l *qualityLog) record(e agentroster.QualityEvent) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *qualityLog) all() []agentroster.QualityEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agentroster.QualityEvent(nil), l.events...)
}

func newQualityConductor(t *testing.T, ex *execScript, lim Limits) (*Conductor, *qualityLog) {
	t.Helper()
	log := &qualityLog{}
	c := New(context.Background(), Config{
		Cwd: "/w", Limits: lim, Staffer: &Staffer{Roster: []Model{opus, haiku}, Lead: opus, Library: lib()},
		ReadAccess: true, Exec: ex.exec, Emit: &recEmitter{}, Quality: &Quality{Record: log.record},
	})
	t.Cleanup(c.Close)
	return c, log
}

func TestResultsAndRatingsAreRecorded(t *testing.T) {
	ctx := context.Background()
	ex := &execScript{answers: map[string]*monomind.TurnResult{
		"claude/haiku": {SawDone: true, Err: &monomind.ProtocolError{Code: "internal", Message: "crashed"}},
	}}
	c, log := newQualityConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 1})
	if _, err := c.Handle(ctx, ToolRate, json.RawMessage(`{"agent_id":"w9","rating":"good"}`)); err == nil {
		t.Error("rating an unknown worker must fail")
	}
	if _, err := c.Spawn(ctx, SpawnRequest{Brief: "implement it", Role: "engineering-code-reviewer", Runtime: "claude", Model: "opus", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Spawn(ctx, SpawnRequest{Brief: "test it", Role: "qa-tester", Runtime: "claude", Model: "haiku", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Handle(ctx, ToolRate, json.RawMessage(`{"agent_id":"w1","rating":"meh"}`)); err == nil {
		t.Error("a rating other than good or bad must fail")
	}
	out, err := c.Handle(ctx, ToolRate, json.RawMessage(`{"agent_id":"w1","rating":"Bad"}`))
	if err != nil || !strings.Contains(out, `"recorded":true`) || !strings.Contains(out, `"category":"engineering"`) {
		t.Fatalf("rate = %s, %v", out, err)
	}
	if _, err := c.Handle(ctx, ToolRate, json.RawMessage(`{"agent_id":"w1","rating":"good"}`)); err == nil || !strings.Contains(err.Error(), "already rated") {
		t.Errorf("second rating of one result: %v", err)
	}
	got := log.all()
	want := []agentroster.QualityEvent{
		{Runtime: "claude", Model: "opus", Category: "engineering", Kind: agentroster.KindOutcome, Success: true},
		{Runtime: "claude", Model: "haiku", Category: "testing", Kind: agentroster.KindOutcome, Success: false},
		{Runtime: "claude", Model: "opus", Category: "engineering", Kind: agentroster.KindRating, Success: false},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %+v", got)
	}
	for i := range want {
		g := got[i]
		g.At = time.Time{}
		if g != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, g, want[i])
		}
	}

	// A follow-up is a new result, so it can be rated again.
	if _, err := c.Message(ctx, "w1", "one more thing"); err != nil {
		t.Fatal(err)
	}
	c.Wait(ctx, []string{"w1"}, MaxWait)
	if _, err := c.Handle(ctx, ToolRate, json.RawMessage(`{"agent_id":"w1","rating":"good"}`)); err != nil {
		t.Errorf("rating a follow-up's result: %v", err)
	}
	if n := len(log.all()); n != 5 {
		t.Errorf("events after the follow-up = %d, want 5", n)
	}
}

// A run the budget stopped says nothing about the model: no outcome, and
// nothing for the lead to rate.
func TestBudgetStopIsNotAQualityEvent(t *testing.T) {
	ctx := context.Background()
	ex := &execScript{answers: map[string]*monomind.TurnResult{
		"claude/opus": {SawDone: true, ResultText: "half done", Err: &monomind.ProtocolError{Code: monomind.ErrBudget, Message: "budget exceeded"}},
	}}
	c, log := newQualityConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 1})
	info, err := c.Spawn(ctx, SpawnRequest{Brief: "implement it", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != chatevents.AgentFailed {
		t.Fatalf("info = %+v", info)
	}
	if got := log.all(); len(got) != 0 {
		t.Errorf("budget stop recorded %+v", got)
	}
	if _, err := c.Rate("w1", RatingBad); err == nil || !strings.Contains(err.Error(), "nothing to rate") {
		t.Errorf("rating a budget stop: %v", err)
	}
}
