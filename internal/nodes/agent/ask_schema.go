package agent

// AskNodeSchema documents the config keys AskNode.Execute reads out of its
// map[string]interface{} config — see internal/nodes/control.SetNodeSchema's
// doc comment for why this is a companion struct rather than the runtime
// config, and internal/tools/schemagen for the tag grammar.
type AskNodeSchema struct {
	Runtime string `json:"runtime" schema:"label=Agent Runtime,type=text,required,help=Locally-installed agent runtime id — claude， codex， kimi， qwen， … (list: monoagentcli agent scan --installed)."`

	Prompt string `json:"prompt" schema:"label=Prompt,type=textarea,required,rows=4,default={{$json.text}},help=Supports {{$json.field}} placeholders."`

	Model string `json:"model" schema:"label=Model,type=text,help=Falls back to the runtime's default model."`

	SystemPrompt string `json:"system_prompt" schema:"label=System Prompt,type=textarea,rows=3"`

	OutputKey string `json:"output_key" schema:"label=Output Key,type=text,default=agent_response"`

	Timeout float64 `json:"timeout" schema:"label=Timeout (seconds),type=number,default=300"`
}
