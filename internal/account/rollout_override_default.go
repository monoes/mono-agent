//go:build !devaccount

package account

import "time"

// devEnforceFrom is the default build's side of the override: there is none, and nothing here
// looks at the environment.
func devEnforceFrom() (time.Time, bool) { return time.Time{}, false }
