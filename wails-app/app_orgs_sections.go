package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/orgdesign"
)

// Org Designer — sections (monomind org-runtime §6.7). Thin bindings over
// the orgdesign section mutators: every rule (one section per role, a lead
// per section, no direct cross-section message link) lives there, and every
// save goes through saveAndRespond, i.e. the same Go validation, `org
// validate` check and rollback as the role mutators.

// mutateOrgDoc loads the org, applies fn and saves it.
func (a *App) mutateOrgDoc(orgName string, fn func(d *orgdesign.Doc) error) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	if err := fn(d); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}

// AddOrgSection declares a section from the given role ids (moved out of any
// section they are in). In a plain org the first section takes every
// non-root role. specJSON: {"members":["id",...]}.
func (a *App) AddOrgSection(orgName, name, specJSON string) string {
	var spec struct {
		Members []string `json:"members"`
	}
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return aiError(fmt.Errorf("invalid section: %w", err))
	}
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.NewSectionFrom(name, spec.Members) })
}

// UpdateOrgSection patches a section's lead, budget_usd, writes and
// max_rework_rounds. patchJSON keys present overwrite; "budget_usd" and
// "max_rework_rounds" set to null remove the value.
func (a *App) UpdateOrgSection(orgName, name, patchJSON string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(patchJSON), &raw); err != nil {
		return aiError(fmt.Errorf("invalid patch: %w", err))
	}
	var p orgdesign.SectionPatch
	if err := optField(raw, "lead", &p.Lead, nil); err != nil {
		return aiError(err)
	}
	if err := optField(raw, "writes", &p.Writes, nil); err != nil {
		return aiError(err)
	}
	if err := optField(raw, "budget_usd", &p.BudgetUSD, &p.ClearBudget); err != nil {
		return aiError(err)
	}
	if err := optField(raw, "max_rework_rounds", &p.MaxReworkRounds, &p.ClearMaxRework); err != nil {
		return aiError(err)
	}
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.UpdateSection(name, p) })
}

// optField reads patch key: absent leaves dst alone, null sets *clear (when
// the field can be cleared), anything else decodes into dst.
func optField[T any](raw map[string]json.RawMessage, key string, dst **T, clear *bool) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if string(v) == "null" {
		if clear == nil {
			return fmt.Errorf("%s: cannot be null", key)
		}
		*clear = true
		return nil
	}
	var t T
	if err := json.Unmarshal(v, &t); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = &t
	return nil
}

// DeleteOrgSection removes a section; its roles move to moveTo.
func (a *App) DeleteOrgSection(orgName, name, moveTo string) string {
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.DeleteSection(name, moveTo) })
}

// AssignOrgRole moves a non-root role into a section (membership change).
func (a *App) AssignOrgRole(orgName, roleID, section string) string {
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.AssignRole(roleID, section) })
}

// AddOrgRoleToSection is AddOrgRole for a role that joins the named section.
func (a *App) AddOrgRoleToSection(orgName, section, roleJSON string) string {
	var r orgdesign.Role
	if err := json.Unmarshal([]byte(roleJSON), &r); err != nil {
		return aiError(fmt.Errorf("invalid role: %w", err))
	}
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error {
		_, err := d.AddRoleToSection(r, section, a.restrictFileWriteForOrgs())
		return err
	})
}

// AddOrgDocumentEdge makes producer's section publish docType to consumer's.
func (a *App) AddOrgDocumentEdge(orgName, producer, consumer, docType string) string {
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.AddDocumentEdge(producer, consumer, docType) })
}

// RemoveOrgDocumentEdge removes a handoff drawn with AddOrgDocumentEdge.
func (a *App) RemoveOrgDocumentEdge(orgName, producer, consumer, docType string) string {
	return a.mutateOrgDoc(orgName, func(d *orgdesign.Doc) error { return d.RemoveDocumentEdge(producer, consumer, docType) })
}
