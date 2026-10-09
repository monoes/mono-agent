package monomind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// MCPPinMinVersion is the first monomind whose `init` writes the running
// version into .mcp.json (`npx -y --package=@monoes/monomindcli@<version>
// monomind mcp start`, monomind#419) instead of `monomind@latest`.
const MCPPinMinVersion = "2.19.0"

const mcpPinPrefix = "--package=@monoes/monomindcli@"

// MCPPin returns the monomind version root's .mcp.json pins its monomind
// server to; "" when there is no .mcp.json, no monomind server, or it is
// not pinned.
func MCPPin(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	for _, a := range cfg.MCPServers["monomind"].Args {
		if v, ok := strings.CutPrefix(a, mcpPinPrefix); ok {
			return v
		}
	}
	return ""
}

// StaleMCPPin reports whether root's .mcp.json pins a monomind version other
// than the installed one, and which. A monomind older than MCPPinMinVersion
// does not write a pin, so re-running its init could not change one.
func StaleMCPPin(root, installed string) (pinned string, stale bool) {
	installed = strings.TrimPrefix(strings.TrimSpace(installed), "v")
	if installed == "" || !versionAtLeast(installed, MCPPinMinVersion) {
		return "", false
	}
	pinned = MCPPin(root)
	return pinned, pinned != "" && pinned != installed
}
