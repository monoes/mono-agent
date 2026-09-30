package dynorg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// outcomeLog records what the conductor reports as model outcomes.
type outcomeLog struct {
	mu  sync.Mutex
	got []string
}

func (l *outcomeLog) record(runtime, model, status, detail string, _ time.Time) {
	l.mu.Lock()
	l.got = append(l.got, runtime+"/"+model+"="+status)
	l.mu.Unlock()
}

func (l *outcomeLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got...)
}

// #261: the conductor staffed a research worker as confined by a read-only
// sandbox (from the coder status's scan), but at run time Exec could not
// apply it. The worker must not run unconfined beside a writer: the refused
// run is retried holding the write lease, without the sandbox.
func TestConfinedResearchFailsClosedWhenTheSandboxIsNotApplied(t *testing.T) {
	sandboxed := Model{Runtime: "copilot", Model: "gpt", FullAccess: true, ReadOnlySandbox: true}
	for _, mode := range []string{"exec refuses", "start event says unsupported"} {
		t.Run(mode, func(t *testing.T) {
			var (
				mu            sync.Mutex
				calls         []monomind.ExecOptions
				writerRunning bool
				overlap       bool
			)
			exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
				mu.Lock()
				calls = append(calls, o)
				mu.Unlock()
				research := strings.Contains(o.Prompt, "investigate")
				if research && o.Sandbox != "" {
					if !o.RequireSandbox {
						t.Error("a sandbox-confined research run must require its sandbox")
					}
					if mode == "exec refuses" {
						return nil, fmt.Errorf("%w: read-only sandbox for copilot is unsupported", monomind.ErrSandboxRequired)
					}
					on(monomind.Event{Type: monomind.EventStart, SandboxFields: monomind.SandboxFields{SandboxStatus: monomind.SandboxStatusUnsupported}})
					<-ctx.Done() // the conductor cancels it
					return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
				}
				mu.Lock()
				if research && writerRunning {
					overlap = true
				}
				if !research {
					writerRunning = true
				}
				mu.Unlock()
				on(monomind.Event{Type: monomind.EventStart})
				select {
				case <-time.After(150 * time.Millisecond):
				case <-ctx.Done():
				}
				mu.Lock()
				if !research {
					writerRunning = false
				}
				mu.Unlock()
				return okTurn("did " + o.Prompt), nil
			}
			em := &recEmitter{}
			outcomes := &outcomeLog{}
			c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{sandboxed}, Lead: sandboxed},
				Exec: exec, Emit: em, Outcome: outcomes.record, Limits: Limits{MaxAgents: 3, MaxConcurrent: 3}})
			defer c.Close()
			if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Access: ProfileCoding}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond) // the writer holds the lease
			info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate it", Access: ProfileResearch})
			if err != nil {
				t.Fatal(err)
			}
			res := c.Wait(context.Background(), []string{info.ID}, 5*time.Second)
			if res[0].Status != chatevents.AgentDone {
				t.Fatalf("research worker = %+v", res[0])
			}
			mu.Lock()
			defer mu.Unlock()
			if overlap {
				t.Error("the unconfined research run overlapped the writer")
			}
			last := calls[len(calls)-1]
			if !strings.Contains(last.Prompt, "investigate") || last.Sandbox != "" || last.RequireSandbox {
				t.Errorf("retry = sandbox %q require %v prompt %q; want an unsandboxed run under the write lease", last.Sandbox, last.RequireSandbox, last.Prompt)
			}
			waited := false
			for _, p := range em.find(chatevents.EventAgentStatus) {
				if s := p.(chatevents.AgentStatusPayload); s.AgentID == info.ID && s.To == chatevents.AgentWaitingLease && s.Detail == "write" {
					waited = true
				}
			}
			if !waited {
				t.Error("the retried research worker should wait for the write lease")
			}
			// The writer's run and the research retry; not the refusal.
			if got := outcomes.list(); len(got) != 2 || strings.Join(got, ",") != "copilot/gpt=ok_unexpected,copilot/gpt=ok_unexpected" {
				t.Errorf("outcomes = %v; the refused run must not be recorded", got)
			}
		})
	}
}

// A research run whose sandbox was applied keeps running beside a writer.
func TestConfinedResearchWithItsSandboxIsNotRetried(t *testing.T) {
	sandboxed := Model{Runtime: "copilot", Model: "gpt", FullAccess: true, ReadOnlySandbox: true}
	ex := &execScript{}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{sandboxed}, Lead: sandboxed}, Exec: ex.exec, Emit: &recEmitter{}})
	defer c.Close()
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate", Access: ProfileResearch, Wait: true})
	if info.Status != chatevents.AgentDone || len(ex.calls) != 1 || ex.calls[0].Sandbox != monomind.SandboxReadOnly || !ex.calls[0].RequireSandbox {
		t.Fatalf("status %s, calls %+v", info.Status, ex.calls)
	}
}

// #261: a run the org's budget refused, or that monomind stopped at its
// budget cap, says nothing about the model and is not recorded as its
// outcome.
func TestBudgetRefusalIsNotAModelOutcome(t *testing.T) {
	ex := &execScript{hold: 30 * time.Millisecond}
	outcomes := &outcomeLog{}
	c := New(context.Background(), Config{Cwd: "/w", ReadAccess: true, Staffer: &Staffer{Roster: []Model{opus, haiku}, Lead: opus},
		Exec: ex.exec, Emit: &recEmitter{}, Outcome: outcomes.record, Limits: Limits{MaxAgents: 3, MaxConcurrent: 1, BudgetUSD: 0.01}})
	defer c.Close()
	a, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "implement a", Access: ProfileCoding})
	b, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "implement b", Access: ProfileCoding})
	res := c.Wait(context.Background(), []string{a.ID, b.ID}, 5*time.Second)
	// Either writer may take the lease first; the other finds the budget spent.
	done, refused := 0, 0
	for _, r := range res {
		switch {
		case r.Status == chatevents.AgentDone:
			done++
		case r.Status == chatevents.AgentFailed && strings.HasPrefix(r.Error, "budget: the workers' budget"):
			refused++
		}
	}
	if done != 1 || refused != 1 {
		t.Fatalf("workers = %+v", res)
	}
	if got := outcomes.list(); len(got) != 1 || !strings.HasSuffix(got[0], "=ok_unexpected") {
		t.Fatalf("outcomes = %v, want only the first run's", got)
	}
	if len(ex.calls) != 1 {
		t.Fatalf("the refused worker must not exec or fall back: %d execs", len(ex.calls))
	}

	// monomind's own budget stop (error code "budget").
	ex2 := &execScript{answers: map[string]*monomind.TurnResult{
		"claude/opus": {SawDone: true, ResultText: "half done", Err: &monomind.ProtocolError{Code: monomind.ErrBudget, Message: "budget exceeded"}},
	}}
	outcomes2 := &outcomeLog{}
	c2 := New(context.Background(), Config{Cwd: "/w", ReadAccess: true, Staffer: &Staffer{Roster: []Model{opus, haiku}, Lead: opus},
		Exec: ex2.exec, Emit: &recEmitter{}, Outcome: outcomes2.record})
	defer c2.Close()
	info, _ := c2.Spawn(context.Background(), SpawnRequest{Brief: "implement", Access: ProfileCoding, Runtime: "claude", Model: "opus", Wait: true})
	if info.Status != chatevents.AgentFailed || len(outcomes2.list()) != 0 || len(ex2.calls) != 1 {
		t.Fatalf("status %s, outcomes %v, execs %d", info.Status, outcomes2.list(), len(ex2.calls))
	}
	// The partial report survives the budget stop.
	if info.Report != "half done" || !strings.HasPrefix(info.Error, "budget") {
		t.Fatalf("report %q, error %q", info.Report, info.Error)
	}
}

func leadEdit(phase, id string) monomind.Event {
	return monomind.Event{Type: monomind.EventToolActivity, ID: id, Name: "Edit",
		CoderFields: monomind.CoderFields{Phase: phase, Input: json.RawMessage(`{"file_path":"main.go"}`)}}
}

// #260: the lead's own edits take the write lease, so a writer spawned
// while the lead edits waits until the edit ends.
func TestWriterWaitsForTheLeadsEdit(t *testing.T) {
	ex := &execScript{}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 3})
	c.LeadEvent(leadEdit("start", "e1"))
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Access: ProfileCoding})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	ex.mu.Lock()
	started := len(ex.calls)
	ex.mu.Unlock()
	if started != 0 {
		t.Fatal("the writer ran while the lead was editing")
	}
	c.LeadEvent(leadEdit("end", "e1"))
	res := c.Wait(context.Background(), []string{info.ID}, 5*time.Second)
	if res[0].Status != chatevents.AgentDone {
		t.Fatalf("writer = %+v", res[0])
	}
	waited := false
	for _, p := range em.find(chatevents.EventAgentStatus) {
		if s := p.(chatevents.AgentStatusPayload); s.To == chatevents.AgentWaitingLease && s.Detail == "write" {
			waited = true
		}
	}
	if !waited {
		t.Error("the writer should report waiting for the write lease")
	}
	// Reads don't take the lease.
	c.LeadEvent(monomind.Event{Type: monomind.EventToolActivity, ID: "r1", Name: "Read", CoderFields: monomind.CoderFields{Phase: "start"}})
	if !c.write.tryAcquire() {
		t.Fatal("a read took the write lease")
	}
	c.write.release()
}

// #260: an edit the lead starts while a writer holds the lease can't be
// refused (its tools are native), so it is reported: a notice, and a
// warning in the lead's next org tool result. The writer keeps the lease.
func TestLeadEditWhileAWriterHoldsTheLeaseIsReported(t *testing.T) {
	ex := &execScript{hold: 200 * time.Millisecond}
	c, em := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 3})
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Access: ProfileCoding})
	time.Sleep(40 * time.Millisecond)
	c.LeadEvent(leadEdit("start", "e1"))
	c.LeadEvent(leadEdit("end", "e1"))
	notices := em.find(chatevents.EventNotice)
	if len(notices) != 1 || notices[0].(chatevents.NoticePayload).Code != NoticeLeadEditConflict ||
		!strings.Contains(notices[0].(chatevents.NoticePayload).Message, info.ID) {
		t.Fatalf("notices = %+v", notices)
	}
	out, err := c.Handle(context.Background(), ToolWait, json.RawMessage(`{"timeout_s":5}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Warnings []string        `json:"warnings"`
		Result   json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(out), &got) != nil || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "main.go") || !strings.Contains(string(got.Result), `"status":"done"`) {
		t.Fatalf("org_wait result = %s", out)
	}
	// Reported once; the writer released the lease and the lead took none.
	if out, _ := c.Handle(context.Background(), ToolWait, json.RawMessage(`{"timeout_s":1}`)); strings.Contains(out, "warnings") {
		t.Fatalf("warning repeated: %s", out)
	}
	if !c.write.tryAcquire() {
		t.Fatal("the write lease is still held")
	}
	c.write.release()
}

// An edit whose end event never comes doesn't keep writers queued once the
// lead waits for them.
func TestLeadWaitReleasesItsEditLease(t *testing.T) {
	ex := &execScript{}
	c, _ := newTestConductor(t, ex, Limits{MaxAgents: 3, MaxConcurrent: 3})
	c.LeadEvent(leadEdit("start", "e1"))
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "implement it", Access: ProfileCoding, Wait: true})
	if info.Status != chatevents.AgentDone {
		t.Fatalf("writer = %+v", info)
	}
	c.LeadEvent(leadEdit("end", "e1")) // late end: nothing to release
	if !c.write.tryAcquire() {
		t.Fatal("the write lease is still held")
	}
	c.write.release()
}

// The retry after a refused sandbox doesn't go back to a model that
// already failed to run the worker (auth), nor run its current model twice.
func TestUnconfinedRetrySkipsModelsThatFailed(t *testing.T) {
	a := Model{Runtime: "copilot", Model: "a", FullAccess: true, ReadOnlySandbox: true}
	b := Model{Runtime: "copilot", Model: "b", FullAccess: true, ReadOnlySandbox: true}
	var mu sync.Mutex
	var calls []string
	exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		mu.Lock()
		calls = append(calls, o.Model+":"+o.Sandbox)
		mu.Unlock()
		if o.Model == "a" {
			return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrAuth, Message: "Not logged in"}}, nil
		}
		if o.Sandbox != "" {
			return nil, fmt.Errorf("%w: unsupported", monomind.ErrSandboxRequired)
		}
		on(monomind.Event{Type: monomind.EventStart})
		return okTurn("found it"), nil
	}
	c := New(context.Background(), Config{Cwd: "/w", Staffer: &Staffer{Roster: []Model{a, b}, Lead: a}, Exec: exec, Emit: &recEmitter{}})
	defer c.Close()
	info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate", Access: ProfileResearch, Runtime: "copilot", Model: "a", Wait: true})
	mu.Lock()
	defer mu.Unlock()
	if info.Status != chatevents.AgentDone || strings.Join(calls, ",") != "a:read-only,b:read-only,b:" {
		t.Fatalf("status %s, execs %v; want a (auth), b refused, then b unsandboxed", info.Status, calls)
	}
}
