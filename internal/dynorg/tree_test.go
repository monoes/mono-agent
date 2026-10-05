package dynorg

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// treeExec is a fake agent exec for spawn trees: each "\nSPAWN {json}" line
// of the prompt (see brief) is an org_spawn call the worker makes (when it has the tool), and
// a prompt whose first line has "slow" runs until cancelled or hold passes.
type treeExec struct {
	mu    sync.Mutex
	calls []monomind.ExecOptions
	hold  time.Duration
}

func (e *treeExec) exec(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, o)
	e.mu.Unlock()
	on(monomind.Event{Type: monomind.EventStart})
	on(monomind.Event{Type: monomind.EventSession, SessionID: "sess-" + o.Model})
	head := strings.SplitN(o.Prompt, "\nSPAWN ", 2)[0]
	report := "did " + head
	for _, call := range strings.Split(o.Prompt, "\nSPAWN ")[1:] {
		if o.OnToolCall == nil {
			report += " | no org tools"
			continue
		}
		out, err := o.OnToolCall(ctx, ToolSpawn, json.RawMessage(strings.TrimSpace(call)))
		if err != nil {
			report += " | error: " + err.Error()
			continue
		}
		report += " | " + out
	}
	if strings.Contains(head, "slow") {
		select {
		case <-time.After(e.hold):
		case <-ctx.Done():
		}
	}
	if ctx.Err() != nil {
		return &monomind.TurnResult{SawDone: true, StopReason: monomind.StopCancelled}, nil
	}
	return okTurn(report), nil
}

// brief is a worker's brief for treeExec: head, then one org_spawn call per
// args.
func brief(head string, calls ...map[string]any) string {
	for _, a := range calls {
		b, _ := json.Marshal(a)
		head += "\nSPAWN " + string(b)
	}
	return head
}

type m = map[string]any

func (e *treeExec) callFor(prompt string) (monomind.ExecOptions, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, o := range e.calls {
		if strings.HasPrefix(o.Prompt, prompt) {
			return o, true
		}
	}
	return monomind.ExecOptions{}, false
}

var (
	treeModel  = Model{Runtime: "claude", Model: "opus", FullAccess: true, Read: true, CallerTools: true, CallerToolsFull: true, Resume: true}
	openModel  = Model{Runtime: "codex", Model: "x", FullAccess: true, CallerTools: true, CallerToolsFull: true}
	treeLimits = Limits{MaxAgents: 6, MaxConcurrent: 3}
)

func newTreeConductor(t *testing.T, ex *treeExec, lim Limits, roster ...Model) (*Conductor, *recEmitter) {
	t.Helper()
	if len(roster) == 0 {
		roster = []Model{treeModel}
	}
	if ex.hold == 0 {
		ex.hold = 5 * time.Second
	}
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", Limits: lim, Staffer: &Staffer{Roster: roster, Lead: roster[0]},
		ReadAccess: true, Exec: ex.exec, Emit: em})
	t.Cleanup(c.Close)
	return c, em
}

func toolNames(o monomind.ExecOptions) []string {
	var out []string
	for _, t := range o.Tools {
		out = append(out, t.Name)
	}
	return out
}

func TestSpawnDepthIsCappedAtTwo(t *testing.T) {
	ex := &treeExec{}
	c, em := newTreeConductor(t, ex, treeLimits)

	// Without allow_spawn a worker gets no org tools.
	c.Spawn(context.Background(), SpawnRequest{Brief: brief("plain", m{"brief": "x"}), Access: ProfileCoding, Wait: true})
	if o, _ := ex.callFor("plain"); len(o.Tools) != 0 || strings.Contains(o.SystemPrompt, "sub-workers") {
		t.Fatalf("a worker the lead didn't allow to spawn got %v", toolNames(o))
	}

	info, err := c.Spawn(context.Background(), SpawnRequest{
		Brief:  brief("parent", m{"brief": brief("child", m{"brief": "grandchild"}), "wait": true}, m{"brief": "eager", "allow_spawn": true}),
		Access: ProfileCoding, AllowSpawn: true, Wait: true,
	})
	if err != nil || info.Status != chatevents.AgentDone || !info.AllowSpawn {
		t.Fatalf("parent = %+v, %v", info, err)
	}
	parent, _ := ex.callFor("parent")
	if got := toolNames(parent); !slices.Equal(got, []string{ToolSpawn, ToolWait, ToolMessage}) {
		t.Errorf("parent's tools = %v", got)
	}
	if _, ok := parent.Tools[0].Schema["properties"].(map[string]any)["allow_spawn"]; ok {
		t.Error("a worker's org_spawn must not offer allow_spawn")
	}
	if !strings.Contains(parent.SystemPrompt, "sub-workers with org_spawn") {
		t.Error("the parent's prompt doesn't tell it about sub-workers")
	}
	child, ok := ex.callFor("child")
	if !ok || len(child.Tools) != 0 || !strings.Contains(child.SystemPrompt, "can't hand work") {
		t.Fatalf("a sub-worker must get no org tools: %v", toolNames(child))
	}
	if !strings.Contains(info.Report, "no org tools") || !strings.Contains(info.Report, "can't start workers of its own") {
		t.Errorf("parent report = %s", info.Report)
	}
	// And the conductor refuses a sub-worker's spawn even if one got there.
	c.mu.Lock()
	w3 := c.workers["w3"]
	c.mu.Unlock()
	if _, err := c.handle(context.Background(), w3, ToolSpawn, json.RawMessage(`{"brief":"deeper"}`)); err == nil {
		t.Error("a sub-worker's org_spawn went through")
	}
	if _, err := c.handle(context.Background(), w3, ToolRoster, nil); err == nil {
		t.Error("a worker got org_roster")
	}

	var spawned []chatevents.AgentSpawnedPayload
	for _, p := range em.find(chatevents.EventAgentSpawned) {
		spawned = append(spawned, p.(chatevents.AgentSpawnedPayload))
	}
	if len(spawned) != 3 || spawned[2].AgentID != "w3" || spawned[2].ParentID != "w2" || spawned[1].ParentID != "" || !spawned[1].AllowSpawn {
		t.Errorf("agent.spawned = %+v", spawned)
	}
	for _, p := range em.find(chatevents.EventAgentMessage) {
		if m := p.(chatevents.AgentMessagePayload); m.AgentID == "w3" && m.Direction == "brief" && m.From != "w2" {
			t.Errorf("w3's brief is from %q, want w2", m.From)
		}
	}
	// The lead can't message a sub-worker; its parent can.
	if _, err := c.Message(context.Background(), "w3", "more"); err == nil || !strings.Contains(err.Error(), "w2's sub-worker") {
		t.Errorf("lead messaged a sub-worker: %v", err)
	}
	c.mu.Lock()
	w1 := c.workers["w1"]
	c.mu.Unlock()
	if _, err := c.message(context.Background(), w1, "w3", "more"); err == nil {
		t.Error("a worker messaged another worker's sub-worker")
	}
	assertClean(t, c)
}

func TestSubWorkerAccessNeverExceedsItsParent(t *testing.T) {
	for _, tc := range []struct {
		parent, child string
		ok            bool
	}{
		{ProfileResearch, ProfileResearch, true}, {ProfileResearch, ProfileCoding, false}, {ProfileResearch, ProfileQA, false},
		{ProfileCoding, ProfileResearch, true}, {ProfileCoding, ProfileCoding, true}, {ProfileCoding, ProfileQA, false},
		{ProfileCoding, ProfileAutomation, false}, {ProfileQA, ProfileQA, true}, {ProfileQA, ProfileCoding, true},
		{ProfileQA, ProfileAutomation, false}, {ProfileAutomation, ProfileAutomation, true}, {ProfileAutomation, ProfileQA, false},
	} {
		if got := accessWithin(tc.parent, tc.child); got != tc.ok {
			t.Errorf("accessWithin(%s, %s) = %v", tc.parent, tc.child, got)
		}
	}

	ex := &treeExec{}
	// opus confines a research worker (--access read); codex can't.
	c, _ := newTreeConductor(t, ex, treeLimits, treeModel, openModel)
	info, err := c.Spawn(context.Background(), SpawnRequest{
		Brief: brief("research-parent", m{"brief": "write it", "access": "coding"}, m{"brief": "edit it", "needs_write": true},
			m{"brief": "read on codex", "runtime": "codex", "model": "x"}, m{"brief": "read it", "wait": true}),
		Access: ProfileResearch, AllowSpawn: true, Wait: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`access "coding" exceeds yours`, "needs_write", "can't run confined"} {
		if !strings.Contains(info.Report, want) {
			t.Errorf("research parent report lacks %q: %s", want, info.Report)
		}
	}
	child, ok := ex.callFor("read it")
	if !ok || child.Access != monomind.AccessRead || child.Model != "opus" {
		t.Fatalf("a research parent's sub-worker must run confined: %+v", child)
	}
	c.mu.Lock()
	sub := c.workers["w2"]
	c.mu.Unlock()
	if sub.staff.Access != ProfileResearch || !sub.mustConfine || slices.ContainsFunc(sub.staff.Fallbacks, func(m Model) bool { return !c.confined(m) }) {
		t.Errorf("sub-worker = %s, mustConfine %v, fallbacks %v", sub.staff.Access, sub.mustConfine, sub.staff.Fallbacks)
	}

	// A qa parent's sub-worker: qa or coding, never automation; an
	// automation pick is lowered to coding.
	ex2 := &treeExec{}
	c2, _ := newTreeConductor(t, ex2, treeLimits)
	info, _ = c2.Spawn(context.Background(), SpawnRequest{
		Brief:  brief("qa-parent", m{"brief": "automate it", "access": "automation"}, m{"brief": "test it", "access": "qa", "wait": true}),
		Access: ProfileQA, AllowSpawn: true, Wait: true,
	})
	if !strings.Contains(info.Report, `access "automation" exceeds yours`) || !strings.Contains(info.Report, `"access":"qa"`) {
		t.Errorf("qa parent report = %s", info.Report)
	}
	st := Staff{Access: ProfileAutomation, Model: treeModel}
	c2.mu.Lock()
	qaParent := c2.workers["w1"]
	c2.mu.Unlock()
	if err := c2.fitChild(qaParent, SpawnRequest{}, &st); err != nil || st.Access != ProfileCoding {
		t.Errorf("a picked automation access under qa = %s, %v; want coding", st.Access, err)
	}
}

func TestLimitsCountTheWholeTree(t *testing.T) {
	ex := &treeExec{}
	c, _ := newTreeConductor(t, ex, Limits{MaxAgents: 2, MaxConcurrent: 3})
	info, _ := c.Spawn(context.Background(), SpawnRequest{
		Brief:  brief("parent", m{"brief": "one", "wait": true}, m{"brief": "two"}),
		Access: ProfileCoding, AllowSpawn: true, Wait: true,
	})
	if !strings.Contains(info.Report, "already has its 2 workers") {
		t.Errorf("the second sub-worker went past the turn's 2 workers: %s", info.Report)
	}
	if _, err := c.Spawn(context.Background(), SpawnRequest{Brief: "lead's second"}); err == nil {
		t.Error("the lead spawned past the limit its worker's sub-worker used")
	}

	// Budget: the sub-worker's cost counts, so the next spawn is refused.
	ex2 := &treeExec{}
	c2, _ := newTreeConductor(t, ex2, Limits{MaxAgents: 6, MaxConcurrent: 3, BudgetUSD: 0.01})
	info, _ = c2.Spawn(context.Background(), SpawnRequest{
		Brief:  brief("parent", m{"brief": "one", "wait": true}, m{"brief": "two"}),
		Access: ProfileCoding, AllowSpawn: true, Wait: true,
	})
	if !strings.Contains(info.Report, "budget") {
		t.Errorf("a sub-worker's cost didn't count toward the budget: %s", info.Report)
	}

	// Concurrency: with one slot, a writer waiting for its writing
	// sub-worker holds neither the slot nor the write lease meanwhile.
	ex3 := &treeExec{}
	c3, _ := newTreeConductor(t, ex3, Limits{MaxAgents: 6, MaxConcurrent: 1})
	done := make(chan WorkerInfo, 1)
	go func() {
		info, _ := c3.Spawn(context.Background(), SpawnRequest{
			Brief: brief("parent", m{"brief": "write sub", "access": "coding", "wait": true}), Access: ProfileCoding, AllowSpawn: true, Wait: true,
		})
		done <- info
	}()
	select {
	case info := <-done:
		if info.Status != chatevents.AgentDone || !strings.Contains(info.Report, `"status":"done"`) {
			t.Errorf("parent = %+v", info)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a parent waiting for its sub-worker deadlocked on the only slot or the write lease")
	}
	assertClean(t, c3)
}

func TestSubWorkersStopWithTheirParent(t *testing.T) {
	ex := &treeExec{hold: 10 * time.Second}
	c, em := newTreeConductor(t, ex, treeLimits)
	// The parent finishes without waiting: its running sub-worker ends.
	start := time.Now()
	c.Spawn(context.Background(), SpawnRequest{Brief: brief("parent", m{"brief": "slow sub"}), Access: ProfileCoding, AllowSpawn: true, Wait: true})
	infos := c.Wait(context.Background(), []string{"w2"}, 5*time.Second)
	if infos[0].Status != chatevents.AgentCancelled || time.Since(start) > 5*time.Second {
		t.Errorf("sub-worker after its parent finished = %+v", infos[0])
	}

	// org_stop on a parent that waits for its sub-worker stops both.
	go c.Spawn(context.Background(), SpawnRequest{Brief: brief("parent2", m{"brief": "slow sub2", "wait": true}), Access: ProfileCoding, AllowSpawn: true})
	waitFor(t, func() bool {
		_, ok := ex.callFor("slow sub2")
		return ok
	})
	if _, err := c.Stop("w3"); err != nil {
		t.Fatal(err)
	}
	infos = c.Wait(context.Background(), []string{"w3", "w4"}, 5*time.Second)
	for _, i := range infos {
		if i.Status != chatevents.AgentCancelled {
			t.Errorf("%s = %s, want cancelled", i.ID, i.Status)
		}
	}
	if types := em.types("w4"); !slices.Contains(types, "agent.finished") {
		t.Errorf("w4 events = %v", types)
	}
	assertClean(t, c)
}
