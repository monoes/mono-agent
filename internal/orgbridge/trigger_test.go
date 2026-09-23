package orgbridge

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func loadRecorded(t *testing.T) []Event {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "orgs", "runs", "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no recorded runs: %v", err)
	}
	var evs []Event
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			ev, err := ParseEvent(sc.Bytes())
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			evs = append(evs, ev)
		}
		fh.Close()
	}
	return evs
}

// C-45: a question subscription (default ask_human) never fires on the
// Bash approvals the recorded forge run contains; question_kind approval
// catches exactly those.
func TestTriggerFilterQuestionKinds(t *testing.T) {
	evs := loadRecorded(t)
	askHuman := FilterFromConfig(map[string]interface{}{"org_name": "forge", "event_types": []interface{}{"question"}})
	approvals := FilterFromConfig(map[string]interface{}{"org_name": "forge", "event_types": "question", "question_kind": "approval"})
	var nAsk, nApproval, nQuestions int
	for _, ev := range evs {
		if ev.Type == "question" {
			nQuestions++
		}
		if askHuman.Match(ev) {
			nAsk++
		}
		if approvals.Match(ev) {
			nApproval++
		}
	}
	if nQuestions == 0 {
		t.Fatal("recorded runs contain no question events; fixture drifted")
	}
	if nAsk != 0 {
		t.Fatalf("ask_human subscription fired %d times on approval events", nAsk)
	}
	if nApproval != nQuestions {
		t.Fatalf("approval subscription matched %d of %d approvals", nApproval, nQuestions)
	}
}

func TestTriggerFilterRoleSubjectAndGate(t *testing.T) {
	evs := loadRecorded(t)
	gates := FilterFromConfig(map[string]interface{}{"org_name": "herald", "event_types": []interface{}{"gate"}, "subject_match": "PUBLISH"})
	toCTO := FilterFromConfig(map[string]interface{}{"org_name": "forge", "event_types": []interface{}{"xorg"}, "role": "cto"})
	var nGate, nXorgCTO int
	for _, ev := range evs {
		if gates.Match(ev) {
			nGate++
			items := TriggerItems(ev)
			data := items[0].JSON["event"].(map[string]interface{})["data"].(map[string]interface{})
			if data["gateId"] == "" {
				t.Fatalf("gate trigger data lost gateId: %v", items[0].JSON)
			}
		}
		if toCTO.Match(ev) {
			nXorgCTO++
			if !strings.Contains(ev.To+ev.From, "cto") {
				t.Fatalf("role filter matched %s -> %s", ev.From, ev.To)
			}
		}
	}
	if nGate != 1 {
		t.Fatalf("gate matches = %d, want 1", nGate)
	}
	if nXorgCTO == 0 {
		t.Fatal("no xorg events to or from cto matched")
	}
}

func TestTriggerItemsCapsAssetContentAndStripsTrace(t *testing.T) {
	big := strings.Repeat("x", maxEventContentBytes+100)
	ev := Event{ID: "e", Org: "forge", Type: "asset", Path: "/w/a.go", Data: map[string]interface{}{"content": big}}
	items := TriggerItems(ev)
	if items[0].JSON["trigger_type"] != workflow.TriggerTypeOrgEvent {
		t.Fatalf("trigger_type = %v", items[0].JSON["trigger_type"])
	}
	// RedactAndTruncateItems bounds each item further; either way the full
	// content never reaches trigger data.
	if strings.Contains(items[0].JSON["event"].(map[string]interface{})["data"].(map[string]interface{})["content"].(string), big) {
		t.Fatal("asset content not capped")
	}

	msg := Event{ID: "m", Org: "g", Type: "message", Msg: "[trace chn_a hop=2]\nhello"}
	it := TriggerItems(msg)[0].JSON
	if it["trace"].(map[string]interface{})["hop"] != 2 || it["event"].(map[string]interface{})["msg"] != "hello" {
		t.Fatalf("trace not lifted out of the message: %v", it)
	}
}

// A granted call's tool event carries its chain in data, not in a trace
// line. trigger.org must continue that chain, or a loop through a workflow
// started by the call begins a fresh chain every round. Other tool events
// carry the role's run-long chain too, and must not: an audit workflow on
// every Bash call would climb it and get the role refused as a loop.
func TestTriggerItemsTakeATrace(t *testing.T) {
	trace := func(ev Event) interface{} { return TriggerItems(ev)[0].JSON["trace"] }
	data := func(chain string, hop interface{}) map[string]interface{} {
		return map[string]interface{}{"chain_id": chain, "hop": hop}
	}

	for _, name := range []string{"monoagent__automation_publish", "mcp__monoagent__automation_publish", "monoagent__org_start"} {
		tool := Event{ID: "t", Org: "g", Type: "tool", Tool: name, Data: data("chn_tool1", float64(3))}
		if got, ok := trace(tool).(map[string]interface{}); !ok || got["chain_id"] != "chn_tool1" || got["hop"] != 3 {
			t.Fatalf("%s: trace = %v", name, trace(tool))
		}
	}
	for _, name := range []string{"Bash", "Read", "other__automation_x", "monoagentx__y", ""} {
		if got := trace(Event{ID: "t", Org: "g", Type: "tool", Tool: name, Data: data("chn_tool1", float64(3))}); got != nil {
			t.Errorf("%s: trace = %v, want none (not a granted call)", name, got)
		}
	}
	if got := trace(Event{ID: "s", Org: "g", Type: "status", Tool: "monoagent__x", Data: data("chn_tool1", float64(3))}); got != nil {
		t.Errorf("non-tool event: trace = %v, want none", got)
	}
	// A trace line in the message wins over data.
	both := Event{ID: "b", Org: "g", Type: "message", Msg: "[trace chn_msg hop=5]\nhi", Data: data("chn_tool1", float64(1))}
	if got := trace(both).(map[string]interface{}); got["chain_id"] != "chn_msg" || got["hop"] != 5 {
		t.Fatalf("message trace = %v", got)
	}
	for name, d := range map[string]map[string]interface{}{
		"no chain":     {"hop": float64(1)},
		"bad chain":    data("../../etc", float64(1)),
		"no hop":       {"chain_id": "chn_x"},
		"negative hop": data("chn_x", float64(-1)),
		"string hop":   data("chn_x", "2"),
	} {
		if got := trace(Event{ID: "x", Org: "g", Type: "tool", Tool: "monoagent__a", Data: d}); got != nil {
			t.Errorf("%s: trace = %v, want none", name, got)
		}
	}
}
