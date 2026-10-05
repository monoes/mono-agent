package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestRunTurnPassesTheFieldsOfALeg(t *testing.T) {
	var got monomind.ExecOptions
	h := newHarness(t, func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		got = opts
		return okTurn("ok")(ctx, opts, onEvent)
	})
	handler := func(context.Context, string, json.RawMessage) (string, error) { return "", errors.New("later") }
	tools := []monomind.ToolSpec{{Name: "f", Description: "d"}}

	if _, err := h.g.runTurn(context.Background(), turn{
		Runtime: "codex", Model: "default", Prompt: "p", Policy: anyPolicy, RequireSandbox: true,
		Tools: tools, OnToolCall: handler, Resume: "sess-1", Access: monomind.AccessRead, MaxTurns: 7,
	}); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "f" || got.OnToolCall == nil {
		t.Errorf("tools: %+v handler %v", got.Tools, got.OnToolCall != nil)
	}
	if got.Resume != "sess-1" || got.Access != monomind.AccessRead || got.MaxTurns != 7 {
		t.Errorf("resume %q access %q max turns %d", got.Resume, got.Access, got.MaxTurns)
	}
	// What makes a turn the gateway's own does not change with tools.
	if got.Sandbox != monomind.TurnSandboxMode || !got.RequireSandbox || got.WorkspacePurpose != "api" || len(got.Settings) != 0 || len(got.AllowBashPrefixes) != 0 {
		t.Errorf("a leg keeps the posture of a turn: %+v", got)
	}
}

func TestRunTurnOnEventSeesTheEventsInOrder(t *testing.T) {
	h := newHarness(t, okTurn("ok"))
	var types []string
	res, err := h.g.runTurn(context.Background(), turn{
		Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy,
		OnEvent: func(ev monomind.Event) { types = append(types, ev.Type) },
	})
	if err != nil || res == nil || res.ResultText != "ok" {
		t.Fatalf("runTurn = %+v, %v", res, err)
	}
	want := []string{monomind.EventStart, monomind.EventAssistant, monomind.EventUsage, monomind.EventResult, monomind.EventDone}
	if len(types) != len(want) {
		t.Fatalf("events seen: %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events seen: %v, want %v", types, want)
		}
	}
}

// A turn the policy stops is not the caller's: whatever its runtime says after
// the start event never reaches the hook.
func TestRunTurnOnEventSeesNothingAfterAPolicyDenial(t *testing.T) {
	var types []string
	h := newHarness(t, scriptedExec(
		evStart(false, "none"), // codex that reports no sandbox: above a policy of sandboxed
		monomind.Event{V: 1, Type: monomind.EventSession, SessionID: "s"},
		monomind.Event{V: 1, Type: monomind.EventToolCall, ID: "tc_1", Name: "f", Args: json.RawMessage(`{}`)},
		evDone(0)))
	_, err := h.g.runTurn(context.Background(), turn{
		Runtime: "codex", Model: "default", Prompt: "p", Policy: Policy{Max: Sandboxed},
		OnEvent: func(ev monomind.Event) { types = append(types, ev.Type) },
	})
	if !errors.Is(err, errPolicyDenied) {
		t.Fatalf("err = %v, want errPolicyDenied", err)
	}
	if len(types) != 1 || types[0] != monomind.EventStart {
		t.Errorf("the hook saw %v after the policy denied the turn", types)
	}
}

// monomind 2.22 runs a codex turn with access read and the workspace-write
// sandbox read-only (its start event says so), which is stricter than the
// sandbox the class was decided from: the check of the start event must keep
// passing it, and must still stop a turn that reports no sandbox.
func TestRunTurnAcceptsTheStartEventOfACodexLegWithReadAccess(t *testing.T) {
	const start = `{"v":1,"type":"start","runtime":"codex","cwd":"/x/slot-0","pid":4242,"access":"read","native_sandbox":"read-only","approvals":"off","sandbox_requested":"workspace-write","sandbox_applied":"workspace-write","streams_incrementally":false}`
	var ev monomind.Event
	if err := json.Unmarshal([]byte(start), &ev); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, scriptedExec(ev, evText("fine"), evResult("fine", monomind.StopEndTurn), evDone(0)))
	res, err := h.g.runTurn(context.Background(), turn{
		Runtime: "codex", Model: "default", Prompt: "p", Policy: Policy{Max: Sandboxed}, RequireSandbox: true, Access: monomind.AccessRead,
	})
	if err != nil || res == nil || res.ResultText != "fine" {
		t.Fatalf("a read-only codex leg was refused: %+v, %v", res, err)
	}
}
