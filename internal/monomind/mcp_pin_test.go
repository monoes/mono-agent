package monomind

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMCP(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPPin(t *testing.T) {
	root := t.TempDir()
	if got := MCPPin(root); got != "" {
		t.Fatalf("no .mcp.json: pin %q", got)
	}
	writeMCP(t, root, `{"mcpServers":{"other":{"args":["--package=@monoes/monomindcli@9.9.9"]},"monomind":{"command":"npx","args":["-y","--package=@monoes/monomindcli@2.20.0","monomind","mcp","start"]}}}`)
	if got := MCPPin(root); got != "2.20.0" {
		t.Fatalf("pin = %q, want 2.20.0", got)
	}
	writeMCP(t, root, `{"mcpServers":{"monomind":{"command":"npx","args":["-y","monomind@latest","mcp","start"]}}}`)
	if got := MCPPin(root); got != "" {
		t.Fatalf("unpinned entry: pin %q", got)
	}
	writeMCP(t, root, `not json`)
	if got := MCPPin(root); got != "" {
		t.Fatalf("bad json: pin %q", got)
	}
}

func TestStaleMCPPin(t *testing.T) {
	root := t.TempDir()
	writeMCP(t, root, `{"mcpServers":{"monomind":{"args":["-y","--package=@monoes/monomindcli@2.20.0","monomind"]}}}`)
	if p, stale := StaleMCPPin(root, "2.24.4"); !stale || p != "2.20.0" {
		t.Errorf("older pin: %q %v", p, stale)
	}
	if p, stale := StaleMCPPin(root, "v2.20.0"); stale {
		t.Errorf("matching pin reported stale: %q", p)
	}
	// An installed monomind that writes no pin cannot fix one.
	if _, stale := StaleMCPPin(root, "2.18.0"); stale {
		t.Error("monomind < 2.19 reported a stale pin")
	}
	if _, stale := StaleMCPPin(root, ""); stale {
		t.Error("unknown version reported a stale pin")
	}
}
