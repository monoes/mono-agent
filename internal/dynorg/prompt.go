package dynorg

import (
	"fmt"
	"strings"
)

// Caps on what goes into a worker's system prompt.
const (
	maxAgentBody = 12000
	maxSkillText = 6000
)

// profileRules is what each access profile tells the worker it may do.
var profileRules = map[string]string{
	ProfileCoding: "You have full access to this folder: run commands, edit and create files, and use the web.",
	ProfileQA: "You have full access to this folder and the web. You can also drive the user's browser through " +
		"`monoagentcli` on your PATH: `monoagentcli ref commands` lists what it can do (`node run` for browser.* " +
		"nodes, `extension` for the browser extension, `login` for sites). Test like a user would and report evidence.",
	ProfileAutomation: "You have full access to this folder and the web, and to mono-agent's workflows and browser " +
		"automations through `monoagentcli` on your PATH (`monoagentcli ref commands`, `workflow`, `automation`, `node`, " +
		"`extension`).",
	ProfileResearch: "You read and report: read files, search, and use the web. Do not edit, create or delete files, " +
		"and run only read-only commands.",
}

// workerSystemPrompt is a worker's system prompt: its agent definition,
// its skills, and the worker contract.
func workerSystemPrompt(st Staff, cwd string, files []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, a worker in a team led by the lead agent of a mono-agent coder chat. ", st.Role)
	fmt.Fprintf(&b, "You work in %s.\n\n", cwd)
	if body := strings.TrimSpace(st.AgentBody); body != "" {
		b.WriteString("## Your role\n\n")
		b.WriteString(clipText(body, maxAgentBody))
		b.WriteString("\n\n")
	}
	for _, sk := range st.Skills {
		fmt.Fprintf(&b, "## Skill: %s\n\n", sk.Name)
		if t := strings.TrimSpace(sk.Text); t != "" {
			b.WriteString(clipText(t, maxSkillText))
		} else {
			fmt.Fprintf(&b, "Use the %s skill if it is available to you.", sk.Name)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("## How you work\n\n")
	b.WriteString("- " + profileRules[st.Access] + "\n")
	if len(files) > 0 {
		b.WriteString("- Stay within these files unless the brief needs more: " + strings.Join(files, ", ") + "\n")
	}
	b.WriteString("- Do only what the brief asks. Other workers may be working in the same folder at the same time; don't undo their changes.\n")
	b.WriteString("- You can't hand work to other agents and can't change anyone's access.\n")
	b.WriteString("- Never read, send or change the user's messages or people records.\n")
	b.WriteString("- Finish with a report for the lead of at most 200 words: what you did, what you found, and, if you changed files, the list of files.\n")
	return b.String()
}

// LeadPrompt is added to the lead's system prompt when its chat runs as a
// dynamic org.
func LeadPrompt(l Limits) string {
	budget := ""
	if l.BudgetUSD > 0 {
		budget = fmt.Sprintf(", and $%.2f of worker cost", l.BudgetUSD)
	}
	return "\n\nYou lead a dynamic org. Besides your own tools you have org tools to bring in workers: " +
		"org_roster (the models, roles and access profiles you can use), org_spawn (start a worker with a brief), " +
		"org_wait (wait for workers and read their reports), org_message (a follow-up to a finished worker), " +
		"org_stop and org_rate (rate a worker's result). Use workers when the task splits into parts that can run in parallel or need a different " +
		"specialty (review, testing, research); do small things yourself. " +
		"You decide each worker's brief and may choose its role, skills, model, effort and access profile " +
		"(coding, qa, automation, research); leave any of them out and they are picked for you. " +
		"Only one worker edits files at a time (the others queue), so run research and review workers in parallel and " +
		"give writers separate files. Workers can't spawn workers. " +
		"Your own file edits take the same write lease: writers wait while you edit, and you must not edit files " +
		"while a writing worker runs; org_wait for it first. " +
		fmt.Sprintf("This turn allows %d workers, %d running at once%s. ", l.MaxAgents, l.MaxConcurrent, budget) +
		"Prefer org_spawn over your runtime's own subagent tool: org workers can use other models and the user sees them. " +
		"After you read a worker's report, org_rate it once: good if it did the job, bad if it didn't; ratings teach staffing which models fit which work. " +
		"Always org_wait for the workers you started before you finish, then tell the user what the team did."
}

func clipText(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n…(cut)"
}
