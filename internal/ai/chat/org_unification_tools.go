package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/ai"
)

// Org × workflow unification tools (plan §7.1, §7.7). Each shells back into
// `monoagentcli org …` — the same code path the CLI and the GUI use — so
// grant rows, reconcile, and autonomy rules live in one place. Granting a
// role an automation or raising an org's autonomy hands agents new power,
// so both refuse once untrusted synced communications entered the session
// and both preview unless confirm:true.

func orgUnificationToolDefs(def func(name, desc string, props map[string]interface{}, required []string) ai.ToolDef) []ai.ToolDef {
	return []ai.ToolDef{
		def("add_org_automation", "Add an existing workflow to an org as one of its automations, under a short alias (lowercase letters, digits, underscores). Roles can then be granted it as a tool with set_org_grant, or it can become an automation role.", map[string]interface{}{
			"org_name":    strParam("The org's name"),
			"workflow_id": strParam("The workflow to add (same profile)"),
			"alias":       strParam("Name the org uses for it; becomes the tool automation_<alias>"),
			"owned":       boolParam("Whether this org owns the workflow's lifecycle (at most one org may)"),
		}, []string{"org_name", "workflow_id", "alias"}),
		def("set_org_grant", "Grant (or with revoke:true, revoke) one org role the right to call one of the org's automations as a tool. Workflows with outbound nodes (email, chat, social, service writes) default to needing a decision per call. Without confirm:true this only previews the change.", map[string]interface{}{
			"org_name":          strParam("The org's name"),
			"role_id":           strParam("The agent role receiving the tool"),
			"automation":        strParam("The automation's alias in this org"),
			"mode":              strParam("run (default) | trigger | status"),
			"wait":              boolParam("Wait for the run and return its output (default true)"),
			"timeout_seconds":   intParam("Seconds to wait when wait is true (default 600)"),
			"approval":          strParam("none | required — required makes every call a decision routed by the org's autonomy level; omit for the safe default"),
			"max_calls_per_run": intParam("Calls allowed per org run (default 20)"),
			"revoke":            boolParam("Revoke the grant instead of setting it"),
			"confirm":           boolParam("Must be true to apply; omit to preview"),
		}, []string{"org_name", "role_id", "automation"}),
		def("set_org_autonomy", "Change who decides an org's pending approvals, questions, and gates: level manual (a human decides everything), mid (rules approve routine items, the decider handles consequential ones, a human handles irreversible ones), or full (the decider handles everything, no human). Without confirm:true this only previews the change.", map[string]interface{}{
			"org_name":           strParam("The org's name"),
			"level":              strParam("manual | mid | full"),
			"decider":            strParam("model | boss | parent"),
			"decider_model":      strParam("Model id for the model decider"),
			"decider_runtime":    strParam("Runtime for the model decider (e.g. claude)"),
			"policy":             strParam("Instructions the decider follows (operator text)"),
			"on_decider_failure": strParam("deny | human — what full does when the decider fails"),
			"confirm":            boolParam("Must be true to apply; omit to preview"),
		}, []string{"org_name"}),
	}
}

// executeOrgUnification handles the tools above; ok is false for any other
// name.
func (mt *MonoagentTools) executeOrgUnification(ctx context.Context, name, args string) (string, bool, error) {
	switch name {
	case "add_org_automation":
		out, err := mt.addOrgAutomation(ctx, args)
		return out, true, err
	case "set_org_grant":
		out, err := mt.setOrgGrant(ctx, args)
		return out, true, err
	case "set_org_autonomy":
		out, err := mt.setOrgAutonomy(ctx, args)
		return out, true, err
	}
	return "", false, nil
}

// runOrgCLI runs `monoagentcli --profile <active> org <args…>` and returns
// its JSON line (the CLI may print warnings to stderr before it).
func (mt *MonoagentTools) runOrgCLI(ctx context.Context, tool string, args ...string) (string, error) {
	if mt.selfBin == "" {
		return "", fmt.Errorf("%s: unavailable in this session (no monoagentcli binary)", tool)
	}
	cctx, cancel, err := runExecTimeoutCtx(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", tool, err)
	}
	defer cancel()
	full := append([]string{"--profile", mt.ProfileID(), "org"}, args...)
	out, err := runSelfExec(cctx, mt.selfBin, full...)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", tool, err, truncateRunErrOutput(strings.TrimSpace(string(out))))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); json.Valid([]byte(l)) && strings.HasPrefix(l, "{") {
			return l, nil
		}
	}
	return "", fmt.Errorf("%s: unexpected output: %s", tool, truncateRunErrOutput(string(out)))
}

type addOrgAutomationArgs struct {
	OrgName    string `json:"org_name"`
	WorkflowID string `json:"workflow_id"`
	Alias      string `json:"alias"`
	Owned      bool   `json:"owned"`
}

func (mt *MonoagentTools) addOrgAutomation(ctx context.Context, args string) (string, error) {
	var a addOrgAutomationArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkWorkflowOwnership(a.WorkflowID); err != nil {
		return "", err
	}
	cli := []string{"automation", "add", a.OrgName, "--workflow", a.WorkflowID, "--alias", a.Alias}
	if a.Owned {
		cli = append(cli, "--owned")
	}
	return mt.runOrgCLI(ctx, "add_org_automation", cli...)
}

type setOrgGrantArgs struct {
	OrgName        string `json:"org_name"`
	RoleID         string `json:"role_id"`
	Automation     string `json:"automation"`
	Mode           string `json:"mode"`
	Wait           *bool  `json:"wait"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Approval       string `json:"approval"`
	MaxCallsPerRun int    `json:"max_calls_per_run"`
	Revoke         bool   `json:"revoke"`
	Confirm        bool   `json:"confirm"`
}

func (mt *MonoagentTools) setOrgGrant(ctx context.Context, args string) (string, error) {
	var a setOrgGrantArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkInjectionGate("set_org_grant"); err != nil {
		return "", err
	}
	var cli []string
	if a.Revoke {
		cli = []string{"grant", "remove", a.OrgName, "--role", a.RoleID, "--automation", a.Automation}
	} else {
		cli = []string{"grant", "add", a.OrgName, "--role", a.RoleID, "--automation", a.Automation}
		if a.Mode != "" {
			cli = append(cli, "--mode", a.Mode)
		}
		if a.Wait != nil {
			cli = append(cli, "--wait="+strconv.FormatBool(*a.Wait))
		}
		if a.TimeoutSeconds > 0 {
			cli = append(cli, "--timeout", strconv.Itoa(a.TimeoutSeconds))
		}
		if a.Approval != "" {
			cli = append(cli, "--approval", a.Approval)
		}
		if a.MaxCallsPerRun > 0 {
			cli = append(cli, "--max-calls-per-run", strconv.Itoa(a.MaxCallsPerRun))
		}
	}
	if !a.Confirm {
		return marshalJSON(map[string]interface{}{
			"would_apply": true,
			"command":     "monoagentcli org " + strings.Join(cli, " "),
			"note":        "this changes which automations an org role can run; call again with confirm:true to apply",
		})
	}
	return mt.runOrgCLI(ctx, "set_org_grant", cli...)
}

type setOrgAutonomyArgs struct {
	OrgName          string  `json:"org_name"`
	Level            string  `json:"level"`
	Decider          string  `json:"decider"`
	DeciderModel     string  `json:"decider_model"`
	DeciderRuntime   string  `json:"decider_runtime"`
	Policy           *string `json:"policy"`
	OnDeciderFailure string  `json:"on_decider_failure"`
	Confirm          bool    `json:"confirm"`
}

func (mt *MonoagentTools) setOrgAutonomy(ctx context.Context, args string) (string, error) {
	var a setOrgAutonomyArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if err := mt.checkInjectionGate("set_org_autonomy"); err != nil {
		return "", err
	}
	cli := []string{"autonomy", "set", a.OrgName, "--by", "chat"}
	if a.Level != "" {
		cli = append(cli, "--level", a.Level)
	}
	if a.Decider != "" {
		cli = append(cli, "--decider", a.Decider)
	}
	if a.DeciderModel != "" {
		cli = append(cli, "--decider-model", a.DeciderModel)
	}
	if a.DeciderRuntime != "" {
		cli = append(cli, "--decider-runtime", a.DeciderRuntime)
	}
	if a.Policy != nil {
		cli = append(cli, "--policy", *a.Policy)
	}
	if a.OnDeciderFailure != "" {
		cli = append(cli, "--on-decider-failure", a.OnDeciderFailure)
	}
	if !a.Confirm {
		return marshalJSON(map[string]interface{}{
			"would_apply": true,
			"command":     "monoagentcli org " + strings.Join(cli, " "),
			"note":        "this changes who decides the org's approvals, questions, and gates; call again with confirm:true to apply",
		})
	}
	return mt.runOrgCLI(ctx, "set_org_autonomy", cli...)
}
