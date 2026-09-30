package dynorg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Tool names the lead gets.
const (
	ToolRoster  = "org_roster"
	ToolSpawn   = "org_spawn"
	ToolWait    = "org_wait"
	ToolMessage = "org_message"
	ToolStop    = "org_stop"
	ToolRate    = "org_rate"
)

// ToolTimeout is the lead's --tool-timeout: above MaxWait, so a wait
// always answers before monomind gives up on the call.
const ToolTimeout = MaxWait + 30*time.Second

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// ToolSpecs are the lead's org tools.
func ToolSpecs() []monomind.ToolSpec {
	return []monomind.ToolSpec{
		{Name: ToolRoster, Description: "List what you can staff workers with: the ready models (runtime, model, effort levels, access), the access profiles, the built-in roles, the limits, and the workers so far.",
			Schema: obj(map[string]any{})},
		{Name: ToolSpawn, Description: "Start a worker agent on a brief. It runs in the background in this chat's folder; use org_wait to read its report. Leave role, skills, model, effort or access out to have them picked for you.",
			Schema: obj(map[string]any{
				"brief":       str("What the worker should do, with everything it needs to know. It can't see this conversation."),
				"role":        str("A role: a monomind agent id (e.g. engineering-code-reviewer) or a built-in role (coder, reviewer, tester, researcher, planner)."),
				"skills":      strList("Skill names to give the worker."),
				"runtime":     str("Runtime to run on (e.g. claude, codex). Must have a ready model; see org_roster."),
				"model":       str("Model id on that runtime; see org_roster."),
				"effort":      str("Reasoning effort, one of the model's effort levels."),
				"access":      str("Access profile: coding (default for writers), qa, automation, or research (read only)."),
				"files":       strList("Files the worker should keep to."),
				"needs_write": map[string]any{"type": "boolean", "description": "The worker will edit files (it then waits its turn for the write lease)."},
				"wait":        map[string]any{"type": "boolean", "description": "Wait for the worker to finish (up to 100s) before returning."},
			}, "brief")},
		{Name: ToolWait, Description: "Wait for workers to finish (up to timeout_s, at most 100) and get their reports. Call it again for workers still running.",
			Schema: obj(map[string]any{
				"agent_ids": strList("Workers to wait for; empty = all."),
				"timeout_s": map[string]any{"type": "integer", "description": "Seconds to wait, 1-100 (default 100)."},
			})},
		{Name: ToolMessage, Description: "Send a follow-up to a worker that has finished; it continues where it left off. Then org_wait for its new report.",
			Schema: obj(map[string]any{"agent_id": str("The worker."), "text": str("The follow-up.")}, "agent_id", "text")},
		{Name: ToolStop, Description: "Stop a running worker.",
			Schema: obj(map[string]any{"agent_id": str("The worker.")}, "agent_id")},
		{Name: ToolRate, Description: "Rate a worker's latest report after you read it: good if it did the job, bad if it didn't. Ratings teach staffing which models fit which kind of work; rate each report once.",
			Schema: obj(map[string]any{"agent_id": str("The worker."), "rating": str("good or bad.")}, "agent_id", "rating")},
	}
}

// Handle runs one of the lead's org tool calls. A refusal (a limit, an
// unknown model) is returned as an error, which the lead reads as the
// tool's failed result.
func (c *Conductor) Handle(ctx context.Context, name string, args json.RawMessage) (string, error) {
	out, err := c.handle(ctx, name, args)
	// Edits the lead made while a writer held the write lease (LeadEvent)
	// are reported with its next org tool call, which it reads.
	if warnings := c.takeLeadWarnings(); len(warnings) > 0 {
		if err != nil {
			return "", fmt.Errorf("%w (warning: %s)", err, strings.Join(warnings, " "))
		}
		return marshal(map[string]any{"warnings": warnings, "result": json.RawMessage(out)})
	}
	return out, err
}

func (c *Conductor) handle(ctx context.Context, name string, args json.RawMessage) (string, error) {
	switch name {
	case ToolRoster:
		return marshal(c.Roster())
	case ToolSpawn:
		var req SpawnRequest
		if err := json.Unmarshal(orEmpty(args), &req); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		info, err := c.Spawn(ctx, req)
		if err != nil {
			return "", err
		}
		return marshal(info)
	case ToolWait:
		var a struct {
			AgentIDs []string `json:"agent_ids"`
			Timeout  int      `json:"timeout_s"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		return marshal(map[string]any{"workers": c.Wait(ctx, a.AgentIDs, time.Duration(a.Timeout)*time.Second)})
	case ToolMessage:
		var a struct {
			AgentID string `json:"agent_id"`
			Text    string `json:"text"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		info, err := c.Message(ctx, a.AgentID, a.Text)
		if err != nil {
			return "", err
		}
		return marshal(info)
	case ToolStop:
		var a struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		info, err := c.Stop(a.AgentID)
		if err != nil {
			return "", err
		}
		return marshal(info)
	case ToolRate:
		var a struct {
			AgentID string `json:"agent_id"`
			Rating  string `json:"rating"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		res, err := c.Rate(a.AgentID, a.Rating)
		if err != nil {
			return "", err
		}
		return marshal(res)
	case ToolMerge:
		var a struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil {
			return "", fmt.Errorf("bad arguments: %v", err)
		}
		res, err := c.Merge(ctx, a.AgentID)
		if err != nil {
			return "", err
		}
		return marshal(res)
	}
	return "", fmt.Errorf("unknown org tool %q", name)
}

// RosterView is org_roster's result.
type RosterView struct {
	Models   []Model           `json:"models"`
	Profiles map[string]string `json:"access_profiles"`
	Roles    []map[string]any  `json:"builtin_roles"`
	RoleNote string            `json:"roles_note"`
	Limits   map[string]any    `json:"limits"`
	Workers  []WorkerInfo      `json:"workers"`
}

// Roster describes what the lead can staff with, and the workers so far.
func (c *Conductor) Roster() RosterView {
	v := RosterView{
		Models:   c.cfg.Staffer.Roster,
		Profiles: profileRules,
		RoleNote: "role also accepts any monomind agent id from this folder's agent registry; leave it empty to have one picked for the brief",
	}
	for _, r := range builtinRoles {
		v.Roles = append(v.Roles, map[string]any{"id": r.ID, "title": r.Title, "default_access": r.Profile})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v.Limits = map[string]any{
		"max_agents": c.cfg.Limits.MaxAgents, "max_concurrent": c.cfg.Limits.MaxConcurrent,
		"spawned": c.spawned, "budget_usd": c.cfg.Limits.BudgetUSD, "spent_usd": c.cost,
	}
	v.Workers = []WorkerInfo{}
	for _, id := range c.order {
		v.Workers = append(v.Workers, c.infoLocked(c.workers[id], false))
	}
	return v
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func orEmpty(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return b
}
