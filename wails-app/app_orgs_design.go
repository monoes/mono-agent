package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// ─────────────────────────────────────────────────────────────────────────────
// Org Designer — read/write org config files directly
//
// This file necessarily breaks app_orgs.go's doctrine ("never import
// monomind internals, every call shells out to `monoagentcli org ...`"):
// there is no `monomind org` subcommand for editing an existing org's roles
// or hierarchy (`org create` is a template scaffolder with no --file/stdin
// spec — confirmed against the CLI's own usage string), so direct JSON file
// mutation via internal/orgdesign is the only mutation path available. That
// package handles round-trip fidelity (unknown fields survive a save) and
// its own structural validation (including cycle detection, which
// monomind's own `org validate` does not perform).
//
// Every mutation here still runs `monoagentcli org validate` as an
// authoritative second check after writing — the Zod schema is the source
// of truth for anything this package's Go model doesn't know about — and
// rolls back to the pre-write bytes on failure, so a config that monomind
// itself can't load is never left on disk (see saveAndValidate).
// ─────────────────────────────────────────────────────────────────────────────

// GetOrgDesign returns one org's full design (roles, hierarchy, canvas
// layout) plus validation status, in one round trip for the canvas to load.
func (a *App) GetOrgDesign(orgName string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	valid, errs := a.validateDoc(d)
	b, err := json.Marshal(map[string]interface{}{
		"v":      1,
		"org":    d,
		"valid":  valid,
		"errors": errs,
	})
	if err != nil {
		return aiError(err)
	}
	return string(b)
}

// ListOrgDesigns lists every org config file under the active profile's
// orgs directory — distinct from ListOrgs (app_orgs.go), which proxies
// `org list`'s runtime state: this reads config files directly, so it
// includes designs that have never been run.
func (a *App) ListOrgDesigns() string {
	root := a.orgDesignRoot()
	names, err := orgdesign.ListOrgNames(root)
	if err != nil {
		return aiError(err)
	}
	type item struct {
		Name      string `json:"name"`
		Goal      string `json:"goal"`
		Status    string `json:"status"`
		RoleCount int    `json:"roleCount"`
		// Kind is "holding" for a holding org (plan §6.1), else "" or
		// "standard" — the org list uses it to offer the group view.
		Kind string `json:"kind,omitempty"`
	}
	items := make([]item, 0, len(names))
	for _, name := range names {
		d, err := orgdesign.Load(root, name)
		if err != nil {
			continue // skip a config that fails to parse rather than failing the whole list
		}
		items = append(items, item{Name: d.Name, Goal: d.Goal, Status: d.Status, RoleCount: len(d.Roles), Kind: orgDocKind(d)})
	}
	b, err := json.Marshal(map[string]interface{}{"v": 1, "items": items})
	if err != nil {
		return aiError(err)
	}
	return string(b)
}

// orgDocKind reads the top-level `kind` key from a doc's JSON form, so it
// works whether orgdesign keeps `kind` in Extra or models it as a field.
func orgDocKind(d *orgdesign.Doc) string {
	b, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(b, &probe) != nil {
		return ""
	}
	return probe.Kind
}

// CreateOrgDesign creates a new org with a single root role. specJSON:
// {"name","goal","schedule"?,"runtime"?,"workspace"?,"rootRoleId"?,"rootRoleTitle"?}
func (a *App) CreateOrgDesign(specJSON string) string {
	var spec struct {
		Name          string `json:"name"`
		Goal          string `json:"goal"`
		Schedule      string `json:"schedule"`
		Runtime       string `json:"runtime"`
		Workspace     string `json:"workspace"`
		RootRoleID    string `json:"rootRoleId"`
		RootRoleTitle string `json:"rootRoleTitle"`
	}
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return aiError(fmt.Errorf("invalid spec: %w", err))
	}
	root := a.orgDesignRoot()
	if _, err := orgdesign.Load(root, spec.Name); err == nil {
		return aiError(fmt.Errorf("an org named %q already exists", spec.Name))
	}
	var scheduleRaw json.RawMessage
	if spec.Schedule != "" {
		scheduleRaw, _ = json.Marshal(spec.Schedule)
	}
	d := orgdesign.NewOrg(spec.Name, spec.Goal, orgdesign.NewOrgOptions{
		Schedule:          scheduleRaw,
		Runtime:           spec.Runtime,
		Workspace:         spec.Workspace,
		RootRoleID:        spec.RootRoleID,
		RootRoleTitle:     spec.RootRoleTitle,
		RestrictFileWrite: a.restrictFileWriteForOrgs(),
	})
	return a.saveAndRespond(root, d, "ui")
}

// DeleteOrgDesign removes an org's config file only — its run-data
// subdirectory (bus.jsonl, checkpoints, sentinels) is left in place; a full
// teardown belongs to `monomind org delete` via the existing app_orgs.go
// surface, not this method.
func (a *App) DeleteOrgDesign(orgName string) string {
	root := a.orgDesignRoot()
	if err := orgdesign.Delete(root, orgName); err != nil {
		return aiError(err)
	}
	a.emitOrgDesignUpdated(orgName, "ui", true, nil, true, nil)
	return `{"ok":true}`
}

// AddOrgRole adds a role to an org. roleJSON is a orgdesign.Role literal
// (id may be omitted to auto-derive one from title).
func (a *App) AddOrgRole(orgName, roleJSON string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	var r orgdesign.Role
	if err := json.Unmarshal([]byte(roleJSON), &r); err != nil {
		return aiError(fmt.Errorf("invalid role: %w", err))
	}
	if _, err := d.AddRole(r, a.restrictFileWriteForOrgs()); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}

// UpdateOrgRole applies a shallow patch to an existing role. patchJSON keys
// present overwrite; keys absent are left unchanged.
func (a *App) UpdateOrgRole(orgName, roleID, patchJSON string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	var raw struct {
		Title                  *string          `json:"title"`
		Type                   *string          `json:"type"`
		Responsibilities       *[]string        `json:"responsibilities"`
		Runtime                *string          `json:"runtime"`
		Model                  *string          `json:"model"`
		Icon                   *string          `json:"icon"`
		Color                  *string          `json:"color"`
		MaxTurnsPerMessage     *int             `json:"max_turns_per_message"`
		BudgetTokens           *int             `json:"budget_tokens"`
		BudgetUSD              *float64         `json:"budget_usd"`
		Policy                 *json.RawMessage `json:"policy"`
		Provider               *json.RawMessage `json:"provider"`
		InstructionsFile       *string          `json:"instructions_file"`
		AdapterConfigProvider  *string          `json:"adapter_config_provider"`
		AdapterConfigMaxTokens *int             `json:"adapter_config_max_tokens"`
	}
	if err := json.Unmarshal([]byte(patchJSON), &raw); err != nil {
		return aiError(fmt.Errorf("invalid patch: %w", err))
	}
	patch := orgdesign.RolePatch{
		Title: raw.Title, Type: raw.Type, Responsibilities: raw.Responsibilities,
		Runtime: raw.Runtime, Model: raw.Model, Icon: raw.Icon, Color: raw.Color,
		MaxTurnsPerMessage: raw.MaxTurnsPerMessage, BudgetTokens: raw.BudgetTokens, BudgetUSD: raw.BudgetUSD,
		Policy: raw.Policy, Provider: raw.Provider, InstructionsFile: raw.InstructionsFile,
		AdapterConfigProvider: raw.AdapterConfigProvider, AdapterConfigMaxTokens: raw.AdapterConfigMaxTokens,
	}
	if _, err := d.UpdateRole(roleID, patch); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}

// PromoteRoleToRoot makes roleID the org's root role ("boss"), reversing the
// path from the current root down to roleID so the swap is correct at any
// depth — see orgdesign.Doc.PromoteToRoot. Distinct from
// SetOrgRoleReportsTo(roleID, ""), which just refuses when a different root
// already exists; this is the one-click "set as org boss" action.
func (a *App) PromoteRoleToRoot(orgName, roleID string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	if err := d.PromoteToRoot(roleID); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}

// ChooseInstructionsFile opens a native file picker for a role's optional
// instructions_file path (markdown or plain text). Returns the chosen
// absolute path, or "" if the user cancelled.
func (a *App) ChooseInstructionsFile() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose Instructions File",
		Filters: []runtime.FileFilter{
			{DisplayName: "Markdown", Pattern: "*.md"},
			{DisplayName: "Text", Pattern: "*.txt"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

// RemoveOrgRole removes a role. strategy is "reparent" (default — direct
// reports inherit the removed role's own manager) or "cascade" (the role
// and its whole subtree).
func (a *App) RemoveOrgRole(orgName, roleID, strategy string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	s := orgdesign.RemoveStrategy(strategy)
	if s == "" {
		s = orgdesign.Reparent
	}
	removed, err := d.RemoveRole(roleID, s)
	if err != nil {
		return aiError(err)
	}
	sha, cliErr := a.saveOrgDoc(root, d)
	if cliErr != nil {
		return aiError(cliErr)
	}
	a.emitOrgDesignUpdated(orgName, "ui", false, d, true, nil)
	b, _ := json.Marshal(map[string]interface{}{"ok": true, "rev": sha, "deletedIds": removed})
	return string(b)
}

// SetOrgRoleReportsTo moves roleID under parentID. parentID == "" makes
// roleID the root role (only allowed when the org has no other root).
func (a *App) SetOrgRoleReportsTo(orgName, roleID, parentID string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	if err := d.SetReportsTo(roleID, parentID); err != nil {
		return aiError(err)
	}
	return a.saveAndRespond(root, d, "ui")
}

// SaveOrgLayout persists canvas positions/icons only. layoutJSON:
// {"<roleId>":{"x":120,"y":40,"icon":"coder","color":"#5b8"}, ...}. Fires on
// every drag-end, so it deliberately skips the CLI validate round-trip
// (position can't break structural validity) — see orgdesign.SetLayout.
func (a *App) SaveOrgLayout(orgName, layoutJSON string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	var pos map[string]orgdesign.RoleUI
	if err := json.Unmarshal([]byte(layoutJSON), &pos); err != nil {
		return aiError(fmt.Errorf("invalid layout: %w", err))
	}
	_ = d.SetLayout(pos)
	sha, err := orgdesign.Save(root, d)
	if err != nil {
		return aiError(err)
	}
	if a.orgWatcher != nil {
		a.orgWatcher.MarkSelfWrite(orgName, sha)
	}
	a.emitOrgDesignUpdated(orgName, "ui", false, d, true, nil)
	b, _ := json.Marshal(map[string]interface{}{"ok": true, "rev": sha})
	return string(b)
}

// SaveOrgDesign replaces the whole document from the canvas (used for bulk
// operations like "tidy layout" or an initial import) — still runs the same
// validate/CLI-check/rollback path as every other mutator.
func (a *App) SaveOrgDesign(orgName, docJSON string) string {
	root := a.orgDesignRoot()
	var d orgdesign.Doc
	if err := json.Unmarshal([]byte(docJSON), &d); err != nil {
		return aiError(fmt.Errorf("invalid org document: %w", err))
	}
	if d.Name == "" {
		d.Name = orgName
	}
	return a.saveAndRespond(root, &d, "ui")
}

// ValidateOrgDesign checks an org's design without writing anything.
func (a *App) ValidateOrgDesign(orgName string) string {
	root := a.orgDesignRoot()
	d, err := orgdesign.Load(root, orgName)
	if err != nil {
		return aiError(err)
	}
	valid, errs := a.validateDoc(d)
	b, _ := json.Marshal(map[string]interface{}{"valid": valid, "errors": errs})
	return string(b)
}

// ReloadOrg tells a running org's daemon to pick up config changes it
// wouldn't otherwise notice (the daemon polls for a `reload` sentinel file,
// not the config's mtime).
func (a *App) ReloadOrg(orgName string) string {
	root := a.orgDesignRoot()
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	args := []string{"org", "reload", orgName}
	if root != "" {
		args = append(args, "--project", root)
	}
	cmd := exec.Command(cliBin, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return aiError(fmt.Errorf("%s", strings.TrimSpace(string(out))))
	}
	return string(out)
}

// ── internals ─────────────────────────────────────────────────────────────

// orgDesignRoot resolves the active profile's project root the same way
// app_orgs.go's orgProjectRoot does, so org design reads/writes and the
// existing `monoagentcli org` observe/action surface agree on where an
// org's files live. Returns "" (meaning: use the CLI's own default project
// root, `~/.monoagent`) when there's no usable profile — orgdesign.OrgsDir
// handles the join either way via the same call site pattern as
// orgsDirForActiveProfile.
func (a *App) orgDesignRoot() string {
	return a.orgProjectRoot()
}

// saveAndRespond validates+saves d, runs the CLI validate check, and on
// success emits the live-update event and returns {"ok":true,"rev":sha,
// "org":<fresh doc>}. On any failure it returns the aiError envelope.
func (a *App) saveAndRespond(root string, d *orgdesign.Doc, origin string) string {
	sha, err := a.saveOrgDoc(root, d)
	if err != nil {
		return aiError(err)
	}
	a.emitOrgDesignUpdated(d.Name, origin, false, d, true, nil)
	b, _ := json.Marshal(map[string]interface{}{"ok": true, "rev": sha, "org": d})
	return string(b)
}

// saveOrgDoc runs orgdesign's own Go-side Validate (inside Save), writes
// atomically, registers the write with the watcher so it doesn't
// re-announce our own change, then runs `monoagentcli org validate` as the
// authoritative second check against monomind's real Zod schema. On CLI
// validation failure, restores the pre-write bytes so an org config
// monomind itself can't load is never left on disk, and returns the CLI's
// error text.
//
// The CLI check deliberately runs BEFORE reconcileOrgDoc: reconcile revokes
// the grant rows a document no longer backs, and rollback can only restore
// the FILE. Reconciling first would mean a rejected save leaves the pre-image
// on disk — still listing the role and its `automations` display copies —
// while the rows behind them are gone for good, so the role silently loses
// its tools at runtime with no error anywhere.
func (a *App) saveOrgDoc(root string, d *orgdesign.Doc) (sha string, err error) {
	var preImage *orgdesign.Doc
	if existing, loadErr := orgdesign.Load(root, d.Name); loadErr == nil {
		preImage = existing
	}

	if _, err := a.writeOrgDoc(root, d); err != nil {
		return "", err
	}
	if cliErr := a.cliValidate(root, d.Name); cliErr != nil {
		a.rollbackOrgDoc(root, d.Name, preImage)
		return "", fmt.Errorf("monomind rejected this change: %s", cliErr.Error())
	}

	// Only now touch rows — and write the document again, since reconcile
	// rewrites the display copies it just matched against them.
	if err := a.reconcileOrgDoc(root, d, preImage == nil); err != nil {
		a.rollbackOrgDoc(root, d.Name, preImage)
		return "", err
	}
	return a.writeOrgDoc(root, d)
}

// writeOrgDoc saves d and registers the write with the watcher so it doesn't
// re-announce our own change as an external edit.
func (a *App) writeOrgDoc(root string, d *orgdesign.Doc) (string, error) {
	sha, err := orgdesign.Save(root, d)
	if err != nil {
		return "", err
	}
	if a.orgWatcher != nil {
		a.orgWatcher.MarkSelfWrite(d.Name, sha)
	}
	return sha, nil
}

// rollbackOrgDoc restores the pre-write bytes — never leave a config on disk
// that monomind's own schema rejects, even transiently. A save that created
// the file (no pre-image) removes it instead.
func (a *App) rollbackOrgDoc(root, name string, preImage *orgdesign.Doc) {
	if preImage == nil {
		_ = orgdesign.Delete(root, name)
		return
	}
	if sha, err := orgdesign.Save(root, preImage); err == nil && a.orgWatcher != nil {
		a.orgWatcher.MarkSelfWrite(name, sha)
	}
}

// reconcileOrgDoc brings a full-document save from the canvas into line with
// the grant, endpoint, and autonomy rows before it reaches disk, exactly as
// every CLI save does: a stale or hand-edited document can never carry
// grants, tool providers, or an autonomy level no row backs (C-3, C-54).
// A new org gets its starting autonomy row (mid, Q8).
func (a *App) reconcileOrgDoc(root string, d *orgdesign.Doc, isNew bool) error {
	if a.db == nil {
		return nil
	}
	profileID := a.getActiveProfileID()
	if profileID == "" || profiledir.Root(a.db, profileID) != root {
		return nil
	}
	ctx := context.Background()
	store := orgdecide.NewStore(a.db)
	if isNew {
		// Same rule as cmd/monoagentcli's ensureNewOrgAutonomy: mid by
		// default (Q8), manual when the document asks for it, and the
		// document's decider policy text carried over — a document can never
		// start an org at full.
		if row, err := store.Get(ctx, profileID, d.Name); err == nil && !row.Stored {
			row.Level = orgdesign.LevelMid
			if d.Autonomy != nil && d.Autonomy.Level == orgdesign.LevelManual {
				row.Level = orgdesign.LevelManual
			}
			if d.Autonomy != nil && d.Autonomy.Policy != "" {
				row.Policy = d.Autonomy.Policy
			}
			if err := store.Put(ctx, row, "gui"); err != nil {
				return err
			}
		}
	}
	cli, _ := findMonoAgentCLI()
	apiAddr := orggrant.DefaultAPIAddr
	var v string
	if err := a.db.QueryRow(`SELECT value FROM settings WHERE key = 'daemon_api_addr'`).Scan(&v); err == nil && v != "" {
		apiAddr = v
	}
	if _, err := orggrant.Reconcile(ctx, orggrant.NewStore(a.db), d, orggrant.GenOptions{ProfileID: profileID, CLIPath: cli, APIAddr: apiAddr}); err != nil {
		return fmt.Errorf("reconcile grants: %w", err)
	}
	if _, err := orgdecide.ReconcileAutonomy(ctx, store, profileID, d); err != nil {
		return fmt.Errorf("reconcile autonomy: %w", err)
	}
	return nil
}

// cliValidate runs `monoagentcli org validate <name>` and returns a non-nil
// error (the CLI's own message) when it reports invalid.
func (a *App) cliValidate(root, name string) error {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		// No CLI available to double-check with — don't block the save on
		// an environment problem unrelated to the org's own validity; our
		// own Go-side Validate (already run inside orgdesign.Save) is still
		// authoritative for structure even without this second check.
		return nil
	}
	args := []string{"org", "validate", name}
	if root != "" {
		args = append(args, "--project", root)
	}
	cmd := exec.Command(cliBin, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	var payload struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	}
	if jsonErr := json.Unmarshal(out, &payload); jsonErr == nil && !payload.Valid {
		if payload.Error != "" {
			return fmt.Errorf("%s", payload.Error)
		}
		return fmt.Errorf("invalid org config")
	}
	return nil
}

// validateDoc runs both the Go-side and CLI-side checks for a read-only
// status report (GetOrgDesign/ValidateOrgDesign), merging their error text.
func (a *App) validateDoc(d *orgdesign.Doc) (bool, []string) {
	var errs []string
	if err := orgdesign.Validate(d); err != nil {
		if ve, ok := err.(*orgdesign.ValidationError); ok {
			errs = append(errs, ve.Errors...)
		} else {
			errs = append(errs, err.Error())
		}
	}
	return len(errs) == 0, errs
}

// emitOrgDesignUpdated fires the single event name the frontend subscribes
// to for the org designer, regardless of what triggered the change (an
// in-app save, the watcher observing an external/AI-driven edit, or a
// deletion).
func (a *App) emitOrgDesignUpdated(orgName, origin string, deleted bool, d *orgdesign.Doc, valid bool, errs []string) {
	payload := map[string]interface{}{
		"v":         1,
		"orgName":   orgName,
		"profileID": a.getActiveProfileID(),
		"origin":    origin, // "ui" | "external"
		"deleted":   deleted,
		"valid":     valid,
		"errors":    errs,
	}
	if d != nil {
		payload["org"] = d
	} else {
		payload["org"] = nil
	}
	runtime.EventsEmit(a.ctx, "org:designUpdated", payload)
}
