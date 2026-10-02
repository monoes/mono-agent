package openaiapi

import (
	"fmt"
	"slices"
	"strings"
)

// defaultToolRuntimes serve tool calling when MONOAGENT_API_TOOL_RUNTIMES is not
// set. Which runtimes call a declared function reliably is measured, not known:
// in the spike claude and codex did (9 of 10 each), antigravity went for its own
// tools instead (8 of 10, and its file edits bypass the declared ones), so the
// list is the operator's to change and not knowledge of any CLI. Read only.
var defaultToolRuntimes = []string{"claude", "codex"}

// ToolRuntimeList is the runtimes that serve tool calling: the configured ones,
// or the defaults when none were configured. A list configured with nothing in it
// is empty, not the default. The result is read only.
func (c Config) ToolRuntimeList() []string {
	if c.ToolRuntimes == nil {
		return defaultToolRuntimes
	}
	return c.ToolRuntimes
}

// ToolsOff reports whether tool calling is switched off: the list is empty, which
// MONOAGENT_API_TOOL_RUNTIMES=none makes it.
func (c Config) ToolsOff() bool { return len(c.ToolRuntimeList()) == 0 }

// toolsOffBy says that tool calling was switched off, and by what.
const toolsOffBy = "switched off on this server (the operator set MONOAGENT_API_TOOL_RUNTIMES to none)"

// ParseToolRuntimes reads MONOAGENT_API_TOOL_RUNTIMES: runtime ids separated by
// commas. Case and spaces do not matter, "agy" means antigravity and a repeat
// counts once. An empty value is the default list, and "none" alone is the off
// switch: a list with nothing in it, so that no model serves tool calling.
func ParseToolRuntimes(v string) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return slices.Clone(defaultToolRuntimes), nil
	}
	if strings.EqualFold(strings.TrimSpace(v), "none") {
		return []string{}, nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		rt := strings.ToLower(strings.TrimSpace(part))
		if rt == "none" {
			return nil, fmt.Errorf("MONOAGENT_API_TOOL_RUNTIMES: none switches tool calling off and is not a runtime to list with others, got %q", v)
		}
		if alias, ok := aliases[rt]; ok {
			rt = alias
		}
		if !runtimeRE.MatchString(rt) {
			return nil, fmt.Errorf("MONOAGENT_API_TOOL_RUNTIMES must be a comma-separated list of runtime ids, such as claude,codex, got %q", v)
		}
		if !slices.Contains(out, rt) {
			out = append(out, rt)
		}
	}
	return out, nil
}

// ServesTools reports whether a request that declares tools may run on m. It is
// the one question behind the refusal of such a request and behind the tools
// capability of a model in GET /v1/models, so the two cannot disagree.
func (c Config) ServesTools(m ModelInfo) bool { return c.toolsRefusal(m) == nil }

// toolsRefusal is the 400 for a request that declares tools for a model that
// cannot serve them, nil when it can: its runtime is in the list, and either its
// own tools are gated by monomind (chat-only) or monomind can run it read-only,
// which keeps its native tools from being used instead of the declared ones.
func (c Config) toolsRefusal(m ModelInfo) *apiError {
	list := c.ToolRuntimeList()
	switch {
	case len(list) == 0:
		return errUnsupported("tools", "tool calling is "+toolsOffBy)
	case !slices.Contains(list, m.Runtime):
		return errUnsupported("tools", fmt.Sprintf("tool calling is not available on model %s: it is served on %s only (the operator sets that list with MONOAGENT_API_TOOL_RUNTIMES)", m.ID, strings.Join(list, ", ")))
	case m.Class != ChatOnly && !m.ReadAccess:
		return errUnsupported("tools", fmt.Sprintf("tool calling is not available on model %s here: it needs the runtime to run read-only, which this machine's monomind cannot do for it (monomind's agent-exec-access-read)", m.ID))
	}
	return nil
}
