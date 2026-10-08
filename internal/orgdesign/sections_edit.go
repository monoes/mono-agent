package orgdesign

import (
	"encoding/json"
	"fmt"
)

// Section editing for the Org Designer: the mutations the canvas needs on
// top of sections_mutate.go (which keeps a role in exactly one section).
// Each refuses before changing anything.

// SectionPatch is a shallow patch of a section's own settings. Nil leaves a
// field alone; the Clear flags remove an optional value.
type SectionPatch struct {
	Lead            *string
	BudgetUSD       *float64
	ClearBudget     bool
	Writes          *[]string
	MaxReworkRounds *int
	ClearMaxRework  bool
}

// UpdateSection applies patch to the named section.
func (d *Doc) UpdateSection(name string, p SectionPatch) error {
	s := d.Sections.Find(name)
	if s == nil {
		return fmt.Errorf("section not found: %s", name)
	}
	if p.BudgetUSD != nil && *p.BudgetUSD <= 0 {
		return fmt.Errorf("section %q budget must be a positive USD amount", name)
	}
	if p.MaxReworkRounds != nil && *p.MaxReworkRounds <= 0 {
		return fmt.Errorf("section %q max_rework_rounds must be a positive integer", name)
	}
	if p.Lead != nil {
		if err := d.SetSectionLead(name, *p.Lead); err != nil {
			return err
		}
	}
	switch {
	case p.ClearBudget:
		s.Budget = nil
	case p.BudgetUSD != nil:
		if s.Budget == nil {
			s.Budget = &SectionBudget{}
		}
		v := *p.BudgetUSD
		s.Budget.USD = &v
	}
	if p.Writes != nil {
		s.Writes = append([]string(nil), (*p.Writes)...)
	}
	switch {
	case p.ClearMaxRework:
		s.MaxReworkRounds = nil
	case p.MaxReworkRounds != nil:
		v := *p.MaxReworkRounds
		s.MaxReworkRounds = &v
	}
	return nil
}

// NewSectionFrom declares section `name` from the given roles, moving them
// out of any section they are in. In a plain org the first section takes
// every non-root role (a sections org has none outside a section), the given
// roles leading it first.
func (d *Doc) NewSectionFrom(name string, ids []string) error {
	if !sectionNameRe.MatchString(name) {
		return fmt.Errorf("invalid section name %q: lowercase letters, digits and '-', starting with a letter, at most 40 characters", name)
	}
	if d.Sections.Find(name) != nil {
		return fmt.Errorf("duplicate section: %s", name)
	}
	for _, id := range ids {
		if err := d.checkPlaceable(id, name, true); err != nil {
			return err
		}
	}
	members := append([]string(nil), ids...)
	if !d.SectionsEnabled() {
		for _, r := range d.Roles {
			if r.ReportsTo != nil && r.Kind != "endpoint" && !containsString(members, r.ID) {
				members = append(members, r.ID)
			}
		}
	}
	if len(members) == 0 {
		return fmt.Errorf("section %q needs at least one non-root role: select a role to put in it", name)
	}
	fix, err := d.planSectionRemoval(ids)
	if err != nil {
		return err
	}
	fix()
	s := Section{Members: members}
	if len(members) > 1 {
		s.Lead = members[0]
	}
	d.Sections = append(d.Sections, NamedSection{Name: name, Section: s})
	if d.Documents == nil {
		d.Documents = DocumentSet{} // monomind wants a documents map on a sections org, even an empty one
	}
	d.raiseAgentCap()
	return nil
}

// raiseAgentCap lifts run_config.max_concurrent_agents to the number of agent
// roles when it is lower (monomind's default is 4): on a sections org a role
// past the cap never starts and monomind refuses the definition.
func (d *Doc) raiseAgentCap() {
	agents := 0
	for _, r := range d.Roles {
		if r.Kind != "endpoint" {
			agents++
		}
	}
	limit := 4
	if raw, ok := d.RunConfig["max_concurrent_agents"]; ok {
		var v int
		if json.Unmarshal(raw, &v) != nil {
			return // not a number: monomind reports it
		}
		limit = v
	}
	if limit >= agents {
		return
	}
	if d.RunConfig == nil {
		d.RunConfig = map[string]json.RawMessage{}
	}
	d.RunConfig["max_concurrent_agents"] = json.RawMessage(fmt.Sprint(agents))
}

// crossSectionRefusal says why a reports_to link between two roles is not
// allowed: sections never message each other, work crosses as documents.
func (d *Doc) crossSectionRefusal(childID, parentID string) error {
	a, b := d.SectionOf(childID), d.SectionOf(parentID)
	if a == "" || b == "" || a == b {
		return nil
	}
	return fmt.Errorf("sections %q and %q cannot message each other directly: work crosses sections only as a typed document (draw a document edge from the producing section to the consuming one)", a, b)
}

var defaultDocumentSchema = json.RawMessage(`{"type":"object"}`)

// AddDocumentEdge makes producer's section publish docType and consumer's
// section consume it, declaring the document type when it is new.
func (d *Doc) AddDocumentEdge(producer, consumer, docType string) error {
	p, c := d.Sections.Find(producer), d.Sections.Find(consumer)
	if p == nil {
		return fmt.Errorf("section not found: %s", producer)
	}
	if c == nil {
		return fmt.Errorf("section not found: %s", consumer)
	}
	if producer == consumer {
		return fmt.Errorf("a section cannot hand a document to itself")
	}
	if !sectionNameRe.MatchString(docType) {
		return fmt.Errorf("invalid document type %q: lowercase letters, digits and '-', starting with a letter, at most 40 characters", docType)
	}
	if containsString(p.Consumes, docType) {
		return fmt.Errorf("section %q already consumes %q: a section cannot both consume and publish one type", producer, docType)
	}
	if containsString(c.Publishes, docType) {
		return fmt.Errorf("section %q already publishes %q: a section cannot both consume and publish one type", consumer, docType)
	}
	if containsString(p.Publishes, docType) && containsString(c.Consumes, docType) {
		return fmt.Errorf("%q already flows from %q to %q", docType, producer, consumer)
	}
	if !containsString(p.Publishes, docType) {
		p.Publishes = append(p.Publishes, docType)
	}
	if !containsString(c.Consumes, docType) {
		c.Consumes = append(c.Consumes, docType)
	}
	if d.Documents.Find(docType) == nil {
		d.Documents = append(d.Documents, NamedDocument{Name: docType, Document: Document{Schema: defaultDocumentSchema}})
	}
	return nil
}

// RemoveDocumentEdge undoes AddDocumentEdge. A type flows from every
// publisher to every consumer, so it removes the side that is not shared
// with another edge and refuses when both are.
func (d *Doc) RemoveDocumentEdge(producer, consumer, docType string) error {
	p, c := d.Sections.Find(producer), d.Sections.Find(consumer)
	if p == nil {
		return fmt.Errorf("section not found: %s", producer)
	}
	if c == nil {
		return fmt.Errorf("section not found: %s", consumer)
	}
	if !containsString(p.Publishes, docType) || !containsString(c.Consumes, docType) {
		return fmt.Errorf("%q does not flow from %q to %q", docType, producer, consumer)
	}
	var pubs, cons int
	for _, ns := range d.Sections {
		if containsString(ns.Publishes, docType) {
			pubs++
		}
		if containsString(ns.Consumes, docType) {
			cons++
		}
	}
	switch {
	case pubs == 1 && cons == 1:
		p.Publishes = without(p.Publishes, map[string]bool{docType: true})
		c.Consumes = without(c.Consumes, map[string]bool{docType: true})
	case pubs == 1:
		c.Consumes = without(c.Consumes, map[string]bool{docType: true})
	case cons == 1:
		p.Publishes = without(p.Publishes, map[string]bool{docType: true})
	default:
		return fmt.Errorf("%q is shared by several sections on both sides: edit the publishing or consuming section instead", docType)
	}
	if !d.documentInUse(docType) {
		if doc := d.Documents.Find(docType); doc != nil && string(doc.Schema) == string(defaultDocumentSchema) && len(doc.Extra) == 0 {
			var out DocumentSet
			for _, nd := range d.Documents {
				if nd.Name != docType {
					out = append(out, nd)
				}
			}
			d.Documents = out
		}
	}
	return nil
}

func (d *Doc) documentInUse(docType string) bool {
	for _, ns := range d.Sections {
		if containsString(ns.Publishes, docType) || containsString(ns.Consumes, docType) {
			return true
		}
	}
	return false
}
