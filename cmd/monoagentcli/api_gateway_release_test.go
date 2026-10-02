package main

// The gateway takes the working folders of the home when it is built. What fails
// before a listener serves it must give them back, or a process that serves
// nothing keeps every other one from serving /v1.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/openaiapi"
)

// A dedicated listener that cannot bind must not have taken the folders first.
func TestStartV1DoesNotBuildTheGatewayWhenItCannotBind(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0") // an address in use
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: busy.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.startV1(context.Background()); err == nil {
		t.Fatal("binding an address in use must fail")
	}
	if rt.built() != nil {
		t.Error("a listener that could not bind must not leave the working folders held")
	}
	other := newAPIRuntimeSharingHome(t, apiFlags{}, func(string, ...any) {})
	if other.mainMount("127.0.0.1:9322") == nil {
		t.Error("another process over the same home must be able to serve /v1")
	}
}

// The HTTP API server takes its routes when it is made, so the gateway exists
// before its listener is bound: a bind that then fails hands it back.
func TestReleaseUnusedFreesTheWorkingFoldersForAnotherProcess(t *testing.T) {
	first, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if first.mainMount("127.0.0.1:9322") == nil || first.built() == nil {
		t.Fatal("the first process builds the gateway for its main listener")
	}
	second := newAPIRuntimeSharingHome(t, apiFlags{}, func(string, ...any) {})
	if second.mainMount("127.0.0.1:9322") != nil {
		t.Fatal("while the first holds the folders the second serves no /v1")
	}

	first.releaseUnused() // its main listener failed to bind: nothing serves the gateway
	if first.built() != nil {
		t.Error("the gateway must be gone")
	}
	third := newAPIRuntimeSharingHome(t, apiFlags{}, func(string, ...any) {})
	if third.mainMount("127.0.0.1:9322") == nil {
		t.Error("the folders must be free for another process once nothing serves the gateway")
	}
}

func TestReleaseUnusedKeepsTheGatewayTheDedicatedListenerServes(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.startV1(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer rt.drain()

	rt.releaseUnused()
	if rt.built() == nil {
		t.Error("a gateway a listener serves must not be released")
	}
	other := newAPIRuntimeSharingHome(t, apiFlags{}, func(string, ...any) {})
	if other.mainMount("127.0.0.1:9322") != nil {
		t.Error("the folders are still in use")
	}
}

// A process that cannot build the API (its working folders are not writable, say)
// says so in its log and serves its other routes, as it does when another process
// owns them; an explicit --v1-addr is an error, because that listener's only
// purpose is /v1.
func TestMainMountDegradesWhenTheGatewayCannotBeBuilt(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	rt.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.conf.ScratchRoot = filepath.Join(file, "workspaces") // cannot be created: its parent is a file

	if rt.mainMount("127.0.0.1:9322") != nil {
		t.Error("a process that cannot build the API serves no /v1 on its main listener")
	}
	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "does not serve /v1") {
		t.Errorf("it must say so: %q", joined)
	}
	_, err = rt.startV1(context.Background())
	if err == nil || errors.Is(err, openaiapi.ErrScratchBusy) || !strings.Contains(err.Error(), "starting the OpenAI-compatible API") {
		t.Errorf("an explicit --v1-addr must fail with the reason: %v", err)
	}
}

// A second interrupt makes the command exit at once, after it has ended the turns
// in flight. Building the gateway holds the runtime's lock while it empties the
// working folders, which takes as long as a process that keeps changing one makes
// it take: a second Ctrl+C must not wait for that. While the gateway is being
// built it serves nothing, so there is no turn to end.
func TestKillTurnsDoesNotWaitForAGatewayThatIsBeingBuilt(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{})
	if err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock() // what gateway() holds for the whole of the build
	done := make(chan struct{})
	go func() {
		rt.killTurns()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("killTurns waited for the gateway to be built: a second interrupt would not end the command")
	}
	rt.mu.Unlock()
	<-done
}
