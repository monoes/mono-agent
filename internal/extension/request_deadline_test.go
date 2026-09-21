package extension

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The deadline invariant, which this file's header states as "every request
// is answered exactly once, including when its handler … blocks past its
// deadline".
//
// A handler is arbitrary code. Passing it a context that expires is a
// request, not a guarantee: a handler that never selects on ctx.Done() —
// one parked on a channel, or on an exec whose stdout pipe a grandchild is
// still holding open — settles nothing and, worse, never gives its
// in-flight slot back. Eight of those and the channel is busy for the rest
// of the process's life, with nothing in the logs to say why.
func TestRequestChannelSettlesAHandlerThatIgnoresItsDeadline(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	srv.SetRequestTimeout(300 * time.Millisecond)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.HandleRequest("wedged", func(context.Context, *Request, ProgressFunc) (any, error) {
		<-release // never looks at ctx.Done(): that is the whole point
		return true, nil
	})
	srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) {
		return "alive", nil
	})

	// Fill every slot with a handler that will not return on its own.
	for i := 0; i < maxInflightRequests; i++ {
		ext.ask(fmt.Sprintf("req-wedged-%d", i), "wedged", nil)
	}

	// Each one is settled at its deadline, not left hanging.
	for i := 0; i < maxInflightRequests; i++ {
		reply := ext.settled()
		if reply.OK {
			t.Fatalf("%s: a handler that never returned answered ok", reply.ID)
		}
		if reply.Code != CodeTimeout {
			t.Fatalf("%s: code = %q, want %q", reply.ID, reply.Code, CodeTimeout)
		}
	}

	// And the slots came back with the replies.
	ext.ask("req-after", "echo", nil)
	reply := ext.settled()
	if reply.ID != "req-after" {
		t.Fatalf("reply id = %q, want req-after", reply.ID)
	}
	if !reply.OK {
		t.Fatalf("the channel is still busy after %d handlers timed out: %s (%s)",
			maxInflightRequests, reply.Error, reply.Code)
	}
}

// A handler that outlives its deadline must not go on writing progress
// frames for a request that was already settled — the extension has moved
// on, and a reply id it no longer knows is noise at best.
func TestRequestChannelSilencesProgressAfterTheDeadline(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	srv.SetRequestTimeout(200 * time.Millisecond)

	settledNow := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.HandleRequest("chatty", func(_ context.Context, _ *Request, progress ProgressFunc) (any, error) {
		<-settledNow
		progress("still-going", "after the deadline")
		<-release
		return true, nil
	})
	srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) {
		return "alive", nil
	})

	ext.ask("req-chatty", "chatty", nil)
	reply := ext.settled()
	if reply.Code != CodeTimeout {
		t.Fatalf("code = %q, want %q", reply.Code, CodeTimeout)
	}
	close(settledNow)

	// The next settling frame belongs to the next request, with no stray
	// progress for the timed-out one in between.
	ext.ask("req-next", "echo", nil)
	for {
		next := ext.nextReply()
		if next.Progress != nil {
			t.Fatalf("progress %q arrived for %s after it was settled", next.Progress.Stage, next.ID)
		}
		if next.ID == "req-next" {
			return
		}
	}
}
