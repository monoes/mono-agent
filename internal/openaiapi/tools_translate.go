package openaiapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/monoes/mono-agent/internal/monomind"
)

// What a turn with tools says to the runtime besides the client's own words.
const (
	// toolOutro ends a transcript that carries tool calls and results, with the
	// wording the spike measured (replay worked 7 of 7 on claude, codex and
	// antigravity). It is not the "last user message only" line of a plain
	// transcript, which is wrong when the transcript ends in a tool result.
	toolOutro      = "Reply as the assistant; do not repeat the conversation. Use the function results above; call a function again only if you still need one."
	plainToolOutro = "Reply as the assistant; do not repeat the conversation. Use the function results above."
	// resumeIntro opens the prompt of a resumed session. When a leg ends at a
	// call the agent CLI is cancelled mid-call, and it writes a rejected result for
	// the call into the session; the model must not take that for what happened.
	resumeIntro = "The caller ran the tool you called, whatever an earlier note in this conversation says about the call being rejected or cancelled. Its result follows. Continue from it."

	// toolLegMaxTurns caps the agent turns of a leg. A leg ends at its first call,
	// so this is only headroom for a model that first tries its own tools, which
	// monomind refuses one turn at a time.
	toolLegMaxTurns = 12
)

// toolChoiceLine is the best-effort instruction behind a tool_choice that
// forces a call: the runtimes' tool bridges have no such option (a named
// function was obeyed 9 of 9 times in the spike, required 5 of 6).
func toolChoiceLine(pick toolChoice) string {
	switch pick.Mode {
	case choiceRequired:
		return "You must call at least one of the available functions before you answer."
	case choiceFunction:
		return fmt.Sprintf("You must call the function %s before you answer. Never answer without calling it.", pick.Name)
	}
	return ""
}

// legAccess is the access mode a tool leg runs with. A runtime whose own tools
// monomind gates (chat-only: claude) needs nothing more. Any other keeps its
// native tools in play, and the spike saw them pull the model away from the
// declared ones: codex made 31 of 31 native attempts, edited files itself, told
// the caller it had succeeded and used the declared tool 0 of 4 times, against
// 2 of 2 with read access. So its leg runs read-only.
func legAccess(m ModelInfo) string {
	if m.Class == ChatOnly {
		return ""
	}
	return monomind.AccessRead
}

// trailingRound finds the tool round a conversation ends in: the index of its
// last assistant message when that message made calls and a tool message follows
// it. It reports false for a conversation that is a first request, one that ends
// in an answer, and one whose calls are not answered.
func trailingRound(req *ChatRequest) (int, bool) {
	ai := -1
	for i, m := range req.Messages {
		if m.Role == "assistant" {
			ai = i
		}
	}
	if ai < 0 || len(req.Messages[ai].ToolCalls) == 0 {
		return -1, false
	}
	for _, m := range req.Messages[ai+1:] {
		if m.Role == "tool" {
			return ai, true
		}
	}
	return -1, false
}

// replayPrompt renders a whole conversation that carries tool calls and results
// as one prompt, for a turn that starts from nothing: a call is the line "(called
// the function NAME with arguments ARGS)" under the assistant's words, and a
// result is "[tool NAME (ID)]" and its text, fenced as data. active says whether
// the turn may call a function again. System messages are not part of it: they are
// the system prompt.
func replayPrompt(req *ChatRequest, active bool) string {
	names := map[string]string{} // call id to function name
	var b strings.Builder
	b.WriteString(transcriptIntro)
	for _, m := range req.Messages {
		switch m.Role {
		case "system", "developer":
		case "assistant":
			b.WriteString("\n\n[assistant]")
			if strings.TrimSpace(m.Content.Text) != "" {
				b.WriteString("\n" + m.Content.Text)
			}
			for _, c := range m.ToolCalls {
				names[c.ID] = c.Function.Name
				fmt.Fprintf(&b, "\n(called the function %s with arguments %s)", c.Function.Name, argumentsText(c.Function.Arguments))
			}
		case "tool":
			fmt.Fprintf(&b, "\n\n[tool %s (%s)]\n%s", names[m.ToolCallID], m.ToolCallID, fenceResult(m.Content.Text))
		default:
			fmt.Fprintf(&b, "\n\n[%s]\n%s", m.Role, m.Content.Text)
		}
	}
	if active {
		b.WriteString("\n\n" + toolOutro)
	} else {
		b.WriteString("\n\n" + plainToolOutro)
	}
	return b.String()
}

var (
	// resultFenceRE matches an opening or closing tag of the fence a result sits
	// in, in any case and spacing.
	resultFenceRE = regexp.MustCompile(`(?i)<\s*/?\s*function_result`)
	// turnMarkerRE matches the start of a line that would open a turn of the
	// transcript: [user], [assistant], [tool NAME (ID)] and the other roles.
	turnMarkerRE = regexp.MustCompile(`(?im)^([ \t]*)\[(user|assistant|tool|system|developer|function)\b`)
)

// fenceResult renders the result of a function as data. A result is whatever the
// client's function returned (a file, a page, an API's answer), so nothing in it
// may close the fence or pass for a turn of the transcript: the tags of the fence
// and the markers at the start of a line are defanged, as the knowledge excerpts'
// are, and the words stay readable.
func fenceResult(text string) string {
	text = resultFenceRE.ReplaceAllStringFunc(text, func(m string) string { return "&lt;" + m[1:] })
	text = turnMarkerRE.ReplaceAllString(text, "${1}&#91;${2}")
	return "<function_result>\n" + text + "\n</function_result>"
}

// argumentsText is the arguments of a call of the request as text: the string
// the client sent, or {} when it sent none.
func argumentsText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil || strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// resumePrompt is what a resumed session is told: the results of the call it
// ended at, fenced as data, and whatever the user said after them. ai is the index
// of the assistant message that made the call (trailingRound). The session already
// holds everything before, so nothing before is repeated.
func resumePrompt(req *ChatRequest, ai int) string {
	names := map[string]string{}
	for _, c := range req.Messages[ai].ToolCalls {
		names[c.ID] = c.Function.Name
	}
	parts := []string{resumeIntro}
	for _, m := range req.Messages[ai+1:] {
		switch m.Role {
		case "tool":
			parts = append(parts, fmt.Sprintf("Result of %s (call %s):\n%s", names[m.ToolCallID], m.ToolCallID, fenceResult(m.Content.Text)))
		case "user":
			parts = append(parts, m.Content.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
