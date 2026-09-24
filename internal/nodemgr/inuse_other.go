//go:build !linux

package nodemgr

// processUsing has no cheap implementation outside Linux; see removeVersion.
func processUsing(...string) (int, bool) { return 0, false }
