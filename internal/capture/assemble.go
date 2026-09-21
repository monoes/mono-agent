package capture

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// The wire shape an Assembler consumes is the `data` object of a
// page_capture response (see internal/extension/capture.go for the command
// half). Two message shapes share one id:
//
//	chunk:  {"chunk": {"index": 0, "total": 4, "of": "page.mhtml"}, "bytes": "<base64 slice>"}
//	final:  {"meta": {…}, "warnings": […], "final": true,
//	         "artifacts": [{"name": …, "encoding": "base64", "bytes": …},
//	                       {"name": …, "chunked": true, "chunks": 4}]}
//
// Every chunk for an artifact precedes the single closing message, which is
// always last. An artifact that was chunked carries no inline bytes; it
// declares `chunks`, the count the sender sent, which is what turns a lost
// frame into an error here instead of a truncated file on disk. An
// unchunked capture is one closing message with every artifact inline.
//
// A message is treated as the closing one when it carries no `chunk` key;
// the sender's `final: true` is honoured but not required, so a producer
// that omits it still works.

// Assembler reassembles chunked page_capture responses, keyed by command
// id, so several captures (a requested one plus a queue flushed on
// reconnect) can be in flight at once. It is safe for concurrent use.
//
// Memory is bounded two ways: no single capture may exceed MaxBytes across
// all its artifacts, and a capture whose remaining messages never arrive is
// dropped by Expire rather than held forever.
type Assembler struct {
	mu       sync.Mutex
	opts     Options
	inflight map[string]*inflight
	// expired remembers ids Expire dropped, so the tail of a timed-out
	// capture is rejected outright instead of being written to the inbox
	// with the missing artifact silently absent.
	expired map[string]time.Time
}

// Options configures an Assembler. The zero value is usable: every field
// falls back to its documented default.
type Options struct {
	// MaxBytes caps the decoded size of one capture's artifacts. Since
	// chunked artifacts are spooled to disk, this bounds disk rather than
	// memory; the default matches what the extension is willing to
	// produce, so a legal capture never fails on this side.
	MaxBytes int64
	// ChunkTimeout is how long a partially-received capture may sit idle
	// before Expire drops it.
	ChunkTimeout time.Duration
	// SpoolDir is the directory chunked artifacts are staged under while
	// they arrive. Empty means DefaultInbox, which keeps the spool on the
	// same filesystem as the envelope it is about to become — and off
	// /tmp, which on this machine is a tmpfs shared with everything else.
	SpoolDir string
}

// Defaults for Options.
const (
	// DefaultMaxBytes is the extension's own ceiling: four artifacts at
	// its 25MB-per-artifact cap, so nothing it will send can exceed it.
	DefaultMaxBytes     = 100 << 20
	DefaultChunkTimeout = 2 * time.Minute
	// maxChunks bounds the per-artifact chunk count a sender may declare,
	// so a bogus "total" cannot make the assembler allocate a huge map
	// before any bytes have arrived.
	maxChunks = 8192
	// expiredTTL is how long a dropped id is remembered by Expire.
	expiredTTL = 10 * time.Minute
)

// ErrExpired is returned for a message belonging to a capture that Expire
// already dropped.
var ErrExpired = errors.New("capture expired: chunks stopped arriving")

func (o Options) maxBytes() int64 {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return DefaultMaxBytes
}

func (o Options) chunkTimeout() time.Duration {
	if o.ChunkTimeout > 0 {
		return o.ChunkTimeout
	}
	return DefaultChunkTimeout
}

func (o Options) spoolRoot() string {
	if strings.TrimSpace(o.SpoolDir) != "" {
		return o.SpoolDir
	}
	return DefaultInbox()
}

// NewAssembler creates an Assembler with the given options.
func NewAssembler(opts Options) *Assembler {
	return &Assembler{
		opts:     opts,
		inflight: make(map[string]*inflight),
		expired:  make(map[string]time.Time),
	}
}

// inflight is one partially-received capture.
type inflight struct {
	touched time.Time
	chunks  map[string]*chunkSet
	total   int64  // decoded bytes accumulated so far
	dir     string // spool directory, created on the first chunk
}

// Accept feeds one page_capture `data` object for command id. It returns a
// finished Envelope when the capture is complete, (nil, nil) when more
// messages are expected, and an error for a capture that can no longer be
// completed — in which case the id's state has already been dropped and the
// caller should fail the capture.
func (a *Assembler) Accept(id string, data map[string]any) (*Envelope, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if _, gone := a.expired[id]; gone {
		return nil, ErrExpired
	}
	if data == nil {
		a.drop(id)
		return nil, errors.New("page_capture response carried no data object")
	}
	if _, chunked := data["chunk"]; chunked {
		if final, _ := data["final"].(bool); final {
			a.drop(id)
			return nil, errors.New("message claims to be both a chunk and the closing envelope")
		}
		if err := a.acceptChunk(id, data); err != nil {
			a.drop(id)
			return nil, err
		}
		return nil, nil
	}
	env, err := a.acceptFinal(id, data)
	if err != nil {
		a.drop(id) // takes the spooled chunks with it
		return nil, err
	}
	// The envelope now owns the spool directory; forget the capture
	// without deleting what it is about to be written from.
	delete(a.inflight, id)
	return env, nil
}

// Pending reports whether id has partially-received state. Test/diagnostic
// helper; the dispatch path does not need it.
func (a *Assembler) Pending(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.inflight[id]
	return ok
}

// Drop forgets any partial state for id, without marking it expired. The
// requested-capture path defers this so a command that timed out or errored
// leaves nothing behind.
func (a *Assembler) Drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.drop(id)
}

// drop forgets a capture and deletes anything it spooled. Must be called
// with a.mu held.
func (a *Assembler) drop(id string) {
	if st := a.inflight[id]; st != nil && st.dir != "" {
		_ = os.RemoveAll(st.dir)
	}
	delete(a.inflight, id)
}

// Expire drops every capture that has sat idle longer than the configured
// chunk timeout and returns their ids, for the caller to log. Dropped ids
// are remembered for a while so a late message is rejected with ErrExpired
// instead of starting a fresh, half-empty capture under the same id.
func (a *Assembler) Expire(now time.Time) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	var dropped []string
	for id, st := range a.inflight {
		if now.Sub(st.touched) > a.opts.chunkTimeout() {
			// drop, not delete: an abandoned capture's spooled chunks have
			// to go with it, or a stalled extension leaks them until the
			// next sweep.
			a.drop(id)
			a.expired[id] = now
			dropped = append(dropped, id)
		}
	}
	for id, at := range a.expired {
		if now.Sub(at) > expiredTTL {
			delete(a.expired, id)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// budget charges n decoded bytes against the capture's size cap.
func (a *Assembler) budget(st *inflight, n int64) error {
	if st.total+n > a.opts.maxBytes() {
		return fmt.Errorf("capture exceeds the %d byte limit", a.opts.maxBytes())
	}
	return nil
}

// acceptFinal turns the closing message (plus anything already assembled
// from chunks) into an Envelope. Called with a.mu held.
func (a *Assembler) acceptFinal(id string, data map[string]any) (*Envelope, error) {
	st := a.inflight[id]
	if st == nil {
		st = &inflight{chunks: map[string]*chunkSet{}}
	}

	env := &Envelope{Artifacts: make(map[string]Artifact), spoolDir: st.dir}
	if rawMeta, ok := data["meta"]; ok {
		blob, err := json.Marshal(rawMeta)
		if err != nil {
			return nil, fmt.Errorf("re-encode meta: %w", err)
		}
		if err := json.Unmarshal(blob, &env.Meta); err != nil {
			return nil, fmt.Errorf("decode meta: %w", err)
		}
	}
	if warnings, ok := data["warnings"].([]any); ok {
		for _, w := range warnings {
			if s, ok := w.(string); ok {
				env.Warnings = append(env.Warnings, s)
			}
		}
	}

	listed, err := a.inlineArtifacts(st, data)
	if err != nil {
		return nil, err
	}
	for name, payload := range listed {
		env.Artifacts[name] = payload
	}

	// Anything assembled from chunks that the final message did not carry
	// inline. Both "listed with empty bytes" and "not listed at all" land
	// here, so either sender behaviour works.
	for name, set := range st.chunks {
		if _, done := env.Artifacts[name]; done {
			continue
		}
		if !set.complete() {
			return nil, fmt.Errorf("artifact %s is incomplete: got %d of %d chunks", name, len(set.parts), set.total)
		}
		env.Artifacts[name] = spooledArtifact(set.paths(), set.size)
	}
	return env, nil
}

// inlineArtifacts decodes the final message's artifacts array.
func (a *Assembler) inlineArtifacts(st *inflight, data map[string]any) (map[string]Artifact, error) {
	rawList, _ := data["artifacts"].([]any)
	out := make(map[string]Artifact, len(rawList))
	for _, item := range rawList {
		entry, _ := item.(map[string]any)
		if entry == nil {
			return nil, errors.New("artifacts: entry is not an object")
		}
		name, _ := entry["name"].(string)
		if !ValidArtifactName(name) {
			return nil, fmt.Errorf("artifacts: invalid artifact name %q", name)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("artifacts: %s listed twice", name)
		}
		encoded, _ := entry["bytes"].(string)
		set, haveChunks := st.chunks[name]
		chunkedFlag, _ := entry["chunked"].(bool)
		declared, declaredOK := toInt(entry["chunks"])

		if chunkedFlag || declaredOK || encoded == "" {
			// A chunked entry is a receipt, not a payload: check it against
			// what actually arrived, so a dropped frame fails the capture
			// here rather than landing a truncated artifact on disk.
			if chunkedFlag || declaredOK {
				if err := verifyChunked(name, encoded, set, haveChunks, declared, declaredOK); err != nil {
					return nil, err
				}
			}
			continue // the bytes come from the chunk buffer
		}
		if haveChunks && len(set.parts) > 0 {
			return nil, fmt.Errorf("artifact %s arrived both chunked and inline", name)
		}
		encoding, _ := entry["encoding"].(string)
		payload, err := decodePayload(encoding, encoded)
		if err != nil {
			return nil, fmt.Errorf("artifact %s: %w", name, err)
		}
		if err := a.budget(st, int64(len(payload))); err != nil {
			return nil, fmt.Errorf("artifact %s: %w", name, err)
		}
		st.total += int64(len(payload))
		out[name] = Inline(payload)
	}
	return out, nil
}

// decodePayload decodes one inline artifact body. Anything that is not
// explicitly a text encoding is treated as base64, which is what the
// contract specifies and what every binary artifact must use.
func decodePayload(encoding, body string) ([]byte, error) {
	switch encoding {
	case "utf8", "utf-8", "text", "plain":
		return []byte(body), nil
	default:
		return decodeBase64(body)
	}
}

// decodeBase64 accepts both standard and URL-safe base64, with or without
// padding — JS producers differ, and a capture is too expensive to lose to
// an alphabet mismatch.
func decodeBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("bytes are not valid base64")
}

// toInt reads a JSON number (float64 from encoding/json, or the int forms a
// Go caller might hand over in a test) as an int.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n == float64(int(n))
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
