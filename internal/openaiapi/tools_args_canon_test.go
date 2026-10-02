package openaiapi

import (
	"strings"
	"testing"
	"time"
)

// A session is continued only when the arguments of the call the client answers, and of the calls
// of its history, are the ones the session was given. A client that stores the history and sends
// it again re-serialises it (Python's json.dumps, Go's json.Marshal, a JavaScript client's
// JSON.stringify), and JSON lets one value be spelled in many ways: the keys in any order, a
// letter as itself or as an escape, "<" as itself or as a < escape, "/" as itself or as \/.
// What the arguments say is what is compared; an edited value is another conversation.
//
// In this file an @ stands for a backslash, so that the escapes of JSON can be read in the
// source of a raw string.

func esc(s string) string { return strings.ReplaceAll(s, "@", "\\") }

func TestArgsHashIsOfTheValueNotOfHowItIsWritten(t *testing.T) {
	same := [][]string{
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`, `{ "b": 2, "a": 1 }`, "{\n  \"b\": 2,\n  \"a\": 1\n}"},
		{esc(`{"q":"ü"}`), esc(`{"q":"@u00fc"}`), esc(`{"q":"@u00FC"}`)},
		{esc(`{"q":"a<b>&c"}`), esc(`{"q":"a@u003cb@u003e@u0026c"}`), esc(`{"q":"a@u003cb>&c"}`), esc(`{"q":"a@u003Cb@u003E@u0026c"}`)},
		{esc(`{"p":"a/b"}`), esc(`{"p":"a@/b"}`)},
		{esc(`{"e":"😀"}`), esc(`{"e":"@ud83d@ude00"}`)},
		{`{"a":{"y":1,"x":[{"k":2,"j":1}]},"b":null}`, `{"b":null,"a":{"x":[{"j":1,"k":2}],"y":1}}`},
		{`{"a":1,"a":2}`, `{"a":2}`},
		{esc(`{"q":"a@nb"}`), esc(`{"q":"a@u000ab"}`)},
		{`{"q":"x"}`, esc(`{"@u0071":"@u0078"}`)},
		{`[{"b":1,"a":2},{"d":3,"c":4}]`, `[{"a":2,"b":1},{"c":4,"d":3}]`},
		{`"text"`, esc(` "te@u0078t" `)},
		{"{}", "", "  ", `{ }`},
		{"not json", "  not json\n"},
	}
	for _, group := range same {
		for _, other := range group[1:] {
			if argsHash(group[0]) != argsHash(other) {
				t.Errorf("%q and %q say the same", group[0], other)
			}
		}
	}
	different := [][2]string{
		{`{"a":1}`, `{"a":2}`}, {`{"a":"x"}`, `{"a":"y"}`}, {`{"a":1}`, `{"b":1}`}, {`{"a":{"b":1}}`, `{"a":{"b":2}}`},
		{`[1,2]`, `[2,1]`}, {`{"a":[1,2]}`, `{"a":[2,1]}`},
		{`{"a":1.0}`, `{"a":1}`}, {`{"a":1e2}`, `{"a":100}`}, // numbers stay as written
		{`{"q":"é"}`, esc(`{"q":"e@u0301"}`)}, // and so does the text: no normalisation of it
		{`{"a":1e400}`, `{"a":2e400}`},        // a number that float64 cannot hold is not read as one
		{`{"a":` + strings.Repeat("9", 400) + `}`, `{"a":` + strings.Repeat("9", 399) + `8}`},
		{"not json", "not  json"}, {"{}", "not json"}, {`{"a":1} x`, `{"a":1}`}, {`{"a":1}{"a":1}`, `{"a":1}`},
		{`{"q":"a<b"}`, `{"q":"a&lt;b"}`},
	}
	for _, pair := range different {
		if argsHash(pair[0]) == argsHash(pair[1]) {
			t.Errorf("%q and %q say different things", pair[0], pair[1])
		}
	}
}

// What the client is sent of the arguments is what the model wrote: the canonical form is for
// the hash alone.
func TestTheArgumentsTheClientGetsAreTheModelsOwnSpelling(t *testing.T) {
	const written = `{"note":"a<b ü/","city":"Paris"}`
	if got := compactArgs([]byte(written)); got != written {
		t.Errorf("compactArgs(%s) = %s", written, got)
	}
}

// The calls of the history, as the client sends them back.
func multiCall(id, name, arguments string) string {
	return `{"id":` + jsonString(id) + `,"type":"function","function":{"name":` + jsonString(name) + `,"arguments":` + jsonString(arguments) + `}}`
}

func multiCallMsg(arguments string) string {
	return `{"role":"assistant","content":null,"tool_calls":[` + multiCall("call_a", "get_weather", arguments) + `]}`
}

// The arguments of the first call of a conversation as the gateway gave them (the model's own,
// compact), and what a client may send back instead.
const given = `{"city":"Paris","units":"c","note":"a<b ü/"}`

var respellings = []struct {
	name, arguments string
	resumes         bool
}{
	{"as the gateway gave them", given, true},
	{"the keys in another order", `{"note":"a<b ü/","units":"c","city":"Paris"}`, true},
	{"a letter as an escape", esc(`{"city":"Paris","units":"c","note":"a<b @u00fc/"}`), true},
	{"an angle bracket as an escape", esc(`{"city":"Paris","units":"c","note":"a@u003cb ü/"}`), true},
	{"a slash as an escape", esc(`{"city":"Paris","units":"c","note":"a<b ü@/"}`), true},
	{"spaced out, as Python writes it", `{"city": "Paris", "units": "c", "note": "a<b ü/"}`, true},
	{"all of them at once", esc("{\n \"units\": \"c\",\n \"note\": \"a@u003cb @u00fc@/\",\n \"city\": \"Par@u0069s\"\n}"), true},
	{"a value edited", `{"city":"Paris","units":"f","note":"a<b ü/"}`, false},
	{"a key added", `{"city":"Paris","units":"c","note":"a<b ü/","extra":1}`, false},
	{"a key dropped", `{"city":"Paris","note":"a<b ü/"}`, false},
	{"a value that only looks the same", `{"city":"Paris","units":"c","note":"a&lt;b ü/"}`, false},
}

// An earlier call of the history that the client respells does not cost a replay in a later round,
// for either runtime: the hash of the conversation holds the arguments of every call in it.
func TestAnEarlierCallWhoseArgumentsTheClientRespellsStillResumes(t *testing.T) {
	first := sysBrief + "," + askParis + "," + multiCallMsg(given) + "," + paris21
	for _, runtime := range []string{"claude", "codex"} {
		for _, c := range respellings {
			follow := sysBrief + "," + askParis + "," + multiCallMsg(c.arguments) + "," + paris21 + "," + callRomeMsg + "," + rome25
			want := legReplay
			if c.resumes {
				want = legResume
			}
			if got := planAfter(t, runtime, "call_b", "", first, "", follow); got.Kind != want {
				t.Errorf("%s, %s: the plan is a %s, want a %s", runtime, c.name, got.Kind, want)
			}
		}
	}
}

// The call the client answers is the one the record was made for when its arguments say what the
// gateway gave, however the client spells them.
func TestTheCallTheClientAnswersIsTheOneTheRecordWasMadeForWhateverItsSpelling(t *testing.T) {
	for _, c := range respellings {
		h, req, m, pr, rec := planFixture(t, sysBrief+","+askParis+","+multiCallMsg(c.arguments)+","+paris21)
		rec.Args = argsHash(given)
		h.g.conts.put(rec)
		want := legReplay
		if c.resumes {
			want = legResume
		}
		if got := h.g.planLeg(pr, req, m, "unused"); got.Kind != want {
			t.Errorf("%s: the plan is a %s, want a %s", c.name, got.Kind, want)
		}
	}
}

// Arguments are as large as a model made them (it may write a file into them): reading them to
// hash them costs a moment, not the request's time.
func TestHashingTheArgumentsOfALargeCallCostsLittle(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"content":"`)
	b.WriteString(esc(strings.Repeat(`line <of> code @u00fc @/ @"x@"@n`, 20000)))
	b.WriteString(`"`)
	for i := range 20000 {
		b.WriteString(`,"k` + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + `":[1,2,{"z":1,"a":2}]`)
	}
	b.WriteString(`}`)
	begin := time.Now()
	for range 3 {
		_ = argsHash(b.String())
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Errorf("hashing %d bytes of arguments three times took %v", b.Len(), took)
	}
}
