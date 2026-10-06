package orgdesign

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// One-section-per-non-root-role invariant (monomind's validator: "a role
// outside every section can only be the root"; the root is in no section;
// a role is in at most one section). Every mutation here and in mutate.go
// keeps it, and refuses — before changing anything — rather than leave a
// role outside every section or a section without a lead.

// sectionNameRe is monomind's NAME_RE (documents/definition-util.ts).
var sectionNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// SectionsEnabled mirrors monomind's sectionsSurface: true when at least
// one section is declared with something in it. `sections: {}`, an empty
// entry, or `documents`/`requires` alone leave the org a plain one.
func (d *Doc) SectionsEnabled() bool {
	for _, ns := range d.Sections {
		s := ns.Section
		s.Extra = nil
		if len(ns.Extra) > 0 || !reflect.DeepEqual(s, Section{}) {
			return true
		}
	}
	return false
}

// sectionsRootID is the root as monomind's sections checks see it
// (documents/definition.ts): the first role of type "boss", else the first
// role with no reports_to. Validate also requires "boss" to be the single
// reports_to-null role, so on a valid org this is the same role everywhere.
func (d *Doc) sectionsRootID() string {
	for _, r := range d.Roles {
		if r.Type == "boss" {
			return r.ID
		}
	}
	for _, r := range d.Roles {
		if r.ReportsTo == nil {
			return r.ID
		}
	}
	return ""
}

func (d *Doc) isSectionsRoot(id string) bool { return id != "" && id == d.sectionsRootID() }

// Roster is a section's members plus its lead when that is a dedicated lead
// outside Members; each role once, members first.
func (s *Section) Roster() []string {
	out := append([]string(nil), s.Members...)
	if s.Lead != "" && !containsString(s.Members, s.Lead) {
		out = append(out, s.Lead)
	}
	return out
}

// SectionOf is the name of the section id belongs to (member or lead), or
// "" when it is in none (the root, an endpoint, or any role of a plain org).
func (d *Doc) SectionOf(id string) string {
	for i := range d.Sections {
		if containsString(d.Sections[i].Roster(), id) {
			return d.Sections[i].Name
		}
	}
	return ""
}

// LeadOf is the section's lead: the declared lead, else its first member.
func (s *Section) LeadOf() string {
	if s.Lead != "" {
		return s.Lead
	}
	if len(s.Members) > 0 {
		return s.Members[0]
	}
	return ""
}

// RoleBudgetUSD is the role's own USD cap (`budget_usd`, kept in Extra), the
// figure monomind sums against its section's `budget.usd`.
func RoleBudgetUSD(r *Role) (float64, bool) {
	raw, ok := r.Extra["budget_usd"]
	if !ok {
		return 0, false
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	return v, true
}

func containsString(ss []string, s string) bool { return indexOf(ss, s) != -1 }

func without(ss []string, drop map[string]bool) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

// AddRoleToSection is AddRole for a sections org with the section named: the
// new role becomes its member. AddRole alone places a new role in its
// parent's section and refuses when the parent is in none (the root).
func (d *Doc) AddRoleToSection(r Role, section string, restrictFileWrite ...bool) (*Role, error) {
	if section == "" {
		return nil, fmt.Errorf("section is required")
	}
	return d.addRole(r, section, len(restrictFileWrite) > 0 && restrictFileWrite[0])
}

// sectionForNewRole is the section a role about to be added joins ("" for
// none), or an error when a sections org has no valid place for it.
func (d *Doc) sectionForNewRole(r *Role, explicit string) (string, error) {
	if explicit != "" {
		if d.Sections.Find(explicit) == nil {
			return "", fmt.Errorf("section not found: %s", explicit)
		}
		if r.ReportsTo == nil {
			return "", fmt.Errorf("role %q is a root: the root is in no section", r.ID)
		}
		if ps := d.SectionOf(*r.ReportsTo); ps != "" && ps != explicit {
			return "", fmt.Errorf("role %q would join section %q but report into section %q: sections cannot message each other directly", r.ID, explicit, ps)
		}
		return explicit, nil
	}
	if !d.SectionsEnabled() || r.ReportsTo == nil || r.Kind == "endpoint" {
		return "", nil
	}
	if s := d.SectionOf(*r.ReportsTo); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("role %q reports to %q, which is in no section (the root is): name the section it joins (AddRoleToSection)", r.ID, *r.ReportsTo)
}

// joinSection adds id to the section's members. A section with a single
// member and no declared lead leads itself; before a second member joins,
// that lead is written down, as monomind then requires.
func (d *Doc) joinSection(section, id string) {
	s := d.Sections.Find(section)
	if s == nil || containsString(s.Members, id) {
		return
	}
	if s.Lead == "" && len(s.Members) == 1 {
		s.Lead = s.Members[0]
	}
	s.Members = append(s.Members, id)
}

// AddSection declares a new section. Its roster must name existing,
// non-root roles that are in no other section; the first member leads when
// no lead is given and there are several.
func (d *Doc) AddSection(name string, s Section) error {
	if !sectionNameRe.MatchString(name) {
		return fmt.Errorf("invalid section name %q: lowercase letters, digits and '-', starting with a letter, at most 40 characters", name)
	}
	if d.Sections.Find(name) != nil {
		return fmt.Errorf("duplicate section: %s", name)
	}
	if len(s.Members) == 0 {
		return fmt.Errorf("section %q needs at least one member role", name)
	}
	if s.Lead == "" && len(s.Members) > 1 {
		s.Lead = s.Members[0]
	}
	for _, id := range s.Roster() {
		if err := d.checkPlaceable(id, "", false); err != nil {
			return err
		}
	}
	d.Sections = append(d.Sections, NamedSection{Name: name, Section: s})
	return nil
}

// checkPlaceable reports why role id cannot join a section: unknown, the
// root, or (unless move) already in another section than `into`.
func (d *Doc) checkPlaceable(id, into string, move bool) error {
	if _, idx := d.FindRole(id); idx == -1 {
		return fmt.Errorf("role not found: %s", id)
	}
	if d.isSectionsRoot(id) {
		return fmt.Errorf("role %q is the root: the root is in no section", id)
	}
	if cur := d.SectionOf(id); !move && cur != "" && cur != into {
		return fmt.Errorf("role %q is already in section %q: a role belongs to at most one section", id, cur)
	}
	return nil
}

// DeleteSection removes a section. Its roles must go somewhere: moveTo names
// the section that takes them (required while it has any). Deleting the only
// section leaves a plain org and needs no moveTo. Its publishes/consumes
// edges go with it; documents stay declared.
func (d *Doc) DeleteSection(name, moveTo string) error {
	s := d.Sections.Find(name)
	if s == nil {
		return fmt.Errorf("section not found: %s", name)
	}
	roster := s.Roster()
	if len(d.Sections) > 1 && len(roster) > 0 {
		if moveTo == "" || moveTo == name {
			return fmt.Errorf("section %q has roles (%s): name another section to move them to", name, strings.Join(roster, ", "))
		}
		if d.Sections.Find(moveTo) == nil {
			return fmt.Errorf("section not found: %s", moveTo)
		}
	}
	var out SectionSet
	for _, ns := range d.Sections {
		if ns.Name != name {
			out = append(out, ns)
		}
	}
	d.Sections = out
	if len(out) > 0 && len(roster) > 0 {
		for _, id := range roster {
			d.joinSection(moveTo, id)
			d.reportToLead(id, moveTo)
		}
	}
	return nil
}

// AssignRole moves a non-root role into the named section. A lead leaving
// its section is replaced there (see electLead); a section never left empty.
func (d *Doc) AssignRole(id, section string) error {
	if d.Sections.Find(section) == nil {
		return fmt.Errorf("section not found: %s", section)
	}
	if err := d.checkPlaceable(id, section, true); err != nil {
		return err
	}
	if d.SectionOf(id) == section {
		return nil
	}
	fix, err := d.planSectionRemoval([]string{id})
	if err != nil {
		return err
	}
	fix()
	d.joinSection(section, id)
	d.reportToLead(id, section)
	return nil
}

// reportToLead points a role that just moved into section at that section's
// lead, as monomind advises, so it keeps no link into the section it left.
// Left alone when it already reports inside the section or it would loop.
func (d *Doc) reportToLead(id, section string) {
	s := d.Sections.Find(section)
	r, _ := d.FindRole(id)
	lead := s.LeadOf()
	if r == nil || lead == "" || lead == id || (r.ReportsTo != nil && d.SectionOf(*r.ReportsTo) == section) {
		return
	}
	if WouldCycle(d.Roles, id, lead) {
		return
	}
	r.ReportsTo = &lead
}

// SetSectionLead makes id the section's lead. id must be a member, or a
// non-root role in no section (it then leads without being a member).
func (d *Doc) SetSectionLead(section, id string) error {
	s := d.Sections.Find(section)
	if s == nil {
		return fmt.Errorf("section not found: %s", section)
	}
	if err := d.checkPlaceable(id, section, false); err != nil {
		return err
	}
	s.Lead = id
	return nil
}

// planSectionRemoval validates taking the roles in gone out of their
// sections and returns the function that applies it. Every section losing
// roles keeps at least one member; a lost lead is replaced by electLead.
func (d *Doc) planSectionRemoval(gone []string) (func(), error) {
	drop := map[string]bool{}
	for _, id := range gone {
		drop[id] = true
	}
	type edit struct {
		s       *Section
		members []string
		lead    string
	}
	var edits []edit
	for i := range d.Sections {
		s := &d.Sections[i].Section
		hit := false
		for _, id := range s.Roster() {
			hit = hit || drop[id]
		}
		if !hit {
			continue
		}
		members := without(s.Members, drop)
		if len(members) == 0 {
			return nil, fmt.Errorf("section %q would be left without a member: delete it first (DeleteSection) or keep a role in it", d.Sections[i].Name)
		}
		lead := s.Lead
		if drop[lead] {
			lead = d.electLead(members, lead)
		}
		edits = append(edits, edit{s, members, lead})
	}
	return func() {
		for _, e := range edits {
			e.s.Members, e.s.Lead = e.members, e.lead
		}
	}, nil
}

// electLead picks the role that takes over a lead that is leaving: a
// remaining member that reported to it, else the first remaining member.
func (d *Doc) electLead(members []string, leaving string) string {
	for _, m := range members {
		if r, _ := d.FindRole(m); r != nil && r.ReportsTo != nil && *r.ReportsTo == leaving {
			return m
		}
	}
	return members[0]
}

// sectionsForRemoval is RemoveRole's hook: it vets removing id under
// strategy and returns what to run once the roles are gone.
func (d *Doc) sectionsForRemoval(id string, strategy RemoveStrategy) (func(), error) {
	noop := func() {}
	if len(d.Sections) == 0 {
		return noop, nil
	}
	gone := []string{id}
	if strategy == Cascade {
		gone = append(d.descendantsOf(id), id)
	}
	if d.SectionsEnabled() {
		if r, _ := d.FindRole(id); r != nil && d.isSectionsRoot(id) {
			return nil, fmt.Errorf("cannot remove root role %q of a sections org: its successor would be a section member, and the root is in no section — restructure the sections first", id)
		}
	}
	return d.planSectionRemoval(gone)
}

// checkReparentInSections vets SetReportsTo for a sections org.
func (d *Doc) checkReparentInSections(child *Role, newParentID string) error {
	if !d.SectionsEnabled() || child.Kind == "endpoint" {
		return nil
	}
	if newParentID != "" {
		if err := d.crossSectionRefusal(child.ID, newParentID); err != nil {
			return err
		}
	}
	in := d.SectionOf(child.ID)
	switch {
	case newParentID == "" && in != "":
		return fmt.Errorf("role %q is in section %q: the root is in no section — remove it from the section first", child.ID, in)
	case newParentID != "" && (child.ReportsTo == nil || d.isSectionsRoot(child.ID)) && in == "":
		return fmt.Errorf("role %q is the root: placing it under %q would leave it in no section — assign it to a section (AssignRole) first", child.ID, newParentID)
	}
	return nil
}
