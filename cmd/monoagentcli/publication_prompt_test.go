package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationCLIInvocationPinsTurnScope(t *testing.T) {
	db := filepath.Join(t.TempDir(), "turn.db")
	cfg := &globalConfig{ProfileID: "writer profile", DBPath: db}
	got := publicationCLIInvocation("/my cli/monoagentcli", cfg)
	for _, want := range []string{shellQuote("/my cli/monoagentcli"), "--profile " + shellQuote(cfg.ProfileID), "--db-path " + shellQuote(db)} {
		if !strings.Contains(got, want) {
			t.Errorf("invocation %q missing %q", got, want)
		}
	}
	prompt := publicationAgentPrompt(cfg)
	for _, want := range []string{"--profile " + shellQuote(cfg.ProfileID), "publication register --stdin-json", "publication.register", "after", "returned URL/ID"} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
