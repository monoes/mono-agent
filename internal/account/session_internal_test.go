package account

import (
	"testing"
	"time"
)

func TestElapsed(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"never recorded", time.Time{}, true},
		{"a minute ago", now.Add(-time.Minute), true},
		{"a second short of a minute", now.Add(-time.Minute + time.Second), false},
		{"just now", now, false},
		{"in the future: the clock went back", now.Add(time.Hour), true},
	}
	for _, c := range cases {
		if got := elapsed(now, c.t, time.Minute); got != c.want {
			t.Errorf("%s: elapsed = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGraceReason(t *testing.T) {
	cases := map[string]Reason{
		"unreachable":         ReasonUnreachable,
		"server_error":        ReasonServerError,
		"keyring_unavailable": ReasonKeyringUnavailable,
		"key_unknown":         ReasonServerError,
		"ok":                  ReasonUnreachable,
		"refused":             ReasonUnreachable,
		"":                    ReasonUnreachable,
		"something new":       ReasonUnreachable,
	}
	for in, want := range cases {
		if got := graceReason(in); got != want {
			t.Errorf("graceReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJudgeWithoutAReceiptIsInvalidNotAPanic(t *testing.T) {
	st := judge(&Session{V: 1, AccessToken: "x"}, nil, nil, time.Now())
	if st.State != StateLocked || st.Reason != ReasonInvalid {
		t.Fatalf("judge = %s/%s, want locked/invalid", st.State, st.Reason)
	}
}
