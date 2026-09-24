// Package monomind is the mono-agent client for monomind's Agent Exec
// Protocol (doc/agent-exec-protocol.md in the monomind repo, v1/rev 5): the
// subprocess contract monoagentcli uses to delegate every AI interaction to
// a locally-installed monomind, which in turn drives the installed agent
// CLIs. mono-agent never learns agent-CLI wire formats — only this protocol.
package monomind

import "encoding/json"

// Protocol version implemented by this client (handshake-checked).
const ProtocolVersion = 1

// MinMonomindVersion is the minimum monomind release this client requires.
const MinMonomindVersion = "2.10.0"

// RequiredCapabilities are the handshake capabilities mono-agent relies on.
var RequiredCapabilities = []string{"agent-exec", "agent-scan", "org-json-v1"}

// Event is one NDJSON event from `monomind agent exec` (protocol §3.2).
// Exactly one Type* constant is set in Type; unknown event types are
// ignored per the spec's forward-compatibility rule.
type Event struct {
	V    int    `json:"v"`
	Type string `json:"type"`

	// start
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Resume  string `json:"resume,omitempty"`
	Pid     int    `json:"pid,omitempty"`
	// StreamsIncrementally (protocol rev 5): whether this runtime delivers
	// real incremental `assistant` text as a turn streams, vs. only ever a
	// complete message at a step/turn boundary. Deliberately no
	// `omitempty` — unlike the other `start` fields, `false` is a real,
	// common, meaningful value here (most runtimes don't stream), and
	// Event has its own MarshalJSON below; `omitempty` on a bool drops the
	// key entirely on `false`, which would silently defeat any caller that
	// re-encodes a decoded Event (see ScanEntry's own comment on the same
	// hazard, which is actually exercised via ScanAgentRuntimes' re-marshal).
	StreamsIncrementally bool `json:"streams_incrementally"`

	// session
	SessionID string `json:"session_id,omitempty"`

	// assistant
	Text string `json:"text,omitempty"`

	// tool_call / tool_result
	ID     string          `json:"id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	OK     *bool           `json:"ok,omitempty"`
	Result *struct {
		Text string `json:"text"`
	} `json:"result,omitempty"`

	// usage / result. Plain scalars (not pointers) so existing Go consumers
	// that compare them directly (e.g. fixtures_test.go's
	// result.CostUSD != 0.0041) keep compiling and working unchanged — the
	// plan is explicit that a metric's presence must be preserved without
	// "changing every scalar to a pointer". Presence is tracked separately
	// in HasInputTokens/HasOutputTokens/HasCostUSD by UnmarshalJSON/
	// MarshalJSON below: a metric reported as exactly 0 is not the same as
	// a metric never reported at all (e.g. cost is genuinely unavailable,
	// not $0).
	InputTokens  int64   `json:"input_tokens,omitempty"`
	OutputTokens int64   `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`

	// HasInputTokens/HasOutputTokens/HasCostUSD are set by UnmarshalJSON to
	// whether the corresponding key was present in the decoded JSON object.
	// Not part of the wire format themselves (see eventJSON).
	HasInputTokens  bool `json:"-"`
	HasOutputTokens bool `json:"-"`
	HasCostUSD      bool `json:"-"`

	// result
	Subtype    string `json:"subtype,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`

	// error
	Code       string `json:"code,omitempty"`
	ErrMessage string `json:"message,omitempty"`
	Fatal      bool   `json:"fatal,omitempty"`

	// done
	ExitCode int `json:"exit_code,omitempty"`
}

// eventJSON mirrors Event's wire shape exactly, except the three optional
// numeric metrics are pointers purely so decoding can detect whether the
// key was present at all. It exists only inside Event's UnmarshalJSON/
// MarshalJSON below — see Event's own field comments for why the public
// struct keeps plain scalars instead of switching to this shape directly.
type eventJSON struct {
	V    int    `json:"v"`
	Type string `json:"type"`

	Runtime              string `json:"runtime,omitempty"`
	Model                string `json:"model,omitempty"`
	Cwd                  string `json:"cwd,omitempty"`
	Resume               string `json:"resume,omitempty"`
	Pid                  int    `json:"pid,omitempty"`
	StreamsIncrementally bool   `json:"streams_incrementally"`

	SessionID string `json:"session_id,omitempty"`

	Text string `json:"text,omitempty"`

	ID     string          `json:"id,omitempty"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	OK     *bool           `json:"ok,omitempty"`
	Result *struct {
		Text string `json:"text"`
	} `json:"result,omitempty"`

	InputTokens  *int64   `json:"input_tokens,omitempty"`
	OutputTokens *int64   `json:"output_tokens,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`

	Subtype    string `json:"subtype,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`

	Code       string `json:"code,omitempty"`
	ErrMessage string `json:"message,omitempty"`
	Fatal      bool   `json:"fatal,omitempty"`

	ExitCode int `json:"exit_code,omitempty"`
}

// UnmarshalJSON decodes the wire event and records, in HasInputTokens/
// HasOutputTokens/HasCostUSD, whether each optional metric key was actually
// present — a plain struct-tag decode into Event directly cannot preserve
// this distinction since a missing key and an explicit 0 both decode to the
// Go zero value.
func (e *Event) UnmarshalJSON(data []byte) error {
	var w eventJSON
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*e = Event{
		V: w.V, Type: w.Type,
		Runtime: w.Runtime, Model: w.Model, Cwd: w.Cwd, Resume: w.Resume, Pid: w.Pid,
		StreamsIncrementally: w.StreamsIncrementally,
		SessionID:            w.SessionID,
		Text:                 w.Text,
		ID:                   w.ID, Name: w.Name, Args: w.Args, OK: w.OK, Result: w.Result,
		Subtype: w.Subtype, IsError: w.IsError, StopReason: w.StopReason,
		Code: w.Code, ErrMessage: w.ErrMessage, Fatal: w.Fatal,
		ExitCode: w.ExitCode,
	}
	if w.InputTokens != nil {
		e.InputTokens = *w.InputTokens
		e.HasInputTokens = true
	}
	if w.OutputTokens != nil {
		e.OutputTokens = *w.OutputTokens
		e.HasOutputTokens = true
	}
	if w.CostUSD != nil {
		e.CostUSD = *w.CostUSD
		e.HasCostUSD = true
	}
	return nil
}

// MarshalJSON re-encodes the event, omitting a metric key entirely when its
// Has* flag is false — so an event that started as "cost never reported"
// serializes back the same way instead of gaining a fabricated 0. Nothing
// in this codebase currently re-serializes a decoded Event (verified: no
// json.Marshal call site takes one), but this keeps decode/encode
// symmetric, matching the plan's "preserve reported metric presence
// through CLI decoding/serialization" requirement.
func (e Event) MarshalJSON() ([]byte, error) {
	w := eventJSON{
		V: e.V, Type: e.Type,
		Runtime: e.Runtime, Model: e.Model, Cwd: e.Cwd, Resume: e.Resume, Pid: e.Pid,
		StreamsIncrementally: e.StreamsIncrementally,
		SessionID:            e.SessionID,
		Text:                 e.Text,
		ID:                   e.ID, Name: e.Name, Args: e.Args, OK: e.OK, Result: e.Result,
		Subtype: e.Subtype, IsError: e.IsError, StopReason: e.StopReason,
		Code: e.Code, ErrMessage: e.ErrMessage, Fatal: e.Fatal,
		ExitCode: e.ExitCode,
	}
	if e.HasInputTokens {
		w.InputTokens = &e.InputTokens
	}
	if e.HasOutputTokens {
		w.OutputTokens = &e.OutputTokens
	}
	if e.HasCostUSD {
		w.CostUSD = &e.CostUSD
	}
	return json.Marshal(w)
}

// Event types (protocol §3.2).
const (
	EventStart      = "start"
	EventSession    = "session"
	EventAssistant  = "assistant"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventUsage      = "usage"
	EventResult     = "result"
	EventError      = "error"
	EventDone       = "done"
)

// Error codes (protocol §3.4). Unknown codes must be treated as non-fatal.
const (
	ErrAuth          = "auth"
	ErrQuota         = "quota"
	ErrMissingBinary = "missing-binary"
	ErrNoRunner      = "no-runner"
	ErrBudget        = "budget"
	ErrRunnerError   = "runner-error"
	ErrTimeout       = "timeout"
	ErrCancelled     = "cancelled"
	ErrBadFrame      = "bad-frame"
)

// Stop reasons (protocol §3.2 result.stop_reason).
const (
	StopEndTurn      = "end_turn"
	StopMaxTurns     = "max_turns"
	StopToolRoundCap = "tool_round_cap"
	StopCancelled    = "cancelled"
	StopTimeout      = "timeout"
)

// ProtocolError is a terminal `error` event from an exec turn.
type ProtocolError struct {
	Code    string
	Message string
	Fatal   bool
	// ExitCode is the process exit code that accompanied the error, when
	// the turn terminated without a success result.
	ExitCode int
}

func (e *ProtocolError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// ToolSpec is a tool definition for --tools-file (protocol §4.1): JSON
// Schema {type:"object", properties, required}.
type ToolSpec struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema,omitempty"`
}

// VersionInfo is the `monomind --version --json` handshake payload (§2).
type VersionInfo struct {
	V            int      `json:"v"`
	Version      string   `json:"version"`
	MinCaller    string   `json:"min_caller"`
	Capabilities []string `json:"capabilities"`
}

// HasCapability reports whether the handshake advertised the capability.
func (vi *VersionInfo) HasCapability(cap string) bool {
	for _, c := range vi.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// ScanEntry is one runtime detection result from `agent scan` (§6).
type ScanEntry struct {
	ID          string  `json:"id"`
	Installed   bool    `json:"installed"`
	Binary      *string `json:"binary"`
	Version     *string `json:"version"`
	InstallHint string  `json:"install_hint"`
	// Install is InstallHint as a recipe a caller can run without a shell
	// (protocol rev 9); nil from an older monomind.
	Install *InstallRecipe `json:"install,omitempty"`
	// LoginHint is the runtime's own sign-in command (rev 9), when any.
	LoginHint *string `json:"login_hint,omitempty"`
	// StreamsIncrementally (protocol rev 5): mirrors Event's own field —
	// see its doc comment for why this deliberately has no `omitempty`.
	// Static per-runtime metadata: unlike Installed/Version it never
	// depends on probing the binary, so it's present even when
	// Installed is false.
	StreamsIncrementally bool `json:"streams_incrementally"`
}

// ScanResult is the `agent scan --json` payload (§6).
type ScanResult struct {
	V      int         `json:"v"`
	Agents []ScanEntry `json:"agents"`
}

// Installed returns only the installed runtimes, ordered as scanned.
func (s *ScanResult) Installed() []ScanEntry {
	out := make([]ScanEntry, 0, len(s.Agents))
	for _, a := range s.Agents {
		if a.Installed {
			out = append(out, a)
		}
	}
	return out
}

// Find returns a runtime by id from the scan result (nil when absent).
func (s *ScanResult) Find(id string) *ScanEntry {
	for i := range s.Agents {
		if s.Agents[i].ID == id {
			return &s.Agents[i]
		}
	}
	return nil
}

// InstallRecipe is `agent scan`'s structured install hint (rev 9):
// kind "npm" (Packages), "script" (URL run with Shell) or "manual".
type InstallRecipe struct {
	Kind     string   `json:"kind"`
	Packages []string `json:"packages,omitempty"`
	URL      string   `json:"url,omitempty"`
	Shell    string   `json:"shell,omitempty"`
}
