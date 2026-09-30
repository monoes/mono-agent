package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

// Stopping one worker of a running dynamic-org turn (#255) from outside the
// turn's process. The control path is a mailbox folder per turn next to the
// database, <db dir>/chat-control/<turn-id>/: `chat turn stop` drops a
// stop-<agent-id> file there and the turn process, which polls the folder
// while its conductor runs, stops that worker and removes the file. A file
// works from any process of the same user and on every OS (no signal or
// socket), survives nothing it shouldn't (the turn removes its folder when
// it ends), and removing the file is the acknowledgement: the worker's
// status then comes from the turn's journal like every other agent event.

// agentStopPoll is how often a running turn looks for stop requests, and
// how often `chat turn stop` checks the journal while it waits.
var agentStopPoll = 250 * time.Millisecond

const agentStopPrefix = "stop-"

// controlID is what a turn or agent id must look like to name a file. No
// leading "-", so an id never reads as a flag either.
var controlID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

// agentControlPIDFile holds the turn process's pid, so a folder a crashed
// turn left behind can be told from a live one.
const agentControlPIDFile = "pid"

// agentControlSweepAge is how old a folder without a pid file must be
// before a sweep removes it (a turn writes its pid right after creating
// the folder).
const agentControlSweepAge = time.Minute

// agentControlRoot holds every turn's mailbox folder.
func agentControlRoot(cfg *globalConfig) string {
	return filepath.Join(filepath.Dir(expandPath(cfg.DBPath)), "chat-control")
}

// agentControlDir is turnID's mailbox folder.
func agentControlDir(cfg *globalConfig, turnID string) string {
	return filepath.Join(agentControlRoot(cfg), turnID)
}

// turnProcessGone reports whether dir's turn recorded a pid that is no
// longer running: the turn crashed (kill -9) and left its folder behind.
// With no readable pid it can't tell, and says no.
func turnProcessGone(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, agentControlPIDFile))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return err == nil && !daemonhb.ProcessAlive(pid)
}

// sweepAgentControl removes the mailbox folders under root that no running
// turn owns: its pid is gone, or it never got one and is older than
// agentControlSweepAge. `chat history reconcile` (run at app start) calls
// it. It returns how many it removed.
func sweepAgentControl(root string, now time.Time) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		stale := turnProcessGone(dir)
		if _, err := os.Stat(filepath.Join(dir, agentControlPIDFile)); errors.Is(err, os.ErrNotExist) {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > agentControlSweepAge {
				stale = true
			}
		}
		if stale && os.RemoveAll(dir) == nil {
			n++
		}
	}
	return n
}

// watchAgentStops starts polling dir for stop requests and calls stop for
// each; the returned func ends the watch and removes dir. A request left
// over from an earlier run of the same turn id is dropped first.
func watchAgentStops(dir string, stop func(agentID string)) func() {
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
	_ = os.WriteFile(filepath.Join(dir, agentControlPIDFile), []byte(strconv.Itoa(os.Getpid())), 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(agentStopPoll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				id, ok := strings.CutPrefix(e.Name(), agentStopPrefix)
				if !ok || !controlID.MatchString(id) {
					continue
				}
				stop(id)
				// On Windows the remove fails while the requester still has
				// the file open; the next tick sees it again and stops the
				// worker again, which is a no-op.
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}()
	return func() {
		cancel()
		<-done
		_ = os.RemoveAll(dir)
	}
}

// chatAgentStopResult is `chat turn stop --json`'s output.
type chatAgentStopResult struct {
	ConversationID string `json:"conversation_id"`
	TurnID         string `json:"turn_id"`
	AgentID        string `json:"agent_id"`
	// Status is the worker's status after the stop: cancelled, or the
	// status it had already finished with (done, failed); "stopping" when
	// it hadn't finished within --wait; "unknown" when the turn has no
	// such worker.
	Status string `json:"status"`
	// Requested is true when a stop was sent to the running turn; false
	// for a no-op (the worker or the turn had already finished).
	Requested  bool   `json:"requested"`
	TurnStatus string `json:"turn_status"`
	// Detail explains a no-op that isn't obvious from the statuses.
	Detail string `json:"detail,omitempty"`
}

// detailTurnGone is Detail when the turn is still active in the database
// but its process is gone (a crash); `chat history reconcile` finishes it.
const detailTurnGone = "the turn's process is no longer running"

const agentStatusUnknown = "unknown"

// turnActive is a running turn's status (ai.Turn.Status).
const turnActive = "active"

func newChatTurnCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "turn",
		Short: "Control a running chat turn",
	}
	cmd.AddCommand(newChatTurnStopCmd(cfg))
	cmd.AddCommand(newChatTurnAnswerCmd(cfg))
	return cmd
}

func newChatTurnStopCmd(cfg *globalConfig) *cobra.Command {
	var agentID string
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "stop <conversation-id> <turn-id> --agent <agent-id>",
		Short: "Stop one worker of a running dynamic-org turn, leaving the lead and the other workers running",
		Long: "Asks the running `chat --turn` process to cancel one worker, then waits up to --wait for the " +
			"journal to show it finished (agent.status to cancelled, agent.finished with outcome cancelled). " +
			"It works whichever window or process runs the turn. Stopping a worker that already finished, " +
			"or a turn that is no longer running, is a no-op that reports the worker's status.",
		Example: `  monoagentcli chat turn stop 3f2c… turn-7 --agent w2 --json`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return errInvalidInput("--agent is required")
			}
			if !controlID.MatchString(agentID) {
				return errInvalidInput("invalid --agent %q", agentID)
			}
			if !controlID.MatchString(args[1]) {
				return errInvalidInput("invalid turn id %q", args[1])
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			res, err := stopChatAgent(cmd.Context(), store, profileID, agentControlDir(cfg, args[1]), args[0], args[1], agentID, wait)
			if err != nil {
				return err
			}
			if cfg.JSONOutput {
				return printJSON(res)
			}
			switch {
			case res.Status == agentStatusUnknown:
				fmt.Printf("turn %s has no worker %s\n", res.TurnID, res.AgentID)
			case !res.Requested:
				fmt.Printf("worker %s already %s\n", res.AgentID, res.Status)
			default:
				fmt.Printf("worker %s: %s\n", res.AgentID, res.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "", "The worker's agent id (agent.spawned's agentId), e.g. w2")
	cmd.Flags().DurationVar(&wait, "wait", 20*time.Second, "How long to wait for the worker to finish")
	return cmd
}

// stopChatAgent is `chat turn stop`: it sends the request when the worker
// may still be running and waits for the journal to show the outcome.
func stopChatAgent(ctx context.Context, store *ai.AIStore, profileID, dir, conversationID, turnID, agentID string, wait time.Duration) (chatAgentStopResult, error) {
	res := chatAgentStopResult{ConversationID: conversationID, TurnID: turnID, AgentID: agentID}
	// done fills res for a stop that needn't (or can't) go further.
	done := func(t ai.Turn, status, detail string) (chatAgentStopResult, error) {
		res.TurnStatus, res.Status, res.Detail = t.Status, status, detail
		if status == "" {
			res.Status = agentStatusUnknown
		}
		return res, nil
	}
	t, status, err := agentStatusInTurn(store, profileID, conversationID, turnID, agentID)
	if err != nil {
		return res, err
	}
	if t.Status != turnActive || agentFinished(status) {
		return done(t, status, "")
	}
	if turnProcessGone(dir) {
		_ = os.RemoveAll(dir)
		return done(t, status, detailTurnGone)
	}

	req := filepath.Join(dir, agentStopPrefix+agentID)
	if err := os.WriteFile(req, nil, 0o600); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("request the stop: %w", err)
		}
		// No mailbox. The turn may have just ended (its folder goes with
		// it); otherwise it runs solo, or on a build without the mailbox.
		if t, status, err = agentStatusInTurn(store, profileID, conversationID, turnID, agentID); err != nil {
			return res, err
		}
		if t.Status != turnActive || agentFinished(status) || status == "" {
			return done(t, status, "")
		}
		return res, errInvalidInput("turn %s can't stop a single worker (it has no control folder)", turnID)
	}
	res.Requested = true
	deadline := time.Now().Add(wait)
	for {
		t, status, err = agentStatusInTurn(store, profileID, conversationID, turnID, agentID)
		if err != nil {
			return res, err
		}
		res.TurnStatus, res.Status = t.Status, status
		_, statErr := os.Stat(req)
		taken := errors.Is(statErr, os.ErrNotExist)
		switch {
		case agentFinished(status):
			return res, nil
		case t.Status != turnActive:
			// The turn ended; its Close cancelled every worker still running.
			_ = os.Remove(req)
			return done(t, status, "")
		case taken && status == "":
			return done(t, status, "")
		case !taken && turnProcessGone(dir):
			_ = os.RemoveAll(dir)
			return done(t, status, detailTurnGone)
		case time.Now().After(deadline):
			res.Status = "stopping"
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(agentStopPoll):
		}
	}
}

// agentStatusInTurn loads the turn and agentID's latest agent.status ("" when
// the journal never mentions it).
func agentStatusInTurn(store *ai.AIStore, profileID, conversationID, turnID, agentID string) (ai.Turn, string, error) {
	t, err := turnInConversation(store, profileID, conversationID, turnID)
	if err != nil {
		return t, "", err
	}
	status := ""
	for after := int64(0); ; {
		evs, err := store.GetEvents(conversationID, turnID, profileID, after, 1000)
		if err != nil {
			return t, "", err
		}
		for _, ev := range evs {
			after = ev.Seq
			if ev.Type != chatevents.EventAgentStatus {
				continue
			}
			var p chatevents.AgentStatusPayload
			if json.Unmarshal(ev.Payload, &p) == nil && p.AgentID == agentID {
				status = p.To
			}
		}
		if len(evs) < 1000 {
			return t, status, nil
		}
	}
}

func agentFinished(status string) bool {
	switch status {
	case chatevents.AgentDone, chatevents.AgentFailed, chatevents.AgentCancelled:
		return true
	}
	return false
}
