package openaiapi

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// toolRun is what the answer to a request with tools needs besides the net/http
// request: who asked, what the request says, the model it runs on and the policy
// it is held to, the id of the completion, the turn (with the tools, the options
// of a leg and the system prompt) and the plan for how it starts.
type toolRun struct {
	pr   Principal
	req  *ChatRequest
	m    ModelInfo
	eff  Policy
	id   string
	t    turn
	plan legPlan
}

// toolLog is what the log line of a request with tools adds to the usual one: how
// many tools the request declared, how the leg that answered started and how many
// calls did not match their schema. Never a name, an argument or a result.
type toolLog struct {
	tools   int
	leg     string
	badArgs int
}

func (l toolLog) String() string {
	if l.leg == "" {
		return ""
	}
	s := fmt.Sprintf(" tools=%d leg=%s", l.tools, l.leg)
	if l.badArgs > 0 {
		s += fmt.Sprintf(" badargs=%d", l.badArgs)
	}
	return s
}

// toolChat answers a chat completion that declares tools: it runs one leg of the
// conversation, a first leg, a resume or a replay, and answers with the tool call
// the leg ended at, or with the model's answer when it called nothing. It returns
// what handleChat logs. The request has been validated, and its model resolved,
// allowed and found able to serve tools (a named model is refused before a slot
// is taken, and the candidates of auto are only models that serve them); t is the
// turn chat would run, with the slot and the system prompt.
func (g *Gateway) toolChat(w http.ResponseWriter, r *http.Request, pr Principal, req *ChatRequest, t turn, m ModelInfo, eff Policy, id string) (int, string, toolLog) {
	t.Tools = toolSpecs(req.toolDecls)
	t.Access, t.MaxTurns = legAccess(m), toolLegMaxTurns
	// Every leg requires the sandbox, a chat-only runtime's too: under it monomind lets
	// only the prefixed names of the declared functions through, and without it a
	// function called Bash would open the runtime's own Bash. Exec refuses the leg
	// (ErrSandboxRequired) rather than run it without.
	t.RequireSandbox = true
	pick := req.toolPick
	pick.Name = req.wireName(pick.Name) // the model knows the function by its alias, if it has one
	if line := toolChoiceLine(pick); line != "" {
		t.System = strings.TrimSpace(t.System + "\n\n" + line)
	}
	run := toolRun{pr: pr, req: req, m: m, eff: eff, id: id, t: t, plan: g.planLeg(pr, req, m, t.Prompt)}
	if run.plan.Kind == legResume {
		// A resume the runtime cannot continue is run again from the transcript: a
		// second turn, and the response has the time of both.
		extendWriteDeadline(w, 2*g.cfg.TurnTimeout+3*turnGrace)
	}
	if req.Stream {
		return g.streamToolLeg(w, r, run, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
	}
	return g.answerToolLeg(w, r, run)
}

// answerToolLeg runs the leg and answers with a plain JSON response.
func (g *Gateway) answerToolLeg(w http.ResponseWriter, r *http.Request, run toolRun) (int, string, toolLog) {
	pl := g.runPlanned(r.Context(), run.t, run.plan, run.req)
	tl := toolLog{tools: len(run.req.toolDecls), leg: pl.Kind}
	e, gone := g.legError(r.Context(), pl.legResult, run.m, run.eff)
	switch {
	case gone:
		return 499, "", tl
	case e != nil:
		g.keepSession(run.plan, pl)
		writeError(w, e)
		return e.Status, e.detail, tl
	}
	if pl.Res.SandboxStatus != "" {
		w.Header().Set("X-Monoagent-Sandbox", pl.Res.SandboxStatus)
	}
	if pl.Call == nil {
		writeJSON(w, http.StatusOK, completion{
			ID: run.id, Object: "chat.completion", Created: time.Now().Unix(), Model: run.m.ID,
			Choices: []completionChoice{{Message: assistantMessage{Role: "assistant", Content: pl.Res.ResultText}, FinishReason: finishReason(pl.Res)}},
			Usage:   usageFrom(pl.Res),
		})
		return http.StatusOK, "", tl
	}
	call, bad := g.rememberCall(run, pl)
	tl.badArgs = bad
	var said *string
	if strings.TrimSpace(pl.Text) != "" {
		said = &pl.Text
	}
	// A leg that ended at a call carries no usage: the runtimes report one usage
	// event per turn, and a cancelled turn's is not reliable.
	writeJSON(w, http.StatusOK, toolCompletion{
		ID: run.id, Object: "chat.completion", Created: time.Now().Unix(), Model: run.m.ID,
		Choices: []toolCompletionChoice{{Message: toolMessage{Role: "assistant", Content: said, ToolCalls: []wireToolCall{call}}, FinishReason: "tool_calls"}},
	})
	return http.StatusOK, "", tl
}

// rememberCall gives the call the leg ended at an id, as the client will see it,
// and keeps the runtime's session under that id so that the follow-up can continue
// it. A leg that reported no session leaves no record, and the follow-up replays.
// It also counts the call when it does not match its declared schema: such a call
// is returned all the same, and the client decides.
func (g *Gateway) rememberCall(run toolRun, pl plannedLeg) (call wireToolCall, badArgs int) {
	name := run.req.declaredName(pl.Call.Name) // the client knows the function by the name it declared
	call = wireToolCall{ID: newRequestID("call_"), Type: "function",
		Function: wireToolFunc{Name: name, Arguments: compactArgs(pl.Call.Args)}}
	if sid := pl.Res.SessionID; sid != "" {
		g.conts.put(contRecord{CallID: call.ID, KeyID: run.pr.KeyID, ProfileID: run.pr.ProfileID, Model: run.m.ID,
			Name: name, Session: sid, ToolsHash: toolsHash(run.req.toolDecls), Convo: convoHash(run.m.Runtime, run.req, len(run.req.Messages)),
			Args: argsHash(call.Function.Arguments)})
	}
	badArgs = 1
	for _, d := range run.req.toolDecls {
		if d.Name == name {
			if argsMatch(d, pl.Call.Args) {
				badArgs = 0
			}
			break
		}
	}
	return call, badArgs
}
