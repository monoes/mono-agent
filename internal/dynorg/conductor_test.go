package dynorg

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

type recEmitter struct {
	mu     sync.Mutex
	events []recEvent
}

type recEvent struct {
	typ     chatevents.EventType
	payload any
}

func (r *recEmitter) Emit(typ chatevents.EventType, payload any) {
	r.mu.Lock()
	r.events = append(r.events, recEvent{typ, payload})
	r.mu.Unlock()
}

func (r *recEmitter) types(agent string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		b, _ := json.Marshal(e.payload)
		var p struct {
			AgentID string `json:"agentId"`
			To      string `json:"to"`
		}
		_ = json.Unmarshal(b, &p)
		if p.AgentID != agent {
			continue
		}
		s := string(e.typ)
		if e.typ == chatevents.EventAgentStatus {
			s += ":" + p.To
		}
		out = append(out, s)
	}
	return out
}

func (r *recEmitter) find(typ chatevents.EventType) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, e := range r.events {
		if e.typ == typ {
			out = append(out, e.payload)
		}
	}
	return out
}

func okTurn(text string) *monomind.TurnResult {
	return &monomind.TurnResult{SawDone: true, ResultText: text, StopReason: monomind.StopEndTurn, CostUSD: 0.01, HasCostUSD: true}
}

// execScript is a fake agent exec: it emits start/session/one edit, waits
// `hold` (or until cancelled), and answers per model.
type execScript struct {
	hold    time.Duration
	answers map[string]*monomind.TurnResult // model key -> result; missing = ok
	mu      sync.Mutex
	calls   []monomind.ExecOptions
	active  map[string]int // access -> running count
	maxBy   map[string]int
	running int32
	maxRun  int32
}

func (e *execScript) exec(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, o)
	e.mu.Unlock()
	n := atomic.AddInt32(&e.running, 1)
	for {
		m := atomic.LoadInt32(&e.maxRun)
		if n <= m || atomic.CompareAndSwapInt32(&e.maxRun, m, n) {
			break
		}
	}
	defer atomic.AddInt32(&e.running, -1)
	on(monomind.Event{Type: monomind.EventStart})
	on(monomind.Event{Type: monomind.EventSession, SessionID: "sess-" + o.Model})
	on(monomind.Event{Type: monomind.EventToolActivity, CoderFields: monomind.CoderFields{Phase: "start", Input: json.RawMessage(`{"file_path":"a.go"}`)}, ID: "t1", Name: "Edit"})
	on(monomind.Event{Type: monomind.EventToolActivity, CoderFields: monomind.CoderFields{Phase: "end"}, ID: "t1", Name: "Edit"})
	select {
	case <-time.After(e.hold):
	case <-ctx.Done():
		return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
	}
	if r, ok := e.answers[o.Runtime+"/"+o.Model]; ok {
		return r, nil
	}
	return okTurn("did " + o.Prompt), nil
}

func newTestConductor(t *testing.T, ex *execScript, lim Limits, roster ...Model) (*Conductor, *recEmitter) {
	t.Helper()
	em := &recEmitter{}
	if len(roster) == 0 {
		roster = []Model{opus, haiku}
	}
	c := New(context.Background(), Config{
		Cwd: "/w", Limits: lim, Staffer: &Staffer{Roster: roster, Lead: roster[0]},
		ReadAccess: true, Exec: ex.exec, Emit: em,
	})
	t.Cleanup(c.Close)
	return c, em
}

func TestSpawnRunsAndJournals(t *testing.T) {
	ex := &execScript{}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 2})
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != chatevents.AgentDone || info.Report != "did implement it" || len(info.Files) != 1 || info.Files[0] != "a.go" {
		t.Errorf("info = %+v", info)
	}
	got := strings.Join(em.types("w1"), " ")
	want := "agent.spawned agent.message agent.status:queued agent.status:starting agent.status:working tool.started tool.completed agent.status:done agent.message agent.finished"
	if got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.CostUSD == nil || *fin.CostUSD != 0.01 || fin.Outcome != chatevents.AgentDone {
		t.Errorf("finished = %+v", fin)
	}
	ts := em.find(chatevents.EventToolStarted)[0].(chatevents.ToolStartedPayload)
	if ts.CallID != "w1:t1" || ts.AgentID != "w1" {
		t.Errorf("worker tool call must be tagged and unique: %+v", ts)
	}
	o := ex.calls[0]
	if o.Access != monomind.AccessFull || o.Cwd != "/w" || !strings.Contains(o.SystemPrompt, "Coder") || len(o.Tools) != 0 {
		t.Errorf("exec opts = %+v", o)
	}
}

func TestResearchRunsReadOnly(t *testing.T) {
	ex := &execScript{}
	c, _ := newTestConductor(t, ex, Limits{})
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate the cache", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if ex.calls[0].Access != monomind.AccessRead {
		t.Errorf("research access = %q, want read", ex.calls[0].Access)
	}
}

func TestLimits(t *testing.T) {
	ex := &execScript{hold: 50 * time.Millisecond}
	c, _ := newTestConductor(t, ex, Limits{MaxAgents: 2, MaxConcurrent: 1})
	for i := 0; i < 2; i++ {
		if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate part", Access: ProfileResearch}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "one more"}); err == nil || !strings.Contains(err.Error(), "2 workers") {
		t.Errorf("third spawn err = %v", err)
	}
	c.Wait(context.Background(), nil, 5*time.Second)
	if ex.maxRun != 1 {
		t.Errorf("max running = %d, want 1", ex.maxRun)
	}
}

func TestWriteLeaseSerializesWritersNotReaders(t *testing.T) {
	ex := &execScript{hold: 80 * time.Millisecond}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 3})
	c.Spawn(context.Background(), SpawnRequest{Brief: "implement a", Access: ProfileCoding})
	c.Spawn(context.Background(), SpawnRequest{Brief: "implement b", Access: ProfileCoding})
	c.Spawn(context.Background(), SpawnRequest{Brief: "investigate c", Access: ProfileResearch})
	c.Wait(context.Background(), nil, 5*time.Second)
	if ex.maxRun != 2 {
		t.Errorf("max running = %d, want 2 (one writer + the reader)", ex.maxRun)
	}
	waited := false
	for _, p := range em.find(chatevents.EventAgentStatus) {
		if s := p.(chatevents.AgentStatusPayload); s.To == chatevents.AgentWaitingLease && s.Detail == "write" {
			waited = true
		}
	}
	if !waited {
		t.Error("the second writer should report waiting for the write lease")
	}
}

func TestReassignOnUnusableModel(t *testing.T) {
	ex := &execScript{answers: map[string]*monomind.TurnResult{
		"claude/opus": {SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrAuth, Message: "Not logged in"}},
	}}
	var outcomes []string
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{opus, haiku}, Lead: opus}, Exec: ex.exec, Emit: em,
		Outcome: func(rt, m, status, _ string, _ time.Time) { outcomes = append(outcomes, rt+"/"+m+"="+status) }})
	defer c.Close()
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement", Runtime: "claude", Model: "opus", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != chatevents.AgentDone || info.Model != "haiku" {
		t.Errorf("info = %+v", info)
	}
	r := em.find(chatevents.EventAgentReassigned)
	if len(r) != 1 || r[0].(chatevents.AgentReassignedPayload).ToModel != "haiku" {
		t.Errorf("reassigned = %+v", r)
	}
	if strings.Join(outcomes, ",") != "claude/opus=auth,claude/haiku=ok_unexpected" {
		t.Errorf("outcomes = %v", outcomes)
	}
}

func TestStopAndClose(t *testing.T) {
	ex := &execScript{hold: time.Hour}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 3})
	c.Spawn(context.Background(), SpawnRequest{Brief: "investigate x", Access: ProfileResearch})
	c.Spawn(context.Background(), SpawnRequest{Brief: "investigate y", Access: ProfileResearch})
	time.Sleep(30 * time.Millisecond)
	if _, err := c.Stop("w1"); err != nil {
		t.Fatal(err)
	}
	infos := c.Wait(context.Background(), []string{"w1"}, 5*time.Second)
	if infos[0].Status != chatevents.AgentCancelled {
		t.Errorf("stopped worker = %+v", infos[0])
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the running worker")
	}
	if n := len(em.find(chatevents.EventAgentFinished)); n != 2 {
		t.Errorf("finished events = %d, want 2", n)
	}
}

func TestWaitTimesOutAndMessageResumes(t *testing.T) {
	ex := &execScript{hold: 150 * time.Millisecond}
	c, em := newTestConductor(t, ex, Limits{})
	c.Spawn(context.Background(), SpawnRequest{Brief: "implement", Access: ProfileCoding})
	if infos := c.Wait(context.Background(), []string{"w1"}, 10*time.Millisecond); infos[0].Report != "" || !running(infos[0].Status) {
		t.Errorf("early wait = %+v", infos[0])
	}
	if _, err := c.Message(context.Background(), "w1", "more"); err == nil {
		t.Error("a follow-up to a running worker must be refused")
	}
	c.Wait(context.Background(), nil, 5*time.Second)
	if _, err := c.Message(context.Background(), "w1", "now add tests"); err != nil {
		t.Fatal(err)
	}
	infos := c.Wait(context.Background(), []string{"w1"}, 5*time.Second)
	if infos[0].Status != chatevents.AgentDone {
		t.Errorf("after follow-up = %+v", infos[0])
	}
	last := ex.calls[len(ex.calls)-1]
	if last.Resume != "sess-opus" || last.Prompt != "now add tests" {
		t.Errorf("follow-up exec = resume %q prompt %q", last.Resume, last.Prompt)
	}
	dirs := []string{}
	for _, p := range em.find(chatevents.EventAgentMessage) {
		dirs = append(dirs, p.(chatevents.AgentMessagePayload).Direction)
	}
	if strings.Join(dirs, ",") != "brief,result,followup,result" {
		t.Errorf("messages = %v", dirs)
	}
}

func TestBudgetStopsNewSpawns(t *testing.T) {
	ex := &execScript{}
	c, _ := newTestConductor(t, ex, Limits{MaxAgents: 5, MaxConcurrent: 2, BudgetUSD: 0.01})
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement", Wait: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "more"}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Errorf("err = %v", err)
	}
}

func TestHandleTools(t *testing.T) {
	ex := &execScript{}
	c, _ := newTestConductor(t, ex, Limits{})
	out, err := c.Handle(context.Background(), ToolSpawn, json.RawMessage(`{"brief":"implement","wait":true}`))
	if err != nil || !strings.Contains(out, `"status":"done"`) {
		t.Fatalf("spawn = %s, %v", out, err)
	}
	out, err = c.Handle(context.Background(), ToolRoster, nil)
	if err != nil || !strings.Contains(out, `"spawned":1`) || !strings.Contains(out, "claude") {
		t.Errorf("roster = %s, %v", out, err)
	}
	if _, err := c.Handle(context.Background(), ToolSpawn, json.RawMessage(`{"brief":""}`)); err == nil {
		t.Error("empty brief must fail")
	}
	if _, err := c.Handle(context.Background(), "org_nope", nil); err == nil {
		t.Error("unknown tool must fail")
	}
	if len(ToolSpecs()) != 5 {
		t.Error("five org tools")
	}
}
