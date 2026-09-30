package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// The lead's native subagent (monomind#387): its lifecycle and text are
// journaled as agent.* events for "native:<call id>", and its text stays
// out of the lead's own.
func TestJournalLeadSubagentEvents(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "claude", "", "")
	store.CreateTurn(conv.ID, "default", "t1", "", "hi")
	var out bytes.Buffer
	j := newTurnJournal(store, "default", conv.ID, "t1", "claude", &out)
	for _, line := range []string{
		`{"v":1,"type":"tool_activity","id":"toolu_task","phase":"start","name":"Task","input":{"subagent_type":"Explore","prompt":"Find the loader."}}`,
		`{"v":1,"type":"subagent","phase":"started","id":"task_1","tool_use_id":"toolu_task","subagent_type":"Explore","prompt":"Find the loader."}`,
		`{"v":1,"type":"assistant","text":"Searching.","parent_tool_use_id":"toolu_task"}`,
		`{"v":1,"type":"subagent","phase":"progress","id":"task_1","tool_use_id":"toolu_task","summary":"Found it","last_tool":"Grep"}`,
		`{"v":1,"type":"subagent","phase":"finished","id":"task_1","tool_use_id":"toolu_task","status":"completed","summary":"It is loadConfig."}`,
		`{"v":1,"type":"tool_activity","id":"toolu_task","phase":"end","name":"Task","ok":true,"output":"It is loadConfig."}`,
		`{"v":1,"type":"assistant","text":"Done."}`,
	} {
		j.handle(monomindEvent(t, line))
	}
	j.finish(false, nil)

	evs, _ := store.GetEvents(conv.ID, "t1", "default", 0, 100)
	var got []string
	for _, ev := range evs {
		var p struct {
			AgentID string `json:"agentId"`
			To      string `json:"to"`
			Text    string `json:"text"`
		}
		json.Unmarshal(ev.Payload, &p)
		s := string(ev.Type)
		if p.AgentID != "" {
			s += "@" + p.AgentID
		}
		if ev.Type == chatevents.EventAssistantDelta {
			s += ":" + p.Text
		}
		got = append(got, s)
	}
	want := "tool.started|agent.spawned@native:toolu_task|agent.status@native:toolu_task|assistant.delta@native:toolu_task:Searching.|" +
		"agent.status@native:toolu_task|agent.message@native:toolu_task|agent.finished@native:toolu_task|tool.completed|assistant.delta:Done.|turn.finished"
	if strings.Join(got, "|") != want {
		t.Errorf("journal = %s\nwant      %s", strings.Join(got, "|"), want)
	}
	if j.usage.ResultText != "Done." {
		t.Errorf("the lead's answer = %q, want its own text only", j.usage.ResultText)
	}
}

// Without agent-exec-access-read (older monomind), a research worker runs
// with full access and the prompt fallback, and the journal says so.
func TestDynamicOrgResearchWithoutAccessReadRunsFull(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	dbPath := newChatCLITestDB(t)
	bin, argsLog := writeOrgMonomind(t)
	withCoderCaps(t, append(monomind.CoderCapabilities, monomind.CapAgentExecFullAccessTools)...)
	withCoderScan(t, orgScan(true))
	setCoderSettings(t, dbPath, coderSettings{Enabled: true})
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "dynamic"); code != 0 {
		t.Fatal("set-org failed")
	}
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "where is the cache?")
	if err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	logged, _ := os.ReadFile(argsLog)
	var execs []string
	for _, l := range strings.Split(string(logged), "\n") {
		if strings.HasPrefix(l, "agent exec") {
			execs = append(execs, l)
		}
	}
	if len(execs) != 2 {
		t.Fatalf("execs = %q", execs)
	}
	for _, l := range execs {
		if strings.Contains(l, "--access read") || !strings.Contains(l, "--access full") {
			t.Errorf("without the capability every exec is --access full: %s", l)
		}
	}
	j := parseJournaledTurn(t, out)
	confinement := ""
	for _, r := range j.byType(chatevents.EventAgentStatus) {
		var p chatevents.AgentStatusPayload
		json.Unmarshal(r.Payload, &p)
		if p.AgentID == "w1" && p.To == chatevents.AgentWorking {
			confinement = p.Confinement
		}
	}
	if confinement != chatevents.ConfinementWriteLease {
		t.Errorf("journaled confinement = %q, want %q", confinement, chatevents.ConfinementWriteLease)
	}
}
