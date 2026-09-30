package dynorg

import (
	"slices"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// Worker follow-ups across turns (#230): workers persist per conversation.
// After each run the conductor hands Config.Remember the worker (its
// session, folder, model and role); the next turn's conductor loads the
// conversation's latest ones with AddVeterans as idle veterans. The lead
// sees them in org_roster and org_wait and can org_message them, which
// resumes their session (in the same folder, on a runtime that resumes)
// or re-briefs them with their last report. A veteran holds no slot until
// it runs, and doesn't count toward the turn's workers.

// MaxVeterans caps the veterans a turn loads: the conversation's latest.
const MaxVeterans = 12

// Veteran is a worker kept for a conversation's later turns.
type Veteran struct {
	ID         string
	ParentID   string // "" = the lead
	Role       string
	AgentType  string
	Category   string
	Access     string
	Skills     []string
	Runtime    string
	Model      string
	Effort     string
	Session    string
	Cwd        string
	Report     string
	Outcome    string
	AllowSpawn bool
}

// remember hands w to Config.Remember once a run of it ended: only a
// worker that has something to continue from (a session or a report).
func (c *Conductor) remember(w *worker) {
	if c.cfg.Remember == nil {
		return
	}
	c.mu.Lock()
	v := Veteran{
		ID: w.id, ParentID: parentID(w), Role: w.staff.Role, AgentType: w.staff.AgentType, Category: w.staff.Category,
		Access: w.staff.Access, Runtime: w.model.Runtime, Model: w.model.Model, Effort: w.staff.Effort,
		Session: w.session, Cwd: w.cwd, Report: w.report, Outcome: w.status, AllowSpawn: w.allowSpawn,
	}
	for _, s := range w.staff.Skills {
		v.Skills = append(v.Skills, s.Name)
	}
	c.mu.Unlock()
	if v.Session == "" && v.Report == "" {
		return
	}
	c.cfg.Remember(v)
}

// AddVeterans loads workers of the conversation's earlier turns (oldest
// first) as idle veterans, and journals each (agent.spawned with veteran
// set, then agent.status idle) so the stage shows them greyed out. A
// sub-worker whose parent isn't among them is left out: only its parent
// may message it. New workers are numbered after them.
func (c *Conductor) AddVeterans(vets []Veteran) {
	// Parents first: a parent's last run can end after its sub-worker's.
	vets = slices.Clone(vets)
	slices.SortStableFunc(vets, func(a, b Veteran) int {
		switch {
		case a.ParentID == "" && b.ParentID != "":
			return -1
		case a.ParentID != "" && b.ParentID == "":
			return 1
		}
		return 0
	})
	c.mu.Lock()
	var added []*worker
	for _, v := range vets {
		if v.ID == "" || c.workers[v.ID] != nil {
			continue
		}
		var parent *worker
		if v.ParentID != "" {
			if parent = c.workers[v.ParentID]; parent == nil || !parent.veteran {
				continue
			}
		}
		st := Staff{Role: v.Role, AgentType: v.AgentType, Category: v.Category, Access: v.Access, Effort: v.Effort, Model: c.veteranModel(v)}
		for _, s := range v.Skills {
			st.Skills = append(st.Skills, Skill{Name: s})
		}
		w := &worker{
			id: v.ID, staff: st, model: st.Model, status: chatevents.AgentIdle, report: v.Report, session: v.Session,
			changed: map[string]bool{}, started: c.cfg.Now(), parent: parent, allowSpawn: v.AllowSpawn && parent == nil,
			mustConfine: parent != nil && parent.staff.Access == ProfileResearch, veteran: true, cwd: v.Cwd,
		}
		c.workers[w.id] = w
		c.order = append(c.order, w.id)
		if n, err := strconv.Atoi(strings.TrimPrefix(w.id, "w")); err == nil && n > c.seq {
			c.seq = n
		}
		added = append(added, w)
	}
	c.mu.Unlock()
	for _, w := range added {
		skills := make([]string, len(w.staff.Skills))
		for i, s := range w.staff.Skills {
			skills[i] = s.Name
		}
		c.cfg.Emit.Emit(chatevents.EventAgentSpawned, chatevents.AgentSpawnedPayload{
			AgentID: w.id, ParentID: parentID(w), Veteran: true, AllowSpawn: w.allowSpawn, Role: w.staff.Role, AgentType: w.staff.AgentType,
			Skills: skills, Runtime: w.model.Runtime, Model: w.model.Model, Fidelity: w.model.Fidelity, Effort: w.staff.Effort,
			Access: w.staff.Access, Why: "from an earlier turn of this chat",
		})
		c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, To: chatevents.AgentIdle, Detail: "veteran"})
	}
}

// veteranModel is the roster entry a veteran ran on; when the roster no
// longer lists that model, the runtime's abilities come from another of
// its models (they are the runtime's), else it is taken as it was.
func (c *Conductor) veteranModel(v Veteran) Model {
	m := Model{Runtime: v.Runtime, Model: v.Model, FullAccess: true}
	if c.cfg.Staffer == nil {
		return m
	}
	for _, r := range c.cfg.Staffer.Roster {
		if r.Runtime == v.Runtime && r.Model == v.Model {
			return r
		}
	}
	for _, r := range append([]Model{c.cfg.Staffer.Lead}, c.cfg.Staffer.Roster...) {
		if r.Runtime == v.Runtime {
			r.Model, r.Label, r.Efforts, r.CostUSD, r.LatencyMs = v.Model, "", nil, 0, 0
			return r
		}
	}
	return m
}
