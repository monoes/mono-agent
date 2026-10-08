package monomind

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercise actual process exit: a goroutine truncating in this test process
// would pass the existing capture tests but disappear with a real CLI.
func TestStartDrainOutlivesLauncher(t *testing.T) {
	const helperEnv = "MONOMIND_TEST_DRAIN_LAUNCHER"
	if root := os.Getenv(helperEnv); root != "" {
		f, err := os.OpenFile(filepath.Join(root, "capture"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
		if err != nil {
			panic(err)
		}
		cap := &startCapture{f: f}
		if err := cap.startDrain(context.Background(), os.Args[0], root); err != nil {
			panic(err)
		}
		writer := exec.Command("sh", "-c", `i=0; while [ "$i" -lt 30 ]; do head -c 65536 /dev/zero; i=$((i+1)); sleep 0.05; done; echo completed; touch "$1"`, "writer", filepath.Join(root, "done"))
		writer.Stdout, writer.Stderr = cap.stream, cap.stream
		writer, err = startDetached(writer)
		if err != nil {
			panic(err)
		}
		cap.stream.Close()
		os.WriteFile(filepath.Join(root, "writer-pid"), []byte(strconv.Itoa(writer.Process.Pid)), 0600)
		os.WriteFile(filepath.Join(root, "drainer-pid"), []byte(strconv.Itoa(cap.drainer.Process.Pid)), 0600)
		os.Exit(0)
	}
	if runtime.GOOS == "windows" {
		t.Skip("writer fixture needs sh")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js not installed")
	}
	root := t.TempDir()
	launcher := exec.Command(os.Args[0], "-test.run=^TestStartDrainOutlivesLauncher$")
	launcher.Env = append(os.Environ(), helperEnv+"="+root)
	if out, err := launcher.CombinedOutput(); err != nil {
		t.Fatalf("launcher: %v: %s", err, out)
	}
	for _, name := range []string{"writer-pid", "drainer-pid"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := os.Stat(filepath.Join(root, "capture"))
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > captureTail {
			t.Fatalf("capture grew after launcher exit: %d bytes", st.Size())
		}
		if _, err := os.Stat(filepath.Join(root, "done")); err == nil {
			b, _ := os.ReadFile(filepath.Join(root, "capture"))
			if strings.HasSuffix(string(b), "completed\n") {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("writer failed to complete after launcher exit")
}

func TestStartDrainRejectsFailedReadiness(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake node needs sh")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "monomind")
	pins.Lock()
	if pins.nodeDir == nil {
		pins.nodeDir = map[string]string{}
	}
	pins.nodeDir[bin] = dir
	pins.Unlock()
	t.Cleanup(ResetCapabilityCache)
	cap, err := newStartCapture()
	if err != nil {
		t.Fatal(err)
	}
	defer cap.close()
	began := time.Now()
	if err := cap.startDrain(context.Background(), bin, t.TempDir()); err == nil {
		t.Fatal("dead helper must fail readiness")
	}
	if time.Since(began) > time.Second {
		t.Fatal("dead helper took too long to reject")
	}
	if cap.stream != nil {
		if _, err := cap.stream.Write([]byte("x")); err == nil {
			t.Fatal("failed helper kept stream open")
		}
	}
}

func TestWatchStartStopsRunWhenDrainerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("writer fixture needs sh")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js not installed")
	}
	cap, err := newStartCapture()
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.startDrain(context.Background(), os.Args[0], t.TempDir()); err != nil {
		cap.close()
		t.Fatal(err)
	}
	writer := exec.Command("sh", "-c", "sleep 10")
	writer.Stdout, writer.Stderr = cap.stream, cap.stream
	writer, err = startDetached(writer)
	cap.stream.Close()
	if err != nil {
		cap.finishDrain()
		cap.close()
		t.Fatal(err)
	}
	go func() { time.Sleep(50 * time.Millisecond); _ = cap.drainer.Process.Kill() }()
	err = watchStart(context.Background(), writer, "sec", cap, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "output capture failed") {
		t.Fatalf("failed drainer: %v", err)
	}
	if writer.ProcessState == nil {
		t.Fatal("writer was not reaped")
	}
}
