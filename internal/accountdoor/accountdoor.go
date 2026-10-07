// Package accountdoor is what every network door of the daemon says to a
// caller it refuses while the monoes.me account is locked: the HTTP API, the
// org receiver, the /v1 gateway, the webhook server, the extension bridge and
// the MCP server each answer in their own wire shape, but with one sentence,
// one code and one account summary. It holds no policy: whether to refuse is
// account.Require's verdict.
package accountdoor

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

const (
	// Message is the sentence a refused caller reads: the first line of every
	// refusal of the account package, said alone.
	Message = account.LoginRequiredMessage
	// Code is the machine-readable code of a refusal.
	Code = "login_required"
)

// Summary is the part of the account state a door may show to a caller that
// holds no monoes.me login: the verdict and why, never who. Enforced says
// whether anything is refused: while dormant, and until the enforcement date,
// State can be locked while every door still answers.
type Summary struct {
	State      account.State  `json:"state"`
	Reason     account.Reason `json:"reason"`
	ValidUntil time.Time      `json:"valid_until,omitzero"`
	Enforced   bool           `json:"enforced"`
}

// SummaryOf reduces a verdict to what a door may show.
func SummaryOf(st account.Status) Summary {
	return Summary{State: st.State, Reason: st.Reason, ValidUntil: st.ValidUntil, Enforced: st.Enforced}
}

type lockedAccount struct {
	State  account.State  `json:"state"`
	Reason account.Reason `json:"reason"`
}

type unauthorizedBody struct {
	Error         string        `json:"error"`
	LoginRequired bool          `json:"login_required"`
	Account       lockedAccount `json:"account"`
}

// WriteUnauthorized answers a refused HTTP request as the HTTP API and the org
// receiver do: 401 and {"error":"login_required","login_required":true,
// "account":{"state","reason"}}. It sets no WWW-Authenticate header: the
// caller's bearer is not what was refused. The account is the verdict err
// carries (account.Require refuses with a *account.LoginRequiredError); any
// other error is described by the current verdict.
func WriteUnauthorized(w http.ResponseWriter, err error) {
	var refusal *account.LoginRequiredError
	if !errors.As(err, &refusal) {
		refusal = &account.LoginRequiredError{Status: account.CurrentStatus()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(unauthorizedBody{
		Error:         Code,
		LoginRequired: true,
		Account:       lockedAccount{State: refusal.Status.State, Reason: refusal.Status.Reason},
	})
}
