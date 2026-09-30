package dynorg

import (
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// MaxWorkerText bounds the assistant text journaled per worker, so a
// chatty worker can't bloat the lead's turn journal; its report still
// arrives whole in agent.message{result}.
const MaxWorkerText = 64 * 1024

// textCutNote ends a worker's journaled text once MaxWorkerText is spent.
const textCutNote = "\n\n[…more text from this agent was not journaled]"

// workerStream journals a worker's own text (assistant.delta with its
// AgentID, #258) and its usage as it runs (usage.updated with its AgentID,
// #257). Text is coalesced like the lead's (chatevents.TextCoalescer) and
// a part ends at each tool call, with part ids unique across the org
// ("w1:p1", "w1:p2", …).
type workerStream struct {
	mu        sync.Mutex
	coalescer *chatevents.TextCoalescer
	part      string
	parts     int
	bytes     int
	cut       bool
	timer     *time.Timer
	usageAt   time.Time // when its last usage snapshot was journaled
}

// usageEvery spaces a worker's usage snapshots; its result's always goes.
const usageEvery = time.Second

// stream handles the events of a worker's exec that workerEvent doesn't:
// text and usage. run is that exec's accounting so far. It runs before
// workerEvent, so text ahead of a tool call is journaled before the call.
func (c *Conductor) stream(w *worker, ev monomind.Event, run *monomind.TurnResult) {
	switch ev.Type {
	case monomind.EventAssistant:
		c.pushText(w, ev.Text)
	case monomind.EventToolActivity, monomind.EventToolCall:
		c.flushText(w, true)
	case monomind.EventUsage, monomind.EventResult:
		// Text so far goes ahead of the usage it led to.
		c.flushText(w, ev.Type == monomind.EventResult)
		if ev.Type == monomind.EventResult || w.stream.usageDue(time.Now()) {
			c.emitUsage(w, run)
		}
	}
}

// usageDue reports whether a usage snapshot may be journaled now, and
// if so counts it as journaled.
func (s *workerStream) usageDue(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.usageAt.IsZero() && now.Sub(s.usageAt) < usageEvery {
		return false
	}
	s.usageAt = now
	return true
}

func (c *Conductor) pushText(w *worker, text string) {
	if text == "" {
		return
	}
	s := &w.stream
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cut {
		return
	}
	if s.bytes+len(text) > MaxWorkerText {
		keep := max(0, MaxWorkerText-s.bytes)
		for keep > 0 && !utf8.RuneStart(text[keep]) {
			keep--
		}
		text = text[:keep] + textCutNote
		s.cut = true
	}
	s.bytes += len(text)
	if s.coalescer == nil {
		s.coalescer = chatevents.NewTextCoalescer()
	}
	if s.part == "" {
		s.parts++
		s.part = w.id + ":p" + strconv.Itoa(s.parts)
	}
	if part, out, ok := s.coalescer.Push(s.part, text, time.Now()); ok {
		c.emitText(w, part, out)
	} else if s.timer == nil {
		s.timer = time.AfterFunc(chatevents.CoalesceWindow+10*time.Millisecond, func() { c.flushText(w, false) })
	}
}

// flushText journals the worker's pending text; end also ends its current
// part (a tool call, or the end of the exec).
func (c *Conductor) flushText(w *worker, end bool) {
	s := &w.stream
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.coalescer != nil {
		if part, out, ok := s.coalescer.ForceFlush(); ok {
			c.emitText(w, part, out)
		}
	}
	if end {
		s.part = ""
	}
}

func (c *Conductor) emitText(w *worker, part, text string) {
	c.cfg.Emit.Emit(chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{AgentID: w.id, PartID: part, Text: text})
}

// emitUsage journals the worker's usage so far: its earlier execs (other
// models it fell over from, follow-ups) plus this one, the same totals
// agent.finished reports at the end.
func (c *Conductor) emitUsage(w *worker, run *monomind.TurnResult) {
	if !run.HasInputTokens && !run.HasOutputTokens && !run.HasCostUSD {
		return
	}
	c.mu.Lock()
	p := chatevents.UsageUpdatedPayload{AgentID: w.id, Source: "worker"}
	if run.HasInputTokens || run.HasOutputTokens || w.hasTok {
		in, out := w.inTok+run.InputTokens, w.outTok+run.OutputTokens
		p.InputTokens, p.OutputTokens = &in, &out
	}
	if cost, estimated, ok := liveCost(w.model, run); ok || w.hasCost {
		total := w.cost + cost
		p.CostUSD, p.CostEstimated = &total, estimated || w.costEstimated
	}
	c.mu.Unlock()
	c.cfg.Emit.Emit(chatevents.EventUsageUpdated, p)
}
