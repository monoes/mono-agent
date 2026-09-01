//go:build windows

package workflow

import "golang.org/x/sys/windows"

// stillActive is the STILL_ACTIVE sentinel exit code (259) Windows reports
// via GetExitCodeProcess for a process that has not yet terminated. Not
// exported by golang.org/x/sys/windows, so it's defined here directly —
// this is a fixed Win32 API constant, not something that varies by OS
// version.
const stillActive = 259

// processAlive reports whether a process with the given pid is currently
// running. It opens the process with the minimal query rights and checks
// its exit code — STILL_ACTIVE means the process has not yet terminated.
// Access denial (e.g. a pid reused by a process owned by another user)
// still implies the pid is in use, so it counts as alive, mirroring the
// Unix EPERM-means-alive behavior in process_unix.go.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return true
		}
		return false
	}
	defer windows.CloseHandle(h)

	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}
