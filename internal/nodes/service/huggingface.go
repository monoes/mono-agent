package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/monoes/mono-agent/internal/workflow"
)

// HuggingFaceNode implements service.huggingface for image generation via
// the HuggingFace Inference API (free tier).
//
// generate_image: POST to /models/{model} with {"inputs": prompt}, response is binary image.
// generate_text:  removed — text generation over HTTP with the node's own
// key is LLM inference, which runs through local agents (agent.ask) now.
// Local agents cannot generate images, so generate_image stays.
type HuggingFaceNode struct{}

// huggingFaceTextRemoved is generate_text's fail-fast error.
var huggingFaceTextRemoved = errors.New(`huggingface: operation generate_text was removed by the local-agent transition — replace it with the "agent.ask" node (local AI agent via monomind; see "monoagentcli ref node agent.ask"); generate_image still works`)

func (n *HuggingFaceNode) Type() string { return "service.huggingface" }

func (n *HuggingFaceNode) Execute(ctx context.Context, input workflow.NodeInput, config map[string]interface{}) ([]workflow.NodeOutput, error) {
	operation := strVal(config, "operation")
	if operation == "" {
		operation = "generate_image"
	}
	if operation == "generate_text" {
		return nil, huggingFaceTextRemoved
	}

	apiKey := strVal(config, "api_key")
	if apiKey == "" {
		return nil, fmt.Errorf("huggingface: api_key is required")
	}

	items := input.Items
	if len(items) == 0 {
		items = []workflow.Item{workflow.NewItem(make(map[string]interface{}))}
	}

	var outputItems []workflow.Item
	for _, item := range items {
		var enriched workflow.Item
		var err error
		switch operation {
		case "generate_image":
			enriched, err = n.generateImage(ctx, apiKey, config, item)
		default:
			return nil, fmt.Errorf("huggingface: unknown operation %q", operation)
		}
		if err != nil {
			return nil, err
		}
		outputItems = append(outputItems, enriched)
	}
	return []workflow.NodeOutput{{Handle: "main", Items: outputItems}}, nil
}

func (n *HuggingFaceNode) generateImage(ctx context.Context, apiKey string, config map[string]interface{}, item workflow.Item) (workflow.Item, error) {
	prompt := strVal(config, "prompt")
	if prompt == "" {
		return item, fmt.Errorf("huggingface generate_image: prompt is required")
	}
	model := strVal(config, "model")
	if model == "" {
		model = "black-forest-labs/FLUX.1-schnell"
	}

	body, _ := json.Marshal(map[string]interface{}{"inputs": prompt})
	url := fmt.Sprintf("https://router.huggingface.co/hf-inference/models/%s", model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return item, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return item, fmt.Errorf("huggingface generate_image: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return item, fmt.Errorf("huggingface: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return item, fmt.Errorf("huggingface generate_image: HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	// Response is raw binary image data — save to temp file.
	f, err := os.CreateTemp("", "monoagent_hf_*.png")
	if err != nil {
		return item, fmt.Errorf("huggingface: create temp file: %w", err)
	}
	filePath := f.Name()
	if _, err := f.Write(respBytes); err != nil {
		f.Close()
		return item, fmt.Errorf("huggingface: write image: %w", err)
	}
	f.Close()

	enriched := copyItem(item)
	enriched.JSON["file_path"] = filePath
	enriched.JSON["url"] = url
	return enriched, nil
}

// copyItem returns item with a shallow copy of its JSON, for a node that
// enriches its input item rather than replacing it.
func copyItem(item workflow.Item) workflow.Item {
	newJSON := make(map[string]interface{}, len(item.JSON)+2)
	for k, v := range item.JSON {
		newJSON[k] = v
	}
	return workflow.Item{JSON: newJSON, Binary: item.Binary}
}
