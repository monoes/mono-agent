package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/storage"
)

// journaledTurn is a parsed `chat --conversation … --turn …` stdout.
type journaledTurn struct {
	admission chatAdmission
	events    []chatevents.Record
}

func (j journaledTurn) byType(typ chatevents.EventType) []chatevents.Record {
	var out []chatevents.Record
	for _, ev := range j.events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func (j journaledTurn) finished(t *testing.T) chatevents.TurnFinishedPayload {
	t.Helper()
	fin := j.byType(chatevents.EventTurnFinished)
	if len(fin) != 1 {
		t.Fatalf("want exactly one turn.finished, got %d: %+v", len(fin), j.events)
	}
	var p chatevents.TurnFinishedPayload
	if err := json.Unmarshal(fin[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func parseJournaledTurn(t *testing.T, out string) journaledTurn {
	t.Helper()
	var j journaledTurn
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || json.Unmarshal([]byte(lines[0]), &j.admission) != nil || j.admission.Turn.ID == "" {
		t.Fatalf("first line is not an admission: %q", out)
	}
	for _, l := range lines[1:] {
		var rec chatevents.Record
		if err := json.Unmarshal([]byte(l), &rec); err != nil || rec.Type == "" {
			t.Fatalf("stdout line is not an event: %q", l)
		}
		j.events = append(j.events, rec)
	}
	return j
}

// writeArgsLoggingMonomind is writeFakeMonomindForChat that also records
// each `agent exec` argv in the returned file.
func writeArgsLoggingMonomind(t *testing.T, transcript string) (bin, argsLog string) {
	t.Helper()
	argsLog = filepath.Join(t.TempDir(), "exec-args.log")
	return writeFakeMonomindForChat(t, `  echo "$*" >> '`+argsLog+`'`+"\n"+transcript), argsLog
}

func TestJournaledTurnWritesItsOwnHistory(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "fake-model")
	bin, argsLog := writeArgsLoggingMonomind(t, `  echo '{"v":1,"type":"start","runtime":"fake","cwd":"/app","pid":1}'
  echo '{"v":1,"type":"session","session_id":"th_journal"}'
  echo '{"v":1,"type":"assistant","text":"hello "}'
  echo '{"v":1,"type":"assistant","text":"world"}'
  echo '{"v":1,"type":"error","code":"adapter-warning","fatal":false,"message":"`+strings.Repeat("x", chatevents.MaxToolPreviewBytes+100)+`"}'
  echo '{"v":1,"type":"usage","input_tokens":3,"output_tokens":7,"cost_usd":0.001}'
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"hello world"}'
  echo '{"v":1,"type":"done","exit_code":0}'`)

	// "history" as the prompt: after -- it is never the subcommand.
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--instance", "inst-1", "--", "history")
	if err != nil {
		t.Fatalf("chat: %v\n%s", err, out)
	}
	j := parseJournaledTurn(t, out)
	if !j.admission.Admitted || j.admission.Existed || j.admission.Turn.Status != "active" || j.admission.Turn.OwnerInstanceID != "inst-1" {
		t.Errorf("admission = %+v", j.admission)
	}
	if j.events[0].Type != chatevents.EventTurnStarted {
		t.Fatalf("first event = %s, want turn.started", j.events[0].Type)
	}
	var started chatevents.TurnStartedPayload
	json.Unmarshal(j.events[0].Payload, &started)
	if started.Text != "history" || started.Runtime != "fake" || started.Model != "fake-model" {
		t.Errorf("turn.started = %s", j.events[0].Payload)
	}
	if p := j.finished(t); p.Status != chatevents.StatusCompleted || !p.HistorySaved {
		t.Errorf("turn.finished = %+v", p)
	}
	var text string
	for _, d := range j.byType(chatevents.EventAssistantDelta) {
		var p chatevents.AssistantDeltaPayload
		json.Unmarshal(d.Payload, &p)
		text += p.Text
	}
	if text != "hello world" {
		t.Errorf("assistant text = %q", text)
	}
	// The first notice is the sandbox badge: this fake monomind predates
	// agent-exec-sandbox (see chat_sandbox_test.go).
	notices := j.byType(chatevents.EventNotice)
	if len(notices) != 2 {
		t.Fatalf("notices = %+v", notices)
	}
	var notice chatevents.NoticePayload
	json.Unmarshal(notices[0].Payload, &notice)
	if notice.Code != noticeAgentSandbox || notice.Message != monomind.SandboxStatusNeedsMonomind {
		t.Errorf("first notice = %+v, want the sandbox badge", notice)
	}
	json.Unmarshal(notices[1].Payload, &notice)
	if len(notice.Message) > chatevents.MaxToolPreviewBytes || notice.Severity != chatevents.SeverityWarning {
		t.Errorf("notice is %d bytes / %s, want bounded warning", len(notice.Message), notice.Severity)
	}

	// Every printed event is exactly what was committed, in seq order.
	stored, _ := store.GetEvents(conv.ID, "turn-1", "default", 0, 100)
	if len(stored) != len(j.events) {
		t.Fatalf("printed %d events, stored %d", len(j.events), len(stored))
	}
	for i, ev := range stored {
		if j.events[i].Seq != ev.Seq || j.events[i].Type != ev.Type || string(j.events[i].Payload) != string(ev.Payload) {
			t.Errorf("event %d printed %+v, stored %+v", i, j.events[i], ev)
		}
	}
	turn, _ := store.GetTurn("turn-1", "default")
	if turn.Status != "completed" || turn.LastCommittedSeq != stored[len(stored)-1].Seq {
		t.Errorf("turn = %+v", turn)
	}
	got, _ := store.GetConversation(conv.ID, "default")
	if got.SessionID != "th_journal" {
		t.Errorf("session not bound: %+v", got)
	}
	if n := countChatMessages(t, dbPath, conv.HistoryKey); n != 0 {
		t.Errorf("a journaled turn wrote %d legacy transcript rows", n)
	}

	// The next turn resumes the bound session with the conversation's model.
	if out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-2", "--", "again"); err != nil {
		t.Fatalf("second turn: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(argsLog)
	execs := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(execs) != 2 || strings.Contains(execs[0], "--resume") || !strings.Contains(execs[1], "--resume th_journal") ||
		!strings.Contains(execs[1], "--runtime fake") || !strings.Contains(execs[1], "--model fake-model") {
		t.Errorf("exec argv = %q", execs)
	}

	// A retried start of a finished turn reports it and never runs again.
	out, err = runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "history")
	if err != nil {
		t.Fatal(err)
	}
	retry := parseJournaledTurn(t, out)
	if retry.admission.Admitted || !retry.admission.Existed || retry.admission.Turn.Status != "completed" || len(retry.events) != 0 {
		t.Errorf("retry = %q", out)
	}
	raw, _ = os.ReadFile(argsLog)
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 2 {
		t.Errorf("the retried turn ran the runtime again (%d execs)", n)
	}
}

func TestJournaledTurnFatalErrorIsFailed(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	bin := writeFakeMonomindForChat(t, `  echo '{"v":1,"type":"start","runtime":"fake","cwd":"/app","pid":1}'
  echo '{"v":1,"type":"error","code":"auth","fatal":true,"message":"not logged in"}'
  echo '{"v":1,"type":"done","exit_code":1}'`)
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "t-fatal", "--", "hi")
	if err == nil {
		t.Fatal("want the turn's protocol error")
	}
	if p := parseJournaledTurn(t, out).finished(t); p.Status != chatevents.StatusFailed {
		t.Errorf("status = %s", p.Status)
	}
	if turn, _ := store.GetTurn("t-fatal", "default"); turn.Status != "failed" {
		t.Errorf("stored status = %s", turn.Status)
	}
}

func TestJournaledTurnWithoutDoneIsInterrupted(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	bin := writeFakeMonomindForChat(t, `  echo '{"v":1,"type":"start","runtime":"fake","cwd":"/app","pid":1}'
  echo '{"v":1,"type":"assistant","text":"partial answer, then the pipe just closed"}'`)
	out, _ := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "t-nodone", "--", "hi")
	j := parseJournaledTurn(t, out)
	if p := j.finished(t); p.Status != chatevents.StatusInterrupted {
		t.Errorf("status = %s, want interrupted (a clean exit without done)", p.Status)
	}
	if turn, _ := store.GetTurn("t-nodone", "default"); turn.Status == "active" {
		t.Error("turn left active")
	}
}

func TestJournaledTurnRefusals(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	bin := writeFakeMonomindForChat(t, successTranscript)

	provider, _ := store.CreateConversation("default", "provider", "general", "", "old-provider", "gpt-x")
	_, err := runChatCmd(t, dbPath, bin, "--conversation", provider.ID, "--turn", "t1", "--", "hi")
	if exitCodeFor(err) != 3 || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("provider conversation: %v (exit %d), want read-only / 3", err, exitCodeFor(err))
	}
	if turns, _, _ := store.ListTurns(provider.ID, "default", "", 10); len(turns) != 0 {
		t.Errorf("a refused turn was created: %+v", turns)
	}

	_, err = runChatCmd(t, dbPath, bin, "--conversation", "nope", "--turn", "t1", "--", "hi")
	if exitCodeFor(err) != 2 {
		t.Errorf("unknown conversation exit %d, want 2", exitCodeFor(err))
	}

	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	store.CreateTurn(conv.ID, "default", "theirs", "instance-a", "hi")
	_, err = runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "mine", "--instance", "instance-b", "--", "hi")
	if exitCodeFor(err) != 3 || !errorsIsOwned(err) {
		t.Errorf("another instance's active turn: %v (exit %d), want 3", err, exitCodeFor(err))
	}
	if _, err := store.GetTurn("mine", "default"); err == nil {
		t.Error("the refused turn was created")
	}
	_, err = runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "theirs", "--instance", "instance-b", "--", "hi")
	if exitCodeFor(err) != 3 {
		t.Errorf("re-running a turn still active elsewhere exit %d, want 3", exitCodeFor(err))
	}

	for _, extra := range [][]string{{"--runtime", "x"}, {"--model", "m"}, {"--resume", "s"}, {"--history-id", "h"}} {
		args := append([]string{"--conversation", conv.ID, "--turn", "t2"}, extra...)
		if _, err := runChatCmd(t, dbPath, bin, append(args, "--", "hi")...); exitCodeFor(err) != 3 {
			t.Errorf("%v with --conversation exit %d, want 3", extra, exitCodeFor(err))
		}
	}
	if _, err := runChatCmd(t, dbPath, bin, "--turn", "t2", "--", "hi"); exitCodeFor(err) != 3 {
		t.Errorf("--turn without --conversation exit %d, want 3", exitCodeFor(err))
	}
}

func errorsIsOwned(err error) bool {
	return err != nil && strings.Contains(err.Error(), ai.ErrTurnOwnedByOtherInstance.Error())
}

// A plain `chat <prompt>` whose prompt is quoted "history …" still runs the
// turn; only the bare first word routes to the subcommand.
func TestChatPromptStartingWithHistoryStillRuns(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	bin := writeFakeMonomindForChat(t, successTranscript)
	out, err := runChatCmd(t, dbPath, bin, "--runtime", "fake", "--no-history", "history of rome")
	if err != nil || !strings.Contains(out, "hello from the fake runtime") {
		t.Errorf("quoted prompt: %v %q", err, out)
	}
}

// newJournalOverDeadDB returns a journal whose turn exists but whose store
// can no longer write — a stand-in for any persistence failure mid-turn.
func newJournalOverDeadDB(t *testing.T) (*turnJournal, *bytes.Buffer) {
	t.Helper()
	dbPath := newChatCLITestDB(t)
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := ai.NewAIStore(db.DB)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	if _, _, err := store.CreateTurn(conv.ID, "default", "t1", "", "hi"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var out bytes.Buffer
	return newTurnJournal(store, "default", conv.ID, "t1", "fake", &out), &out
}

// When the terminal write fails, turn.finished is still printed live-only:
// historySaved false and seq MaxSafeSeq, so the UI's "seq <= lastSeq"
// dedup never discards it and the panel doesn't hang.
func TestJournalFinalizeFailurePrintsLiveOnlyFinished(t *testing.T) {
	j, out := newJournalOverDeadDB(t)
	if err := j.start("", "hi"); err == nil {
		t.Fatal("start over a dead DB: want error")
	}
	if out.Len() != 0 {
		t.Fatalf("an uncommitted event was printed: %s", out)
	}
	j.fail(errors.New("boom"))
	j.fail(errors.New("again")) // exactly once
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("printed %d lines, want one turn.finished: %s", len(lines), out)
	}
	var rec chatevents.Record
	json.Unmarshal([]byte(lines[0]), &rec)
	var p chatevents.TurnFinishedPayload
	json.Unmarshal(rec.Payload, &p)
	if rec.Type != chatevents.EventTurnFinished || rec.Seq != chatevents.MaxSafeSeq || p.HistorySaved || p.Status != chatevents.StatusFailed {
		t.Errorf("live-only turn.finished = %+v %+v", rec, p)
	}
}

// A tool call closes the current text part; later text opens a new one,
// and tool arguments and results are journaled bounded.
func TestJournalToolEventsSplitTextParts(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, _ := store.CreateConversation("default", "agent", "general", "fake", "", "")
	store.CreateTurn(conv.ID, "default", "t1", "", "hi")
	var out bytes.Buffer
	j := newTurnJournal(store, "default", conv.ID, "t1", "fake", &out)
	j.handle(monomindEvent(t, `{"v":1,"type":"assistant","text":"Creating nodes…"}`))
	j.handle(monomindEvent(t, `{"v":1,"type":"tool_call","id":"tc_1","name":"create_nodes","args":{"count":2}}`))
	j.handle(monomindEvent(t, `{"v":1,"type":"tool_result","id":"tc_1","ok":true,"result":{"text":"`+strings.Repeat("r", chatevents.MaxToolPreviewBytes+50)+`"}}`))
	j.handle(monomindEvent(t, `{"v":1,"type":"assistant","text":"Done."}`))
	j.finish(false, nil)

	evs, _ := store.GetEvents(conv.ID, "t1", "default", 0, 100)
	var got []string
	for _, ev := range evs {
		switch ev.Type {
		case chatevents.EventAssistantDelta:
			var p chatevents.AssistantDeltaPayload
			json.Unmarshal(ev.Payload, &p)
			got = append(got, "text:"+p.PartID+":"+p.Text)
		case chatevents.EventToolStarted:
			var p chatevents.ToolStartedPayload
			json.Unmarshal(ev.Payload, &p)
			got = append(got, "tool:"+p.CallID+":"+p.Name)
		case chatevents.EventToolCompleted:
			var p chatevents.ToolCompletedPayload
			json.Unmarshal(ev.Payload, &p)
			if len(p.Result) > chatevents.MaxToolPreviewBytes {
				t.Errorf("tool result is %d bytes, want bounded", len(p.Result))
			}
			got = append(got, "done:"+p.CallID)
		default:
			got = append(got, string(ev.Type))
		}
	}
	want := "text:part-1:Creating nodes…|tool:tc_1:create_nodes|done:tc_1|text:part-2:Done.|turn.finished"
	if strings.Join(got, "|") != want {
		t.Errorf("journal = %s\nwant      %s", strings.Join(got, "|"), want)
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "\n") + 1; n != len(evs) {
		t.Errorf("printed %d lines for %d committed events", n, len(evs))
	}
}

func monomindEvent(t *testing.T, line string) monomind.Event {
	t.Helper()
	var ev monomind.Event
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	return ev
}
