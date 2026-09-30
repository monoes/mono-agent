package dynorg

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// subagentStream is protocol §3.2.1's claude example
// (doc/agent-exec-protocol/fixtures/subagent.ndjson), abridged.
var subagentStream = []string{
	`{"v":1,"type":"start","runtime":"claude"}`,
	`{"v":1,"type":"assistant","text":"Let me look."}`,
	`{"v":1,"type":"tool_activity","id":"toolu_task","phase":"start","name":"Task","kind":"task","input":{"subagent_type":"Explore","description":"Find config loader","prompt":"Find where the app loads its config."}}`,
	`{"v":1,"type":"subagent","phase":"started","id":"task_1","tool_use_id":"toolu_task","subagent_type":"Explore","description":"Find config loader","prompt":"Find where the app loads its config."}`,
	`{"v":1,"type":"assistant","text":"Searching for loadConfig.","parent_tool_use_id":"toolu_task"}`,
	`{"v":1,"type":"tool_activity","id":"toolu_grep","phase":"start","name":"Grep","input":{"pattern":"loadConfig"},"parent_tool_use_id":"toolu_task"}`,
	`{"v":1,"type":"tool_activity","id":"toolu_grep","phase":"end","name":"Grep","ok":true,"output":"src/config.ts"}`,
	`{"v":1,"type":"subagent","phase":"progress","id":"task_1","tool_use_id":"toolu_task","summary":"Found loadConfig in src/config.ts","last_tool":"Grep","usage":{"total_tokens":4210,"tool_uses":1,"duration_ms":2310}}`,
	`{"v":1,"type":"assistant","text":"It is in src/config.ts.","parent_tool_use_id":"toolu_task"}`,
	`{"v":1,"type":"subagent","phase":"finished","id":"task_1","tool_use_id":"toolu_task","status":"completed","summary":"Config is loaded by loadConfig() in src/config.ts.","usage":{"total_tokens":5120,"tool_uses":1,"duration_ms":3050}}`,
	`{"v":1,"type":"tool_activity","id":"toolu_task","phase":"end","name":"Task","ok":true,"output":"Config is loaded by loadConfig() in src/config.ts."}`,
	`{"v":1,"type":"assistant","text":"The config loader is loadConfig."}`,
}

func decodeEvents(t *testing.T, lines []string) []monomind.Event {
	t.Helper()
	out := make([]monomind.Event, len(lines))
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestSubagentsJournalsTheLifecycle(t *testing.T) {
	em := &recEmitter{}
	var s Subagents
	var rest []string
	for _, ev := range decodeEvents(t, subagentStream) {
		if !s.Handle(em, ev, "w1:", "w1") {
			rest = append(rest, ev.Type)
		}
	}
	// Everything that isn't the subagent's is left to the caller.
	if got := strings.Join(rest, " "); got != "start assistant tool_activity tool_activity tool_activity tool_activity assistant" {
		t.Errorf("unhandled = %s", got)
	}
	id := NativeAgentID("w1:toolu_task")
	if got, want := strings.Join(em.types(id), " "), "agent.spawned agent.status:working assistant.delta agent.status:working assistant.delta agent.message agent.finished"; got != want {
		t.Errorf("events\n got %s\nwant %s", got, want)
	}
	sp := em.find(chatevents.EventAgentSpawned)[0].(chatevents.AgentSpawnedPayload)
	if sp.AgentID != "native:w1:toolu_task" || sp.ParentID != "w1" || sp.AgentType != "native" || sp.Role != "Explore" || sp.Brief != "Find where the app loads its config." {
		t.Errorf("spawned = %+v", sp)
	}
	st := em.find(chatevents.EventAgentStatus)[1].(chatevents.AgentStatusPayload)
	if st.Detail != "Found loadConfig in src/config.ts" {
		t.Errorf("progress detail = %q", st.Detail)
	}
	texts := em.find(chatevents.EventAssistantDelta)
	if a, b := texts[0].(chatevents.AssistantDeltaPayload), texts[1].(chatevents.AssistantDeltaPayload); a.AgentID != id || a.Text != "Searching for loadConfig." || a.PartID == b.PartID {
		t.Errorf("text = %+v %+v", a, b)
	}
	msg := em.find(chatevents.EventAgentMessage)[0].(chatevents.AgentMessagePayload)
	if msg.Direction != "result" || msg.From != id || msg.To != "w1" {
		t.Errorf("result message = %+v", msg)
	}
	fin := em.find(chatevents.EventAgentFinished)[0].(chatevents.AgentFinishedPayload)
	if fin.Outcome != chatevents.AgentDone || fin.DurationMs != 3050 || !strings.Contains(fin.Summary, "loadConfig()") {
		t.Errorf("finished = %+v", fin)
	}
}

func TestSubagentsLeadOwnerAndOutcomes(t *testing.T) {
	for status, want := range map[string]string{"completed": chatevents.AgentDone, "failed": chatevents.AgentFailed, "denied": chatevents.AgentFailed, "stopped": chatevents.AgentCancelled} {
		em := &recEmitter{}
		var s Subagents
		// Rev 24's synthesized shape: id and tool_use_id are the call's id,
		// no progress, no usage.
		for _, ev := range decodeEvents(t, []string{
			`{"v":1,"type":"subagent","phase":"started","id":"call_task","tool_use_id":"call_task","description":"Find it"}`,
			`{"v":1,"type":"subagent","phase":"finished","id":"call_task","tool_use_id":"call_task","status":"` + status + `"}`,
			`{"v":1,"type":"subagent","phase":"finished","id":"call_task","tool_use_id":"call_task","status":"completed"}`,
		}) {
			s.Handle(em, ev, "", "")
		}
		sp := em.find(chatevents.EventAgentSpawned)[0].(chatevents.AgentSpawnedPayload)
		if sp.AgentID != "native:call_task" || sp.ParentID != "" || sp.Role != "Find it" || sp.Brief != "Find it" {
			t.Errorf("lead's subagent = %+v", sp)
		}
		fins := em.find(chatevents.EventAgentFinished)
		if len(fins) != 1 || fins[0].(chatevents.AgentFinishedPayload).Outcome != want {
			t.Errorf("%s: finished = %+v, want one %s", status, fins, want)
		}
		if n := len(em.find(chatevents.EventAgentMessage)); n != 0 {
			t.Errorf("%s: no summary, yet %d result messages", status, n)
		}
	}
}

// A worker's native subagent is journaled under it, and the subagent's
// text neither joins the worker's text nor becomes its report.
func TestWorkerSubagentEventsAreJournaled(t *testing.T) {
	events := decodeEvents(t, subagentStream)
	exec := func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		var run monomind.TurnResult
		for _, ev := range events {
			monomind.ApplyEventToResult(&run, ev)
			on(ev)
		}
		run.SawDone, run.StopReason = true, monomind.StopEndTurn
		return &run, nil
	}
	em := &recEmitter{}
	c := New(context.Background(), Config{Cwd: "/w", ReadAccess: true, Staffer: &Staffer{Roster: []Model{opus}, Lead: opus}, Exec: exec, Emit: em})
	defer c.Close()
	info, err := c.Spawn(context.Background(), SpawnRequest{Brief: "investigate the config", Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if info.Report != "The config loader is loadConfig." {
		t.Errorf("report = %q, want the worker's own last message", info.Report)
	}
	var workerText, nativeText []string
	for _, p := range em.find(chatevents.EventAssistantDelta) {
		d := p.(chatevents.AssistantDeltaPayload)
		switch d.AgentID {
		case "w1":
			workerText = append(workerText, d.Text)
		case NativeAgentID("w1:toolu_task"):
			nativeText = append(nativeText, d.Text)
		}
	}
	if got := strings.Join(workerText, ""); strings.Contains(got, "Searching") || !strings.Contains(got, "The config loader") {
		t.Errorf("worker text = %q", got)
	}
	if len(nativeText) != 2 {
		t.Errorf("subagent text = %q", nativeText)
	}
	var grep chatevents.ToolStartedPayload
	for _, p := range em.find(chatevents.EventToolStarted) {
		if ts := p.(chatevents.ToolStartedPayload); ts.Name == "Grep" {
			grep = ts
		}
	}
	if grep.ParentCallID != "w1:toolu_task" {
		t.Errorf("the subagent's call must nest under its Task call: %+v", grep)
	}
	sp := em.find(chatevents.EventAgentSpawned)
	if len(sp) != 2 || sp[1].(chatevents.AgentSpawnedPayload).ParentID != "w1" {
		t.Errorf("spawned = %+v", sp)
	}
}

// Two workers whose runtimes reuse a call id each get their subagent's
// finish: the ids are tracked per caller, not raw.
func TestSubagentsTrackedPerCaller(t *testing.T) {
	em := &recEmitter{}
	var s Subagents
	started := `{"v":1,"type":"subagent","phase":"started","id":"call_1","tool_use_id":"call_1","description":"Find it"}`
	finished := `{"v":1,"type":"subagent","phase":"finished","id":"call_1","tool_use_id":"call_1","status":"completed"}`
	for _, step := range []struct{ line, prefix, owner string }{
		{started, "w1:", "w1"}, {started, "w2:", "w2"}, {finished, "w1:", "w1"}, {finished, "w2:", "w2"},
	} {
		s.Handle(em, decodeEvents(t, []string{step.line})[0], step.prefix, step.owner)
	}
	var got []string
	for _, p := range em.find(chatevents.EventAgentFinished) {
		got = append(got, p.(chatevents.AgentFinishedPayload).AgentID)
	}
	if strings.Join(got, " ") != "native:w1:call_1 native:w2:call_1" {
		t.Errorf("finished = %v, want both workers' subagents", got)
	}
}
