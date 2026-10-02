package openaiapi

import (
	"slices"
	"sync"
	"time"
)

// Limits of the continuation store. A record is a few hundred bytes and no
// process stands behind it, so the caps are about not growing without bound, not
// about resources.
const (
	contTTL       = 10 * time.Minute
	contMaxTotal  = 1024
	contMaxPerKey = 64
)

// contRecord remembers where a leg that ended at a tool call left its runtime's
// session, so that the follow-up carrying the result can continue it. It holds
// ids and names, never the arguments of the call or anyone's words: the result
// itself comes with the follow-up.
type contRecord struct {
	// CallID is the id the client was given for the call.
	CallID string
	// KeyID and ProfileID are who the leg ran for: only that key may continue it.
	KeyID, ProfileID string
	// Model is the id of the model that ran the leg ("claude/sonnet"), Name the
	// function it called and ToolsHash the functions it was given: a session is
	// only continued by the same model with the same tools.
	Model, Name, ToolsHash string
	// Convo is the hash of the conversation the leg was given (convoHash): a
	// session is only continued by a follow-up whose conversation before the call is
	// the same. A hash, never the words.
	Convo string
	// Args is the hash of the arguments the call was given to the client with
	// (argsHash): the session holds the call as the model made it, so a follow-up
	// whose assistant message says other arguments is not continuing it. A hash,
	// never the arguments.
	Args string
	// Session is the runtime's session id.
	Session string
	Expires time.Time
}

// contStore keeps the records of the legs that ended at a call, in memory:
// a server that restarts loses them, and the follow-ups then start again from
// their transcripts.
type contStore struct {
	now func() time.Time

	// The limits, set to the constants above; a test sets smaller ones.
	maxTotal, maxPerKey int
	ttl                 time.Duration

	mu     sync.Mutex
	byCall map[string]*contRecord
	perKey map[string]int
	order  []*contRecord // oldest first
}

func newContStore(now func() time.Time) *contStore {
	return &contStore{now: now, maxTotal: contMaxTotal, maxPerKey: contMaxPerKey, ttl: contTTL,
		byCall: map[string]*contRecord{}, perKey: map[string]int{}}
}

// put adds a record. Expired ones are dropped first, then the oldest record of
// a key that is at its cap, then the oldest of all while the store is at its cap.
func (s *contStore) put(r contRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for len(s.order) > 0 && !now.Before(s.order[0].Expires) {
		s.removeLocked(s.order[0])
	}
	for s.perKey[r.KeyID] >= s.maxPerKey && s.dropOldestLocked(func(x *contRecord) bool { return x.KeyID == r.KeyID }) {
	}
	for len(s.order) >= s.maxTotal && s.dropOldestLocked(func(*contRecord) bool { return true }) {
	}
	r.Expires = now.Add(s.ttl)
	if old := s.byCall[r.CallID]; old != nil { // an id is never reused, but a record must never be counted twice
		s.removeLocked(old)
	}
	rec := &r
	s.byCall[r.CallID] = rec
	s.perKey[r.KeyID]++
	s.order = append(s.order, rec)
}

// take returns the record of a call and removes it, if it is there, has not
// expired and match accepts it. A record is used once: a client that retries its
// follow-up finds nothing and starts again from the transcript, so a session is
// never continued twice. A record that match refuses stays for its owner: asking
// with another key must not use it up.
func (s *contStore) take(callID string, match func(contRecord) bool) (contRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.byCall[callID]
	if r == nil {
		return contRecord{}, false
	}
	if !s.now().Before(r.Expires) {
		s.removeLocked(r)
		return contRecord{}, false
	}
	if !match(*r) {
		return contRecord{}, false
	}
	s.removeLocked(r)
	return *r, true
}

// size is the number of records held.
func (s *contStore) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.order)
}

// dropOldestLocked removes the oldest record that pick accepts and reports
// whether there was one.
func (s *contStore) dropOldestLocked(pick func(*contRecord) bool) bool {
	for _, r := range s.order {
		if pick(r) {
			s.removeLocked(r)
			return true
		}
	}
	return false
}

func (s *contStore) removeLocked(r *contRecord) {
	delete(s.byCall, r.CallID)
	if s.perKey[r.KeyID]--; s.perKey[r.KeyID] <= 0 {
		delete(s.perKey, r.KeyID)
	}
	for i, x := range s.order {
		if x == r {
			s.order = slices.Delete(s.order, i, i+1)
			break
		}
	}
}
