package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/accountdoor"
)

// The bridge's door for the monoes.me account. A locked account does not close
// the bridge: the extension still connects and pairs, so that ping can tell the
// side panel why nothing else works. What is refused is what does work for a
// caller: extension requests (serveRequest), commands relayed in from other
// processes (handleRelay) and the CDP socket (handleCdpSocket).
//
// Left open on purpose: the handshake and pairing, the probes (/monoagent/health
// also tells other processes where this bridge is and must keep answering 200)
// and the listing and resolving of browsers. Binding pushes, activity-recording
// frames and pages the extension flushes unasked are data, not work: they stay
// accepted (record.analyze, the work on a recording, is a request).

// errAccountLocked is the error of every reply the bridge refuses.
var errAccountLocked = errors.New(accountdoor.Message)

// accountRefuses reports whether the bridge refuses work now. It reads the
// process guard's cached verdict (no I/O), so it is cheap on every frame.
func accountRefuses() bool { return account.Require(context.Background()) != nil }

// accountSummary is what ping reports of the account: the state, why and, for a
// session, until when, never who.
func accountSummary() accountdoor.Summary {
	return accountdoor.SummaryOf(account.CurrentStatus())
}

// writeRelayLocked refuses a relayed command with a 503: the relay client reads
// a 401 or 403 as a pairing-token mismatch (ErrRelayUnauthorized) and would tell
// the user to re-pair, while a non-2xx reply that carries a Response comes back
// as the extension's own error, with the sentence to act on.
func writeRelayLocked(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(&Response{Error: accountdoor.Message})
}
