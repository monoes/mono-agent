//go:build windows

package proctree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// Kill ends a started child and its descendants: taskkill /T /F by the
// child's pid, then the child itself if taskkill missed it. A no-op before
// Start, and once the child has been waited for, since its pid may already
// name an unrelated process by then. A descendant whose own parent already
// exited cannot be linked back by taskkill; only a Job Object catches that.
func Kill(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
		return
	}
	TaskkillTree(cmd.Process.Pid)
	_ = cmd.Process.Kill()
}

// TaskkillTree runs taskkill /T /F against pid, from System32, never from
// PATH, without showing a console window.
func TaskkillTree(pid int) {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	tk := exec.Command(filepath.Join(root, "System32", "taskkill.exe"), "/T", "/F", "/PID", strconv.Itoa(pid))
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	_ = tk.Run()
}
