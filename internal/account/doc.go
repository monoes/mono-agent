// Package account is the machine-wide monoes.me session. One access token,
// an EdDSA-signed JWT that monoes.me issues and this binary verifies offline
// against keys pinned in it, plus a refresh token sealed under the OS keyring
// key, prove that a signed-in account exists on this machine. A Guard turns
// that session into a cached verdict (ok, grace or locked) that every gate of
// the program asks for: the CLI gate, the engine and runner checks, and the
// doors (HTTP API, webhook server, extension bridge, MCP).
//
// Until the enforcement date in rollout.go is set the package is dormant:
// nothing locks, nothing warns and nothing contacts monoes.me implicitly.
//
// Import rule: this package imports the standard library, golang.org/x/sys
// (the Windows lock) and internal/secrets (the keyring key), and nothing else
// of this repository, so every other package may import it.
//
// Files: claims.go (constants), state.go and session.go (the verdict),
// verify.go and keys.go (the proof), sealer.go and store.go (the session on
// disk), guard*.go and process.go (the cached verdict and the process-wide
// guard), testhooks.go (test seams that panic outside a test binary).
package account
