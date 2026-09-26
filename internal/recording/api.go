package recording

import (
	"bufio"
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

// ErrNotImplemented marks skeleton functions not yet filled in.
var ErrNotImplemented = errors.New("recording: not implemented")

// ErrNotFound is returned by Find for an id that names no recording.
var ErrNotFound = errors.New("recording not found")

// Meta Extra keys a recording envelope carries (see finalize.go).
const (
	ExtraRecordingID = "recordingId"
	ExtraGoal        = "goal"
	ExtraTabID       = "tabId"
	ExtraStartedAt   = "startedAt"
	ExtraEndedAt     = "endedAt"
	ExtraStopReason  = "stopReason"
	ExtraEventCount  = "eventCount"
	ExtraComplete    = "complete"
	ExtraWarnings    = "warnings"
	ExtraAutomation  = "automation"
)

// List returns recordings in the active scope's stores, newest first.
// See StoreDirs for which stores those are. An incomplete recording with no
// events (a failed start, from before such recordings were discarded) is
// left out; Find still resolves it, so it can be deleted.
func List() ([]Summary, error) {
	all, err := listAll()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, s := range all {
		if !s.Complete && s.Events == 0 {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// listAll is List without the empty-recording filter.
func listAll() ([]Summary, error) {
	var out []Summary
	migrateLegacy()
	for _, inbox := range StoreDirs() {
		entries, err := capture.List(inbox)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Meta.Source != SourceRecording {
				continue
			}
			out = append(out, summaryOf(e.Path, &e.Meta))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// Find resolves a recording id (envelope dir name, or unique prefix) to
// its directory. The extension's own recording id (meta recordingId) is
// accepted too, so the side panel can name a recording it just stopped.
func Find(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !ValidID(id) {
		// Same answer as an id that names nothing: the id may come from
		// a browser, and a malformed one says nothing about the disk.
		return "", ErrNotFound
	}
	all, err := listAll()
	if err != nil {
		return "", err
	}
	for _, s := range all {
		if s.ID == id {
			return s.Dir, nil
		}
	}
	var byRecID, byPrefix []Summary
	for _, s := range all {
		if meta, err := capture.ReadMeta(s.Dir); err == nil && extraString(meta, ExtraRecordingID) == id {
			byRecID = append(byRecID, s)
		}
		if strings.HasPrefix(s.ID, id) {
			byPrefix = append(byPrefix, s)
		}
	}
	for _, set := range [][]Summary{byRecID, byPrefix} {
		switch len(set) {
		case 0:
			continue
		case 1:
			return set[0].Dir, nil
		default:
			ids := make([]string, 0, len(set))
			for _, s := range set {
				ids = append(ids, s.ID)
			}
			return "", fmt.Errorf("recording id %q is ambiguous: %s", id, strings.Join(ids, ", "))
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Load reads a recording envelope: summary, events (in seq order).
func Load(dir string) (*Summary, []Event, error) {
	meta, err := readRecordingMeta(dir)
	if err != nil {
		return nil, nil, err
	}
	sum := summaryOf(dir, meta)
	events, err := readEvents(filepath.Join(dir, EventsArtifact))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	if events == nil {
		events = []Event{}
	}
	return &sum, events, nil
}

// Artifacts lists a recording envelope's files (meta.json excluded).
func Artifacts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == capture.MetaFile {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// DOMSnippet returns dom-<eventId>.html for an event ("" when absent).
func DOMSnippet(dir, eventID string) (string, error) {
	if !ValidID(eventID) {
		return "", fmt.Errorf("invalid event id %q", eventID)
	}
	b, err := os.ReadFile(filepath.Join(dir, domName(eventID)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Delete removes a recording envelope. Only a directory whose meta.json
// says it is a recording is removed; the store has no trash to move it to.
func Delete(id string) error {
	dir, err := Find(id)
	if err != nil {
		return err
	}
	if _, err := readRecordingMeta(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// SetAutomation records which automation package a recording was saved
// into (meta Extra "automation"), so `record list` can show the link. An
// empty id clears it. meta.json is replaced atomically.
func SetAutomation(dir, automationID string) error {
	meta, err := readRecordingMeta(dir)
	if err != nil {
		return err
	}
	if meta.Extra == nil {
		meta.Extra = map[string]json.RawMessage{}
	}
	if automationID == "" {
		delete(meta.Extra, ExtraAutomation)
	} else {
		raw, _ := json.Marshal(automationID)
		meta.Extra[ExtraAutomation] = raw
	}
	blob, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".meta-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(blob, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, capture.MetaFile))
}

// readRecordingMeta reads meta.json and insists it is a recording's.
func readRecordingMeta(dir string) (*capture.Meta, error) {
	meta, err := capture.ReadMeta(dir)
	if err != nil {
		return nil, err
	}
	if meta.Source != SourceRecording {
		return nil, fmt.Errorf("%s is not a recording (source %q)", filepath.Base(dir), meta.Source)
	}
	return meta, nil
}

func summaryOf(dir string, meta *capture.Meta) Summary {
	started := extraString(meta, ExtraStartedAt)
	if started == "" {
		started = meta.CapturedAt
	}
	return Summary{
		ID:         filepath.Base(dir),
		Dir:        dir,
		Title:      meta.Title,
		URL:        meta.URL,
		Goal:       extraString(meta, ExtraGoal),
		StartedAt:  started,
		Events:     extraInt(meta, ExtraEventCount),
		Complete:   extraBool(meta, ExtraComplete),
		StopReason: extraString(meta, ExtraStopReason),
		Automation: extraString(meta, ExtraAutomation),
		Profile:    firstNonEmpty(meta.Profile, profileOfStore(filepath.Dir(dir))),
	}
}

// readEvents parses an events.jsonl file, skipping lines that do not
// decode (a recovered spool may end in a torn line).
func readEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), MaxEventLineBytes+1)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line), &ev) == nil && ev.ID != "" {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

func extraString(m *capture.Meta, key string) string {
	var s string
	if raw, ok := m.Extra[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func extraInt(m *capture.Meta, key string) int {
	var n float64
	if raw, ok := m.Extra[key]; ok && json.Unmarshal(raw, &n) == nil {
		return int(n)
	}
	return 0
}

func extraBool(m *capture.Meta, key string) bool {
	var b bool
	if raw, ok := m.Extra[key]; ok && json.Unmarshal(raw, &b) == nil {
		return b
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func domName(eventID string) string { return "dom-" + eventID + ".html" }

// msToRFC3339 renders a unix-ms timestamp; zero is "".
func msToRFC3339(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}
