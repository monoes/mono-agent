package openaiapi

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// What the prompt of a turn with tools tells apart from a client's data: a result sits in
// a fence (<function_result> ... </function_result>), and a turn of the transcript opens with
// a marker ([user], [assistant], [tool NAME (ID)]) at the start of a line. A result is
// whatever the client's function returned (a file, a page, an API's answer), and so are the
// words an assistant said before a call, which a result may have steered: neither may end the
// fence, open one or pass for a turn. The fence is the defence, a model is told that what is
// inside is data; this is a second layer, which closes the disguises known to work on a reader
// that looks through them.
//
// What is matched is not the text but its skeleton, the text as such a reader takes it: any
// character a renderer or a tokenizer may end a line at ends one, every kind of space is a
// space, case does not matter, a look-alike of an ASCII character (full-width, bold, circled,
// superscript, a ligature) is that character (NFKC), and whatever renders as nothing (control and
// format characters, the blank-looking fillers, combining marks, symbols) is not there. A
// match is neutralised where the original has it (a bracket or an angle bracket becomes an
// entity) and everything else is left as it was: line ends of every kind included, so that a
// file with CRLF line ends reaches the model as it is. A letter of another script that looks like
// a Latin one (a Cyrillic "е" for an "e") is not looked through: it needs a table of confusables,
// and a dependency for it; the fence does not depend on it.

// otherLetter stands in the skeleton for a letter or a digit that is not ASCII: it breaks a word
// without being any letter of it.
const otherLetter = '\x01'

// maxExpansion is the most characters one character is read as: NFKC takes an Arabic ligature
// apart into eighteen, and nothing that could spell a role word is longer than four.
const maxExpansion = 8

var (
	// fenceTagRE reads the skeleton without its spaces and line ends, since spaces may be
	// put anywhere in a tag: the bracket, slashes and the name of the fence.
	fenceTagRE = regexp.MustCompile(`</*FUNCTION_?RESULT`)
	// turnMarkerRE reads the skeleton: upper case, one space for any space. The marker is at
	// the start of a line, behind spaces only, and the role word is followed by the closing bracket,
	// a space or the end of the line: [tool.poetry] and [users] are not markers.
	turnMarkerRE = regexp.MustCompile(`(?m)^( *)\[ *(?:USER|ASSISTANT|TOOL|SYSTEM|DEVELOPER|FUNCTION)(?:[\] ]|$)`)
)

// skeleton is text as a reader that looks through disguises takes it, and where each of its
// bytes came from.
type skeleton struct {
	text string
	at   []int32 // at[i] is the offset in the original of the character that gave byte i
}

// see is what a reader makes of one character (one of those NFKC left): a line end, a space, an
// upper case ASCII character, a mark for a letter or a digit of another script, or nothing.
func see(r rune) (byte, bool) {
	switch {
	case r == '\n' || r == '\r' || r == '\v' || r == '\f' || (0x1c <= r && r <= 0x1e) || r == 0x85 || r == 0x2028 || r == 0x2029:
		return '\n', true
	case unicode.IsSpace(r):
		return ' ', true
	case r < utf8.RuneSelf:
		switch {
		case 'a' <= r && r <= 'z':
			return byte(r) - 'a' + 'A', true
		case 0x21 <= r && r <= 0x7e:
			return byte(r), true
		}
		return 0, false // a control character
	case unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r): // the Hangul fillers, among others, are letters that render as nothing
		return 0, false
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return otherLetter, true
	}
	return 0, false // format characters, combining marks, symbols, other punctuation
}

func newSkeleton(s string) skeleton {
	var b strings.Builder
	at := make([]int32, 0, len(s)+1)
	for i, r := range s {
		if r < utf8.RuneSelf {
			if c, ok := see(r); ok {
				b.WriteByte(c)
				at = append(at, int32(i))
			}
			continue
		}
		n := 0
		for _, q := range norm.NFKC.String(string(r)) {
			if n++; n > maxExpansion {
				break
			}
			if c, ok := see(q); ok {
				b.WriteByte(c)
				at = append(at, int32(i))
			}
		}
	}
	return skeleton{text: b.String(), at: at}
}

// withoutSpaces is the skeleton with its spaces and line ends taken out, and where each of its
// bytes came from.
func (sk skeleton) withoutSpaces() skeleton {
	var b strings.Builder
	at := make([]int32, 0, len(sk.at))
	for i := 0; i < len(sk.text); i++ {
		if c := sk.text[i]; c != ' ' && c != '\n' {
			b.WriteByte(c)
			at = append(at, sk.at[i])
		}
	}
	return skeleton{text: b.String(), at: at}
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
	if strings.IndexByte(sk.text, '<') >= 0 {
		flat := sk.withoutSpaces()
		for _, m := range fenceTagRE.FindAllStringIndex(flat.text, -1) {
			edits = append(edits, edit{int(flat.at[m[0]]), "&lt;"}) // the "<"
		}
	}
	if markers && strings.IndexByte(sk.text, '[') >= 0 {
		for _, m := range turnMarkerRE.FindAllStringSubmatchIndex(sk.text, -1) {
			edits = append(edits, edit{int(sk.at[m[3]]), "&#91;"}) // the "[", behind the spaces of group 1
		}
	}
	return applyEdits(text, edits)
}

// defangResult is text of a client's function or of a model as the prompt may carry it: no
// fence tag and no turn marker the model could read in it, and nothing else changed.
func defangResult(text string) string { return neutralise(text, true) }

// rawLineBreaks are the three characters that end a line and that JSON lets through raw
// inside a string (every other one is a control character, which JSON forbids there): they
// are escaped in the arguments of a call, which stay the JSON they were.
var rawLineBreaks = strings.NewReplacer("\u0085", `\u0085`, "\u2028", `\u2028`, "\u2029", `\u2029`)

// argumentsInPrompt is the arguments of a call of the request as the transcript renders
// them, outside any fence. JSON is compact, which leaves no line break in it but the three
// that rawLineBreaks escapes, and is otherwise as the model made it (code goes through the
// arguments of a write or an edit, and a model that reads its own call back with its angle
// brackets and ampersands escaped reads something it never wrote); the fence tags a
// skeleton finds in it are neutralised all the same. Anything else is defanged as a result is.
func argumentsInPrompt(raw json.RawMessage) string {
	text := argumentsText(raw)
	var compact bytes.Buffer
	if json.Compact(&compact, []byte(text)) != nil {
		return defangResult(text)
	}
	return neutralise(rawLineBreaks.Replace(compact.String()), false)
}
