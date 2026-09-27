package chat

// ToolDef is one tool definition in the OpenAI function-calling shape. The
// chat and MCP tool sets describe themselves with it; `monoagentcli chat`
// renders it into the JSON Schema tools file monomind's runners read.
type ToolDef struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction is a ToolDef's name, description and JSON Schema parameters.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}
