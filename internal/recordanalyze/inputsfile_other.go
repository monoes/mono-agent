//go:build !unix

package recordanalyze

import "os"

// ownedByCurrentUser cannot be checked portably here; the permission check
// in ReadInputsFile still applies.
func ownedByCurrentUser(os.FileInfo) bool { return true }
