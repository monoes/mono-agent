package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// writeSandboxMonomind writes a fake monomind that advertises (or not)
// agent-exec-sandbox on top of caps, logs every agent exec argv, and
// streams transcript for it.
func writeSandboxMonomind(t *testing.T, advertise bool, transcript string, caps ...string) (bin, argsLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake monomind is a shell script")
	}
	all := append([]string{"agent-exec", "agent-scan", "org-json-v1"}, caps...)
	if advertise {
		all = append(all, monomind.CapAgentExecSandbox)
	}
	capsJSON, _ := json.Marshal(all)
	dir := t.TempDir()
	bin = filepath.Join(dir, "monomind")
	argsLog = filepath.Join(dir, "exec-args.log")
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":` + string(capsJSON) + `}'; exit 0; fi` + "\n" +
		`if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then` + "\n" +
		`  echo "$*" >> '` + argsLog + "'\n" + transcript + "\n  exit 0\nfi\n" +
		`echo "unsupported: $*" >&2` + "\nexit 2\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	return bin, argsLog
}

func sandboxTranscript(startExtra string) string {
	return `  echo '{"v":1,"type":"start","runtime":"codex","pid":1` + startExtra + `}'
  echo '{"v":1,"type":"assistant","text":"hi"}'
  echo '{"v":1,"type":"result","subtype":"success","stop_reason":"end_turn","text":"hi"}'
  echo '{"v":1,"type":"done","exit_code":0}'`
}

func lastExecLine(t *testing.T, argsLog string) string {
	t.Helper()
	logged, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("no agent exec ran: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	return lines[len(lines)-1]
}

// sandboxNotices returns the agent.sandbox notices' messages.
func sandboxNotices(j journaledTurn) []string {
	var out []string
	for _, n := range j.byType(chatevents.EventNotice) {
		var p chatevents.NoticePayload
		json.Unmarshal(n.Payload, &p)
		if p.Code == noticeAgentSandbox {
			out = append(out, p.Message)
		}
	}
	return out
}

func TestChatTurnSandbox(t *testing.T) {
	cases := []struct {
		name       string
		advertise  bool
		startExtra string
		wantArgs   []string
		neverArgs  []string
		wantStatus string
	}{
		{name: "sandboxed", advertise: true, startExtra: `,"sandbox":"workspace"`,
			wantArgs:   []string{"--sandbox workspace", "--cwd "}, // cwd checked below
			wantStatus: monomind.SandboxStatusSandboxed},
		{name: "runtime unsupported", advertise: true, startExtra: `,"sandbox":"off","sandbox_unsupported":true`,
			wantArgs:   []string{"--sandbox workspace"},
			wantStatus: monomind.SandboxStatusUnsupported},
		{name: "older monomind", advertise: false,
			neverArgs:  []string{"--sandbox", "--cwd"},
			wantStatus: monomind.SandboxStatusNeedsMonomind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := newChatCLITestDB(t)
			conv, err := openTestChatStore(t, dbPath).CreateConversation("default", "agent", "general", "codex", "", "")
			if err != nil {
				t.Fatal(err)
			}
			bin, argsLog := writeSandboxMonomind(t, tc.advertise, sandboxTranscript(tc.startExtra))
			out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "hi")
			if err != nil {
				t.Fatalf("turn: %v\n%s", err, out)
			}
			line := lastExecLine(t, argsLog)
			for _, want := range tc.wantArgs {
				if !strings.Contains(line, want) {
					t.Errorf("exec argv lacks %q: %s", want, line)
				}
			}
			for _, never := range tc.neverArgs {
				if strings.Contains(line, never) {
					t.Errorf("exec argv has %q: %s", never, line)
				}
			}
			if tc.advertise {
				want := "--cwd " + filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", monomind.WorkspaceChat)
				if !strings.Contains(line, want) {
					t.Errorf("a plain chat turn should run in the chat workspace (%s): %s", want, line)
				}
			}
			j := parseJournaledTurn(t, out)
			if got := sandboxNotices(j); len(got) != 1 || got[0] != tc.wantStatus {
				t.Errorf("sandbox notices = %v, want [%s]", got, tc.wantStatus)
			}
			if p := j.finished(t); p.Sandbox != tc.wantStatus {
				t.Errorf("turn.finished sandbox = %q, want %q", p.Sandbox, tc.wantStatus)
			}
		})
	}
}

// A claude chat turn is sandboxed too, but keeps its (empty) folder:
// claude's sessions are keyed by folder.
func TestChatTurnSandboxClaudeKeepsFolder(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	conv, _ := openTestChatStore(t, dbPath).CreateConversation("default", "agent", "general", "claude", "", "")
	bin, argsLog := writeSandboxMonomind(t, true, sandboxTranscript(`,"sandbox":"workspace"`))
	if out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "hi"); err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	line := lastExecLine(t, argsLog)
	if !strings.Contains(line, "--sandbox workspace") || strings.Contains(line, "--cwd") {
		t.Errorf("claude turn argv: %s", line)
	}
}

// Without --conversation, `chat` prints the protocol events; the start
// event carries the verdict for --json readers.
func TestChatPlainTurnPrintsSandboxOnStart(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	bin, _ := writeSandboxMonomind(t, true, sandboxTranscript(`,"sandbox":"workspace"`))
	out, err := runChatCmd(t, dbPath, bin, "--runtime", "codex", "--no-history", "--", "hi")
	if err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	var start map[string]any
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `"type":"start"`) {
			json.Unmarshal([]byte(l), &start)
		}
	}
	if start["sandbox"] != "workspace" || start["sandbox_status"] != monomind.SandboxStatusSandboxed {
		t.Errorf("start event = %v", start)
	}
}

// Coder mode is never sandboxed, even when monomind could.
func TestCoderTurnNeverSandboxed(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	bin, argsLog := writeSandboxMonomind(t, true, coderTranscript, monomind.CoderCapabilities...)
	withCoderCaps(t, append([]string{monomind.CapAgentExecSandbox}, monomind.CoderCapabilities...)...)
	setCoderSettings(t, dbPath, coderSettings{Enabled: true, BudgetUSD: 3})
	cwd := t.TempDir()
	conv, err := openTestChatStore(t, dbPath).CreateConversationMode("default", "agent", "general", "claude", "", "", ai.ModeCoder, cwd)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--instance", "i", "--", "fix it")
	if err != nil {
		t.Fatalf("coder turn: %v\n%s", err, out)
	}
	line := lastExecLine(t, argsLog)
	if strings.Contains(line, monomind.SandboxFlag) || !strings.Contains(line, "--access full") {
		t.Errorf("coder exec argv: %s", line)
	}
	j := parseJournaledTurn(t, out)
	if got := sandboxNotices(j); len(got) != 0 {
		t.Errorf("coder turn journaled sandbox notices %v", got)
	}
	if p := j.finished(t); p.Sandbox != "" {
		t.Errorf("coder turn.finished sandbox = %q", p.Sandbox)
	}
}

func TestAgentTestIsSandboxed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, argsLog := writeSandboxMonomind(t, true, sandboxTranscript(`,"sandbox":"workspace"`))
	cmd := newAgentTestCmd(&globalConfig{})
	cmd.SetArgs([]string{"codex"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	line := lastExecLine(t, argsLog)
	want := "--cwd " + filepath.Join(os.Getenv("HOME"), ".monoagent", "workspaces", monomind.WorkspaceAgentTest)
	if !strings.Contains(line, "--sandbox workspace") || !strings.Contains(line, want) {
		t.Errorf("agent test argv: %s", line)
	}
}
