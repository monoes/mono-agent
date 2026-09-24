package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// keyCheckProviders are the providers whose model list needs a valid key,
// so listing models is a free check that a key works. Elsewhere the list is
// public (OpenRouter), missing (Perplexity) or behind a URL the user fills
// in (Azure, Vertex), and only a completion shows whether the key works.
var keyCheckProviders = map[string]bool{
	"openai": true, "anthropic": true, "google": true, "xai": true,
	"mistral": true, "deepseek": true, "groq": true, "together": true,
}

// VerifyKey checks p's API key with a free request, listing the provider's
// models. ok is false when p has no such check: a provider outside
// keyCheckProviders, or a custom base URL (a proxy may list models without
// a key). The caller then falls back to a completion.
func VerifyKey(ctx context.Context, p AIProvider) (ok bool, err error) {
	def, known := GetProviderDef(p.ProviderID)
	if !known || !keyCheckProviders[p.ProviderID] || (p.BaseURL != "" && p.BaseURL != def.DefaultBaseURL) {
		return false, nil
	}
	client, err := NewClient(p)
	if err != nil {
		return true, err
	}
	switch c := client.(type) {
	case *OpenAIClient:
		return true, c.listModels(ctx)
	case *AnthropicClient:
		return true, c.listModels(ctx)
	case *GoogleClient:
		return true, c.listModels(ctx)
	}
	return false, nil
}

func (c *OpenAIClient) listModels(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	return getModels(c.httpClient, req, "openai")
}

func (c *AnthropicClient) listModels(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models?limit=1", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	return getModels(c.httpClient, req, "anthropic")
}

func (c *GoogleClient) listModels(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/models?pageSize=1&key="+url.QueryEscape(c.apiKey), nil)
	if err != nil {
		return err
	}
	return c.scrubErr(getModels(c.httpClient, req, "google"))
}

// getModels runs a model-list request; any status but 200 is an error
// carrying the status and the start of the body (the provider's reason).
func getModels(hc *http.Client, req *http.Request, provider string) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: list models: %w", provider, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return fmt.Errorf("%s: list models: status %d: %s", provider, resp.StatusCode, body)
}
