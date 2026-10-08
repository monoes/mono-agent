//go:build devaccount

package account

import (
	"fmt"
	"os"
	"time"
)

// devEnforceFromEnv moves the enforcement date of a development build: a past date forces
// enforcement and a future one a warn period, which is what the real-binary smoke needs. It only sets
// a date. Anything that is not an RFC 3339 time, and the zero time, which would switch the gate off,
// stop the process at start instead of being read as one.
const devEnforceFromEnv = "MONOAGENT_DEV_ENFORCE_FROM"

func devEnforceFrom() (time.Time, bool) {
	v := os.Getenv(devEnforceFromEnv)
	if v == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(fmt.Sprintf("%s must be an RFC 3339 time: %v", devEnforceFromEnv, err))
	}
	if at.IsZero() {
		panic(devEnforceFromEnv + " must be a date: the zero time would switch the gate off")
	}
	return at, true
}
