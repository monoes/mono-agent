package nodes

import (
	"context"
	"database/sql"
	"strings"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/secrets"
)

// resolveSecret reads one vault secret; a var so tests can use a fake vault.
var resolveSecret = func(ctx context.Context, db *sql.DB, profileID, name string) (string, error) {
	return secrets.Resolve(ctx, db, profileID, "@secret:"+name)
}

// secretLookup backs {{secret:<name>}} with the profile vault. The name is
// tried as "<automation>/<name>" first. Only built-in and local automations
// (and legacy actions without a package) fall back to the bare "<name>":
// an imported package may read nothing but the secrets filed under its own
// id, so it can't pull, say, a GitHub token into a page it controls.
// Values are never logged.
func secretLookup(ctx context.Context, db *sql.DB, profileID, automationID string) func(string) (string, bool) {
	bare := true
	if info, ok := bootedInfo(automationID); ok && info.Trust == automation.SourceImported {
		bare = false
	}
	return func(name string) (string, bool) {
		name = strings.TrimSpace(name)
		if name == "" {
			return "", false
		}
		candidates := []string{automationID + "/" + name}
		if bare {
			candidates = append(candidates, name)
		}
		for _, c := range candidates {
			if v, err := resolveSecret(ctx, db, profileID, c); err == nil {
				return v, true
			}
		}
		return "", false
	}
}

// SecretLookup is the vault lookup for {{secret:<name>}} used outside
// browser nodes (e.g. `record verify`), with the same rules as a node run:
// "<automation>/<name>" first, and the bare "<name>" only for automations
// that aren't imported. It needs the registry booted (BootAutomations; the
// CLI root does it) to know an automation's trust; an id the registry doesn't
// know, such as a new draft, counts as the user's own.
func SecretLookup(ctx context.Context, db *sql.DB, profileID, automationID string) func(string) (string, bool) {
	return secretLookup(ctx, db, profileID, strings.ToLower(strings.TrimSpace(automationID)))
}

// bootedInfo returns the registry's installed info for an automation.
func bootedInfo(id string) (*automation.InstalledInfo, bool) {
	bootMu.Lock()
	reg := bootedReg
	bootMu.Unlock()
	if reg == nil {
		return nil, false
	}
	info, err := reg.Info(id)
	if err != nil || info == nil {
		return nil, false
	}
	return info, true
}
