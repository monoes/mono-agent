package main

import (
	"errors"
	"time"

	"github.com/rs/zerolog"

	browserpkg "github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/extension"
)

// Per-profile browsers: each monoagent profile's browser actions run in the
// browser profile bound to it (see internal/extension/conns.go for the
// routing rule). Everything here is the CLI's half: choosing the profile a
// command runs as and narrowing the bridge to its browser.

// bridgeForProfile narrows bridge to the browser bound to profileID. A
// bridge that cannot route (a test double), or no profile at all, is
// returned unchanged, which reaches the default browser.
func bridgeForProfile(bridge browserpkg.ExtensionBridge, profileID string) browserpkg.ExtensionBridge {
	if r, ok := bridge.(browserpkg.ProfileRouter); ok && profileID != "" {
		return r.ForProfile(profileID)
	}
	return bridge
}

// setupProfileBridge is setupExtensionBridge narrowed to profileID's browser.
func setupProfileBridge(profileID string, logger zerolog.Logger, wait time.Duration) browserpkg.ExtensionBridge {
	return bridgeForProfile(setupExtensionBridge(logger, wait), profileID)
}

// browserProfile resolves the profile this invocation runs as, i.e.
// --profile (an id or a name) or the active one, to its id. initDB does the
// resolving; a command that never opened the database would otherwise route
// by a raw name, or by nothing.
func browserProfile(cfg *globalConfig) string {
	db, err := initDB(cfg)
	if err != nil {
		return cfg.ProfileID
	}
	db.Close()
	return cfg.ProfileID
}

// routeHint explains, for a bridge narrowed to a profile, why no browser
// may run it. It is appended to ensureExtensionConnected's timeout errors,
// which otherwise only say "did not connect".
func routeHint(bridge connChecker) string {
	r, ok := bridge.(interface {
		Route() (extension.ConnInfo, error)
	})
	if !ok {
		return ""
	}
	_, err := r.Route()
	var nb *extension.NoBrowserError
	if errors.As(err, &nb) {
		return "\n" + nb.Error()
	}
	return ""
}
