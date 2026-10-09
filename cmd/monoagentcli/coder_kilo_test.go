package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Kilo's runner accepts only --access full --settings user,project,local:
// --effort and --budget-usd would make monomind refuse the turn.
func TestCoderTurnOnKiloSendsNeitherEffortNorBudget(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	bin, argsLog := writeCoderMonomind(t, codexTranscript)
	withCoderCaps(t, allRuntimeCaps...)
	withCoderScan(t, monomind.ScanEntry{ID: "kilo", Installed: true, FullAccess: true, ToolActivityFidelity: "full", Effort: true})
	setCoderSettings(t, dbPath, coderSettings{Enabled: true, BudgetUSD: 3})
	conv, err := openTestChatStore(t, dbPath).CreateConversationModeEffort("default", "agent", "general", "kilo", "", "", ai.ModeCoder, t.TempDir(), "high")
	if err != nil {
		t.Fatal(err)
	}
	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "go")
	if err != nil {
		t.Fatalf("kilo turn: %v\n%s", err, out)
	}
	execLine := lastAgentExecLine(t, argsLog)
	if strings.Contains(execLine, "--effort") || strings.Contains(execLine, "--budget-usd") || !strings.Contains(execLine, "--access full") {
		t.Errorf("exec argv: %s", execLine)
	}
	if !strings.Contains(out, "Kilo takes no effort or budget") {
		t.Errorf("no notice about the dropped options:\n%s", out)
	}
}

func TestDropKiloOptionsLeavesOtherRuntimesAlone(t *testing.T) {
	opts := monomind.ExecOptions{Runtime: "codex", Effort: "high", BudgetUSD: 2}
	if got := dropKiloOptions(&opts); got != "" || opts.Effort != "high" || opts.BudgetUSD != 2 {
		t.Errorf("codex: dropped %q, opts %+v", got, opts)
	}
}
