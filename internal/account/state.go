package account

import "time"

// State is the verdict of a session (spec §4.3).
type State string

const (
	StateOK     State = "ok"
	StateGrace  State = "grace"
	StateLocked State = "locked"
)

// Reason says why a session is locked, or, in grace, why it was not refreshed.
type Reason string

const (
	ReasonNone               Reason = ""
	ReasonNotLoggedIn        Reason = "not_logged_in"
	ReasonExpired            Reason = "expired"
	ReasonRefused            Reason = "refused"
	ReasonClockRollback      Reason = "clock_rollback"
	ReasonClockSkew          Reason = "clock_skew"
	ReasonKeyUnknown         Reason = "key_unknown"
	ReasonInvalid            Reason = "invalid"
	ReasonUnreachable        Reason = "unreachable"         // grace: why it was not refreshed
	ReasonServerError        Reason = "server_error"        // grace
	ReasonKeyringUnavailable Reason = "keyring_unavailable" // grace
	ReasonUnconfirmed        Reason = "unconfirmed"         // grace, then locked: a refresh whose answer never arrived (A24)
)

// User is the account the session belongs to.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

// Status is the verdict and the JSON of `account status --json` (spec §7).
type Status struct {
	V           int       `json:"v"` // always 1
	State       State     `json:"state"`
	Reason      Reason    `json:"reason"`
	User        *User     `json:"user,omitempty"` // display data from the stored session, not verified (a verified token with no stored user shows its sub); the verified identity is the token's sub
	Plan        string    `json:"plan"`
	IssuedAt    time.Time `json:"issued_at,omitzero"`
	ValidUntil  time.Time `json:"valid_until,omitzero"` // the access token's exp
	GraceUntil  time.Time `json:"grace_until,omitzero"` // iat + GraceWindow
	EnforceFrom time.Time `json:"enforce_from,omitzero"`
	Enforced    bool      `json:"enforced"`
}

// Allowed reports whether work may run: true when the gate is not enforced
// yet, or the state is ok or grace.
func (s Status) Allowed() bool {
	return !s.Enforced || s.State == StateOK || s.State == StateGrace
}
