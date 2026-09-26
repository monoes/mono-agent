package extension

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// pickAnswer runs PickElement against the fake extension, answering the
// pick_element command with reply (nil: never answer).
func pickAnswer(t *testing.T, timeout time.Duration, reply func(cmd *Command) any) (string, error, *Command) {
	t.Helper()
	srv, ext, _ := startCaptureServer(t)
	page := NewExtensionPage(srv, 12)
	type res struct {
		url string
		err error
	}
	done := make(chan res, 1)
	go func() {
		fp, url, err := page.PickElement(context.Background(), "Click: the Send button", timeout)
		if err == nil && (fp == nil || fp.Tag != "button") {
			err = errors.New("wrong fingerprint")
		}
		done <- res{url, err}
	}()
	cmd := ext.nextCommand()
	if r := reply(cmd); r != nil {
		ext.send(r)
	}
	select {
	case r := <-done:
		return r.url, r.err, cmd
	case <-time.After(timeout + 10*time.Second):
		t.Fatal("PickElement never returned")
		return "", nil, nil
	}
}

func TestPickElementReturnsFingerprint(t *testing.T) {
	url, err, cmd := pickAnswer(t, 2*time.Second, func(cmd *Command) any {
		return map[string]any{"id": cmd.ID, "success": true, "data": map[string]any{
			"url": "https://x.test/compose?token=abc#frag",
			"fingerprint": map[string]any{"tag": "button", "text": "Send",
				"candidates": []any{map[string]any{"kind": "css", "value": "#send", "unique": true, "count": 1, "score": 0.9}}},
		}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://x.test/compose?token=REDACTED" {
		t.Fatalf("url = %q", url)
	}
	if cmd.Type != CmdPickElement || cmd.TabID != 12 || cmd.Params["prompt"] != "Click: the Send button" || cmd.Params["timeoutMs"] != float64(2000) {
		t.Fatalf("command = %+v", cmd)
	}
}

func TestPickElementErrors(t *testing.T) {
	for reason, want := range map[string]error{"cancelled": ErrPickCancelled, "timeout": ErrPickTimeout} {
		_, err, _ := pickAnswer(t, 2*time.Second, func(cmd *Command) any {
			return map[string]any{"id": cmd.ID, "success": false, "error": reason}
		})
		if !errors.Is(err, want) || err.Error() != reason {
			t.Errorf("%s: err = %v", reason, err)
		}
	}
	// The extension never answers: Go's own deadline (timeout + slack).
	start := time.Now()
	_, err, _ := pickAnswer(t, 100*time.Millisecond, func(*Command) any { return nil })
	if !errors.Is(err, ErrPickTimeout) || time.Since(start) < pickSlack {
		t.Errorf("unanswered pick: %v after %s", err, time.Since(start))
	}
	// No fingerprint in a success is an error, not a nil pointer.
	_, err, _ = pickAnswer(t, 2*time.Second, func(cmd *Command) any {
		return map[string]any{"id": cmd.ID, "success": true, "data": map[string]any{"url": "https://x.test/"}}
	})
	if err == nil {
		t.Error("empty fingerprint accepted")
	}
}

func TestPickElementNotConnected(t *testing.T) {
	srv := NewServer("127.0.0.1:0", zerolog.Nop())
	_, _, err := NewExtensionPage(srv, 1).PickElement(context.Background(), "x", time.Second)
	if !errors.Is(err, ErrBridgeNotConnected) || err.Error() != "browser bridge not connected" {
		t.Fatalf("err = %v", err)
	}
}

func TestPickElementHonoursContext(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := NewExtensionPage(srv, 1).PickElement(ctx, "x", time.Minute)
		done <- err
	}()
	ext.nextCommand()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PickElement ignored its context")
	}
}
