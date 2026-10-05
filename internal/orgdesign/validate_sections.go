package orgdesign

import (
	"fmt"
	"strings"
)

// validateSections checks the structural half of monomind's sections rules
// (documents/definition.ts checkSections), the part Save must never break:
// who is in which section. Everything else (document types, budgets,
// schemas, rework rounds, ...) is monomind's `org validate`, whose output
// the app shows unchanged — this does not second-guess it. Messages follow
// monomind's wording. Only runs for a sections org (SectionsEnabled).
func validateSections(d *Doc) []string {
	if !d.SectionsEnabled() {
		return nil
	}
	var errs []string
	byID := make(map[string]*Role, len(d.Roles))
	for i := range d.Roles {
		byID[d.Roles[i].ID] = &d.Roles[i]
	}
	home := map[string]string{}
	rootID := d.sectionsRootID()
	for _, ns := range d.Sections {
		at := "sections." + ns.Name
		if !sectionNameRe.MatchString(ns.Name) {
			errs = append(errs, fmt.Sprintf("%s: %q is not a valid section name — use lowercase letters, digits and \"-\", starting with a letter, at most 40 characters", at, ns.Name))
		}
		if len(ns.Members) == 0 {
			errs = append(errs, at+".members: a section needs at least one member role — list its role ids")
		}
		// monomind only asks for a lead when the key is absent: a written
		// "lead": "" (kept in Extra) is accepted.
		if _, written := ns.Extra["lead"]; !written && ns.Lead == "" && len(ns.Members) > 1 {
			errs = append(errs, fmt.Sprintf("%s.lead: a section with %d members must name its lead — add \"lead\": one of %s", at, len(ns.Members), strings.Join(ns.Members, ", ")))
		}
		seen := map[string]bool{}
		for _, id := range ns.Members {
			if seen[id] {
				errs = append(errs, fmt.Sprintf("%s.members: role %q is listed twice — remove the duplicate", at, id))
			}
			seen[id] = true
		}
		for _, id := range ns.Roster() {
			r := byID[id]
			switch {
			case r == nil:
				errs = append(errs, fmt.Sprintf("%s: role %q does not exist", at, id))
			case r.ID == rootID:
				errs = append(errs, fmt.Sprintf("%s: role %q is the root (it reports to no one); the root is in no section — remove it from the section", at, id))
			}
			if other, dup := home[id]; dup && other != ns.Name {
				errs = append(errs, fmt.Sprintf("role %q is in sections %q and %q — a role belongs to at most one section; remove it from one", id, other, ns.Name))
			} else {
				home[id] = ns.Name
			}
		}
	}
	for _, r := range d.Roles {
		if r.ID != rootID && r.Kind != "endpoint" && home[r.ID] == "" {
			errs = append(errs, fmt.Sprintf("roles.%s: a role outside every section can only be the root — add it to a section's members or make it a lead", r.ID))
		}
	}
	return errs
}
