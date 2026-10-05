package account

import "time"

// The constants below are proposals until spike S6 has read them from a real
// token (spec §4.1). If S6 corrects one, this is the only file that changes.
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
