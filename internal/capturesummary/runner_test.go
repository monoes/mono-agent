package capturesummary

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func TestAnswerOf(t *testing.T) {
	ok, err := answerOf(&monomind.TurnResult{ResultText: " ## TL;DR\nx ", StopReason: "end_turn", SawDone: true, CostUSD: 0.2, HasCostUSD: true})
	if err != nil || ok.Text != "## TL;DR\nx" || ok.CostUSD != 0.2 {
		t.Fatalf("answer = %+v, %v", ok, err)
	}
	if _, err := answerOf(&monomind.TurnResult{ResultText: "half", StopReason: "max_turns", SawDone: true}); err == nil || !strings.Contains(err.Error(), "max_turns") {
		t.Errorf("a turn stopped on a limit is not a summary: %v", err)
	}
	if _, err := answerOf(&monomind.TurnResult{Err: &monomind.ProtocolError{Code: "runtime_missing", Message: "claude not found"}}); err == nil || !strings.Contains(err.Error(), "claude not found") {
		t.Errorf("protocol error lost: %v", err)
	}
	if _, err := answerOf(&monomind.TurnResult{ResultText: "x", ExitCode: 2}); err == nil {
		t.Error("a runtime that died without finishing is not a summary")
	}
}
