package tasks

import (
	"errors"
	"strings"
	"testing"
)

// The operator can delegate two abilities to agents, per profile, off by default (spec D33).

func TestAgentAccessIsOffByDefault(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, err := s.AgentAccess(bg, "default")
	if err != nil || a.View || a.Approve {
		t.Fatalf("default access: %+v, %v; want everything off", a, err)
	}
	inbox := mustAdd(t, s, "default", "captured text", false)
	// Exactly the refusals of before.
	if _, err := s.Approve(bg, "default", []int64{inbox.ID}, false, bot("amy")); !errors.Is(err, ErrOperatorOnly) || !strings.Contains(err.Error(), "approve a task") {
		t.Fatalf("agent approve while off: %v", err)
	}
	if _, err := s.Board(bg, "default", 0, bot("amy")); !errors.Is(err, ErrOperatorOnly) || !strings.Contains(err.Error(), "show the board") {
		t.Fatalf("agent board while off: %v", err)
	}
	ts, err := s.List(bg, "default", Filter{}, bot("amy"))
	if err != nil || len(ts) != 0 {
		t.Fatalf("agent list while off shows %d tasks, %v; the Inbox must stay out of the default list", len(ts), err)
	}
}

func TestSetAgentAccessIsOperatorOnly(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, who := range []Actor{bot("amy"), {Kind: Agent}, {Kind: Capture, Name: "os"}, {}} {
		if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, who); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v changed the access: %v", who, err)
		}
	}
	if a, _ := s.AgentAccess(bg, "default"); a.View || a.Approve {
		t.Fatalf("access changed by a refused call: %+v", a)
	}
}

func TestViewSwitchShowsInboxAndBoardToAgents(t *testing.T) {
	s, _, _ := newTestStore(t)
	inbox := mustAdd(t, s, "default", "captured text", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true}, human); err != nil {
		t.Fatal(err)
	}
	ts, err := s.List(bg, "default", Filter{}, bot("amy"))
	if err != nil || len(ts) != 1 || ts[0].ID != inbox.ID {
		t.Fatalf("agent list with view on: %v, %v", ts, err)
	}
	if b, err := s.Board(bg, "default", 0, bot("amy")); err != nil || b.Counts.Inbox != 1 {
		t.Fatalf("agent board with view on: %+v, %v", b.Counts, err)
	}
	// view alone does not approve.
	if _, err := s.Approve(bg, "default", []int64{inbox.ID}, false, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("view allowed an approval: %v", err)
	}
	// A capture surface never reads, delegation or not.
	if _, err := s.Board(bg, "default", 0, Actor{Kind: Capture, Name: "os"}); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("capture board: %v", err)
	}
	if _, err := s.List(bg, "default", Filter{}, Actor{Kind: Capture, Name: "os"}); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("capture list: %v", err)
	}
}

func TestApproveSwitchLetsANamedAgentApproveAndMarksTheEvent(t *testing.T) {
	s, _, _ := newTestStore(t)
	inbox := mustAdd(t, s, "default", "captured text", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	// approve alone does not open the board or the default list.
	if _, err := s.Board(bg, "default", 0, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("approve allowed the board: %v", err)
	}
	// A nameless agent is refused: the history must say who approved.
	if _, err := s.Approve(bg, "default", []int64{inbox.ID}, false, Actor{Kind: Agent}); err == nil {
		t.Fatal("a nameless agent approved")
	}
	got, err := s.Approve(bg, "default", []int64{inbox.ID}, false, bot("amy"))
	if err != nil || len(got) != 1 || got[0].Status != StatusReady {
		t.Fatalf("approve with the switch on: %v, %v", got, err)
	}
	_, events, _ := s.Get(bg, "default", inbox.ID)
	last := events[len(events)-1]
	if last.Actor != "amy" || last.Kind != "moved" || last.ToStatus != "ready" || !strings.Contains(last.Note, DelegatedApprovalNote) {
		t.Fatalf("approval event: %+v", last)
	}
	// Only Inbox -> Ready, as for the operator.
	if _, err := s.Approve(bg, "default", []int64{inbox.ID}, false, bot("amy")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("approving a Ready task: %v", err)
	}
	// An operator approval carries the old note, unmarked.
	other := mustAdd(t, s, "default", "second", false)
	if _, err := s.Approve(bg, "default", []int64{other.ID}, false, human); err != nil {
		t.Fatal(err)
	}
	_, events, _ = s.Get(bg, "default", other.ID)
	if last := events[len(events)-1]; last.Actor != "you" || last.Note != "approved" {
		t.Fatalf("operator approval event: %+v", last)
	}
}

func TestDenyTakesEffectAtOnce(t *testing.T) {
	s, _, _ := newTestStore(t)
	a := mustAdd(t, s, "default", "a", false)
	b := mustAdd(t, s, "default", "b", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(bg, "default", []int64{a.ID}, false, bot("amy")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAgentAccess(bg, "default", AgentAccess{}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(bg, "default", []int64{b.ID}, false, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("approve after deny: %v", err)
	}
	if _, err := s.Board(bg, "default", 0, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("board after deny: %v", err)
	}
	if ts, _ := s.List(bg, "default", Filter{}, bot("amy")); len(ts) != 1 || ts[0].Status != StatusReady {
		t.Fatalf("list after deny: %v", ts)
	}
}

func TestAgentAccessIsPerProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p-other")
	inboxOther := mustAdd(t, s, other, "other's", false)
	mustAdd(t, s, "default", "mine", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.AgentAccess(bg, other); a.View || a.Approve {
		t.Fatalf("the other profile inherited %+v", a)
	}
	if _, err := s.Approve(bg, other, []int64{inboxOther.ID}, false, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("approve on the other profile: %v", err)
	}
	if _, err := s.Board(bg, other, 0, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("board of the other profile: %v", err)
	}
	if ts, _ := s.List(bg, other, Filter{}, bot("amy")); len(ts) != 0 {
		t.Fatalf("the other profile's inbox is listed: %v", ts)
	}
}

func TestDelegationDoesNotExtendToOtherOperatorVerbs(t *testing.T) {
	s, _, _ := newTestStore(t)
	inbox := mustAdd(t, s, "default", "x", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	am := bot("amy")
	if _, _, err := s.Add(bg, "default", AddInput{Title: "t", Ready: true}, am); err == nil {
		t.Error("an agent added straight to Ready")
	}
	if _, err := s.Archive(bg, "default", []int64{inbox.ID}, am); !errors.Is(err, ErrOperatorOnly) {
		t.Errorf("archive: %v", err)
	}
	if _, err := s.Move(bg, "default", inbox.ID, StatusReady, Placement{}, am); !errors.Is(err, ErrOperatorOnly) {
		t.Errorf("move: %v", err)
	}
	if _, err := s.Edit(bg, "default", inbox.ID, Edit{}, am); !errors.Is(err, ErrOperatorOnly) {
		t.Errorf("edit: %v", err)
	}
}
