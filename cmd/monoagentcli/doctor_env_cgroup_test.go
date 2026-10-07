package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBridgeUserServiceAcceptsBridgeInUnitCgroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux only")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$3\" in\n--property=MainPID) echo 100;;\n--property=ControlGroup) case \"$6\" in\nsys.service) echo /system.slice/sys.service;;\n*) echo /user.slice/user-1000.slice/user@1000.service/app.slice/b.service;;\nesac;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	proc := t.TempDir()
	old := procRoot
	procRoot = proc
	t.Cleanup(func() { procRoot = old })
	write := func(pid, cg string) {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, pid, "cgroup"), []byte(cg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("200", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/b.service/child\n")
	write("201", "0::/user.slice/user-1000.slice/user@1000.service/app.slice/other.service\n")
	write("202", "0::/system.slice/sys.service\n")
	for _, tc := range []struct {
		pid        int
		unit, want string
	}{
		{200, "b.service", "b.service"}, // wrapper is MainPID, bridge is in the cgroup
		{201, "b.service", ""},          // another unit's cgroup
		{202, "sys.service", ""},        // system service never accepted
		{203, "b.service", ""},          // cgroup unreadable
	} {
		if got := bridgeUserService(context.Background(), tc.pid, tc.unit); got != tc.want {
			t.Errorf("bridgeUserService(%d, %q) = %q, want %q", tc.pid, tc.unit, got, tc.want)
		}
	}
}
