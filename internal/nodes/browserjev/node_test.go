package browserjev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/workflow"
)

// searchPage is a tiny site: a search box (node 10), a Go button (node 20).
func searchPage(query string, results bool) *pageState {
	p := &pageState{URL: "https://example.test/", Title: "Search", Text: "Search", Scroll: json.RawMessage(`{"y":0}`),
		Marker: json.RawMessage(`["m","` + query + `"]`)}
	if results {
		p.URL, p.Text = "https://example.test/results?q="+query, "Results for "+query
	}
	p.Actions = []action{
		{ID: "e1", Kind: "fill", Label: "Search", Role: "textbox", Value: query, Node: 10},
		{ID: "e2", Kind: "click", Label: "Open Search", Role: "textbox", Value: query, Node: 10},
		{ID: "e3", Kind: "click", Label: "Go", Role: "button", Node: 20},
		{ID: "wait", Kind: "wait", Label: "Wait for the page to update"},
	}
	p.Fingerprint = fingerprint(p)
	return p
}

// fakeDriver applies actions to searchPage state and records executions.
type fakeDriver struct {
	query    string
	results  bool
	executed []string
	staleAct int  // fail this many act calls with errStale
	inert    bool // clicks change nothing
}

func (f *fakeDriver) observe(context.Context) (*pageState, error) {
	return searchPage(f.query, f.results), nil
}

func (f *fakeDriver) fresh(p *pageState, _ *action) (bool, error) {
	return p.Fingerprint == searchPage(f.query, f.results).Fingerprint, nil
}

func (f *fakeDriver) act(_ context.Context, p *pageState, a *action, text string) error {
	if f.staleAct > 0 {
		f.staleAct--
		return errStale
	}
	f.executed = append(f.executed, a.ID+":"+text)
	if f.inert {
		return nil
	}
	switch a.ID {
	case "e1":
		f.query = text
	case "e3":
		f.results = f.query != ""
	}
	return nil
}

// policyServer answers like a sensible Jev: type when empty, click Go when
// filled, DONE on results. Every head gets a valid distribution.
func policyServer(t *testing.T, calls *atomic.Int32, override func(q map[string]jev.Question) map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			State struct {
				Page     struct{ URL string }
				Elements []element
			}
			Questions map[string]jev.Question
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		want := map[string]string{}
		switch {
		case strings.Contains(req.State.Page.URL, "results"):
			want["operation"] = "DONE"
		case req.State.Elements[0].Value == "":
			want["operation"], want["type_text_target"] = "TYPE_TEXT", "1"
		default:
			want["operation"], want["click_target"] = "CLICK", "2"
		}
		if override != nil {
			want = override(req.Questions)
		}
		answers := map[string]jev.Answer{}
		for id, q := range req.Questions {
			ids := jev.OptionIDs(q)
			pick := want[id]
			if pick == "" {
				pick = ids[0]
			}
			probs := map[string]float64{}
			for _, o := range ids {
				probs[o] = 0
			}
			probs[pick] = 1
			answers[id] = jev.Answer{Type: jev.TypeChoice, Choice: pick, Probabilities: probs, Confidence: 0.9}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-test", "answers": answers, "usage": map[string]int{"input_tokens": 7}})
	}))
}

func testNode(t *testing.T, srv *httptest.Server, drv driver, writer textWriter) *Node {
	t.Helper()
	t.Cleanup(srv.Close)
	t.Setenv("TYPESAFE_BASE_URL", srv.URL)
	return &Node{
		writer: writer,
		open: func(context.Context, string) (driver, func(), error) {
			return drv, func() {}, nil
		},
	}
}

func runNode(t *testing.T, n *Node) map[string]interface{} {
	t.Helper()
	out, err := n.Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{
		"url": "https://example.test/", "goal": "Search for zurich", "api_key": "k",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out[0].Items[0].JSON
}

func zurich(context.Context, map[string]any) (string, error) { return "zurich", nil }

func TestActionSpaceOneIndexPerNode(t *testing.T) {
	s := actionSpace(searchPage("", false).Actions)
	if len(s.elements) != 2 {
		t.Fatalf("elements = %+v, want 2 (search box + Go)", s.elements)
	}
	if got := s.elements[0].Operations; len(got) != 2 || got[0] != "TYPE_TEXT" || got[1] != "CLICK" {
		t.Errorf("search box operations = %v", got)
	}
	if s.targets["TYPE_TEXT"]["1"].ID != "e1" || s.targets["CLICK"]["1"].ID != "e2" || s.targets["CLICK"]["2"].ID != "e3" {
		t.Errorf("targets = %+v", s.targets)
	}
	if s.controls["WAIT"] == nil {
		t.Error("WAIT control missing")
	}
}

func TestActionSpaceSelectOptions(t *testing.T) {
	s := actionSpace([]action{
		{ID: "e1", Kind: "select", Label: "Class → Business", Value: "b", CurrentValue: "Economy", Node: 5},
		{ID: "e2", Kind: "select", Label: "Class → First", Value: "f", CurrentValue: "Economy", Node: 5},
	})
	if len(s.elements) != 1 || s.elements[0].Value != "Economy" || len(s.elements[0].Options) != 2 {
		t.Fatalf("elements = %+v", s.elements)
	}
	if s.targets["SELECT"]["1:2"].ID != "e2" {
		t.Errorf("select targets = %+v", s.targets["SELECT"])
	}
}

func TestRunReachesDone(t *testing.T) {
	var calls atomic.Int32
	drv := &fakeDriver{}
	var writes int
	res := runNode(t, testNode(t, policyServer(t, &calls, nil), drv, func(ctx context.Context, f map[string]any) (string, error) {
		writes++
		if f["goal"] != "Search for zurich" {
			t.Errorf("text helper context = %v", f)
		}
		return "zurich", nil
	}))
	if res["status"] != "done" {
		t.Fatalf("status = %v (%v)", res["status"], res["reason"])
	}
	if got := strings.Join(drv.executed, ","); got != "e1:zurich,e3:" {
		t.Errorf("executed = %s", got)
	}
	if writes != 1 || calls.Load() != 3 || res["decisions"] != 3 || res["jev_input_tokens"] != 21 {
		t.Errorf("writes=%d calls=%d decisions=%v tokens=%v", writes, calls.Load(), res["decisions"], res["jev_input_tokens"])
	}
	steps := res["steps"].([]step)
	if len(steps) != 2 || steps[0].Operation != "TYPE_TEXT" || !*steps[0].PageChanged || steps[1].Target != "2" {
		t.Errorf("steps = %+v", steps)
	}
}

func TestStaleActionIsReDecidedNotReplayed(t *testing.T) {
	var calls atomic.Int32
	drv := &fakeDriver{staleAct: 1}
	res := runNode(t, testNode(t, policyServer(t, &calls, nil), drv, zurich))
	if res["status"] != "done" || strings.Join(drv.executed, ",") != "e1:zurich,e3:" {
		t.Fatalf("status=%v executed=%v", res["status"], drv.executed)
	}
	if calls.Load() != 4 {
		t.Errorf("jev calls = %d, want 4 (one decision discarded as stale)", calls.Load())
	}
}

func TestRepeatedNoOpBlocks(t *testing.T) {
	var calls atomic.Int32
	drv := &fakeDriver{inert: true}
	clickGo := func(map[string]jev.Question) map[string]string {
		return map[string]string{"operation": "CLICK", "click_target": "2"}
	}
	res := runNode(t, testNode(t, policyServer(t, &calls, clickGo), drv, zurich))
	if res["status"] != "blocked" || len(drv.executed) != 3 {
		t.Fatalf("status=%v executed=%v", res["status"], drv.executed)
	}
}

func TestMissingValueBlocksWithoutTyping(t *testing.T) {
	var calls atomic.Int32
	drv := &fakeDriver{}
	res := runNode(t, testNode(t, policyServer(t, &calls, nil), drv, func(context.Context, map[string]any) (string, error) {
		return "", errNoValue
	}))
	if res["status"] != "blocked" || len(drv.executed) != 0 {
		t.Fatalf("status=%v executed=%v", res["status"], drv.executed)
	}
}

func TestInvalidJevAnswerExecutesNothing(t *testing.T) {
	var calls atomic.Int32
	drv := &fakeDriver{}
	invent := func(map[string]jev.Question) map[string]string {
		return map[string]string{"operation": "CLICK", "click_target": "99"}
	}
	srv := policyServer(t, &calls, invent)
	_, err := testNode(t, srv, drv, zurich).Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"url": "u", "goal": "g", "api_key": "k"})
	if !errors.Is(err, jev.ErrInvalidAnswer) || len(drv.executed) != 0 {
		t.Fatalf("err=%v executed=%v", err, drv.executed)
	}
}

func TestFailOnBlocked(t *testing.T) {
	var calls atomic.Int32
	blocked := func(map[string]jev.Question) map[string]string { return map[string]string{"operation": "BLOCKED"} }
	n := testNode(t, policyServer(t, &calls, blocked), &fakeDriver{}, zurich)
	_, err := n.Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"url": "u", "goal": "g", "api_key": "k", "fail_on_blocked": true})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v", err)
	}
}

func TestConfigValidation(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	n := &Node{}
	for name, cfg := range map[string]map[string]interface{}{
		"no url":  {"goal": "g", "api_key": "k"},
		"no goal": {"url": "u", "api_key": "k"},
		"no key":  {"url": "u", "goal": "g"},
		"budget":  {"url": "u", "goal": "g", "api_key": "k", "max_actions": float64(0)},
		"secret":  {"url": "u", "goal": "g", "api_key": "@secret:typesafe"},
	} {
		if _, err := n.Execute(context.Background(), workflow.NodeInput{}, cfg); !errors.Is(err, workflow.ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestParseTextValue(t *testing.T) {
	if v, err := parseTextValue("```json\n{\"text\": \"Zürich\"}\n```"); err != nil || v != "Zürich" {
		t.Errorf("fenced: %q %v", v, err)
	}
	if _, err := parseTextValue(`{"text": null}`); !errors.Is(err, errNoValue) {
		t.Errorf("null: %v", err)
	}
	for _, bad := range []string{`no json`, `{"text":"a","extra":1}`, `{"value":"a"}`} {
		if _, err := parseTextValue(bad); err == nil || errors.Is(err, errNoValue) {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestRegisterAll(t *testing.T) {
	r := workflow.NewNodeTypeRegistry()
	RegisterAll(r)
	if f, ok := r.Get("browser.jev"); !ok || f().Type() != "browser.jev" {
		t.Fatal("browser.jev not registered")
	}
}

func TestUnresolvedSecretFallsBackToEnv(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer env-key" {
			t.Errorf("Authorization = %q, want the env key", got)
		}
		calls.Add(1)
		w.WriteHeader(401)
	}))
	n := testNode(t, srv, &fakeDriver{}, zurich)
	t.Setenv("TYPESAFE_API_KEY", "env-key")
	_, _ = n.Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"url": "u", "goal": "g", "api_key": "@secret:typesafe"})
	if calls.Load() != 1 {
		t.Fatalf("calls = %d", calls.Load())
	}
}
