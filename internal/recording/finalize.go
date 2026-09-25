package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/capture"
)

// ErrNoEvents is Finalize's answer for a recording that captured no events
// (a failed or abandoned start): its spool is dropped and nothing lands in
// the inbox.
var ErrNoEvents = errors.New("recording has no events: discarded")

// Finalize writes a stopped recording as a capture envelope and removes its
// spool. The spool is kept when the write fails, so a retry (or Recover in
// the next process) can still publish it. A recording with no events is
// not written at all (ErrNoEvents).
func (in *Ingest) Finalize(st *Stopped) (*capture.Result, error) {
	if st == nil || st.s == nil {
		return nil, fmt.Errorf("nil recording")
	}
	s := st.s
	env, err := s.envelope(in.now())
	if err != nil {
		return nil, err
	}
	if s.kept == 0 {
		_ = os.RemoveAll(s.dir)
		return nil, ErrNoEvents
	}
	res, err := in.writer().Write(env)
	if err != nil {
		return nil, err
	}
	_ = os.RemoveAll(s.dir)
	max := in.MaxRecordings
	if max <= 0 {
		max = MaxRecordingsPerInbox
	}
	res.Warnings = append(res.Warnings, pruneInbox(filepath.Dir(res.Path), res.Path, max)...)
	return res, nil
}

// envelope reads the spool back into a capture envelope: events deduped by
// id and sorted by seq, privacy re-applied, meta carrying the recording's
// provenance in Extra.
func (s *session) envelope(now time.Time) (*capture.Envelope, error) {
	events, err := readEvents(filepath.Join(s.dir, EventsArtifact))
	if err != nil && !os.IsNotExist(err) {
		s.notes = append(s.notes, fmt.Sprintf("events.jsonl partly unreadable: %v", err))
	}
	seen := map[string]bool{}
	kept := events[:0]
	for _, ev := range events {
		if seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		kept = append(kept, ev)
	}
	events = kept
	sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	if len(events) > MaxEvents {
		s.dropEvents += len(events) - MaxEvents
		events = events[:MaxEvents]
	}
	var buf bytes.Buffer
	sensitive := map[string]bool{}
	for i := range events {
		Sanitize(&events[i])
		if events[i].Target != nil && events[i].Target.Sensitive {
			sensitive[events[i].ID] = true
		}
		line, err := json.Marshal(events[i])
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	s.kept = len(events)
	artifacts := map[string]capture.Artifact{EventsArtifact: capture.Inline(buf.Bytes())}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read recording spool: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		isDOM := strings.HasPrefix(name, "dom-") && strings.HasSuffix(name, ".html")
		if e.IsDir() || !(isDOM || name == NetworkArtifact) || !capture.ValidArtifactName(name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			return nil, fmt.Errorf("read spooled %s: %w", name, err)
		}
		if isDOM {
			eventID := strings.TrimSuffix(strings.TrimPrefix(name, "dom-"), ".html")
			b = []byte(ScrubHTML(string(b), sensitive[eventID] || s.sensitive[eventID]))
		}
		artifacts[name] = capture.Inline(b)
	}

	meta := capture.Meta{
		URL:     SanitizeURL(s.start.URL),
		Title:   clipRunes(s.start.Title, MaxTitleRunes),
		Source:  SourceRecording,
		Profile: s.start.Profile,
	}
	if meta.URL == "" && len(events) > 0 {
		meta.URL = events[0].URL
	}
	if meta.URL == "" {
		meta.URL = "about:blank"
	}
	if meta.Title == "" {
		meta.Title = clipRunes(firstNonEmpty(s.start.Goal, "Recording "+s.id), MaxTitleRunes)
	}
	started := s.start.StartedAt
	if started <= 0 {
		started = now.UnixMilli()
	}
	meta.CapturedAt = msToRFC3339(started)
	extra := map[string]any{
		ExtraRecordingID: s.id,
		ExtraStartedAt:   meta.CapturedAt,
		ExtraEndedAt:     now.UTC().Format(time.RFC3339),
		ExtraStopReason:  s.stopReason,
		ExtraEventCount:  len(events),
		ExtraComplete:    s.complete,
	}
	if s.start.Goal != "" {
		extra[ExtraGoal] = clipRunes(s.start.Goal, MaxGoalRunes)
	}
	if s.start.TabID != 0 {
		extra[ExtraTabID] = s.start.TabID
	}
	if w := s.warnings(); len(w) > 0 {
		extra[ExtraWarnings] = w
	}
	meta.Extra = map[string]json.RawMessage{}
	for k, v := range extra {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		meta.Extra[k] = raw
	}
	return &capture.Envelope{Meta: meta, Artifacts: artifacts, Warnings: s.warnings()}, nil
}

func (s *session) warnings() []string {
	out := append([]string(nil), s.notes...)
	if s.dropEvents > 0 {
		out = append(out, fmt.Sprintf("dropped %d event(s) over the caps (%d events, %d bytes total)", s.dropEvents, MaxEvents, MaxTotalBytes))
	}
	if s.dropSnaps > 0 {
		out = append(out, fmt.Sprintf("dropped %d DOM snapshot(s) over the caps (%d bytes each, %d bytes total)", s.dropSnaps, MaxSnapshotBytes, MaxTotalBytes))
	}
	if s.dropNet > 0 {
		out = append(out, fmt.Sprintf("dropped %d network entr(ies) over the %d-byte recording cap", s.dropNet, MaxTotalBytes))
	}
	return out
}
