// Package extension implements a WebSocket-based communication layer between
// the Go monoagentcli-agent and a Chrome Extension. The extension acts as a browser
// bridge, executing DOM commands on real Chrome tabs that already have the
// user's session cookies.
package extension

// Command is sent from Go to the Chrome extension over WebSocket.
type Command struct {
	ID     string                 `json:"id"`
	Type   string                 `json:"type"`
	TabID  int                    `json:"tabId,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// Response is received from the Chrome extension over WebSocket.
type Response struct {
	ID      string      `json:"id"`
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`

	// Type echoes the command type this response belongs to. Optional for
	// a response to a command this process sent (the id already matches
	// it), but the only reliable marker on a message nothing asked for —
	// a capture the extension queued while the backend was down and
	// flushed on reconnect (CLIP-08). See isCaptureResponse.
	Type string `json:"type,omitempty"`
}

// Command type constants.
const (
	// Tab management
	CmdCreateTab = "create_tab"
	CmdCloseTab  = "close_tab"
	CmdNavigate  = "navigate"
	CmdReload    = "reload"
	CmdPageInfo  = "page_info"

	// Element queries
	CmdElement  = "element"
	CmdElements = "elements"
	CmdHas      = "has"

	// Element actions
	CmdClick          = "click"
	CmdInput          = "input"
	CmdText           = "text"
	CmdAttribute      = "attribute"
	CmdHTML           = "html"
	CmdProperty       = "property"
	CmdFocus          = "focus"
	CmdScrollIntoView = "scroll_into_view"
	CmdSetFiles       = "set_files"

	// Page-level input
	CmdScroll        = "scroll"
	CmdKeyboardType  = "keyboard_type"
	CmdKeyboardPress = "keyboard_press"
	CmdInsertText    = "insert_text"

	// JavaScript evaluation
	CmdEval = "eval"

	// Page capture (the browser track's capture envelope; see
	// internal/capture and docs/BROWSER_TRACK_PLAN.md)
	CmdPageCapture = "page_capture"

	// Waiting
	CmdWaitLoad    = "wait_load"
	CmdWaitElement = "wait_element"
	CmdRace        = "race"
)

// HighlightsArtifact is the envelope member carrying a page's saved
// highlights (RCL-04). An ordinary artifact as far as the capture path is
// concerned — it passes capture.ValidArtifactName's charset whitelist, so
// nothing between the extension and the inbox has to know what it is. The
// ingest side reads it by this name; see
// packages/@monomind/cli/src/knowledge/highlights.ts.
const HighlightsArtifact = "highlights.json"
