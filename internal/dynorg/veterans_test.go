package dynorg

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// keptWorkers collects what Config.Remember is handed, as the store would.
type keptWorkers struct {
	mu   sync.Mutex
	byID map[string]Veteran
	ids  []string
}

func (k *keptWorkers) remember(v Veteran) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byID == nil {
		k.byID = map[string]Veteran{}
	}
	if _, ok := k.byID[v.ID]; !ok {
		k.ids = append(k.ids, v.ID)
	}
	k.byID[v.ID] = v
}

func (k *keptWorkers) list() []Veteran {
	k.mu.Lock()
	defer k.mu.Unlock()
	var out []Veteran
	for _, id := range k.ids {
		out = append(out, k.byID[id])
	}
	return out
}

func newVeteranConductor(t *testing.T, ex *treeExec, cwd string, lim Limits, kept *keptWorkers) (*Conductor, *recEmitter) {
	t.Helper()
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: cwd, Limits: lim, Staffer: &Staffer{Roster: []Model{treeModel}, Lead: treeModel},
		ReadAccess: true, Exec: ex.exec, Emit: em, Remember: kept.remember})
	t.Cleanup(c.Close)
	return c, em
}

func TestVeteransAreKeptLoadedAndResumed(t *testing.T) {
	kept := &keptWorkers{}
	// Turn 1: a worker, and a worker with a sub-worker.
	c1, _ := newVeteranConductor(t, &treeExec{}, "/w", treeLimits, kept)
	c1.Spawn(context.Background(), SpawnRequest{Brief: "map the cache", Access: ProfileResearch, Wait: true})
	c1.Spawn(context.Background(), SpawnRequest{Brief: brief("fix it", m{"brief": "test it", "wait": true}), Access: ProfileCoding, AllowSpawn: true, Wait: true})
	c1.Close()
	vets := kept.list()
	if len(vets) != 3 {
		t.Fatalf("kept %d workers, want 3: %+v", len(vets), vets)
	}
	w1 := kept.byID["w1"]
	if w1.Session != "sess-opus" || w1.Cwd != "/w" || w1.Runtime != "claude" || w1.Model != "opus" || w1.Role == "" ||
		w1.Access != ProfileResearch || w1.Report != "did map the cache" || w1.Outcome != chatevents.AgentDone {
		t.Errorf("w1 kept as %+v", w1)
	}
	if v := kept.byID["w3"]; v.ParentID != "w2" || !kept.byID["w2"].AllowSpawn {
		t.Errorf("the tree wasn't kept: w2 %+v, w3 %+v", kept.byID["w2"], v)
	}

	// Turn 2: they come back idle. They hold no slot and don't count
	// toward the turn's workers, and new workers are numbered after them.
	ex := &treeExec{}
	c2, em := newVeteranConductor(t, ex, "/w", Limits{MaxAgents: 1, MaxConcurrent: 1}, kept)
	// A sub-worker listed before its parent (its run ended first) still
	// finds it.
	slices.Reverse(vets)
	c2.AddVeterans(vets)
	if n := len(c2.slots); n != 0 {
		t.Errorf("veterans took %d slots", n)
	}
	roster := c2.Roster()
	if len(roster.Workers) != 3 || roster.Limits["spawned"] != 0 {
		t.Fatalf("roster = %+v, limits %v", roster.Workers, roster.Limits)
	}
	for _, w := range roster.Workers {
		if !w.Veteran || w.Status != chatevents.AgentIdle {
			t.Errorf("veteran %s = %+v", w.ID, w)
		}
	}
	infos := c2.Wait(context.Background(), nil, 0)
	if len(infos) != 3 || infos[0].Report != "" || infos[0].Status != chatevents.AgentIdle {
		t.Errorf("org_wait on veterans = %+v", infos)
	}
	var spawned []chatevents.AgentSpawnedPayload
	for _, p := range em.find(chatevents.EventAgentSpawned) {
		spawned = append(spawned, p.(chatevents.AgentSpawnedPayload))
	}
	if len(spawned) != 3 || !spawned[0].Veteran || spawned[2].AgentID != "w3" || spawned[2].ParentID != "w2" {
		t.Errorf("veterans journaled as %+v", spawned)
	}
	if got := em.types("w1"); !slices.Equal(got, []string{"agent.spawned", "agent.status:idle"}) {
		t.Errorf("w1 events = %v", got)
	}

	info, err := c2.Spawn(context.Background(), SpawnRequest{Brief: "new work", Wait: true})
	if err != nil || info.ID != "w4" {
		t.Fatalf("new worker = %+v, %v", info, err)
	}
	// The lead messages a veteran: its session resumes.
	if _, err := c2.Message(context.Background(), "w1", "and the eviction?"); err != nil {
		t.Fatal(err)
	}
	infos = c2.Wait(context.Background(), []string{"w1"}, 0)
	if infos[0].Status != chatevents.AgentDone || infos[0].Report != "did and the eviction?" {
		t.Errorf("w1 after the follow-up = %+v", infos[0])
	}
	o, _ := ex.callFor("and the eviction?")
	if o.Resume != "sess-opus" || o.Access != "read" {
		t.Errorf("veteran exec: resume %q, access %q", o.Resume, o.Access)
	}
	if got := em.types("w1"); !slices.Contains(got, "agent.status:queued") || !slices.Contains(got, "agent.finished") {
		t.Errorf("w1 events = %v", got)
	}
	// Only its parent may message a veteran sub-worker.
	if _, err := c2.Message(context.Background(), "w3", "more"); err == nil {
		t.Error("the lead messaged a veteran sub-worker")
	}
	assertClean(t, c2)
}

func TestVeteranInAnotherFolderIsReBriefed(t *testing.T) {
	ex := &treeExec{}
	c, _ := newVeteranConductor(t, ex, "/elsewhere", treeLimits, &keptWorkers{})
	c.AddVeterans([]Veteran{
		{ID: "w2", Role: "Researcher", Access: ProfileResearch, Runtime: "claude", Model: "opus", Session: "sess-old", Cwd: "/w", Report: "the cache is in cache.go"},
		{ID: "w9", ParentID: "w5", Role: "Orphan", Access: ProfileCoding, Runtime: "claude", Model: "opus", Session: "s"},
	})
	if r := c.Roster(); len(r.Workers) != 1 {
		t.Errorf("a sub-worker without its parent was loaded: %+v", r.Workers)
	}
	c.Message(context.Background(), "w2", "where exactly?")
	c.Wait(context.Background(), nil, 0)
	o, _ := ex.callFor("Your earlier report")
	if o.Resume != "" || !strings.Contains(o.Prompt, "the cache is in cache.go") || !strings.Contains(o.Prompt, "where exactly?") {
		t.Errorf("a session from another folder must not resume: resume %q prompt %q", o.Resume, o.Prompt)
	}
	if info, _ := c.Spawn(context.Background(), SpawnRequest{Brief: "next"}); info.ID != "w3" {
		t.Errorf("new worker id = %s, want w3", info.ID)
	}
}

func TestVeteranWhoseModelIsNotReadyIsRefused(t *testing.T) {
	ex := &treeExec{}
	c, _ := newVeteranConductor(t, ex, "/w", treeLimits, &keptWorkers{})
	c.AddVeterans([]Veteran{{ID: "w1", Role: "Coder", Access: ProfileCoding, Runtime: "gone", Model: "m", Session: "s", Cwd: "/w", Report: "r"}})
	if r := c.Roster(); len(r.Workers) != 1 || !strings.Contains(r.Workers[0].Error, "no longer ready") {
		t.Errorf("roster = %+v", r.Workers)
	}
	if _, err := c.Message(context.Background(), "w1", "more"); err == nil || !strings.Contains(err.Error(), "no longer ready") {
		t.Errorf("a veteran on a model that isn't ready was messaged: %v", err)
	}
	if len(ex.calls) != 0 {
		t.Errorf("it ran: %+v", ex.calls)
	}
}

// A research worker's veteran sub-worker never runs unconfined: not when
// its model left the roster, not when its model no longer confines it,
// and not even if a run of it were started anyway.
func TestResearchVeteranSubWorkerStaysConfined(t *testing.T) {
	for _, tc := range []struct {
		name  string
		child Veteran
		want  string
	}{
		{"model gone", Veteran{Runtime: "codex", Model: "gone"}, "no longer ready"},
		{"model no longer confines", Veteran{Runtime: "codex", Model: "x"}, "can't run confined any more"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ex := &treeExec{}
			em := &recEmitter{}
			c := New(context.Background(), Config{Cwd: "/w", Limits: treeLimits, Staffer: &Staffer{Roster: []Model{treeModel, openModel}, Lead: treeModel},
				ReadAccess: true, Exec: ex.exec, Emit: em})
			t.Cleanup(c.Close)
			child := tc.child
			child.ID, child.ParentID, child.Role, child.Access, child.Session, child.Cwd, child.Report = "w2", "w1", "Researcher", ProfileResearch, "s2", "/w", "r2"
			c.AddVeterans([]Veteran{
				{ID: "w1", Role: "Researcher", Access: ProfileResearch, Runtime: "claude", Model: "opus", Session: "s1", Cwd: "/w", AllowSpawn: true},
				child,
			})
			c.mu.Lock()
			w1, w2 := c.workers["w1"], c.workers["w2"]
			c.mu.Unlock()
			if !w2.mustConfine {
				t.Fatal("the veteran sub-worker of a research worker must be confined")
			}
			if _, err := c.message(context.Background(), w1, "w2", "more"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message = %v, want %q", err, tc.want)
			}
			// Fail closed even past the check: the run is refused before
			// any exec, and it never takes the write lease.
			c.mu.Lock()
			ctx, cancel, done := c.prepareRunLocked(w2)
			c.mu.Unlock()
			c.launch(w2, ctx, cancel, done, "more", "s2", false)
			<-done
			if info := c.Wait(context.Background(), []string{"w2"}, 0)[0]; info.Status != chatevents.AgentFailed {
				t.Errorf("w2 = %+v", info)
			}
			if len(ex.calls) != 0 {
				t.Errorf("an unconfined exec ran: access %q", ex.calls[0].Access)
			}
			for _, p := range em.find(chatevents.EventAgentStatus) {
				if s := p.(chatevents.AgentStatusPayload); s.AgentID == "w2" && (slices.Contains(s.Leases, "write") || s.To == chatevents.AgentWaitingLease) {
					t.Errorf("w2 went for the write lease: %+v", s)
				}
			}
			assertClean(t, c)
		})
	}
}
