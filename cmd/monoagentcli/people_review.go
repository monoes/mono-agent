package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/monoes/mono-agent/internal/peoplereview"
	"github.com/monoes/mono-agent/internal/storage"
	"github.com/spf13/cobra"
)

// newPeopleReviewCmd is the review queue for people a workflow staged for
// outreach (category "pending_approval" with a drafted introduction). The
// GUI's Human in Loop page is a front end for these commands.
func newPeopleReviewCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review people staged for outreach: list, approve (and send), reject",
		Long: "Workflows can stage a lead for human review by saving it with category " +
			"\"pending_approval\" and a drafted introduction. `review list` shows the queue; " +
			"`review approve` marks a person approved, optionally editing the introduction and " +
			"sending it through a dispatch workflow; `review reject` drops them from the queue.",
	}
	cmd.AddCommand(newPeopleReviewListCmd(cfg), newPeopleReviewApproveCmd(cfg), newPeopleReviewRejectCmd(cfg))
	return cmd
}

func newPeopleReviewListCmd(cfg *globalConfig) *cobra.Command {
	var suggest, resuggest bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List people awaiting review",
		Long: "List people awaiting review.\n\n" +
			"--suggest adds TypeSafe Jev's suggestion to each person: {suggest approve|reject, p, " +
			"intro_fit on_topic|generic|off, intro_fit_p}. It needs `jev enable people_review`; a " +
			"suggestion is computed once per person (one request each) and cached until the person " +
			"or their introduction changes. --resuggest recomputes. Suggestions never approve or " +
			"reject anyone.",
		Example: `  monoagentcli --json people review list
  monoagentcli --json people review list --suggest`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			people, err := peoplereview.ListPending(cmd.Context(), db.DB, cfg.ProfileID)
			if err != nil {
				return err
			}
			if !suggest && !resuggest {
				if cfg.JSONOutput {
					return printReviewJSON(people)
				}
				if len(people) == 0 {
					fmt.Println("No one is waiting for review.")
					return nil
				}
				table := newPlainTable(os.Stdout, []string{"ID", "Platform", "Username", "Name", "Introduction"}, nil)
				for _, p := range people {
					table.Append([]string{p.ID, p.Platform, truncateStr(p.PlatformUsername, 20),
						truncateStr(p.FullName, 20), truncateStr(p.Introduction, 40)})
				}
				table.Render()
				return nil
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			ctx, cancel := context.WithTimeout(ctx, reviewSuggestTimeout)
			defer cancel()
			reviewed, warns := peoplereview.WithSuggestions(ctx, db.DB, cfg.ProfileID, people, resuggest)
			for _, w := range warns {
				fmt.Fprintln(os.Stderr, "warning: "+w)
			}
			if cfg.JSONOutput {
				return printReviewJSON(reviewed)
			}
			if len(reviewed) == 0 {
				fmt.Println("No one is waiting for review.")
				return nil
			}
			table := newPlainTable(os.Stdout, []string{"ID", "Platform", "Username", "Name", "Introduction", "Suggestion"}, nil)
			for _, r := range reviewed {
				sug := ""
				if s := r.Suggestion; s != nil {
					sug = fmt.Sprintf("%s p=%.2f intro=%s", s.Suggest, s.P, s.IntroFit)
				}
				table.Append([]string{r.ID, r.Platform, truncateStr(r.PlatformUsername, 20),
					truncateStr(r.FullName, 20), truncateStr(r.Introduction, 40), sug})
			}
			table.Render()
			return nil
		},
	}
	cmd.Flags().BoolVar(&suggest, "suggest", false, "Add TypeSafe Jev's suggestion per person (cached; needs `jev enable people_review`)")
	cmd.Flags().BoolVar(&resuggest, "resuggest", false, "Recompute every suggestion (implies --suggest)")
	return cmd
}

// reviewSuggestTimeout bounds one `people review list --suggest` run's Jev calls.
const reviewSuggestTimeout = 60 * time.Second

// reviewSend is the dispatch run for an approved person: which workflow,
// and the trigger input it gets.
type reviewSend struct {
	WorkflowID   string                 `json:"workflow_id"`
	WorkflowName string                 `json:"workflow_name"`
	Input        map[string]interface{} `json:"input"`
}

type reviewApproval struct {
	Person peoplereview.Person `json:"person"`
	Send   *reviewSend         `json:"send,omitempty"`
}

func newPeopleReviewApproveCmd(cfg *globalConfig) *cobra.Command {
	var (
		intro        string
		send         bool
		sendWorkflow string
		sendPlan     bool
	)
	cmd := &cobra.Command{
		Use:   "approve <person-id>",
		Short: "Approve a person, optionally sending their introduction",
		Long: "Marks a person approved. --intro replaces the drafted introduction.\n\n" +
			"--send then runs the dispatch workflow with the person as trigger input " +
			"({person_id, platform, platform_username, introduction}) and waits for it. The " +
			"workflow is --send-workflow (an id or part of a name) or, by convention, the " +
			"profile's one workflow named like \"" + peoplereview.SendWorkflowName + "\". It is " +
			"resolved before approving, so a missing, ambiguous or inactive workflow leaves the " +
			"person in the queue.\n\n" +
			"--send-plan approves and prints the dispatch run (workflow and input) without " +
			"starting it, for a caller that runs workflows itself (the GUI).",
		Example: `  monoagentcli people review approve 3f2a…
  monoagentcli people review approve 3f2a… --intro "Hi Sam, …" --send
  monoagentcli people review approve 3f2a… --send --send-workflow "LinkedIn DMs"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if send && sendPlan {
				return errInvalidInput("--send and --send-plan are exclusive")
			}
			if sendWorkflow != "" && !send && !sendPlan {
				return errInvalidInput("--send-workflow needs --send or --send-plan")
			}
			ctx := cmd.Context()
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			// Everything that can refuse the send is checked before the
			// person leaves the queue.
			var wf peoplereview.Workflow
			if send || sendPlan {
				if wf, err = pickReviewWorkflow(ctx, cfg, db, sendWorkflow); err != nil {
					return err
				}
			}
			p, err := peoplereview.Approve(ctx, db.DB, cfg.ProfileID, args[0], intro)
			if err != nil {
				return reviewErr(err)
			}
			res := reviewApproval{Person: p}
			if send || sendPlan {
				res.Send = &reviewSend{WorkflowID: wf.ID, WorkflowName: wf.Name, Input: peoplereview.SendInput(p)}
			}
			if !send {
				if cfg.JSONOutput {
					return printReviewJSON(res)
				}
				fmt.Printf("Approved %s (%s).\n", displayName(p), p.ID)
				if res.Send != nil {
					fmt.Printf("Send with workflow %q (%s).\n", wf.Name, wf.ID)
				}
				return nil
			}

			if !cfg.JSONOutput {
				fmt.Printf("Approved %s (%s); sending with workflow %q.\n", displayName(p), p.ID, wf.Name)
			}
			input, err := json.Marshal(res.Send.Input)
			if err != nil {
				return err
			}
			db.Close() // the run opens its own
			run := newWorkflowRunCmd(cfg)
			run.SetArgs([]string{wf.ID, "--input", string(input)})
			run.SetContext(ctx)
			if err := run.Execute(); err != nil {
				return fmt.Errorf("%s is approved, but sending failed: %w", p.ID, err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&intro, "intro", "", "Replace the drafted introduction")
	cmd.Flags().BoolVar(&send, "send", false, "Run the dispatch workflow for this person after approving")
	cmd.Flags().StringVar(&sendWorkflow, "send-workflow", "", `Dispatch workflow id or name (default: the one named like "`+peoplereview.SendWorkflowName+`")`)
	cmd.Flags().BoolVar(&sendPlan, "send-plan", false, "Approve and print the dispatch run without starting it")
	return cmd
}

func newPeopleReviewRejectCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "reject <person-id>",
		Short: "Reject a person so they leave the queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()
			if err := peoplereview.Reject(cmd.Context(), db.DB, cfg.ProfileID, args[0]); err != nil {
				return reviewErr(err)
			}
			if cfg.JSONOutput {
				return printReviewJSON(map[string]string{"id": args[0], "category": peoplereview.Rejected})
			}
			fmt.Printf("Rejected %s.\n", args[0])
			return nil
		},
	}
}

// pickReviewWorkflow resolves the dispatch workflow among the profile's
// workflows (files and SQLite alike) and refuses an inactive one, which
// the engine would not run.
func pickReviewWorkflow(ctx context.Context, cfg *globalConfig, db *storage.Database, want string) (peoplereview.Workflow, error) {
	all, err := newHybridStore(db).ListWorkflows(ctx, cfg.ProfileID)
	if err != nil {
		return peoplereview.Workflow{}, fmt.Errorf("listing workflows: %w", err)
	}
	cands := make([]peoplereview.Workflow, len(all))
	for i, w := range all {
		cands[i] = peoplereview.Workflow{ID: w.ID, Name: w.Name, Active: w.IsActive}
	}
	wf, err := peoplereview.PickSendWorkflow(cands, want)
	if err != nil {
		return wf, reviewErr(err)
	}
	if !wf.Active {
		return wf, errInvalidInput("workflow %q (%s) is inactive — activate it first: monoagentcli workflow activate %s", wf.Name, wf.ID, wf.ID)
	}
	return wf, nil
}

func reviewErr(err error) error {
	switch {
	case errors.Is(err, peoplereview.ErrNotFound):
		return errNotFound("%v", err)
	case errors.Is(err, peoplereview.ErrAmbiguous):
		return errInvalidInput("%v", err)
	}
	return err
}

func displayName(p peoplereview.Person) string {
	if p.FullName != "" {
		return p.FullName
	}
	return p.PlatformUsername
}

func printReviewJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
