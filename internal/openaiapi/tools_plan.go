package openaiapi

import "context"

// How the turn of a request with tools starts. The first request of a
// conversation has nothing to continue. A follow-up that carries a tool result
// continues the runtime's session when the leg that made the call left a record
// that fits, and otherwise starts from the transcript, which is always possible:
// replay is the way every follow-up can be served, and resume only saves it its
// cost (the spike measured a resumed leg at half the price of a replayed one on
// claude, and a few seconds faster).
const (
	legFirst  = "first"
	legResume = "resume"
	legReplay = "replay"
)

// legPlan is how a request's turn starts: its kind (legFirst, legResume or
// legReplay), the prompt, and for a resume the runtime session to continue.
type legPlan struct {
	Kind    string
	Prompt  string
	Session string
}

// planLeg decides how the turn of a request that declares tools starts. m is the
// model it runs on and firstPrompt the prompt of a conversation without tool
// history. The request's model has already passed the key's policy, so a record
// never widens what the key may use: it only has to fit it.
//
// A session is continued only when all of these hold, and a replay serves every
// other case:
//   - the conversation ends in a tool round, and the assistant message that made
//     the calls made one call, which the tool messages after it answer (a leg
//     returns one call, so a message with several calls did not come from here);
//   - a record of that call exists, has not expired and has not been used (it is
//     used up by this plan);
//   - it belongs to the same key and profile, the same model, the same function
//     and the same declared tools: a resumed codex session does not hear a new
//     tool list, and another model cannot continue the session.
func (g *Gateway) planLeg(pr Principal, req *ChatRequest, m ModelInfo, firstPrompt string) legPlan {
	if !req.hasToolHistory() {
		return legPlan{Kind: legFirst, Prompt: firstPrompt}
	}
	replay := legPlan{Kind: legReplay, Prompt: replayPrompt(req, true)}
	ai, ok := trailingRound(req)
	if !ok {
		return replay
	}
	calls := req.Messages[ai].ToolCalls
	if len(calls) != 1 || !answersOnly(req.Messages[ai+1:], calls[0].ID) {
		return replay
	}
	hash := toolsHash(req.toolDecls)
	rec, ok := g.conts.take(calls[0].ID, func(r contRecord) bool {
		return r.KeyID == pr.KeyID && r.ProfileID == pr.ProfileID && r.Model == m.ID &&
			r.Name == calls[0].Function.Name && r.ToolsHash == hash
	})
	if !ok {
		return replay
	}
	return legPlan{Kind: legResume, Prompt: resumePrompt(req, ai), Session: rec.Session}
}

// plannedLeg is a leg that ran as a plan said, or as the replay that took the
// place of a resume the runtime could not continue.
type plannedLeg struct {
	legResult
	// Kind is how the leg that produced the result started: legReplay when it
	// fell back, whatever the plan was.
	Kind     string
	FellBack bool
}

// runPlanned runs the leg a plan describes. A resume that the runtime could not
// continue (resumeFailed) is run again from the transcript, once, in the same
// request: the caller never learns that the session was gone, and replay is the
// path that always works. t carries the tools and the options of the leg; the
// prompt and the session are the plan's.
func (g *Gateway) runPlanned(ctx context.Context, t turn, plan legPlan, req *ChatRequest) plannedLeg {
	t.Prompt, t.Resume = plan.Prompt, plan.Session
	lr := g.runLeg(ctx, t)
	if plan.Kind == legResume && lr.resumeFailed() {
		t.Prompt, t.Resume = replayPrompt(req, true), ""
		return plannedLeg{legResult: g.runLeg(ctx, t), Kind: legReplay, FellBack: true}
	}
	return plannedLeg{legResult: lr, Kind: plan.Kind}
}

// answersOnly reports whether every tool message among msgs answers the call id.
func answersOnly(msgs []Message, id string) bool {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != id {
			return false
		}
	}
	return true
}
