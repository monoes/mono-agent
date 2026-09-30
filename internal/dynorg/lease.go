package dynorg

import (
	"context"
	"slices"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// lease is a first-come, first-served lock that can be waited for with a
// context: the write lease (one worker edits the chat folder at a time) and
// the browser lease (one worker drives the browser at a time).
type lease struct{ ch chan struct{} }

func newLease() *lease { return &lease{ch: make(chan struct{}, 1)} }

// tryAcquire takes the lease if it is free.
func (l *lease) tryAcquire() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// acquire waits for the lease or ctx.
func (l *lease) acquire(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *lease) release() { <-l.ch }

// heldLease is a lease a worker holds, with the name the stage reads.
type heldLease struct {
	l    *lease
	name string
}

// leaseNames lists the names of the leases in hs.
func leaseNames(hs []heldLease) []string {
	if len(hs) == 0 {
		return nil
	}
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.name
	}
	return out
}

// holdLease records that w holds l (named name) until the returned func
// releases it, and journals that at once (agent.status with its status
// unchanged). Every agent.status reports the leases its worker holds, so
// the org stage reads who holds the pen and the browser from the journal
// instead of re-deriving the rules (#228). The release frees l only if w
// still holds it: a worker waiting on the user has already let go of its
// leases (#256), and freeing one it doesn't hold would free another
// worker's.
func (c *Conductor) holdLease(w *worker, l *lease, name string) (release func()) {
	c.mu.Lock()
	w.leases = append(w.leases, heldLease{l: l, name: name})
	// Say so now: a worker that holds a lease may still wait for another
	// lease or a free slot before its status changes.
	c.reportLocked(w)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		i := slices.IndexFunc(w.leases, func(h heldLease) bool { return h.l == l })
		if i >= 0 {
			w.leases = slices.Delete(w.leases, i, i+1)
		}
		c.mu.Unlock()
		if i >= 0 {
			l.release()
		}
	}
}

// reportLocked journals w's current status again, with the leases it
// holds now.
func (c *Conductor) reportLocked(w *worker) {
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{AgentID: w.id, From: w.status, To: w.status, Leases: leaseNames(w.leases)})
}

// LeadAgentID names the lead in the lease report the stage reads.
const LeadAgentID = "lead"

// reportLeadLocked journals whether the lead holds the write lease: an
// agent.status for "lead" (working, as the lead is while it edits) with
// its leases, so the stage shows the lead holding the pen too.
func (c *Conductor) reportLeadLocked() {
	var leases []string
	if c.leadHolds {
		leases = []string{"write"}
	}
	c.cfg.Emit.Emit(chatevents.EventAgentStatus, chatevents.AgentStatusPayload{
		AgentID: LeadAgentID, From: chatevents.AgentWorking, To: chatevents.AgentWorking, Leases: leases,
	})
}
