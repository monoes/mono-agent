// Package recording ingests activity recordings streamed by the browser
// extension (kind:"recording" frames) and stores each one as a capture
// envelope with source "recording" (spec §8.2–§8.3).
package recording

import "github.com/monoes/mono-agent/internal/capture"

// Frame is one extension → Go WebSocket message with kind "recording".
//
//	op "start":    RecordingID, TabID, URL, Title, Goal, StartedAt
//	op "event":    RecordingID, Event
//	op "snapshot": RecordingID, EventID, Name ("dom-<eventId>.html"), Data
//	op "network":  RecordingID, Net
//	op "stop":     RecordingID, Reason ("user"|"tab_closed"|"new_tab"|"panel_closed"|"error")
//
// Go answers every frame that carries an ID with a Response{ID, Success,
// Type:"recording"}.
type Frame struct {
	Kind        string    `json:"kind"` // always "recording"
	ID          string    `json:"id,omitempty"`
	Op          string    `json:"op"`
	RecordingID string    `json:"recordingId"`
	TabID       int       `json:"tabId,omitempty"`
	URL         string    `json:"url,omitempty"`
	Title       string    `json:"title,omitempty"`
	Goal        string    `json:"goal,omitempty"`
	StartedAt   int64     `json:"startedAt,omitempty"` // unix ms
	Profile     string    `json:"profile,omitempty"`
	Event       *Event    `json:"event,omitempty"`
	EventID     string    `json:"eventId,omitempty"`
	Name        string    `json:"name,omitempty"`
	Data        string    `json:"data,omitempty"`
	Net         *NetEntry `json:"net,omitempty"`
	Reason      string    `json:"reason,omitempty"`
}

// Event types.
const (
	EvClick     = "click"
	EvType      = "type"
	EvSelect    = "select_option"
	EvCheck     = "check"
	EvSubmit    = "submit"
	EvPressKey  = "press_key"
	EvNavigate  = "navigate"  // user-initiated (typed URL, reload, back/forward)
	EvNavigated = "navigated" // caused by the previous event (link, form)
	EvUpload    = "upload"
	EvScroll    = "scroll"
	EvExtract   = "extract" // user marked data to extract
	EvParam     = "param"   // user marked a typed field as a run-time input
)

// Event is one recorded user action (one line of events.jsonl).
type Event struct {
	ID       string       `json:"id"`  // "e1", "e2", ...
	Seq      int          `json:"seq"` // 1-based, strictly increasing
	T        int64        `json:"t"`   // ms since recording start
	Type     string       `json:"type"`
	URL      string       `json:"url"`
	Frame    []int        `json:"frame,omitempty"` // iframe index path; empty = top
	Target   *Fingerprint `json:"target,omitempty"`
	Value    string       `json:"value,omitempty"`    // final value (type/select), file name (upload)
	Checked  *bool        `json:"checked,omitempty"`  // check
	Key      string       `json:"key,omitempty"`      // press_key, e.g. "Enter", "Control+a"
	Masked   bool         `json:"masked,omitempty"`   // value withheld (password / card / hidden)
	SecretAs string       `json:"secretAs,omitempty"` // suggested secret input name when masked
	NavCause string       `json:"navCause,omitempty"` // typed|link|form|reload|back_forward|script
	Extract  *ExtractMark `json:"extract,omitempty"`
	Param    *ParamMark   `json:"param,omitempty"`
	ScrollY  int          `json:"scrollY,omitempty"`
	Note     string       `json:"note,omitempty"` // optional user annotation
}

// Fingerprint describes the target element (spec §8.2).
type Fingerprint struct {
	Tag         string `json:"tag"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	TestID      string `json:"testId,omitempty"` // data-testid / data-test / data-qa
	Role        string `json:"role,omitempty"`
	AriaName    string `json:"ariaName,omitempty"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Text        string `json:"text,omitempty"` // trimmed, ≤ 120 chars
	Href        string `json:"href,omitempty"`
	InputType   string `json:"inputType,omitempty"`
	// Autocomplete is the element's autocomplete attribute; ingest treats
	// "cc-*" and the password tokens as sensitive (privacy re-check).
	Autocomplete string      `json:"autocomplete,omitempty"`
	CSS          string      `json:"css,omitempty"`   // structural CSS path
	XPath        string      `json:"xpath,omitempty"` // absolute-ish XPath
	Rect         *Rect       `json:"rect,omitempty"`
	Candidates   []Candidate `json:"candidates"` // ranked, best first
}

type Rect struct {
	X, Y, W, H float64
}

// Candidate is one selector candidate, uniqueness-checked in the live DOM.
type Candidate struct {
	Kind   string  `json:"kind"`            // css | xpath | aria | text
	Value  string  `json:"value,omitempty"` // css/xpath/text
	Role   string  `json:"role,omitempty"`  // aria
	Name   string  `json:"name,omitempty"`  // aria
	Unique bool    `json:"unique"`
	Count  int     `json:"count"` // elements matched at record time
	Score  float64 `json:"score"`
}

// ExtractMark is set on EvExtract.
type ExtractMark struct {
	Field string `json:"field,omitempty"` // user/AI field name
	List  bool   `json:"list"`            // a repeated item pattern
	// For lists: the container of repeated items and the per-item selector,
	// plus this field's selector relative to the item.
	ContainerSelector string   `json:"containerSelector,omitempty"`
	ItemSelector      string   `json:"itemSelector,omitempty"`
	FieldSelector     string   `json:"fieldSelector,omitempty"`
	Attribute         string   `json:"attribute,omitempty"` // "" = text, else href/src/...
	Samples           []string `json:"samples,omitempty"`   // up to 5 example values
}

// ParamMark is set on EvParam: the value of event RefEvent is a run-time input.
type ParamMark struct {
	RefEvent string `json:"refEvent"`
	Name     string `json:"name,omitempty"`
}

// NetEntry is one network summary line (network.jsonl), no bodies.
type NetEntry struct {
	T           int64  `json:"t"`
	Method      string `json:"method"`
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	AfterEvent  string `json:"afterEvent,omitempty"`
}

// Artifact names inside a recording envelope.
const (
	EventsArtifact  = "events.jsonl"
	NetworkArtifact = "network.jsonl"
	// DOM snippets: "dom-<eventId>.html"; screenshots: "shot-<eventId>.png".
	SourceRecording = capture.SourceRecording
)

// Summary is one row of `record list --json`.
type Summary struct {
	ID         string `json:"id"` // envelope directory name
	Dir        string `json:"dir"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Goal       string `json:"goal,omitempty"`
	StartedAt  string `json:"startedAt"` // RFC3339
	Events     int    `json:"events"`
	Complete   bool   `json:"complete"` // a stop frame arrived
	StopReason string `json:"stopReason,omitempty"`
	Automation string `json:"automation,omitempty"` // linked package once saved
	Profile    string `json:"profile,omitempty"`    // profile inbox it lives in ("" = default inbox)
}
