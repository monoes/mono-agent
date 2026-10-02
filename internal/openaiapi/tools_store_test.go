package openaiapi

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testClock is a clock a test moves.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func record(call, key string) contRecord {
	return contRecord{CallID: call, KeyID: key, ProfileID: "alice", Model: "claude/default", Name: "get_weather", Session: "sess-" + call, ToolsHash: "h"}
}

func matchAll(contRecord) bool { return true }

func TestContStorePutAndTake(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	s.put(record("call_1", "key_a"))
	r, ok := s.take("call_1", matchAll)
	if !ok || r.Session != "sess-call_1" || r.KeyID != "key_a" || r.Model != "claude/default" || r.Name != "get_weather" {
		t.Fatalf("take = %+v, %v", r, ok)
	}
	if _, ok := s.take("call_1", matchAll); ok {
		t.Error("a record is single-use: a retry of the same follow-up must replay, not resume the session twice")
	}
	if _, ok := s.take("call_unknown", matchAll); ok {
		t.Error("an unknown call id found a record")
	}
	if s.size() != 0 {
		t.Errorf("size = %d after the only record was taken", s.size())
	}
}

func TestContStoreRecordsExpire(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	s.put(record("call_1", "key_a"))
	clock.advance(contTTL - time.Second)
	if _, ok := s.take("call_1", func(contRecord) bool { return false }); ok {
		t.Fatal("a record the match refused was taken")
	}
	clock.advance(2 * time.Second) // past the ten minutes
	if _, ok := s.take("call_1", matchAll); ok {
		t.Error("a record outlived its TTL")
	}
	if s.size() != 0 {
		t.Errorf("an expired record is dropped when it is looked up: size = %d", s.size())
	}
}

func TestContStoreDropsExpiredRecordsWhenOthersArePut(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	for i := range 5 {
		s.put(record(fmt.Sprintf("call_%d", i), "key_a"))
	}
	clock.advance(contTTL + time.Second)
	s.put(record("call_new", "key_b"))
	if s.size() != 1 {
		t.Errorf("size = %d: the expired records of nobody who comes back must not pile up", s.size())
	}
}

// A record given back after a resume that failed before the model ran keeps the expiry it
// had: the retry is no reason for a session to live longer than the leg that left it was
// told it would, and the sweep that drops the expired records from the front must still find
// it where its expiry puts it.
func TestContStoreAGivenBackRecordKeepsItsExpiry(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	s.put(record("call_1", "key_a"))
	clock.advance(9 * time.Minute)
	r, ok := s.take("call_1", matchAll)
	if !ok {
		t.Fatal("no record")
	}
	s.giveBack(r)
	clock.advance(time.Minute / 2) // 9.5 minutes in: still its own ten
	if _, ok := s.take("call_1", func(contRecord) bool { return false }); ok || s.size() != 1 {
		t.Fatalf("a record given back before its expiry is held: size %d", s.size())
	}
	clock.advance(time.Minute) // 10.5 minutes after the leg ended: the given back record has had its ten
	if _, ok := s.take("call_1", matchAll); ok {
		t.Error("a given back record outlived the ten minutes of the leg that left it")
	}
}

func TestContStoreAGivenBackRecordIsSweptWhenItExpires(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	s.put(record("old", "key_a")) // expires at ten minutes
	clock.advance(5 * time.Minute)
	r, _ := s.take("old", matchAll)
	s.put(record("newer", "key_b")) // expires at fifteen
	s.giveBack(r)                   // goes before it, where its own ten minutes put it
	clock.advance(6 * time.Minute)  // eleven: "old" is over, "newer" is not
	s.put(record("another", "key_c"))
	if s.size() != 2 {
		t.Errorf("size = %d: the expired record behind a newer one must be swept with the others", s.size())
	}
	if _, ok := s.take("newer", matchAll); !ok {
		t.Error("the sweep took a record that had not expired")
	}
}

func TestContStoreARecordThatExpiredIsNotGivenBack(t *testing.T) {
	clock := newTestClock()
	s := newContStore(clock.now)
	s.put(record("call_1", "key_a"))
	r, _ := s.take("call_1", matchAll)
	clock.advance(contTTL + time.Second) // the resume that failed took longer than the record had
	s.giveBack(r)
	if s.size() != 0 {
		t.Errorf("an expired record was put back: size %d", s.size())
	}
}

func TestContStoreAGivenBackRecordRespectsTheCaps(t *testing.T) {
	s := newContStore(newTestClock().now)
	s.maxPerKey = 2
	s.put(record("a1", "key_a"))
	r, _ := s.take("a1", matchAll)
	s.put(record("a2", "key_a"))
	s.put(record("a3", "key_a"))
	s.giveBack(r) // key_a is at its cap: the oldest goes, the count never passes it
	if got := s.size(); got != 2 {
		t.Errorf("size = %d, want the cap of 2", got)
	}
}

// A match that refuses leaves the record for whoever it belongs to: asking with
// another key must not use it up.
func TestContStoreARefusedMatchLeavesTheRecord(t *testing.T) {
	s := newContStore(newTestClock().now)
	s.put(record("call_1", "key_a"))
	if _, ok := s.take("call_1", func(r contRecord) bool { return r.KeyID == "key_b" }); ok {
		t.Fatal("a record was handed to another key")
	}
	if _, ok := s.take("call_1", func(r contRecord) bool { return r.KeyID == "key_a" }); !ok {
		t.Error("the owner lost its record to a stranger's attempt")
	}
}

func TestContStoreCapsPerKeyEvictTheOldestOfThatKey(t *testing.T) {
	s := newContStore(newTestClock().now)
	s.maxPerKey = 3
	s.put(record("a1", "key_a"))
	s.put(record("b1", "key_b"))
	s.put(record("a2", "key_a"))
	s.put(record("a3", "key_a"))
	s.put(record("a4", "key_a")) // key_a is at its cap: a1 goes
	if _, ok := s.take("a1", matchAll); ok {
		t.Error("the oldest record of a key at its cap must be evicted")
	}
	for _, id := range []string{"a2", "a3", "a4", "b1"} {
		if _, ok := s.take(id, matchAll); !ok {
			t.Errorf("record %s was evicted, but it is not the oldest of a key at its cap", id)
		}
	}
}

func TestContStoreTotalCapEvictsTheOldest(t *testing.T) {
	s := newContStore(newTestClock().now)
	s.maxTotal = 4
	for i := range 6 {
		s.put(record(fmt.Sprintf("c%d", i), fmt.Sprintf("key_%d", i)))
	}
	if s.size() != 4 {
		t.Fatalf("size = %d, want the cap of 4", s.size())
	}
	for _, id := range []string{"c0", "c1"} {
		if _, ok := s.take(id, matchAll); ok {
			t.Errorf("record %s is among the oldest and must have been evicted", id)
		}
	}
	for _, id := range []string{"c2", "c3", "c4", "c5"} {
		if _, ok := s.take(id, matchAll); !ok {
			t.Errorf("record %s was evicted", id)
		}
	}
}

func TestContStoreDefaultCaps(t *testing.T) {
	s := newContStore(newTestClock().now)
	if s.maxTotal != 1024 || s.maxPerKey != 64 || s.ttl != 10*time.Minute {
		t.Errorf("caps: %d in all, %d per key, ttl %v", s.maxTotal, s.maxPerKey, s.ttl)
	}
}

func TestContStoreConcurrentTakesHaveOneWinner(t *testing.T) {
	s := newContStore(newTestClock().now)
	for round := range 50 {
		id := fmt.Sprintf("call_%d", round)
		s.put(record(id, "key_a"))
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, ok := s.take(id, matchAll); ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("round %d: %d takers got the record", round, wins.Load())
		}
	}
}

func TestContStoreConcurrentUseKeepsItsBooks(t *testing.T) {
	s := newContStore(newTestClock().now)
	s.maxPerKey, s.maxTotal = 8, 40
	var wg sync.WaitGroup
	for g := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				id := fmt.Sprintf("g%d-%d", g, i)
				s.put(record(id, fmt.Sprintf("key_%d", g%3)))
				if i%3 == 0 {
					s.take(id, matchAll)
				}
			}
		}()
	}
	wg.Wait()
	if n := s.size(); n > 40 || n != len(s.byCall) {
		t.Errorf("size %d, index %d, cap 40", n, len(s.byCall))
	}
	total := 0
	for _, n := range s.perKey {
		if n < 0 || n > 8 {
			t.Errorf("a key holds %d records, the cap is 8", n)
		}
		total += n
	}
	if total != s.size() {
		t.Errorf("the per-key counts add up to %d, there are %d records", total, s.size())
	}
}
