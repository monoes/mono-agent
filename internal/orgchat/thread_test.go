package orgchat

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func loadFixture(t *testing.T) ([]Event, HumanItems) {
	t.Helper()
	f, err := os.Open("testdata/boss-thread.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	raw, err := os.ReadFile("testdata/human-items.json")
	if err != nil {
		t.Fatal(err)
	}
	var lists map[string]json.RawMessage
	if err := json.Unmarshal(raw, &lists); err != nil {
		t.Fatal(err)
	}
	var h HumanItems
	if h.Questions, err = ParseQuestions(lists["questions"]); err != nil {
		t.Fatal(err)
	}
	if h.Approvals, err = ParseApprovals(lists["approvals"]); err != nil {
		t.Fatal(err)
	}
	if h.Gates, err = ParseGates(lists["gates"]); err != nil {
		t.Fatal(err)
	}
	return events, h
}

func kinds(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

func TestBuildThreadFromFixture(t *testing.T) {
	events, human := loadFixture(t)
	items := BuildThread("acme", "ceo", events, human, 0)

	want := []string{
		ItemStatus,   // org started
		ItemHuman,    // the person's message (its second bus copy dropped)
		ItemBoss,     // "On it…"
		ItemTeam,     // ceo → dev
		ItemApproval, // dev's Bash, approved
		ItemQuestion, // q-1700, answered
		ItemTeam,     // dev → ceo
		ItemGate,     // pending
		ItemBoss,     // "Waiting on…"
		ItemTeam,     // ceo → herald:editor, external
		ItemQuestion, // q-2250 from qa, pending
		ItemQuestion, // q-0900 from an earlier run, still pending
	}
	if got := kinds(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v\nwant %v", got, want)
	}

	human0 := items[1]
	if human0.Text != "Please ship the release notes today." || human0.To != "ceo" {
		t.Errorf("human item = %+v (trace line must be stripped)", human0)
	}
	if items[2].Role != "ceo" || items[2].Text != "On it. I'm asking dev to draft them." {
		t.Errorf("boss item = %+v", items[2])
	}
	if it := items[3]; it.From != "ceo" || it.To != "dev" || it.External {
		t.Errorf("team row = %+v", it)
	}
	if it := items[4]; it.Pending || it.Resolution != StateApproved || it.Ref != "dev:Bash:1600" || it.ResolvedBy != "human" {
		t.Errorf("approval = %+v", it)
	}
	if it := items[5]; it.Pending || it.Resolution != StateAnswered || it.Answer != "1.2.0" || it.Ref != "q-1700-ab12" {
		t.Errorf("answered question = %+v", it)
	}
	if it := items[7]; !it.Pending || it.Ref != "gate-1850-x1" || it.Name != "publish-notes" || it.Text == "" {
		t.Errorf("gate = %+v", it)
	}
	if it := items[9]; !it.External || it.To != "herald:editor" {
		t.Errorf("cross-org row = %+v", it)
	}
	if it := items[10]; !it.Pending || it.Role != "qa" {
		t.Errorf("qa question = %+v", it)
	}
	if it := items[11]; !it.Pending || it.Ref != "q-0900-zz99" || it.TS != 900 {
		t.Errorf("earlier-run question = %+v", it)
	}
	// Only the boss's words are boss items: dev's chat line is not.
	for _, it := range items {
		if it.Kind == ItemBoss && it.Role != "ceo" {
			t.Errorf("boss item from %q", it.Role)
		}
	}
}

func TestBuildThreadIsPure(t *testing.T) {
	events, human := loadFixture(t)
	a := BuildThread("acme", "ceo", events, human, 0)
	b := BuildThread("acme", "ceo", events, human, 0)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same input built two different threads")
	}
	// Re-delivered events (a tail that restarted) change nothing.
	c := BuildThread("acme", "ceo", append(append([]Event{}, events...), events...), human, 0)
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("replaying the log twice changed the thread:\n%v\n%v", kinds(a), kinds(c))
	}
}

func TestBuildThreadWithoutListsUsesTheBus(t *testing.T) {
	events, _ := loadFixture(t)
	items := BuildThread("acme", "ceo", events, HumanItems{}, 0)
	var q, gate *Item
	for i := range items {
		switch items[i].Ref {
		case "q-1700-ab12":
			q = &items[i]
		case "gate-1850-x1":
			gate = &items[i]
		}
	}
	if q == nil || q.Pending || q.Resolution != StateAnswered || q.ResolvedBy != "human" {
		t.Errorf("question from the bus alone = %+v", q)
	}
	if gate == nil || !gate.Pending {
		t.Errorf("gate from the bus alone = %+v", gate)
	}
}

func TestBuildThreadLimitKeepsPending(t *testing.T) {
	events, human := loadFixture(t)
	items := BuildThread("acme", "ceo", events, human, 2)
	var pending int
	for _, it := range items {
		if it.Pending {
			pending++
		}
	}
	if pending != 3 {
		t.Fatalf("pending kept = %d, want 3 (gate, q-2250, q-0900): %v", pending, kinds(items))
	}
	if items[len(items)-1].Ref != "q-0900-zz99" || len(items) != 3 {
		t.Fatalf("limit 2 kept %v", kinds(items))
	}
}

func TestParseLogsShapes(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"items":[{"id":"a","type":"chat"}]}`,
		`{"events":[{"id":"a","type":"chat"}]}`,
		`[{"id":"a","type":"chat"}]`,
	} {
		got, err := ParseLogs([]byte(raw))
		if err != nil || len(got) != 1 || got[0].ID != "a" {
			t.Errorf("ParseLogs(%s) = %v, %v", raw, got, err)
		}
	}
}

func TestBuildThreadEmptyIsAnEmptyList(t *testing.T) {
	items := BuildThread("acme", "ceo", nil, HumanItems{}, 0)
	if items == nil || len(items) != 0 {
		t.Fatalf("items = %#v, want an empty, non-nil list", items)
	}
}
