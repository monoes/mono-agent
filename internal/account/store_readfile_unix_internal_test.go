//go:build !windows

package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// makeFIFO makes a FIFO at path. Its cleanup opens the FIFO for writing, so that
// a reader a regression left waiting in open returns and its goroutine ends; it
// is registered after the directory's, so it runs first.
func makeFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if w, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
	})
}

// session.json and refresh.enc that are FIFOs, or links to a FIFO or to a device,
// are refused at once. Opening a FIFO waits for a writer that never comes (in a
// daemon with the guard's reload lock held, so every Status waits behind it, and
// for refresh.enc with session.lock held), and a device such as /dev/zero would be
// read until the process is out of memory. A refresh.enc that is refused never
// reaches the key store.
func TestTheStoreRefusesAFIFOOrADeviceWithoutWaiting(t *testing.T) {
	for _, c := range []struct {
		name string
		make func(t *testing.T, path string)
	}{
		{"a FIFO", makeFIFO},
		{"a link to a FIFO", func(t *testing.T, path string) {
			fifo := path + ".fifo"
			makeFIFO(t, fifo)
			if err := os.Symlink(fifo, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"a link to a device", func(t *testing.T, path string) {
			if err := os.Symlink(os.DevNull, path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.make(t, filepath.Join(dir, sessionFile))
			c.make(t, filepath.Join(dir, refreshFile))
			sealer := &openCounter{Sealer: NewMemorySealer()}
			st := OpenStore(dir, sealer)
			if l := loadWithin(t, st); l.sess != nil || !errors.Is(l.err, errNotRegular) || !errors.Is(l.err, errSessionInvalid) {
				t.Errorf("session.json %s: a session %v, %v, want the not-a-regular-file refusal", c.name, l.sess != nil, l.err)
			}
			if r := loadRefreshWithin(t, st); r.token != "" || !errors.Is(r.err, errNotRegular) {
				t.Errorf("refresh.enc %s: a token %v, %v, want the not-a-regular-file refusal", c.name, r.token != "", r.err)
			}
			if n := sealer.opens.Load(); n != 0 {
				t.Errorf("a refresh.enc that is not a regular file reached the key store %d times", n)
			}
		})
	}
}

// The third security review's FIFO: a guard whose session.json is a FIFO answers
// at once, and refuses (locked, invalid), instead of waiting in Load for a writer
// with the reload lock held.
func TestAGuardWhoseSessionIsAFIFOAnswersAtOnceAndRefuses(t *testing.T) {
	SetEnforceFromForTest(t, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	makeFIFO(t, filepath.Join(dir, sessionFile))
	g := NewGuard(GuardOptions{Store: OpenStore(dir, NewMemorySealer())})
	err := within(t, "Require", func() error { return g.Require(context.Background()) })
	var lre *LoginRequiredError
	if !errors.As(err, &lre) || lre.Status.State != StateLocked || lre.Status.Reason != ReasonInvalid {
		t.Errorf("Require = %v, want locked(invalid)", err)
	}
}
