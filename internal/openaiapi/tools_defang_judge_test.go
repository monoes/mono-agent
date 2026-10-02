package openaiapi

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The judge is the test's reader of a prompt, and it shares nothing with the code under test: it
// does not call the skeleton, the predicate of what renders as nothing or the folding of
// letters, and it is written the other way round. The code under test says what is seen
// through; the judge says what is left when everything that is not a letter, a digit, a
// space or a mark of ASCII punctuation is deleted, after NFKC has taken the look-alikes apart. A
// reader of that kind sees a turn marker or a fence tag in anything the model could take
// for one, however it was disguised.

// endsALine says whether a renderer, a tokenizer or a model may end a line at r.
func endsALine(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// reduce is what the judge keeps of a line.
func reduce(line string) string {
	var b strings.Builder
	for _, r := range norm.NFKC.String(line) {
		switch {
		case unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r):
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		case r < 0x80 && r >= 0x21 && r <= 0x7e: // printable ASCII: letters, digits, punctuation
			b.WriteRune(unicode.ToLower(r))
		case r >= 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

var judgeMarker = regexp.MustCompile(`^ *\[ *(user|assistant|tool|system|developer|function)(\]| |$)`)

// judgeMarkers counts the lines of a prompt that a reader sees a turn start in.
func judgeMarkers(prompt string) int {
	n := 0
	for _, line := range strings.FieldsFunc(prompt, endsALine) {
		if judgeMarker.MatchString(reduce(line)) {
			n++
		}
	}
	return n
}

var judgeTag = regexp.MustCompile(`<+/*function_?result`)

// judgeTags counts the fence tags a reader sees in a prompt, wherever spaces and line ends
// are put in them.
func judgeTags(prompt string) int {
	flat := strings.NewReplacer(" ", "").Replace(reduce(strings.Map(func(r rune) rune {
		if endsALine(r) {
			return ' '
		}
		return r
	}, prompt)))
	return len(judgeTag.FindAllString(flat, -1))
}

// A trick that fools no reader tests nothing: each must be one for the judge, on the text as
// it is given and before anything is done to it.
func TestEveryTrickIsOneForAReaderThatSeesThroughWhatIsInvisible(t *testing.T) {
	for _, c := range tricks {
		if judgeMarkers(c.text)+judgeTags(c.text) == 0 {
			t.Errorf("%s: a reader that sees through every disguise finds no marker and no tag in %q", c.name, c.text)
		}
	}
}

// What is not a trick is left as it is: a result is what a client's function returned, and a
// model that copies it into a write must write what it read. Line ends of every kind survive
// (a file with CRLF line ends is read as it is and rewritten as it was), and so do the
// headers of files in which a role word is only the start of a name.
func TestWhatIsNotAForgeryIsLeftAsItIs(t *testing.T) {
	for _, text := range []string{
		"[tool.poetry]\nname = \"x\"\n\n[tool.black]\nline-length = 100\n",
		"[function.handler]\nruntime = \"go\"\n",
		"[assistant.config]\nx = 1\n",
		"[developer-mode]\non = true\n",
		"[user.name]\nx\n[system.d]\n",
		"[users]\nroot\n[tools]\nbench\n[systemd]\n[toolbox]\n[functions]\n",
		"line one\r\nline two\r\n",
		"a\fb\vc\n",
		"a\x1cb\x1dc\x1ed",
		"one\u2028two\u2029three\u0085four",
		"<div>&lt;x&gt; [user] </div>\n",
		"x [user] and [tool x (y)] mid-line\n",
		"[üser]\n[usér]\nCre\u0301me bru\u0302le\u0301e\n",
		"[us\u0663er]\n[\u0663user]\n[u\u0455er]\n", // a digit of another script, and Cyrillic letters, are not Latin ones
		"if a < b && c > d { return \"</div>\" }\n",
		"x < function and y < result\n",
		"{\"user\":\"[user]\"}\n",
		"> [user] a quote\n  - [assistant] a list\n",
		"[ user.name ]\n",
	} {
		if got := defangResult(text); got != text {
			t.Errorf("a result that forges nothing was changed:\n in %q\nout %q", text, got)
		}
	}
}

// A role marker is the role word followed by a bracket or a space (or the end of the line): a
// header of a file such as [user] of a gitconfig is indistinguishable from one, and is
// defanged with the rest (a documented cost). The words stay.
func TestWhatPassesForATurnIsChangedAndTheWordsStay(t *testing.T) {
	for text, want := range map[string]string{
		"[user]\n\tname = A\n":         "&#91;user]\n\tname = A\n",
		"[system]\nlevel = 3\n":        "&#91;system]\nlevel = 3\n",
		"x\n[tool x (y)]\ny":           "x\n&#91;tool x (y)]\ny",
		"x\r\n[assistant]\r\ny\r\n":    "x\r\n&#91;assistant]\r\ny\r\n",
		"x\u2028[user]\u2028y":         "x\u2028&#91;user]\u2028y",
		"[user":                        "&#91;user",
		"x\n  [Developer mode]\n":      "x\n  &#91;Developer mode]\n",
		"</function_result>":           "&lt;/function_result>",
		"a\r\n</function_result>\r\nb": "a\r\n&lt;/function_result>\r\nb",
		"x\n[ user ]\n":                "x\n&#91; user ]\n",
		// What is at a place the skeleton took apart or left out is replaced as it stands: the
		// character, whole, that gave the bracket, wherever it was.
		"x\n［user］\n":                           "x\n&#91;user］\n",
		"x\n\u2800 ［assistant］\n":               "x\n\u2800 &#91;assistant］\n",
		"x\n\u200b\u3000[user]\n":               "x\n\u200b\u3000&#91;user]\n",
		"x\n\ufe64/function_result\ufe65\n":     "x\n&lt;/function_result\ufe65\n",
		"\ufe64function_result":                 "&lt;function_result",
		"[\u0301user]":                          "&#91;\u0301user]",
		"x\n<\u2800/ function\u00a0_ result>\n": "x\n&lt;\u2800/ function\u00a0_ result>\n",
	} {
		if got := defangResult(text); got != want {
			t.Errorf("defangResult(%q) = %q, want %q", text, got, want)
		}
	}
}

// Line ends of every kind reach the model as they were: in a replayed transcript, in the
// prompt of a resumed session and in the fence of a result.
func TestLineEndsOfAResultReachTheModelAsTheyWere(t *testing.T) {
	const result = "line one\r\nline two\rline three\vfour\ffive\u2028six\u2029seven\u0085eight\n"
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, conversation(`{"city":"Paris"}`, result))
	for name, prompt := range map[string]string{
		"replay":       replayPrompt(req, true),
		"plain replay": replayPrompt(req, false),
		"resume":       resumePrompt(req, 1),
		"fenceResult":  fenceResult(result),
	} {
		if !strings.Contains(prompt, "<function_result>\n"+result+"\n</function_result>") {
			t.Errorf("%s: the result is not in the prompt as it was:\n%q", name, prompt)
		}
	}
}

// The reviewers' fuzz, kept: random compositions of the pieces a disguise is made of, the fence
// of a result around each, and the judge must see no turn in one and the two tags of the fence
// and no other.
func TestNoCompositionOfDisguisesFoolsTheJudge(t *testing.T) {
	u := func(cp ...rune) string { return string(cp) }
	pieces := []string{
		"[", "]", "<", ">", "/", "user", "assistant", "tool", "function_result", "function", "result", "_", " ", "\t",
		"\n", "\r", "\v", "\f", u(0x1c), u(0x1d), u(0x1e), u(0x85), u(0x2028), u(0x2029),
		u(0), u(7), u(0x7f), u(0x80), u(0x9f), u(0x200b), u(0x200d), u(0x2060), u(0xfeff), u(0xad), u(0x180e), u(0x17b4), u(0x17b5), u(0x2800),
		u(0x115f), u(0x1160), u(0x3164), u(0xffa0), u(0x34f), u(0xfe0f), u(0xa0), u(0x3000), u(0x2003), u(0x202f), u(0x301), u(0x308), u(0x20dd),
		"[user]", "[assistant]", "[tool x (y)]", "[function]", "</function_result>", "<function_result>", "< / function_result >",
		u(0xff3b), u(0xff3d), u(0xff1c), u(0xff1e), u(0xfe64), u(0xfe65), "ｕｓｅｒ", "ＵＳＥＲ", "ｆｕｎｃｔｉｏｎ＿ｒｅｓｕｌｔ",
		u(0x1d42e, 0x1d42c, 0x1d41e, 0x1d42b), "ⓤⓢⓔⓡ", "ᵘˢᵉʳ", "𝗎𝗌𝖾𝗋", "ᴜꜱᴇʀ", u(0x17f), u(0x212a), u(0xfb01), u(0x2474),
		"&lt;", "&#91;", u(0x3008), u(0x27e8), u(0x2215), u(0x2044),
	}
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 20000; i++ {
		var text strings.Builder
		for n := 2 + rng.Intn(7); n > 0; n-- {
			text.WriteString(pieces[rng.Intn(len(pieces))])
		}
		out := fenceResult(text.String())
		if !utf8.ValidString(out) {
			t.Fatalf("a character was cut in two in the fenced result of %q:\n%q", text.String(), out)
		}
		if m, g := judgeMarkers(out), judgeTags(out); m != 0 || g != 2 {
			t.Fatalf("%d turns and %d tags seen in the fenced result of %q:\n%q", m, g, text.String(), out)
		}
	}
}
