//go:build releasee2e

package release

import "os"

// Test-only (built with -tags releasee2e, never in a release): pin the key
// in MONOAGENT_E2E_PINNED_KEY so scripts/release-e2e-test.sh can exercise the
// pinned-key paths of a built binary without editing keys.go.
func init() {
	if line := os.Getenv("MONOAGENT_E2E_PINNED_KEY"); line != "" {
		pinnedReleaseKeys = append(pinnedReleaseKeys, line)
	}
}
