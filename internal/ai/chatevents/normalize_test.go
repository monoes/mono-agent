package chatevents

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestComputeTurnStatus_StopRequestedWinsEvenWithAnError(t *testing.T) {
	// Rule 1 outranks rule 2: a Stop accepted before finalization is
	// cancelled, not failed, even if the process also happened to record
	// an error (e.g. the runtime's own cancellation error).
	res := &monomind.TurnResult{
		SawDone: true,
		Err:     &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled by caller"},
	}
	status, reason := ComputeTurnStatus(true, res)
	if status != StatusCancelled {
		t.Errorf("status = %q, want %q", status, StatusCancelled)
	}
	if reason == "" {
		t.Error("reason is empty")
	}
}

func TestComputeTurnStatus_ErrorWinsOverMissingDone(t *testing.T) {
	// Rule 2 outranks rule 3: an explicit failure is reported as failed
	// even if done was also never seen (e.g. the process crashed right
	// after emitting a fatal error, before it could send done).
	res := &monomind.TurnResult{
		SawDone: false,
		Err:     &monomind.ProtocolError{Code: monomind.ErrAuth, Message: "not logged in"},
	}
	status, _ := ComputeTurnStatus(false, res)
	if status != StatusFailed {
		t.Errorf("status = %q, want %q", status, StatusFailed)
	}
}

func TestComputeTurnStatus_MissingDoneIsInterrupted(t *testing.T) {
	res := &monomind.TurnResult{SawDone: false, Err: nil}
	status, reason := ComputeTurnStatus(false, res)
	if status != StatusInterrupted {
		t.Errorf("status = %q, want %q", status, StatusInterrupted)
	}
	if reason == "" {
		t.Error("reason is empty")
	}
}

func TestComputeTurnStatus_CleanCompletionPreservesEndTurn(t *testing.T) {
	res := &monomind.TurnResult{SawDone: true, Err: nil, StopReason: monomind.StopEndTurn}
	status, reason := ComputeTurnStatus(false, res)
	if status != StatusCompleted {
		t.Errorf("status = %q, want %q", status, StatusCompleted)
	}
	if reason != monomind.StopEndTurn {
		t.Errorf("reason = %q, want %q", reason, monomind.StopEndTurn)
	}
}

// TestComputeTurnStatus_LimitReachedIsNotPlainSuccess is the plan's explicit
// callout: max_turns/tool_round_cap must never display as a normal
// task-success claim, even though the turn technically completed cleanly.
func TestComputeTurnStatus_LimitReachedIsNotPlainSuccess(t *testing.T) {
	for _, sr := range []string{monomind.StopMaxTurns, monomind.StopToolRoundCap} {
		res := &monomind.TurnResult{SawDone: true, Err: nil, StopReason: sr}
		status, reason := ComputeTurnStatus(false, res)
		if status != StatusCompleted {
			t.Errorf("%s: status = %q, want %q (still completed, just capped)", sr, status, StatusCompleted)
		}
		if !strings.Contains(reason, "limit reached") {
			t.Errorf("%s: reason = %q, want it to mention a limit was reached, not read as plain success", sr, reason)
		}
	}
}

func TestComputeTurnStatus_NilResultIsInterrupted(t *testing.T) {
	status, _ := ComputeTurnStatus(false, nil)
	if status != StatusInterrupted {
		t.Errorf("status = %q, want %q", status, StatusInterrupted)
	}
}

func TestTextCoalescer_AdjacentPushesUnderSamePartIDConcatenate(t *testing.T) {
	c := NewTextCoalescer()
	now := time.Now()
	if _, _, flushed := c.Push("p1", "Hello, ", now); flushed {
		t.Fatal("first push flushed unexpectedly")
	}
	if _, _, flushed := c.Push("p1", "world!", now.Add(time.Millisecond)); flushed {
		t.Fatal("second push (same part, well within window) flushed unexpectedly")
	}
	partID, text, ok := c.ForceFlush()
	if !ok {
		t.Fatal("ForceFlush found nothing buffered")
	}
	if partID != "p1" || text != "Hello, world!" {
		t.Errorf("flushed (%q, %q), want (p1, %q)", partID, text, "Hello, world!")
	}
}

func TestTextCoalescer_DifferentPartIDForcesFlushOfThePrevious(t *testing.T) {
	c := NewTextCoalescer()
	now := time.Now()
	c.Push("p1", "first part", now)
	partID, text, flushed := c.Push("p2", "second part", now)
	if !flushed {
		t.Fatal("switching partID did not flush the previous part")
	}
	if partID != "p1" || text != "first part" {
		t.Errorf("flushed (%q, %q), want (p1, %q)", partID, text, "first part")
	}
	// p2's text is now the only thing buffered.
	partID2, text2, ok := c.ForceFlush()
	if !ok || partID2 != "p2" || text2 != "second part" {
		t.Errorf("remaining buffer = (%q, %q, %v), want (p2, second part, true)", partID2, text2, ok)
	}
}

func TestTextCoalescer_TimeWindowTriggersFlush(t *testing.T) {
	c := NewTextCoalescer()
	start := time.Now()
	c.Push("p1", "slow trickle", start)
	if c.ShouldFlush(start.Add(10 * time.Millisecond)) {
		t.Error("ShouldFlush = true well within the 50ms window")
	}
	if !c.ShouldFlush(start.Add(51 * time.Millisecond)) {
		t.Error("ShouldFlush = false after the 50ms window elapsed")
	}
	partID, text, ok := c.Flush(start.Add(51 * time.Millisecond))
	if !ok || partID != "p1" || text != "slow trickle" {
		t.Errorf("Flush = (%q, %q, %v)", partID, text, ok)
	}
}

func TestTextCoalescer_SizeBoundTriggersFlushWithinPush(t *testing.T) {
	c := NewTextCoalescer()
	now := time.Now()
	big := strings.Repeat("x", CoalesceMaxBytes)
	partID, text, flushed := c.Push("p1", big, now)
	if !flushed {
		t.Fatal("pushing exactly CoalesceMaxBytes did not self-flush")
	}
	if partID != "p1" || len(text) != CoalesceMaxBytes {
		t.Errorf("flushed part = (%q, %d bytes), want (p1, %d bytes)", partID, len(text), CoalesceMaxBytes)
	}
	// Buffer must be empty after a self-triggered flush.
	if _, _, ok := c.ForceFlush(); ok {
		t.Error("buffer still had content after Push self-flushed it")
	}
}

func TestTextCoalescer_EmptyForceFlushReportsNotOK(t *testing.T) {
	c := NewTextCoalescer()
	if _, _, ok := c.ForceFlush(); ok {
		t.Error("ForceFlush on an empty coalescer reported ok=true")
	}
}

func TestBoundText_ShortTextUnmodified(t *testing.T) {
	bounded, truncated, orig := BoundText("short", 100)
	if truncated || bounded != "short" || orig != 5 {
		t.Errorf("BoundText(short) = (%q, %v, %d)", bounded, truncated, orig)
	}
}

func TestBoundText_CutsOnRuneBoundaryNotMidCharacter(t *testing.T) {
	// "café" — é is a 2-byte UTF-8 sequence; a naive byte-cut at len-1 would
	// split it and produce invalid UTF-8.
	s := "café"
	bounded, truncated, orig := BoundText(s, len(s)-1)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if orig != len(s) {
		t.Errorf("originalBytes = %d, want %d", orig, len(s))
	}
	if !isValidUTF8(bounded) {
		t.Errorf("bounded text %q is not valid UTF-8", bounded)
	}
	if bounded != "caf" {
		t.Errorf("bounded = %q, want %q (the split é dropped entirely)", bounded, "caf")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestRedactAndBoundJSON_MasksSensitiveKeys(t *testing.T) {
	raw := json.RawMessage(`{"username":"alice","api_key":"sk-secret123"}`)
	out := RedactAndBoundJSON(raw)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal redacted output: %v", err)
	}
	if got["api_key"] != "***" {
		t.Errorf(`api_key = %v, want "***"`, got["api_key"])
	}
	if got["username"] != "alice" {
		t.Errorf(`username = %v, want "alice" (unrelated key untouched)`, got["username"])
	}
}

func TestRedactAndBoundJSON_NonObjectInputBoundedNotRedacted(t *testing.T) {
	raw := json.RawMessage(`[1,2,3]`)
	out := RedactAndBoundJSON(raw)
	var got string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("expected bounded text output, got: %s (unmarshal err: %v)", out, err)
	}
	if got != "[1,2,3]" {
		t.Errorf("got = %q, want the array text unchanged (short enough not to truncate)", got)
	}
}

func TestRedactAndBoundJSON_EmptyInputUnchanged(t *testing.T) {
	if out := RedactAndBoundJSON(nil); out != nil {
		t.Errorf("RedactAndBoundJSON(nil) = %v, want nil", out)
	}
}
