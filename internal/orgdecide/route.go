package orgdecide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// Item kinds.
const (
	KindApproval = "approval"
	KindGate     = "gate"
	KindQuestion = "question"
	KindHIL      = "hil"
)

// Item is one pending decision.
type Item struct {
	Kind      string                   `json:"kind"`
	Ref       string                   `json:"ref"`       // requestId | role:action:ts | gateId | questionId | hil id
	Requester string                   `json:"requester"` // role id (execution id for hil)
	Class     string                   `json:"class"`
	Tier      string                   `json:"tier"`
	Summary   string                   `json:"summary"`
	Action    string                   `json:"-"` // approvals: the action to approve
	RequestID string                   `json:"-"` // approvals with M5
	Inputs    []map[string]interface{} `json:"-"` // approvals: every pending input for (role, action) (C-55)
	Text      string                   `json:"-"` // question text, or gate description (agent-written, untrusted)
	Name      string                   `json:"-"` // gate name
	WaitingMS int64                    `json:"-"` // epoch ms the item appeared
	Hash      string                   `json:"-"`
}

// ClassForAction maps an approval action to its decision class (contracts
// §8): org_complete, grant:<alias> for monoagent__automation_<alias>,
// org_start for monoagent__org_start, else tool:<action>.
func ClassForAction(action string) string {
	switch {
	case action == "org_complete":
		return "org_complete"
	case strings.HasPrefix(action, "monoagent__automation_"):
		return "grant:" + strings.TrimPrefix(action, "monoagent__automation_")
	case action == "monoagent__org_start":
		return "org_start"
	}
	return "tool:" + action
}

// DefaultTiers is the plan §7.7 tier table for classes whose tier does not
// depend on facts; grant:<alias> and hil:<alias> take their grant's tier.
var DefaultTiers = map[string]string{
	"tool:*":       orgdesign.TierRoutine,
	"org_complete": orgdesign.TierConsequential,
	"question":     orgdesign.TierConsequential,
	"org_start":    orgdesign.TierConsequential,
	"gate":         orgdesign.TierIrreversible,
}

// TierFacts are the Go-side facts a tier depends on — read from the DB and
// the org config, never from anything the requesting agent wrote.
type TierFacts struct {
	// GrantTier returns a grant's tier by alias ("" when unknown).
	GrantTier func(alias string) string
	// RoleHasGrantsAndBash reports a role that holds a grant but may still
	// use Bash, which can bypass its grants (C-2).
	RoleHasGrantsAndBash func(role string) bool
}

// TierFor computes an item's tier: an exact override wins, then a wildcard
// override, then the fact-based default. Unknown classes are irreversible —
// an item nobody classified never gets the cheapest route.
func TierFor(class, requester string, overrides map[string]string, facts TierFacts) string {
	if t, ok := overrides[class]; ok && orgdesign.ValidTier(t) {
		return t
	}
	for _, p := range []string{"tool:", "grant:"} {
		if strings.HasPrefix(class, p) {
			if t, ok := overrides[p+"*"]; ok && orgdesign.ValidTier(t) {
				return t
			}
		}
	}
	switch {
	case strings.HasPrefix(class, "tool:"):
		if class == "tool:Bash" && facts.RoleHasGrantsAndBash != nil && facts.RoleHasGrantsAndBash(requester) {
			return orgdesign.TierConsequential
		}
		return orgdesign.TierRoutine
	case strings.HasPrefix(class, "grant:"), strings.HasPrefix(class, "hil:"):
		alias := class[strings.IndexByte(class, ':')+1:]
		if facts.GrantTier != nil {
			if t := facts.GrantTier(alias); orgdesign.ValidTier(t) {
				return t
			}
		}
		return orgdesign.TierIrreversible
	}
	if t, ok := DefaultTiers[class]; ok {
		return t
	}
	return orgdesign.TierIrreversible
}

// Routes.
const (
	RouteRule    = "rule"
	RouteDecider = "decider"
	RouteHuman   = "human"
)

// Route applies the level × tier matrix (plan §7.7).
func Route(level, tier string) string {
	switch level {
	case orgdesign.LevelMid:
		switch tier {
		case orgdesign.TierRoutine:
			return RouteRule
		case orgdesign.TierConsequential:
			return RouteDecider
		}
		return RouteHuman
	case orgdesign.LevelFull:
		if tier == orgdesign.TierRoutine {
			return RouteRule
		}
		return RouteDecider
	}
	return RouteHuman
}

// hashItem keys the repeat-denial rule: kind, requester, and normalized
// request content.
func hashItem(kind, requester string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind + "\x00" + requester))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(strings.Join(strings.Fields(p), " ")))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

type approvalEntry struct {
	RoleID     string                 `json:"roleId"`
	Action     string                 `json:"action"`
	Question   string                 `json:"question"`
	TS         int64                  `json:"ts"`
	Approved   *bool                  `json:"approved"`
	RequestID  *string                `json:"requestId"`
	Input      map[string]interface{} `json:"input"`
	ResolvedBy *string                `json:"resolvedBy"`
}

type questionEntry struct {
	QuestionID string  `json:"questionId"`
	Role       string  `json:"role"`
	Question   string  `json:"question"`
	TS         int64   `json:"ts"`
	Answer     *string `json:"answer"`
}

type gateEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RoleID      string `json:"roleId"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"createdAt"`
}

type itemsPayload[T any] struct {
	Items []T `json:"items"`
}

// ParseApprovals turns `org approvals <org> --format json` into items.
// With M5 every request is its own item; before M5 one resolution covers
// every pending request of a (role, action) pair, so they are grouped into
// one item that carries all of their inputs for the decider (C-55).
func ParseApprovals(raw []byte) ([]Item, error) {
	var p itemsPayload[approvalEntry]
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("orgdecide: approvals: %w", err)
	}
	var items []Item
	grouped := map[string]int{}
	for _, a := range p.Items {
		if a.Approved != nil {
			continue
		}
		class := ClassForAction(a.Action)
		if a.RequestID != nil && *a.RequestID != "" {
			in, _ := json.Marshal(a.Input)
			items = append(items, Item{
				Kind: KindApproval, Ref: *a.RequestID, RequestID: *a.RequestID, Requester: a.RoleID, Class: class,
				Action: a.Action, Summary: fmt.Sprintf("%s wants to use %s", a.RoleID, a.Action),
				Inputs: []map[string]interface{}{a.Input}, WaitingMS: a.TS,
				Hash: hashItem(KindApproval, a.RoleID, a.Action, string(in)),
			})
			continue
		}
		key := a.RoleID + ":" + a.Action
		if i, ok := grouped[key]; ok {
			if a.Input != nil {
				items[i].Inputs = append(items[i].Inputs, a.Input)
			}
			continue
		}
		grouped[key] = len(items)
		it := Item{
			Kind: KindApproval, Ref: fmt.Sprintf("%s:%s:%d", a.RoleID, a.Action, a.TS), Requester: a.RoleID, Class: class,
			Action: a.Action, Summary: fmt.Sprintf("%s wants to use %s", a.RoleID, a.Action), WaitingMS: a.TS,
			Hash: hashItem(KindApproval, a.RoleID, a.Action),
		}
		if a.Input != nil {
			it.Inputs = []map[string]interface{}{a.Input}
		}
		items = append(items, it)
	}
	return items, nil
}

// ParseQuestions turns `org questions <org> --format json` into items.
func ParseQuestions(raw []byte) ([]Item, error) {
	var p itemsPayload[questionEntry]
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("orgdecide: questions: %w", err)
	}
	var items []Item
	for _, q := range p.Items {
		if q.Answer != nil {
			continue
		}
		items = append(items, Item{
			Kind: KindQuestion, Ref: q.QuestionID, Requester: q.Role, Class: "question", Text: q.Question,
			Summary: fmt.Sprintf("%s asks: %s", q.Role, oneLine(q.Question, 120)), WaitingMS: q.TS,
			Hash: hashItem(KindQuestion, q.Role, q.Question),
		})
	}
	return items, nil
}

// ParseGates turns `org gates <org> --format json` into items.
func ParseGates(raw []byte) ([]Item, error) {
	var p itemsPayload[gateEntry]
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("orgdecide: gates: %w", err)
	}
	var items []Item
	for _, g := range p.Items {
		if g.Status != "pending" {
			continue
		}
		items = append(items, Item{
			Kind: KindGate, Ref: g.ID, Requester: g.RoleID, Class: "gate", Name: g.Name, Text: g.Description,
			Summary: fmt.Sprintf("%s opened gate %q", g.RoleID, g.Name), WaitingMS: g.CreatedAt,
			Hash: hashItem(KindGate, g.RoleID, g.Name, g.Description),
		})
	}
	return items, nil
}

// SortItems orders items oldest first.
func SortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].WaitingMS < items[j].WaitingMS })
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
