package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// defaultWatchInterval is how often Watch polls when it is not told.
const defaultWatchInterval = 2 * time.Second

// Change is what Watch reports: the board revision and the counts at it.
type Change struct {
	Rev    int64
	Counts Counts
}

// Watch calls fn once at the start and then each time the profile's board revision moves, polling every
// interval (two seconds when it is not above 0), until ctx ends; it then returns nil. It blocks, so the
// caller runs it in a goroutine of its own. A poll, the call of fn and the wait for the next poll follow one
// another: fn is called with no connection held, so it may read the board, and a slow fn delays the next
// poll and never piles polls up. Watch returns only after the call of fn in progress has returned, and fn may
// be called once more just as ctx ends: a caller that has moved on to another profile must drop a call that
// comes late, by the profile id its watcher was started with.
//
// A poll reads the profile, the revision and, when the revision moved (spec 5.4), the counts, in one
// snapshot, so the counts are those of the revision they come with. A poll that fails is skipped: the next
// one tries again. Rev and Counts do not check the profile, so Watch does: a profile that does not exist is
// an ErrInvalid, returned before any call, and one that is deleted while it is watched (once any poll has
// found it, a report made or not) ends Watch with an error that wraps ErrNotFound, and no call. (Stale, the
// claims past their lease, is the one count that the clock moves without a write: a lease that runs out is
// not reported until a write moves the revision.)
func (s *Store) Watch(ctx context.Context, profileID string, interval time.Duration, fn func(Change)) error {
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	var (
		last      int64 // the revision fn was last called with
		delivered bool  // whether fn has been called yet
		seen      bool  // whether a poll has found the profile yet
	)
	for {
		c, moved, found, err := s.watchPoll(ctx, profileID, last, delivered)
		seen = seen || found
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, ErrInvalid) && seen:
			return fmt.Errorf("%w: profile %q was deleted", ErrNotFound, echo(profileID))
		case errors.Is(err, ErrInvalid):
			return err
		case err == nil && moved:
			last, delivered = c.Rev, true
			fn(c)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// watchPoll is one poll of Watch: the change to report, when there is one (moved), or the error of the
// poll. The first poll, and any poll that finds another revision than the last one reported, reads the
// counts too. found says that the profile was there, even when the poll failed after it had found it.
func (s *Store) watchPoll(ctx context.Context, profileID string, last int64, delivered bool) (c Change, moved, found bool, err error) {
	err = s.snapshot(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		found = true
		rev, err := s.revOf(ctx, x, profileID)
		if err != nil || (delivered && rev == last) {
			return err
		}
		counts, err := s.countsOf(ctx, x, profileID)
		if err != nil {
			return err
		}
		c, moved = Change{Rev: rev, Counts: counts}, true
		return nil
	})
	return c, moved, found, err
}
