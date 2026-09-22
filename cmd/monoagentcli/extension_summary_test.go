package main

import "testing"

func TestConfiguredSummaryRuntimePrecedence(t *testing.T) {
	t.Cleanup(func() { summaryRuntimeFlag = "" })

	summaryRuntimeFlag = ""
	t.Setenv(summaryRuntimeEnv, "")
	if got := configuredSummaryRuntime(); got != "claude" {
		t.Errorf("default = %q, want claude", got)
	}
	t.Setenv(summaryRuntimeEnv, "codex")
	if got := configuredSummaryRuntime(); got != "codex" {
		t.Errorf("env = %q, want codex", got)
	}
	summaryRuntimeFlag = "opencode"
	if got := configuredSummaryRuntime(); got != "opencode" {
		t.Errorf("flag = %q, want opencode (the flag beats the env)", got)
	}
}

func TestCheckSummaryChoice(t *testing.T) {
	ok := []struct{ mode, runtime, model string }{
		{"full", "", ""},
		{"summary", "claude", ""},
		{"video", "", "claude-haiku-4-5-20251001"},
		{"summary", "codex", "gpt-5.1-codex"},
	}
	for _, c := range ok {
		if err := checkSummaryChoice(c.mode, c.runtime, c.model); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []struct{ mode, runtime, model string }{
		{"full", "claude", ""},         // no summary to write
		{"", "", "claude-sonnet-5"},    // likewise
		{"summary", "Claude Code", ""}, // not an id
		{"summary", "claude", "--dangerously-skip-permissions"},
	}
	for _, c := range bad {
		if err := checkSummaryChoice(c.mode, c.runtime, c.model); err == nil {
			t.Errorf("%+v: accepted", c)
		}
	}
}
