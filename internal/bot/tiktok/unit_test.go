//go:build social

package tiktok

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/jev"
)

// Browser-free checks (run in every `go test -tags social`).

func TestProfileTarget(t *testing.T) {
	cases := []struct{ in, url, handle string }{
		{"https://www.tiktok.com/@fake_a", "https://www.tiktok.com/@fake_a", "fake_a"},
		{"https://www.tiktok.com/@fake_a/video/1", "https://www.tiktok.com/@fake_a/video/1", "fake_a"},
		{"/@fake_b", "https://www.tiktok.com/@fake_b", "fake_b"},
		{"@fake_c", "https://www.tiktok.com/@fake_c", "fake_c"},
		{"fake_d", "https://www.tiktok.com/@fake_d", "fake_d"},
	}
	for _, c := range cases {
		u, h, err := profileTarget(c.in)
		if err != nil || u != c.url || h != c.handle {
			t.Errorf("profileTarget(%q) = %q, %q, %v; want %q, %q", c.in, u, h, err, c.url, c.handle)
		}
	}
	for _, bad := range []string{"", "  ", "two words", "@"} {
		if _, _, err := profileTarget(bad); err == nil {
			t.Errorf("profileTarget(%q): want an error", bad)
		}
	}
}

func TestSendVerified(t *testing.T) {
	empty, typed := "", "hello"
	cases := []struct {
		name          string
		before, after chatState
		want          bool
	}{
		{"new bubble", chatState{N: 0, Composer: &typed}, chatState{N: 1, Composer: &typed}, true},
		{"composer cleared", chatState{Composer: &typed}, chatState{Composer: &empty}, true},
		{"nothing changed", chatState{N: 1, Composer: &typed}, chatState{N: 1, Composer: &typed}, false},
		{"composer gone", chatState{Composer: &typed}, chatState{}, false},
	}
	for _, c := range cases {
		if got := sendVerified(c.before, c.after); got != c.want {
			t.Errorf("%s: sendVerified = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSnippetAndJSString(t *testing.T) {
	if got := snippet("  a \n b  "); got != "a b" {
		t.Errorf("snippet = %q", got)
	}
	if got := snippet(strings.Repeat("x", 100)); len(got) != 60 {
		t.Errorf("snippet len = %d", len(got))
	}
	if got := jsString("it's </script>\n"); got != `'it\'s \x3c/script>\n'` {
		t.Errorf("jsString = %s", got)
	}
}

// Every call_bot_method the shipped TikTok actions use exists, and methods
// reject a missing page before touching anything.
func TestShippedActionsResolveTheirMethods(t *testing.T) {
	b := &TikTokBot{}
	loader := action.GetLoader()
	all, err := loader.ListAvailable()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range all {
		platform, typ, _ := strings.Cut(a, "/")
		if platform != "tiktok" {
			continue
		}
		def, err := loader.Load(platform, typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range def.Steps {
			if st.Type != "call_bot_method" {
				continue
			}
			n++
			fn, ok := b.GetMethodByName(st.MethodName)
			if !ok {
				t.Errorf("%s/%s: method %q not found", typ, st.ID, st.MethodName)
				continue
			}
			if _, err := fn(bg); err == nil || !strings.Contains(err.Error(), "missing page") {
				t.Errorf("%s: no page: err = %v", st.MethodName, err)
			}
		}
	}
	if n < 16 {
		t.Fatalf("only %d call_bot_method steps found", n)
	}
	if _, ok := b.GetMethodByName("no_such_method"); ok {
		t.Fatal("unknown method resolved")
	}
}

func TestTikTokBotIsAdapterWithJevSetter(t *testing.T) {
	var a botpkg.BotAdapter = &TikTokBot{}
	if _, ok := a.(interface{ SetJevPicker(*jev.Client, float64) }); !ok {
		t.Fatal("TikTokBot must expose SetJevPicker")
	}
	if botpkg.PlatformRegistry["TIKTOK"] == nil {
		t.Fatal("TIKTOK not registered")
	}
}
