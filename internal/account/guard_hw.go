package account

import (
	"context"
	"time"
)

// bumpHW raises the high-water mark to now once it is a minute stale. It never
// lowers it: only a verified token resets it (NewSession).
func bumpHW(s *Session, now time.Time) {
	if now.After(s.HW) && now.Sub(s.HW) >= hwInterval {
		s.HW = now
	}
}

// touchHW persists the high-water mark (spec §4.5): only when the package is not
// dormant, the stored mark is a minute stale, and this guard has not tried in the
// last minute. A refused session has no mark worth writing. A machine that has no
// session at all keeps the record too, from the enforcement date on (A25): the date
// is judged on max(now, hw) and hw lives only in session.json, so without a file a
// clock set back to before the date would un-enforce the gate for a machine that was
// refused, or never signed in. It never waits long for the lock and never reports a
// failure: a missed write is made up at the next call.
func (g *Guard) touchHW(now time.Time) {
	if dormant() {
		return
	}
	sess, _ := g.cached()
	switch {
	case sess == nil:
		if !Enforced(now, time.Time{}) { // a date is set and the clock has reached it
			return
		}
	case sess.State == stateRefused || !now.After(sess.HW) || now.Sub(sess.HW) < hwInterval:
		return
	}
	g.mu.Lock()
	try := elapsed(now, g.lastHWAttempt, hwInterval)
	if try {
		g.lastHWAttempt = now
	}
	g.mu.Unlock()
	if !try {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), hwLockWait)
	defer cancel()
	unlock, err := g.store.Lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	fresh, err := g.store.Load()
	if err != nil {
		return
	}
	if fresh == nil {
		if sess == nil { // not a session that vanished under a cached one: that file is not this guard's to bring back
			g.keepRecord(now)
		}
		return
	}
	if fresh.State == stateRefused {
		// Another process marked the session refused. Take it in now: a marker
		// that left session.json's modification time as it was (a coarse file
		// timestamp) would otherwise stay unseen by a long-running guard.
		g.adopt(fresh)
		return
	}
	next := *fresh
	bumpHW(&next, now)
	if !next.PendingSince.IsZero() && now.After(next.LastAttempt) && !next.HW.Equal(fresh.HW) {
		// A grant is in doubt: the write also carries the last attempt up to the clock,
		// never down, so that a running refresher keeps the evidence that pendingExpired
		// reads at most a minute old, and a clock set back is seen at its next pass (A24).
		next.LastAttempt = now
	}
	if next.HW.Equal(fresh.HW) {
		// A peer has raised the mark already: nothing to write, only to take in.
		g.adoptUnlessOlder(fresh)
		return
	}
	if g.store.Save(&next) != nil {
		// The mark was not written. The session read under the lock is as new as the
		// file, and another process may have written it within one modification-time
		// tick, so take it in, unless the guard is ahead of the file.
		g.adoptUnlessOlder(fresh)
		return
	}
	g.adopt(&next)
}

// keepRecord saves the session with no token that a machine which never signed in
// leaves (A25): a host and a mark, the record that a logout leaves, and nothing else.
// The caller holds the lock and has found no session. A failure is not reported.
func (g *Guard) keepRecord(now time.Time) {
	record := &Session{V: sessionVersion, Host: HostURL, HW: now}
	if g.store.Save(record) != nil {
		return
	}
	g.adopt(record)
}

// adoptUnlessOlder takes fresh, a session just read under the file lock, in as the
// cached one, unless the guard is ahead of the file. It is ahead when a write of its
// own failed (applyTokens' save of a refreshed session, say) and only the cache
// holds that session: the older file must not put it back, or the guard would go
// back to the old token, find it due and refresh again at every call. LastAttempt
// orders the writes that change a session (NewSession, recordAttempt and
// applyRefusal set it); a high-water write leaves it alone, so a file that only
// has a newer mark is as new as the session it holds, except over a grant in doubt,
// where it raises it to the clock: that file is then newer than a cache that is
// ahead of it, and is taken in, as it should be, since the disk holds the marker.
func (g *Guard) adoptUnlessOlder(fresh *Session) {
	if cur, _ := g.cached(); cur == nil || !fresh.LastAttempt.Before(cur.LastAttempt) {
		g.adopt(fresh)
	}
}
