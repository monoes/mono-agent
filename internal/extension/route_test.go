package extension

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// answerNext reads the next command on conn and answers it with data after
// delay. It returns the command so the test can check what arrived where.
func answerNext(t *testing.T, conn *websocket.Conn, delay time.Duration, data any) *Command {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Errorf("read command: %v", err)
		return nil
	}
	var cmd Command
	if err := json.Unmarshal(msg, &cmd); err != nil {
		t.Errorf("decode command: %v", err)
		return nil
	}
	time.Sleep(delay)
	if err := conn.WriteJSON(Response{ID: cmd.ID, Success: true, Data: data}); err != nil {
		t.Errorf("write response: %v", err)
	}
	return &cmd
}

// expectSilence fails if conn receives anything within d.
func expectSilence(t *testing.T, conn *websocket.Conn, d time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	if _, msg, err := conn.ReadMessage(); err == nil {
		t.Fatalf("unexpected frame: %s", msg)
	}
}

func TestSendCommandToRoutesByProfile(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	done := make(chan int, 1)
	go func() {
		id, err := srv.CreateTabFor(Target{Profile: "p-b"}, "https://example.com")
		if err != nil {
			t.Errorf("CreateTabFor: %v", err)
		}
		done <- id
	}()
	cmd := answerNext(t, b, 0, map[string]any{"tabId": 42})
	if cmd == nil || cmd.Type != CmdCreateTab {
		t.Fatalf("B got %+v, want create_tab", cmd)
	}
	if id := <-done; id != 42 {
		t.Fatalf("tab id = %d, want 42", id)
	}
	expectSilence(t, a, 200*time.Millisecond)
}

func TestTwoBrowsersRunInParallel(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	const slow = 300 * time.Millisecond
	go answerNext(t, a, slow, map[string]any{"tabId": 1})
	go answerNext(t, b, slow, map[string]any{"tabId": 2})

	start := time.Now()
	var wg sync.WaitGroup
	for _, p := range []string{"p-a", "p-b"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			if _, err := srv.CreateTabFor(Target{Profile: p}, "https://example.com"); err != nil {
				t.Errorf("%s: %v", p, err)
			}
		}(p)
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 2*slow-50*time.Millisecond {
		t.Fatalf("two browsers took %s — they ran one after the other", elapsed)
	}
}

func TestSetBindingUpdatesTheServersView(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "Old")
	waitBrowsers(t, srv, 1)

	errc := make(chan error, 1)
	go func() {
		_, err := srv.SendCommandTo(Target{Instance: instA}, &Command{
			Type: CmdSetBinding, Params: map[string]interface{}{"profile": "p-z", "label": "New"},
		}, 5*time.Second)
		errc <- err
	}()
	cmd := answerNext(t, a, 0, map[string]any{"profile": "p-z"})
	if cmd == nil || cmd.Type != CmdSetBinding {
		t.Fatalf("A got %+v", cmd)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	got := srv.Browsers()[0]
	if got.Profile != "p-z" || got.Label != "New" {
		t.Fatalf("after set_binding: %+v", got)
	}
}

func TestBindingFrameUpdatesTheServersView(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "", "")
	waitBrowsers(t, srv, 1)
	if err := a.WriteJSON(map[string]string{"kind": KindBinding, "profile": "p-q", "label": "Work Edge"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := srv.Browsers()[0]
		if got.Profile == "p-q" && got.Label == "Work Edge" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding frame not applied: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCapturePageGoesToTheTargetBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	srv.SetCaptureInbox(t.TempDir())
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	errc := make(chan error, 1)
	go func() {
		_, err := srv.CapturePage(CaptureRequest{Target: Target{Profile: "p-a"}, Timeout: 5 * time.Second})
		errc <- err
	}()
	_ = a.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := a.ReadMessage()
	if err != nil {
		t.Fatalf("A read: %v", err)
	}
	var cmd Command
	_ = json.Unmarshal(msg, &cmd)
	if cmd.Type != CmdPageCapture {
		t.Fatalf("A got %s", msg)
	}
	_ = a.WriteJSON(Response{ID: cmd.ID, Success: false, Error: "stop here", Type: CmdPageCapture})
	if err := <-errc; err == nil {
		t.Fatal("capture should report the extension's error")
	}
	expectSilence(t, b, 200*time.Millisecond)
}

func TestNoBrowserErrorReachesTheCaller(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	_, err := srv.CreateTabFor(Target{Profile: "p-b"}, "https://example.com")
	var nb *NoBrowserError
	if !errors.As(err, &nb) {
		t.Fatalf("err = %v, want *NoBrowserError", err)
	}
}

func TestProfileListDefaultsToTheBoundProfile(t *testing.T) {
	src := func(context.Context) ([]profiledir.Profile, error) {
		return []profiledir.Profile{
			{ID: "p-home", Name: "Personal"},
			{ID: "p-work", Name: "Work", Default: true},
		}, nil
	}
	got, err := listProfiles(context.Background(), src, "p-home")
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != "p-home" || !got.Profiles[0].Default || got.Profiles[1].Default {
		t.Fatalf("bound browser should default to its profile: %+v", got)
	}
	unbound, _ := listProfiles(context.Background(), src, "")
	if unbound.Default != "p-work" {
		t.Fatalf("unbound browser keeps monoagent's default: %+v", unbound)
	}
	stale, _ := listProfiles(context.Background(), src, "p-gone")
	if stale.Default != "p-work" {
		t.Fatalf("a binding to a deleted profile must not win: %+v", stale)
	}
}
