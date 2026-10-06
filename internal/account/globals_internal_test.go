package account

import (
	"sync"
	"testing"
)

func TestIsStrictFollowsTheStrictFlag(t *testing.T) {
	if isStrict() {
		t.Fatal("isStrict() = true in a fresh test binary, want false")
	}
	t.Run("while a test holds the flag", func(t *testing.T) {
		StrictForTest(t)
		if !isStrict() {
			t.Fatal("isStrict() = false after StrictForTest")
		}
	})
	if isStrict() {
		t.Fatal("isStrict() = true after the test that set the flag ended")
	}
}

// The flag is read under globalsMu, so that a read never races with the hook
// that sets it. The reader runs while the hook flips the flag; only the race
// detector can see a read that takes no lock, so run this with -race.
func TestIsStrictReadsTheFlagUnderTheLock(t *testing.T) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				isStrict()
			}
		}
	}()
	for range 200 {
		t.Run("flip", func(t *testing.T) { StrictForTest(t) }) // sets the flag, and the cleanup puts it back
	}
	close(stop)
	wg.Wait()
	if isStrict() {
		t.Fatal("the flag was left set")
	}
}
