package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// The request channel's contract, from the browser's side of the socket.
//
// Every test here drives a real Server over a real WebSocket through the
// same fake extension the capture tests use, because the properties that
// matter are wire properties: a request is correlated by id, it is answered
// exactly once, and nothing the handler does can leave the extension
// waiting forever.

// ask writes one request frame and returns its id.
func (f *fakeExtension) ask(id, method string, params map[string]any) string {
	f.t.Helper()
	f.send(Request{Kind: KindRequest, ID: id, Method: method, Params: params})
	return id
}

// nextReply reads frames until one is a reply, skipping anything else the
// server interleaves (pings are control frames and never surface here, but
// a command might).
func (f *fakeExtension) nextReply() *Reply {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = f.conn.SetReadDeadline(deadline)
		_, msg, err := f.conn.ReadMessage()
		if err != nil {
			f.t.Fatalf("read reply: %v", err)
		}
		var reply Reply
		if err := json.Unmarshal(msg, &reply); err != nil || reply.Kind != KindReply {
			continue
		}
		return &reply
	}
}

// settled reads past any progress frames to the one frame that settles the
// request.
func (f *fakeExtension) settled() *Reply {
	f.t.Helper()
	for {
		reply := f.nextReply()
		if reply.Progress == nil {
			return reply
		}
	}
}

func TestRequestChannelAnswersByID(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	srv.HandleRequest("echo", func(_ context.Context, req *Request, _ ProgressFunc) (any, error) {
		return map[string]any{"said": req.String("say")}, nil
	})

	ext.ask("req-1", "echo", map[string]any{"say": "hello"})
	reply := ext.settled()

	if reply.ID != "req-1" {
		t.Fatalf("reply id = %q, want req-1", reply.ID)
	}
	if !reply.OK {
		t.Fatalf("reply not ok: %s (%s)", reply.Error, reply.Code)
	}
	data, _ := reply.Data.(map[string]any)
	if data["said"] != "hello" {
		t.Fatalf("data = %#v, want said=hello", reply.Data)
	}
}

// Two requests in flight at once must not be able to take each other's
// answer — which is the entire reason the channel carries correlation ids
// rather than assuming one question at a time.
func TestRequestChannelCorrelatesConcurrentRequests(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	release := make(chan struct{})
	srv.HandleRequest("slow", func(_ context.Context, req *Request, _ ProgressFunc) (any, error) {
		<-release // both handlers are parked until the second request is in
		return map[string]any{"who": req.String("who")}, nil
	})

	ext.ask("req-a", "slow", map[string]any{"who": "a"})
	ext.ask("req-b", "slow", map[string]any{"who": "b"})
	close(release)

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		reply := ext.settled()
		data, _ := reply.Data.(map[string]any)
		who, _ := data["who"].(string)
		got[reply.ID] = who
	}
	if got["req-a"] != "a" || got["req-b"] != "b" {
		t.Fatalf("replies crossed: %#v", got)
	}
}

func TestRequestChannelUnknownMethod(t *testing.T) {
	_, ext, _ := startCaptureServer(t)

	ext.ask("req-x", "nope.not.a.method", nil)
	reply := ext.settled()

	if reply.OK {
		t.Fatal("unknown method answered ok")
	}
	if reply.Code != CodeUnknownMethod {
		t.Fatalf("code = %q, want %q", reply.Code, CodeUnknownMethod)
	}
}

// A handler that panics must still settle its request. An extension waiting
// on a reply that never comes is the one failure this channel cannot
// recover from by itself.
func TestRequestChannelSettlesAPanickingHandler(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	srv.HandleRequest("boom", func(context.Context, *Request, ProgressFunc) (any, error) {
		panic("handler exploded")
	})

	ext.ask("req-boom", "boom", nil)
	reply := ext.settled()

	if reply.OK {
		t.Fatal("panicking handler answered ok")
	}
	if reply.Code != CodeInternal {
		t.Fatalf("code = %q, want %q", reply.Code, CodeInternal)
	}
	if reply.ID != "req-boom" {
		t.Fatalf("reply id = %q, want req-boom", reply.ID)
	}
}

func TestRequestChannelCarriesErrorCodes(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	srv.HandleRequest("missing", func(context.Context, *Request, ProgressFunc) (any, error) {
		return nil, Unavailable("monomind is not installed")
	})
	srv.HandleRequest("broken", func(context.Context, *Request, ProgressFunc) (any, error) {
		return nil, errors.New("something went wrong")
	})
	// A wrapped RequestError must keep its code: handlers annotate errors
	// on the way out, and an annotated "unavailable" that arrives as
	// "internal" turns a quiet absence into a red box.
	srv.HandleRequest("wrapped", func(context.Context, *Request, ProgressFunc) (any, error) {
		return nil, fmt.Errorf("looking up the page: %w", Unavailable("no monomind"))
	})

	for _, tc := range []struct{ method, code string }{
		{"missing", CodeUnavailable},
		{"broken", CodeInternal},
		{"wrapped", CodeUnavailable},
	} {
		ext.ask("req-"+tc.method, tc.method, nil)
		reply := ext.settled()
		if reply.OK {
			t.Fatalf("%s answered ok", tc.method)
		}
		if reply.Code != tc.code {
			t.Fatalf("%s code = %q, want %q", tc.method, reply.Code, tc.code)
		}
	}
}

// Progress frames arrive before the settling frame and do not settle it.
func TestRequestChannelStreamsProgress(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	srv.HandleRequest("staged", func(_ context.Context, _ *Request, progress ProgressFunc) (any, error) {
		progress("searching", "12 documents")
		progress("citing", "1/2")
		return map[string]any{"done": true}, nil
	})

	ext.ask("req-p", "staged", nil)

	var stages []string
	for {
		reply := ext.nextReply()
		if reply.Progress == nil {
			if !reply.OK {
				t.Fatalf("settling frame not ok: %s", reply.Error)
			}
			break
		}
		if reply.OK {
			t.Fatal("a progress frame must not claim ok")
		}
		stages = append(stages, reply.Progress.Stage)
	}
	if strings.Join(stages, ",") != "searching,citing" {
		t.Fatalf("stages = %v, want [searching citing]", stages)
	}
}

// A request that arrives while the in-flight bound is full is refused
// immediately rather than queued — the browser can ask again, and an
// unbounded goroutine fan-out driven from a tab is not something to offer.
func TestRequestChannelRefusesWhenBusy(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(maxInflightRequests)
	srv.HandleRequest("park", func(context.Context, *Request, ProgressFunc) (any, error) {
		started.Done()
		<-release
		return true, nil
	})
	defer close(release)

	for i := 0; i < maxInflightRequests; i++ {
		ext.ask(fmt.Sprintf("req-park-%d", i), "park", nil)
	}
	started.Wait()

	ext.ask("req-over", "park", nil)
	reply := ext.settled()
	if reply.ID != "req-over" {
		t.Fatalf("reply id = %q, want req-over", reply.ID)
	}
	if reply.Code != CodeBusy {
		t.Fatalf("code = %q, want %q", reply.Code, CodeBusy)
	}
}

// The built-in ping is what the extension probes with before showing a
// panel that depends on this channel existing at all.
func TestRequestChannelPingListsMethods(t *testing.T) {
	_, ext, _ := startCaptureServer(t)

	ext.ask("req-ping", MethodPing, nil)
	reply := ext.settled()
	if !reply.OK {
		t.Fatalf("ping failed: %s", reply.Error)
	}
	data, _ := reply.Data.(map[string]any)
	raw, _ := data["methods"].([]any)
	have := map[string]bool{}
	for _, m := range raw {
		s, _ := m.(string)
		have[s] = true
	}
	for _, want := range []string{MethodPing, MethodDocLookup, MethodDocAsk, MethodDocRelated} {
		if !have[want] {
			t.Fatalf("ping did not advertise %q; got %v", want, raw)
		}
	}
}

// A request frame must not be mistaken for a response to something this
// process sent — and a response must not be mistaken for a request.
func TestIsRequestFrame(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"request", `{"kind":"request","id":"1","method":"ping"}`, true},
		{"reply", `{"kind":"reply","id":"1","ok":true}`, false},
		{"plain response", `{"id":"1","success":true,"data":{}}`, false},
		{"capture push", `{"id":"ext-1","success":true,"type":"page_capture","data":{}}`, false},
		{"garbage", `not json`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		if got := isRequestFrame([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: isRequestFrame = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A request frame carries no `success`, so before the channel existed it
// would have decoded as a failed Response and been logged as unmatched.
// This pins that the read loop routes it to the handler instead, leaving
// the capture path untouched.
func TestRequestFrameDoesNotReachCaptureDispatch(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)

	captures := make(chan error, 1)
	srv.OnCapture(func(_ *capture.Result, err error) { captures <- err })

	ext.ask("req-not-a-capture", MethodPing, nil)
	if reply := ext.settled(); !reply.OK {
		t.Fatalf("ping failed: %s", reply.Error)
	}
	select {
	case err := <-captures:
		t.Fatalf("a request frame was handled as a capture: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRequestParamCoercion(t *testing.T) {
	req := &Request{Params: map[string]any{
		"url":     "  https://example.com  ",
		"limit":   float64(7),
		"stringy": "12",
		"bogus":   []any{1},
	}}
	if got := req.String("url"); got != "https://example.com" {
		t.Errorf("String trimmed to %q", got)
	}
	if got := req.String("missing"); got != "" {
		t.Errorf("missing String = %q, want empty", got)
	}
	if got := req.Int("limit", 3); got != 7 {
		t.Errorf("Int = %d, want 7", got)
	}
	if got := req.Int("stringy", 3); got != 12 {
		t.Errorf("Int of a numeric string = %d, want 12", got)
	}
	if got := req.Int("bogus", 3); got != 3 {
		t.Errorf("Int of a non-number = %d, want the default 3", got)
	}
	if got := req.Int("absent", 3); got != 3 {
		t.Errorf("absent Int = %d, want the default 3", got)
	}
}
