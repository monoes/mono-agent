package main

import (
	"os"
	"path/filepath"
	"strings"
)

// publicationCLIInvocation keeps registration in the turn's profile and database.
func publicationCLIInvocation(bin string, cfg *globalConfig) string {
	if bin == "" {
		bin = "monoagentcli"
	}
	parts := []string{shellQuote(bin)}
	if cfg.ProfileID != "" {
		parts = append(parts, "--profile", shellQuote(cfg.ProfileID))
	}
	if cfg.DBPath != "" && cfg.DBPath != defaultDBPath {
		if abs, err := filepath.Abs(expandPath(cfg.DBPath)); err == nil {
			parts = append(parts, "--db-path", shellQuote(abs))
		}
	}
	return strings.Join(parts, " ")
}

func publicationAgentPrompt(cfg *globalConfig) string {
	bin, _ := os.Executable()
	cli := publicationCLIInvocation(bin, cfg)
	return "After any authorized publication through an external tool, register the successful result with `" + cli +
		" publication register --stdin-json` using exact published content, destination, returned URL/ID and your agent identity. " +
		"See `" + cli + " ref publication`. Built-in publishing nodes record automatically. " +
		"For custom workflow publishers, add a downstream publication.register node. Registration is local tracking and does not publish."
}
