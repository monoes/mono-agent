package monomind

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"testing"
)

func TestCloneForRetryCopiesStartFieldsAndOwnsSysProcAttr(t *testing.T) {
	var out bytes.Buffer
	orig := exec.Command("monomind", "org", "serve", "--cross-process")
	orig.Dir = t.TempDir()
	orig.Env = []string{"A=1"}
	orig.Stdin = os.Stdin
	orig.Stdout = &out
	orig.Stderr = &out
	orig.SysProcAttr = &syscall.SysProcAttr{}

	c := cloneForRetry(orig)
	if c.Path != orig.Path || !reflect.DeepEqual(c.Args, orig.Args) || !reflect.DeepEqual(c.Env, orig.Env) || c.Dir != orig.Dir {
		t.Fatalf("clone lost start fields: %+v", c)
	}
	if c.Stdin != orig.Stdin || c.Stdout != orig.Stdout || c.Stderr != orig.Stderr {
		t.Fatal("clone lost stdio")
	}
	if c.SysProcAttr == nil || c.SysProcAttr == orig.SysProcAttr {
		t.Fatal("clone must own a copy of SysProcAttr, so clearing a flag on it leaves the original alone")
	}
	if c.Process != nil {
		t.Fatal("clone must be unstarted")
	}
}

func TestCloneForRetryNilSysProcAttr(t *testing.T) {
	if c := cloneForRetry(exec.Command("x")); c.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %+v, want nil", c.SysProcAttr)
	}
}
