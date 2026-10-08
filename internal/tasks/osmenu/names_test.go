package osmenu

import (
	"encoding/xml"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMenuNameCleansTheProfileName(t *testing.T) {
	for _, c := range []struct{ name, id, want string }{
		{"Work", "w", "Work"},
		{"  Side\tproject\r\nalpha  ", "w", "Side project alpha"},
		{"Clients/2026: Q4", "w", "Clients-2026- Q4"},
		{"Esc\x1b[31mRed\x7f", "w", "Esc[31mRed"},
		{"invoice\U0000202Egnp.exe", "w", "invoicegnp.exe"},
		{"tag\U000E0041\U000E0042s", "w", "tags"},
		{"bom\U0000FEFFx\U00002066y", "w", "bomxy"},
		{"bad\xffutf8", "w", "bad\U0000FFFDutf8"},
		{" \x00\x07 ", "work-id", "work-id"},
		{"a\U0000FFFEb\U0000FFFF", "w", "ab"},
		{"", "a/b", "a-b"},
		{"\x01", "\x02", "profile"},
	} {
		if got := menuName(c.name, c.id); got != c.want {
			t.Errorf("menuName(%q, %q) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
}

// hidden is a copy of internal/tasks' set: each range end is in it, and the
// neighbour outside each end is not.
func TestHiddenMatchesTheTasksSet(t *testing.T) {
	for r, want := range map[rune]bool{
		0xE0000: true, 0xE007F: true, 0xDFFFF: false, 0xE0080: false,
		0x202A: true, 0x202E: true, 0x2029: false, 0x202F: false,
		0x2066: true, 0x2069: true, 0x2065: false, 0x206A: false,
		0xFEFF: true, 0xFEFE: false, 0xFF00: false,
	} {
		if hidden(r) != want {
			t.Errorf("hidden(%U) = %v, want %v", r, !want, want)
		}
	}
}

func TestNamesAreCutAtTheLimit(t *testing.T) {
	exact := strings.Repeat("x", maxNameRunes)
	if got := menuName(exact, "w"); got != exact {
		t.Errorf("a name of exactly %d runes became %q", maxNameRunes, got)
	}
	got := menuName(exact+"y", "w")
	if utf8.RuneCountInString(got) != maxNameRunes || got != strings.Repeat("x", maxNameRunes-1)+"\U00002026" {
		t.Errorf("a name one over the limit became %q", got)
	}
	long := BundleName(strings.Repeat("\U0001F600", 100), "w")
	if len(long) > 255 || !strings.HasPrefix(long, MenuPrefix+" (") || !strings.HasSuffix(long, ").workflow") {
		t.Errorf("a bundle name of %d bytes: %q", len(long), long)
	}
	if got := MenuTitle("Work", "w"); got != "Add to MonoAgent Tasks: Work" {
		t.Errorf("MenuTitle = %q", got)
	}
	if got := BundleName("Work", "w"); got != "Add to MonoAgent Tasks (Work).workflow" {
		t.Errorf("BundleName = %q", got)
	}
}

func TestShellQuoteRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	for _, s := range []string{"plain", "it's", "'", "''", `a"b`, "$(echo injected)", "`echo injected`", `back\slash`, "new\nline", "-n", "*", "~", ""} {
		out, err := exec.Command("/bin/sh", "-c", "printf '%s' "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("sh read %s as %q (%v), want %q", shellQuote(s), out, err, s)
		}
	}
}

func TestPlistEscapeKeepsTextAsText(t *testing.T) {
	s := "a & b < c > d ]]> \"e\" 'f'\n\tg"
	esc := plistEscape(s)
	if esc != "a &amp; b &lt; c &gt; d ]]&gt; \"e\" 'f'\n\tg" {
		t.Errorf("plistEscape = %q", esc)
	}
	var back string
	if err := xml.Unmarshal([]byte("<string>"+esc+"</string>"), &back); err != nil || back != s {
		t.Errorf("an XML reader read %q (%v), want %q", back, err, s)
	}
}

func TestPlainValueRefusesWhatABundleCannotCarry(t *testing.T) {
	if err := plainValue("the profile id", "711ef586-9f4b-4b1f-b2fd-cad23eec0a03"); err != nil {
		t.Errorf("a uuid: %v", err)
	}
	if err := plainValue("the profile id", ""); err == nil || err.Error() != "the profile id is empty" {
		t.Errorf("empty: %v", err)
	}
	for _, v := range []string{"a\nb", "a\x1bb", "a\x7fb", "a\U0000202Eb", "a\xffb"} {
		err := plainValue("the profile id", v)
		if err == nil || !strings.HasSuffix(err.Error(), " holds a control or hidden character, or is not UTF-8") {
			t.Errorf("%q: %v", v, err)
		}
	}
}

func TestBundleIDIsOnePerProfile(t *testing.T) {
	a, b := bundleID("default"), bundleID("work-id")
	ok := regexp.MustCompile(`^com\.monoagent\.tasks\.menu\.[0-9a-f]{12}$`)
	if a == b || !ok.MatchString(a) || !ok.MatchString(b) || bundleID("default") != a {
		t.Errorf("bundle ids %q and %q", a, b)
	}
}
