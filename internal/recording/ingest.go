package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// Ingest turns the extension's kind:"recording" frames into capture
// envelopes (contracts §6). Each recording streams into its own spool
// directory inside the store it will land in (see StoreDir) — dot-prefixed like the
// capture assembler's, so a directory watcher skips it and the writer's stale
// sweep eventually removes one nobody finalised — and is written through the
// capture Writer on `stop`, or by Reap once it has sat idle.
//
// Nothing is held in memory beyond counters: events and snapshots go to disk
// as they arrive, so a long recording costs a few kilobytes of RAM.
type Ingest struct {
	// Writer publishes finished recordings. Nil means a Writer for the
	// recording store of the frame's profile (StoreDir).
	Writer *capture.Writer
	// Now supplies the clock; nil means time.Now. Tests inject it to drive
	// the idle reaper.
	Now func() time.Time
	// IdleTimeout ends a recording no frame has touched for this long.
	// Zero means DefaultIdleTimeout.
	IdleTimeout time.Duration
	// MaxRecordings caps recordings per store; zero means
	// MaxRecordingsPerInbox.
	MaxRecordings int

	mu       sync.Mutex
	active   map[string]*session
	finished map[string]time.Time
}

// Limits a recording is held to. Past them frames are dropped (acked, not
// failed) and the envelope's meta says what was lost.
const (
	MaxEvents          = 5000
	MaxSnapshotBytes   = 64 << 10
	MaxTotalBytes      = 20 << 20
	MaxEventLineBytes  = 256 << 10
	DefaultIdleTimeout = 30 * time.Minute
	// finishedTTL is how long a stopped recording's id is remembered, so a
	// late frame is refused instead of opening an orphan recording.
	finishedTTL = 10 * time.Minute
)

// Stop reasons ingest itself assigns.
const (
	StopIdle      = "idle"
	StopRecovered = "recovered"
)

// Frame ops.
const (
	OpStart    = "start"
	OpEvent    = "event"
	OpSnapshot = "snapshot"
	OpNetwork  = "network"
	OpStop     = "stop"
)

// spoolPrefix shares the capture assembler's prefix on purpose: the
// writer's sweepStaleStaging removes abandoned ones after a day.
const spoolPrefix = ".spool-capture-recording-"

// Spool file names.
const (
	startFile = "start.json"
)

// Outcome is what one frame did.
type Outcome struct {
	// Dropped explains a frame that was accepted but not kept (a cap was
	// hit, a duplicate event).
	Dropped string
	// Stopped is set when the frame ended the recording; the caller hands
	// it to Finalize (off the read loop).
	Stopped *Stopped
}

// Stopped is a recording detached from the ingest, ready to be written.
type Stopped struct{ s *session }

// RecordingID is the extension's id for the stopped recording.
func (st *Stopped) RecordingID() string { return st.s.id }

type session struct {
	id         string
	dir        string
	start      Frame
	seen       map[string]bool
	sensitive  map[string]bool // events on a sensitive field: their snippets lose every value
	events     int
	kept       int // events written by envelope (after dedupe and caps)
	bytes      int64
	dropEvents int
	dropSnaps  int
	dropNet    int
	notes      []string
	touched    time.Time
	stopReason string
	complete   bool
}

func (in *Ingest) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

func (in *Ingest) idle() time.Duration {
	if in.IdleTimeout > 0 {
		return in.IdleTimeout
	}
	return DefaultIdleTimeout
}

func (in *Ingest) writer() *capture.Writer {
	if in.Writer != nil {
		return in.Writer
	}
	return &capture.Writer{}
}

// spoolRoot is the store a recording will land in (StoreDir, or the
// Writer's explicit Inbox), so the spool sits beside the envelope it
// becomes. Never the capture inbox.
func (in *Ingest) spoolRoot(profile string) (string, error) {
	if w := in.writer(); strings.TrimSpace(w.Inbox) != "" {
		return w.Inbox, nil
	}
	if dir, err := StoreDir(profile); err == nil {
		return dir, nil
	}
	return StoreDir("")
}

// Handle applies one frame. An error means the frame was refused; the
// caller acks it with Success=false.
func (in *Ingest) Handle(f *Frame) (Outcome, error) {
	if f == nil {
		return Outcome{}, errors.New("nil frame")
	}
	if !ValidID(f.RecordingID) {
		return Outcome{}, fmt.Errorf("invalid recordingId %q", clamp(f.RecordingID))
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	now := in.now()
	in.forgetFinished(now)
	if _, done := in.finished[f.RecordingID]; done {
		return Outcome{}, fmt.Errorf("recording %s already finished", f.RecordingID)
	}
	switch f.Op {
	case OpStart, OpEvent, OpSnapshot, OpNetwork, OpStop:
	default:
		return Outcome{}, fmt.Errorf("unknown recording op %q", clamp(f.Op))
	}
	s, err := in.session(f, now)
	if err != nil {
		return Outcome{}, err
	}
	s.touched = now
	switch f.Op {
	case OpStart:
		return Outcome{}, s.applyStart(f)
	case OpEvent:
		return s.addEvent(f.Event)
	case OpSnapshot:
		return s.addSnapshot(f)
	case OpNetwork:
		return s.addNetwork(f.Net)
	default: // OpStop
		s.stopReason = firstNonEmpty(strings.TrimSpace(f.Reason), "user")
		s.complete = true
		return Outcome{Stopped: in.detach(s, now)}, nil
	}
}

// session returns the in-memory session for f, adopting a spool left by an
// earlier process (the bridge restarted mid-recording) or opening a new one.
func (in *Ingest) session(f *Frame, now time.Time) (*session, error) {
	if s := in.active[f.RecordingID]; s != nil {
		return s, nil
	}
	if in.active == nil {
		in.active = map[string]*session{}
	}
	root, err := in.spoolRoot(f.Profile)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(root); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, spoolPrefix+f.RecordingID)
	s, err := adoptSpool(dir)
	if err != nil {
		return nil, err
	}
	if s == nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create recording spool: %w", err)
		}
		s = &session{id: f.RecordingID, dir: dir, seen: map[string]bool{}, sensitive: map[string]bool{}}
		if f.Op != OpStart {
			s.notes = append(s.notes, "no start frame was received")
			s.start = Frame{RecordingID: f.RecordingID, Profile: f.Profile, StartedAt: now.UnixMilli()}
			if err := s.saveStart(); err != nil {
				return nil, err
			}
		}
	}
	s.touched = now
	in.active[f.RecordingID] = s
	return s, nil
}

func (in *Ingest) detach(s *session, now time.Time) *Stopped {
	delete(in.active, s.id)
	if in.finished == nil {
		in.finished = map[string]time.Time{}
	}
	in.finished[s.id] = now
	return &Stopped{s: s}
}

func (in *Ingest) forgetFinished(now time.Time) {
	for id, at := range in.finished {
		if now.Sub(at) > finishedTTL {
			delete(in.finished, id)
		}
	}
}

// Reap detaches every recording idle for longer than IdleTimeout; they are
// finalised as incomplete (stopReason "idle").
func (in *Ingest) Reap() []*Stopped {
	in.mu.Lock()
	defer in.mu.Unlock()
	now := in.now()
	var out []*Stopped
	for _, s := range in.active {
		if now.Sub(s.touched) < in.idle() {
			continue
		}
		s.stopReason = StopIdle
		s.complete = false
		out = append(out, in.detach(s, now))
	}
	return out
}

// Recover adopts recording spools a previous process left in the given
// store directories: one idle past IdleTimeout is returned for finalising (stopReason
// "recovered"); a fresher one becomes active again, for the reaper or a
// continuing extension to finish.
func (in *Ingest) Recover(inboxes ...string) []*Stopped {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.active == nil {
		in.active = map[string]*session{}
	}
	now := in.now()
	var out []*Stopped
	for _, inbox := range inboxes {
		matches, _ := filepath.Glob(filepath.Join(inbox, spoolPrefix+"*"))
		for _, dir := range matches {
			s, err := adoptSpool(dir)
			if err != nil || s == nil || in.active[s.id] != nil {
				continue
			}
			if _, done := in.finished[s.id]; done {
				continue // being finalised right now
			}
			if now.Sub(s.touched) < in.idle() {
				in.active[s.id] = s
				continue
			}
			s.stopReason = StopRecovered
			s.notes = append(s.notes, "recovered after the bridge stopped mid-recording")
			out = append(out, in.detach(s, now))
		}
	}
	return out
}

// Active reports how many recordings are in progress.
func (in *Ingest) Active() int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.active)
}

// adoptSpool rebuilds a session from an existing spool directory, or
// returns nil when there is none.
func adoptSpool(dir string) (*session, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := strings.TrimPrefix(filepath.Base(dir), spoolPrefix)
	if !ValidID(id) {
		return nil, nil
	}
	s := &session{id: id, dir: dir, seen: map[string]bool{}, sensitive: map[string]bool{}, touched: info.ModTime()}
	if blob, err := os.ReadFile(filepath.Join(dir, startFile)); err == nil {
		_ = json.Unmarshal(blob, &s.start)
	}
	s.start.RecordingID = id
	events, _ := readEvents(filepath.Join(dir, EventsArtifact))
	for _, ev := range events {
		if !s.seen[ev.ID] {
			s.seen[ev.ID] = true
			s.events++
		}
		if SensitiveTarget(ev.Target) {
			s.sensitive[ev.ID] = true
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			s.bytes += fi.Size()
			if fi.ModTime().After(s.touched) {
				s.touched = fi.ModTime()
			}
		}
	}
	return s, nil
}

func clamp(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}
