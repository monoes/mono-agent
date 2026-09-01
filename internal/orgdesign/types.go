// Package orgdesign reads, mutates, and validates monomind Org Runtime v2
// config files (<profile>/.monomind/orgs/<name>.json) directly — there is no
// `monomind org` subcommand for editing an existing org's roles or hierarchy
// (only a template-scaffolding `create`), so this package is the only
// mutation path available to the app's org designer canvas and to the AI
// chat tool surface.
//
// Round-trip fidelity is the central design constraint: monomind's own
// OrgDefSchema and RoleSchema (packages/@monomind/cli/src/orgrt/types.ts)
// are both Zod `.passthrough()` schemas, meaning real org configs routinely
// carry fields this package doesn't model (policy, adapter_config, provider,
// run_config internals, instructions_file, fence, ...). A save must never
// drop a field it doesn't understand, so every type here keeps an `Extra`
// bucket of raw JSON for anything not explicitly modeled, and custom
// (Un)MarshalJSON implementations merge it back in on write.
package orgdesign

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Doc is one org config file's contents.
type Doc struct {
	Name      string                     `json:"name"`
	Goal      string                     `json:"goal"`
	Status    string                     `json:"status"`
	Schedule  json.RawMessage            `json:"schedule"` // string | number | null — never typed
	RunConfig map[string]json.RawMessage `json:"run_config,omitempty"`
	Roles     []Role                     `json:"roles"`
	Runtime   string                     `json:"runtime,omitempty"`

	// Extra holds every top-level key not modeled above, verbatim, so a
	// save never drops something monomind or a hand edit put there (e.g.
	// `fence`).
	Extra map[string]json.RawMessage `json:"-"`
}

// docKnownKeys lists the JSON keys handled by named Doc fields, used by
// UnmarshalJSON to compute Extra and by MarshalJSON to avoid emitting a key
// twice.
var docKnownKeys = map[string]bool{
	"name": true, "goal": true, "status": true, "schedule": true,
	"run_config": true, "roles": true, "runtime": true,
}

// RoleUI is the org designer canvas's own per-role state — never read or
// written by monomind itself, but safe to store because RoleSchema is
// `.passthrough()` (types.ts) and migrateOrgConfig spreads roles with
// `{...role}` (migrate.ts), so it survives both a monomind parse and an
// `org migrate` untouched.
type RoleUI struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Icon  string  `json:"icon,omitempty"`  // opaque id into agent-avatars.json
	Color string  `json:"color,omitempty"` // optional user override
}

// Role is one org role.
type Role struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
	// ReportsTo is nil for the (exactly one) root role — this must marshal
	// to JSON `null`, not be omitted, so no `omitempty` here.
	ReportsTo        *string                    `json:"reports_to"`
	Responsibilities []string                   `json:"responsibilities"`
	AdapterConfig    map[string]json.RawMessage `json:"adapter_config,omitempty"`
	UI               *RoleUI                    `json:"ui,omitempty"`

	// Extra holds every role-level key not modeled above (policy, provider,
	// instructions_file, runtime, max_turns_per_message, budget_tokens,
	// budget_usd, ...) verbatim.
	Extra map[string]json.RawMessage `json:"-"`
}

var roleKnownKeys = map[string]bool{
	"id": true, "title": true, "type": true, "reports_to": true,
	"responsibilities": true, "adapter_config": true, "ui": true,
}

// UnmarshalJSON decodes a Doc, routing every key not in docKnownKeys into
// Extra so it round-trips on the next Save.
func (d *Doc) UnmarshalJSON(data []byte) error {
	type alias Doc
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	extra := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if !docKnownKeys[k] {
			extra[k] = v
		}
	}
	a.Extra = extra
	*d = Doc(a)
	return nil
}

// MarshalJSON encodes a Doc, merging Extra back in alongside the modeled
// fields. Key order for the modeled fields matches the field declaration
// order; Extra keys are sorted for deterministic output.
func (d Doc) MarshalJSON() ([]byte, error) {
	type alias Doc
	base, err := json.Marshal(alias(d))
	if err != nil {
		return nil, err
	}
	if len(d.Extra) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range d.Extra {
		m[k] = v
	}
	return marshalOrderedMap(m)
}

// UnmarshalJSON decodes a Role, routing every key not in roleKnownKeys into
// Extra.
func (r *Role) UnmarshalJSON(data []byte) error {
	type alias Role
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	extra := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if !roleKnownKeys[k] {
			extra[k] = v
		}
	}
	a.Extra = extra
	*r = Role(a)
	return nil
}

// MarshalJSON encodes a Role, merging Extra back in.
func (r Role) MarshalJSON() ([]byte, error) {
	type alias Role
	base, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		m[k] = v
	}
	return marshalOrderedMap(m)
}

// marshalOrderedMap marshals a map[string]json.RawMessage with sorted keys,
// since Go's own map marshaling already sorts string keys but we want that
// behavior guaranteed (not an implementation detail) and documented here as
// the one place Doc/Role key order can diverge from the source file's
// original order — acceptable because the roles ARRAY (where order matters
// for canvas stability) is a slice, preserved exactly as decoded.
func marshalOrderedMap(m map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	buf := make([]byte, 0, 256)
	buf = append(buf, '{')
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf = append(buf, kb...)
		buf = append(buf, ':')
		buf = append(buf, m[k]...)
	}
	buf = append(buf, '}')
	return buf, nil
}

// FindRole returns a pointer to the role with the given id within d.Roles,
// and its index, or (nil, -1) if not found. The pointer aliases the slice
// element, so mutating through it mutates d.
func (d *Doc) FindRole(id string) (*Role, int) {
	for i := range d.Roles {
		if d.Roles[i].ID == id {
			return &d.Roles[i], i
		}
	}
	return nil, -1
}

// RootRole returns the role with ReportsTo == nil, and true if exactly one
// exists. If zero or more than one exist, returns (nil, false) — callers
// needing the specific violation should use Validate instead.
func (d *Doc) RootRole() (*Role, bool) {
	var found *Role
	count := 0
	for i := range d.Roles {
		if d.Roles[i].ReportsTo == nil {
			count++
			found = &d.Roles[i]
		}
	}
	if count != 1 {
		return nil, false
	}
	return found, true
}

// Children returns every role directly reporting to parentID.
func (d *Doc) Children(parentID string) []*Role {
	var out []*Role
	for i := range d.Roles {
		if d.Roles[i].ReportsTo != nil && *d.Roles[i].ReportsTo == parentID {
			out = append(out, &d.Roles[i])
		}
	}
	return out
}

// strPtr is a small helper for constructing *string literals (Go has no
// address-of-literal syntax), used throughout this package and its callers.
func strPtr(s string) *string { return &s }

// reportsToLabel renders a role's ReportsTo for error messages ("null" vs a
// quoted id).
func reportsToLabel(r *string) string {
	if r == nil {
		return "null"
	}
	return fmt.Sprintf("%q", *r)
}
