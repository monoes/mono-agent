package dynorg

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// Spawn depth 2 (#230): the lead may let a worker spawn sub-workers
// (org_spawn's allow_spawn). That worker gets org_spawn, org_wait and
// org_message for its own sub-workers, which never get org_spawn: the
// tree is at most lead → worker → sub-worker. The turn's limits (workers,
// concurrency, budget) count the whole tree, a sub-worker's access never
// exceeds its parent's, and a sub-worker's runs end with its parent's run.

// parentID is the id of the worker that spawned w, or "" for the lead.
func parentID(w *worker) string {
	if w.parent == nil {
		return ""
	}
	return w.parent.id
}

// orLead names who sends a brief or follow-up: the parent worker, or the
// lead.
func orLead(from *worker) string {
	if from == nil {
		return "lead"
	}
	return from.id
}

// childRequest checks a worker's org_spawn before staffing: the depth cap,
// and a research parent's sub-workers are research too.
func childRequest(parent *worker, req SpawnRequest) (SpawnRequest, error) {
	if parent.parent != nil || !parent.allowSpawn {
		return req, fmt.Errorf("sub-workers can't start workers of their own")
	}
	if req.AllowSpawn {
		return req, fmt.Errorf("allow_spawn: a sub-worker can't start workers of its own (the org is at most two levels deep)")
	}
	if parent.staff.Access == ProfileResearch {
		if req.Access != "" && req.Access != ProfileResearch {
			return req, fmt.Errorf("access %q exceeds yours: a research worker's sub-workers are research workers", req.Access)
		}
		if req.NeedsWrite != nil && *req.NeedsWrite {
			return req, fmt.Errorf("needs_write: a research worker's sub-workers can't edit files")
		}
		req.Access = ProfileResearch
	}
	return req, nil
}

// accessWithin reports whether a sub-worker with access child stays within
// its parent's: research under anyone, coding under any writer, and qa or
// automation only under the same profile.
func accessWithin(parent, child string) bool {
	switch {
	case child == ProfileResearch:
		return true
	case parent == ProfileResearch:
		return false
	case child == ProfileCoding:
		return true
	}
	return child == parent
}

// fitChild keeps a staffed sub-worker within its parent's access. An
// access the parent chose is refused; one staffing picked is lowered to
// coding. A research parent's sub-worker must run confined (--access read
// or a read-only sandbox), so only confining models staff it.
func (c *Conductor) fitChild(parent *worker, req SpawnRequest, st *Staff) error {
	if !accessWithin(parent.staff.Access, st.Access) {
		if req.Access != "" {
			return fmt.Errorf("access %q exceeds yours (%s)", req.Access, parent.staff.Access)
		}
		st.Access = ProfileCoding
		st.Why = append(st.Why, "access lowered to coding to stay within its parent's")
	}
	if parent.staff.Access != ProfileResearch {
		return nil
	}
	var ok []Model
	for _, m := range append([]Model{st.Model}, st.Fallbacks...) {
		if c.confined(m) {
			ok = append(ok, m)
		}
	}
	switch {
	case len(ok) == 0:
		return fmt.Errorf("no ready model can run a confined research sub-worker (--access read or a read-only sandbox)")
	case !c.confined(st.Model) && (req.Runtime != "" || req.Model != ""):
		return fmt.Errorf("%s can't run confined, and a research worker's sub-workers must; use one of: %s", st.Model.Key(), keys(ok))
	}
	if modelKey(ok[0]) != modelKey(st.Model) {
		st.Why = append(st.Why, "model switched to one that runs confined")
	}
	st.Model, st.Fallbacks = ok[0], ok[1:]
	return nil
}

// messageScope: the lead messages its own workers, a worker its own
// sub-workers.
func messageScope(from, w *worker) error {
	switch {
	case from == nil && w.parent != nil:
		return fmt.Errorf("%s is %s's sub-worker; only %s can message it", w.id, w.parent.id, w.parent.id)
	case from != nil && w.parent != from:
		return fmt.Errorf("%s is not one of your sub-workers", w.id)
	}
	return nil
}

// waitFor is org_wait for the lead (parent nil) or for a worker, which
// waits only for its own sub-workers (all of them when ids is empty). A
// worker holds nothing while it waits: its sub-workers may need its slot
// or its lease.
func (c *Conductor) waitFor(ctx context.Context, parent *worker, ids []string, timeout time.Duration) []WorkerInfo {
	if parent == nil {
		// A lead that waits for workers is not editing: a writer it waits
		// for must not wait on the lead's lease.
		c.leadStopsEditing()
		return c.wait(ctx, ids, timeout)
	}
	c.mu.Lock()
	var mine []string
	var foreign []WorkerInfo
	for _, id := range c.order {
		if w := c.workers[id]; w.parent == parent && (len(ids) == 0 || slices.Contains(ids, id)) {
			mine = append(mine, id)
		}
	}
	for _, id := range ids {
		if !slices.Contains(mine, id) {
			foreign = append(foreign, WorkerInfo{ID: id, Status: "unknown", Error: "not one of your sub-workers"})
		}
	}
	c.mu.Unlock()
	if len(mine) == 0 {
		return append([]WorkerInfo{}, foreign...)
	}
	c.suspend(parent)
	out := c.wait(ctx, mine, timeout)
	// Back to working only once it holds its slot again: a question it
	// asked meanwhile may still be waiting (waiting_user).
	if retook, err := c.unsuspend(ctx, parent); retook && err == nil {
		c.setStatus(parent, chatevents.AgentWorking, "")
	}
	return append(out, foreign...)
}

// spawnsOn reports whether w gets the sub-worker tools on model m in this
// access mode: the lead allowed it, it is not a sub-worker itself, and
// the runtime takes caller tools in that mode.
func (c *Conductor) spawnsOn(w *worker, m Model, access string) bool {
	return w.allowSpawn && w.parent == nil && canTakeTools(m, access)
}

// canTakeTools reports whether an exec on m in this access mode can be
// given caller tools.
func canTakeTools(m Model, access string) bool {
	if access == monomind.AccessFull {
		return m.CallerToolsFull
	}
	return m.CallerTools
}

// suspend lets go of w's leases and slot for a wait (a question for the
// user, or its sub-workers); unsuspend takes them back once the last of
// its waits ends. Waits of one run can overlap (tool calls of one message
// run concurrently), so they are counted.
func (c *Conductor) suspend(w *worker) {
	c.mu.Lock()
	w.suspended++
	first := w.suspended == 1
	c.mu.Unlock()
	if !first {
		return
	}
	held := c.releaseHeld(w)
	c.mu.Lock()
	w.held = held
	c.mu.Unlock()
}

// retook reports that this was the last wait, so it took them back.
func (c *Conductor) unsuspend(ctx context.Context, w *worker) (retook bool, err error) {
	c.mu.Lock()
	w.suspended--
	last := w.suspended == 0
	held := w.held
	if last {
		w.held = nil
	}
	c.mu.Unlock()
	if !last {
		return false, nil
	}
	return true, c.retakeHeld(ctx, w, held)
}
