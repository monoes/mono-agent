package orgbridge

import (
	"context"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// StreamFunc follows one org's bus events, calling onLine per NDJSON line,
// until ctx ends or the stream breaks. since is a resume cursor (event id
// or ISO timestamp).
type StreamFunc func(ctx context.Context, root, org, since string, onLine func([]byte)) error

// MonomindStream follows events with `monomind org events --follow`.
func MonomindStream(ctx context.Context, root, org, since string, onLine func([]byte)) error {
	return monomind.OrgEvents(ctx, root, org, monomind.OrgEventsOptions{Follow: true, Since: since}, onLine)
}

// Handler receives events for one subscription.
type Handler func(Event)

// Mux shares one event tail per (profile root, org) among every
// subscriber — N trigger.org workflows and the decision service watching
// one org cost one `org events --follow` process, not N (C-27). A tail
// starts with the first subscriber, restarts with backoff when the stream
// breaks (resuming after the last event it saw), and stops with the last
// unsubscribe.
type Mux struct {
	stream StreamFunc
	now    func() time.Time

	mu    sync.Mutex
	tails map[tailKey]*tail
}

type tailKey struct{ root, org string }

type tail struct {
	cancel context.CancelFunc
	subs   map[int]Handler
	next   int
}

// NewMux returns a Mux using stream (MonomindStream when nil).
func NewMux(stream StreamFunc) *Mux {
	if stream == nil {
		stream = MonomindStream
	}
	return &Mux{stream: stream, now: time.Now, tails: map[tailKey]*tail{}}
}

// Subscribe delivers every new event of org (under root) to h until the
// returned function is called. Events that happened before the tail
// started are not replayed.
func (m *Mux) Subscribe(root, org string, h Handler) (unsubscribe func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tailKey{root, org}
	t := m.tails[k]
	if t == nil {
		ctx, cancel := context.WithCancel(context.Background())
		t = &tail{cancel: cancel, subs: map[int]Handler{}}
		m.tails[k] = t
		// Sample the resume cursor here, not inside the goroutine: the
		// caller's very next action can make an event happen (monomind
		// emits the confirming bus event right after its 202), and a
		// cursor taken after that would skip it as older than the tail.
		go m.run(ctx, k, t, m.now().UTC().Format(time.RFC3339Nano))
	}
	id := t.next
	t.next++
	t.subs[id] = h
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			delete(t.subs, id)
			if len(t.subs) == 0 && m.tails[k] == t {
				t.cancel()
				delete(m.tails, k)
			}
		})
	}
}

// Subscribers reports how many subscriptions share org's tail (tests).
func (m *Mux) Subscribers(root, org string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.tails[tailKey{root, org}]; t != nil {
		return len(t.subs)
	}
	return 0
}

func (m *Mux) run(ctx context.Context, k tailKey, t *tail, since string) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		started := m.now()
		_ = m.stream(ctx, k.root, k.org, since, func(line []byte) {
			ev, err := ParseEvent(line)
			if err != nil {
				return
			}
			if ev.ID != "" {
				since = ev.ID
			}
			m.dispatch(t, ev)
		})
		if ctx.Err() != nil {
			return
		}
		if m.now().Sub(started) > time.Minute {
			backoff = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (m *Mux) dispatch(t *tail, ev Event) {
	m.mu.Lock()
	handlers := make([]Handler, 0, len(t.subs))
	for _, h := range t.subs {
		handlers = append(handlers, h)
	}
	m.mu.Unlock()
	for _, h := range handlers {
		h(ev)
	}
}

// Deduper recognizes the second bus copy of a cross-org message: each one
// lands on both the sender's and the receiver's bus (C-43). A subscriber
// that watches several orgs owns one Deduper; one watching a single org
// needs none.
type Deduper struct {
	now  func() time.Time
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewDeduper returns an empty Deduper.
func NewDeduper() *Deduper { return &Deduper{now: time.Now, seen: map[string]time.Time{}} }

// FirstSighting reports whether ev is new within 5 seconds (keyed by the M3
// messageId, else by sender, recipient, subject, and body). Non-xorg events
// always pass.
func (d *Deduper) FirstSighting(ev Event) bool {
	if ev.Type != "xorg" {
		return true
	}
	const window = 5 * time.Second
	now := d.now()
	key := ev.dedupeKey()
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, at := range d.seen {
		if now.Sub(at) > window {
			delete(d.seen, k)
		}
	}
	if at, ok := d.seen[key]; ok && now.Sub(at) <= window {
		return false
	}
	d.seen[key] = now
	return true
}
