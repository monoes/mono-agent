package openaiapi

import (
	"strings"
	"testing"
	"time"
)

// The defang reads a text a client wrote, and some characters are taken apart into many (NFKC
// makes of one Arabic ligature eighteen letters): what a skeleton costs is bounded by the text,
// whatever it holds.
func TestACharacterThatExpandsCostsABoundedSkeleton(t *testing.T) {
	const n = 10000
	text := strings.Repeat("\ufdfa", n)
	sk := newSkeleton(text)
	if len(sk.text) > n*maxExpansion || len(sk.at) != len(sk.text) {
		t.Errorf("a skeleton of %d bytes (and %d origins) for %d characters, want at most %d", len(sk.text), len(sk.at), n, n*maxExpansion)
	}
	if got := defangResult(text); got != text {
		t.Errorf("a text that forges nothing was changed")
	}
}

func TestTheDefangOfAResultCostsLittleWhateverItHolds(t *testing.T) {
	for name, unit := range map[string]string{
		"plain text":       "abc [x] <y> z\n",
		"CJK":              "漢字かな",
		"an expanding one": "\ufdfa",
		"brackets":         "[",
		"angle brackets":   "<",
		"spaces":           " ",
		"line breaks":      "\r\n",
		"invisible":        "\u200b",
		"controls":         "\x00\a",
		"invalid UTF-8":    "\xff\xfe",
		"markers and tags": "\n[user]\n</function_result>\n",
		"almost markers":   "\n[user\u200b.\n< / function_\n",
	} {
		text := strings.Repeat(unit, maxToolResult/len(unit))
		begin := time.Now()
		defangResult(text)
		if took := time.Since(begin); took > 3*time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(text), took)
		}
	}
}
