//go:build darwin

package autostart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A plist alone isn't "starts at login": launchd must have the job loaded.
func TestStatusNeedsTheJobLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ctx := context.Background()
	loaded := false
	var calls [][]string
	orig := launchctl
	t.Cleanup(func() { launchctl = orig })
	launchctl = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if loaded {
			return []byte("state = running\n"), nil
		}
		return []byte("Could not find service\n"), errors.New("exit status 113")
	}
	in := darwinInstaller{}
	if ok, where := in.Status(ctx); ok || where != "" || len(calls) != 0 {
		t.Fatalf("no plist: %v %q, calls %v", ok, where, calls)
	}
	path, _ := plistPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("<plist/>"), 0o644)
	if ok, where := in.Status(ctx); ok || !strings.Contains(where, "not loaded") {
		t.Fatalf("not loaded: %v %q", ok, where)
	}
	loaded = true
	if ok, where := in.Status(ctx); !ok || where != path {
		t.Fatalf("loaded: %v %q", ok, where)
	}
	if got := strings.Join(calls[len(calls)-1], " "); got != "print "+launchdDomain()+"/"+Label {
		t.Fatalf("launchctl %s", got)
	}
}
