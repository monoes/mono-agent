package account

// init lets a development build move the enforcement date (spec A12, index §3.6). devEnforceFrom
// reads the environment only in a build with the devaccount tag; a default build's version never
// looks, so nothing in a user's environment can move the date of a release.
func init() {
	if at, ok := devEnforceFrom(); ok {
		enforceFrom = at // init runs before any goroutine, so the accessors' lock is not needed
	}
}
