package orgdesign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageDrivenGoal(t *testing.T) {
	cases := []struct {
		name   string
		goal   string
		parent string
		want   bool
	}{
		{"plan phrasing", "Handle requests from hq; when idle, complete.", "hq", true},
		{"shipped sales template", "Handle requests from hq about pricing and plans; when the request is answered, report to hq and complete.", "hq", true},
		{"shipped support template", "Handle support requests from hq; when the issue is resolved or escalated, report to hq and complete.", "hq", true},
		{"messages plus idle", "Answer messages that arrive; when idle, stop.", "hq", true},
		{"parent in role address", "Respond to hq:ceo's asks about invoices.", "hq", true},
		{"hyphenated parent", "Handle requests from my-hq.", "my-hq", true},
		{"case insensitive", "HANDLE REQUESTS FROM HQ", "hq", true},
		{"reply cue with completes", "Reply to each inquiry, then completes the run.", "hq", true},
		{"standing goal", "Ship the Q4 roadmap.", "hq", false},
		{"empty goal", "", "hq", false},
		{"whitespace goal", "   ", "hq", false},
		{"no request cue", "Keep hq's pricing page up to date; complete when done.", "hq", false},
		{"request cue but no parent or idle or complete", "Handle customer requests about pricing.", "hq", false},
		{"parent name only as substring", "Handle requests from hqx.", "hq", false},
		{"completion only as substring", "Handle incomplete requests.", "hq", false},
		{"requestor is not a cue", "Be the requestor for hq and finish the complete roadmap.", "hq", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MessageDrivenGoal(tc.goal, tc.parent); got != tc.want {
				t.Fatalf("MessageDrivenGoal(%q, %q) = %v, want %v", tc.goal, tc.parent, got, tc.want)
			}
		})
	}
}

func TestChildGoalWarnings(t *testing.T) {
	hq := &Doc{Name: "hq", Kind: OrgKindHolding, ChildOrgs: []ChildOrg{{Org: "sales"}, {Org: "roadmap"}, {Org: "ghost"}}}
	sales := &Doc{Name: "sales", Goal: "Handle requests from hq; when idle, complete."}
	roadmap := &Doc{Name: "roadmap", Goal: "Ship the Q4 roadmap."}
	other := &Doc{Name: "other", Goal: "Ship the Q4 roadmap."}
	docs := []*Doc{roadmap, other, hq, sales}

	cases := []struct {
		name  string
		only  string
		wantN int
	}{
		{"all orgs", "", 1},
		{"filtered to the holding org", "hq", 1},
		{"filtered to the offending child", "roadmap", 1},
		{"filtered to a message-driven child", "sales", 0},
		{"filtered to an org outside any group", "other", 0},
		{"missing child is not warned about", "ghost", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ChildGoalWarnings(docs, tc.only)
			if len(got) != tc.wantN {
				t.Fatalf("ChildGoalWarnings(only=%q) = %v, want %d warning(s)", tc.only, got, tc.wantN)
			}
			for _, w := range got {
				if !strings.Contains(w, `"roadmap"`) || !strings.Contains(w, `"hq"`) || !strings.Contains(w, "org_start") {
					t.Fatalf("warning should name child, holding org, and org_start: %q", w)
				}
			}
		})
	}
}

// TestShippedHoldingExampleHasNoChildGoalWarnings keeps examples/orgs/holding
// free of C-36 warnings: the shipped children must be message-driven.
func TestShippedHoldingExampleHasNoChildGoalWarnings(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "orgs", "holding")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var docs []*Doc
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var d Doc
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		docs = append(docs, &d)
	}
	if len(docs) < 2 {
		t.Fatalf("expected the holding example's org files, found %d", len(docs))
	}
	if w := ChildGoalWarnings(docs, ""); len(w) > 0 {
		t.Fatalf("shipped holding example has child goal warnings: %v", w)
	}
}
