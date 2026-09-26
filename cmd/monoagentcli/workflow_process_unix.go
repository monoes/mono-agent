//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// readProcessCommandLine returns the command line of the process with the
// given pid. alive=false with a nil error means the process is no longer
// running (nothing to signal); a non-nil error means it could not be
// determined.
func readProcessCommandLine(pid int) (cmdline string, alive bool, err error) {
	if runtime.GOOS == "linux" {
		data, rerr := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return "", false, nil
			}
			return "", false, rerr
		}
		// /proc/<pid>/cmdline is NUL-separated; zombies and kernel threads
		// read back empty, which also means "not signalable".
		cmd := strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
		if cmd == "" {
			return "", false, nil
		}
		return cmd, true, nil
	}
	// macOS and the BSDs: `ps` exits nonzero when the pid doesn't exist.
	out, perr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if perr != nil {
		var xerr *exec.Error
		if errors.As(perr, &xerr) {
			return "", false, fmt.Errorf("cannot inspect pid %d: %w", pid, perr)
		}
		return "", false, nil // nonzero exit: no such process
	}
	cmd := strings.TrimSpace(string(out))
	if cmd == "" {
		return "", false, nil
	}
	return cmd, true, nil
}

// signalWorkflowPID SIGTERMs the process recorded for an execution, but
// only after verifying the pid still belongs to a monoagent binary. A stale
// pid can have been reused by the OS for an unrelated process; signalling
// that must never happen (RA1-8). A dead pid is a no-op (false, nil).
func signalWorkflowPID(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	cmd, alive, err := readProcessCommandLine(pid)
	if err != nil {
		return false, fmt.Errorf("refusing to signal non-monoagent process (pid %d could not be verified: %v)", pid, err)
	}
	if !alive {
		return false, nil
	}
	if !strings.Contains(strings.ToLower(cmd), "monoagent") {
		return false, fmt.Errorf("refusing to signal non-monoagent process (pid %d: %s)", pid, cmd)
	}
	proc, perr := os.FindProcess(pid)
	if perr != nil {
		return false, nil
	}
	return proc.Signal(syscall.SIGTERM) == nil, nil
}

// isMonoagentDaemonProcess reports whether pid is a running `monoagentcli
// daemon` process. `ps -o command=` gives the full argv on every unix,
// where readProcessCommandLine's macOS path only has the executable name.
func isMonoagentDaemonProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	cmdline := string(out)
	return strings.Contains(cmdline, "monoagentcli") && strings.Contains(cmdline, "daemon")
}
