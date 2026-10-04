package openaiapi

import (
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// What is code, documentation or a log is not a forgery, and a model that copies it into an
// exact-match edit must write what it read: none of it may change. This is the other half of the
// judge's test. The judge and the code are written apart, but they say the same grammar of what a
// turn marker and a fence tag are, so a fuzz that looks for what leaks cannot see that the code takes
// too much; a corpus of what is not a marker or a tag can.
var benign = []struct{ name, text string }{
	{"generics of a type called FunctionResult", "async function run(): Promise<FunctionResult> {\n  return await call<FunctionResult>(args);\n}\n" +
		"public CompletableFuture<FunctionResult> invoke(Task<FunctionResult> task) {\n    List<FunctionResult> all = new ArrayList<>();\n    Map<String, FunctionResult> byName = new HashMap<>();\n}\n" +
		"fn run() -> Result<FunctionResult, Error> {\n    let v: Vec<FunctionResult> = Vec::new();\n    Ok(FunctionResult::default())\n}\n" +
		"std::vector<FunctionResult> out;\nstd::optional<functionResult> last;\n"},
	{"comparisons with a variable called functionResult", "for (let i = 0; i < functionResult.length; i++) {\n  if (i < functionResultCount && n <functionresult.size) { total += 1 }\n}\n"},
	{"elements and names that go on after function result", "<FunctionResultList>\n  <FunctionResultItem id=\"1\"/>\n</FunctionResultList>\nfunctionResultHandler(); FunctionResultCache.clear()\n" +
		"</functionResults>\n</functionResult2>\n</functionResult_x>\n</functionResultant>\n</functionResultЖ>\n"},
	{"markdown links and reference definitions that start with a role word", "[User guide](docs/user.md)\n[System requirements](docs/system.md)\n[Tool use](docs/tool-use.md)\n" +
		"[Assistant professor of physics](people/a.md)\n[Developer tools](tools/dev.md)\n[Function reference](ref/f.md)\n[Developer Guide]: https://example.com/dev\n"},
	{"Python comprehensions", "[tool for tool in tools]\n    [user for user in users],\n[system.name for system in systems if system]\n[tool(x) for x in xs]\n"},
	{"the tests of a shell", "[ user = root ]\n[ \"$tool\" = x ] && echo ok\n[[ $system == linux ]]\n"},
	{"INI and TOML sections", "[tool.poetry]\nname = \"x\"\n\n[tool.black]\nline-length = 100\n\n[[tool.uv.index]]\nname = \"pypi\"\n\n[users]\nroot = 0\n\n[system.d]\n[developer-mode]\n[function.handler]\n[assistant.config]\n"},
	{"YAML and JSON sequences", "users: [alice, bob]\nroles:\n  - [user, admin]\n[user, tool]\n[tool, system]\n"},
	{"Go slices and C# attributes", "[]user{{Name: \"a\"}}\n[]tool{{Name: \"x\"}}\n[...]system{}\n[Function(\"ProcessOrder\")]\n[Tool(Name = \"x\")]\n"},
	{"log lines", "[tool.run] started\n[user-service] listening on :8080\n[system-info] ok\n[assistant-api] 200\n[INFO] a user logged in\n[WARN] tool timed out\n"},
	{"headings, wiki links and bracketed words", "## [Unreleased]\n[[User:Alice]]\n[[Tool]]\n[Tool use]\n[Tool use(beta)]\n[tool call(s)]\n[user manual]\n[system tray]\n"},
	{"a marker in the middle of a line, quoted or listed", "see [user] and [tool x (y)] mid-line\n> [user] a quote\n  - [assistant] a list\n"},
	{"tags of other protocols that are not the fence", "<function_calls>\n<invoke name=\"x\">\n</invoke>\n</function_calls>\n<functions>\n<result>ok</result>\n<function>x</function>\n<function-result>\n</functionality>\n"},
	{"bracketed words with a diacritic that are not a role word", "[Café]\n[Über uns]\n[Système de fichiers]\n[usuário]\n[função]\n"},
}

func TestWhatIsCodeDocumentationOrALogIsLeftAsItIs(t *testing.T) {
	for _, c := range benign {
		if got := defangResult(c.text); got != c.text {
			t.Errorf("%s was changed:\n in %q\nout %q", c.name, c.text, got)
		}
		// The judge reads it as innocent too: a judge that saw a turn in it would grade the code by a
		// grammar looser than the code's, and a leak could hide behind that.
		if m, g := judgeMarkers(c.text), judgeTags(c.text); m != 0 || g != 0 {
			t.Errorf("%s: the judge sees %d turns and %d tags in it", c.name, m, g)
		}
		// And it reaches the model as it was, as a result and as the words of an assistant.
		req := toolRequest(t, `"tools":[`+weatherTool+`]`, conversationWithWords(c.text, c.text))
		prompt := replayPrompt(req, true)
		if !strings.Contains(prompt, "<function_result>\n"+c.text+"\n</function_result>") {
			t.Errorf("%s is not in the replay as a result as it was:\n%q", c.name, prompt)
		}
		if !strings.Contains(prompt, "[assistant]\n"+c.text+"\n(called the function") {
			t.Errorf("%s is not in the replay as the words of an assistant as they were:\n%q", c.name, prompt)
		}
	}
}

// diacritics lists every letter a role word is spelled with and every combining mark that makes a
// precomposed letter of it (u and the diaeresis make ü), as the letter, the mark and the letter
// with the mark precomposed.
func diacritics() (list [][3]string) {
	for _, letter := range "acdefilmnoprstuvyACDEFILMNOPRSTUVY" { // some marks compose with a capital only
		for mark := rune(0x300); mark <= 0xffff; mark++ {
			if !unicode.Is(unicode.Mn, mark) {
				continue
			}
			apart := string(letter) + string(mark)
			if composed := norm.NFC.String(apart); composed != apart && len([]rune(composed)) == 1 {
				list = append(list, [3]string{string(letter), string(mark), composed})
			}
		}
	}
	return list
}

// A diacritic does not tell a role word apart: a model reads "üser" as "user", and the same
// letter is spelled as one character (NFC) or as a letter and a mark (NFD) by what writes it, so
// both are the role word. The test is every combining mark that makes a letter of a role word, a
// capital included (a dot above makes the dotted capital I: about thirty in the Unicode of today),
// each in both forms and in every role word that has the letter, in the case of the letter.
func TestADiacriticDoesNotTellARoleWordApart(t *testing.T) {
	list := diacritics()
	marks := map[string]bool{}
	for _, d := range list {
		marks[d[1]] = true
	}
	if len(marks) < 20 {
		t.Fatalf("only %d marks make a precomposed letter of a letter of a role word: the list is empty or Unicode has changed", len(marks))
	}
	n := 0
	for _, d := range list {
		for _, word := range []string{"user", "assistant", "tool", "system", "developer", "function"} {
			if unicode.IsUpper([]rune(d[0])[0]) {
				word = strings.ToUpper(word)
			}
			i := strings.Index(word, d[0])
			if i < 0 {
				continue
			}
			for form, spelled := range map[string]string{
				"a mark apart": word[:i+1] + d[1] + word[i+1:],
				"precomposed":  word[:i] + d[2] + word[i+1:],
			} {
				n++
				text := "x\n[" + spelled + "]\ny"
				if got, want := defangResult(text), "x\n&#91;"+spelled+"]\ny"; got != want {
					t.Errorf("%s, %s (%+q): defangResult = %q, want %q", word, form, spelled, got, want)
				}
				if got := judgeMarkers(text); got != 1 {
					t.Errorf("%s, %s (%+q): the judge sees %d turns, want 1", word, form, spelled, got)
				}
			}
		}
	}
	t.Logf("%d marks, %d spellings", len(marks), n)
	if n < 500 {
		t.Errorf("%d spellings tried, want at least five hundred", n)
	}
}

// A letter that only looks like a Latin one, and is not one with a mark or a case of one (a small
// capital, a Cyrillic "е", a Greek omicron), is not looked through: that takes a table of confusables
// (a decision of the second round, and of the third: the fence is the defence against such a
// disguise, and a word of another script in brackets is not a forgery).
func TestALetterThatOnlyLooksLikeALatinOneIsNotLookedThrough(t *testing.T) {
	for _, word := range []string{
		"ᴜꜱᴇʀ",      // small capitals
		"usеr",      // a Cyrillic е
		"uѕer",      // a Cyrillic ѕ
		"υser",      // a Greek upsilon
		"tοοl",      // Greek omicrons
		"ɑssistant", // a Latin alpha
		"developеr", // a Cyrillic е
		"functіon",  // a Cyrillic і
	} {
		text := "x\n[" + word + "]\ny"
		if got := defangResult(text); got != text {
			t.Errorf("[%s] was changed: %q", word, got)
		}
	}
}
