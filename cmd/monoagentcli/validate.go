package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/workflow"
	"github.com/spf13/cobra"
)

// planStep is one entry of a dry-run execution plan.
type planStep struct {
	Order int    `json:"order"`
	ID    string `json:"id"`
	Type  string `json:"type"`
}

// dryRunJSON is the `workflow run --dry-run --json` payload.
type dryRunJSON struct {
	Valid  bool       `json:"valid"`
	Errors []string   `json:"errors"`
	Plan   []planStep `json:"plan"`
}

// validationJSON is the `workflow validate --json` payload.
type validationJSON struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// loadWorkflowDefinition fetches a workflow by id from the canonical hybrid
// store. Returns an errNotFound sentinel error when it does not exist.
func loadWorkflowDefinition(ctx context.Context, cfg *globalConfig, workflowID string) (*workflow.Workflow, error) {
	db, err := initDB(cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	store := newHybridStore(db)
	wf, err := store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("get workflow: %w", err)
	}
	if wf == nil {
		return nil, errNotFound("workflow %q not found", workflowID)
	}
	return wf, nil
}

// normalizeConnectionHandles defaults empty connection handles to "main",
// matching the engine's runtime routing (an unset handle means the "main"
// port) so validation checks the same post-normalization shape `workflow
// import` accepts and the engine executes.
func normalizeConnectionHandles(wf *workflow.Workflow) {
	for i := range wf.Connections {
		if wf.Connections[i].SourceHandle == "" {
			wf.Connections[i].SourceHandle = "main"
		}
		if wf.Connections[i].TargetHandle == "" {
			wf.Connections[i].TargetHandle = "main"
		}
	}
}

// dryRunSteps validates wf with ValidateForActivation and computes its
// topological execution order. Returns the validation/DAG error when invalid.
// Nothing is executed.
func dryRunSteps(wf *workflow.Workflow) ([]planStep, error) {
	normalizeConnectionHandles(wf)
	if err := workflow.ValidateForActivation(wf); err != nil {
		return nil, err
	}
	dag, err := workflow.BuildDAG(wf.Nodes, wf.Connections)
	if err != nil {
		return nil, err
	}
	sorted, err := dag.TopologicalSort()
	if err != nil {
		return nil, err
	}
	steps := make([]planStep, 0, len(sorted))
	for i, n := range sorted {
		steps = append(steps, planStep{Order: i + 1, ID: n.ID, Type: n.Type})
	}
	return steps, nil
}

// newWorkflowValidateCmd validates a saved workflow (<id>) or a JSON file
// (--file, without saving) against the same rules activation enforces.
// File input goes through the same normalization `workflow import` uses
// (legacy key conversion; connection handles default to "main"), so a file
// that imports cleanly also validates.
func newWorkflowValidateCmd(cfg *globalConfig) *cobra.Command {
	var inputFile string

	cmd := &cobra.Command{
		Use:   "validate <id> | --file <workflow.json>",
		Short: "Validate a workflow (structure, DAG, triggers) without saving or running it",
		Long: "Runs the full activation validation: unique node ids, resolvable connections, " +
			"no cycles, a trigger node present, and required trigger config (cron/path). " +
			"Files are normalized the same way `workflow import` does it (legacy key " +
			"conversion; unset connection handles default to \"main\"), so validate " +
			"checks the post-normalization shape. " +
			"Exit 0 when valid, 2 when the workflow is not found, 3 when invalid.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var wf *workflow.Workflow
			if inputFile != "" {
				raw, err := os.ReadFile(inputFile)
				if err != nil {
					if os.IsNotExist(err) {
						return errNotFound("read workflow file: %v", err)
					}
					return fmt.Errorf("read workflow file: %w", err)
				}
				parsed, err := parseWorkflowDefinition(raw)
				if err != nil {
					return err
				}
				wf = &parsed
			} else {
				if len(args) != 1 {
					return errInvalidInput("pass a workflow id, or --file <workflow.json>")
				}
				var err error
				wf, err = loadWorkflowDefinition(cmd.Context(), cfg, args[0])
				if err != nil {
					return err
				}
			}

			normalizeConnectionHandles(wf)

			if err := workflow.ValidateForActivation(wf); err != nil {
				if cfg.JSONOutput {
					_ = json.NewEncoder(cmd.OutOrStdout()).Encode(validationJSON{Valid: false, Errors: []string{err.Error()}})
				}
				return errInvalidInput("%v", err)
			}

			if cfg.JSONOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(validationJSON{Valid: true, Errors: []string{}})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Workflow is valid.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&inputFile, "file", "f", "", "Validate a workflow JSON file without saving it")
	return cmd
}

// runWorkflowDryRun backs `workflow run --dry-run`: validate + print the
// topological execution plan; never start an execution.
func runWorkflowDryRun(cfg *globalConfig, cmd *cobra.Command, workflowID string) error {
	wf, err := loadWorkflowDefinition(cmd.Context(), cfg, workflowID)
	if err != nil {
		return err
	}

	steps, err := dryRunSteps(wf)
	if err != nil {
		if cfg.JSONOutput {
			_ = json.NewEncoder(cmd.OutOrStdout()).Encode(dryRunJSON{Valid: false, Errors: []string{err.Error()}})
		}
		return errInvalidInput("%v", err)
	}

	if cfg.JSONOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(dryRunJSON{Valid: true, Errors: []string{}, Plan: steps})
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dry run — workflow %q is valid. Execution plan (%d nodes):\n", wf.Name, len(steps))
	for _, s := range steps {
		fmt.Fprintf(out, "  %d. %s  (%s)\n", s.Order, s.ID, s.Type)
	}
	fmt.Fprintln(out, "No execution was started.")
	return nil
}
