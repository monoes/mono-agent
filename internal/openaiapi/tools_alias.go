package openaiapi

import (
	"crypto/sha256"
	"encoding/hex"
)

// monomind puts "mcp__org__" in front of the name of a function, and the runtimes take 64
// characters at most, so a name of 54 is the longest it can be given. Clients name functions
// after their MCP server and tool, which is often longer, up to the 64 of OpenAI's own limit:
// a name of 55 to 64 characters is known to monomind and to the model by an alias that is
// worked out from it, and what the client sees (the response, the record, its own history and
// tool_choice) is the name it declared.

// toolAlias is the name monomind and the model know a function of 55 to 64 characters by:
// its first 45 characters, an underscore and 8 hex digits of the SHA-256 of the whole name,
// which is 54 characters. A name of 54 or fewer is its own.
func toolAlias(name string) string {
	if len(name) <= maxToolName {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return name[:maxToolName-9] + "_" + hex.EncodeToString(sum[:])[:8]
}

// wireName is the name the runtime and the model know a declared function by.
func (r *ChatRequest) wireName(declared string) string {
	if wire, ok := r.toolWire[declared]; ok {
		return wire
	}
	return declared
}

// declaredName is the name the client gave a function the runtime calls by its alias; any
// other name, one the model made up included, is its own.
func (r *ChatRequest) declaredName(wire string) string {
	if declared, ok := r.toolDeclared[wire]; ok {
		return declared
	}
	return wire
}
