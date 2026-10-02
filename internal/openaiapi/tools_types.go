package openaiapi

import (
	"encoding/json"
	"regexp"
)

// Limits of what a request may declare or carry for tool calling.
const (
	maxTools = 64
	// maxToolName is 54, not the 64 of the runtimes: monomind puts a prefix
	// (mcp__org__) in front of the name before the runtime sees it.
	maxToolName        = 54
	maxToolDescription = 16 << 10
	maxToolSchema      = 64 << 10
	// maxToolResult is the longest text of one tool message.
	maxToolResult = 256 << 10
	// The calls of an assistant message of the request: how many, and how long
	// the id, the name and the arguments of each may be.
	maxHistoryCalls  = 64
	maxCallID        = 128
	maxCallName      = 128
	maxCallArguments = 256 << 10
)

var toolNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,54}$`)

// toolDecl is one function the request declares, validated.
type toolDecl struct {
	Name        string
	Description string
	// Params is the parameters schema as the client sent it, compacted: nil when
	// it sent none.
	Params json.RawMessage
	// Props are the top-level properties of Params, and Required its required
	// list: the only part of a schema monomind keeps.
	Props    map[string]json.RawMessage
	Required []string
}

// The modes of a toolChoice.
const (
	choiceAuto     = "auto"
	choiceNone     = "none"
	choiceRequired = "required"
	choiceFunction = "function"
)

// toolChoice is the request's tool_choice. The zero value is auto.
type toolChoice struct {
	Mode string
	// Name is the function a choiceFunction names.
	Name string
}

// ToolCall is one call of an assistant message of the request.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc is the function of a ToolCall. Arguments is kept raw: it must be
// a JSON string, which validation checks.
type ToolCallFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolsActive reports whether the request declares tools the model may call.
func (r *ChatRequest) toolsActive() bool {
	return len(r.toolDecls) > 0 && r.toolPick.Mode != choiceNone
}

// hasToolHistory reports whether the conversation carries tool calls or their
// results.
func (r *ChatRequest) hasToolHistory() bool {
	for _, m := range r.Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// The shapes of an answer that ends at a tool call. They are apart from
// completion and assistantMessage, which an answer without a call keeps using:
// the content of this message is null when the model said nothing before it.
type toolCompletion struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []toolCompletionChoice `json:"choices"`
}

type toolCompletionChoice struct {
	Index        int         `json:"index"`
	Message      toolMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type toolMessage struct {
	Role      string         `json:"role"`
	Content   *string        `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls"`
}

// wireToolCall is a tool call as a response carries it.
type wireToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

type wireToolFunc struct {
	Name string `json:"name"`
	// Arguments is a string of JSON, as OpenAI's is.
	Arguments string `json:"arguments"`
}

// deltaToolCall is a tool call as a streamed chunk carries it. Index is always
// sent, the id, the type and the name only in the first chunk of a call, and
// the arguments in the pieces after it.
type deltaToolCall struct {
	Index    int            `json:"index"`
	ID       string         `json:"id,omitempty"`
	Type     string         `json:"type,omitempty"`
	Function *deltaToolFunc `json:"function,omitempty"`
}

type deltaToolFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}
