package dynorg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakePicker struct {
	agents, skills       []Candidate
	agentsConf, skillsOK bool
	err                  error
}

func (p fakePicker) Agents(context.Context, string) ([]Candidate, bool, error) {
	return p.agents, p.agentsConf, p.err
}
func (p fakePicker) Skills(context.Context, string) ([]Candidate, bool, error) {
	return p.skills, p.skillsOK, p.err
}

type fakeChooser struct {
	answers map[string]string // question prefix -> choice
	asked   []string
}

func (c *fakeChooser) Choose(_ context.Context, _ any, q string, opts map[string]string) (string, float64, error) {
	c.asked = append(c.asked, q)
	for prefix, a := range c.answers {
		if strings.HasPrefix(q, prefix) {
			if _, ok := opts[a]; !ok {
				return "", 0, errors.New("not an option: " + a)
			}
			return a, 0.9, nil
		}
	}
	return "", 0, errors.New("no answer")
}

type fakeLibrary struct{ agents map[string][3]string }

func (l fakeLibrary) AgentBody(_ context.Context, id string) (string, string, string, error) {
	a, ok := l.agents[id]
	if !ok {
		return "", "", "", errors.New("no agent " + id)
	}
	return a[0], a[1], a[2], nil
}
func (l fakeLibrary) SkillText(_ context.Context, name string) (string, error) {
	if name == "missing" {
		return "", errors.New("no skill")
	}
	return "text of " + name, nil
}

var (
	opus   = Model{Runtime: "claude", Model: "opus", FullAccess: true, Read: true, Efforts: []string{"low", "high"}, CostUSD: 0.02, LatencyMs: 1500, Resume: true}
	haiku  = Model{Runtime: "claude", Model: "haiku", FullAccess: true, Read: true, CostUSD: 0.001, LatencyMs: 700, Resume: true}
	gpt    = Model{Runtime: "codex", Model: "gpt-5.5", FullAccess: true, Read: true, Efforts: []string{"low", "medium"}, CostUSD: 0.01, LatencyMs: 1200}
	readOK = Model{Runtime: "pi", Model: "", Read: true, CostUSD: 0.0005}
)

func lib() fakeLibrary {
	return fakeLibrary{agents: map[string][3]string{
		"engineering-code-reviewer": {"Review like a mentor.", "Code Reviewer", "engineering"},
		"qa-tester":                 {"Test everything.", "QA Tester", "testing"},
	}}
}

func TestStaffLeadChoicesWin(t *testing.T) {
	s := &Staffer{Roster: []Model{opus, haiku, gpt}, Lead: opus, ModelPicker: PickerLeadThenJev, Picker: fakePicker{}, Library: lib()}
	st, err := s.Staff(context.Background(), SpawnRequest{Brief: "review the diff", Role: "engineering-code-reviewer", Runtime: "codex", Model: "gpt-5.5", Effort: "medium", Access: ProfileResearch, Skills: []string{"golang-pro"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.Role != "Code Reviewer" || st.Model.Key() != "codex/gpt-5.5" || st.Effort != "medium" || st.Access != ProfileResearch {
		t.Errorf("staff = %+v", st)
	}
	if len(st.Skills) != 1 || st.Skills[0].Text != "text of golang-pro" {
		t.Errorf("skills = %+v", st.Skills)
	}
	if len(st.Fallbacks) != 2 {
		t.Errorf("fallbacks = %v", keys(st.Fallbacks))
	}
}

func TestStaffRefusesWithAlternatives(t *testing.T) {
	s := &Staffer{Roster: []Model{opus, readOK}, Lead: opus, Library: lib()}
	_, err := s.Staff(context.Background(), SpawnRequest{Brief: "x", Runtime: "codex", Model: "gpt-9"})
	if err == nil || !strings.Contains(err.Error(), "claude/opus") {
		t.Errorf("unknown model err = %v", err)
	}
	_, err = s.Staff(context.Background(), SpawnRequest{Brief: "fix it", Runtime: "pi", Access: ProfileCoding})
	if err == nil || !strings.Contains(err.Error(), "no full access") {
		t.Errorf("read-only model for a writer err = %v", err)
	}
	_, err = s.Staff(context.Background(), SpawnRequest{Brief: "x", Role: "wizard"})
	if err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("unknown role err = %v", err)
	}
	_, err = s.Staff(context.Background(), SpawnRequest{Brief: "x", Effort: "max"})
	if err == nil || !strings.Contains(err.Error(), "effort") {
		t.Errorf("bad effort err = %v", err)
	}
	_, err = s.Staff(context.Background(), SpawnRequest{Brief: "  "})
	if err == nil {
		t.Error("empty brief must be refused")
	}
	_, err = s.Staff(context.Background(), SpawnRequest{Brief: "x", Access: "admin"})
	if err == nil || !strings.Contains(err.Error(), "unknown access") {
		t.Errorf("bad access err = %v", err)
	}
}

func TestStaffPickAndJev(t *testing.T) {
	ch := &fakeChooser{answers: map[string]string{"Which agent": "qa-tester", "Which model": "codex/gpt-5.5", "How much": "low"}}
	s := &Staffer{Roster: []Model{opus, haiku, gpt}, Lead: opus, ModelPicker: PickerLeadThenJev, Chooser: ch, Library: lib(),
		Picker: fakePicker{agents: []Candidate{{ID: "engineering-code-reviewer", Probability: 0.4}, {ID: "qa-tester", Probability: 0.3}}, skills: []Candidate{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}, skillsOK: true}}
	st, err := s.Staff(context.Background(), SpawnRequest{Brief: "test the login page"})
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentType != "qa-tester" || st.Access != ProfileQA {
		t.Errorf("not-confident pick should go to Jev: %+v", st)
	}
	if st.Model.Key() != "codex/gpt-5.5" || st.Effort != "low" || st.JevConf == nil {
		t.Errorf("Jev model/effort: %+v", st)
	}
	if len(st.Skills) != 3 {
		t.Errorf("confident skills capped at 3: %+v", st.Skills)
	}
	if !strings.Contains(strings.Join(st.Why, ";"), "Jev chose the model") {
		t.Errorf("why = %v", st.Why)
	}
}

func TestStaffConfidentPickSkipsJev(t *testing.T) {
	ch := &fakeChooser{answers: map[string]string{}}
	s := &Staffer{Roster: []Model{opus}, Lead: opus, Chooser: ch, Library: lib(),
		Picker: fakePicker{agents: []Candidate{{ID: "engineering-code-reviewer", Probability: 0.9}}, agentsConf: true}}
	st, err := s.Staff(context.Background(), SpawnRequest{Brief: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentType != "engineering-code-reviewer" || st.PickConf == nil || *st.PickConf != 0.9 {
		t.Errorf("staff = %+v", st)
	}
	for _, q := range ch.asked {
		if strings.HasPrefix(q, "Which agent") {
			t.Error("a confident pick must not ask Jev for the role")
		}
	}
}

func TestStaffRulesWithoutJev(t *testing.T) {
	s := &Staffer{Roster: []Model{opus, haiku, gpt}, Lead: opus}
	st, err := s.Staff(context.Background(), SpawnRequest{Brief: "investigate where the config is loaded"})
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentType != "researcher" || st.Access != ProfileResearch || st.Model.Key() != "claude/haiku" {
		t.Errorf("research by rule should be the built-in researcher on the cheapest model: %+v", st)
	}
	st, err = s.Staff(context.Background(), SpawnRequest{Brief: "implement the parser"})
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentType != "coder" || st.Model.Key() != "claude/opus" {
		t.Errorf("writing work by rule should run on the lead's model: %+v", st)
	}
	yes := true
	st, _ = s.Staff(context.Background(), SpawnRequest{Brief: "review and fix", NeedsWrite: &yes})
	if st.Access != ProfileCoding {
		t.Errorf("needs_write must lift research to coding: %+v", st.Access)
	}
}

func TestStaffLeadPickerRequiresModel(t *testing.T) {
	s := &Staffer{Roster: []Model{opus, gpt}, Lead: opus, ModelPicker: PickerLead}
	if _, err := s.Staff(context.Background(), SpawnRequest{Brief: "do it"}); err == nil || !strings.Contains(err.Error(), "choose a model") {
		t.Errorf("err = %v", err)
	}
	if _, err := s.Staff(context.Background(), SpawnRequest{Brief: "do it", Runtime: "codex"}); err != nil {
		t.Errorf("a runtime alone picks its ready model: %v", err)
	}
}

func TestWorkerPromptCarriesContract(t *testing.T) {
	p := workerSystemPrompt(Staff{Role: "Tester", AgentBody: "Test.", Access: ProfileQA, Skills: []Skill{{Name: "e2e", Text: "Use playwright."}, {Name: "bare"}}}, "/w", []string{"a.go"}, false)
	for _, want := range []string{"Tester", "/w", "Test.", "Use playwright.", "Use the bare skill", "monoagentcli", "a.go", "200 words", "people records"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q", want)
		}
	}
	if !strings.Contains(workerSystemPrompt(Staff{Role: "R", Access: ProfileResearch}, "/w", nil, false), "Do not edit") {
		t.Error("research prompt must forbid edits")
	}
}

func TestStaffRefusesBadSkillsAndRoles(t *testing.T) {
	s := &Staffer{Roster: []Model{opus}, Lead: opus, Library: lib()}
	for _, sk := range []string{"missing", "../etc", "-rf", "a*b", "x/y"} {
		if _, err := s.Staff(context.Background(), SpawnRequest{Brief: "x", Skills: []string{sk}}); err == nil {
			t.Errorf("skill %q must be refused", sk)
		}
	}
	for _, role := range []string{"../../agent", "-x", "a?"} {
		if _, err := s.Staff(context.Background(), SpawnRequest{Brief: "x", Role: role}); err == nil {
			t.Errorf("role %q must be refused", role)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"golang-pro", "engineering-code-reviewer", "a.b_c", "x1"} {
		if !ValidName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-flag", "..", "a..b", "a/b", "a*", "a b", ".hidden"} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
