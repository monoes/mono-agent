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
		// The line and paragraph separators are line ends too: the printers split on \n only, and a renderer that
		// breaks lines on U+2028 and U+2029 would show the rest of a line at the margin.
		{"line and paragraph separators", "a\U00002028b\U00002029c", "a\nb\nc"},
		{"separators next to other line ends", "a\U00002028\U00002029\r\nb", "a\n\n\nb"},
		{"separators at the ends are trimmed", " \U00002028 a \U00002029 ", "a"},
		{"only separators", "\U00002028\U00002029", ""},
		{"escape byte", "red \x1b[31mtext\x1b[0m", "red [31mtext[0m"},
		{"tab and newline stay", "a\tb\nc", "a\tb\nc"},
		{"other controls", "a\x00b\x07c\x7fd", "abcd"},
		{"c1 controls", "a\U0000009b31mb\U0000009dc", "a31mbc"},
		{"unicode tag characters", "visible\U000E0049\U000E0067hidden", "visiblehidden"},
		{"range ends", "a\U0000202ab\U00002069c\U000E0000d\U000E007Fe", "abcde"},
		// The zero-width non-joiner is essential to Persian and the joiner to emoji sequences: never strip them.
		{"joiners stay", "a\U0000200cb\U0000200dc", "a\U0000200cb\U0000200dc"},
		{"bidi overrides", "a\u202eb\u2066c", "abc"},
		{"byte order mark", "\ufefftext", "text"},
		{"invalid utf-8", "a\xffb", "a\ufffdb"},
		{"invalid run", "a\xff\xfeb", "a\U0000fffdb"},
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

// Some invisible characters are kept on purpose, because the text of a language or of an emoji needs them:
// the zero-width non-joiner and joiner (Persian, Indic scripts, emoji sequences), the left-to-right and
// right-to-left marks (Hebrew and Arabic next to Latin text) and the Arabic letter mark. Each is kept
// alone, in a title as in a text, and a mutation that cleans one away fails on that one.
func TestTheInvisibleCharactersThatLanguagesNeedAreKept(t *testing.T) {
	for _, c := range []struct {
		name string
		mark string
	}{
		{"zero-width non-joiner U+200C", "\U0000200c"},
		{"zero-width joiner U+200D", "\U0000200d"},
		{"left-to-right mark U+200E", "\U0000200e"},
		{"right-to-left mark U+200F", "\U0000200f"},
		{"Arabic letter mark U+061C", "\U0000061c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := "a" + c.mark + "b"
			if got := cleanText(in); got != in {
				t.Errorf("cleanText(%q) = %q, want it kept", in, got)
			}
			if got := cleanTitle("a " + c.mark + "b"); got != "a "+c.mark+"b" {
				t.Errorf("cleanTitle kept %q, want %q", got, "a "+c.mark+"b")
			}
		})
	}
}

// A title is one line: the line and paragraph separators collapse with the other white space, as a newline
// does, and the text that a title is derived from ends its first line at one.
func TestTheLineAndParagraphSeparatorsCollapseInATitleAndEndTheFirstLineOfATextTitle(t *testing.T) {
	for _, in := range []string{"one\U00002028two\U00002029three", "one \U00002028 two\U00002029\U00002028three", "\U00002028one\ntwo\U00002029three\U00002029"} {
		if got := cleanTitle(in); got != "one two three" {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, "one two three")
		}
	}
	for _, c := range []struct{ name, text, wantTitle, wantNotes string }{
		{"a line separator", "Reply to Sam\U00002028about the invoice", "Reply to Sam", "Reply to Sam\nabout the invoice"},
		{"a paragraph separator", "Reply to Sam\U00002029about the invoice", "Reply to Sam", "Reply to Sam\nabout the invoice"},
	} {
		title, notes, err := deriveTitleNotes("", "", c.text)
		if err != nil || title != c.wantTitle || notes != c.wantNotes {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q)", c.name, title, notes, err, c.wantTitle, c.wantNotes)
		}
	}
}

func TestCutRunesGivesValidUTF8WithinTheLimitEndingInAnEllipsis(t *testing.T) {
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

func TestCutRunesKeepsExactlyTheLimitAndCutsOneRuneMore(t *testing.T) {
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"ten runes", "abcdefghij", 10, "abcdefghij"},
		{"eleven runes", "abcdefghijk", 10, "abcdefghi…"},
		{"ten two-byte runes", strings.Repeat("é", 10), 10, strings.Repeat("é", 10)},
		{"eleven two-byte runes", strings.Repeat("é", 11), 10, strings.Repeat("é", 9) + "…"},
		{"the space before the ellipsis goes", "hello world", 7, "hello…"},
	}
	for _, c := range cases {
		if got := cutRunes(c.in, c.n); got != c.want {
			t.Errorf("%s: cutRunes(%q, %d) = %q, want %q", c.name, c.in, c.n, got, c.want)
		}
	}
}

// checkCut asserts what every cut text owes: valid UTF-8, within the limit, and
// unchanged when it is cut again, also after cleaning, as an edit re-sends it.
func checkCut(t *testing.T, got string, limit int) {
	t.Helper()
	if !utf8.ValidString(got) {
		t.Error("the cut text is not valid UTF-8")
	}
	if len(got) > limit {
		t.Errorf("the cut text is %d bytes, over the limit of %d", len(got), limit)
	}
	if again := cutBytes(got, limit); again != got {
		t.Errorf("cutting the cut text again changed it: %d bytes became %d", len(got), len(again))
	}
	if again := cutBytes(cleanText(got), limit); again != got {
		t.Errorf("cutting the cleaned cut text again changed it: %d bytes became %d", len(got), len(again))
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
	checkCut(t, got, MaxNotesBytes)
	// The notice takes 46 bytes, which leaves 65490 for the text. Three leading bytes put that budget three bytes into a
	// four-byte character, so the cut has to step back three times.
	in := "xxx" + strings.Repeat("\U0001F600", 20000)
	notice := "\n[truncated: 20003 characters in the original]"
	if utf8.ValidString(in[:MaxNotesBytes-len(notice)]) {
		t.Fatal("the test input no longer puts the limit inside a character")
	}
	got = cutBytes(in, MaxNotesBytes)
	if want := "xxx" + strings.Repeat("\U0001F600", 16371) + notice; got != want {
		t.Errorf("a cut inside a four-byte character: %d bytes, valid UTF-8 %v, want %d bytes", len(got), utf8.ValidString(got), len(want))
	}
	checkCut(t, got, MaxNotesBytes)
	if short := cutBytes("fits", MaxNotesBytes); short != "fits" {
		t.Errorf("a short text changed: %q", short)
	}
}

func TestCutBytesKeepsExactlyTheLimitAndCutsOneByteMore(t *testing.T) {
	at := strings.Repeat("a", 65536)
	if got := cutBytes(at, MaxNotesBytes); got != at {
		t.Errorf("a text of exactly 65536 bytes changed (%d bytes now)", len(got))
	}
	got := cutBytes(at+"a", MaxNotesBytes)
	if want := strings.Repeat("a", 65490) + "\n[truncated: 65537 characters in the original]"; got != want {
		t.Errorf("a text of 65537 bytes: %d bytes, want %d", len(got), len(want))
	}
	checkCut(t, got, MaxNotesBytes)
}

func TestCutBytesWithALimitShorterThanTheNoticeGivesTheNoticeAlone(t *testing.T) {
	got := cutBytes(strings.Repeat("a", 100), 10)
	if want := "\n[truncated: 100 characters in the original]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveTitleNotes(t *testing.T) {
	cases := []struct {
		name                 string
		title, notes, text   string
		wantTitle, wantNotes string
		wantErr              string // a part of the error message; empty means no error
	}{
		{name: "title only", title: "Fix it", wantTitle: "Fix it"},
		{name: "title and notes", title: "Fix it", notes: "In api.go", wantTitle: "Fix it", wantNotes: "In api.go"},
		{name: "title and text: the text is the notes", title: "Fix it", text: "details", wantTitle: "Fix it", wantNotes: "details"},
		{name: "one line of text is the title", text: "Reply to Sam", wantTitle: "Reply to Sam"},
		{name: "several lines", text: "Reply to Sam\nabout the invoice", wantTitle: "Reply to Sam", wantNotes: "Reply to Sam\nabout the invoice"},
		{name: "spaces collapse in the title only", text: "Fix   the\nbug", wantTitle: "Fix the", wantNotes: "Fix   the\nbug"},
		{name: "nothing", wantErr: "needs a title or some text"},
		{name: "only spaces and controls", text: " \x1b\x00\t\n ", wantErr: "needs a title or some text"},
		{name: "a title of controls only", title: "\x07 \x1b", wantErr: "needs a title or some text"},
		{name: "notes without a title", notes: "orphan", wantErr: "need a title"},
		{name: "title, notes and text: no words are dropped silently", title: "Fix it", notes: "In api.go", text: "details", wantErr: "not both"},
		{name: "notes and text without a title", notes: "orphan", text: "stray", wantErr: "not both"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, notes, err := deriveTitleNotes(c.title, c.notes, c.text)
			if c.wantErr != "" {
				if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err %v, want ErrInvalid containing %q", err, c.wantErr)
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

func TestADerivedTitleOfOneHundredTwentyCharactersIsKeptAndOneMoreIsCut(t *testing.T) {
	at := strings.Repeat("é", 120)
	title, notes, err := deriveTitleNotes("", "", at)
	if err != nil || title != at || notes != "" {
		t.Errorf("a text of 120 characters: got (%d characters, %q, %v), want the text itself as the title and no notes", utf8.RuneCountInString(title), notes, err)
	}
	title, notes, err = deriveTitleNotes("", "", at+"é")
	if want := strings.Repeat("é", 119) + "…"; err != nil || title != want || notes != at+"é" {
		t.Errorf("a text of 121 characters: got (%q, %d bytes of notes, %v), want title %q and the whole text as the notes", title, len(notes), err, want)
	}
}

func TestATitleIsKeptAtTwoHundredCharactersAndCutBeyond(t *testing.T) {
	at := strings.Repeat("é", 200)
	if got := cleanTitle(at); got != at {
		t.Errorf("a title of 200 characters changed: %d characters now", utf8.RuneCountInString(got))
	}
	if got, want := cleanTitle(at+"é"), strings.Repeat("é", 199)+"…"; got != want {
		t.Errorf("a title of 201 characters: %d characters, want %d, ending in an ellipsis", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
}

func TestDeriveTitleNotesCutsNotesAndTextAtSixtyFourKiB(t *testing.T) {
	over := strings.Repeat("a", 64<<10+1)
	// 65537 bytes of text cut to 65536: the 46-byte notice leaves room for 65490 bytes of it.
	wantNotes := strings.Repeat("a", 65490) + "\n[truncated: 65537 characters in the original]"
	cases := []struct{ name, title, notes, text, wantTitle string }{
		{"notes over the limit", "T", over, "", "T"},
		{"text over the limit becomes the notes", "T", "", over, "T"},
		{"text alone over the limit", "", "", over, strings.Repeat("a", 119) + "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, notes, err := deriveTitleNotes(c.title, c.notes, c.text)
			if err != nil || title != c.wantTitle || notes != wantNotes {
				t.Errorf("got (%q, %d bytes of notes, %v), want title %q and %d bytes of notes", title, len(notes), err, c.wantTitle, len(wantNotes))
			}
			checkCut(t, notes, MaxNotesBytes)
		})
	}
}

func TestCleanURL(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", MaxURLBytes)
	multibyte := "https://example.com/" + strings.Repeat("é", 700) // 1,420 bytes as typed, over 2,048 once net/url escapes it
	if len(multibyte) > MaxURLBytes {
		t.Fatalf("the test URL is %d bytes as typed: the first length check would refuse it, not the one after escaping", len(multibyte))
	}
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
		{multibyte, ""},
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

func TestParseStatus(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Status
	}{
		{"inbox", StatusInbox},
		{"ready", StatusReady},
		{"in_progress", StatusInProgress},
		{"review", StatusReview},
		{"done", StatusDone},
		{"archived", StatusArchived},
		{"progress", StatusInProgress},
		{"in-progress", StatusInProgress},
		{"DONE", StatusDone},
		{"In-Progress", StatusInProgress},
		{" \treview\n", StatusReview},
	} {
		if got, err := ParseStatus(c.in); err != nil || got != c.want {
			t.Errorf("ParseStatus(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "bogus"} {
		if got, err := ParseStatus(bad); !errors.Is(err, ErrInvalid) || got != "" {
			t.Errorf("ParseStatus(%q) = %q, %v; want ErrInvalid and no status", bad, got, err)
		}
	}
}

func TestActorLabel(t *testing.T) {
	for _, c := range []struct {
		actor Actor
		want  string
	}{
		{Actor{Kind: Human}, "you"},
		{Actor{Kind: Human, Name: "morteza"}, "you"},
		{Actor{Kind: Agent, Name: "claude-7f3a"}, "claude-7f3a"},
		{Actor{Kind: Agent}, "agent"},
		{Actor{Kind: Capture, Name: "chrome"}, "chrome"},
		{Actor{Kind: Capture}, "capture"},
	} {
		if got := c.actor.Label(); got != c.want {
			t.Errorf("%+v.Label() = %q, want %q", c.actor, got, c.want)
		}
	}
}
