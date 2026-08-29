//go:build social

package instagram

import (
	"strings"
	"testing"
)

// TestTrackStagnation is a regression test: FetchFollowersList previously
// compared len(results) == len(seen) to detect scroll stagnation, which is a
// tautology (both are updated in lockstep, so it's always true) — the loop
// unconditionally broke after 4 attempts regardless of whether scrolling
// actually loaded new followers. trackStagnation instead compares the current
// count against the count from the previous scroll, matching the working
// pattern used in internal/bot/tiktok/actions.go.
func TestTrackStagnation(t *testing.T) {
	prevCount, noChangeRounds := 0, 0

	// New items loaded each round — never stagnates.
	for _, count := range []int{5, 12, 20} {
		prevCount, noChangeRounds = trackStagnation(count, prevCount, noChangeRounds)
		if noChangeRounds != 0 {
			t.Fatalf("count=%d: noChangeRounds = %d, want 0", count, noChangeRounds)
		}
	}

	// No new items for 3 consecutive rounds — should accumulate stagnation.
	for i := 1; i <= 3; i++ {
		prevCount, noChangeRounds = trackStagnation(20, prevCount, noChangeRounds)
		if noChangeRounds != i {
			t.Fatalf("stagnant round %d: noChangeRounds = %d, want %d", i, noChangeRounds, i)
		}
	}
	if prevCount != 20 {
		t.Fatalf("prevCount = %d, want 20", prevCount)
	}

	// A subsequent increase resets stagnation.
	prevCount, noChangeRounds = trackStagnation(25, prevCount, noChangeRounds)
	if noChangeRounds != 0 || prevCount != 25 {
		t.Fatalf("after growth: prevCount=%d, noChangeRounds=%d, want 25, 0", prevCount, noChangeRounds)
	}
}

// TestResolveCommentTargetState is an outbound-safety regression test:
// LikeComment and ReplyToComment previously fell back to the most recent
// comment whenever the requested commentAuthor had no match, silently
// liking/replying to the wrong person. Table covers the found, no-comments,
// author-not-found, and unexpected-state cases.
func TestResolveCommentTargetState(t *testing.T) {
	cases := []struct {
		name          string
		state         string
		commentAuthor string
		wantMarked    bool
		wantErr       string
	}{
		{
			name:       "author found and marked",
			state:      "marked",
			wantMarked: true,
		},
		{
			name:  "no candidate comments on page — no-op",
			state: "not_found",
		},
		{
			name:          "author specified but unmatched",
			state:         "author_not_found",
			commentAuthor: "somebody",
			wantErr:       "no comment by author \"somebody\" found",
		},
		{
			name:    "unexpected state surfaces as error",
			state:   "weird",
			wantErr: "unexpected comment lookup state",
		},
	}
	for _, c := range cases {
		marked, err := resolveCommentTargetState(c.state, c.commentAuthor, "https://www.instagram.com/p/abc123/", "like")
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: expected error containing %q, got: %v", c.name, c.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
		if marked != c.wantMarked {
			t.Errorf("%s: marked = %v, want %v", c.name, marked, c.wantMarked)
		}
	}
}

// TestSendVerified covers the honest post-send gate: a cleared composer
// confirms the send, a rendered bubble confirms the send, and anything else
// is a verification failure that must surface as an error.
func TestSendVerified(t *testing.T) {
	cases := []struct {
		name     string
		composer string
		bubbles  []string
		message  string
		want     bool
	}{
		{"composer cleared", "", nil, "hello", true},
		{"composer whitespace only", "   \n", nil, "hello", true},
		{"bubble contains message", "hello", []string{"earlier msg", "hello"}, "hello", true},
		{"composer uncleared, no bubbles", "hello", nil, "hello", false},
		{"composer uncleared, bubble mismatch", "hello", []string{"different"}, "hello", false},
	}
	for _, c := range cases {
		if got := sendVerified(c.composer, c.bubbles, c.message); got != c.want {
			t.Errorf("%s: sendVerified(%q, %v, %q) = %v, want %v", c.name, c.composer, c.bubbles, c.message, got, c.want)
		}
	}
}
