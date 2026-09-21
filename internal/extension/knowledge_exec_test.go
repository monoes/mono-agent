//go:build !windows

package extension

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Cancelling an exec.Cmd's context kills the process. It does not close the
// process's stdout — and cmd.Output() waits for that pipe to reach EOF, not
// for the process to exit. Any grandchild that inherited the pipe therefore
// holds this exec open long past its deadline: monomind spawns node, node
// spawns workers, and a killed CLI that leaves one behind wedges the
// handler that called it. That handler then holds one of the request
// channel's eight slots (see request_deadline_test.go) for as long as the
// grandchild lives.
func TestKnowledgeRunnerDoesNotWaitOnAGrandchildHoldingStdout(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "monomind")
	// `sleep 30 &` inherits stdout and outlives the kill: a stand-in for
	// any worker process monomind leaves running.
	script := "#!/bin/sh\nsleep 30 &\nsleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake monomind: %v", err)
	}
	t.Setenv(monomind.EnvOverride, bin)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		r := &cliRunner{}
		_, err := r.Run(ctx, "doc", "list", "--json")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a killed monomind reported success")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("Run took %s to give up on a dead monomind", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run was still waiting 5s after its context expired: " +
			"a grandchild is holding monomind's stdout pipe open")
	}
}
