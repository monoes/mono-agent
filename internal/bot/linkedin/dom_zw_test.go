//go:build !nosocial

package linkedin

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

// TestTextDropsZeroWidthSpaces: L.text and L.lines drop the zero-width
// spaces LinkedIn embeds in names ("Entrepreneurs'\u200b Organization") but
// keep the joiners emoji sequences and Persian text need.
func TestTextDropsZeroWidthSpaces(t *testing.T) {
	fastTimings(t)
	b := bottest.Launch(t)
	p, _ := newPage(t, b, bottest.Route{Pattern: "https://www.linkedin.com/zw-test/", Body: "<html><body>" +
		"<p id=a>Entrepreneurs'\u200b Organization\ufeff</p>" +
		"<p id=b>\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645 \U0001F468\u200d\U0001F4BB</p>" +
		"</body></html>"})
	if err := navigate(context.Background(), p, "https://www.linkedin.com/zw-test/"); err != nil {
		t.Fatal(err)
	}
	var got struct{ A, B, Line string }
	if err := run(p, `() => ({ A: L.text(document.getElementById('a')), B: L.text(document.getElementById('b')), Line: L.lines(document.body)[0] })`, &got); err != nil {
		t.Fatal(err)
	}
	if got.A != "Entrepreneurs' Organization" || got.Line != got.A {
		t.Errorf("text = %q, lines[0] = %q", got.A, got.Line)
	}
	if want := "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645 \U0001F468\u200d\U0001F4BB"; got.B != want {
		t.Errorf("joiners lost: %q, want %q", got.B, want)
	}
}
