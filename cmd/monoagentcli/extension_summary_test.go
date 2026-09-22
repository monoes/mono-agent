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
