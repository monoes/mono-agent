package orgdesign

import (
	"encoding/json"
	"fmt"
)

// NewOrgOptions configures NewOrg. Zero values mean "use the default".
type NewOrgOptions struct {
	Schedule      json.RawMessage // nil -> JSON null
	Runtime       string
	Workspace     string // run_config.workspace
	RootRoleID    string // default "lead"
	RootRoleTitle string // default "Lead"
	RootRoleType  string // default "boss"
}

// NewOrg builds a fresh Doc with a single root role. It does not write
// anything to disk — callers pass the result to Save.
func NewOrg(name, goal string, opts NewOrgOptions) *Doc {
	rootID := opts.RootRoleID
	if rootID == "" {
		rootID = "lead"
	}
	rootTitle := opts.RootRoleTitle
	if rootTitle == "" {
		rootTitle = "Lead"
	}
	rootType := opts.RootRoleType
	if rootType == "" {
		rootType = "boss"
	}

	schedule := opts.Schedule
	if schedule == nil {
		schedule = json.RawMessage("null")
	}

	d := &Doc{
		Name:     name,
		Goal:     goal,
		Status:   "stopped",
		Schedule: schedule,
		Runtime:  opts.Runtime,
		Roles: []Role{{
			ID:               rootID,
			Title:            rootTitle,
			Type:             rootType,
			ReportsTo:        nil,
			Responsibilities: []string{},
		}},
	}
	if opts.Workspace != "" {
		wsJSON, _ := json.Marshal(opts.Workspace)
		d.RunConfig = map[string]json.RawMessage{"workspace": wsJSON}
	}
	return d
}

// RolePatch is a shallow, patch-style update for UpdateRole. A nil field is
// left unchanged. A non-nil pointer to an empty string (or empty slice)
// explicitly clears the field — needed so the UI/AI can return an optional
// value (e.g. budget_usd) back to "inherit" rather than being unable to
// ever unset it once set. Model, Icon, and Color patch nested fields
// (AdapterConfig["model"], UI.Icon, UI.Color respectively).
type RolePatch struct {
	Title            *string
	Type             *string
	Responsibilities *[]string
	Runtime          *string // "" clears role.Extra["runtime"]
	Model            *string // "" clears AdapterConfig["model"]
	Icon             *string
	Color            *string
	// MaxTurnsPerMessage, BudgetTokens, BudgetUSD patch role.Extra's
	// like-named keys (max_turns_per_message, budget_tokens, budget_usd —
	// all RoleSchema-optional, unset means "inherit the org-wide default").
	// Known limitation: since JSON.stringify drops undefined-valued object
	// keys, the frontend inspector cannot currently distinguish "user
	// cleared this field back to inherit" from "field untouched" for these
	// three — both arrive as the key simply being absent from patchJSON, so
	// nil here always means "leave unchanged," never "clear." Unlike
	// Runtime/Model above, there is no in-band empty-value sentinel for a
	// number (0 is a real, different value from "unset"). Clearing an
	// already-set budget currently requires editing the org's JSON file
	// directly; revisit if that turns out to matter in practice.
	MaxTurnsPerMessage *int
	BudgetTokens       *int
	BudgetUSD          *float64
	// Policy and Provider replace the role's whole `policy`/`provider`
	// object wholesale (the frontend always sends the complete object, not
	// a deep partial) — nil leaves it unchanged, a RawMessage of `null`
	// clears it. Both are opaque here (they live in role.Extra); monomind's
	// own schema (RolePolicySchema/ProviderSchema) is the source of truth
	// for their shape, this package only round-trips them.
	Policy   *json.RawMessage
	Provider *json.RawMessage
	// InstructionsFile patches role.Extra["instructions_file"]; "" clears it.
	InstructionsFile *string
	// AdapterConfigProvider/AdapterConfigMaxTokens patch
	// AdapterConfig["provider"]/["max_tokens"], parallel to Model above.
	AdapterConfigProvider  *string
	AdapterConfigMaxTokens *int
}

// AddRole appends a new role to d. If r.ID is empty, one is derived from
// r.Title via UniqueRoleID. If r.ID is non-empty and already exists, AddRole
// returns an error rather than silently renaming it — a caller-supplied id
// (in particular from an AI tool call that references it in the same turn,
// e.g. for reports_to) must be honored or explicitly rejected, never
// changed underneath the caller.
func (d *Doc) AddRole(r Role) (*Role, error) {
	if r.ID == "" {
		r.ID = UniqueRoleID(d, r.Title)
	} else if _, idx := d.FindRole(r.ID); idx != -1 {
		return nil, fmt.Errorf("duplicate role id: %s", r.ID)
	}
	if r.Responsibilities == nil {
		r.Responsibilities = []string{}
	}
	if r.Type == "" {
		r.Type = "specialist"
	}
	// "boss" is derived from tree position (see UpdateRole's matching guard,
	// PromoteToRoot) — silently downgrade rather than error here, since a
	// non-root add with type "boss" is never a deliberate user choice (there
	// is no UI path to type it directly anymore) but can arrive incidentally
	// — e.g. the palette's category->type suggestion map happens to say
	// "boss" for a "management"/"sparc" archetype, and every palette-driven
	// add is non-root by construction (resolveDefaultParent always assigns
	// an existing role as parent once one exists).
	if r.Type == "boss" && r.ReportsTo != nil {
		r.Type = "specialist"
	}
	d.Roles = append(d.Roles, r)
	added, _ := d.FindRole(r.ID)
	return added, nil
}

// UpdateRole applies patch to the named role. Returns an error if the role
// doesn't exist.
func (d *Doc) UpdateRole(id string, patch RolePatch) (*Role, error) {
	r, idx := d.FindRole(id)
	if idx == -1 {
		return nil, fmt.Errorf("role not found: %s", id)
	}
	if patch.Title != nil {
		r.Title = *patch.Title
	}
	if patch.Type != nil {
		// "boss" is derived from tree position (see PromoteToRoot), never a
		// freely-typed label — reject here so a stray patch (including from
		// the AI tool path, which also goes through UpdateRole) can't create
		// the exact ambiguous multi-"boss" state PromoteToRoot exists to
		// prevent. Only reject the string itself; everything else stays a
		// free-form cosmetic label.
		if *patch.Type == "boss" && r.ReportsTo != nil {
			return nil, fmt.Errorf(`role %q is not the org root — use "set as org boss" (PromoteToRoot) to make a role type "boss" instead of patching it directly`, id)
		}
		r.Type = *patch.Type
	}
	if patch.Responsibilities != nil {
		r.Responsibilities = *patch.Responsibilities
	}
	if patch.Runtime != nil {
		if *patch.Runtime == "" {
			delete(r.Extra, "runtime")
		} else {
			b, _ := json.Marshal(*patch.Runtime)
			if r.Extra == nil {
				r.Extra = map[string]json.RawMessage{}
			}
			r.Extra["runtime"] = b
		}
	}
	if patch.Model != nil {
		if *patch.Model == "" {
			delete(r.AdapterConfig, "model")
		} else {
			if r.AdapterConfig == nil {
				r.AdapterConfig = map[string]json.RawMessage{}
			}
			b, _ := json.Marshal(*patch.Model)
			r.AdapterConfig["model"] = b
		}
	}
	if patch.Icon != nil || patch.Color != nil {
		if r.UI == nil {
			r.UI = &RoleUI{}
		}
		if patch.Icon != nil {
			r.UI.Icon = *patch.Icon
		}
		if patch.Color != nil {
			r.UI.Color = *patch.Color
		}
	}
	if patch.AdapterConfigProvider != nil {
		if *patch.AdapterConfigProvider == "" {
			delete(r.AdapterConfig, "provider")
		} else {
			if r.AdapterConfig == nil {
				r.AdapterConfig = map[string]json.RawMessage{}
			}
			b, _ := json.Marshal(*patch.AdapterConfigProvider)
			r.AdapterConfig["provider"] = b
		}
	}
	setExtraNumber(r, "max_turns_per_message", patch.MaxTurnsPerMessage)
	setExtraNumber(r, "budget_tokens", patch.BudgetTokens)
	setExtraNumberF(r, "budget_usd", patch.BudgetUSD)
	if patch.AdapterConfigMaxTokens != nil {
		if r.AdapterConfig == nil {
			r.AdapterConfig = map[string]json.RawMessage{}
		}
		b, _ := json.Marshal(*patch.AdapterConfigMaxTokens)
		r.AdapterConfig["max_tokens"] = b
	}
	if patch.Policy != nil {
		setExtraRaw(r, "policy", *patch.Policy)
	}
	if patch.Provider != nil {
		setExtraRaw(r, "provider", *patch.Provider)
	}
	if patch.InstructionsFile != nil {
		if *patch.InstructionsFile == "" {
			delete(r.Extra, "instructions_file")
		} else {
			b, _ := json.Marshal(*patch.InstructionsFile)
			if r.Extra == nil {
				r.Extra = map[string]json.RawMessage{}
			}
			r.Extra["instructions_file"] = b
		}
	}
	return r, nil
}

// setExtraNumber writes an integer field into r.Extra under key, or does
// nothing if v is nil (meaning "leave unchanged" — see RolePatch's own
// doc comment on why a present-but-zero value is intentionally
// indistinguishable from absent for these three fields today).
func setExtraNumber(r *Role, key string, v *int) {
	if v == nil {
		return
	}
	b, _ := json.Marshal(*v)
	if r.Extra == nil {
		r.Extra = map[string]json.RawMessage{}
	}
	r.Extra[key] = b
}

// setExtraNumberF is setExtraNumber for a float64 field (budget_usd).
func setExtraNumberF(r *Role, key string, v *float64) {
	if v == nil {
		return
	}
	b, _ := json.Marshal(*v)
	if r.Extra == nil {
		r.Extra = map[string]json.RawMessage{}
	}
	r.Extra[key] = b
}

// setExtraRaw writes a whole-object json.RawMessage into r.Extra under key
// (used for policy/provider, both replaced wholesale rather than deep-
// patched). A literal JSON `null` clears the key instead of storing it —
// callers send `null` to mean "remove this object entirely."
func setExtraRaw(r *Role, key string, v json.RawMessage) {
	if len(v) == 0 || string(v) == "null" {
		delete(r.Extra, key)
		return
	}
	if r.Extra == nil {
		r.Extra = map[string]json.RawMessage{}
	}
	r.Extra[key] = v
}

// SetReportsTo moves childID under newParentID (empty string means "make
// this the root role"). Rejects: an unknown child, an unknown non-empty
// parent, an edge that would create a cycle (via WouldCycle), and — when
// newParentID is empty — a second root when one already exists.
func (d *Doc) SetReportsTo(childID, newParentID string) error {
	child, idx := d.FindRole(childID)
	if idx == -1 {
		return fmt.Errorf("role not found: %s", childID)
	}
	if newParentID == "" {
		if root, ok := d.RootRole(); ok && root.ID != childID {
			return fmt.Errorf("org already has a root role (%q) — reassign or remove it before making %q the root", root.ID, childID)
		}
		child.ReportsTo = nil
		return nil
	}
	if _, pidx := d.FindRole(newParentID); pidx == -1 {
		return fmt.Errorf("role not found: %s", newParentID)
	}
	if WouldCycle(d.Roles, childID, newParentID) {
		return fmt.Errorf("circular reporting: %q already reports (directly or indirectly) to %q — this edge would create a cycle", newParentID, childID)
	}
	child.ReportsTo = strPtr(newParentID)
	return nil
}

// PromoteToRoot makes newRootID the org's root role — the org's single
// "boss" (see UpdateRole's boss-guard: type "boss" is derived from tree
// position, never patched directly). Unlike SetReportsTo(id, ""), which
// simply refuses when a different root already exists, PromoteToRoot
// performs the swap by reversing every edge along the path from the
// current root down to newRootID: each role that used to be newRootID's
// ancestor becomes, in order, its descendant instead (old root -> its
// former child on that path -> ... -> newRootID's former direct parent),
// so "make someone else the boss" is always a single clean action from the
// UI, correct at any depth, rather than a two-step reassign-then-promote
// dance (and rather than the simpler-but-wrong "just swap the two
// endpoints" version, which would mis-parent everyone strictly between old
// root and newRootID for a promotion deeper than one level). Roles outside
// that path are untouched. Also flips `type` so exactly one role ever
// reads "boss": the new root's type becomes "boss"; the old root's type is
// downgraded to "specialist" only if it was "boss" (an old root that had
// been given some other custom label keeps it, since that label was never
// the thing that made it boss — its tree position was).
func (d *Doc) PromoteToRoot(newRootID string) error {
	newRoot, idx := d.FindRole(newRootID)
	if idx == -1 {
		return fmt.Errorf("role not found: %s", newRootID)
	}
	if newRoot.ReportsTo == nil {
		return fmt.Errorf("role %q is already the org root", newRootID)
	}

	// path = [newRootID, its parent, its grandparent, ..., the old root's id].
	path := []string{newRootID}
	cur := newRoot
	for cur.ReportsTo != nil {
		parentID := *cur.ReportsTo
		path = append(path, parentID)
		parent, pidx := d.FindRole(parentID)
		if pidx == -1 {
			return fmt.Errorf("role %q has reports_to %q, which does not exist — cannot promote", cur.ID, parentID)
		}
		cur = parent
	}
	oldRootID := path[len(path)-1]

	// Reverse each edge on the path: path[i] becomes path[i+1]'s new parent.
	for i := 0; i < len(path)-1; i++ {
		r, _ := d.FindRole(path[i+1])
		r.ReportsTo = strPtr(path[i])
	}

	oldRoot, _ := d.FindRole(oldRootID)
	if oldRoot.Type == "boss" {
		oldRoot.Type = "specialist"
	}
	newRoot.ReportsTo = nil
	newRoot.Type = "boss"
	return nil
}

// SetLayout applies a partial map of role id -> canvas position/icon/color.
// Roles absent from pos keep their existing UI unchanged. Deliberately does
// not run structural Validate — a position-only change cannot break the
// tree, and this is the highest-frequency call (fired on every drag-end),
// so it must stay cheap.
func (d *Doc) SetLayout(pos map[string]RoleUI) error {
	for id, ui := range pos {
		r, idx := d.FindRole(id)
		if idx == -1 {
			continue // silently skip unknown ids — a stale/late drag-end for a role deleted mid-drag shouldn't fail the whole batch
		}
		uiCopy := ui
		r.UI = &uiCopy
	}
	return nil
}

// RemoveStrategy selects what happens to a removed role's direct reports.
type RemoveStrategy string

const (
	// Reparent (default): every direct report of the removed role inherits
	// the removed role's own ReportsTo. Always produces a valid tree except
	// for the root special-case (see RemoveRole).
	Reparent RemoveStrategy = "reparent"
	// Cascade deletes the removed role and its entire subtree. Callers
	// exposing this to an AI tool must additionally gate it behind an
	// explicit confirm — see internal/ai/chat/monoagent_tools.go.
	Cascade RemoveStrategy = "cascade"
)

// RemoveRole removes id from d using strategy, returning every role id that
// was actually removed (more than one under Cascade). Refuses to remove the
// last remaining role. Refuses to remove the root role unless it has
// exactly one direct child (which then becomes the new root) — reparenting
// a root's multiple children to nil would otherwise produce multiple roots,
// an unrepresentable "fix" for RemoveRole to silently perform.
func (d *Doc) RemoveRole(id string, strategy RemoveStrategy) ([]string, error) {
	target, idx := d.FindRole(id)
	if idx == -1 {
		return nil, fmt.Errorf("role not found: %s", id)
	}
	if len(d.Roles) == 1 {
		return nil, fmt.Errorf("cannot remove %q: an org must have at least one role", id)
	}

	isRoot := target.ReportsTo == nil
	children := d.Children(id)

	if isRoot && len(children) > 1 {
		return nil, fmt.Errorf("cannot remove root role %q: it has %d direct reports — re-parent them under a single role first, or delete the org", id, len(children))
	}

	switch strategy {
	case Cascade, "":
		if strategy == "" {
			strategy = Reparent
		}
	}

	switch strategy {
	case Reparent:
		var newParent *string
		if isRoot {
			if len(children) == 1 {
				newParent = nil // the sole child becomes the new root
			}
		} else {
			newParent = target.ReportsTo
		}
		for _, c := range children {
			c.ReportsTo = newParent
		}
		d.removeByID(id)
		return []string{id}, nil

	case Cascade:
		toRemove := d.descendantsOf(id)
		toRemove = append(toRemove, id)
		for _, rid := range toRemove {
			d.removeByID(rid)
		}
		return toRemove, nil

	default:
		return nil, fmt.Errorf("unknown remove strategy: %s", strategy)
	}
}

// descendantsOf returns every role id in id's subtree (not including id
// itself), via a simple BFS over Children.
func (d *Doc) descendantsOf(id string) []string {
	var out []string
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range d.Children(cur) {
			out = append(out, c.ID)
			queue = append(queue, c.ID)
		}
	}
	return out
}

func (d *Doc) removeByID(id string) {
	for i, r := range d.Roles {
		if r.ID == id {
			d.Roles = append(d.Roles[:i], d.Roles[i+1:]...)
			return
		}
	}
}
