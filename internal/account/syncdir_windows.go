//go:build windows

package account

// syncDir does nothing on Windows, so the power-cut window that it closes on Unix
// stays open there: os.Rename passes MOVEFILE_REPLACE_EXISTING only, and the
// rename is not written through (MOVEFILE_WRITE_THROUGH would do it).
func syncDir(string) {}
