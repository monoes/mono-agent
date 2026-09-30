package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
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

// controlID is what a turn or agent id must look like to name a file.
var controlID = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// agentControlDir is turnID's mailbox folder.
func agentControlDir(cfg *globalConfig, turnID string) string {
	return filepath.Join(filepath.Dir(expandPath(cfg.DBPath)), "chat-control", turnID)
}

// watchAgentStops starts polling dir for stop requests and calls stop for
// each; the returned func ends the watch and removes dir. A request left
// over from an earlier run of the same turn id is dropped first.
func watchAgentStops(dir string, stop func(agentID string)) func() {
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}
	}
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
}

const agentStatusUnknown = "unknown"

// turnActive is a running turn's status (ai.Turn.Status).
const turnActive = "active"

func newChatTurnCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "turn",
		Short: "Control a running chat turn",
	}
	cmd.AddCommand(newChatTurnStopCmd(cfg))
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
	t, status, err := agentStatusInTurn(store, profileID, conversationID, turnID, agentID)
	if err != nil {
		return res, err
	}
	res.TurnStatus, res.Status = t.Status, status
	if t.Status != turnActive || agentFinished(status) {
		if status == "" {
			res.Status = agentStatusUnknown
		}
		return res, nil
	}

	req := filepath.Join(dir, agentStopPrefix+agentID)
	if err := os.WriteFile(req, nil, 0o600); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No mailbox: the turn runs solo, or on a build without it.
			if status == "" {
				res.Status = agentStatusUnknown
				return res, nil
			}
			return res, errInvalidInput("turn %s can't stop a single worker (it has no control folder)", turnID)
		}
		return res, fmt.Errorf("request the stop: %w", err)
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
			if status == "" {
				res.Status = agentStatusUnknown
			}
			return res, nil
		case taken && status == "":
			res.Status = agentStatusUnknown
			return res, nil
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
