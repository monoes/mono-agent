//go:build windows

package account

// syncDir does nothing on Windows, which has no directory flush. A write closes
// the power-cut window another way there: its rename is written through
// (replaceFile, MOVEFILE_WRITE_THROUGH). A remove (DeleteRefresh) is not: a power
// cut just after one can bring the file back. No rule depends on a remove being
// durable: whatever the session file says when the token comes back (a refusal, a
// marker, a drop) still forbids presenting it, and that write is written through.
func syncDir(string) {}
