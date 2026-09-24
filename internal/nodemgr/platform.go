package nodemgr

import (
	"fmt"
	"os"
	"path/filepath"
)

// nodeArch is nodejs.org's name for this platform's architecture, "" when
// it publishes no build for it. Node >= 22 has no 32-bit x86 build for
// Linux (Windows' win-x86 exists for 22.x only) and no armv6 one: 32-bit
// arm means armv7l, which is Linux only.
func (m *Manager) nodeArch() string {
	if m.GOOS != "linux" && m.GOOS != "darwin" && m.GOOS != "windows" {
		return ""
	}
	switch {
	case m.GOARCH == "amd64":
		return "x64"
	case m.GOARCH == "arm64":
		return "arm64"
	case m.GOOS == "linux" && (m.GOARCH == "ppc64le" || m.GOARCH == "s390x"):
		return m.GOARCH
	case m.GOOS == "linux" && m.GOARCH == "arm":
		return "armv7l"
	case m.GOOS == "windows" && m.GOARCH == "386":
		return "x86"
	}
	return ""
}

// checkPlatform refuses, with a reason a person can act on, a platform
// nodejs.org has no build for.
func (m *Manager) checkPlatform() error {
	if m.nodeArch() == "" {
		return fmt.Errorf("nodejs.org publishes no Node.js build for %s/%s — install Node >= %s another way", m.GOOS, m.GOARCH, MinVersion)
	}
	if m.GOOS == "linux" && m.musl() {
		return fmt.Errorf("this system uses musl libc (e.g. Alpine Linux) and nodejs.org's Linux builds need glibc — " +
			"install Node >= " + MinVersion + " with the system package manager (apk add nodejs npm)")
	}
	return nil
}

// musl reports a musl-only Linux: a musl loader and no glibc one (Alpine
// with gcompat has both, and runs the glibc build).
func (m *Manager) musl() bool {
	root := m.sysRoot
	if root == "" {
		root = "/"
	}
	has := func(pattern string) bool {
		found, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, f := range found {
			if _, err := os.Stat(f); err == nil {
				return true
			}
		}
		return false
	}
	if !has("lib/ld-musl-*.so.1") {
		return false
	}
	for _, glibc := range []string{"lib/ld-linux*.so.*", "lib64/ld-linux*.so.*", "lib/*-linux-gnu*/ld-linux*.so.*"} {
		if has(glibc) {
			return false
		}
	}
	return true
}
