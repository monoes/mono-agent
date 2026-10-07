package workflow

import (
	"sync"
	"time"
)

// lockedLogEvery spaces the log lines of an engine that is dropping triggers
// because the account is locked: a schedule that fires every second must not
// write a line per tick.
const lockedLogEvery = time.Minute

// dropNotes counts the triggers a locked engine dropped and says when a log
// line is due: at most one per lockedLogEvery, carrying the count since the
// last one. The zero value is ready to use.
type dropNotes struct {
	mu      sync.Mutex
	last    time.Time
	dropped int
	now     func() time.Time // nil: time.Now; a test sets it
}

// note counts one drop. When a line is due it returns how many drops that line
// covers and true.
func (d *dropNotes) note() (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	d.dropped++
	at := now()
	if !d.last.IsZero() && at.Sub(d.last) < lockedLogEvery {
		return 0, false
	}
	n := d.dropped
	d.last, d.dropped = at, 0
	return n, true
}
