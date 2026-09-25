package recording

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/capture"
)

// Per-frame handling: each op appends to (or writes a file in) the
// session's spool directory. Called with Ingest.mu held.

func (s *session) applyStart(f *Frame) error {
	keepStarted := s.start.StartedAt
	s.start = Frame{
		RecordingID: s.id,
		TabID:       f.TabID,
		URL:         strings.TrimSpace(f.URL),
		Title:       f.Title,
		Goal:        f.Goal,
		StartedAt:   f.StartedAt,
		Profile:     strings.TrimSpace(f.Profile),
	}
	if s.start.StartedAt <= 0 {
		s.start.StartedAt = keepStarted
	}
	return s.saveStart()
}

func (s *session) saveStart() error {
	blob, err := json.Marshal(s.start)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, startFile), blob, 0o600)
}

func (s *session) addEvent(ev *Event) (Outcome, error) {
	if ev == nil {
		return Outcome{}, errors.New("event frame has no event")
	}
	if !ValidID(ev.ID) {
		return Outcome{}, fmt.Errorf("invalid event id %q", clamp(ev.ID))
	}
	if s.seen[ev.ID] {
		return Outcome{Dropped: "duplicate event " + ev.ID}, nil
	}
	if s.events >= MaxEvents {
		s.dropEvents++
		return Outcome{Dropped: fmt.Sprintf("event cap (%d) reached", MaxEvents)}, nil
	}
	Sanitize(ev)
	line, err := json.Marshal(ev)
	if err != nil {
		return Outcome{}, err
	}
	if len(line) > MaxEventLineBytes {
		s.dropEvents++
		return Outcome{Dropped: "event too large"}, nil
	}
	if s.bytes+int64(len(line))+1 > MaxTotalBytes {
		s.dropEvents++
		return Outcome{Dropped: "recording size cap reached"}, nil
	}
	if err := appendLine(filepath.Join(s.dir, EventsArtifact), line); err != nil {
		return Outcome{}, err
	}
	s.seen[ev.ID] = true
	s.events++
	s.bytes += int64(len(line)) + 1
	return Outcome{}, nil
}

func (s *session) addSnapshot(f *Frame) (Outcome, error) {
	if !ValidID(f.EventID) {
		return Outcome{}, fmt.Errorf("invalid snapshot eventId %q", clamp(f.EventID))
	}
	name := domName(f.EventID)
	if f.Name != "" && f.Name != name {
		return Outcome{}, fmt.Errorf("snapshot name %q does not match %q", clamp(f.Name), name)
	}
	if !capture.ValidArtifactName(name) {
		return Outcome{}, fmt.Errorf("invalid snapshot name %q", clamp(name))
	}
	if len(f.Data) > MaxSnapshotBytes {
		s.dropSnaps++
		return Outcome{Dropped: fmt.Sprintf("snapshot over %d bytes", MaxSnapshotBytes)}, nil
	}
	data := ScrubHTML(f.Data)
	path := filepath.Join(s.dir, name)
	var prev int64
	if fi, err := os.Stat(path); err == nil {
		prev = fi.Size()
	}
	if s.bytes-prev+int64(len(data)) > MaxTotalBytes {
		s.dropSnaps++
		return Outcome{Dropped: "recording size cap reached"}, nil
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		return Outcome{}, fmt.Errorf("spool snapshot: %w", err)
	}
	s.bytes += int64(len(data)) - prev
	return Outcome{}, nil
}

func (s *session) addNetwork(n *NetEntry) (Outcome, error) {
	if n == nil {
		return Outcome{}, errors.New("network frame has no net entry")
	}
	line, err := json.Marshal(n)
	if err != nil {
		return Outcome{}, err
	}
	if len(line) > MaxEventLineBytes || s.bytes+int64(len(line))+1 > MaxTotalBytes {
		s.dropNet++
		return Outcome{Dropped: "recording size cap reached"}, nil
	}
	if err := appendLine(filepath.Join(s.dir, NetworkArtifact), line); err != nil {
		return Outcome{}, err
	}
	s.bytes += int64(len(line)) + 1
	return Outcome{}, nil
}

func appendLine(path string, line []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("spool %s: %w", filepath.Base(path), err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return fmt.Errorf("spool %s: %w", filepath.Base(path), err)
	}
	return f.Close()
}
