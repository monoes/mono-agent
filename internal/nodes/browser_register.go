package nodes

import (
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/workflow"
)

// RegisterBrowserNodes registers every browser automation action as a
// workflow node "<automation>.<action>", e.g. "linkedin.find_by_keyword".
//
// Actions come from the automation registry (enabled, available packages)
// once it is booted, else from the legacy loader (embedded seed plus
// ~/.monoagent/actions).
func RegisterBrowserNodes(r *workflow.NodeTypeRegistry) {
	ensureAutomationsBooted()

	available, err := listActions()
	if err != nil {
		return
	}

	registered := make(map[string]bool, len(available))
	var locals [][2]string
	for _, entry := range available {
		// entry is "automation/action_type"
		parts := strings.SplitN(entry, "/", 2)
		if len(parts) != 2 {
			continue
		}
		automationID, actionType := parts[0], parts[1]
		nodeType := automationID + "." + actionType
		if registered[nodeType] || !compiledIn(automationID) {
			continue
		}
		p, a := automationID, actionType
		r.Register(nodeType, func() workflow.NodeExecutor {
			return NewBrowserNode(p, a)
		})
		registered[nodeType] = true
		if strings.HasPrefix(automationID, "local-") {
			locals = append(locals, [2]string{automationID, actionType})
		}
	}

	// Legacy ~/.monoagent/actions/<p>/ files are wrapped into a
	// "local-<p>" package; workflows saved before that still say
	// "<p>.<action>", so resolve that name wherever no package claims it.
	// The package id is a sanitised slug (google_maps → local-google-maps),
	// so the alias uses the original directory name when the registry
	// records it.
	for _, la := range locals {
		legacy := legacyPlatform(la[0]) + "." + la[1]
		if !registered[legacy] {
			r.Alias(legacy, la[0]+"."+la[1])
		}
	}
}

// legacyPlatform is the ~/.monoagent/actions/<p> name a local-* package
// was made from: the registry's record of it, else the id without
// "local-".
func legacyPlatform(id string) string {
	if src := action.CurrentDefSource(); src != nil {
		if lp, ok := src.Package(id).(interface{ LegacyPlatform() string }); ok {
			if name := lp.LegacyPlatform(); name != "" {
				return name
			}
		}
	}
	return strings.TrimPrefix(id, "local-")
}

// listActions returns "<automation>/<action>" entries from the installed
// definition source, or the legacy loader when none is installed.
func listActions() ([]string, error) {
	if src := action.CurrentDefSource(); src != nil {
		return src.List()
	}
	return action.GetLoader().ListAvailable()
}

// compiledIn applies the social build gate. With the registry booted it
// applies only to packages that need a compiled Go bot (requires.native);
// the registry already marks policy-blocked packages unavailable. Legacy
// platforms are their own native bot.
func compiledIn(automationID string) bool {
	if m, ok := bootedManifest(automationID); ok {
		return m.Requires.Native == "" || bot.PlatformCompiledIn(m.Requires.Native)
	}
	return bot.PlatformCompiledIn(automationID)
}
