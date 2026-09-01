package extension

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// tokenHeader carries the shared-secret token that authenticates relay
// requests (see handleRelay). Only local monoagentcli processes that can
// read ~/.monoagent/extension.token can drive the extension through it —
// an arbitrary local process or web page cannot.
const tokenHeader = "X-Monoagent-Extension-Token"

// tokenPath returns the path to the relay's shared-secret token file.
func tokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".monoagent", "extension.token"), nil
}

// generateToken creates a new random token and writes it to tokenPath,
// readable only by the current user. Call this only after winning the
// extension server's port bind: since exactly one process can ever hold
// that port, there is no write race with other processes that lose the
// bind and relay through the winner instead.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(buf)

	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(token), 0600); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	return token, nil
}

// loadToken reads the token written by generateToken. Callers that relay
// through an already-running server (rather than owning it) use this, since
// the server holding the port bind is the source of truth for the token.
func loadToken() (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token: %w", err)
	}
	return string(data), nil
}

// loadOrCreateToken returns the existing extension token if one is already
// on disk, generating a fresh one only when none exists. The extension
// WebSocket handshake (see handleWS) and the relay both authenticate against
// this same token, and the user pairs the extension with it via
// `monoagentcli extension pair` — reusing an existing token across restarts
// means that pairing survives process restarts instead of forcing the user
// to re-pair the extension every time monoagentcli starts.
func loadOrCreateToken() (string, error) {
	if tok, err := loadToken(); err == nil && tok != "" {
		return tok, nil
	}
	return generateToken()
}

// ResetToken deletes the on-disk extension token, invalidating both the
// relay and the paired extension's WebSocket handshake. The next server
// start generates a fresh token and the user must re-pair the extension
// with `monoagentcli extension pair`. This is the "security reset" escape
// hatch: it revokes a compromised or unknown token immediately.
func ResetToken() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token: %w", err)
	}
	return nil
}

// CurrentToken returns the token the running (or next-started) server will
// authenticate against, generating one if none exists yet. Used by
// `monoagentcli extension pair` to print the value the user pastes into the
// extension popup.
func CurrentToken() (string, error) {
	return loadOrCreateToken()
}
