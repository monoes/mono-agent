package account

import (
	"testing"
	"time"
)

// These tests are in package account because holdFor is not exported, and the
// case it covers best, a token that cannot be read back, cannot be made to
// happen from outside: NewSession has just verified the token that the cache
// verifies again.

func TestHoldForIsHalfTheLifetimeOfTheTokenJustObtained(t *testing.T) {
	at := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	receipt := func(life time.Duration) *Receipt { return &Receipt{IssuedAt: at, ExpiresAt: at.Add(life)} }
	cases := []struct {
		name string
		rcpt *Receipt
		want time.Duration
	}{
		{"a token that lives an hour", receipt(time.Hour), 30 * time.Minute},
		{"the longest life a token may have", receipt(MaxTokenLife), MaxTokenLife / 2},
		{"a token that lives eight minutes", receipt(8 * time.Minute), 4 * time.Minute},
		{"an odd number of seconds", receipt(61 * time.Second), 30*time.Second + 500*time.Millisecond},
		{"no receipt: the cache cannot read the token it just stored", nil, backoffMin},
	}
	for _, c := range cases {
		if got := holdFor(c.rcpt); got != c.want {
			t.Errorf("%s: holdFor = %v, want %v", c.name, got, c.want)
		}
	}
}
