//go:build windows

package tracesig

// checkPrivate is a no-op on Windows, where the key lives in the user's
// profile folder and file modes do not describe its ACL.
func checkPrivate(string) error { return nil }
