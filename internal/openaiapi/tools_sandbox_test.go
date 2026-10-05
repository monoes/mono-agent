package openaiapi

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// A function the caller declares may be named like a tool of the runtime itself. monomind
// lets the declared one through by its bare name unless the turn has its sandbox (under
// --sandbox only the prefixed name is let through), so a leg that runs without it, with a
// function called Bash, would open claude's own Bash. Every tool leg therefore requires
// the sandbox, whatever the functions are called, and is refused when it cannot be applied.

// fnTool declares a function named name with one string argument.
func fnTool(name string) string {
	return `{"type":"function","function":{"name":"` + name + `","description":"d","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}`
}

// callsFn is a leg whose runtime calls the function name and waits for its result.
func callsFn(name, session string) execFunc {
	return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession(session))
		emit(evCall(name, `{"command":"ls"}`))
		<-ctx.Done()
	})
}

// fnFollowUp is the follow-up of a call of the function name.
func fnFollowUp(name, callID, result string) string {
	return toolChatBody("claude", `"tools":[`+fnTool(name)+`]`, weatherQuestion+
		`,{"role":"assistant","content":null,"tool_calls":[{"id":"`+callID+`","type":"function","function":{"name":"`+name+`","arguments":"{\"command\":\"ls\"}"}}]},`+
		`{"role":"tool","tool_call_id":"`+callID+`","content":`+jsonString(result)+`}`)
}

func TestEveryToolLegRequiresTheSandboxWhateverTheFunctionsAreCalled(t *testing.T) {
	for _, name := range []string{"get_weather", "Bash", "Write", "Read", "Edit"} {
		script := &execScript{turns: []execFunc{callsFn(name, "sess-1"), answers("done"), answers("done again")}}
		h := toolHarness(t, script.exec)
		secret := h.key(t, "default", "app", false)

		rec := post(h, anyPolicy, secret, toolChatBody("claude", `"tools":[`+fnTool(name)+`]`, weatherQuestion))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: first leg: %d %s", name, rec.Code, rec.Body)
		}
		id := decodeToolReply(t, rec).Choices[0].Message.ToolCalls[0].ID
		follow := fnFollowUp(name, id, "ok")
		post(h, anyPolicy, secret, follow) // continues the session
		post(h, anyPolicy, secret, follow) // its record is used up: starts from the transcript

		calls := script.calls()
		if len(calls) != 3 {
			t.Fatalf("%s: want a first leg, a resume and a replay, got %d Exec calls", name, len(calls))
		}
		if calls[1].Resume == "" || calls[2].Resume != "" {
			t.Fatalf("%s: the second leg must resume the session (%q) and the third start from the transcript (%q)", name, calls[1].Resume, calls[2].Resume)
		}
		for i, c := range calls {
			if !c.RequireSandbox || c.Sandbox != monomind.TurnSandboxMode {
				t.Errorf("%s: leg %d runs with RequireSandbox %v and sandbox %q: a function called %s must not open the tool of that name", name, i+1, c.RequireSandbox, c.Sandbox, name)
			}
		}
	}
}

// What Exec answers when the sandbox cannot be applied and is required: the leg ends
// at once, nothing is run, and the answer names no function.
func TestAToolLegWhoseSandboxCannotBeAppliedIsRefusedAndRunsNothing(t *testing.T) {
	const marker = "MARKERsandboxFn"
	for _, stream := range []bool{false, true} {
		var asked atomic.Int32
		h := toolHarness(t, func(_ context.Context, o monomind.ExecOptions, _ func(monomind.Event)) (*monomind.TurnResult, error) {
			asked.Add(1)
			if !o.RequireSandbox {
				t.Error("the leg did not require the sandbox")
			}
			return nil, fmt.Errorf("%w: %s sandbox for %s is %s", monomind.ErrSandboxRequired, o.Sandbox, o.Runtime, monomind.SandboxStatusScoped)
		})
		secret := h.key(t, "default", "app", false)
		extra := `"tools":[` + fnTool(marker) + `]`
		if stream {
			extra += `,"stream":true`
		}
		rec := post(h, anyPolicy, secret, toolChatBody("claude", extra, weatherQuestion))
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusForbidden || e["code"] != "policy_denied" || !strings.Contains(strings.ToLower(msg), "sandbox") || !strings.Contains(strings.ToLower(msg), "tool") {
			t.Errorf("stream %v: %d %v: want a 403 policy_denied that says tool calling needs the sandbox", stream, rec.Code, e)
		}
		if asked.Load() != 1 {
			t.Errorf("stream %v: Exec was asked %d times: a refusal is not retried as a replay", stream, asked.Load())
		}
		if strings.Contains(rec.Body.String(), marker) {
			t.Errorf("stream %v: the answer names the function: %s", stream, rec.Body)
		}
		for _, l := range h.logged() {
			if strings.Contains(l, marker) {
				t.Errorf("stream %v: the log names the function: %s", stream, l)
			}
		}
	}
}

// A model offers tools only where the sandbox its leg needs can be applied: monomind
// has --sandbox and the runtime's scan lists the mode a turn asks for. Otherwise
// the model has no tools capability and a request that declares tools is refused
// before anything starts.
func TestToolsAreOnlyOfferedWhereTheSandboxCanBeApplied(t *testing.T) {
	claudeModes := func(modes []string) func(*Deps, *Config) {
		return func(d *Deps, _ *Config) {
			scan := d.Catalog.Scan
			d.Catalog.Scan = func(ctx context.Context) (*monomind.ScanResult, error) {
				res, err := scan(ctx)
				if err != nil {
					return nil, err
				}
				out := *res
				out.Agents = slices.Clone(res.Agents)
				for i := range out.Agents {
					if out.Agents[i].ID == "claude" {
						out.Agents[i].SandboxModes = modes
					}
				}
				return &out, nil
			}
		}
	}
	noFlag := func(d *Deps, _ *Config) {
		d.Catalog.Caps = func(context.Context) (*monomind.CapabilitySet, error) {
			return monomind.NewCapabilitySet("2.22.0", monomind.CapAgentExecAccessRead), nil
		}
	}
	for _, c := range []struct {
		name   string
		mutate func(*Deps, *Config)
		tools  bool
	}{
		{"monomind 2.22", nil, true},
		{"claude lists the mode among others", claudeModes([]string{"read-only", "workspace-write"}), true},
		{"claude lists only full (monomind 2.19)", claudeModes([]string{"full"}), false},
		{"claude lists read-only but not workspace-write", claudeModes([]string{"read-only", "full"}), false},
		{"the scan lists no modes for claude", claudeModes(nil), false},
		{"the handshake has no agent-exec-sandbox", noFlag, false},
	} {
		var spawned atomic.Int32
		mutate := []func(*Deps, *Config){func(d *Deps, _ *Config) {
			d.Exec = func(ctx context.Context, o monomind.ExecOptions, ev func(monomind.Event)) (*monomind.TurnResult, error) {
				spawned.Add(1)
				return okTurn("x")(ctx, o, ev)
			}
		}}
		if c.mutate != nil {
			mutate = append(mutate, c.mutate)
		}
		h := toolHarness(t, nil, mutate...)
		secret := h.key(t, "default", "app", false)

		if got := slices.Contains(capabilitiesOf(t, h, anyPolicy, secret)["claude/default"], "tools"); got != c.tools {
			t.Errorf("%s: claude has the tools capability: %v, want %v", c.name, got, c.tools)
		}
		rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
		if c.tools {
			continue
		}
		e := decodeErrorBody(t, rec)
		msg, _ := e["message"].(string)
		if rec.Code != http.StatusBadRequest || e["code"] != "unsupported_parameter" || e["param"] != "tools" || !strings.Contains(msg, "sandbox") {
			t.Errorf("%s: %d %v: want a 400 on tools that says why", c.name, rec.Code, e)
		}
		if spawned.Load() != 0 {
			t.Errorf("%s: %d turns started for a request that is refused", c.name, spawned.Load())
		}
	}
}
