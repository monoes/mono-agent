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
// lets go of the worker's leases while it waits, and polls Config.Answers
// until the user answers (`chat history answer`), the question times out,
// or the turn ends.

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
	"(at most 3 per task) and wait for the answer; otherwise decide yourself and say what you assumed.\n"

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
	if w.questions >= MaxQuestions {
		c.mu.Unlock()
		return "", fmt.Errorf("you already asked %d questions; decide yourself and say what you assumed", MaxQuestions)
	}
	w.questions++
	qid := "q" + strconv.Itoa(w.questions)
	c.mu.Unlock()

	// Nobody edits while it waits: its leases go to others meanwhile.
	held := c.releaseLeases(w)
	// Status first: the stage clears "needs you" on the next status change.
	c.setStatus(w, chatevents.AgentWaitingUser, qid)
	bounded, cut, _ := chatevents.BoundText(question, 2000)
	c.cfg.Emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
		AgentID: w.id, Direction: "question", QuestionID: qid, From: w.id, To: "user", Text: bounded, Truncated: cut,
	})

	answer, answered, err := c.waitAnswer(ctx, w.id, qid)
	if answered {
		c.emitAnswer(w.id, qid, answer)
	}
	if lerr := c.retakeLeases(ctx, w, held); lerr != nil {
		return "", lerr
	}
	c.setStatus(w, chatevents.AgentWorking, "")
	switch {
	case err != nil:
		return "", err
	case !answered:
		return fmt.Sprintf("The user didn't answer within %s. Go on with your best judgment and say in your report what you assumed.", AskTimeout), nil
	}
	return "The user answered: " + answer, nil
}

func (c *Conductor) emitAnswer(agentID, qid, answer string) {
	bounded, cut, _ := chatevents.BoundText(answer, maxReport)
	c.cfg.Emit.Emit(chatevents.EventAgentMessage, chatevents.AgentMessagePayload{
		AgentID: agentID, Direction: "followup", QuestionID: qid, From: "user", To: agentID, Text: bounded, Truncated: cut,
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

// releaseLeases lets go of the leases w holds and returns them.
func (c *Conductor) releaseLeases(w *worker) []*lease {
	c.mu.Lock()
	held := w.leases
	w.leases = nil
	c.mu.Unlock()
	for _, l := range held {
		l.release()
	}
	return held
}

// retakeLeases takes the leases back, in their original order.
func (c *Conductor) retakeLeases(ctx context.Context, w *worker, held []*lease) error {
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
	return nil
}
