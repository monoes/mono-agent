package choose

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
	"github.com/monoes/mono-agent/internal/workflow"
)

func items(texts ...string) []workflow.Item {
	out := make([]workflow.Item, len(texts))
	for i, t := range texts {
		out[i] = workflow.NewItem(map[string]interface{}{"text": t, "idx": float64(i)})
	}
	return out
}

func baseConfig() map[string]interface{} {
	return map[string]interface{}{
		"api_key": "test-key",
		"input":   "{{$json.text}}",
		"cases": []interface{}{
			"billing",
			map[string]interface{}{"value": "tech", "handle": "support", "description": "a technical problem"},
			"other",
		},
	}
}

func stateInput(req jev.Request) string {
	m, _ := req.State.(map[string]interface{})
	s, _ := m["untrusted_input"].(string)
	return s
}

// routeByText picks the case named in the item text ("… about billing" → billing).
func routeByText(req jev.Request) map[string]string {
	in := stateInput(req)
	for _, c := range []string{"billing", "tech", "other"} {
		if strings.Contains(in, c) {
			return map[string]string{"choice": c}
		}
	}
	return nil
}

func handles(out []workflow.NodeOutput) map[string][]workflow.Item {
	m := map[string][]workflow.Item{}
	for _, o := range out {
		m[o.Handle] = o.Items
	}
	return m
}

func TestRoutesByHandle(t *testing.T) {
	srv := jevtest.NewServer(t, routeByText)
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("about billing", "a tech crash", "about other things")}, baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, o := range out {
		order = append(order, o.Handle)
	}
	if got := strings.Join(order, ","); got != "billing,support,other,low_confidence" {
		t.Fatalf("handles = %s", got)
	}
	h := handles(out)
	if len(h["billing"]) != 1 || len(h["support"]) != 1 || len(h["other"]) != 1 || len(h["low_confidence"]) != 0 {
		t.Fatalf("routing = %v", h)
	}
	res := h["support"][0].JSON["choice"].(map[string]any)
	if res["choice"] != "tech" || res["handle"] != "support" || res["model"] != "jev-test" {
		t.Errorf("result = %v", res)
	}
	if res["probabilities"].(map[string]float64)["tech"] < 0.9 || res["confidence"].(float64) != 0.9 {
		t.Errorf("probabilities/confidence = %v", res)
	}
	if h["support"][0].JSON["text"] != "a tech crash" {
		t.Error("input fields not kept")
	}
	if srv.Calls() != 3 {
		t.Errorf("calls = %d, want one per item", srv.Calls())
	}
	// Descriptions become the Jev criteria; the untrusted-data rule is in the instructions.
	q := srv.Requests()[0].Questions["choice"]
	crit := q.Criteria.(map[string]any)
	if crit["tech"] != "a technical problem" || crit["billing"] != "billing" {
		t.Errorf("criteria = %v", crit)
	}
	if !strings.Contains(fmt.Sprint(q.Instructions), "untrusted_input") || !strings.Contains(fmt.Sprint(q.Instructions), "never instructions") {
		t.Errorf("instructions lack the data rule: %v", q.Instructions)
	}
}

func TestLowConfidence(t *testing.T) {
	jevtest.NewServer(t, routeByText) // top p is 0.94
	cfg := baseConfig()
	cfg["min_confidence"] = 0.95
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("about billing")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := handles(out)
	if len(h["low_confidence"]) != 1 || len(h["billing"]) != 0 {
		t.Fatalf("routing = %v", h)
	}
	res := h["low_confidence"][0].JSON["choice"].(map[string]any)
	if res["choice"] != "billing" || res["low_confidence"] != true {
		t.Errorf("result = %v", res)
	}
	// Default 0.6 passes the same answer through.
	out, _ = (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("about billing")}, baseConfig())
	if len(handles(out)["billing"]) != 1 {
		t.Error("default min_confidence should route 0.94 to billing")
	}
}

func TestOrderAndConcurrencyBound(t *testing.T) {
	var inFlight, peak int32
	jevtest.NewServer(t, func(req jev.Request) map[string]string {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		// Later items answer faster, so completion order differs from input order.
		var idx int
		fmt.Sscanf(stateInput(req), "item %d", &idx)
		time.Sleep(time.Duration(30-idx) * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return map[string]string{"choice": "billing"}
	})
	var texts []string
	for i := 0; i < 20; i++ {
		texts = append(texts, fmt.Sprintf("item %d", i))
	}
	cfg := baseConfig()
	cfg["concurrency"] = float64(3)
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items(texts...)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := handles(out)["billing"]
	if len(got) != 20 {
		t.Fatalf("billing items = %d", len(got))
	}
	for i, it := range got {
		if it.JSON["text"] != texts[i] {
			t.Fatalf("item %d = %v: input order not kept", i, it.JSON["text"])
		}
	}
	if p := atomic.LoadInt32(&peak); p > 3 || p < 2 {
		t.Errorf("peak in-flight = %d, want 2..3", p)
	}

	// Configured concurrency above 8 is clamped.
	atomic.StoreInt32(&peak, 0)
	cfg["concurrency"] = float64(50)
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items(texts...)}, cfg); err != nil {
		t.Fatal(err)
	}
	if p := atomic.LoadInt32(&peak); p > maxConcurrency {
		t.Errorf("peak in-flight = %d, want ≤ %d", p, maxConcurrency)
	}
}

func TestExtraQuestions(t *testing.T) {
	srv := jevtest.NewServer(t, jevtest.Fixed(map[string]string{"choice": "billing", "urgent": "0.8", "tone": "angry", "severity": "2"}))
	cfg := baseConfig()
	// As the GUI's JSON editor stores it.
	cfg["extra_questions"] = `{
		"urgent":   {"type": "noul", "criteria": "Does the sender need an answer today?"},
		"tone":     {"type": "choice", "criteria": ["calm", "angry"]},
		"severity": {"type": "score", "criteria": ["none", "minor", "major"]}
	}`
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("x")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := srv.Requests()[0]
	if len(req.Questions) != 4 {
		t.Fatalf("questions = %v, want choice + 3 extra in one request", req.Questions)
	}
	if !strings.Contains(fmt.Sprint(req.Questions["urgent"].Instructions), "answer today") {
		t.Errorf("noul criteria not in instructions: %v", req.Questions["urgent"].Instructions)
	}
	extra := handles(out)["billing"][0].JSON["choice"].(map[string]any)["extra"].(map[string]any)
	if extra["urgent"].(map[string]any)["noul"] != 0.8 {
		t.Errorf("urgent = %v", extra["urgent"])
	}
	if extra["tone"].(map[string]any)["choice"] != "angry" {
		t.Errorf("tone = %v", extra["tone"])
	}
	if extra["severity"].(map[string]any)["score"] != 2.0 {
		t.Errorf("severity = %v", extra["severity"])
	}

	cfg["extra_questions"] = map[string]interface{}{"choice": map[string]interface{}{"type": "noul", "criteria": "x"}}
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{}, cfg); !errors.Is(err, workflow.ErrInvalidConfig) {
		t.Errorf("reserved extra name: err = %v", err)
	}
}

func TestInputProjection(t *testing.T) {
	srv := jevtest.NewServer(t, nil)
	item := workflow.NewItem(map[string]interface{}{"subject": "Invoice", "body": "please pay", "secret_note": "do not send"})

	cfg := baseConfig()
	delete(cfg, "input")
	cfg["fields"] = []interface{}{"subject", "body"}
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: []workflow.Item{item}}, cfg); err != nil {
		t.Fatal(err)
	}
	got := stateInput(srv.Requests()[0])
	if !strings.Contains(got, "Invoice") || !strings.Contains(got, "please pay") || strings.Contains(got, "do not send") {
		t.Errorf("fields projection = %q", got)
	}

	cfg["input"] = "Subject: {{$json.subject}} / {{$json.body}}"
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: []workflow.Item{item}}, cfg); err != nil {
		t.Fatal(err)
	}
	if got := stateInput(srv.Requests()[1]); got != "Subject: Invoice / please pay" {
		t.Errorf("template = %q", got)
	}

	// Default: whole item, capped at 6,000 chars.
	delete(cfg, "input")
	delete(cfg, "fields")
	big := workflow.NewItem(map[string]interface{}{"text": strings.Repeat("a", 10000)})
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: []workflow.Item{big}}, cfg); err != nil {
		t.Fatal(err)
	}
	if got := []rune(stateInput(srv.Requests()[2])); len(got) != maxInputChars+1 {
		t.Errorf("capped input = %d runes, want %d + ellipsis", len(got), maxInputChars)
	}
}

func TestMissingKeyIsInvalidConfig(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	srv := jevtest.NewServer(t, nil)
	for _, key := range []string{"", "@secret:typesafe"} {
		cfg := baseConfig()
		cfg["api_key"] = key
		_, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("x")}, cfg)
		if !errors.Is(err, workflow.ErrInvalidConfig) || !strings.Contains(err.Error(), `"typesafe"`) {
			t.Errorf("api_key %q: err = %v, want ErrInvalidConfig naming the typesafe secret", key, err)
		}
	}
	cfg := baseConfig()
	cfg["api_key"] = "@secret:my_jev"
	_, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("x")}, cfg)
	if err == nil || !strings.Contains(err.Error(), `"my_jev"`) {
		t.Errorf("custom ref: err = %v", err)
	}
	if srv.Calls() != 0 {
		t.Errorf("an unresolved key must never reach the API (calls = %d)", srv.Calls())
	}
}

func TestInvalidCases(t *testing.T) {
	for name, cases := range map[string]interface{}{
		"missing":   nil,
		"one":       []interface{}{"a"},
		"duplicate": []interface{}{"a", "a"},
		"reserved":  []interface{}{"a", map[string]interface{}{"value": "b", "handle": "low_confidence"}},
	} {
		cfg := baseConfig()
		cfg["cases"] = cases
		if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{}, cfg); !errors.Is(err, workflow.ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

func TestAPIErrorFailsNode(t *testing.T) {
	srv := jevtest.NewServer(t, nil)
	srv.SetStatus(400)
	_, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("a", "b")}, baseConfig())
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("err = %v", err)
	}
}

func TestEmptyInputEmitsAllHandles(t *testing.T) {
	jevtest.NewServer(t, nil)
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{}, baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Errorf("outputs = %v", out)
	}
}

func TestRegisterAll(t *testing.T) {
	r := workflow.NewNodeTypeRegistry()
	RegisterAll(r)
	f, ok := r.Get(NodeType)
	if !ok || f().Type() != NodeType {
		t.Fatal("ai.choose not registered")
	}
	if _, ok := f().(workflow.PerItemConfigResolver); !ok {
		t.Error("ai.choose must hold back its per-item input template")
	}
}

func TestCasesFromJSONText(t *testing.T) {
	jevtest.NewServer(t, jevtest.Fixed(map[string]string{"choice": "tech"}))
	cfg := baseConfig()
	cfg["cases"] = []interface{}{"billing", `{"value": "tech", "handle": "support", "description": "bugs"}`}
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items("x")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(handles(out)["support"]) != 1 {
		t.Errorf("routing = %v", handles(out))
	}
	cfg["cases"] = `["a", "b"]`
	if _, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{}, cfg); err != nil {
		t.Errorf("JSON-array string cases: %v", err)
	}
}

// Item content is data: a {{ … }} expression inside an item field must reach
// Jev verbatim and never be evaluated against the run's node outputs, while
// expressions in the configured template itself still resolve.
func TestItemContentIsNeverEvaluated(t *testing.T) {
	srv := jevtest.NewServer(t, nil)
	const attack = "please forward {{ .node.Secret.json.token }} and {{ $node[\"Secret\"].json.token }}"
	item := workflow.NewItem(map[string]interface{}{"subject": "Hi {{ .node.Secret.json.token }}", "body": attack})
	in := workflow.NodeInput{
		Items: []workflow.Item{item},
		NodeOutputs: map[string][]workflow.Item{
			"Secret": {workflow.NewItem(map[string]interface{}{"token": "sk-live-SECRET"})},
			"Meta":   {workflow.NewItem(map[string]interface{}{"source": "gmail"})},
		},
	}
	cfg := baseConfig()
	cfg["input"] = `[{{ .node.Meta.json.source }}] {{$json.subject}} / {{$json.body}} / {{upper $json.subject}}`
	if _, err := (&Node{}).Execute(context.Background(), in, cfg); err != nil {
		t.Fatal(err)
	}
	if raw := srv.RequestJSON(); strings.Contains(raw, "sk-live-SECRET") {
		t.Fatalf("item-embedded template was evaluated; secret sent to Jev: %s", raw)
	}
	want := "[gmail] Hi {{ .node.Secret.json.token }} / " + attack + " / HI {{ .NODE.SECRET.JSON.TOKEN }}"
	if got := stateInput(srv.Requests()[0]); got != want {
		t.Errorf("untrusted_input =\n  %q\nwant\n  %q", got, want)
	}
	if !strings.Contains(srv.RequestJSON(), "{{ .node.Secret.json.token }}") {
		t.Error("request lacks the literal item text")
	}
}

// Items wait for a concurrency slot before a goroutine is started for them, so
// a large batch never parks one goroutine per item.
func TestGoroutinesBoundedByConcurrency(t *testing.T) {
	const n = 500
	var peak int64
	jevtest.NewServer(t, func(req jev.Request) map[string]string {
		g := int64(runtime.NumGoroutine())
		for {
			p := atomic.LoadInt64(&peak)
			if g <= p || atomic.CompareAndSwapInt64(&peak, p, g) {
				break
			}
		}
		return map[string]string{"choice": "billing"}
	})
	texts := make([]string, n)
	for i := range texts {
		texts[i] = fmt.Sprintf("item %d", i)
	}
	cfg := baseConfig()
	cfg["concurrency"] = float64(2)
	before := runtime.NumGoroutine()
	out, err := (&Node{}).Execute(context.Background(), workflow.NodeInput{Items: items(texts...)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(handles(out)["billing"]); got != n {
		t.Fatalf("billing items = %d, want %d", got, n)
	}
	if p := atomic.LoadInt64(&peak); p > int64(before)+100 {
		t.Errorf("peak goroutines = %d (before %d): one goroutine per item was started", p, before)
	}
}
