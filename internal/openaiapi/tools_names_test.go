package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// monomind puts "mcp__org__" in front of a function's name, and what comes out may have 64
// characters at most, so it can be given a name of 54 at most. Clients name functions by
// what their MCP server and tool are called, which is often longer: a name of 55 to 64
// characters is known to monomind and to the model by an alias, a deterministic one (its
// first 45 characters, an underscore and 8 hex digits of the SHA-256 of the whole name), and
// everything the client sees is in its own name.

const (
	longTool = "github_enterprise_server_pull_request_review_comment" + "_x1234" // 58 characters
	longArgs = `{"q":"x"}`
)

// aliasOf is the alias, worked out here and not by the code under test.
func aliasOf(name string) string {
	sum := sha256.Sum256([]byte(name))
	return name[:45] + "_" + hex.EncodeToString(sum[:])[:8]
}

func declare(name string) string {
	return `{"type":"function","function":{"name":"` + name + `","description":"Does a thing.","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}`
}

// callsTheFirstDeclared is a leg whose runtime calls the function it was given first, by
// the name it was given.
func callsTheFirstDeclared(args string) execFunc {
	return func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
		return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
			emit(evStart(true, "monomind"))
			emit(evSession("sess-1"))
			emit(evCall(opts.Tools[0].Name, args))
			<-ctx.Done()
		})(ctx, opts, onEvent)
	}
}

func TestEveryLongNameIsAliasedToWhatMonomindCanTake(t *testing.T) {
	if len(longTool) != 58 {
		t.Fatalf("the test name has %d characters", len(longTool))
	}
	for n := 54; n <= 64; n++ {
		name := strings.Repeat("n", n)
		req := toolRequest(t, `"tools":[`+declare(name)+`]`, userHi)
		specs := toolSpecs(req.toolDecls)
		want := name
		if n > 54 {
			want = aliasOf(name)
			if len(want) != 54 {
				t.Fatalf("the alias has %d characters", len(want))
			}
		}
		if specs[0].Name != want {
			t.Errorf("a name of %d characters reaches monomind as %q, want %q", n, specs[0].Name, want)
		}
		if len("mcp__org__"+specs[0].Name) > 64 {
			t.Errorf("a name of %d characters gives monomind a name of %d", n, len("mcp__org__"+specs[0].Name))
		}
	}
	err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(strings.Repeat("n", 65))+`]`, userHi)))
	if err == nil || err.Code != "invalid_value" || err.Param != "tools[0].function.name" {
		t.Errorf("a name of 65 characters: %+v, want 400 invalid_value on tools[0].function.name", err)
	}
}

// A name that another name's alias is, whichever is declared first, is refused, and the
// refusal names the parameter and not the name.
func TestAnAliasThatIsAnotherDeclaredNameIsRefused(t *testing.T) {
	long := longTool
	for name, tools := range map[string][2]string{"the long name first": {long, aliasOf(long)}, "the alias first": {aliasOf(long), long}} {
		err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(tools[0])+`,`+declare(tools[1])+`]`, userHi)))
		if err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "tools[1].function.name" {
			t.Errorf("%s: %+v, want 400 invalid_value on tools[1].function.name", name, err)
			continue
		}
		if b := string(err.body()); strings.Contains(b, long) || strings.Contains(b, aliasOf(long)) {
			t.Errorf("%s: the refusal names a function: %s", name, b)
		}
	}
	// Two long names with nothing in common after their first 45 characters are two aliases.
	other := longTool[:57] + "y"
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(longTool)+`,`+declare(other)+`]`, userHi))); err != nil {
		t.Errorf("two long names that share their first 45 characters: %+v", err)
	}
}

// What the client sees is its own name: the response, the record, the transcript of a
// replay and of a resume say the alias to the model (which knows no other) and the name to the client.
func TestALongNameRoundTripsAndTheModelKnowsItByItsAlias(t *testing.T) {
	alias := aliasOf(longTool)
	tools := `"tools":[` + declare(longTool) + `,` + weatherTool + `]`
	followUpBody := func(id string, extra string) string {
		return toolChatBody("claude", tools+extra, weatherQuestion+
			`,{"role":"assistant","content":null,"tool_calls":[{"id":"`+id+`","type":"function","function":{"name":"`+longTool+`","arguments":"{\"q\":\"x\"}"}}]},`+
			`{"role":"tool","tool_call_id":"`+id+`","content":"found it"}`)
	}
	for _, stream := range []bool{false, true} {
		script := &execScript{turns: []execFunc{callsTheFirstDeclared(longArgs), answers("Done.")}}
		h := toolHarness(t, script.exec)
		secret := h.key(t, "default", "app", false)
		extra := ""
		if stream {
			extra = `,"stream":true`
		}
		rec := post(h, anyPolicy, secret, toolChatBody("claude", tools+extra, weatherQuestion))
		if len(script.calls()) == 0 {
			t.Fatalf("stream %v: nothing ran: %d %s", stream, rec.Code, rec.Body)
		}
		first := script.calls()[0]
		if first.Tools[0].Name != alias || first.Tools[1].Name != "get_weather" {
			t.Fatalf("stream %v: monomind was given %q and %q, want the alias and the short name as they are", stream, first.Tools[0].Name, first.Tools[1].Name)
		}
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, longTool) || strings.Contains(body, alias) {
			t.Fatalf("stream %v: the response must carry the name the client declared and no alias: %d %s", stream, rec.Code, body)
		}
		var id string
		if stream {
			data, _ := sseEvents(body)
			for _, c := range decodeRawChunks(t, data) {
				if raw, ok := c.Choices[0].Delta["tool_calls"]; ok && strings.Contains(string(raw), `"id"`) {
					id = firstMatch(`"id":"(call_[a-z0-9]+)"`, string(raw))
				}
			}
		} else {
			id = decodeToolReply(t, rec).Choices[0].Message.ToolCalls[0].ID
		}
		if id == "" {
			t.Fatalf("stream %v: no call id in %s", stream, body)
		}
		if line := logLineOf(h, rec); strings.Contains(line, longTool) || strings.Contains(line, alias) || !strings.Contains(line, " tools=2 leg=first") || strings.Contains(line, "badargs") {
			t.Errorf("stream %v: the log line names a function, lost the tool count or says the call did not match its schema (it does): %q", stream, line)
		}

		// The follow-up resumes: the record holds the name the client uses, and the model hears the alias.
		rec = post(h, anyPolicy, secret, followUpBody(id, ""))
		calls := script.calls()
		if rec.Code != http.StatusOK || len(calls) != 2 || calls[1].Resume != "sess-1" {
			t.Fatalf("stream %v: the follow-up must resume: %d, %d turns: %s", stream, rec.Code, len(calls), rec.Body)
		}
		if !strings.Contains(calls[1].Prompt, "Result of "+alias+" (call "+id+")") || strings.Contains(calls[1].Prompt, longTool) {
			t.Errorf("stream %v: the resumed prompt must name the function the model knows: %q", stream, calls[1].Prompt)
		}

		// A replay: the transcript says the alias too.
		script = &execScript{turns: []execFunc{answers("Done.")}}
		h = toolHarness(t, script.exec)
		rec = post(h, anyPolicy, h.key(t, "default", "app", false), followUpBody("call_elsewhere", ""))
		calls = script.calls()
		if rec.Code != http.StatusOK || len(calls) != 1 || calls[0].Resume != "" {
			t.Fatalf("stream %v: the replay: %d, %d turns: %s", stream, rec.Code, len(calls), rec.Body)
		}
		for _, want := range []string{"(called the function " + alias + " with arguments", "[tool " + alias + " (call_elsewhere)]"} {
			if !strings.Contains(calls[0].Prompt, want) {
				t.Errorf("stream %v: the replay lacks %q:\n%s", stream, want, calls[0].Prompt)
			}
		}
		if strings.Contains(calls[0].Prompt, longTool) {
			t.Errorf("stream %v: the replay names a function the model does not know:\n%s", stream, calls[0].Prompt)
		}
	}
}

func firstMatch(pattern, s string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// tool_choice names the function by the client's name; the line that tells the model to call
// it names the alias.
func TestAToolChoiceThatNamesALongFunctionTellsTheModelItsAlias(t *testing.T) {
	alias := aliasOf(longTool)
	script := &execScript{turns: []execFunc{callsTheFirstDeclared(longArgs)}}
	h := toolHarness(t, script.exec)
	choice := `,"tool_choice":{"type":"function","function":{"name":"` + longTool + `"}}`
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", `"tools":[`+declare(longTool)+`]`+choice, weatherQuestion))
	if rec.Code != http.StatusOK {
		t.Fatalf("a tool_choice that names a function of 58 characters: %d %s", rec.Code, rec.Body)
	}
	sys := script.calls()[0].SystemPrompt
	if !strings.Contains(sys, "You must call the function "+alias+" before you answer") || strings.Contains(sys, longTool) {
		t.Errorf("the system prompt must name the alias: %q", sys)
	}
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(longTool)+`]`+strings.Replace(choice, longTool, alias, 1), userHi))); err == nil || err.Param != "tool_choice.function.name" {
		t.Errorf("the alias is not a name the client declared: %+v", err)
	}
}

// What identifies the declared tools is what the client declared: the hash of a resume does
// not depend on how a name is aliased.
func TestTheHashOfTheDeclaredToolsIsOfTheNamesTheClientDeclared(t *testing.T) {
	asDeclared := toolRequest(t, `"tools":[`+declare(longTool)+`]`, userHi)
	asAliased := toolRequest(t, `"tools":[`+declare(aliasOf(longTool))+`]`, userHi) // the same function under its alias, as another tool
	if toolsHash(asDeclared.toolDecls) == toolsHash(asAliased.toolDecls) {
		t.Error("the hash is of the aliases, not of the names the client declared")
	}
}

func TestAHistoryCallOfAnotherLongNameIsRenderedAsItIs(t *testing.T) {
	other := strings.Repeat("o", 60)
	req := toolRequest(t, `"tools":[`+declare(longTool)+`]`, `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"`+other+`","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"r"}`)
	if got := replayPrompt(req, true); !strings.Contains(got, "(called the function "+other+" with arguments {})") || !strings.Contains(got, "[tool "+other+" (c)]") {
		t.Errorf("a function that was not declared keeps its name: %s", got)
	}
}

// The longest name a client may declare is the constant, not a number written into a pattern: a
// limit that is changed in one place and not in the other would refuse names the aliasing is
// built for, or take names it cannot alias.
func TestTheLongestDeclaredNameIsTheConstant(t *testing.T) {
	name := strings.Repeat("n", maxDeclaredName)
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(name)+`]`, userHi))); err != nil {
		t.Errorf("a name of %d characters: %+v", maxDeclaredName, err)
	}
	err := validateChat(decodeRequest(t, toolBody(`"tools":[`+declare(name+"n")+`]`, userHi)))
	if err == nil || err.Code != "invalid_value" || err.Param != "tools[0].function.name" {
		t.Errorf("a name of %d characters: %+v, want 400 invalid_value on tools[0].function.name", maxDeclaredName+1, err)
	}
	if want := fmt.Sprintf("^[A-Za-z0-9_-]{1,%d}$", maxDeclaredName); toolNameRE.String() != want {
		t.Errorf("the pattern of a name is %s, want %s", toolNameRE, want)
	}
}
