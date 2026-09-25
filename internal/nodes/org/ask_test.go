package org

import (
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/workflow"
)

// The ask row keeps the question it sent (without the reply instructions),
// so a reply that lost its ask token can be linked back (plan WS9).
func TestNewAskRecordKeepsTheQuestion(t *testing.T) {
	in := workflow.NodeInput{ExecutionID: "exec-1", NodeID: "n1"}
	a := newAskRecord("ask_x", "p", "growth", "lead", "bot", in, "Which region should we launch in?", time.Minute)
	if a.Question != "Which region should we launch in?" || a.EndpointRoleID != "bot" || a.ExecutionID != "exec-1" || a.NodeID != "n1" || a.RoleID != "lead" {
		t.Fatalf("ask = %+v", a)
	}
	if d := time.Until(a.DeadlineAt); d < 50*time.Second || d > time.Minute {
		t.Fatalf("deadline in %s", d)
	}
}
