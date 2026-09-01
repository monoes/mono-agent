package service

// OpenRouterNodeSchema documents the config keys OpenRouterNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar.
//
// credential_platform: openrouter
type OpenRouterNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=OpenRouter Account,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=generate_text|generate_image,default=generate_text"`

	Prompt string `json:"prompt" schema:"label=Prompt,type=text,required,help=The prompt text. Supports {{ $json.fieldName }} expressions."`

	Model string `json:"model" schema:"label=Model,type=text,help=Default: anthropic/claude-3.5-sonnet for generate_text， black-forest-labs/flux-1.1-pro for generate_image."`

	MaxTokens float64 `json:"max_tokens" schema:"label=Max Tokens,type=number,default=500,depends_on_key=operation,depends_on_values=generate_text"`
}
