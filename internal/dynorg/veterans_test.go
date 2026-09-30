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
