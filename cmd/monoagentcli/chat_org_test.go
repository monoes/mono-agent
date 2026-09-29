package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// writeOrgMonomind is a fake monomind for a dynamic-org turn. The lead's
// exec calls org_spawn (waiting) and reports the tool result it gets back;
// a worker's exec (its system prompt names it a worker) edits a file and
// reports. `pick` ranks nothing and `org skills show` fails, so staffing
// uses the built-in roles.
func writeOrgMonomind(t *testing.T) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "monomind")
	argsLog = filepath.Join(dir, "args.log")
	script := `#!/bin/sh
echo "$*" >> '` + argsLog + `'
if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi
if [ "$1" = "pick" ]; then echo '{"agents":{"confident":false,"ranked":[]},"skills":{"confident":false,"ranked":[]}}'; exit 0; fi
if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then
  sys=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--system-file" ]; then sys="$a"; fi
    prev="$a"
  done
  if [ -n "$sys" ] && grep -q "a worker in a team" "$sys"; then
    echo '{"v":1,"type":"start","runtime":"claude","cwd":"/w","pid":2,"access":"read"}'
    echo '{"v":1,"type":"session","session_id":"th_worker"}'
    echo '{"v":1,"type":"tool_activity","id":"r1","phase":"start","name":"Read","input":{"file_path":"/w/cache.go"}}'
    echo '{"v":1,"type":"tool_activity","id":"r1","phase":"end","name":"Read","ok":true,"output":"package cache"}'
    echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"The cache is in cache.go.","cost_usd":0.002}'
    echo '{"v":1,"type":"done","exit_code":0}'
    exit 0
  fi
  echo '{"v":1,"type":"start","runtime":"claude","cwd":"/w","pid":1,"access":"full"}'
  echo '{"v":1,"type":"session","session_id":"th_lead"}'
  echo '{"v":1,"type":"tool_call","id":"c1","name":"org_spawn","args":{"brief":"investigate where the cache lives","wait":true}}'
  read -r reply
  printf '%s\n' "$reply" > '` + filepath.Join(dir, "reply.json") + `'
  echo '{"v":1,"type":"assistant","text":"The team found it."}'
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"The team found it."}'
  echo '{"v":1,"type":"done","exit_code":0}'
  exit 0
fi
echo "unsupported: $*" >&2
exit 2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	return bin, argsLog
}

func orgScan(callerTools bool) monomind.ScanEntry {
	return monomind.ScanEntry{ID: "claude", Installed: true, FullAccess: true, ToolActivityFidelity: "full", Resume: true,
		AccessModes: []string{"scoped", "read", "full"}, CallerTools: true, CallerToolsWithFullAccess: callerTools}
}

func TestDynamicOrgTurnSpawnsAndJournalsWorkers(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "") // staffing must not reach Jev from a test
	dbPath := newChatCLITestDB(t)
	bin, argsLog := writeOrgMonomind(t)
	withCoderCaps(t, append(monomind.CoderCapabilities, monomind.CapAgentExecFullAccessTools, monomind.CapAgentExecAccessRead)...)
	withCoderScan(t, orgScan(true))
	setCoderSettings(t, dbPath, coderSettings{Enabled: true})
	cwd := t.TempDir()
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, cwd)
	if err != nil {
		t.Fatal(err)
	}
	out, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "dynamic")
	var rec ai.ConversationRecord
	decodeChatJSON(t, out, &rec)
	if code != 0 || rec.OrgMode != ai.OrgModeDynamic {
		t.Fatalf("set-org: exit %d %s", code, out)
	}

	out, err = runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "where is the cache?")
	if err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	logged, _ := os.ReadFile(argsLog)
	var leadLine, workerLine string
	for _, l := range strings.Split(string(logged), "\n") {
		if !strings.HasPrefix(l, "agent exec") {
			continue
		}
		if strings.Contains(l, "--tools-file") {
			leadLine = l
		} else {
			workerLine = l
		}
	}
	for _, want := range []string{"--access full", "--tools stdio", "--tools-file", "--tool-timeout 130s"} {
		if !strings.Contains(leadLine, want) {
			t.Errorf("lead argv lacks %q: %s", want, leadLine)
		}
	}
	for _, want := range []string{"--access read", "--cwd " + cwd, "--tools none", "--system-file", "--model opus"} {
		if !strings.Contains(workerLine, want) {
			t.Errorf("worker argv lacks %q: %s", want, workerLine)
		}
	}

	reply, _ := os.ReadFile(filepath.Join(filepath.Dir(argsLog), "reply.json"))
	if !strings.Contains(string(reply), "The cache is in cache.go.") || !strings.Contains(string(reply), `\"status\":\"done\"`) {
		t.Errorf("the lead's org_spawn result = %s", reply)
	}

	j := parseJournaledTurn(t, out)
	var spawned chatevents.AgentSpawnedPayload
	if evs := j.byType(chatevents.EventAgentSpawned); len(evs) != 1 {
		t.Fatalf("agent.spawned events = %d", len(evs))
	} else {
		json.Unmarshal(evs[0].Payload, &spawned)
	}
	if spawned.AgentID != "w1" || spawned.Role != "Researcher" || spawned.Access != "research" || spawned.Runtime != "claude" {
		t.Errorf("spawned = %+v", spawned)
	}
	var finished chatevents.AgentFinishedPayload
	json.Unmarshal(j.byType(chatevents.EventAgentFinished)[0].Payload, &finished)
	if finished.Outcome != chatevents.AgentDone || finished.CostUSD == nil || *finished.CostUSD != 0.002 {
		t.Errorf("finished = %+v", finished)
	}
	workerTools := 0
	for _, e := range j.byType(chatevents.EventToolStarted) {
		var p chatevents.ToolStartedPayload
		json.Unmarshal(e.Payload, &p)
		if p.AgentID == "w1" && p.CallID == "w1:r1" {
			workerTools++
		}
	}
	if workerTools != 1 {
		t.Errorf("worker tool calls journaled = %d, want 1", workerTools)
	}
	if p := j.finished(t); p.Status != chatevents.StatusCompleted {
		t.Errorf("turn.finished = %+v", p)
	}
}

func TestDynamicOrgFallsBackToSoloWithoutCallerTools(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	dbPath := newChatCLITestDB(t)
	bin, argsLog := writeCoderMonomind(t, coderTranscript)
	withCoderCaps(t, monomind.CoderCapabilities...) // no agent-exec-full-access-tools
	withCoderScan(t, orgScan(false))
	setCoderSettings(t, dbPath, coderSettings{Enabled: true})
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversationMode("default", "agent", "general", "claude", "", "", ai.ModeCoder, t.TempDir())
	if err := store.SetConversationOrgMode(conv.ID, "default", ai.OrgModeDynamic); err != nil {
		t.Fatal(err)
	}
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "hi")
	if err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	logged, _ := os.ReadFile(argsLog)
	if strings.Contains(string(logged), "--tools-file") {
		t.Error("a lead without caller-tool support must run without org tools")
	}
	found := false
	for _, e := range parseJournaledTurn(t, out).byType(chatevents.EventNotice) {
		var p chatevents.NoticePayload
		json.Unmarshal(e.Payload, &p)
		if p.Code == noticeOrgUnavailable && strings.Contains(p.Message, "works alone") {
			found = true
		}
	}
	if !found {
		t.Error("the solo fallback must be explained in a notice")
	}
}

func TestSetOrgRefusesAssistantConversations(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	conv, _ := openTestChatStore(t, dbPath).CreateConversation("default", "agent", "general", "claude", "", "")
	if _, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "dynamic"); code != 3 {
		t.Errorf("assistant conversation: exit %d, want 3", code)
	}
	if _, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "crowd"); code != 3 {
		t.Errorf("bad mode: exit %d, want 3", code)
	}
}

func TestCoderSetOrgSettings(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	withCoderCaps(t)
	out, code := runCoderCLI(t, dbPath, "status")
	var st coderStatus
	decodeChatJSON(t, out, &st)
	if code != 0 || st.OrgMaxAgents != 6 || st.OrgMaxConcurrent != 3 || st.OrgModelPicker != "lead-then-jev" {
		t.Fatalf("defaults = %+v", st)
	}
	out, code = runCoderCLI(t, dbPath, "set", "--org-max-agents", "4", "--org-max-concurrent", "2", "--org-budget-usd", "1.5", "--org-model-picker", "lead")
	decodeChatJSON(t, out, &st)
	if code != 0 || st.OrgMaxAgents != 4 || st.OrgMaxConcurrent != 2 || st.OrgBudgetUSD != 1.5 || st.OrgModelPicker != "lead" {
		t.Fatalf("set = exit %d %+v", code, st)
	}
	if _, code := runCoderCLI(t, dbPath, "set", "--org-model-picker", "dice"); code != 3 {
		t.Errorf("bad picker: exit %d", code)
	}
	if _, code := runCoderCLI(t, dbPath, "set", "--org-max-agents", "0"); code != 3 {
		t.Errorf("zero agents: exit %d", code)
	}
}
