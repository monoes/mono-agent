package nodemgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InUseError says a managed Node version was kept because a running
// process uses it.
type InUseError struct {
	Version string // "" for the managed Node as a whole (Remove(""))
	PID     int    // 0 when the OS refused the removal without naming a process
	Err     error
}

func (e *InUseError) Error() string {
	what := "the managed Node.js"
	if e.Version != "" {
		what = "node " + e.Version
	}
	if e.PID > 0 {
		return fmt.Sprintf("%s is in use by process %d; stop it and try again", what, e.PID)
	}
	return fmt.Sprintf("%s is in use (%v); stop what runs it and try again", what, e.Err)
}

func (e *InUseError) Unwrap() error { return e.Err }

// removeVersion deletes Root/<version> unless a running process uses it.
// Linux finds such a process through /proc; on Windows the rename below
// fails while node.exe runs, so the tree is never left half-deleted.
// macOS has no cheap check: a Node running from the version keeps running
// (its files stay open) but can fail loading more of them.
func (m *Manager) removeVersion(version string) error {
	dir := filepath.Join(m.Root, version)
	if pid, ok := processUsing(dir); ok {
		return &InUseError{Version: version, PID: pid}
	}
	trash, err := os.MkdirTemp(m.Root, ".trash-*")
	if err != nil {
		return err
	}
	if err := os.Rename(dir, filepath.Join(trash, version)); err != nil {
		os.Remove(trash)
		if m.GOOS == "windows" {
			return &InUseError{Version: version, Err: err}
		}
		return err
	}
	return os.RemoveAll(trash)
}

// within reports whether path is dir or inside it. Unlike a plain prefix
// test, ~/.monoagent/node-foo is not inside ~/.monoagent/node.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
