package ai

// NewBedrockClient creates a Bedrock client.
// EXPERIMENTAL: it wraps the OpenAI-compatible adapter pointed at Bedrock's
// /openai/v1 surface and therefore requires an OpenAI-compatible Bedrock
// proxy; native SigV4 auth is not supported.
func NewBedrockClient(apiKey, baseURL, extraHeaders string) AIClient {
	if baseURL == "" {
		baseURL = "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"
	}
	return NewOpenAIClient(apiKey, baseURL, extraHeaders)
}
