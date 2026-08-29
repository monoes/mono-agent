package ai

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// maxSSEBufferSize is the maximum size of a single SSE `data:` line the
// streaming adapters will buffer. The default bufio.Scanner limit is 64KB,
// which some providers (notably Google, which sends whole parts per line)
// exceed on large tool-call payloads, aborting the stream with ErrTooLong.
const maxSSEBufferSize = 10 * 1024 * 1024

// completeRetryDelays is the backoff schedule for non-streaming Complete
// calls: up to two retries with jittered delays. Package var so tests can
// shrink it.
var completeRetryDelays = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

// retryableStatusCode reports whether a response is worth another attempt
// (rate limit or server-side failure).
func retryableStatusCode(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// completeWithRetry issues a non-streaming completion request, retrying on
// 429/5xx per completeRetryDelays. buildReq is invoked fresh per attempt
// (http.Request bodies are single-use). wrapErr, when non-nil, decorates
// transport errors (Google scrubs the API key from them). Streaming paths
// do not use this — partial responses must not be retried blindly.
func completeWithRetry(
	ctx context.Context,
	hc *http.Client,
	provider string,
	buildReq func() (*http.Request, error),
	wrapErr func(error) error,
) ([]byte, error) {
	if wrapErr == nil {
		wrapErr = func(err error) error { return err }
	}
	for attempt := 0; ; attempt++ {
		httpReq, err := buildReq()
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		resp, err := hc.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("do request: %w", wrapErr(err))
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode != http.StatusOK && retryableStatusCode(resp.StatusCode) && attempt < len(completeRetryDelays) {
			if err := sleepBackoff(ctx, completeRetryDelays[attempt]); err != nil {
				return nil, fmt.Errorf("%s: retry aborted: %w", provider, err)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: status %d: %s", provider, resp.StatusCode, string(respBody))
		}
		return respBody, nil
	}
}

// sleepBackoff waits base plus up to 50% jitter, or returns early on ctx
// cancellation.
func sleepBackoff(ctx context.Context, base time.Duration) error {
	d := base
	if base > 0 {
		d += time.Duration(rand.Int63n(int64(base / 2)))
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Role constants
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolDef struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type CompletionResponse struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        Usage      `json:"usage"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type StreamChunk struct {
	Content      string     `json:"content,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Done         bool       `json:"done"`
}

type AIClient interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	StreamComplete(ctx context.Context, req CompletionRequest, onChunk func(StreamChunk)) error
}

// NewClient creates the appropriate AIClient for a given provider config.
func NewClient(provider AIProvider) (AIClient, error) {
	def, ok := GetProviderDef(provider.ProviderID)
	if !ok {
		if provider.Tier == "gateway" {
			return NewOpenAIClient(provider.APIKey, provider.BaseURL, provider.ExtraHeaders), nil
		}
		return nil, fmt.Errorf("unknown provider: %s", provider.ProviderID)
	}
	baseURL := provider.BaseURL
	if baseURL == "" {
		baseURL = def.DefaultBaseURL
	}
	switch def.Adapter {
	case "anthropic":
		return NewAnthropicClient(provider.APIKey, baseURL), nil
	case "google":
		return NewGoogleClient(provider.APIKey, baseURL), nil
	case "bedrock":
		return NewBedrockClient(provider.APIKey, baseURL, provider.ExtraHeaders), nil
	default:
		return NewOpenAIClient(provider.APIKey, baseURL, provider.ExtraHeaders), nil
	}
}
