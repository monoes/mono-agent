package orgdecide

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/jev/jevconf"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgbridge"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// Client is the monomind surface the service reads and resolves through.
type Client interface {
	Status(ctx context.Context, root, org string) (json.RawMessage, error)
	Approvals(ctx context.Context, root, org string) (json.RawMessage, error)
	Questions(ctx context.Context, root, org string) (json.RawMessage, error)
	Gates(ctx context.Context, root, org string) (json.RawMessage, error)
	Approve(ctx context.Context, root, org, role, action string, approve bool, opts monomind.ResolveOptions) error
	Answer(ctx context.Context, root, org, questionID, answer string, opts monomind.ResolveOptions) error
	Gate(ctx context.Context, root, org, gateID string, approve bool, resolution string, opts monomind.ResolveOptions) error
	// Notify messages a role — how a denied approval carries its reason,
	// which monomind's approve/deny cannot (C-52).
	Notify(ctx context.Context, root, org, role, subject, body string) error
}

// autonomySender is the sender role of decision-service messages, so a
// requester can tell them from another agent's message.
const autonomySender = "autonomy"

// MonomindClient is Client over the monomind proxies.
type MonomindClient struct{}

func (MonomindClient) Status(ctx context.Context, root, org string) (json.RawMessage, error) {
	return monomind.OrgStatus(ctx, root, org)
}
func (MonomindClient) Approvals(ctx context.Context, root, org string) (json.RawMessage, error) {
	return monomind.OrgApprovals(ctx, root, org)
}
func (MonomindClient) Questions(ctx context.Context, root, org string) (json.RawMessage, error) {
	return monomind.OrgQuestions(ctx, root, org)
}
func (MonomindClient) Gates(ctx context.Context, root, org string) (json.RawMessage, error) {
	return monomind.OrgGates(ctx, root, org)
}
func (MonomindClient) Approve(ctx context.Context, root, org, role, action string, approve bool, opts monomind.ResolveOptions) error {
	var err error
	if approve {
		_, err = monomind.OrgApproveWith(ctx, root, org, role, action, opts)
	} else {
		_, err = monomind.OrgDenyWith(ctx, root, org, role, action, opts)
	}
	return err
}
func (MonomindClient) Notify(ctx context.Context, root, org, role, subject, body string) error {
	_, err := monomind.OrgInbox(ctx, root, org, monomind.InboxMessage{
		To: role, From: org + ":" + autonomySender, Subject: subject, Body: body,
	})
	return err
}
func (MonomindClient) Answer(ctx context.Context, root, org, questionID, answer string, opts monomind.ResolveOptions) error {
	_, err := monomind.OrgAnswerWith(ctx, root, org, questionID, answer, opts)
	return err
}
func (MonomindClient) Gate(ctx context.Context, root, org, gateID string, approve bool, resolution string, opts monomind.ResolveOptions) error {
	_, err := monomind.OrgGateResolveWith(ctx, root, org, gateID, approve, resolution, opts)
	return err
}

// ProfileRoot is one profile's org folder.
type ProfileRoot struct {
	ProfileID string
	Root      string
}

// Service routes and resolves decisions.
type Service struct {
	DB     *sql.DB
	Store  *Store
	Client Client
	// NewDecider builds the model decider for an org's settings (tests
	// inject fakes).
	NewDecider func(a *Autonomy) DeciderImpl
	// Roots lists every profile root to serve (the daemon spans profiles).
	Roots func(ctx context.Context) ([]ProfileRoot, error)
	// WorkflowFacts describes a granted workflow for the decider prompt,
	// from the DB (name, node types, outbound nodes).
	WorkflowFacts func(ctx context.Context, profileID, workflowID string) string
	Interval      time.Duration
	Logf          func(format string, args ...interface{})
	now           func() time.Time

	jevWarned sync.Map // profile/org → struct{}: "jev without a key" logged once
}

// NewService wires a Service with production defaults.
func NewService(db *sql.DB, roots func(ctx context.Context) ([]ProfileRoot, error)) *Service {
	s := &Service{
		DB: db, Store: NewStore(db), Client: MonomindClient{}, Roots: roots, Interval: 15 * time.Second,
		Logf: func(string, ...interface{}) {},
		now:  time.Now,
	}
	s.NewDecider = s.defaultDecider
	return s
}

// defaultDecider builds the model decider, wrapped in a JevDecider when the
// org chose kind jev (that choice is the opt-in, plan D2) and a TypeSafe key
// resolves for its profile. Without a key the model decides alone, and the
// daemon log says so once per org.
func (s *Service) defaultDecider(a *Autonomy) DeciderImpl {
	model := &ModelDecider{Runtime: a.Decider.Runtime, Model: a.Decider.Model, Timeout: time.Duration(a.Decider.TimeoutSeconds) * time.Second}
	if a.Decider.Kind != orgdesign.DeciderJev {
		return model
	}
	client, err := jevconf.NewClient(context.Background(), s.DB, a.ProfileID, "", "", jevconf.Decider)
	if err != nil {
		if _, seen := s.jevWarned.LoadOrStore(a.ProfileID+"/"+a.OrgName, struct{}{}); !seen && s.Logf != nil {
			s.Logf("orgdecide: %s/%s: decider jev has no key, the model decides instead: %v", a.ProfileID, a.OrgName, err)
		}
		return model
	}
	s.jevWarned.Delete(a.ProfileID + "/" + a.OrgName)
	return &JevDecider{Client: client, Fallback: model, Threshold: a.Decider.Threshold}
}

func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// Run ticks until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Logf("orgdecide: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick processes every org that has autonomy settings stored. Orgs without
// a row are manual, so there is nothing for the service to do for them.
func (s *Service) Tick(ctx context.Context) error {
	roots, err := s.Roots(ctx)
	if err != nil {
		return err
	}
	for _, pr := range roots {
		orgs, err := s.Store.ListOrgs(ctx, pr.ProfileID)
		if err != nil {
			return err
		}
		for _, org := range orgs {
			if ctx.Err() != nil {
				return nil
			}
			if _, err := s.ProcessOrg(ctx, pr.ProfileID, pr.Root, org); err != nil {
				s.Logf("orgdecide: %s/%s: %v", pr.ProfileID, org, err)
			}
		}
		s.SweepDelegations(ctx, pr.ProfileID, pr.Root)
	}
	return nil
}

// Pending collects an org's pending items with tiers assigned. The bool
// reports a partial list: at least one source failed, so an item's absence
// here does not mean it is gone.
func (s *Service) Pending(ctx context.Context, profileID, root, org string, a *Autonomy) ([]Item, string, bool, error) {
	var items []Item
	var errs []string
	collect := func(name string, fetch func(context.Context, string, string) (json.RawMessage, error), parse func([]byte) ([]Item, error)) {
		raw, err := fetch(ctx, root, org)
		if err != nil {
			errs = append(errs, name+": "+err.Error())
			return
		}
		got, err := parse(raw)
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		items = append(items, got...)
	}
	collect("approvals", s.Client.Approvals, ParseApprovals)
	collect("questions", s.Client.Questions, ParseQuestions)
	collect("gates", s.Client.Gates, ParseGates)
	if len(errs) == 3 {
		return nil, "", false, errors.New(strings.Join(errs, "; "))
	}
	// A partial fetch still routes what it did see — an item missed this tick
	// is picked up on the next one — but a caller that reads "absent" as
	// "resolved elsewhere" must know the list is incomplete (SweepDelegations).
	partial := len(errs) > 0
	if s.DB != nil {
		doc, _ := orgdesign.Load(root, org)
		if hil, err := PendingHIL(ctx, s.DB, profileID, org, doc); err != nil {
			partial = true
		} else {
			items = append(items, hil...)
		}
	}

	runID := ""
	if raw, err := s.Client.Status(ctx, root, org); err == nil {
		var st struct {
			Run string `json:"run"`
		}
		_ = json.Unmarshal(raw, &st)
		runID = st.Run
	}

	facts := s.tierFacts(ctx, profileID, root, org)
	for i := range items {
		items[i].Tier = TierFor(items[i].Class, items[i].Requester, a.Tiers, facts)
	}
	SortItems(items)
	return items, runID, partial, nil
}

func (s *Service) tierFacts(ctx context.Context, profileID, root, org string) TierFacts {
	grants, _ := orggrant.NewStore(s.DB).ListGrants(ctx, profileID, org, "")
	doc, _ := orgdesign.Load(root, org)
	return TierFacts{
		GrantTier: func(alias string) string {
			for _, g := range grants {
				if t := g.Automation(); t != nil && t.Alias == alias {
					return t.Tier
				}
			}
			return ""
		},
		RoleHasGrantsAndBash: func(role string) bool {
			held := false
			for _, g := range grants {
				if g.RoleID == role {
					held = true
				}
			}
			if !held || doc == nil {
				return false
			}
			r, _ := doc.FindRole(role)
			if r == nil {
				return false
			}
			for _, d := range r.PolicyStrings("denyTools") {
				if d == "Bash" {
					return false
				}
			}
			return true
		},
	}
}

// ProcessOrg routes and resolves every pending item of one org once. An
// item is routed again only when the org's effective level changed since
// its last routing (a pause, a resume, or an operator raising the level).
func (s *Service) ProcessOrg(ctx context.Context, profileID, root, org string) ([]Decision, error) {
	a, err := s.Store.Get(ctx, profileID, org)
	if err != nil {
		return nil, err
	}
	level := a.EffectiveLevel(s.clock())
	items, runID, _, err := s.Pending(ctx, profileID, root, org, a)
	if err != nil {
		return nil, err
	}
	doc, _ := orgdesign.Load(root, org)
	var out []Decision
	for _, it := range items {
		latest, err := s.Store.LatestFor(ctx, profileID, org, it.Kind, it.Ref)
		if err != nil {
			return out, err
		}
		if latest != nil && latest.Level == level {
			continue
		}
		d, err := s.decide(ctx, profileID, root, org, runID, level, a, doc, it)
		if err != nil {
			s.Logf("orgdecide: %s %s %s: %v", org, it.Kind, it.Ref, err)
		}
		if d != nil {
			out = append(out, *d)
		}
	}
	return out, nil
}

func (s *Service) decide(ctx context.Context, profileID, root, org, runID, level string, a *Autonomy, doc *orgdesign.Doc, it Item) (*Decision, error) {
	base := Decision{
		ProfileID: profileID, OrgName: org, RunID: runID, ItemKind: it.Kind, ItemRef: it.Ref, ItemHash: it.Hash,
		Requester: it.Requester, Class: it.Class, Tier: it.Tier, Level: level,
	}
	record := func(resolver, verdict, answer, rationale string, cost *float64, latency *int64) (*Decision, error) {
		d := base
		d.Resolver, d.Verdict, d.AnswerText, d.Rationale, d.CostUSD, d.LatencyMS = resolver, verdict, answer, rationale, cost, latency
		if base.Rationale != "" {
			d.Rationale = base.Rationale + "; " + rationale
		}
		return &d, s.Store.Record(ctx, &d)
	}

	// Repeat-denial rule, at every level (plan §7.7).
	if it.Kind != KindQuestion {
		if n, err := s.Store.DeniedCount(ctx, profileID, org, runID, it.Hash); err == nil && n >= 3 {
			reason := "repeat of a request denied 3 times in this run"
			if err := s.apply(ctx, root, org, it, false, "", "rule", reason); err != nil {
				return nil, err
			}
			return record("rule", VerdictDenied, "", reason, nil, nil)
		}
	}

	route := Route(level, it.Tier)
	if route == RouteRule && it.Kind == KindQuestion {
		route = RouteDecider // a rule cannot write an answer
	}
	switch route {
	case RouteHuman:
		return record("human", VerdictEscalated, "", fmt.Sprintf("%s routes %s items to a person", level, it.Tier), nil, nil)
	case RouteRule:
		reason := fmt.Sprintf("%s items are approved by rule at %s", it.Tier, level)
		if err := s.apply(ctx, root, org, it, true, "", "rule", reason); err != nil {
			return nil, err
		}
		return record("rule", VerdictApproved, "", reason, nil, nil)
	}

	// Decider route. boss and parent hand the item to a role; model and
	// jev decide here.
	if a.Decider.Kind != orgdesign.DeciderModel && a.Decider.Kind != orgdesign.DeciderJev {
		d, reason, err := s.delegate(ctx, profileID, root, org, level, a, doc, it, record)
		if d != nil || err != nil {
			return d, err
		}
		base.Rationale = reason + "; used fallback " + a.Decider.Fallback
	}
	usage, err := s.Store.Usage(ctx, profileID, org, runID)
	if err != nil {
		return nil, err
	}
	if usage.DeciderDecisions >= a.Limits.MaxDecisionsPerRun || usage.DeciderUSD >= a.Limits.MaxDeciderUSDPerRun {
		return s.fail(ctx, root, org, level, a, it, record, "model:"+a.Decider.Model,
			fmt.Sprintf("decider limit for this run reached (%d decisions, $%.2f)", usage.DeciderDecisions, usage.DeciderUSD), nil, nil)
	}

	in := PromptInput{OrgName: org, Item: it, Level: level, OperatorPolicy: a.Policy,
		BudgetNote: fmt.Sprintf("decider has used %d of %d decisions and $%.2f of $%.2f this run", usage.DeciderDecisions, a.Limits.MaxDecisionsPerRun, usage.DeciderUSD, a.Limits.MaxDeciderUSDPerRun)}
	if doc != nil {
		in.OrgGoal = doc.Goal
		if r, _ := doc.FindRole(it.Requester); r != nil {
			in.RequesterTitle, in.Responsibilities = r.Title, r.Responsibilities
		}
	}
	if strings.HasPrefix(it.Class, "grant:") && s.WorkflowFacts != nil {
		alias := strings.TrimPrefix(it.Class, "grant:")
		if doc != nil {
			if ref := doc.FindAutomation(alias); ref != nil {
				in.GrantFacts = s.WorkflowFacts(ctx, profileID, ref.WorkflowID)
			}
		}
	}
	if prior, err := s.Store.List(ctx, profileID, org, DecisionFilter{RunID: runID, Limit: 20}); err == nil {
		for _, p := range prior {
			in.PriorDecisions = append(in.PriorDecisions, fmt.Sprintf("%s %s by %s: %s", p.Class, p.Verdict, p.Resolver, p.Rationale))
		}
	}

	decider := s.NewDecider(a)
	dctx, cancel := context.WithTimeout(ctx, time.Duration(a.Decider.TimeoutSeconds)*time.Second+10*time.Second)
	outcome, derr := decider.Decide(dctx, BuildPrompt(in))
	cancel()
	base.Confidence, base.Probabilities = outcome.Confidence, outcome.Probabilities
	cost := outcome.CostUSD
	lat := outcome.Latency.Milliseconds()
	resolver := outcome.Resolver
	if resolver == "" {
		resolver = "model:" + a.Decider.Model
	}
	if derr != nil {
		return s.fail(ctx, root, org, level, a, it, record, resolver, derr.Error(), &cost, &lat)
	}
	v := outcome.Verdict
	switch v.Verdict {
	case "escalate":
		return record(resolver, VerdictEscalated, "", v.Rationale, &cost, &lat)
	case "answer":
		if err := s.apply(ctx, root, org, it, true, v.Answer, resolver, v.Rationale); err != nil {
			return nil, err
		}
		return record(resolver, VerdictAnswered, v.Answer, v.Rationale, &cost, &lat)
	case "approve", "deny":
		approve := v.Verdict == "approve"
		if err := s.apply(ctx, root, org, it, approve, "", resolver, v.Rationale); err != nil {
			return nil, err
		}
		verdict := VerdictDenied
		if approve {
			verdict = VerdictApproved
		}
		return record(resolver, verdict, "", v.Rationale, &cost, &lat)
	}
	return s.fail(ctx, root, org, level, a, it, record, resolver, "unexpected verdict "+v.Verdict, &cost, &lat)
}

// Delegator hooks, replaced in tests.
var (
	capabilityCheck = func(ctx context.Context) bool {
		set, err := monomind.Capabilities(ctx)
		return err == nil && set.Has(monomind.CapOrgToolProviders)
	}
	delegateSend = func(ctx context.Context, db *sql.DB, req orgbridge.SendRequest) error {
		_, err := orgbridge.Send(ctx, orgbridge.NewLedger(db), req)
		return err
	}
)

// delegate hands an item to a boss or parent decider role. It returns a
// reason (and no decision) when that decider cannot take the item, so the
// caller uses the fallback (C-50): the role raised the item itself, it has
// a gate of its own pending (every tool it has is blocked), it holds no
// decision tools, or monomind cannot give roles tools.
func (s *Service) delegate(ctx context.Context, profileID, root, org, level string, a *Autonomy, doc *orgdesign.Doc, it Item, record recordFunc) (*Decision, string, error) {
	if !capabilityCheck(ctx) {
		return nil, a.Decider.Kind + " decider needs monomind with capability org-tool-providers", nil
	}
	all, _, _ := orgdesign.LoadAll(root)
	dOrg, dRole, err := DeciderRole(a, doc, all)
	if err != nil {
		return nil, a.Decider.Kind + " decider unavailable: " + err.Error(), nil
	}
	if dOrg == org && it.Requester == dRole {
		return nil, "the decider role raised this item itself", nil
	}
	if raw, err := s.Client.Gates(ctx, root, dOrg); err == nil {
		if gates, err := ParseGates(raw); err == nil {
			for _, g := range gates {
				if g.Requester == dRole {
					return nil, "the decider role has its own gate pending", nil
				}
			}
		}
	}
	grants, _ := orggrant.NewStore(s.DB).ListGrants(ctx, profileID, dOrg, dRole)
	holds := false
	for _, g := range grants {
		for _, ot := range g.OrgTools {
			if ot.Tool == ToolDecisionResolve && containsString(ot.Orgs, org) {
				holds = true
			}
		}
	}
	if !holds {
		return nil, fmt.Sprintf("%s:%s holds no decision tools for %s (set them with `org autonomy set --decider %s`)", dOrg, dRole, org, a.Decider.Kind), nil
	}
	timeout := time.Duration(a.Decider.TimeoutSeconds) * time.Second
	del, err := s.Store.Delegate(ctx, profileID, org, level, dOrg, dRole, it, s.clock().Add(timeout))
	if err != nil {
		return nil, "", err
	}
	resolver := "boss:" + dRole
	if a.Decider.Kind == orgdesign.DeciderParent {
		resolver = "parent:" + dOrg + ":" + dRole
	}
	body := fmt.Sprintf("Decision %s for org %s: %s (class %s, tier %s).\nList pending decisions with monoagent__decision_list and resolve this one with monoagent__decision_resolve id=%q, verdict one of %s, and a one-sentence rationale. After %s without a decision it goes to %s.",
		del.ID, org, it.Summary, it.Class, it.Tier, del.ID, strings.Join(AllowedVerdicts(it.Kind, level), ", "), timeout, a.Decider.Fallback)
	if err := delegateSend(ctx, s.DB, orgbridge.SendRequest{
		ProfileID: profileID, Root: root, Org: dOrg, To: dRole, From: "operator:decisions",
		Subject: "[decision needed] " + it.Class + ": " + oneLine(it.Summary, 80), Body: body,
	}); err != nil {
		_, _ = s.Store.CloseDelegation(ctx, del.ID, DelegationExpired)
		return nil, "could not message the decider: " + err.Error(), nil
	}
	d, err := record(resolver, VerdictEscalated, "", "sent to "+resolver+"; fallback "+a.Decider.Fallback+" after "+timeout.String(), nil, nil)
	return d, "", err
}

// SweepDelegations sends every delegation whose decider timed out to the
// fallback model, and closes delegations whose item was resolved elsewhere.
func (s *Service) SweepDelegations(ctx context.Context, profileID, root string) {
	pending, err := s.Store.PendingDelegations(ctx, profileID)
	if err != nil {
		return
	}
	byOrg := map[string][]Delegation{}
	for _, d := range pending {
		byOrg[d.OrgName] = append(byOrg[d.OrgName], d)
	}
	for org, dels := range byOrg {
		a, err := s.Store.Get(ctx, profileID, org)
		if err != nil {
			continue
		}
		items, runID, partial, err := s.Pending(ctx, profileID, root, org, a)
		if err != nil {
			continue
		}
		// The pause brake applies here too: a paused org decides nothing, so
		// a delegation waits rather than falling back to the model. The level
		// is read now, not the one captured when the item was delegated.
		level := a.EffectiveLevel(s.clock())
		if level == orgdesign.LevelManual {
			continue
		}
		live := map[string]Item{}
		for _, it := range items {
			live[it.Kind+"|"+it.Ref] = it
		}
		doc, _ := orgdesign.Load(root, org)
		for _, d := range dels {
			it, stillPending := live[d.Item.Kind+"|"+d.Item.Ref]
			if !stillPending {
				// A partial list cannot tell "resolved elsewhere" from "the
				// source that holds it failed this tick"; closing here would
				// strand the item, because ProcessOrg skips anything whose
				// latest decision is an escalation at the current level.
				if partial {
					continue
				}
				_, _ = s.Store.CloseDelegation(ctx, d.ID, DelegationResolved)
				continue
			}
			if s.clock().Before(d.DeadlineAt) {
				continue
			}
			if ok, _ := s.Store.CloseDelegation(ctx, d.ID, DelegationExpired); !ok {
				continue
			}
			fallback := *a
			fallback.Decider.Kind = orgdesign.DeciderModel
			if _, err := s.decide(ctx, profileID, root, org, runID, level, &fallback, doc, it); err != nil {
				s.Logf("orgdecide: fallback for %s: %v", d.ID, err)
			}
		}
	}
}

type recordFunc func(resolver, verdict, answer, rationale string, cost *float64, latency *int64) (*Decision, error)

// fail applies the decider-failure rule: mid hands the item to a person;
// full denies it (telling the requester why) unless on_decider_failure is
// human (C-52).
func (s *Service) fail(ctx context.Context, root, org, level string, a *Autonomy, it Item, record recordFunc, resolver, reason string, cost *float64, lat *int64) (*Decision, error) {
	if level == orgdesign.LevelFull && a.OnDeciderFailure == "deny" {
		text := ""
		if it.Kind == KindQuestion {
			text = fmt.Sprintf("No decision could be made (%s). Continue without this, or stop.", reason)
		}
		if err := s.apply(ctx, root, org, it, false, text, resolver, "decider failed: "+reason); err != nil {
			return nil, err
		}
		// A question's answer and a gate's resolution already carry the
		// reason; monomind's approve/deny carries nothing, so the requester
		// only sees "DENIED" unless it is also messaged (C-52).
		if it.Kind == KindApproval && it.Requester != "" {
			body := fmt.Sprintf("[decided by %s] %q was denied: no decision could be made (%s). Continue without it, or stop.", resolver, it.Action, reason)
			if err := s.Client.Notify(ctx, root, org, it.Requester, "denied: "+it.Action, body); err != nil {
				s.Logf("orgdecide: telling %s why %s was denied: %v", it.Requester, it.Action, err)
			}
		}
	}
	return record(resolver, VerdictFailed, "", reason, cost, lat)
}

// apply resolves an item in monomind. Resolution text starts with
// "[decided by <resolver>]" so the requesting role sees who decided even
// without M5; with M5 the resolver is also passed as --by.
func (s *Service) apply(ctx context.Context, root, org string, it Item, approve bool, answer, resolver, rationale string) error {
	return ApplyVerdict(ctx, s.Client, s.DB, root, org, it, approve, answer, resolver, rationale)
}

// ApplyVerdict resolves one item for resolver: org items in monomind, HIL
// items in mono-agent's own queue.
func ApplyVerdict(ctx context.Context, c Client, db *sql.DB, root, org string, it Item, approve bool, answer, resolver, rationale string) error {
	opts := monomind.ResolveOptions{By: resolver, RequestID: it.RequestID}
	prefix := "[decided by " + resolver + "] "
	switch it.Kind {
	case KindApproval:
		return c.Approve(ctx, root, org, it.Requester, it.Action, approve, opts)
	case KindGate:
		return c.Gate(ctx, root, org, it.Ref, approve, prefix+rationale, opts)
	case KindQuestion:
		text := answer
		if text == "" {
			text = rationale
		}
		return c.Answer(ctx, root, org, it.Ref, prefix+text, opts)
	case KindHIL:
		if db == nil {
			return fmt.Errorf("orgdecide: no database to resolve HIL item %s", it.Ref)
		}
		return resolveHIL(ctx, db, it.Ref, approve)
	}
	return fmt.Errorf("orgdecide: cannot resolve %s items", it.Kind)
}
