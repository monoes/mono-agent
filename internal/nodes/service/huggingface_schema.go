package service

// HuggingFaceNodeSchema documents the config keys HuggingFaceNode.Execute
// reads out of its map[string]interface{} config — see
// internal/nodes/control/set_schema.go's doc comment for why this is a
// companion struct rather than the runtime config, and
// internal/tools/schemagen for the tag grammar. It is never constructed or
// used at runtime; this struct exists solely to generate
// internal/workflow/schemas/service.huggingface.json.
//
// credential_platform: huggingface
type HuggingFaceNodeSchema struct {
	CredentialID string `json:"credential_id" schema:"label=HuggingFace Account,type=credential_picker,required"`

	Operation string `json:"operation" schema:"label=Operation,type=select,required,options=generate_image|generate_text,default=generate_image"`

	Prompt string `json:"prompt" schema:"label=Prompt,type=text,required,help=The prompt. Supports {{ $json.fieldName }} expressions."`

	Model string `json:"model" schema:"label=Model,type=text,help=Default: black-forest-labs/FLUX.1-schnell for images， meta-llama/Llama-3.2-3B-Instruct for text."`

	MaxTokens float64 `json:"max_tokens" schema:"label=Max New Tokens,type=number,default=500,depends_on_key=operation,depends_on_values=generate_text"`
}
