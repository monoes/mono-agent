package monomind

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// privateTempName is the folder of the monoagent home (~/.monoagent) where Exec
// writes the files it hands monomind when its caller gives it no TempDir.
const privateTempName = "tmp"

// staleTempAge is how old a file Exec left in that folder must be to be swept:
// a crash or a kill leaves one behind, and unlike the system temp directory
// nothing else cleans this folder. A turn reads its files at the start, so a
// file this old belongs to nothing that is still running.
const staleTempAge = 24 * time.Hour

// execTempDir is where Exec creates the prompt, system-prompt and tools files it
// hands monomind: opts.TempDir, else a private folder of the monoagent home,
// else (when the home cannot hold one) the system temp directory, which is "".
//
// The system temp directory is the wrong default. A turn that runs in a
// workspace-write sandbox may write it, so a sandboxed turn (an OpenAI-compatible
// API key holder's, say) could rewrite another turn's prompt file between Exec
// creating it and monomind reading it, and steer a turn with more access than its
// own. The folder under the monoagent home is outside every sandboxed turn's
// writable area.
func execTempDir(opts ExecOptions) string {
	if opts.TempDir != "" {
		return opts.TempDir
	}
	if dir, err := privateTempDir(); err == nil {
		return dir
	}
	return ""
}

// privateTempDir returns ~/.monoagent/tmp, created 0700. MkdirAll keeps the mode
// of a folder that was already there, so a looser one is tightened (a folder that
// cannot be is not ours to put prompts in). Stale files are swept the first time
// a process uses the folder.
func privateTempDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".monoagent", privateTempName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	sweepStaleTemp(dir)
	return dir, nil
}

// sweptTempDirs are the folders this process has swept.
var sweptTempDirs sync.Map

// sweepStaleTemp removes the files Exec itself created in dir (monoagent-*) that
// are older than staleTempAge, once per folder per process. Anything else in the
// folder is left alone.
func sweepStaleTemp(dir string) {
	if _, done := sweptTempDirs.LoadOrStore(dir, true); done {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "monoagent-") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) < staleTempAge {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
