package recordanalyze

import (
	"fmt"
	"testing"

	"github.com/monoes/mono-agent/internal/recording"
)

func TestDetectFormInputsAndOutcome(t *testing.T) {
	a := analyzeFixture(t, "form-submit")
	if a.Login != nil {
		t.Fatalf("a create form with three fields is not a login: %+v", a.Login)
	}
	byName := map[string]InputSpec{}
	for _, in := range a.Inputs {
		byName[in.Name] = in
	}
	if in := byName["email"]; in.Format != "email" || !in.Required || in.Default != "jane@example.com" {
		t.Errorf("email input = %+v", in)
	}
	if in := byName["full_name"]; !in.Marked || !in.Required {
		t.Errorf("marked input = %+v", in)
	}
	if in := byName["account_password"]; in.Type != "secret" || in.Default != "" {
		t.Errorf("secret input = %+v", in)
	}
	save := a.Steps[4]
	if !save.SideEffect || save.Until == nil || save.Until.URLMatches != `/contacts/\d+(?:[?#]|$)` {
		t.Errorf("save = %+v until %+v", save, save.Until)
	}
	if a.Steps[1].Input != "email" {
		t.Errorf("binding = %q", a.Steps[1].Input)
	}
}

func TestDetectListExtract(t *testing.T) {
	a := analyzeFixture(t, "list-scrape")
	if len(a.Extracts) != 1 {
		t.Fatalf("extracts = %+v", a.Extracts)
	}
	g := a.Extracts[0]
	if !g.List || g.Container != "ol.items" || g.Item != "li.item" || len(g.Fields) != 2 {
		t.Fatalf("group = %+v", g)
	}
	if g.Fields[0].Name != "title" || g.Fields[1].Selector != "span.points" || len(g.Fields[0].Samples) != 5 {
		t.Errorf("fields = %+v", g.Fields)
	}
	if len(a.Inputs) != 0 {
		t.Errorf("no inputs expected: %+v", a.Inputs)
	}
}

func TestDetectLoginMultiPage(t *testing.T) {
	a := analyzeFixture(t, "login-multipage")
	if a.Login == nil {
		t.Fatal("login not detected")
	}
	if a.Login.URL != "https://shop.example.test/login" || a.Login.AfterURL != "https://shop.example.test/account" {
		t.Errorf("login = %+v", a.Login)
	}
	if a.ActionFrom != "https://shop.example.test/account" {
		t.Errorf("action start = %s", a.ActionFrom)
	}
	if len(a.Inputs) != 0 {
		t.Errorf("login fields must not become inputs: %+v", a.Inputs)
	}
	if len(a.Segments) != 4 {
		t.Errorf("segments = %+v", a.Segments)
	}
	for _, i := range a.Login.Steps {
		if !a.Steps[i].Login || a.Steps[i].SideEffect {
			t.Errorf("step %d = %+v", i, a.Steps[i])
		}
	}
	var order *Step
	for i := range a.Steps {
		if a.Steps[i].EventID == "e9" {
			order = &a.Steps[i]
		}
	}
	if order == nil || order.Until == nil || order.Until.URLMatches != `/account/orders/\d+(?:[?#]|$)` || order.SideEffect {
		t.Errorf("order click = %+v", order)
	}
}

func TestDetectRepetition(t *testing.T) {
	evs := []recording.Event{{ID: "e0", Seq: 1, Type: recording.EvNavigate, URL: "https://x.test/list"}}
	for i := 1; i <= 4; i++ {
		evs = append(evs,
			recording.Event{ID: fmt.Sprintf("c%d", i), Seq: len(evs) + 1, T: int64(i * 1000), Type: recording.EvClick, URL: "https://x.test/list",
				Target: &recording.Fingerprint{Tag: "button", CSS: fmt.Sprintf("ul#rows > li:nth-child(%d) > button.like", i)}},
			recording.Event{ID: fmt.Sprintf("k%d", i), Seq: len(evs) + 2, T: int64(i*1000 + 500), Type: recording.EvPressKey, URL: "https://x.test/list", Key: "Escape"},
		)
	}
	a := Detect(Normalize(nil, evs))
	if len(a.Repeats) != 1 {
		t.Fatalf("repeats = %+v", a.Repeats)
	}
	r := a.Repeats[0]
	if r.First != 1 || r.Period != 2 || r.Count != 4 || r.ItemSelector != "ul#rows > li" || r.Relative != "button.like" {
		t.Errorf("repetition = %+v", r)
	}
}

func TestDetectURLIDInputs(t *testing.T) {
	evs := []recording.Event{
		{ID: "e1", Seq: 1, Type: recording.EvNavigate, URL: "https://x.test/projects/4412/tasks?tab=open"},
		{ID: "e2", Seq: 2, Type: recording.EvClick, URL: "https://x.test/projects/4412/tasks?tab=open", Target: &recording.Fingerprint{Tag: "a", ID: "t"}},
	}
	a := Detect(Normalize(nil, evs))
	if a.Steps[0].URLTemplate != "https://x.test/projects/{{project_id}}/tasks?tab=open" {
		t.Errorf("template = %q", a.Steps[0].URLTemplate)
	}
	if len(a.Inputs) != 1 || a.Inputs[0].Name != "project_id" || a.Inputs[0].Default != "4412" {
		t.Errorf("inputs = %+v", a.Inputs)
	}
}

func TestSearchIsNotASideEffect(t *testing.T) {
	s := &Step{Kind: KindClick, Submits: true, Target: &recording.Fingerprint{Tag: "button", Text: "Search"}}
	if isSideEffect(s) {
		t.Error("search submit flagged as side effect")
	}
	s = &Step{Kind: KindClick, Target: &recording.Fingerprint{Tag: "button", Text: "Delete item"}}
	if !isSideEffect(s) {
		t.Error("delete click not flagged")
	}
}
