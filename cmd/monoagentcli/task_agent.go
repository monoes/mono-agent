package main

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/tasks"
)

// The agent's commands. Each one that needs the agent's name judges the caller first, before it reads
// its arguments and before it opens the database (the operator's commands refuse an agent the same
// way), then checks its own arguments, so that a mistake is exit 3 with the --json error document that
// cobra's Args validators would not give (they answer exit 1 and no document). The text they print
// carries what the task says only as the notes block of printNotes, and every command an agent is told
// to run next names the profile and, where it is the agent's own, the agent.

// agentNameRE is the alphabet and the length of an agent's name (checkAgentName in internal/tasks/store.go).
var agentNameRE = regexp.MustCompile(fmt.Sprintf(`^[A-Za-z0-9._#@:-]{1,%d}$`, tasks.MaxNameLen))

// reservedAgentNames are the labels the store writes for someone other than an agent: the operator's,
// an unnamed agent's and the capture surfaces'. An agent named like one would write events that read as theirs.
var reservedAgentNames = []string{"you", "agent", "capture", tasks.SourceChrome, tasks.SourceOS}

// agentNameError says why the store would refuse name as the name of an agent, or is nil. The store
// judges a name too, but only once the database is open: this is the same judgment, kept in step by a
// test, so that a name that cannot be accepted is refused before anything is opened.
func agentNameError(name string) error {
	if !agentNameRE.MatchString(name) {
		return errInvalidInput("an agent name is 1-%d characters of letters, digits and . _ # @ : - (got %q): choose one with --as NAME", tasks.MaxNameLen, cutArg(name))
	}
	for _, reserved := range reservedAgentNames {
		if strings.EqualFold(name, reserved) {
			return errInvalidInput("an agent may not be named %q: you, agent, capture, chrome and os are reserved labels: choose another name with --as NAME", name)
		}
	}
	return nil
}

// namedAgent is the agent that runs a command of the agent's: one that says its name (callerFor's
// agent), with a name the store takes.
func namedAgent(caller taskCaller) (tasks.Actor, error) {
	actor, err := caller.agent()
	if err != nil {
		return tasks.Actor{}, err
	}
	if err := agentNameError(actor.Name); err != nil {
		return tasks.Actor{}, err
	}
	return actor, nil
}

// pasteName is name as it is written after --as in a command to paste: the name itself when a shell
// reads it as one word, else the placeholder <name>, as taskCommand writes it. The store takes a name
// that starts with #, which a shell reads as the start of a comment: the command pasted with it would
// lose everything after it.
func pasteName(name string) string {
	if pasteableName.MatchString(name) {
		return name
	}
	return "<name>"
}

// continueHelp tells an agent how to carry on with a task it holds. Every
// command names the profile: the active profile can change under a session.
func continueHelp(w io.Writer, p tasks.Profile, t tasks.Task, name string) {
	cli := "monoagentcli --profile " + p.ID + " task"
	as := pasteName(name)
	fmt.Fprintf(w, "\nWork it, then hand it back. Use the same name (%s) for every call:\n", name)
	fmt.Fprintf(w, "  report progress   %s comment %d --as %s \"what you did\"\n", cli, t.ID, as)
	fmt.Fprintf(w, "  done              %s finish %d --as %s --result \"what you did\"\n", cli, t.ID, as)
	fmt.Fprintf(w, "  need an answer    %s finish %d --as %s --question \"what you need to know\"\n", cli, t.ID, as)
	fmt.Fprintf(w, "  give it back      %s release %d --as %s --note \"why\"\n", cli, t.ID, as)
}

// printClaimed prints a task an agent has just claimed.
func printClaimed(w io.Writer, p tasks.Profile, t tasks.Task, name string) {
	fmt.Fprintf(w, "Claimed #%d (%s): %s\n", t.ID, heldNote(t), t.Title)
	if t.Source.URL != "" {
		fmt.Fprintf(w, "Link: %s\n", t.Source.URL)
	}
	printNotes(w, t.Notes) // Task 9's helper: the notice, the notes indented, an end line
	continueHelp(w, p, t, name)
}

// printNext prints the task an agent would take, and how to take it.
func printNext(w io.Writer, p tasks.Profile, t tasks.Task) {
	fmt.Fprintf(w, "Next task: #%d %s   [%s, from %s]\n", t.ID, t.Title, t.Status, t.Source.Kind)
	if t.Source.URL != "" {
		fmt.Fprintf(w, "Link: %s\n", t.Source.URL)
	}
	printNotes(w, t.Notes) // Task 9's helper: the notice, the notes indented, an end line
	cli := "monoagentcli --profile " + p.ID + " task"
	fmt.Fprintf(w, "\nTake it:\n  %s next --claim --as <your-name>\n  %s claim %d --as <your-name>\n", cli, cli, t.ID)
}

func newTaskNextCmd(cfg *globalConfig) *cobra.Command {
	var claim bool
	var lease time.Duration
	cmd := &cobra.Command{
		Use:   "next [--claim --as NAME [--lease 30m]]",
		Short: "Show the task an AI agent should work next, or claim it",
		Long: `The task is the top of Ready (an approved task), else a claim whose lease has run
out. Without --claim it only looks: two agents that look may see the same task.
With --claim and --as NAME it takes the task for you in one step, so two agents
never get the same one. A claim lasts 30 minutes by default (--lease, at most
24h) and is renewed by your comments. Nothing ready: the task is null.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			caller := callerFor(flagAs(cmd))
			actor := caller.actor
			if claim {
				var err error
				if actor, err = namedAgent(caller); err != nil {
					return err
				}
			}
			if len(args) != 0 {
				return errInvalidInput("task next takes no arguments (got %q): its options are --claim and --lease", cutArg(args[0]))
			}
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				t, err := store.Next(ctx, p.ID, actor, claim, lease)
				if err != nil {
					return taskErr(err)
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{"profile": p, "task": t})
				}
				w := cmd.OutOrStdout()
				printProfileLine(w, p)
				switch {
				case t == nil:
					fmt.Fprintln(w, "Nothing is ready.")
				case claim:
					printClaimed(w, p, *t, actor.Name)
				default:
					printNext(w, p, *t)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&claim, "claim", false, "Take the task for yourself (needs --as NAME)")
	cmd.Flags().DurationVar(&lease, "lease", 0, "How long you hold it, renewed by your comments (default 30m, at most 24h)")
	return cmd
}

func newTaskClaimCmd(cfg *globalConfig) *cobra.Command {
	var lease time.Duration
	cmd := &cobra.Command{
		Use:   "claim ID --as NAME [--lease 30m]",
		Short: "Take a ready task (an AI agent), or renew your hold on one",
		RunE: func(cmd *cobra.Command, args []string) error {
			actor, err := namedAgent(callerFor(flagAs(cmd)))
			if err != nil {
				return err
			}
			if len(args) != 1 {
				return errInvalidInput("task claim takes one task id, written 42 or #42 (got %d arguments)", len(args))
			}
			taskID, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				t, err := store.Claim(ctx, p.ID, taskID, actor, lease)
				if err != nil {
					return taskErr(err)
				}
				if cfg.JSONOutput {
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{"profile": p, "task": t})
				}
				printProfileLine(cmd.OutOrStdout(), p)
				printClaimed(cmd.OutOrStdout(), p, t, actor.Name)
				return nil
			})
		},
	}
	cmd.Flags().DurationVar(&lease, "lease", 0, "How long you hold it (default 30m, at most 24h)")
	return cmd
}

func newTaskCommentCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "comment ID TEXT... [--as NAME]",
		Short: "Add a note to a task: your own, or an AI agent's progress report (renews its lease)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// The comment is the operator's only when nothing says that an agent runs the command:
			// not a marker, not --as (a blank one too), not MONOAGENT_ACTOR. An agent's comment is its own.
			caller := callerFor(flagAs(cmd))
			actor := caller.actor
			if caller.isAgent() {
				var err error
				if actor, err = namedAgent(caller); err != nil {
					return err
				}
			}
			if len(args) < 2 {
				return errInvalidInput("task comment takes a task id and the text of the comment, as in task comment 42 \"what you did\" (got %d arguments)", len(args))
			}
			taskID, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			text := strings.Join(args[1:], " ")
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				t, err := store.Comment(ctx, p.ID, taskID, text, actor)
				if err != nil {
					return taskErr(err)
				}
				return writeOneTask(cfg, cmd, p, t, "Noted")
			})
		},
	}
}

func newTaskFinishCmd(cfg *globalConfig) *cobra.Command {
	var result, question string
	cmd := &cobra.Command{
		Use:   "finish ID --as NAME (--result TEXT | --question TEXT)",
		Short: "Hand a task you hold back for review, with a result or a question",
		Long: `The task goes to Review for you to read. Give exactly one of --result (what you
did) or --question (what you need to know before you can go on).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			actor, err := namedAgent(callerFor(flagAs(cmd)))
			if err != nil {
				return err
			}
			if len(args) != 1 {
				return errInvalidInput("task finish takes one task id, written 42 or #42 (got %d arguments): what you did goes in --result, what you need to know in --question", len(args))
			}
			taskID, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			// The flags that were given count, not their text: --result "" --question "q" is two, however the store
			// would read the text. (A single flag with no text is the store's to refuse.)
			if cmd.Flags().Changed("result") == cmd.Flags().Changed("question") {
				return errInvalidInput("give exactly one of --result TEXT (what you did) and --question TEXT (what you need to know before you can go on)")
			}
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				t, err := store.Finish(ctx, p.ID, taskID, tasks.Outcome{Result: result, Question: question}, actor)
				if err != nil {
					return taskErr(err)
				}
				return writeOneTask(cfg, cmd, p, t, "Handed back")
			})
		},
	}
	cmd.Flags().StringVar(&result, "result", "", "What you did")
	cmd.Flags().StringVar(&question, "question", "", "What you need to know before you can go on")
	return cmd
}

func newTaskReleaseCmd(cfg *globalConfig) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "release ID --as NAME [--note TEXT]",
		Short: "Give a task you hold back to Ready, behind the others",
		RunE: func(cmd *cobra.Command, args []string) error {
			actor, err := namedAgent(callerFor(flagAs(cmd)))
			if err != nil {
				return err
			}
			if len(args) != 1 {
				return errInvalidInput("task release takes one task id, written 42 or #42 (got %d arguments): the reason goes in --note", len(args))
			}
			taskID, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			return withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				t, err := store.Release(ctx, p.ID, taskID, note, actor)
				if err != nil {
					return taskErr(err)
				}
				return writeOneTask(cfg, cmd, p, t, "Released")
			})
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Why you are giving it back")
	return cmd
}

func newTaskDigestCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "digest",
		Short: "A short line about the ready tasks, for a session-start hook (prints nothing when there are none)",
		Long: `Prints nothing when the profile has no ready task, otherwise two short lines: the
counts, the next task and the command to take it. It always exits 0, so a hook
can call it: monoagentcli --profile <id> task digest`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A hook must never fail on a digest: what goes wrong, a mistake in the arguments too, is
			// one line on standard error, and nothing is printed on standard output.
			if len(args) != 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "task digest: takes no arguments (got %q)\n", cutArg(args[0]))
				return nil
			}
			err := withTasks(cfg, cmd, func(ctx context.Context, store *tasks.Store, p tasks.Profile) error {
				c, err := store.Counts(ctx, p.ID)
				if err != nil {
					return err
				}
				var next *tasks.Task
				if c.Ready > 0 {
					if next, err = store.Next(ctx, p.ID, tasks.Actor{Kind: tasks.Agent}, false, 0); err != nil {
						return err
					}
				}
				if cfg.JSONOutput {
					var n any
					if next != nil {
						n = map[string]any{"id": next.ID, "title": next.Title}
					}
					return writeJSONTo(cmd.OutOrStdout(), map[string]any{"profile": p, "ready": c.Ready, "next": n})
				}
				if next == nil {
					return nil
				}
				w := cmd.OutOrStdout()
				fmt.Fprintf(w, "MonoAgent task board (%s): %d ready, %d in progress, %d to review. Next: #%d %s\n",
					p.Name, c.Ready, c.InProgress, c.Review, next.ID, taskCut(next.Title, 80))
				fmt.Fprintf(w, "Take it with: monoagentcli --profile %s task next --claim --as <your-name>\n", p.ID)
				return nil
			})
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "task digest: %v\n", err)
			}
			return nil
		},
	}
}
