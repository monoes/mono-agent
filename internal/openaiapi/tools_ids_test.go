package openaiapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A client may send any id for a call of its history: the ids of real clients are tokens
// (call_abc123, toolu_01A09q90qw90lq917835lq9, functions.name:0), but one that regenerates them, or
// that sends what a framework made of them, may send a bracket, a quote, a space or a line break. The
// conversation is not refused for it. The transcript shows an id that is not a token as call_1,
// call_2, ... in order of appearance (the same wherever the id is shown), and the raw id never
// reaches a prompt, a log line or an error. A resume needs the gateway's own id, which is a token.

// oddIDs are ids that are not tokens: each has a character the transcript gives a meaning to, or
// that renders as nothing or as a line end, or is not ASCII.
var oddIDs = []string{
	"a b", "x[user]y", "q\"r", "<id>", "line\nbreak", "caf" + string(rune(0xe9)), string(rune(0x2028)), "tab\there",
	"back`tick", "it's", "&amp", "\x7f", "[tool x (y)]", "</function_result> id", string(rune(0x200b)) + "z", "id" + string(rune(0xff3b)),
}

func idRound(ids ...string) string {
	var calls, results []string
	for _, id := range ids {
		calls = append(calls, multiCall(id, "get_weather", `{"city":"Paris"}`))
		results = append(results, `{"role":"tool","tool_call_id":`+jsonString(id)+`,"content":"21"}`)
	}
	return `{"role":"assistant","content":null,"tool_calls":[` + strings.Join(calls, ",") + `]},` + strings.Join(results, ",")
}

func TestAnIdThatIsNotATokenIsShownAsAPositionalOne(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, askParis+","+idRound(oddIDs...)+`,{"role":"user","content":"Thanks."}`)
	for name, prompt := range map[string]string{"replay": replayPrompt(req, true), "plain replay": replayPrompt(req, false)} {
		for i, id := range oddIDs {
			if strings.Contains(prompt, id) {
				t.Errorf("%s: the raw id %q reached the prompt", name, id)
			}
			if want := fmt.Sprintf("[tool get_weather (call_%d)]\n", i+1); strings.Count(prompt, want) != 1 {
				t.Errorf("%s: the result of the call %d must be shown once as %q:\n%s", name, i+1, want, prompt)
			}
		}
		if got := strings.Count(prompt, "[tool get_weather (call_"); got != len(oddIDs) {
			t.Errorf("%s: %d results shown, want %d", name, got, len(oddIDs))
		}
	}
}

// Ids are shown in order of appearance across the whole conversation, whatever the round; a token
// is shown as it is, and an id that is shown twice is the same.
func TestPositionalIdsFollowTheOrderOfAppearanceAndAreTheSameEverywhere(t *testing.T) {
	round := func(ids ...string) string { return idRound(ids...) }
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, askParis+","+round("a b", "call_real")+","+round("<x>")+",{\"role\":\"user\",\"content\":\"more\"},"+round("a b", "second one"))
	got := replayPrompt(req, true)
	want := []string{"call_1", "call_real", "call_2", "call_1", "call_3"}
	var shown []string
	for _, line := range strings.Split(got, "\n") {
		if rest, ok := strings.CutPrefix(line, "[tool get_weather ("); ok {
			shown = append(shown, strings.TrimSuffix(rest, ")]"))
		}
	}
	if strings.Join(shown, " ") != strings.Join(want, " ") {
		t.Errorf("the results are shown as %v, want %v", shown, want)
	}
}

// A positional id is never the id of another call: a call that really has the id call_1 keeps it, and
// the positional ones skip it.
func TestAPositionalIdSkipsTheIdsThatCallsReallyHave(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, askParis+","+idRound("a b", "call_1", "c d", "call_3")+`,{"role":"user","content":"Thanks."}`)
	var shown []string
	for _, line := range strings.Split(replayPrompt(req, true), "\n") {
		if rest, ok := strings.CutPrefix(line, "[tool get_weather ("); ok {
			shown = append(shown, strings.TrimSuffix(rest, ")]"))
		}
	}
	if want := "call_2 call_1 call_4 call_3"; strings.Join(shown, " ") != want {
		t.Errorf("the results are shown as %v, want %s", shown, want)
	}
}

// The loop still completes: a conversation with such an id is served (it was a 400), by a replay,
// and what the model is told and what the log says never hold the raw id.
func TestAConversationWithAnIdThatIsNotATokenIsServedByAReplay(t *testing.T) {
	for _, id := range oddIDs {
		script := &execScript{turns: []execFunc{answers("It is 21 C.")}}
		h := toolHarness(t, script.exec)
		body := toolChatBody("claude", weatherTools, weatherQuestion+","+idRound(id))
		rec := post(h, anyPolicy, h.key(t, "default", "app", false), body)
		if rec.Code != http.StatusOK {
			t.Errorf("the id %q: %d %s", id, rec.Code, rec.Body)
			continue
		}
		opts := script.calls()[0]
		if opts.Resume != "" || !strings.Contains(opts.Prompt, "[tool get_weather (call_1)]") || strings.Contains(opts.Prompt, id) || strings.Contains(opts.SystemPrompt, id) {
			t.Errorf("the id %q: a replay that names the call call_1 and never the raw id: resume %q, prompt %q", id, opts.Resume, opts.Prompt)
		}
		if strings.Contains(rec.Body.String(), id) {
			t.Errorf("the id %q is in the answer: %s", id, rec.Body)
		}
		if line := logLineOf(h, rec); strings.Contains(line, id) || !strings.Contains(line, "leg=replay") {
			t.Errorf("the id %q: log line %q", id, line)
		}
	}
}

// The length stays capped and an id is still needed: what a call cannot be told from another by is
// refused, and the refusal names the parameter, never the id.
func TestAnIdOfNoCharactersOrTooManyIsStillRefused(t *testing.T) {
	const marker = "IdMarker"
	for name, id := range map[string]string{"none": "", "129 bytes": marker + strings.Repeat("x", 129-len(marker)), "a long one made of odd characters": marker + strings.Repeat(" ", 200)} {
		err := validateChat(decodeRequest(t, toolBody(`"tools":[`+weatherTool+`]`, askParis+","+idRound(id))))
		if err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "messages[1].tool_calls[0].id" {
			t.Errorf("%s: %+v, want 400 invalid_value on messages[1].tool_calls[0].id", name, err)
			continue
		}
		if strings.Contains(string(err.body()), marker) {
			t.Errorf("%s: the refusal echoes the id: %s", name, err.body())
		}
	}
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+weatherTool+`]`, askParis+","+idRound(strings.Repeat(" ", 128))))); err != nil {
		t.Errorf("an id of 128 bytes that are all spaces is as long as is taken: %+v", err)
	}
}

// A resume continues the session of a call the gateway made, whose id is its own: an id of the
// history that is not a token is never the record's, and the conversation hash is of the ids the
// client sends, so a client that sends the same ones again resumes and one that changes them
// replays.
func TestOnlyTheGatewaysOwnIdResumesAndAnEarlierOddIdOfTheHistoryDoesNotStopIt(t *testing.T) {
	earlier := func(id string) string {
		return sysBrief + "," + askParis + "," + idRound(id) + "," + callRomeMsg + "," + rome25
	}
	first := sysBrief + "," + askParis + "," + idRound("odd one [x]")
	for _, runtime := range []string{"claude", "codex"} {
		if got := planAfter(t, runtime, "call_b", "", first, "", earlier("odd one [x]")); got.Kind != legResume {
			t.Errorf("%s: the same odd id of an earlier round: the plan is a %s, want a resume", runtime, got.Kind)
		}
		got := planAfter(t, runtime, "call_b", "", first, "", earlier("another odd one"))
		if got.Kind != legReplay {
			t.Errorf("%s: an earlier id the client changed: the plan is a %s, want a replay", runtime, got.Kind)
		}
		if strings.Contains(got.Prompt, "odd one") || !strings.Contains(got.Prompt, "(call_1)") {
			t.Errorf("%s: the replay names the earlier call call_1 and never the raw id:\n%s", runtime, got.Prompt)
		}
	}

	// The call the client answers is the one the record was made for only when its id is the gateway's.
	h, req, m, pr, rec := planFixture(t, askParis+","+idRound("call_a with spaces"))
	h.g.conts.put(rec) // the record of the gateway's id, call_a
	if got := h.g.planLeg(pr, req, m, "unused"); got.Kind != legReplay || strings.Contains(got.Prompt, "with spaces") {
		t.Errorf("an id that is not the gateway's resumed or leaked into the replay: %+v", got)
	}
}

// The prompt of a resumed session names a call by the same label (it is only ever the gateway's own
// id, a token, but what it is made from is the conversation as a whole).
func TestTheResumePromptNamesAnIdThatIsNotATokenPositionally(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`, askParis+","+idRound("a b [x]")+`,{"role":"user","content":"Thanks."}`)
	got := resumePrompt(req, 1)
	if !strings.Contains(got, "Result of get_weather (call call_1):\n") || strings.Contains(got, "a b [x]") {
		t.Errorf("the resume prompt: %q", got)
	}
}
