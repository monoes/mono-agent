package service

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// service.openrouter called an LLM over HTTP with its own key; it now fails
// before any request, with the agent.ask migration hint.
func TestOpenRouterNodeFailsFastWithMigrationHint(t *testing.T) {
	for _, op := range []string{"generate_text", "generate_image"} {
		_, err := (&OpenRouterNode{}).Execute(context.Background(), workflow.NodeInput{},
			map[string]interface{}{"api_key": "sk-or-123456", "operation": op, "prompt": "hi"})
		if err == nil || !strings.Contains(err.Error(), "agent.ask") || !strings.Contains(err.Error(), "service.openrouter") {
			t.Errorf("%s: err = %v, want the deprecation hint naming agent.ask", op, err)
		}
	}
	if _, ok := workflow.IsDeprecatedNodeType("service.openrouter"); !ok {
		t.Error("service.openrouter is not listed in workflow.DeprecatedNodeTypes")
	}
}

// service.huggingface keeps image generation but its text generation (LLM
// inference) fails fast, even without a key, pointing at agent.ask.
func TestHuggingFaceTextGenerationRemoved(t *testing.T) {
	_, err := (&HuggingFaceNode{}).Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"operation": "generate_text", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "agent.ask") {
		t.Fatalf("generate_text: err = %v, want the agent.ask hint", err)
	}
	// generate_image is still the real operation: it gets as far as asking
	// for a key.
	_, err = (&HuggingFaceNode{}).Execute(context.Background(), workflow.NodeInput{},
		map[string]interface{}{"operation": "generate_image", "prompt": "a cat"})
	if err == nil || !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("generate_image without a key: err = %v", err)
	}
}
