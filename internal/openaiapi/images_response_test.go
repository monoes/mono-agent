package openaiapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// countingWriter is a response writer that keeps nothing of the body it is given.
type countingWriter struct {
	h      http.Header
	status int
	n      int64
}

func (c *countingWriter) Header() http.Header         { return c.h }
func (c *countingWriter) WriteHeader(status int)      { c.status = status }
func (c *countingWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// bigImages is the turn of a runtime that saves count images of the most one may weigh.
func bigImages(count int) execFunc {
	return func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		for i := range count {
			if err := writeSparse(filepath.Join(givenFolder(o), fmt.Sprintf("img-%d.png", i)), pngBytes, maxImageBytes); err != nil {
				return nil, err
			}
		}
		return imageTurn("saved", nil)(ctx, o, onEvent)
	}
}

// serveImages starts a server over the gateway, for a client that talks to it as it likes.
func serveImages(t *testing.T, h *harness) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// postWithoutReading sends an images request and reads the head of the response, and
// no more: the body stays with the server, as it does for a client that stopped reading.
func postWithoutReading(t *testing.T, srv *httptest.Server, secret, body string) {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: images\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", imagesURL, secret, len(body), body)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReaderSize(conn, 512), nil)
	if err != nil {
		t.Fatalf("no head of the response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_ = conn.SetReadDeadline(time.Time{})
}

// The response is written as the images are encoded, not built whole first: the body is
// a third more than the images, and building it holds the images, the body and a copy of
// it at once.
func TestImagesAResponseOfBigImagesIsNotBuiltInMemory(t *testing.T) {
	h := newHarness(t, bigImages(4))
	secret := h.key(t, "default", "app", false)
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	r := httptest.NewRequest(http.MethodPost, imagesURL, strings.NewReader(`{"prompt":"x","n":4}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	w := &countingWriter{h: http.Header{}}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	mux.ServeHTTP(w, r)
	runtime.ReadMemStats(&after)

	if w.status != http.StatusOK || w.n < int64(base64.StdEncoding.EncodedLen(4*maxImageBytes)) {
		t.Fatalf("status %d, %d bytes written: want the four images of 20 MiB", w.status, w.n)
	}
	t.Logf("allocated %d MiB for images of %d MiB", (after.TotalAlloc-before.TotalAlloc)>>20, 4*maxImageBytes>>20)
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 120<<20 {
		t.Errorf("a response of four images of 20 MiB allocated %d MiB: the images are 80 MiB, and the body is to go out as it is encoded", alloc>>20)
	}
}

// The slot protects the turn's folder and its process, and both are done with when the
// images are read: a client that is slow to read its response does not keep the next
// request from a turn.
func TestImagesTheSlotIsGivenBackBeforeTheResponseIsWritten(t *testing.T) {
	var turns atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		if turns.Add(1) == 1 {
			return bigImages(1)(ctx, o, onEvent)
		}
		return imageTurn("a.png", onePNG("a.png"))(ctx, o, onEvent)
	}, func(_ *Deps, c *Config) { c.MaxConcurrent = 1 })
	secret := h.key(t, "default", "app", false)
	srv := serveImages(t, h)

	postWithoutReading(t, srv, secret, `{"prompt":"x"}`) // its response, 27 MB, is not read

	req, _ := http.NewRequest(http.MethodPost, srv.URL+imagesURL, strings.NewReader(`{"prompt":"x"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the second request got %d: the slot is still held by a response that nobody reads", resp.StatusCode)
	}
}

// A client that stops reading is cut when the write budget is up, not at the end of the
// turn timeout the connection was given for the turn: the images stay in memory until the
// last byte is written.
func TestImagesAClientThatDoesNotReadItsResponseIsCutAfterTheWriteBudget(t *testing.T) {
	old := imageWriteBudget
	imageWriteBudget = 300 * time.Millisecond
	t.Cleanup(func() { imageWriteBudget = old })
	h := newHarness(t, bigImages(1))
	srv := serveImages(t, h)

	postWithoutReading(t, srv, h.key(t, "default", "app", false), `{"prompt":"x"}`)

	// The request leaves its line when it ends.
	for end := time.Now().Add(10 * time.Second); len(h.logged()) == 0 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	if len(h.logged()) == 0 {
		t.Fatal("a client that does not read its response still holds the request after the write budget")
	}
}
