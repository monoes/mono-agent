package extension

import (
	"crypto/subtle"
	"fmt"
	"net/http"
)

// Checking this client's pairing token against a running bridge.
//
// /monoagent/health is unauthenticated on purpose (it must answer "is
// anything there?" for anyone), so `extension status` could report a
// connected bridge that every relayed command from this client would then
// be refused by (401: another HOME, or the token was reset). The
// /monoagent/auth endpoint answers only that one question: 204 when the
// X-Monoagent-Extension-Token header matches the bridge's token, 401
// otherwise. It does nothing else.

// PairingState is what CheckPairing found.
type PairingState string

const (
	// PairingOK: this client's token is the bridge's.
	PairingOK PairingState = "ok"
	// PairingMismatch: the bridge refused this client's token.
	PairingMismatch PairingState = "mismatch"
	// PairingUnknown: the bridge could not be asked (an older bridge
	// without /monoagent/auth, or no answer).
	PairingUnknown PairingState = "unknown"
)

// PairingMismatchHint is the sentence `extension status` prints for
// PairingMismatch.
const PairingMismatchHint = "bridge is running, but this client's pairing token doesn't match it — re-pair or use the same HOME"

// handleAuthProbe answers /monoagent/auth.
func (s *Server) handleAuthProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	got := r.Header.Get(tokenHeader)
	if got == "" || s.token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CheckPairing asks the bridge at baseURL whether this client's token (the
// one relayed commands will carry) is the bridge's.
func CheckPairing(baseURL string) (PairingState, error) {
	token, _ := loadToken()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/monoagent/auth", nil)
	if err != nil {
		return PairingUnknown, err
	}
	req.Header.Set(tokenHeader, token)
	resp, err := (&http.Client{Timeout: statusProbeTimeout}).Do(req)
	if err != nil {
		return PairingUnknown, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return PairingOK, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return PairingMismatch, nil
	default:
		return PairingUnknown, fmt.Errorf("pairing check: HTTP %d", resp.StatusCode)
	}
}
