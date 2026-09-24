//go:build linux

package nodemgr

import (
	"os"
	"strconv"
	"strings"
)

// processUsing finds a running process whose executable, or one of whose
// arguments (a script run by another node), lies in one of dirs, by reading
// /proc/<pid>/exe and cmdline: a few hundred small reads. Other users'
// processes are unreadable and skipped.
func processUsing(dirs ...string) (int, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, false
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		var paths []string
		if exe, err := os.Readlink("/proc/" + e.Name() + "/exe"); err == nil {
			paths = append(paths, strings.TrimSuffix(exe, " (deleted)"))
		}
		if cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil {
			paths = append(paths, strings.Split(string(cmdline), "\x00")...)
		}
		for _, p := range paths {
			if !strings.HasPrefix(p, "/") {
				continue
			}
			for _, dir := range dirs {
				if within(p, dir) {
					return pid, true
				}
			}
		}
	}
	return 0, false
}
