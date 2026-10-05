package orgdesign

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Sections surface (monomind 2.24, doc/concepts/org-runtime.md §6.7). The
// key names below are monomind's own, taken from its source:
//
//	requires                      orgrt/types-sections.ts  RequiresSchema
//	sections.<name>.*             orgrt/documents/definition.ts SECTION_FIELDS
//	sections.<name>.budget.usd    orgrt/documents/section-budget*.ts
//	documents.<type>.*            orgrt/documents/definition-documents.ts KNOWN_FIELDS
//
// Every type keeps an Extra bucket for sub-keys it does not model, and
// decoding is tolerant: a known key with the wrong JSON type is kept
// verbatim in Extra instead of failing the load, because monomind's own
// validator is the one that reports a malformed section. Nothing here ever
// rejects a file monomind can read.

// Requires is the top-level `requires` capability contract.
type Requires struct {
	Sections *int                       `json:"sections,omitempty"`
	Extra    map[string]json.RawMessage `json:"-"`
}

// SectionBudget is `sections.<name>.budget`: {"usd": N} only.
type SectionBudget struct {
	USD   *float64                   `json:"usd,omitempty"`
	Extra map[string]json.RawMessage `json:"-"`
}

// SectionParallelism is `sections.<name>.parallelism`: {"max_parallel": N}.
type SectionParallelism struct {
	MaxParallel *int                       `json:"max_parallel,omitempty"`
	Extra       map[string]json.RawMessage `json:"-"`
}

// Section is one entry of the top-level `sections` map: an isolated sub-org
// with its own roster, lead, budget and single-writer paths. Role caps are
// not stored here: they are each role's own `budget_usd` (Role.Extra).
type Section struct {
	Lead            string              `json:"lead,omitempty"`
	Members         []string            `json:"members,omitzero"`
	Mode            string              `json:"mode,omitempty"`     // legacy: "execution" only, warned as no longer needed
	Requests        string              `json:"requests,omitempty"` // legacy: "via-lead" only, warned as no longer needed
	Consumes        []string            `json:"consumes,omitzero"`
	Publishes       []string            `json:"publishes,omitzero"`
	Writes          []string            `json:"writes,omitzero"`
	Budget          *SectionBudget      `json:"budget,omitempty"`
	MaxReworkRounds *int                `json:"max_rework_rounds,omitempty"`
	Parallelism     *SectionParallelism `json:"parallelism,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// NamedSection is a Section with its map key.
type NamedSection struct {
	Name string
	Section
}

// SectionSet is the `sections` object as an ordered list, so a save keeps
// the file's section order. The wire shape is the file's: an object.
type SectionSet []NamedSection

// Evidence is one `documents.<type>.evidence` entry.
type Evidence struct {
	Kind   string                     `json:"kind,omitempty"` // command | diff | document | source
	Verify string                     `json:"verify,omitempty"`
	Extra  map[string]json.RawMessage `json:"-"`
}

// Document is one `documents.<type>` contract. Schema is the JSON-Schema
// style body contract and stays opaque; fields monomind accepts but gives
// one legal value (acceptance, gates, ...) are typed as written.
type Document struct {
	Schema                 json.RawMessage `json:"schema,omitempty"`
	Evidence               []Evidence      `json:"evidence,omitzero"`
	Acceptance             string          `json:"acceptance,omitempty"`
	Owner                  json.RawMessage `json:"owner,omitempty"`
	Provisional            *bool           `json:"provisional,omitempty"`
	Gates                  json.RawMessage `json:"gates,omitempty"`
	Visibility             string          `json:"visibility,omitempty"` // consumers | org
	Confidential           *bool           `json:"confidential,omitempty"`
	OnStale                string          `json:"on_stale,omitempty"`
	Checks                 json.RawMessage `json:"checks,omitempty"`
	MaxPublishAttempts     *int            `json:"max_publish_attempts,omitempty"`
	MaxConsistencyRefusals *int            `json:"max_consistency_refusals,omitempty"`
	DeliverableFiles       json.RawMessage `json:"deliverable_files,omitempty"`

	Extra map[string]json.RawMessage `json:"-"`
}

// NamedDocument is a Document with its type name (the map key).
type NamedDocument struct {
	Name string
	Document
}

// DocumentSet is the `documents` object as an ordered list.
type DocumentSet []NamedDocument

// ---- tolerant struct codec ------------------------------------------------

// decodeTolerant fills the exported, json-tagged fields of the struct dst
// points to from the JSON object in data and returns every key it did not
// consume — unknown keys and known keys of the wrong type — for Extra.
func decodeTolerant(data []byte, dst any) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	v := reflect.ValueOf(dst).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		rv, ok := raw[name]
		if !ok {
			continue
		}
		if string(rv) == "null" {
			continue // a present null is kept as written, not turned into "absent"
		}
		fresh := reflect.New(t.Field(i).Type)
		if err := json.Unmarshal(rv, fresh.Interface()); err != nil {
			continue // wrong type: stays in raw, so in Extra
		}
		v.Field(i).Set(fresh.Elem())
		delete(raw, name)
	}
	return raw, nil
}

// encodeWithExtra marshals v (a struct value that must not itself have a
// MarshalJSON, i.e. an alias) in field order, then appends extra's keys,
// sorted. A key present in both takes extra's value.
func encodeWithExtra(v any, extra map[string]json.RawMessage) ([]byte, error) {
	base, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(extra) == 0 {
		return base, nil
	}
	keys, vals, err := objectEntries(base)
	if err != nil {
		return nil, err
	}
	var added []string
	for k, ev := range extra {
		if _, dup := vals[k]; !dup {
			added = append(added, k)
		}
		vals[k] = ev
	}
	sort.Strings(added)
	return joinObject(append(keys, added...), vals)
}

func joinObject(keys []string, vals map[string]json.RawMessage) ([]byte, error) {
	buf := []byte{'{'}
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
		buf = append(buf, vals[k]...)
	}
	return append(buf, '}'), nil
}

// objectEntries returns an object's keys in source order and its values.
func objectEntries(data []byte) ([]string, map[string]json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("not a JSON object")
	}
	var keys []string
	vals := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		if _, dup := vals[k]; !dup {
			keys = append(keys, k)
		}
		vals[k] = v
	}
	return keys, vals, nil
}

// ---- per-type codecs --------------------------------------------------------

func (r *Requires) UnmarshalJSON(b []byte) error {
	type alias Requires
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*r = Requires(a)
	return nil
}

func (r Requires) MarshalJSON() ([]byte, error) {
	type alias Requires
	return encodeWithExtra(alias(r), r.Extra)
}

func (s *SectionBudget) UnmarshalJSON(b []byte) error {
	type alias SectionBudget
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*s = SectionBudget(a)
	return nil
}

func (s SectionBudget) MarshalJSON() ([]byte, error) {
	type alias SectionBudget
	return encodeWithExtra(alias(s), s.Extra)
}

func (s *SectionParallelism) UnmarshalJSON(b []byte) error {
	type alias SectionParallelism
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*s = SectionParallelism(a)
	return nil
}

func (s SectionParallelism) MarshalJSON() ([]byte, error) {
	type alias SectionParallelism
	return encodeWithExtra(alias(s), s.Extra)
}

func (s *Section) UnmarshalJSON(b []byte) error {
	type alias Section
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*s = Section(a)
	return nil
}

func (s Section) MarshalJSON() ([]byte, error) {
	type alias Section
	return encodeWithExtra(alias(s), s.Extra)
}

func (e *Evidence) UnmarshalJSON(b []byte) error {
	type alias Evidence
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*e = Evidence(a)
	return nil
}

func (e Evidence) MarshalJSON() ([]byte, error) {
	type alias Evidence
	return encodeWithExtra(alias(e), e.Extra)
}

func (d *Document) UnmarshalJSON(b []byte) error {
	type alias Document
	var a alias
	extra, err := decodeTolerant(b, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*d = Document(a)
	return nil
}

func (d Document) MarshalJSON() ([]byte, error) {
	type alias Document
	return encodeWithExtra(alias(d), d.Extra)
}

// ---- ordered sets -----------------------------------------------------------

func (s *SectionSet) UnmarshalJSON(b []byte) error {
	keys, vals, err := objectEntries(b)
	if err != nil {
		return err
	}
	out := make(SectionSet, 0, len(keys))
	for _, k := range keys {
		var sec Section
		if !isJSONObject(vals[k]) {
			return fmt.Errorf("section %q is not an object", k)
		}
		if err := json.Unmarshal(vals[k], &sec); err != nil {
			return err // an entry that is not an object: caller keeps the whole key in Extra
		}
		out = append(out, NamedSection{Name: k, Section: sec})
	}
	*s = out
	return nil
}

func (s SectionSet) MarshalJSON() ([]byte, error) {
	keys := make([]string, len(s))
	vals := make(map[string]json.RawMessage, len(s))
	for i, ns := range s {
		b, err := json.Marshal(ns.Section)
		if err != nil {
			return nil, err
		}
		keys[i], vals[ns.Name] = ns.Name, b
	}
	return joinObject(keys, vals)
}

func (s *DocumentSet) UnmarshalJSON(b []byte) error {
	keys, vals, err := objectEntries(b)
	if err != nil {
		return err
	}
	out := make(DocumentSet, 0, len(keys))
	for _, k := range keys {
		var doc Document
		if !isJSONObject(vals[k]) {
			return fmt.Errorf("document %q is not an object", k)
		}
		if err := json.Unmarshal(vals[k], &doc); err != nil {
			return err
		}
		out = append(out, NamedDocument{Name: k, Document: doc})
	}
	*s = out
	return nil
}

func (s DocumentSet) MarshalJSON() ([]byte, error) {
	keys := make([]string, len(s))
	vals := make(map[string]json.RawMessage, len(s))
	for i, nd := range s {
		b, err := json.Marshal(nd.Document)
		if err != nil {
			return nil, err
		}
		keys[i], vals[nd.Name] = nd.Name, b
	}
	return joinObject(keys, vals)
}

// Find returns the section named name, or nil. The pointer aliases the set.
func (s SectionSet) Find(name string) *Section {
	for i := range s {
		if s[i].Name == name {
			return &s[i].Section
		}
	}
	return nil
}

// Find returns the document type named name, or nil.
func (s DocumentSet) Find(name string) *Document {
	for i := range s {
		if s[i].Name == name {
			return &s[i].Document
		}
	}
	return nil
}

func isJSONObject(b json.RawMessage) bool {
	t := strings.TrimSpace(string(b))
	return strings.HasPrefix(t, "{")
}

// malformedSectionKeys lists the sections-surface keys in raw whose value
// cannot be decoded into the typed model (a present null included, so it is
// kept as written rather than read as "absent").
func malformedSectionKeys(raw map[string]json.RawMessage) []string {
	var bad []string
	try := func(key string, dst any) {
		if v, ok := raw[key]; ok {
			if string(v) == "null" || json.Unmarshal(v, dst) != nil {
				bad = append(bad, key)
			}
		}
	}
	try("requires", new(Requires))
	try("sections", new(SectionSet))
	try("documents", new(DocumentSet))
	return bad
}
