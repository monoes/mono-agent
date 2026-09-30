package dynorg

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/agentroster"
)

// Candidate is one ranked agent or skill from monomind pick.
type Candidate struct {
	ID          string
	Name        string
	Category    string
	Description string
	Probability float64
}

// Picker ranks monomind's agents and skills for a brief (`monomind pick`).
// confident is pick's own verdict for the list.
type Picker interface {
	Agents(ctx context.Context, brief string) (list []Candidate, confident bool, err error)
	Skills(ctx context.Context, brief string) (list []Candidate, confident bool, err error)
}

// Chooser answers typed questions (Jev). options maps an option id to its
// description.
type Chooser interface {
	Choose(ctx context.Context, state any, question string, options map[string]string) (choice string, confidence float64, err error)
}

// Library reads an agent definition's instructions and a skill's text.
type Library interface {
	AgentBody(ctx context.Context, id string) (body, title, category string, err error)
	SkillText(ctx context.Context, name string) (string, error)
}

// Staffer staffs workers for one turn.
type Staffer struct {
	Roster      []Model // ready models, best first
	Lead        Model   // the lead's own model
	ModelPicker string  // PickerLead, PickerJev or PickerLeadThenJev
	Picker      Picker  // nil = built-in roles only
	Chooser     Chooser // nil = no Jev: the fallback rules choose
	Library     Library // nil = built-in roles only
	// Quality is the roster's track record from earlier workers (#230):
	// bad fits for a worker's category rank last, and Jev sees the rates.
	Quality agentroster.Quality
}

// Staff turns a spawn request into a staffed worker. The lead's own choices
// win and are only checked; the rest comes from pick, Jev, and the fallback
// rules, in that order. A choice that can't be honored is an error the lead
// sees as the tool result, with what it can use instead.
func (s *Staffer) Staff(ctx context.Context, req SpawnRequest) (Staff, error) {
	var st Staff
	if strings.TrimSpace(req.Brief) == "" {
		return st, fmt.Errorf("brief is required: say what the worker should do")
	}
	if err := s.staffRole(ctx, req, &st); err != nil {
		return st, err
	}
	if req.Access != "" {
		if !slices.Contains(Profiles, req.Access) {
			return st, fmt.Errorf("unknown access %q; use one of %s", req.Access, strings.Join(Profiles, ", "))
		}
		st.Access = req.Access
		st.Why = append(st.Why, "lead chose access")
	} else if st.Access == "" {
		st.Access = profileForCategory(st.Category, st.AgentType)
	}
	if req.NeedsWrite != nil && *req.NeedsWrite && st.Access == ProfileResearch {
		st.Access = ProfileCoding
	}
	skills, err := s.staffSkills(ctx, req)
	if err != nil {
		return st, err
	}
	st.Skills = skills
	if err := s.staffModel(ctx, req, &st); err != nil {
		return st, err
	}
	return st, s.staffEffort(ctx, req, &st)
}

func (s *Staffer) staffRole(ctx context.Context, req SpawnRequest, st *Staff) error {
	setBuiltin := func(r builtinRole, why string) {
		st.Role, st.AgentType, st.Category, st.AgentBody = r.Title, r.ID, r.Category, r.Body
		st.Access = r.Profile
		st.Why = append(st.Why, why)
	}
	if req.Role != "" {
		if s.Library != nil {
			if body, title, cat, err := s.Library.AgentBody(ctx, req.Role); err == nil {
				st.Role, st.AgentType, st.Category, st.AgentBody = orElse(title, req.Role), req.Role, cat, body
				st.Why = append(st.Why, "lead chose the role")
				return nil
			}
		}
		if r, ok := builtinByID(req.Role); ok {
			setBuiltin(r, "lead chose the role")
			return nil
		}
		return fmt.Errorf("unknown role %q; leave role empty to have one picked, or use one of the roles org_roster lists", req.Role)
	}
	if s.Picker != nil && s.Library != nil {
		list, confident, err := s.Picker.Agents(ctx, req.Brief)
		if err == nil && len(list) > 0 {
			pick, why := list[0], "role from monomind pick"
			if !confident && s.Chooser != nil && len(list) > 1 {
				opts := map[string]string{}
				for _, c := range list {
					opts[c.ID] = strings.TrimSpace(c.Name + ": " + c.Description)
				}
				if id, conf, err := s.Chooser.Choose(ctx, map[string]any{"brief": req.Brief}, "Which agent should do this brief?", opts); err == nil {
					if i := slices.IndexFunc(list, func(c Candidate) bool { return c.ID == id }); i >= 0 {
						pick, why = list[i], "role from Jev over monomind pick's shortlist"
						st.JevConf = &conf
					}
				}
			}
			if body, title, cat, err := s.Library.AgentBody(ctx, pick.ID); err == nil {
				p := pick.Probability
				st.Role, st.AgentType, st.Category, st.AgentBody = orElse(title, orElse(pick.Name, pick.ID)), pick.ID, orElse(cat, pick.Category), body
				st.PickConf = &p
				st.Why = append(st.Why, why)
				return nil
			}
		}
	}
	setBuiltin(builtinFor(req.Brief), "built-in role (no monomind agent matched)")
	return nil
}

// staffSkills is the lead's skills, else pick's when it is confident (at
// most three). A skill the lead names must exist.
func (s *Staffer) staffSkills(ctx context.Context, req SpawnRequest) ([]Skill, error) {
	names := req.Skills
	fromLead := len(names) > 0
	if len(names) == 0 && s.Picker != nil {
		if list, confident, err := s.Picker.Skills(ctx, req.Brief); err == nil && confident {
			for _, c := range list {
				if len(names) == 3 {
					break
				}
				names = append(names, c.ID)
			}
		}
	}
	out := make([]Skill, 0, len(names))
	for _, n := range names {
		sk := Skill{Name: n}
		if fromLead && !ValidName(n) {
			return nil, fmt.Errorf("invalid skill name %q", n)
		}
		if s.Library != nil {
			text, err := s.Library.SkillText(ctx, n)
			if err != nil && fromLead {
				return nil, fmt.Errorf("unknown skill %q; leave skills empty to have them picked", n)
			}
			sk.Text = text
		}
		out = append(out, sk)
	}
	return out, nil
}

func (s *Staffer) staffModel(ctx context.Context, req SpawnRequest, st *Staff) error {
	eligible := make([]Model, 0, len(s.Roster))
	for _, m := range s.Roster {
		if m.fits(st.Access) {
			eligible = append(eligible, m)
		}
	}
	eligible, bad := s.rankByQuality(eligible, st.Category)
	if req.Runtime != "" || req.Model != "" {
		i := slices.IndexFunc(s.Roster, func(m Model) bool {
			return (req.Runtime == "" || m.Runtime == req.Runtime) && (req.Model == "" || m.Model == req.Model || strings.EqualFold(m.Label, req.Model))
		})
		if i < 0 {
			return fmt.Errorf("%s is not a ready model; ready models: %s", strings.Trim(req.Runtime+"/"+req.Model, "/"), keys(eligible))
		}
		m := s.Roster[i]
		if !m.fits(st.Access) {
			return fmt.Errorf("%s can't run a %s worker (it has no %s access); use one of: %s", m.Key(), st.Access, accessNeed(st.Access), keys(eligible))
		}
		st.Model = m
		st.Fallbacks = without(eligible, m)
		st.Why = append(st.Why, "lead chose the model")
		return nil
	}
	if len(eligible) == 0 {
		return fmt.Errorf("no ready model can run a %s worker; validate models on the AI agents page (`monoagentcli agent validate`)", st.Access)
	}
	if s.ModelPicker == PickerLead {
		return fmt.Errorf("choose a model for this worker (runtime and model); ready models: %s", keys(eligible))
	}
	if s.Chooser != nil && len(eligible) > 1 {
		opts := map[string]string{}
		for _, m := range eligible {
			opts[m.Key()] = modelDescription(m)
			if r, ok := s.trackRecord(m, st.Category); ok {
				opts[m.Key()] += ", " + trackRecordText(r)
			}
		}
		state := map[string]any{"brief": req.Brief, "role": st.Role, "category": st.Category, "access": st.Access, "lead_model": s.Lead.Key()}
		if tr := s.trackRecordState(eligible, st.Category); len(tr) > 0 {
			state["track_record"] = tr
		}
		if id, conf, err := s.Chooser.Choose(ctx, state, "Which model should run this worker? Prefer the cheaper model when two fit equally well, and avoid a model with a low success rate for this kind of work.", opts); err == nil {
			if i := slices.IndexFunc(eligible, func(m Model) bool { return m.Key() == id }); i >= 0 {
				st.Model, st.JevConf = eligible[i], &conf
				st.Fallbacks = without(eligible, eligible[i])
				st.Why = append(st.Why, "Jev chose the model")
				return nil
			}
		}
	}
	// The rule never picks a bad fit while another model can do the work.
	fits := eligible[:len(eligible)-len(bad)]
	if len(fits) == 0 {
		fits = eligible
	}
	st.Model = ruleModel(fits, s.Lead, st.Access)
	st.Fallbacks = without(eligible, st.Model)
	st.Why = append(st.Why, "model by rule (no Jev answer)")
	if len(bad) > 0 && len(bad) < len(eligible) {
		st.Why = append(st.Why, "passed over for a low success rate: "+keys(bad))
	}
	return nil
}

// ruleModel is the model choice without Jev: research goes to the cheapest,
// fastest ready model that runs read-only (--access read or a read-only
// sandbox; one that doesn't must hold the write lease), else the cheapest;
// writing work to the lead's own model when it can, else the priciest (a
// stand-in for the strongest).
func ruleModel(eligible []Model, lead Model, access string) Model {
	sorted := slices.Clone(eligible)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CostUSD != sorted[j].CostUSD {
			return sorted[i].CostUSD < sorted[j].CostUSD
		}
		return sorted[i].LatencyMs < sorted[j].LatencyMs
	})
	if access == ProfileResearch {
		if i := slices.IndexFunc(sorted, func(m Model) bool { return m.Read || m.ReadOnlySandbox }); i >= 0 {
			return sorted[i]
		}
		return sorted[0]
	}
	if i := slices.IndexFunc(eligible, func(m Model) bool { return m.Key() == lead.Key() }); i >= 0 {
		return eligible[i]
	}
	return sorted[len(sorted)-1]
}

func (s *Staffer) staffEffort(ctx context.Context, req SpawnRequest, st *Staff) error {
	levels := st.Model.Efforts
	if req.Effort != "" {
		if !slices.Contains(levels, req.Effort) {
			if len(levels) == 0 {
				return fmt.Errorf("%s takes no effort level; leave effort empty", st.Model.Key())
			}
			return fmt.Errorf("effort %q is not one of %s's levels: %s", req.Effort, st.Model.Key(), strings.Join(levels, ", "))
		}
		st.Effort = req.Effort
		return nil
	}
	if len(levels) == 0 || s.Chooser == nil {
		return nil
	}
	opts := map[string]string{}
	for _, l := range levels {
		opts[l] = "reasoning effort " + l
	}
	if id, _, err := s.Chooser.Choose(ctx, map[string]any{"brief": req.Brief, "role": st.Role}, "How much reasoning effort does this brief need? Less is faster and cheaper.", opts); err == nil && slices.Contains(levels, id) {
		st.Effort = id
	}
	return nil
}

func modelDescription(m Model) string {
	parts := []string{orElse(m.Label, m.Key())}
	if m.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("test turn cost $%.4f", m.CostUSD))
	}
	if m.LatencyMs > 0 {
		parts = append(parts, fmt.Sprintf("answers in %.1fs", float64(m.LatencyMs)/1000))
	}
	return strings.Join(parts, ", ")
}

func accessNeed(profile string) string {
	if profile == ProfileResearch {
		return "read"
	}
	return "full"
}

func keys(ms []Model) string {
	if len(ms) == 0 {
		return "(none)"
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Key()
	}
	return strings.Join(out, ", ")
}

func without(ms []Model, drop Model) []Model {
	out := make([]Model, 0, len(ms))
	for _, m := range ms {
		if m.Key() != drop.Key() {
			out = append(out, m)
		}
	}
	return out
}

func orElse(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
