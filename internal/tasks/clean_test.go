package tasks

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"line ends", "a\r\nb\rc", "a\nb\nc"},
		{"escape byte", "red \x1b[31mtext\x1b[0m", "red [31mtext[0m"},
		{"tab and newline stay", "a\tb\nc", "a\tb\nc"},
		{"other controls", "a\x00b\x07c\x7fd", "abcd"},
		{"unicode tag characters", "visible\U000E0049\U000E0067hidden", "visiblehidden"},
		{"bidi overrides", "a\u202eb\u2066c", "abc"},
		{"byte order mark", "\ufefftext", "text"},
		{"invalid utf-8", "a\xffb", "a\ufffdb"},
		{"trimmed", "  \n text \t\n", "text"},
		{"only controls and space", "\x00\x1b \t\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanText(c.in)
			if got != c.want {
				t.Errorf("cleanText(%q) = %q, want %q", c.in, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("cleanText(%q) is not valid UTF-8", c.in)
			}
		})
	}
}

func TestCutRunesNeverSplitsACharacter(t *testing.T) {
	for _, s := range []string{"héllo wörld, this is long", strings.Repeat("\U0001F468\u200d\U0001F469\u200d\U0001F467", 40), strings.Repeat("日本語", 30)} {
		got := cutRunes(s, 10)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 10 || !strings.HasSuffix(got, "…") {
			t.Errorf("cutRunes(%q, 10) = %q", s, got)
		}
	}
	if got := cutRunes("short", 10); got != "short" {
		t.Errorf("a short string changed: %q", got)
	}
}

func TestCutBytesCutsOnACharacterAndSaysSo(t *testing.T) {
	long := strings.Repeat("é", 70<<10) // two bytes each
	got := cutBytes(long, MaxNotesBytes)
	if !utf8.ValidString(got) {
		t.Fatal("the cut text is not valid UTF-8")
	}
	i := strings.Index(got, "\n[truncated: ")
	if i < 0 || i > MaxNotesBytes {
		t.Fatalf("no truncation marker within the limit (marker at %d)", i)
	}
	if !strings.HasSuffix(got, "71680 characters in the original]") {
		t.Errorf("the marker must carry the original length: %q", got[i:])
	}
	// The leading byte puts the limit three bytes into a four-byte character: the cut must step back to the one before.
	got = cutBytes("x"+strings.Repeat("\U0001F600", 20000), MaxNotesBytes)
	if want := "x" + strings.Repeat("\U0001F600", 16383) + "\n[truncated: 20001 characters in the original]"; got != want {
		t.Errorf("a cut inside a four-byte character: %d bytes, valid UTF-8 %v, want %d bytes", len(got), utf8.ValidString(got), len(want))
	}
	if short := cutBytes("fits", MaxNotesBytes); short != "fits" {
		t.Errorf("a short text changed: %q", short)
	}
}

func TestDeriveTitleNotes(t *testing.T) {
	cases := []struct {
		name                 string
		title, notes, text   string
		wantTitle, wantNotes string
		wantErr              bool
	}{
		{name: "title only", title: "Fix it", wantTitle: "Fix it"},
		{name: "title and notes", title: "Fix it", notes: "In api.go", wantTitle: "Fix it", wantNotes: "In api.go"},
		{name: "title and text: the text is the notes", title: "Fix it", text: "details", wantTitle: "Fix it", wantNotes: "details"},
		{name: "one line of text is the title", text: "Reply to Sam", wantTitle: "Reply to Sam"},
		{name: "several lines", text: "Reply to Sam\nabout the invoice", wantTitle: "Reply to Sam", wantNotes: "Reply to Sam\nabout the invoice"},
		{name: "spaces collapse in the title only", text: "Fix   the\nbug", wantTitle: "Fix the", wantNotes: "Fix   the\nbug"},
		{name: "nothing", wantErr: true},
		{name: "only spaces and controls", text: " \x1b\x00\t\n ", wantErr: true},
		{name: "a title of controls only", title: "\x07 \x1b", wantErr: true},
		{name: "notes without a title", notes: "orphan", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, notes, err := deriveTitleNotes(c.title, c.notes, c.text)
			if c.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("err %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil || title != c.wantTitle || notes != c.wantNotes {
				t.Errorf("got (%q, %q, %v), want (%q, %q)", title, notes, err, c.wantTitle, c.wantNotes)
			}
		})
	}
}

func TestADerivedTitleIsCutAtOneHundredTwentyCharacters(t *testing.T) {
	text := strings.Repeat("word ", 80)
	title, notes, err := deriveTitleNotes("", "", text)
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(title) > DerivedTitleRunes || !strings.HasSuffix(title, "…") {
		t.Errorf("title %q (%d runes)", title, utf8.RuneCountInString(title))
	}
	if notes != strings.TrimSpace(text) {
		t.Error("the notes must keep the whole text")
	}
}

func TestCleanURL(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", MaxURLBytes)
	cases := []struct{ in, want string }{
		{"https://example.com/a?b=1#c", "https://example.com/a?b=1#c"},
		{"  http://example.com  ", "http://example.com"},
		{"https://user:pass@example.com/x", "https://example.com/x"},
		{"ftp://example.com/x", ""},
		{"javascript:alert(1)", ""},
		{"https:///nohost", ""},
		{"not a url", ""},
		{"https://example.com/\x00", ""},
		{"https://example.com/?q=a\U0000202eb", ""},    // a bidi override: net/url keeps a query exactly as typed
		{"https://example.com/?q=a\U000E0049b", ""},    // a tag character
		{"https://example.com/?q=a\U0000009b31mb", ""}, // a C1 control, an escape introducer in a UTF-8 terminal
		{"https://example.com/?q=a\xffb", ""},          // invalid UTF-8
		{"https://example.com/a\U0000202eb", ""},       // anywhere in the URL: net/url would have percent-encoded this one
		{"https://example.com/?q=café&lang=日本語", "https://example.com/?q=café&lang=日本語"},
		{long, ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := cleanURL(c.in); got != c.want {
			t.Errorf("cleanURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNameAndClientIDShapes(t *testing.T) {
	for _, ok := range []string{"claude-7f3a", "agent:claude-code#a3f9", "a.b_c@d"} {
		if !nameRE.MatchString(ok) {
			t.Errorf("name %q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "has space", "semi;colon", strings.Repeat("a", 65), "tab\t"} {
		if nameRE.MatchString(bad) {
			t.Errorf("name %q should be refused", bad)
		}
	}
	if !clientIDRE.MatchString("550e8400-e29b-41d4-a716-446655440000") || clientIDRE.MatchString("a b") || clientIDRE.MatchString("") {
		t.Error("client id shape")
	}
}
