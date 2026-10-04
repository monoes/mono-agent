package openaiapi

// A turn must not run for nobody: not when the server begins to stop, or the
// client leaves, while the turn is still starting; and not when the client of a
// chunked request leaves while it runs.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Between counting a turn in and starting its process (finding monomind,
// preparing the folders) the server may begin to stop. Starting an agent CLI
// only to cancel it a moment later wastes a process, and holds the shutdown up
// for the kill grace.
func TestRunTurnDoesNotStartAProcessOnceTheServerHasBegunToStop(t *testing.T) {
	var started atomic.Int32
	var h *harness
	h = newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		started.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) {
			go h.g.Shutdown(5 * time.Second) // the server begins to stop while the turn is starting
			for !h.g.stopping() {
				time.Sleep(time.Millisecond)
			}
			return "/fake/monomind", nil
		}
	})

	_, err := h.g.runTurn(context.Background(), turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	if !errors.Is(err, errShuttingDown) {
		t.Fatalf("err = %v, want errShuttingDown", err)
	}
	if started.Load() != 0 {
		t.Error("no process may be started for a turn the server is already stopping")
	}
}

func TestRunTurnDoesNotStartAProcessForAClientThatLeftWhileItStarted(t *testing.T) {
	var started atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		started.Add(1)
		return okTurn("x")(ctx, o, onEvent)
	}, func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) {
			cancel() // the client leaves while the turn is starting
			return "/fake/monomind", nil
		}
	})

	res, err := h.g.runTurn(ctx, turn{Runtime: "claude", Model: "default", Prompt: "p", Policy: anyPolicy})
	if err != nil || res == nil || res.Err == nil || res.Err.Code != monomind.ErrCancelled {
		t.Fatalf("want a cancelled result, got %+v, %v", res, err)
	}
	if started.Load() != 0 {
		t.Error("no process may be started for a client that is already gone")
	}
}

// net/http notices that a client has gone only once the request body has been
// read to its end. A decoder stops at the closing brace, and the end of a
// chunked body comes after it, so the body is read to the end: otherwise a client
// that leaves while its turn runs goes unnoticed, and the turn runs for nobody
// until its timeout.
func TestAClientThatLeavesDuringAChunkedRequestEndsItsTurn(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	h := newHarness(t, func(ctx context.Context, _ monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
		close(started)
		<-ctx.Done()
		close(ended)
		return &monomind.TurnResult{SawDone: true, Err: &monomind.ProtocolError{Code: monomind.ErrCancelled, Message: "cancelled"}}, nil
	})
	secret := h.key(t, "default", "app", false)
	srv := httptest.NewServer(h.g.Handler(anyPolicy))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body := `{"model":"claude","messages":[{"role":"user","content":"hi"}]}`
	// The data chunk and the terminating chunk arrive apart, as they do from a
	// client that writes them separately: the decoder is done after the first.
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\n"+
		"Transfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", secret, len(body), body)
	time.Sleep(200 * time.Millisecond)
	fmt.Fprint(conn, "0\r\n\r\n")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never started")
	}

	conn.Close() // the client leaves while the turn runs
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		h.g.Shutdown(5 * time.Second) // so that the server can close, and the test report the failure
		t.Fatal("the turn kept running for a client that left")
	}
}
