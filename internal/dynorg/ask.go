package dynorg

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// A worker asking the user a question (#256). A worker whose exec can take
// caller tools gets ask_user. The conductor journals the question
// (agent.status waiting_user, then agent.message direction "question"),
// lets go of the worker's leases and slot while it waits, and polls
// Config.Answers until the user answers (`chat turn answer`), the question
// times out, or the worker is stopped. Every way a question ends is
// journaled as a followup with its questionId, from "user" or "system".
//
// Limits of the lease hand-off: background processes the worker started
// keep running while it waits, and a tool call it makes in the same
// message as ask_user isn't held back by the lease it gave up.

// ToolAskUser is the worker's question tool.
const ToolAskUser = "ask_user"

// MaxQuestions caps a worker's questions per run.
const MaxQuestions = 3

// AskTimeout bounds one question; unanswered, the worker is told to go on
// with its best judgment.
var AskTimeout = 10 * time.Minute

// askPoll is how often an open question checks for an answer.
var askPoll = time.Second

// AnswerSource hands the conductor the user's answer to a question once it
// has arrived.
type AnswerSource interface {
	Answer(ctx context.Context, agentID, questionID string) (text string, ok bool, err error)
}

const askRule = "- If a decision only the user can make blocks you, call ask_user with one short question " +
	"(at most 3 per task) on its own, not alongside other tool calls, and wait for the answer; otherwise " +
	"decide yourself and say what you assumed.\n"

func askSpec() monomind.ToolSpec {
	return monomind.ToolSpec{
		Name:        ToolAskUser,
		Description: "Ask the user one short question and wait for the answer. Use it only for a decision you can't make yourself.",
		Schema:      obj(map[string]any{"question": str("The question, one or two sentences.")}, "question"),
	}
}

// workerTools gives the worker ask_user when its exec can take caller
// tools in its access mode and answers can come back; otherwise none.
func (c *Conductor) workerTools(w *worker, m Model, opts *monomind.ExecOptions) {
	opts.Tools, opts.OnToolCall = nil, nil
	canTake := m.CallerTools
	if opts.Access == monomind.AccessFull {
		canTake = m.CallerToolsFull
	}
	if c.cfg.Answers == nil || !canTake {
		return
	}
	opts.Tools = []monomind.ToolSpec{askSpec()}
	opts.OnToolCall = func(ctx context.Context, name string, args json.RawMessage) (string, error) {
		if name != ToolAskUser {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		var a struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(orEmpty(args), &a); err != nil || strings.TrimSpace(a.Question) == "" {
			return "", fmt.Errorf("question is required")
		}
		return c.ask(ctx, w, strings.TrimSpace(a.Question))
	}
	opts.ToolTimeout = AskTimeout + time.Minute
	if m.Runtime == "claude" {
		// Claude Code times out MCP calls on its own; let the wait run.
		env := maps.Clone(opts.Env)
		if env == nil {
			env = map[string]string{}
		}
		env["MCP_TOOL_TIMEOUT"] = strconv.FormatInt(opts.ToolTimeout.Milliseconds(), 10)
		opts.Env = env
	}
	opts.SystemPrompt += askRule
}

// ask journals a worker's question and waits for the user's answer.
func (c *Conductor) ask(ctx context.Context, w *worker, question string) (string, error) {
	c.mu.Lock()
	if w.askedThisRun >= MaxQuestions {
		c.mu.Unlock()
		return "", fmt.Errorf("you already asked %d questions; decide yourself and say what you assumed", MaxQuestions)
	}
	w.askedThisRun++
	w.questionSeq++
	qid := "q" + strconv.Itoa(w.questionSeq)
	w.openQuestion = question
	c.mu.Unlock()

	// While it waits it holds nothing: its leases and its concurrency slot
	// go to others, and it takes them back in the conductor's order
	// (leases, then the slot), so no one can deadlock against it.
	held := c.releaseHeld(w)
	// Status first: the stage clears "needs you" on the next status change.
	c.setStatus(w, chatevents.AgentWaitingUser, qid)
	bounded, cut, _ := chatevents.BoundText(question, 2000)
	c.cfg.Emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
		AgentID: w.id, Direction: "question", QuestionID: qid, From: w.id, To: "user", Text: bounded, Truncated: cut,
	})

	answer, answered, err := c.waitAnswer(ctx, w.id, qid)
	c.mu.Lock()
	w.openQuestion = ""
	c.mu.Unlock()
	switch {
	case answered:
		c.emitAnswer(w.id, qid, "user", answer)
	case err != nil:
		// Stopped while waiting: close the question so a late answer is
		// refused (`chat turn answer` checks the journal).
		c.emitAnswer(w.id, qid, "system", "The worker was stopped before the question was answered.")
		return "", err
	default:
		c.emitAnswer(w.id, qid, "system", fmt.Sprintf("No answer within %s; the worker went on without one.", AskTimeout))
	}
	if err := c.retakeHeld(ctx, w, held); err != nil {
		return "", err
	}
	c.setStatus(w, chatevents.AgentWorking, "")
	if !answered {
		return fmt.Sprintf("The user didn't answer within %s. Go on with your best judgment and say in your report what you assumed.", AskTimeout), nil
	}
	return "The user answered: " + answer, nil
}

// emitAnswer closes a question in the journal: the user's answer, or
// (from "system") why it closed without one.
func (c *Conductor) emitAnswer(agentID, qid, from, text string) {
	bounded, cut, _ := chatevents.BoundText(text, maxReport)
	c.cfg.Emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
		AgentID: agentID, Direction: "followup", QuestionID: qid, From: from, To: agentID, Text: bounded, Truncated: cut,
	})
}

// waitAnswer polls for the answer until it arrives, AskTimeout passes, or
// ctx ends (the turn stopped, or org_stop).
func (c *Conductor) waitAnswer(ctx context.Context, agentID, qid string) (string, bool, error) {
	deadline := time.NewTimer(AskTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(askPoll)
	defer tick.Stop()
	for {
		if text, ok, err := c.cfg.Answers.Answer(ctx, agentID, qid); err == nil && ok {
			return text, true, nil
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-deadline.C:
			return "", false, nil
		case <-tick.C:
		}
	}
}

// releaseHeld lets go of the leases and the concurrency slot w holds and
// returns the leases.
func (c *Conductor) releaseHeld(w *worker) []*lease {
	c.mu.Lock()
	held := w.leases
	w.leases = nil
	c.mu.Unlock()
	for _, l := range held {
		l.release()
	}
	c.dropSlot(w)
	return held
}

// retakeHeld takes the leases back in their original order, then a slot:
// the same order run takes them in.
func (c *Conductor) retakeHeld(ctx context.Context, w *worker, held []*lease) error {
	for _, l := range held {
		if !l.tryAcquire() {
			c.setStatus(w, chatevents.AgentWaitingLease, "")
			if err := l.acquire(ctx); err != nil {
				return err
			}
		}
		c.mu.Lock()
		w.leases = append(w.leases, l)
		c.mu.Unlock()
	}
	select {
	case c.slots <- struct{}{}:
	default:
		c.setStatus(w, chatevents.AgentQueued, "slot")
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	w.hasSlot = true
	c.mu.Unlock()
	return nil
}
