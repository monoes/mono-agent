package openaiapi

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What the prompt of a turn with tools tells apart from a client's data: a result sits in
// a fence (<function_result> ... </function_result>), and a turn of the transcript opens with
// a marker ([user], [assistant], [tool NAME (ID)]) at the start of a line. A result is
// whatever the client's function returned (a file, a page, an API's answer), and the
// arguments of a call are what a model, steered or not, wrote: neither may end the fence,
// open one or pass for a turn. So what they say is read the way the model reads it, which is
// not the way an ASCII pattern does: any character a renderer or a tokenizer may end a line
// at ends one, what renders as nothing is not there, every kind of space is a space,
// full-width ASCII is ASCII, and case does not matter. A match is neutralised where the
// original has it (a bracket or an angle bracket becomes an entity) and the words stay
// readable. What looks like a letter and is another script (a Cyrillic "е" for an "e") is
// not looked through: it needs a table of confusables, and a dependency for it.

// lineBreaks are the characters a model, a tokenizer or a renderer may take for the end of
// a line (the ones Python's splitlines ends one at, and Unicode's line and paragraph
// separators); every one is a line feed in what is rendered.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", "\x1c", "\n", "\x1d", "\n", "\x1e", "\n",
	"\u0085", "\n", "\u2028", "\n", "\u2029", "\n")

var (
	// fenceTagRE and turnMarkerRE read a skeleton: upper case, one space for any run of
	// spaces, line feeds kept. The marker is at the start of a line, behind spaces only.
	fenceTagRE   = regexp.MustCompile(`<[ \n]*/?[ \n]*FUNCTION_RESULT`)
	turnMarkerRE = regexp.MustCompile(`(?m)^( *)\[(?:USER|ASSISTANT|TOOL|SYSTEM|DEVELOPER|FUNCTION)\b`)
)

// skeleton is text as a reader that ignores what is invisible takes it, and where each of
// its bytes came from.
type skeleton struct {
	text string
	at   []int // at[i] is the offset in the original of the character that gave byte i; one more at the end
}

// ignorable reports whether a character renders as nothing: the format characters (the
// zero-width ones, the bidirectional marks, the soft hyphen, the byte order mark), the
// variation selectors, the combining grapheme joiner and the Hangul fillers.
func ignorable(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) || strings.ContainsRune("\u034f\u115f\u1160\u3164\uffa0", r)
}

// foldRune is the character every case of a letter folds to: the lowest of them, which for
// a Latin letter is the capital (so the long s and the Kelvin sign are an S and a K).
func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}
	lowest := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < lowest {
			lowest = f
		}
	}
	return lowest
}

func newSkeleton(s string) skeleton {
	var b strings.Builder
	at := make([]int, 0, len(s)+1)
	for i, r := range s {
		switch {
		case r == '\n':
		case ignorable(r):
			continue
		case r >= 0xFF01 && r <= 0xFF5E: // full-width ASCII
			r -= 0xFEE0
		case unicode.IsSpace(r):
			r = ' '
		}
		start := b.Len()
		b.WriteRune(foldRune(r))
		for k := start; k < b.Len(); k++ {
			at = append(at, i)
		}
	}
	return skeleton{text: b.String(), at: append(at, len(s))}
}

// An edit puts a replacement for the character at an offset of the original.
type edit struct {
	at  int
	put string
}

func applyEdits(s string, edits []edit) string {
	if len(edits) == 0 {
		return s
	}
	slices.SortFunc(edits, func(a, b edit) int { return a.at - b.at })
	var b strings.Builder
	last := 0
	for _, e := range edits {
		if e.at < last { // two matches at one character: once is enough
			continue
		}
		_, size := utf8.DecodeRuneInString(s[e.at:])
		b.WriteString(s[last:e.at])
		b.WriteString(e.put)
		last = e.at + size
	}
	b.WriteString(s[last:])
	return b.String()
}

// neutralise rewrites the fence tags in text, and the turn markers when markers is set.
func neutralise(text string, markers bool) string {
	sk := newSkeleton(text)
	var edits []edit
	for _, m := range fenceTagRE.FindAllStringIndex(sk.text, -1) {
		edits = append(edits, edit{sk.at[m[0]], "&lt;"}) // the "<"
	}
	if markers {
		for _, m := range turnMarkerRE.FindAllStringSubmatchIndex(sk.text, -1) {
			edits = append(edits, edit{sk.at[m[3]], "&#91;"}) // the "[", behind the spaces of group 1
		}
	}
	return applyEdits(text, edits)
}

// defangResult is text of a client's function or of a model as the prompt may carry it: every
// line break a line feed, and no fence tag and no turn marker the model could read in it.
func defangResult(text string) string { return neutralise(lineBreaks.Replace(text), true) }

// argumentsInPrompt is the arguments of a call of the request as the transcript renders
// them, outside any fence. JSON is compact (which leaves no line break in it but the
// three that JSON allows raw inside a string, escaped here) and has its angle brackets
// and ampersands escaped; the fence tags a skeleton finds in it are neutralised all the
// same. Anything else is defanged as a result is.
func argumentsInPrompt(raw json.RawMessage) string {
	text := argumentsText(raw)
	var compact bytes.Buffer
	if json.Compact(&compact, []byte(text)) != nil {
		return defangResult(text)
	}
	var escaped bytes.Buffer
	json.HTMLEscape(&escaped, compact.Bytes()) // <, >, & and the line and paragraph separators
	return neutralise(strings.ReplaceAll(escaped.String(), "\u0085", `\u0085`), false)
}
