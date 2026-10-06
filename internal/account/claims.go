package account

import "time"

// The constants below are proposals until spike S6 has read them from a real
// token (spec §4.1). If S6 corrects one, correct it here and then run the tests:
// wire_test.go spells GraceWindow and ClockSkew out in the refusal text ("24
// hours", "5 minutes"), and tests that step a clock across a window use literal
// durations.
const (
	HostURL        = "https://monoes.me"
	Issuer         = "https://monoes.me/api/auth"
	Audience       = "https://monoes.me/api/monoagent"
	ClientID       = "monoagent"
	GraceWindow    = 24 * time.Hour
	MaxTokenLife   = 24 * time.Hour
	ClockSkew      = 5 * time.Minute
	RefreshMargin  = 5 * time.Minute
	NegativeCache  = time.Minute
	ConnectTimeout = 2 * time.Second
	PollInterval   = 5 * time.Second
	LateRefresher  = 5 * time.Minute // a non-serving process starts its refresher after this
)
