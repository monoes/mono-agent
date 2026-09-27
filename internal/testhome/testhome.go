// Package testhome keeps a package's tests out of the real home directory.
//
// Many code paths default to ~/.monoagent (the database, profiles, AI tool
// backups), so a test that forgets t.Setenv("HOME", …) writes into the
// user's real state — and can migrate their real database. Call Main from a
// package's TestMain to give the whole test binary a throwaway HOME:
//
//	func TestMain(m *testing.M) { testhome.Main(m) }
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Main points HOME (and USERPROFILE) at a fresh temp dir, runs the tests,
// removes the dir and exits. Go's build and module caches, and the user cache
// dir (rod's browser download), keep their real locations so tests that
// build binaries or launch a browser don't start cold.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	keepCaches()
	dir, err := os.MkdirTemp("", "monoagent-testhome-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testhome:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	return m.Run()
}

// keepCaches pins cache locations derived from HOME before HOME changes.
func keepCaches() {
	if cache, err := os.UserCacheDir(); err == nil {
		setIfUnset("GOCACHE", filepath.Join(cache, "go-build"))
		if os.Getenv("XDG_CACHE_HOME") == "" && os.Getenv("HOME") != "" {
			// Only Linux and the BSDs read it; harmless elsewhere.
			os.Setenv("XDG_CACHE_HOME", cache)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
			os.Setenv("GOPATH", gopath)
		}
		setIfUnset("GOMODCACHE", filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod"))
	}
}

func setIfUnset(key, value string) {
	if os.Getenv(key) == "" {
		os.Setenv(key, value)
	}
}
