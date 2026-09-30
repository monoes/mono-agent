package dynorg

import "context"

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
