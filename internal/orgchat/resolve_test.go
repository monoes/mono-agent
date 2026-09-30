package orgchat

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// fakeClient is an org in memory: resolving an item flips it, so a second
// resolve sees it done, as monomind would.
type fakeClient struct {
	mu        sync.Mutex
	status    string
	questions []Question
	approvals []Approval
	gates     []Gate
	sent      []string
}

func (f *fakeClient) Status(context.Context, string, string) (json.RawMessage, error) {
	return json.Marshal(map[string]string{"status": f.status})
}

func (f *fakeClient) All(_ context.Context, _, _, kind string) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch kind {
	case "questions":
		return json.Marshal(map[string]interface{}{"items": f.questions})
	case "approvals":
		return json.Marshal(map[string]interface{}{"items": f.approvals})
	default:
		return json.Marshal(map[string]interface{}{"items": f.gates})
	}
}

func (f *fakeClient) Answer(_ context.Context, _, _, id, answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.questions {
		if f.questions[i].QuestionID == id {
			f.questions[i].Answer = &answer
		}
	}
	f.sent = append(f.sent, "answer "+id+" "+answer)
	return nil
}

func (f *fakeClient) Approve(_ context.Context, _, _, role, action string, approve bool, requestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.approvals {
		a := &f.approvals[i]
		if a.RoleID == role && a.Action == action && a.Approved == nil {
			v := approve
			a.Approved = &v
		}
	}
	verb := "deny"
	if approve {
		verb = "approve"
	}
	f.sent = append(f.sent, verb+" "+role+" "+action+" "+requestID)
	return nil
}

func (f *fakeClient) Gate(_ context.Context, _, _, id string, approve bool, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.gates {
		if f.gates[i].ID == id {
			f.gates[i].Status = map[bool]string{true: "approved", false: "rejected"}[approve]
		}
	}
	f.sent = append(f.sent, "gate "+id+" "+note)
	return nil
}

func newFake() *fakeClient {
	req := "req-7"
	return &fakeClient{
		status: "running",
		questions: []Question{
			{QuestionID: "q-1", Role: "ceo", Question: "Which version?", TS: 1},
		},
		approvals: []Approval{
			{RoleID: "dev", Action: "Bash", TS: 5},
			{RoleID: "dev", Action: "WebFetch", TS: 6, RequestID: &req},
		},
		gates: []Gate{{ID: "gate-1", Name: "publish", RoleID: "ceo", Status: "pending", CreatedAt: 9}},
	}
}

func TestResolveAnswerIsIdempotent(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	res, err := Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "q-1", Answer: true, Text: "1.2.0"})
	if err != nil || res.Already || res.State != StateAnswered || res.Kind != KindQuestion {
		t.Fatalf("first answer = %+v, %v", res, err)
	}
	res, err = Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "q-1", Answer: true, Text: "1.2.0"})
	if err != nil || !res.Already || res.State != StateAnswered {
		t.Fatalf("second answer = %+v, %v", res, err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent %v, want one answer", f.sent)
	}
}

func TestResolveConcurrentClicksSendOnce(t *testing.T) {
	f := newFake()
	root := t.TempDir()
	var wg sync.WaitGroup
	results := make([]*Result, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := Resolve(context.Background(), f, root, Request{Org: "acme", Ref: "gate-1", Approve: true})
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	if len(f.sent) != 1 {
		t.Fatalf("sent %v, want exactly one gate approval", f.sent)
	}
	fresh := 0
	for _, r := range results {
		if r != nil && !r.Already {
			fresh++
		}
		if r != nil && r.State != StateApproved {
			t.Errorf("state = %q", r.State)
		}
	}
	if fresh != 1 {
		t.Fatalf("%d results claim to have sent", fresh)
	}
}

func TestResolveRefusesAStoppedOrg(t *testing.T) {
	f := newFake()
	f.status = "stopped"
	_, err := Resolve(context.Background(), f, t.TempDir(), Request{Org: "acme", Ref: "dev:Bash", Approve: true})
	var nr *NotRunningError
	if !errors.As(err, &nr) || nr.Status != "stopped" {
		t.Fatalf("err = %v, want NotRunningError(stopped)", err)
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %v to a stopped org", f.sent)
	}
	// An item resolved before the org stopped still answers "already".
	v := false
	f.approvals[0].Approved = &v
	res, err := Resolve(context.Background(), f, t.TempDir(), Request{Org: "acme", Ref: "dev:Bash", Approve: true})
	if err != nil || !res.Already || res.State != StateDenied {
		t.Fatalf("resolved-then-stopped = %+v, %v", res, err)
	}
}

func TestResolveApprovalRefs(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	res, err := Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "req-7", Approve: false})
	if err != nil || res.State != StateDenied || res.Ref != "req-7" {
		t.Fatalf("by request id = %+v, %v", res, err)
	}
	res, err = Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "dev:Bash:5", Approve: true})
	if err != nil || res.State != StateApproved || res.Ref != "dev:Bash:5" {
		t.Fatalf("by role:action:ts = %+v, %v", res, err)
	}
	want := []string{"deny dev WebFetch req-7", "approve dev Bash "}
	if len(f.sent) != 2 || f.sent[0] != want[0] || f.sent[1] != want[1] {
		t.Fatalf("sent %q, want %q", f.sent, want)
	}
}

func TestResolveNotFoundAndEmptyAnswer(t *testing.T) {
	f := newFake()
	ctx := context.Background()
	if _, err := Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "q-missing", Answer: true, Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing question: %v", err)
	}
	if _, err := Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "gate-missing", Approve: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing gate: %v", err)
	}
	if _, err := Resolve(ctx, f, t.TempDir(), Request{Org: "acme", Ref: "q-1", Answer: true, Text: "  "}); err == nil {
		t.Error("an empty answer was accepted")
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent %v", f.sent)
	}
}
