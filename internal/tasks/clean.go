package tasks

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	nameRE     = regexp.MustCompile(fmt.Sprintf(`^[A-Za-z0-9._#@:-]{1,%d}$`, MaxNameLen))
	clientIDRE = regexp.MustCompile(fmt.Sprintf(`^[A-Za-z0-9_-]{1,%d}$`, MaxClientIDLen))
)

// hidden reports characters that show nothing but can carry text to a reader:
// the Unicode tag block (invisible "ASCII smuggling"), bidi controls and the
// byte order mark.
func hidden(r rune) bool {
	switch {
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return r == 0xFEFF
}

// cleanText makes text safe to store and to print: invalid UTF-8 becomes
// U+FFFD, line ends (CRLF, CR and the Unicode line and paragraph separators
// U+2028 and U+2029, which the printers would not split on) become \n, control
// characters other than \n and \t and the hidden characters are dropped (an
// escape byte would otherwise reach a terminal), and the ends are trimmed. The
// characters that languages and emoji need are kept: the zero-width joiners
// U+200C and U+200D, the marks U+200E and U+200F and the Arabic letter mark
// U+061C.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "\ufffd")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == 0x2028, r == 0x2029:
			return '\n'
		case unicode.IsControl(r), hidden(r):
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// oneLine is cleanText with every run of white space collapsed to one space.
func oneLine(s string) string { return strings.Join(strings.Fields(cleanText(s)), " ") }

// cutRunes cuts s to at most n runes (n >= 2), ending in an ellipsis when it cut.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRightFunc(string(r[:n-1]), unicode.IsSpace) + "…"
}

// cutBytes cuts s (valid UTF-8) to at most maxBytes bytes on a character
// boundary and says how long the original was. The notice counts in the limit,
// so cutting a text that was already cut changes nothing; only a limit shorter
// than the notice itself gives more than maxBytes: the notice alone.
func cutBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	notice := fmt.Sprintf("\n[truncated: %d characters in the original]", utf8.RuneCountInString(s))
	cut := s[:max(maxBytes-len(notice), 0)]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + notice
}

// cleanTitle is a title as stored: one line, at most MaxTitleRunes.
func cleanTitle(s string) string { return cutRunes(oneLine(s), MaxTitleRunes) }

// deriveTitleNotes turns the three ways a task's words arrive into the stored
// title and notes (spec 4.6). It refuses words that clean to nothing, and notes
// given together with text.
func deriveTitleNotes(title, notes, text string) (string, string, error) {
	title, notes, text = cleanTitle(title), cutBytes(cleanText(notes), MaxNotesBytes), cleanText(text)
	switch {
	case notes != "" && text != "":
		return "", "", invalid("give notes or text, not both")
	case title != "":
		if notes == "" {
			notes = cutBytes(text, MaxNotesBytes)
		}
		return title, notes, nil
	case notes != "":
		return "", "", invalid("notes need a title: give a title, or send the text alone and its first line becomes the title")
	case text == "":
		return "", "", invalid("a task needs a title or some text")
	}
	first := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		first = text[:i]
	}
	derived := cutRunes(strings.Join(strings.Fields(first), " "), DerivedTitleRunes)
	if derived == text {
		return derived, "", nil
	}
	return derived, cutBytes(text, MaxNotesBytes), nil
}

// cleanURL returns the URL when it is a plain http or https address, without
// user-info and within the limit; otherwise "". A URL with invalid UTF-8, a
// control character or a hidden character anywhere is refused, not rewritten:
// net/url keeps a query exactly as typed.
func cleanURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURLBytes {
		return ""
	}
	if !utf8.ValidString(raw) || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || hidden(r) }) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.User = nil
	out := u.String()
	if len(out) > MaxURLBytes {
		return ""
	}
	return out
}
