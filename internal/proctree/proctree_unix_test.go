//go:build !windows

package proctree

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A grandchild in the child's process group dies with it.
func TestKillEndsTheWholeGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $!; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var grandchild int
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	for _, c := range buf[:n] {
		if c >= '0' && c <= '9' {
			grandchild = grandchild*10 + int(c-'0')
		}
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	Kill(cmd)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Kill did not end the child")
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(grandchild, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived", grandchild)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
