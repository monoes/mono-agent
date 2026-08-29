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

// setChatProcessGroup isolates the chat subprocess (monoagentcli → monomind
// → agent CLI) in its own process group so a UI stop reaps the whole tree.
func setChatProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killChatProcessGroup SIGTERMs then SIGKILLs the whole chat process group.
func killChatProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

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

// signalWorkflowPID SIGTERMs the external CLI process recorded in the DB for
// an execution — but only after verifying the pid still belongs to a
// monoagent binary. A stale pid can have been reused by the OS for an
// unrelated process; signaling that must never happen (RA1-8). Dead pids are
// a no-op: there is nothing left to signal.
func signalWorkflowPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd, alive, err := readProcessCommandLine(pid)
	if err != nil {
		return fmt.Errorf("refusing to signal non-monoagent process (pid %d could not be verified: %v)", pid, err)
	}
	if !alive {
		return nil
	}
	if !strings.Contains(strings.ToLower(cmd), "monoagent") {
		return fmt.Errorf("refusing to signal non-monoagent process (pid %d: %s)", pid, cmd)
	}
	proc, perr := os.FindProcess(pid)
	if perr != nil {
		return nil
	}
	_ = proc.Signal(syscall.SIGTERM)
	return nil
}
