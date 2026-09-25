package extension

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/capture"
	"github.com/monoes/mono-agent/internal/recording"
)

// Activity recordings (spec §8.2, contracts §6).
//
//	extension → Go, one frame per op, all carrying "kind":"recording"
//	  {"kind":"recording","id":"r-12","op":"start","recordingId":"…",
//	   "tabId":7,"url":"…","title":"…","goal":"…","startedAt":<ms>,
//	   "profile":"…"}
//	  {"kind":"recording","id":"r-13","op":"event","recordingId":"…","event":{…}}
//	  {"kind":"recording","op":"snapshot","recordingId":"…","eventId":"e3",
//	   "name":"dom-e3.html","data":"<raw html, ≤64KB>"}
//	  {"kind":"recording","op":"network","recordingId":"…","net":{…}}
//	  {"kind":"recording","id":"r-99","op":"stop","recordingId":"…","reason":"user"}
//
//	Go → extension, for every frame that carried an id
//	  {"id":"r-13","success":true,"type":"recording"}
//	  {"id":"r-13","success":true,"type":"recording","data":{"dropped":"…"}}
//	  {"id":"r-13","success":false,"type":"recording","error":"…"}
//	  and for stop, once the envelope is on disk:
//	  {"id":"r-99","success":true,"type":"recording",
//	   "data":{"recordingId":"…","id":"<envelope dir>","path":"…","warnings":[…]}}
//
// The heavy lifting — spool, caps, privacy, envelope — is
// internal/recording.Ingest; this file is the socket adapter. Frames are
// applied on the read loop (each is a small append), except a stop, whose
// envelope write (up to 20MB) runs on its own goroutine.

// defaultRecordingReapInterval is how often idle recordings are looked for.
const defaultRecordingReapInterval = time.Minute

// recordingState is the recording slice of Server.
type recordingState struct {
	recMu     sync.Mutex
	recIngest *recording.Ingest
	// recRunner runs this binary's `record … --json` for the record.*
	// request methods (recording_methods.go). Nil means the real binary.
	recRunner Runner
	// onRecording, if set, is told about every recording written (or that
	// failed to be). Tests use it to wait for the stop's async write.
	onRecording func(*capture.Result, error)
	// reapInterval overrides defaultRecordingReapInterval (tests). Read
	// once when the reaper starts.
	reapInterval time.Duration
	// reaperWG lets Close wait for the reaper goroutine to exit;
	// reaperClosed (under recMu) stops a late Start from adding to it
	// while Close is waiting.
	reaperWG     sync.WaitGroup
	reaperClosed bool
}

// SetRecordingReapInterval changes how often idle recordings are looked
// for. Call before Start; tests use it to avoid waiting a minute.
func (s *Server) SetRecordingReapInterval(d time.Duration) {
	s.recMu.Lock()
	s.reapInterval = d
	s.recMu.Unlock()
}

func (s *Server) recordingReapInterval() time.Duration {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.reapInterval > 0 {
		return s.reapInterval
	}
	return defaultRecordingReapInterval
}

// startRecordingReaper runs recordingReaper until ctx ends; Close waits for
// it (waitRecordingReaper).
func (s *Server) startRecordingReaper(ctx context.Context) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.reaperClosed {
		return
	}
	s.reaperWG.Add(1)
	go func() {
		defer s.reaperWG.Done()
		s.recordingReaper(ctx)
	}()
}

// waitRecordingReaper blocks until the reaper goroutine has exited. Only
// meaningful after the server's context was cancelled.
func (s *Server) waitRecordingReaper() {
	s.recMu.Lock()
	s.reaperClosed = true
	s.recMu.Unlock()
	s.reaperWG.Wait()
}

// recordingIngest returns the ingest, building it on first use. Recordings
// go to their own store (recording.StoreDir), never the capture inbox that
// SetCaptureInbox points at: that inbox feeds the knowledge brain.
func (s *Server) recordingIngest() *recording.Ingest {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.recIngest == nil {
		s.recIngest = &recording.Ingest{}
	}
	return s.recIngest
}

// SetRecordingIngest replaces the ingest (tests inject a clock).
func (s *Server) SetRecordingIngest(in *recording.Ingest) {
	s.recMu.Lock()
	s.recIngest = in
	s.recMu.Unlock()
}

// OnRecording registers a callback for every recording envelope written.
func (s *Server) OnRecording(fn func(*capture.Result, error)) {
	s.recMu.Lock()
	s.onRecording = fn
	s.recMu.Unlock()
}

// frameKind returns a raw frame's "kind" ("" for commands' responses,
// which never carry one). One cheap decode shared by every kind.
func frameKind(msg []byte) string {
	var peek struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(msg, &peek) != nil {
		return ""
	}
	return peek.Kind
}

// serveRecording applies one recording frame and acks it.
func (s *Server) serveRecording(msg []byte) {
	var f recording.Frame
	if err := json.Unmarshal(msg, &f); err != nil {
		// Never log the payload: events carry typed values.
		s.logger.Error().Err(err).Int("len", len(msg)).Msg("invalid recording frame")
		return
	}
	ing := s.recordingIngest()
	out, err := ing.Handle(&f)
	if err != nil {
		s.logger.Warn().Err(err).Str("op", f.Op).Str("recording", f.RecordingID).Msg("recording frame refused")
		s.ackRecording(f.ID, false, nil, err.Error())
		return
	}
	if out.Stopped != nil {
		go s.finishRecording(ing, out.Stopped, f.ID)
		return
	}
	var data any
	if out.Dropped != "" {
		data = map[string]any{"dropped": out.Dropped}
	}
	s.ackRecording(f.ID, true, data, "")
}

// finishRecording writes a stopped recording and, when the stop frame
// carried an id, acks it with where the envelope landed.
func (s *Server) finishRecording(ing *recording.Ingest, st *recording.Stopped, ackID string) {
	res, err := ing.Finalize(st)
	s.recMu.Lock()
	fn := s.onRecording
	s.recMu.Unlock()
	if fn != nil {
		fn(res, err)
	}
	if errors.Is(err, recording.ErrNoEvents) {
		// A failed or abandoned start: nothing to keep, nothing wrong.
		s.logger.Info().Str("recording", st.RecordingID()).Msg("discarded a recording with no events")
		s.ackRecording(ackID, true, map[string]any{"recordingId": st.RecordingID(), "discarded": "no events"}, "")
		return
	}
	if err != nil {
		s.logger.Error().Err(err).Str("recording", st.RecordingID()).Msg("recording could not be written")
		s.ackRecording(ackID, false, nil, err.Error())
		return
	}
	s.logger.Info().Str("recording", st.RecordingID()).Str("path", res.Path).Msg("recording saved")
	s.ackRecording(ackID, true, map[string]any{
		"recordingId": st.RecordingID(),
		"id":          filepath.Base(res.Path),
		"path":        res.Path,
		"warnings":    res.Warnings,
	}, "")
}

// ackRecording writes a Response for a frame that carried an id.
func (s *Server) ackRecording(id string, ok bool, data any, errMsg string) {
	if id == "" {
		return
	}
	blob, err := json.Marshal(&Response{ID: id, Success: ok, Data: data, Error: errMsg, Type: RecordingAckType})
	if err != nil {
		return
	}
	s.connMu.Lock()
	conn := s.conn
	s.connMu.Unlock()
	if conn == nil {
		return
	}
	s.writeMu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, blob)
	s.writeMu.Unlock()
	if err != nil {
		s.logger.Debug().Err(err).Str("id", id).Msg("could not ack recording frame")
	}
}

// recordingReaper finalises recordings left by a previous process, then
// every reap interval ends the ones that went idle (tab closed without a
// stop, service worker gone for good).
func (s *Server) recordingReaper(ctx context.Context) {
	// Recordings written into the capture inbox by older builds move to
	// their store first (once; see recording.MigrateLegacy).
	if n, err := recording.MigrateLegacy(); err != nil {
		s.logger.Warn().Err(err).Msg("moving recordings out of the capture inbox")
	} else if n > 0 {
		s.logger.Info().Int("moved", n).Msg("moved recordings out of the capture inbox")
	}
	ing := s.recordingIngest()
	inboxes := recording.StoreDirs()
	if ing.Writer != nil && ing.Writer.Inbox != "" {
		inboxes = []string{ing.Writer.Inbox}
	}
	for _, st := range ing.Recover(inboxes...) {
		s.finishRecording(ing, st, "")
	}
	ticker := time.NewTicker(s.recordingReapInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ing := s.recordingIngest()
			for _, st := range ing.Reap() {
				s.finishRecording(ing, st, "")
			}
		}
	}
}
