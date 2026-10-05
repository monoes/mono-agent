package account_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// The kid of a token whose kid is not pinned is the one piece of token content a
// VerifyError message carries, and whoever made the token chose it. The message
// quotes at most 64 bytes of it, cut at a character boundary and followed by
// "..." when cut, however long the kid is.
func TestVerifyKeyUnknownMessageQuotesAtMost64BytesOfTheKid(t *testing.T) {
	f := accounttest.New(t)
	now := f.Clock.Now()
	want := func(quoted string) string {
		return fmt.Sprintf("account: token key %q is not pinned in this build", quoted)
	}
	short := func(s string) string { // a failure must not dump a kid of thousands of bytes
		if len(s) > 160 {
			return fmt.Sprintf("%s... (%d bytes)", s[:160], len(s))
		}
		return s
	}
	cases := []struct{ name, kid, quoted string }{
		{"a short kid is quoted whole", "rotated-away", "rotated-away"},
		{"64 bytes are quoted whole", strings.Repeat("k", 64), strings.Repeat("k", 64)},
		{"65 bytes are cut", strings.Repeat("k", 65), strings.Repeat("k", 64) + "..."},
		{"5000 bytes are cut", strings.Repeat("k", 5000), strings.Repeat("k", 64) + "..."},
		{"a character that ends at byte 64 stays", strings.Repeat("a", 62) + "é" + "tail", strings.Repeat("a", 62) + "é..."},
		{"a character that straddles byte 64 is dropped whole", strings.Repeat("a", 63) + "é" + "tail", strings.Repeat("a", 63) + "..."},
		{"control characters are escaped, not echoed", "ab\x1b[31m‮" + strings.Repeat("z", 100), "ab\x1b[31m‮" + strings.Repeat("z", 54) + "..."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := account.Verify(f.Token(accounttest.TokenOptions{KID: c.kid}), now)
			if reasonOf(err) != account.ReasonKeyUnknown {
				t.Fatalf("reason = %q, want key_unknown", reasonOf(err))
			}
			if got := err.Error(); got != want(c.quoted) {
				t.Fatalf("message = %s, want %s", short(got), short(want(c.quoted)))
			}
		})
	}
}
