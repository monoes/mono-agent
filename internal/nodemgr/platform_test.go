package nodemgr

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWithin(t *testing.T) {
	root := filepath.FromSlash("/home/me/.monoagent/node")
	for path, want := range map[string]bool{
		"/home/me/.monoagent/node":            true,
		"/home/me/.monoagent/node/24.1.0/bin": true,
		"/home/me/.monoagent/node/":           true,
		"/home/me/.monoagent/node-foo/bin":    false, // what HasPrefix matched
		"/home/me/.monoagent/nodes":           false,
		"/home/me/.monoagent":                 false,
		"/usr/bin":                            false,
		"/home/me/.monoagent/node/../npm/bin": false,
		"/home/me/.monoagent/node/..foo/bin":  true, // a folder named "..foo" is inside
	} {
		if got := within(filepath.FromSlash(path), root); got != want {
			t.Errorf("within(%q) = %v, want %v", path, got, want)
		}
	}
}

// A system Node in a folder that only shares the managed root's prefix is
// still a system Node.
func TestSystemNodeNextToRoot(t *testing.T) {
	m := testManager(t)
	sys := m.Root + "-foo"
	os.MkdirAll(sys, 0o755)
	os.WriteFile(filepath.Join(sys, "node"), []byte("#!/bin/sh\necho v24.5.0\n"), 0o755)
	t.Setenv("PATH", sys)
	if p, v, ok := m.SystemNode(context.Background()); !ok || v != "24.5.0" || p != filepath.Join(sys, "node") {
		t.Fatalf("SystemNode = %q %q %v", p, v, ok)
	}
}

func TestArchMapping(t *testing.T) {
	for _, c := range []struct{ goos, goarch, archive, key string }{
		{"linux", "amd64", "node-v22.12.0-linux-x64.tar.gz", "linux-x64"},
		{"linux", "arm", "node-v22.12.0-linux-armv7l.tar.gz", "linux-armv7l"},
		{"linux", "ppc64le", "node-v22.12.0-linux-ppc64le.tar.gz", "linux-ppc64le"},
		{"windows", "386", "node-v22.12.0-win-x86.zip", "win-x86-zip"},
		{"windows", "arm64", "node-v22.12.0-win-arm64.zip", "win-arm64-zip"},
		{"darwin", "amd64", "node-v22.12.0-darwin-x64.tar.gz", "osx-x64-tar"},
	} {
		m := &Manager{GOOS: c.goos, GOARCH: c.goarch, sysRoot: t.TempDir()}
		if got := m.archiveName("22.12.0"); got != c.archive {
			t.Errorf("%s/%s archive %q, want %q", c.goos, c.goarch, got, c.archive)
		}
		if got := m.indexFileKey(); got != c.key {
			t.Errorf("%s/%s index key %q, want %q", c.goos, c.goarch, got, c.key)
		}
		if err := m.checkPlatform(); err != nil {
			t.Errorf("%s/%s: %v", c.goos, c.goarch, err)
		}
	}
	for _, p := range [][2]string{{"linux", "386"}, {"darwin", "arm"}, {"linux", "riscv64"}, {"freebsd", "amd64"}} {
		m := &Manager{GOOS: p[0], GOARCH: p[1], sysRoot: t.TempDir()}
		if err := m.checkPlatform(); err == nil || !strings.Contains(err.Error(), "no Node.js build for "+p[0]+"/"+p[1]) {
			t.Errorf("%s/%s: %v", p[0], p[1], err)
		}
	}
}

// musl-only Linux gets told why, before anything is downloaded; with a
// glibc loader too (gcompat) the official build runs.
func TestMuslIsExplained(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib"), 0o755)
	os.WriteFile(filepath.Join(root, "lib", "ld-musl-x86_64.so.1"), nil, 0o755)
	m := &Manager{GOOS: "linux", GOARCH: "amd64", sysRoot: root, BaseURL: "http://127.0.0.1:1"}
	_, err := m.Install(context.Background(), "24.1.0", nil)
	if err == nil || !strings.Contains(err.Error(), "musl") || !strings.Contains(err.Error(), "apk add nodejs") {
		t.Fatalf("musl: %v", err)
	}
	os.WriteFile(filepath.Join(root, "lib", "ld-linux-x86-64.so.2"), nil, 0o755)
	if err := m.checkPlatform(); err != nil {
		t.Fatalf("musl with a glibc loader: %v", err)
	}
	if (&Manager{GOOS: "linux", GOARCH: "amd64", sysRoot: t.TempDir()}).musl() {
		t.Fatal("an empty root is not musl")
	}
}

// The system Node's version is probed once, then read from the cache while
// the executable is unchanged.
func TestSystemNodeVersionIsCached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script node")
	}
	m := testManager(t)
	os.MkdirAll(m.Root, 0o755)
	sys := t.TempDir()
	count := filepath.Join(t.TempDir(), "count")
	node := filepath.Join(sys, "node")
	os.WriteFile(node, []byte("#!/bin/sh\necho x >> "+count+"\necho v20.1.0\n"), 0o755)
	t.Setenv("PATH", sys)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, v, ok := m.SystemNode(ctx); !ok || v != "20.1.0" {
			t.Fatalf("SystemNode = %q %v", v, ok)
		}
	}
	if b, _ := os.ReadFile(count); strings.Count(string(b), "x") != 1 {
		t.Fatalf("node --version ran %d times, want 1", strings.Count(string(b), "x"))
	}
	// An upgrade (new content) is probed again.
	os.WriteFile(node, []byte("#!/bin/sh\necho x >> "+count+"\necho v24.10.0 # upgraded\n"), 0o755)
	if _, v, _ := m.SystemNode(ctx); v != "24.10.0" {
		t.Fatalf("after an upgrade: %q", v)
	}
	// Without a managed Node, nothing is written (Root isn't created).
	m2 := testManager(t)
	if _, _, ok := m2.SystemNode(ctx); !ok {
		t.Fatal("no system node")
	}
	if _, err := os.Stat(m2.Root); err == nil {
		t.Fatal("probing the system Node created the managed root")
	}
}
