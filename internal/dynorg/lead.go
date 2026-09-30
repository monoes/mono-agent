package dynorg

import (
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// NoticeLeadEditConflict is the notice code for a lead edit made while a
// worker held the write lease.
const NoticeLeadEditConflict = "org_lead_edit_conflict"

// LeadEvent observes one of the lead's own events (#260). The lead edits
// the same folder as its writers, so it takes the write lease around each
// of its file-editing tool calls: from the call's start until its end (or
// until the lead waits for workers, or the turn ends). Writers queue
// behind it as behind any writer.
//
// The lead's native tools run inside its runtime, so an edit that starts
// while a worker holds the lease cannot be refused from here. It is
// reported instead: a warning notice in the chat, and a warning in the
// lead's next org tool result telling it to org_wait for writers before
// editing.
func (c *Conductor) LeadEvent(ev monomind.Event) {
	if ev.Type != monomind.EventToolActivity || ev.ID == "" {
		return
	}
	switch ev.Phase {
	case "start":
		if !isEditCall(ev) {
			return
		}
		c.mu.Lock()
		if c.leadHolds || c.write.tryAcquire() {
			c.leadHolds = true
			c.leadEdits[ev.ID] = true
			c.mu.Unlock()
			return
		}
		writers := c.writersLocked()
		file := editTarget(ev)
		if file == "" {
			file = ev.Name
		}
		msg := fmt.Sprintf("You edited %s while %s held the write lease; org_wait for writing workers before you edit files yourself.", file, strings.Join(writers, ", "))
		c.leadWarnings = append(c.leadWarnings, msg)
		c.mu.Unlock()
		c.cfg.Emit.Emit(chatevents.EventNotice, chatevents.NoticePayload{
			Code: NoticeLeadEditConflict, Severity: chatevents.SeverityWarning,
			Message: fmt.Sprintf("The lead edited %s while %s held the write lease.", file, strings.Join(writers, ", ")),
		})
	case "end":
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.leadEdits[ev.ID] {
			return
		}
		delete(c.leadEdits, ev.ID)
		if len(c.leadEdits) == 0 && c.leadHolds {
			c.leadHolds = false
			c.write.release()
		}
	}
}

// leadStopsEditing releases the lead's write lease: it is waiting for
// workers, or its turn is over, so an edit whose end event never came
// can't keep writers queued.
func (c *Conductor) leadStopsEditing() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.leadEdits)
	if c.leadHolds {
		c.leadHolds = false
		c.write.release()
	}
}

// takeLeadWarnings returns and clears the warnings for the lead.
func (c *Conductor) takeLeadWarnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.leadWarnings
	c.leadWarnings = nil
	return w
}

// writersLocked lists the running workers that hold or wait on the write
// lease, or "a worker" when none is known.
func (c *Conductor) writersLocked() []string {
	var out []string
	for _, id := range c.order {
		w := c.workers[id]
		if w != nil && running(w.status) && w.status != chatevents.AgentWaitingLease && (writes(w.staff.Access) || w.unconfined || !c.confined(w.model)) {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		out = []string{"a worker"}
	}
	return out
}
