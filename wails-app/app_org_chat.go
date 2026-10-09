package main

import "fmt"

// Org bubbles (#229): chatting with a running org's boss. Every binding
// shells out to `monoagentcli org chat …` (or `org stop|pause|resume`),
// which owns the thread, idempotency, and the refusal once an org stopped.
// Every value the page hands over goes after "--", so none of them can be
// read as a flag (a ref of "--project=/elsewhere" would otherwise pick
// another folder's org).

// GetOrgChatHistory returns the boss thread (`org chat history`).
func (a *App) GetOrgChatHistory(org, run string) string {
	if run != "" {
		return a.runOrgCLI("chat", "history", "--run="+run, "--", org)
	}
	return a.runOrgCLI("chat", "history", "--", org)
}

// SendOrgChat sends the boss a message as the person.
func (a *App) SendOrgChat(org, text string) string {
	return a.runOrgCLI("chat", "send", "--", org, text)
}

// AnswerOrgChat answers one of the org's questions.
func (a *App) AnswerOrgChat(org, questionID, answer string) string {
	return a.runOrgCLI("chat", "answer", "--", org, questionID, answer)
}

// DismissOrgChat closes one of the org's questions without answering it;
// reason is optional.
func (a *App) DismissOrgChat(org, questionID, reason string) string {
	if reason == "" {
		return a.runOrgCLI("chat", "dismiss", "--", org, questionID)
	}
	return a.runOrgCLI("chat", "dismiss", "--reason="+reason, "--", org, questionID)
}

// ResolveOrgChat approves or denies an approval request or a gate; note is
// a gate's resolution.
func (a *App) ResolveOrgChat(org, ref string, approve bool, note string) string {
	verb := "deny"
	if approve {
		verb = "approve"
	}
	if note == "" {
		return a.runOrgCLI("chat", verb, "--", org, ref)
	}
	return a.runOrgCLI("chat", verb, "--", org, ref, note)
}

// ControlOrg pauses, resumes or stops a running org.
func (a *App) ControlOrg(org, verb string) string {
	switch verb {
	case "stop", "pause", "resume":
		return a.runOrgCLI(verb, "--", org)
	}
	return aiError(fmt.Errorf("unknown org control %q", verb))
}
