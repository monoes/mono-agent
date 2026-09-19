package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdecide"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/storage"
)

// pausedIndefinitely stands for "paused until resumed".
var pausedIndefinitely = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

func newOrgAutonomyCmd(env *orgEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "autonomy",
		Short: "Choose who decides an org's approvals, questions, and gates (manual, mid, full)",
		Long: "Levels: manual — a person decides everything. mid — rules approve routine items, the " +
			"decider handles consequential ones, a person handles irreversible ones. full — rules approve " +
			"routine items and the decider handles everything else, with no person involved. Decisions are " +
			"made by `monoagentcli daemon`; without it every org behaves as manual.",
	}
	cmd.AddCommand(newOrgAutonomyShowCmd(env), newOrgAutonomySetCmd(env), newOrgAutonomyPauseCmd(env, true),
		newOrgAutonomyPauseCmd(env, false), newOrgAutonomyDecisionsCmd(env), newOrgAutonomyNeedsYouCmd(env))
	return cmd
}

func autonomyView(a *orgdecide.Autonomy) map[string]interface{} {
	now := time.Now()
	var paused interface{}
	if a.PausedUntil != nil && a.PausedUntil.After(now) {
		paused = a.PausedUntil.UTC().Format(time.RFC3339)
	}
	tiers := a.Tiers
	if tiers == nil {
		tiers = map[string]string{}
	}
	_, daemonLive := daemonhb.Read()
	effective := a.EffectiveLevel(now)
	if !daemonLive {
		effective = orgdesign.LevelManual
	}
	updated := ""
	if !a.UpdatedAt.IsZero() {
		updated = a.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return map[string]interface{}{
		"v": 1, "org": a.OrgName, "level": a.Level, "effective_level": effective, "paused_until": paused,
		"decider": map[string]interface{}{
			"kind": a.Decider.Kind, "runtime": a.Decider.Runtime, "model": a.Decider.Model,
			"fallback": a.Decider.Fallback, "timeout_seconds": a.Decider.TimeoutSeconds,
		},
		"tiers": tiers, "default_tiers": orgdecide.DefaultTiers, "policy": a.Policy,
		"on_decider_failure": a.OnDeciderFailure,
		"limits": map[string]interface{}{
			"max_decisions_per_run": a.Limits.MaxDecisionsPerRun, "max_decider_usd_per_run": a.Limits.MaxDeciderUSDPerRun,
		},
		"updated_at": updated, "updated_by": a.UpdatedBy, "daemon_running": daemonLive,
	}
}

func newOrgAutonomyShowCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "show <org>",
		Short: "Show an org's autonomy settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, _, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			a, err := orgdecide.NewStore(db.DB).Get(cmd.Context(), profileID, doc.Name)
			if err != nil {
				return err
			}
			return printJSONValue(autonomyView(a))
		},
	}
}

func newOrgAutonomySetCmd(env *orgEnv) *cobra.Command {
	var level, decider, runtime, model, fallback, policy, policyFile, onFailure, by string
	var timeout, maxDecisions int
	var maxUSD float64
	var tiers, clearTiers []string
	c := &cobra.Command{
		Use:   "set <org>",
		Short: "Change an org's autonomy level, decider, tiers, or policy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			store := orgdecide.NewStore(db.DB)
			a, err := store.Get(ctx, profileID, doc.Name)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if f.Changed("level") {
				a.Level = level
			}
			if f.Changed("decider") {
				a.Decider.Kind = decider
			}
			if f.Changed("decider-runtime") {
				a.Decider.Runtime = runtime
			}
			if f.Changed("decider-model") {
				a.Decider.Model = model
			}
			if f.Changed("fallback") {
				a.Decider.Fallback = fallback
			}
			if f.Changed("decider-timeout") {
				a.Decider.TimeoutSeconds = timeout
			}
			if f.Changed("policy") {
				a.Policy = policy
			}
			if f.Changed("policy-file") {
				b, err := os.ReadFile(policyFile)
				if err != nil {
					return err
				}
				a.Policy = string(b)
			}
			if f.Changed("on-decider-failure") {
				a.OnDeciderFailure = onFailure
			}
			if f.Changed("max-decisions") {
				a.Limits.MaxDecisionsPerRun = maxDecisions
			}
			if f.Changed("max-decider-usd") {
				a.Limits.MaxDeciderUSDPerRun = maxUSD
			}
			if a.Tiers == nil {
				a.Tiers = map[string]string{}
			}
			for _, kv := range tiers {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return errInvalidInput("--tier %q must be <class>=<tier>", kv)
				}
				a.Tiers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
			for _, k := range clearTiers {
				delete(a.Tiers, k)
			}
			if err := a.Validate(); err != nil {
				return errInvalidInput("%v", err)
			}
			if err := checkDeciderAvailable(ctx, root, doc.Name, a); err != nil {
				return err
			}
			if err := store.Put(ctx, a, by); err != nil {
				return err
			}
			if err := grantDeciderTools(ctx, env, db, profileID, root, doc, a); err != nil {
				return err
			}
			doc.Autonomy = orgdecide.DisplayCopy(a, doc.Autonomy)
			if _, err := saveOrgReconciled(ctx, db, profileID, root, doc, env.genOptions(profileID)); err != nil {
				return err
			}
			return printJSONValue(autonomyView(a))
		},
	}
	f := c.Flags()
	f.StringVar(&level, "level", "", "manual | mid | full")
	f.StringVar(&decider, "decider", "", "model | boss | parent")
	f.StringVar(&runtime, "decider-runtime", "", "Runtime for the model decider (e.g. claude)")
	f.StringVar(&model, "decider-model", "", "Model for the model decider")
	f.StringVar(&fallback, "fallback", "", "Decider for items the chosen one cannot take (model)")
	f.IntVar(&timeout, "decider-timeout", 0, "Seconds before an item goes to the fallback")
	f.StringVar(&policy, "policy", "", "Instructions the decider follows (\"\" clears)")
	f.StringVar(&policyFile, "policy-file", "", "Read the policy from a file")
	f.StringArrayVar(&tiers, "tier", nil, "Override a class's tier: <class>=routine|consequential|irreversible (repeatable)")
	f.StringArrayVar(&clearTiers, "clear-tier", nil, "Remove a tier override (repeatable)")
	f.StringVar(&onFailure, "on-decider-failure", "", "full only: deny | human")
	f.IntVar(&maxDecisions, "max-decisions", 0, "Decider decisions allowed per run")
	f.Float64Var(&maxUSD, "max-decider-usd", 0, "Decider spend allowed per run (USD)")
	f.StringVar(&by, "by", "cli", "Recorded as updated_by: cli | gui | chat")
	return c
}

// checkDeciderAvailable enforces the plan §6.1 decider rules that need the
// outside world: parent needs a holding org listing this org, boss needs
// monomind tool providers.
func checkDeciderAvailable(ctx context.Context, root, org string, a *orgdecide.Autonomy) error {
	switch a.Decider.Kind {
	case orgdesign.DeciderParent:
		docs, _, err := orgdesign.LoadAll(root)
		if err != nil {
			return err
		}
		if orgdesign.HoldingOwner(docs, org) == "" {
			return errInvalidInput("decider parent needs a holding org that lists %q in its children", org)
		}
	case orgdesign.DeciderBoss:
		set, err := monomind.Capabilities(ctx)
		if err != nil {
			return err
		}
		if err := set.Require(monomind.CapOrgToolProviders, "the boss decider"); err != nil {
			return err
		}
	case orgdesign.DeciderModel:
		return checkDeciderModel(ctx, a)
	}
	return nil
}

// Seams for checkDeciderModel's tests: both shell out to a runtime binary,
// which a unit test has no business doing.
var (
	scanAgentRuntimes = monomind.Scan
	listRuntimeModels = monomind.ListModels
)

// checkDeciderModel rejects a model the decider's runtime does not have.
//
// A model decider is only as good as its model name, and a wrong one was
// accepted in silence: it surfaced mid-run as "decider failed: … runner-error:
// done reported nonzero exit_code 1", which reads like a broken decider rather
// than a name that never existed. Two easy ways in — a typo, and
// `--decider-runtime codex` on an org whose decider still carries the default
// Claude model — so the check pays for itself.
//
// Deliberately fail-open on our own inability to look: a runtime with no
// discovery command, an uninstalled binary, or a listing that errors leaves
// the model as typed. Only a definite mismatch is refused.
func checkDeciderModel(ctx context.Context, a *orgdecide.Autonomy) error {
	model := strings.TrimSpace(a.Decider.Model)
	runtimeID := strings.TrimSpace(a.Decider.Runtime)
	if model == "" || runtimeID == "" {
		return nil
	}
	scan, err := scanAgentRuntimes(ctx)
	if err != nil || scan == nil {
		return nil
	}
	binary := ""
	for _, e := range scan.Agents {
		if e.ID == runtimeID {
			if !e.Installed {
				return errInvalidInput("decider runtime %q is not installed%s", runtimeID, installHint(e.InstallHint))
			}
			if e.Binary != nil {
				binary = *e.Binary
			}
			break
		}
	}
	models, err := listRuntimeModels(ctx, runtimeID, binary)
	if err != nil || len(models) == 0 {
		return nil // no catalog to check against — leave the name as typed
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.ID == model {
			return nil
		}
		names = append(names, m.ID)
	}
	return errInvalidInput("decider model %q is not one %s offers; available: %s",
		model, runtimeID, strings.Join(names, ", "))
}

func installHint(hint string) string {
	if strings.TrimSpace(hint) == "" {
		return ""
	}
	return " — " + hint
}

func newOrgAutonomyPauseCmd(env *orgEnv, pause bool) *cobra.Command {
	var all bool
	var forDur string
	use, short := "resume <org>|--all", "Resume autonomy after a pause"
	if pause {
		use, short = "pause <org>|--all", "Drop an org to manual right away (for a while, or until resumed)"
	}
	c := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, profileID, root, err := env.Profile()
			if err != nil {
				return err
			}
			store := orgdecide.NewStore(db.DB)
			var orgs []string
			switch {
			case all:
				if orgs, err = orgdesign.ListOrgNames(root); err != nil {
					return err
				}
			case len(args) == 1:
				orgs = []string{args[0]}
			default:
				return errInvalidInput("name an org or pass --all")
			}
			until := pausedIndefinitely
			if pause && forDur != "" {
				d, err := time.ParseDuration(forDur)
				if err != nil || d <= 0 {
					return errInvalidInput("--for %q must be a duration like 30m or 2h", forDur)
				}
				until = time.Now().Add(d)
			}
			var views []map[string]interface{}
			for _, org := range orgs {
				a, err := store.Get(ctx, profileID, org)
				if err != nil {
					return err
				}
				if pause {
					u := until
					a.PausedUntil = &u
				} else {
					a.PausedUntil = nil
				}
				if err := store.Put(ctx, a, "cli"); err != nil {
					return err
				}
				views = append(views, autonomyView(a))
			}
			if !all && len(views) == 1 {
				return printJSONValue(views[0])
			}
			return printJSONValue(map[string]interface{}{"v": 1, "orgs": views})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Every org in the profile")
	if pause {
		c.Flags().StringVar(&forDur, "for", "", "How long, e.g. 30m or 2h (default: until resumed)")
	}
	return c
}

func newOrgAutonomyDecisionsCmd(env *orgEnv) *cobra.Command {
	var run, verdict string
	var limit int
	c := &cobra.Command{
		Use:   "decisions <org>",
		Short: "List the decisions made for an org (resolver, tier, verdict, rationale, cost)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, _, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			ds, err := orgdecide.NewStore(db.DB).List(cmd.Context(), profileID, doc.Name, orgdecide.DecisionFilter{RunID: run, Verdict: verdict, Limit: limit})
			if err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "decisions": ds})
		},
	}
	c.Flags().StringVar(&run, "run", "", "Only this org run")
	c.Flags().StringVar(&verdict, "verdict", "", "approved | denied | answered | escalated | failed")
	c.Flags().IntVar(&limit, "limit", 200, "Most recent N")
	return c
}

func newOrgAutonomyNeedsYouCmd(env *orgEnv) *cobra.Command {
	return &cobra.Command{
		Use:   "needs-you <org>",
		Short: "List pending items waiting for a person",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, profileID, root, doc, err := loadOrgForEdit(env, args[0])
			if err != nil {
				return err
			}
			items, err := needsYou(cmd.Context(), db, profileID, root, doc.Name)
			if err != nil {
				return err
			}
			return printJSONValue(map[string]interface{}{"v": 1, "org": doc.Name, "items": items})
		},
	}
}

// needsYou lists pending items a person has to decide: everything when no
// daemon runs; otherwise items the level routes to a person, and items the
// daemon escalated or could not decide.
func needsYou(ctx context.Context, db *storage.Database, profileID, root, org string) ([]map[string]interface{}, error) {
	svc := orgdecide.NewService(db.DB, nil)
	a, err := svc.Store.Get(ctx, profileID, org)
	if err != nil {
		return nil, err
	}
	pending, _, _, err := svc.Pending(ctx, profileID, root, org, a)
	if err != nil {
		return nil, err
	}
	_, daemonLive := daemonhb.Read()
	level := a.EffectiveLevel(time.Now())
	out := []map[string]interface{}{}
	for _, it := range pending {
		human := !daemonLive || orgdecide.Route(level, it.Tier) == orgdecide.RouteHuman
		if !human {
			latest, err := svc.Store.LatestFor(ctx, profileID, org, it.Kind, it.Ref)
			if err != nil {
				return nil, err
			}
			human = latest != nil && latest.Level == level &&
				(latest.Verdict == orgdecide.VerdictEscalated || (latest.Verdict == orgdecide.VerdictFailed && (level != orgdesign.LevelFull || a.OnDeciderFailure == "human")))
		}
		if !human {
			continue
		}
		waiting := ""
		if it.WaitingMS > 0 {
			waiting = time.UnixMilli(it.WaitingMS).UTC().Format(time.RFC3339)
		}
		out = append(out, map[string]interface{}{
			"kind": it.Kind, "ref": it.Ref, "requester": it.Requester, "class": it.Class, "tier": it.Tier,
			"summary": it.Summary, "waiting_since": waiting, "idle_stop_in_seconds": nil,
		})
	}
	return out, nil
}

// ensureNewOrgAutonomy gives an org created by an explicit command its
// starting autonomy row: mid by default (Q8), or manual when the document
// asks for it. A document can never start an org at full.
func ensureNewOrgAutonomy(ctx context.Context, db *storage.Database, profileID string, doc *orgdesign.Doc, by string) error {
	store := orgdecide.NewStore(db.DB)
	a, err := store.Get(ctx, profileID, doc.Name)
	if err != nil || a.Stored {
		return err
	}
	a.Level = orgdesign.LevelMid
	if doc.Autonomy != nil && doc.Autonomy.Level == orgdesign.LevelManual {
		a.Level = orgdesign.LevelManual
	}
	if doc.Autonomy != nil && doc.Autonomy.Policy != "" {
		a.Policy = doc.Autonomy.Policy
	}
	if err := store.Put(ctx, a, by); err != nil {
		return fmt.Errorf("store starting autonomy: %w", err)
	}
	return nil
}
