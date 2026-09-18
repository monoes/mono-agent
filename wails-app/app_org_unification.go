package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Org × Workflow unification bindings (plan 2026-09-15 §7.4/§7.7, contracts
// 2026-09-16 §4/§5).
//
// Same doctrine as app_orgs.go: this file never imports internal/monomind or
// any org/grant/autonomy package — every binding shells `monoagentcli …` and
// returns its stdout JSON verbatim. Argument building lives in pure
// functions (…Args) so it is unit-testable without a binary
// (app_org_unification_test.go).
//
// Grants, automation roles, and autonomy MUST go through these bindings, never
// through UpdateOrgRole (app_orgs_design.go): enforcement lives in the CLI's
// DB rows (C-3, C-54), and a direct JSON edit would at best be stripped by
// reconcile and at worst look like it worked.
// ─────────────────────────────────────────────────────────────────────────────

// aliasPattern is the role-id-shaped slug an automation alias must match
// (plan §2 naming rule). Checked here so a bad value fails fast with a clear
// message instead of a CLI round trip.
var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// durationPattern accepts the pause durations the GUI offers (30m, 2h) and
// any other whole number of minutes or hours.
var durationPattern = regexp.MustCompile(`^[0-9]+[mh]$`)

var (
	validGrantModes     = map[string]bool{"run": true, "trigger": true, "status": true}
	validApprovals      = map[string]bool{"none": true, "required": true}
	validLevels         = map[string]bool{"manual": true, "mid": true, "full": true}
	validDeciderKinds   = map[string]bool{"model": true, "boss": true, "parent": true}
	validTiers          = map[string]bool{"routine": true, "consequential": true, "irreversible": true}
	validFailureActions = map[string]bool{"deny": true, "human": true}
	validReplyModes     = regexp.MustCompile(`^(last_node|node:.+)$`)
)

func requireOrg(org string) error {
	if strings.TrimSpace(org) == "" {
		return fmt.Errorf("org name required")
	}
	return nil
}

func requireAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid automation alias %q: start with a letter, then lowercase letters, digits, or underscores (40 max)", alias)
	}
	return nil
}

// ── Pure argument builders (everything after `org`) ─────────────────────────

func automationListArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"automation", "list", org}, nil
}

func automationAddArgs(org, workflowID, alias string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workflowID) == "" {
		return nil, fmt.Errorf("workflow id required")
	}
	if err := requireAlias(alias); err != nil {
		return nil, err
	}
	return []string{"automation", "add", org, "--workflow", workflowID, "--alias", alias}, nil
}

func automationRemoveArgs(org, alias string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if err := requireAlias(alias); err != nil {
		return nil, err
	}
	return []string{"automation", "remove", org, "--alias", alias}, nil
}

func grantListArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"grant", "list", org}, nil
}

// grantSpec is SetOrgGrant's specJSON (contracts §5). Pointer fields are
// optional: absent means "let the CLI apply its default". Note that `grant
// add` is an upsert which rebuilds the whole row from its flags, so an edit
// that omits a field resets it to that default rather than keeping what the
// row already held — the caller has to resend everything it wants preserved.
type grantSpec struct {
	Role           string `json:"role"`
	Automation     string `json:"automation"`
	Mode           string `json:"mode"`
	Wait           *bool  `json:"wait"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
	Approval       string `json:"approval"`
	MaxCallsPerRun *int   `json:"max_calls_per_run"`
	MaxCallsPerDay *int   `json:"max_calls_per_day"`
	MaxOutputBytes *int   `json:"max_output_bytes"`
}

func grantAddArgs(org, specJSON string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	var s grantSpec
	if err := json.Unmarshal([]byte(specJSON), &s); err != nil {
		return nil, fmt.Errorf("invalid grant spec: %w", err)
	}
	if strings.TrimSpace(s.Role) == "" {
		return nil, fmt.Errorf("grant spec: role required")
	}
	if err := requireAlias(s.Automation); err != nil {
		return nil, err
	}
	args := []string{"grant", "add", org, "--role", s.Role, "--automation", s.Automation}
	if s.Mode != "" {
		if !validGrantModes[s.Mode] {
			return nil, fmt.Errorf("grant spec: mode must be run, trigger, or status")
		}
		args = append(args, "--mode", s.Mode)
	}
	if s.Wait != nil {
		args = append(args, "--wait="+strconv.FormatBool(*s.Wait))
	}
	if s.TimeoutSeconds != nil {
		if *s.TimeoutSeconds <= 0 {
			return nil, fmt.Errorf("grant spec: timeout_seconds must be positive")
		}
		args = append(args, "--timeout", strconv.Itoa(*s.TimeoutSeconds))
	}
	if s.Approval != "" {
		if !validApprovals[s.Approval] {
			return nil, fmt.Errorf("grant spec: approval must be none or required")
		}
		args = append(args, "--approval", s.Approval)
	}
	if s.MaxCallsPerRun != nil {
		if *s.MaxCallsPerRun <= 0 {
			return nil, fmt.Errorf("grant spec: max_calls_per_run must be positive")
		}
		args = append(args, "--max-calls-per-run", strconv.Itoa(*s.MaxCallsPerRun))
	}
	if s.MaxCallsPerDay != nil {
		if *s.MaxCallsPerDay <= 0 {
			return nil, fmt.Errorf("grant spec: max_calls_per_day must be positive")
		}
		args = append(args, "--max-calls-per-day", strconv.Itoa(*s.MaxCallsPerDay))
	}
	if s.MaxOutputBytes != nil {
		if *s.MaxOutputBytes <= 0 {
			return nil, fmt.Errorf("grant spec: max_output_bytes must be positive")
		}
		args = append(args, "--max-output-bytes", strconv.Itoa(*s.MaxOutputBytes))
	}
	return args, nil
}

func grantRemoveArgs(org, role, alias string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("role required")
	}
	if err := requireAlias(alias); err != nil {
		return nil, err
	}
	return []string{"grant", "remove", org, "--role", role, "--automation", alias}, nil
}

// automationRoleSpec is AddAutomationRole's specJSON (contracts §5).
type automationRoleSpec struct {
	Alias     string `json:"alias"`
	ReportsTo string `json:"reports_to"`
	Title     string `json:"title"`
	Reply     string `json:"reply"`
}

func automationRoleAddArgs(org, specJSON string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	var s automationRoleSpec
	if err := json.Unmarshal([]byte(specJSON), &s); err != nil {
		return nil, fmt.Errorf("invalid automation role spec: %w", err)
	}
	if err := requireAlias(s.Alias); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.ReportsTo) == "" {
		// An endpoint role may never be root (plan §6.1).
		return nil, fmt.Errorf("automation role spec: reports_to required — an automation cannot be the org's root")
	}
	args := []string{"automation-role", "add", org, "--alias", s.Alias, "--reports-to", s.ReportsTo}
	if s.Title != "" {
		args = append(args, "--title", s.Title)
	}
	if s.Reply != "" {
		if !validReplyModes.MatchString(s.Reply) {
			return nil, fmt.Errorf("automation role spec: reply must be last_node or node:<name>")
		}
		args = append(args, "--reply", s.Reply)
	}
	return args, nil
}

func automationRoleRemoveArgs(org, roleID string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if strings.TrimSpace(roleID) == "" {
		return nil, fmt.Errorf("role id required")
	}
	return []string{"automation-role", "remove", org, "--role", roleID}, nil
}

func effectiveToolsArgs(org, roleID string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	if strings.TrimSpace(roleID) == "" {
		return nil, fmt.Errorf("role id required")
	}
	return []string{"effective-tools", org, "--role", roleID}, nil
}

func autonomyShowArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"autonomy", "show", org}, nil
}

// autonomySpec is SetOrgAutonomy's specJSON (contracts §5). Every field is
// optional so the GUI can change just the level, or just the decider,
// without resending the rest. ClearTiers is a GUI extension mapping to
// repeated --clear-tier.
type autonomySpec struct {
	Level   string `json:"level"`
	Decider *struct {
		Kind           string `json:"kind"`
		Model          string `json:"model"`
		Runtime        string `json:"runtime"`
		Fallback       string `json:"fallback"`
		TimeoutSeconds *int   `json:"timeout_seconds"`
	} `json:"decider"`
	Policy           *string           `json:"policy"`
	Tiers            map[string]string `json:"tiers"`
	ClearTiers       []string          `json:"clear_tiers"`
	OnDeciderFailure string            `json:"on_decider_failure"`
	Limits           *struct {
		MaxDecisionsPerRun  *int     `json:"max_decisions_per_run"`
		MaxDeciderUSDPerRun *float64 `json:"max_decider_usd_per_run"`
	} `json:"limits"`
}

func autonomySetArgs(org, specJSON string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	var s autonomySpec
	if err := json.Unmarshal([]byte(specJSON), &s); err != nil {
		return nil, fmt.Errorf("invalid autonomy spec: %w", err)
	}
	args := []string{"autonomy", "set", org, "--by", "gui"}
	if s.Level != "" {
		if !validLevels[s.Level] {
			return nil, fmt.Errorf("autonomy spec: level must be manual, mid, or full")
		}
		args = append(args, "--level", s.Level)
	}
	if d := s.Decider; d != nil {
		if d.Kind != "" {
			if !validDeciderKinds[d.Kind] {
				return nil, fmt.Errorf("autonomy spec: decider kind must be model, boss, or parent")
			}
			args = append(args, "--decider", d.Kind)
		}
		if d.Runtime != "" {
			args = append(args, "--decider-runtime", d.Runtime)
		}
		if d.Model != "" {
			args = append(args, "--decider-model", d.Model)
		}
		if d.Fallback != "" {
			if d.Fallback != "model" {
				return nil, fmt.Errorf("autonomy spec: decider fallback must be model")
			}
			args = append(args, "--fallback", d.Fallback)
		}
		if d.TimeoutSeconds != nil {
			if *d.TimeoutSeconds <= 0 {
				return nil, fmt.Errorf("autonomy spec: decider timeout_seconds must be positive")
			}
			args = append(args, "--decider-timeout", strconv.Itoa(*d.TimeoutSeconds))
		}
	}
	if s.Policy != nil {
		args = append(args, "--policy", *s.Policy)
	}
	// Sorted so the argument list is deterministic (map order is not).
	tierKeys := make([]string, 0, len(s.Tiers))
	for k := range s.Tiers {
		tierKeys = append(tierKeys, k)
	}
	sort.Strings(tierKeys)
	for _, k := range tierKeys {
		v := s.Tiers[k]
		if !validTiers[v] {
			return nil, fmt.Errorf("autonomy spec: tier for %q must be routine, consequential, or irreversible", k)
		}
		if strings.TrimSpace(k) == "" || strings.Contains(k, "=") {
			return nil, fmt.Errorf("autonomy spec: invalid decision class %q", k)
		}
		args = append(args, "--tier", k+"="+v)
	}
	for _, k := range s.ClearTiers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		args = append(args, "--clear-tier", k)
	}
	if s.OnDeciderFailure != "" {
		if !validFailureActions[s.OnDeciderFailure] {
			return nil, fmt.Errorf("autonomy spec: on_decider_failure must be deny or human")
		}
		args = append(args, "--on-decider-failure", s.OnDeciderFailure)
	}
	if l := s.Limits; l != nil {
		if l.MaxDecisionsPerRun != nil {
			args = append(args, "--max-decisions", strconv.Itoa(*l.MaxDecisionsPerRun))
		}
		if l.MaxDeciderUSDPerRun != nil {
			args = append(args, "--max-decider-usd", strconv.FormatFloat(*l.MaxDeciderUSDPerRun, 'f', -1, 64))
		}
	}
	if len(args) == 5 { // "autonomy", "set", org, "--by", "gui"
		return nil, fmt.Errorf("autonomy spec: nothing to change")
	}
	return args, nil
}

// autonomyPauseArgs: duration "" pauses until resumed; otherwise "30m", "2h", ….
func autonomyPauseArgs(org, duration string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	args := []string{"autonomy", "pause", org}
	if duration != "" {
		if !durationPattern.MatchString(duration) {
			return nil, fmt.Errorf("invalid pause duration %q: use minutes or hours, e.g. 30m or 2h", duration)
		}
		args = append(args, "--for", duration)
	}
	return args, nil
}

func autonomyResumeArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"autonomy", "resume", org}, nil
}

func autonomyDecisionsArgs(org, run string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	args := []string{"autonomy", "decisions", org}
	if run != "" {
		args = append(args, "--run", run)
	}
	return args, nil
}

func needsYouArgs(org string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	return []string{"autonomy", "needs-you", org}, nil
}

func groupArgs(verb, holding string) ([]string, error) {
	if strings.TrimSpace(holding) == "" {
		return nil, fmt.Errorf("holding org name required")
	}
	switch verb {
	case "start", "stop", "status":
	default:
		return nil, fmt.Errorf("unknown group action %q", verb)
	}
	return []string{"group", verb, holding}, nil
}

// sendSpec is SendOrgMessage's specJSON.
type sendSpec struct {
	To      string `json:"to"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func sendArgs(org, specJSON string) ([]string, error) {
	if err := requireOrg(org); err != nil {
		return nil, err
	}
	var s sendSpec
	if err := json.Unmarshal([]byte(specJSON), &s); err != nil {
		return nil, fmt.Errorf("invalid message spec: %w", err)
	}
	if strings.TrimSpace(s.To) == "" {
		return nil, fmt.Errorf("message spec: to required")
	}
	if strings.TrimSpace(s.Subject) == "" {
		return nil, fmt.Errorf("message spec: subject required")
	}
	args := []string{"send", org, "--to", s.To}
	if s.From != "" {
		args = append(args, "--from", s.From)
	}
	args = append(args, "--subject", s.Subject, "--body", s.Body)
	return args, nil
}

// orgCLIArgs builds the full argv for an org subcommand: global flags first
// (profile, JSON mode), then `org`, the project root, and the subcommand.
func orgCLIArgs(profileID, projectRoot string, sub []string) []string {
	full := []string{}
	if profileID != "" {
		full = append(full, "--profile", profileID)
	}
	full = append(full, "--json", "org")
	if projectRoot != "" {
		full = append(full, "--project", projectRoot)
	}
	return append(full, sub...)
}

func statusCLIArgs(profileID string) []string {
	full := []string{}
	if profileID != "" {
		full = append(full, "--profile", profileID)
	}
	return append(full, "--json", "status")
}

// cliResultJSON turns one CLI invocation's outcome into the string handed to
// the frontend: stdout verbatim on success; on failure the CLI's own
// {"error":…} stdout payload when it printed one (contracts §4), else stderr,
// else the exec error — always as the aiError shape.
func cliResultJSON(stdout []byte, runErr error) string {
	trimmed := strings.TrimSpace(string(stdout))
	if runErr == nil {
		if trimmed == "" {
			return aiError(fmt.Errorf("empty output"))
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "{") {
		var probe struct {
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(trimmed), &probe) == nil && probe.Error != "" {
			return trimmed
		}
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) && len(ee.Stderr) > 0 {
		return aiError(fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr))))
	}
	return aiError(runErr)
}

// ── Execution ──────────────────────────────────────────────────────────────

// runUnifiedCLI executes monoagentcli with the given full argv and returns
// the frontend JSON string. label is used for the Logs page only.
func (a *App) runUnifiedCLI(label string, fullArgs []string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, orgCLITimeout)
	defer cancel()
	a.emitLog("ORG", "INFO", fmt.Sprintf("$ %s %s", cliBin, strings.Join(fullArgs, " ")))
	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, cliBin, fullArgs...)
	hideWindow(cmd)
	out, runErr := cmd.Output()
	elapsed := time.Since(startedAt).Round(time.Millisecond)
	res := cliResultJSON(out, runErr)
	if runErr != nil {
		a.emitLog("ORG", "ERROR", fmt.Sprintf("%s failed after %s: %s", label, elapsed, res))
	} else {
		a.emitLog("ORG", "INFO", fmt.Sprintf("%s finished in %s", label, elapsed))
	}
	return res
}

// runOrgSub runs one `org …` subcommand built by a pure builder.
func (a *App) runOrgSub(label string, sub []string, buildErr error) string {
	if buildErr != nil {
		return aiError(buildErr)
	}
	return a.runUnifiedCLI("org "+label, orgCLIArgs(a.getActiveProfileID(), a.orgProjectRoot(), sub))
}

// ── Bindings (contracts §5) ────────────────────────────────────────────────

func (a *App) ListOrgAutomations(org string) string {
	sub, err := automationListArgs(org)
	return a.runOrgSub("automation list", sub, err)
}

func (a *App) ListUnassignedAutomations() string {
	return a.runOrgSub("automation unassigned", []string{"automation", "unassigned"}, nil)
}

func (a *App) AddOrgAutomation(org, workflowID, alias string) string {
	sub, err := automationAddArgs(org, workflowID, alias)
	return a.runOrgSub("automation add", sub, err)
}

func (a *App) RemoveOrgAutomation(org, alias string) string {
	sub, err := automationRemoveArgs(org, alias)
	return a.runOrgSub("automation remove", sub, err)
}

func (a *App) ListOrgGrants(org string) string {
	sub, err := grantListArgs(org)
	return a.runOrgSub("grant list", sub, err)
}

func (a *App) SetOrgGrant(org, specJSON string) string {
	sub, err := grantAddArgs(org, specJSON)
	return a.runOrgSub("grant add", sub, err)
}

func (a *App) RemoveOrgGrant(org, role, alias string) string {
	sub, err := grantRemoveArgs(org, role, alias)
	return a.runOrgSub("grant remove", sub, err)
}

func (a *App) AddAutomationRole(org, specJSON string) string {
	sub, err := automationRoleAddArgs(org, specJSON)
	return a.runOrgSub("automation-role add", sub, err)
}

func (a *App) RemoveAutomationRole(org, roleID string) string {
	sub, err := automationRoleRemoveArgs(org, roleID)
	return a.runOrgSub("automation-role remove", sub, err)
}

func (a *App) GetEffectiveTools(org, roleID string) string {
	sub, err := effectiveToolsArgs(org, roleID)
	return a.runOrgSub("effective-tools", sub, err)
}

func (a *App) GetOrgAutonomy(org string) string {
	sub, err := autonomyShowArgs(org)
	return a.runOrgSub("autonomy show", sub, err)
}

func (a *App) SetOrgAutonomy(org, specJSON string) string {
	sub, err := autonomySetArgs(org, specJSON)
	return a.runOrgSub("autonomy set", sub, err)
}

func (a *App) PauseOrgAutonomy(org, duration string) string {
	sub, err := autonomyPauseArgs(org, duration)
	return a.runOrgSub("autonomy pause", sub, err)
}

func (a *App) ResumeOrgAutonomy(org string) string {
	sub, err := autonomyResumeArgs(org)
	return a.runOrgSub("autonomy resume", sub, err)
}

func (a *App) ListOrgDecisionLog(org, run string) string {
	sub, err := autonomyDecisionsArgs(org, run)
	return a.runOrgSub("autonomy decisions", sub, err)
}

func (a *App) ListNeedsYou(org string) string {
	sub, err := needsYouArgs(org)
	return a.runOrgSub("autonomy needs-you", sub, err)
}

func (a *App) StartOrgGroup(holding string) string {
	sub, err := groupArgs("start", holding)
	return a.runOrgSub("group start", sub, err)
}

func (a *App) StopOrgGroup(holding string) string {
	sub, err := groupArgs("stop", holding)
	return a.runOrgSub("group stop", sub, err)
}

func (a *App) OrgGroupStatus(holding string) string {
	sub, err := groupArgs("status", holding)
	return a.runOrgSub("group status", sub, err)
}

func (a *App) SendOrgMessage(org, specJSON string) string {
	sub, err := sendArgs(org, specJSON)
	return a.runOrgSub("send", sub, err)
}

// GetDaemonStatus reports whether `monoagentcli daemon` (workflow engine +
// endpoint receiver) and `org serve` are up — the "engine offline" badge on
// automation roles reads daemon.running.
func (a *App) GetDaemonStatus() string {
	return a.runUnifiedCLI("status", statusCLIArgs(a.getActiveProfileID()))
}
