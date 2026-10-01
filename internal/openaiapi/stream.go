package openaiapi

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// sseWriteDeadline drops a client that stops reading: one write may not
// block longer than this.
const sseWriteDeadline = 30 * time.Second

// sseWriter streams one chat completion as server-sent events.
//
// The 200 response is committed lazily: on the first content, or when the
// turn has been silent for a while (so keep-alives can flow). Until then a
// failing turn is still answered with its real HTTP status; after it, a
// failure is an SSE error event, because the status line is already sent.
type sseWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	id      string
	model   string
	created int64

	mu          sync.Mutex
	committed   bool
	sentContent bool
	broken      bool // a write failed: the client is gone
}

func newSSE(w http.ResponseWriter, id, model string) *sseWriter {
	return &sseWriter{w: w, rc: http.NewResponseController(w), id: id, model: model, created: time.Now().Unix()}
}

func (s *sseWriter) chunk(c delta, finish *string) chunk {
	return chunk{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
		Choices: []chunkChoice{{Delta: c, FinishReason: finish}}}
}

// write sends one SSE payload and flushes it. Callers hold s.mu.
func (s *sseWriter) write(payload string) {
	if s.broken {
		return
	}
	_ = s.rc.SetWriteDeadline(time.Now().Add(sseWriteDeadline))
	if _, err := io.WriteString(s.w, payload); err != nil {
		s.broken = true
		return
	}
	_ = s.rc.Flush()
}

func (s *sseWriter) data(v any) {
	b, _ := json.Marshal(v)
	s.write("data: " + string(b) + "\n\n")
}

// commitLocked sends the status line, the headers and the role chunk.
func (s *sseWriter) commitLocked() {
	if s.committed {
		return
	}
	s.committed = true
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	s.data(s.chunk(delta{Role: "assistant", Content: strPtr("")}, nil))
}

// commit is commitLocked for callers that don't hold the lock.
func (s *sseWriter) commit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitLocked()
}

// delta streams a piece of the answer.
func (s *sseWriter) delta(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitLocked()
	s.sentContent = true
	s.data(s.chunk(delta{Content: &text}, nil))
}

// keepAlive sends an SSE comment so proxies don't drop an idle stream.
func (s *sseWriter) keepAlive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		s.write(": keep-alive\n\n")
	}
}

// finish completes a successful turn. A runtime that did not stream gets its
// whole answer as one chunk here.
func (s *sseWriter) finish(res *monomind.TurnResult, includeUsage bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitLocked()
	if !s.sentContent && res.ResultText != "" {
		text := res.ResultText
		s.data(s.chunk(delta{Content: &text}, nil))
	}
	reason := finishReason(res)
	s.data(s.chunk(delta{}, &reason))
	if u := usageFrom(res); includeUsage && u != nil {
		s.data(chunk{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model,
			Choices: []chunkChoice{}, Usage: u})
	}
	s.write("data: [DONE]\n\n")
}

// fail reports a failed turn. It returns false, having written nothing, when
// the response is not committed yet: the caller then sends a normal HTTP
// error. Once committed the failure goes out as an SSE error event.
func (s *sseWriter) fail(e *apiError) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.committed {
		return false
	}
	s.write("data: " + string(e.body()) + "\n\n")
	s.write("data: [DONE]\n\n")
	return true
}
