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
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
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

// A delegate cannot approve what it cannot see: approve needs view as well.
func TestDelegatedApproveNeedsView(t *testing.T) {
	s, _, _ := newTestStore(t)
	inbox := mustAdd(t, s, "default", "captured", false)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(bg, "default", []int64{inbox.ID}, false, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("approve without view: %v", err)
	}
	if _, err := s.Board(bg, "default", 0, bot("amy")); !errors.Is(err, ErrOperatorOnly) {
		t.Fatalf("approve allowed the board: %v", err)
	}
}

// The loop "an agent adds a task, the same agent approves it" is closed: a delegate approves only
// tasks the operator wrote or a capture surface filed.
func TestDelegatedApproveRefusesTasksAnAgentCreated(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	own, _, err := s.Add(bg, "default", AddInput{Title: "injected"}, bot("bob"))
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := s.Add(bg, "default", AddInput{Title: "by another agent"}, bot("eve"))
	op := mustAdd(t, s, "default", "operator's", false)
	chrome, _, err := s.Add(bg, "default", AddInput{Title: "from chrome", SourceKind: SourceChrome}, Actor{Kind: Capture, Name: "chrome"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{own.ID, other.ID} {
		_, err := s.Approve(bg, "default", []int64{id}, false, bot("bob"))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "created by an agent") {
			t.Fatalf("approving agent task #%d: %v", id, err)
		}
	}
	// A batch with one agent task approves nothing.
	if _, err := s.Approve(bg, "default", []int64{op.ID, own.ID}, false, bot("bob")); err == nil {
		t.Fatal("a batch with an agent's task was approved")
	}
	if got, _, _ := s.Get(bg, "default", op.ID); got.Status != StatusInbox {
		t.Fatalf("the batch moved a task: %s", got.Status)
	}
	if _, err := s.Approve(bg, "default", []int64{op.ID, chrome.ID}, false, bot("bob")); err != nil {
		t.Fatalf("operator and capture tasks: %v", err)
	}
	// The operator may approve an agent's task, as before.
	if _, err := s.Approve(bg, "default", []int64{own.ID}, false, human); err != nil {
		t.Fatalf("operator approving an agent task: %v", err)
	}
}

func TestDelegatedApproveIsCappedAtTenIDs(t *testing.T) {
	s, _, _ := newTestStore(t)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := 0; i < MaxDelegatedApprove+1; i++ {
		ids = append(ids, mustAdd(t, s, "default", "t", false).ID)
	}
	if _, err := s.Approve(bg, "default", ids, false, bot("amy")); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "at most 10") {
		t.Fatalf("11 ids: %v", err)
	}
	if _, err := s.Approve(bg, "default", ids[:MaxDelegatedApprove], false, bot("amy")); err != nil {
		t.Fatalf("10 ids: %v", err)
	}
	// The operator has no cap.
	if _, err := s.Approve(bg, "default", ids[MaxDelegatedApprove:], false, human); err != nil {
		t.Fatal(err)
	}
	more := make([]int64, 0, 12)
	for i := 0; i < 12; i++ {
		more = append(more, mustAdd(t, s, "default", "m", false).ID)
	}
	if _, err := s.Approve(bg, "default", more, false, human); err != nil {
		t.Fatalf("operator with 12 ids: %v", err)
	}
}

// Each half of the "created by an agent" test stands alone: a task whose source says cli but whose
// creation event names an agent, and one whose source says agent but whose creation names the operator.
func TestDelegatedApproveChecksSourceAndCreatorSeparately(t *testing.T) {
	s, db, _ := newTestStore(t)
	if err := s.SetAgentAccess(bg, "default", AgentAccess{View: true, Approve: true}, human); err != nil {
		t.Fatal(err)
	}
	byEvent, _, _ := s.Add(bg, "default", AddInput{Title: "creator is an agent"}, bot("bob"))
	if _, err := db.Exec(`UPDATE tasks SET source_kind = 'cli' WHERE id = ?`, byEvent.ID); err != nil {
		t.Fatal(err)
	}
	bySource := mustAdd(t, s, "default", "source is agent", false)
	if _, err := db.Exec(`UPDATE tasks SET source_kind = 'agent' WHERE id = ?`, bySource.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{byEvent.ID, bySource.ID} {
		if _, err := s.Approve(bg, "default", []int64{id}, false, bot("amy")); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "created by an agent") {
			t.Errorf("task #%d: %v", id, err)
		}
	}
}
