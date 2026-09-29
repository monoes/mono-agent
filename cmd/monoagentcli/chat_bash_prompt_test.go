package main

import (
	"regexp"
	"strings"
	"testing"
)

// promptCommand matches a command the Bash prompt shows: a binary name
// followed by its first argument (a bare mention such as "monomind"/"x"
// or the "monomind" binary is quoted and does not match).
var promptCommand = regexp.MustCompile(`\b(?:monomind|monoagentcli) [^\s"]+`)

// allowedBashCommand reports whether cmd starts with one of
// chatBashPrefixes, as monomind's literal canUseTool match decides.
func allowedBashCommand(cmd string) bool {
	for _, p := range chatBashPrefixes {
		if cmd == p || strings.HasPrefix(cmd, p+" ") {
			return true
		}
	}
	return false
}

// #247: the prompt's workflow example put --profile before the subcommand,
// which the literal prefix allowlist always denied. Every command the
// prompt shows must be one the allowlist runs.
func TestChatBashPromptCommandsAreAllowed(t *testing.T) {
	prompt := chatBashPrompt("/home/u/project", "prof-123")
	cmds := promptCommand.FindAllString(prompt, -1)
	if len(cmds) < 5 {
		t.Fatalf("found only %d commands in the prompt: %v", len(cmds), cmds)
	}
	for _, c := range cmds {
		if !allowedBashCommand(c) {
			t.Errorf("prompt shows %q, which the Bash allowlist %v denies", c, chatBashPrefixes)
		}
	}
	for _, want := range []string{
		"monoagentcli workflow create <name> --profile prof-123",
		`monoagentcli org create-json <name> --project "/home/u/project"`,
		`with --project "/home/u/project" on every call`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	// The checker itself rejects the form #247 reported.
	if allowedBashCommand("monoagentcli --profile") || !allowedBashCommand("monoagentcli workflow") {
		t.Fatal("allowedBashCommand is wrong")
	}
	if strings.Contains(prompt, "%!") {
		t.Fatalf("prompt has a formatting error: %s", prompt)
	}
}
