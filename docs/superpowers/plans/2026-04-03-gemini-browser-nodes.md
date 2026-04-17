# Gemini Browser Nodes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two browser-automated workflow nodes (`gemini.generate_text`, `gemini.generate_image`) that interact with gemini.google.com using Rod, following the existing 3-tier fallback action JSON pattern.

**Architecture:** GeminiBot adapter (`internal/bot/gemini/bot.go`) with self-registration in PlatformRegistry, two action JSON files under `data/actions/gemini/`, config schemas for Tier 3 fallback, UI schemas, and platform registry entry. Auto-registered via existing `RegisterBrowserNodes`.

**Tech Stack:** Go, Rod (existing), action JSON executor (existing), config schemas (existing).

---

### Task 1: GeminiBot Adapter

**Files:**
- Create: `internal/bot/gemini/bot.go`

- [ ] **Step 1: Create the bot adapter**

Create `internal/bot/gemini/bot.go`:

```go
package gemini

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod"
	botpkg "github.com/monoes/mono-agent/internal/bot"
)

// GeminiBot implements botpkg.BotAdapter for Google Gemini.
type GeminiBot struct{}

func init() {
	botpkg.PlatformRegistry["GEMINI"] = func() botpkg.BotAdapter {
		return &GeminiBot{}
	}
}

func (b *GeminiBot) Platform() string  { return "GEMINI" }
func (b *GeminiBot) LoginURL() string  { return "https://gemini.google.com" }

func (b *GeminiBot) IsLoggedIn(page *rod.Page) (bool, error) {
	// Check for sign-in button — if present, NOT logged in.
	signInSelectors := []string{
		"a[href*='accounts.google.com/ServiceLogin']",
		"a[href*='accounts.google.com/signin']",
		"button[data-signin]",
	}
	for _, sel := range signInSelectors {
		has, _, err := page.Has(sel)
		if err != nil {
			continue
		}
		if has {
			return false, nil
		}
	}

	// Check for prompt input — only appears when logged in.
	promptSelectors := []string{
		"div[contenteditable='true']",
		"rich-textarea",
		"textarea[aria-label*='prompt' i]",
		".ql-editor",
	}
	for _, sel := range promptSelectors {
		has, _, err := page.Has(sel)
		if err != nil {
			continue
		}
		if has {
			return true, nil
		}
	}

	return false, nil
}

func (b *GeminiBot) ResolveURL(input string) string {
	return "https://gemini.google.com"
}

func (b *GeminiBot) ExtractUsername(pageURL string) string {
	return "gemini-user"
}

func (b *GeminiBot) SearchURL(query string) string {
	return ""
}

func (b *GeminiBot) SendMessage(ctx context.Context, page *rod.Page, recipient, message string) error {
	return fmt.Errorf("gemini: SendMessage not supported")
}

func (b *GeminiBot) GetProfileData(ctx context.Context, page *rod.Page, profileURL string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("gemini: GetProfileData not supported")
}
```

- [ ] **Step 2: Add GetMethodByName with all bot methods**

Append to `bot.go`:

```go
func (b *GeminiBot) GetMethodByName(name string) (func(ctx context.Context, args ...interface{}) (interface{}, error), bool) {
	switch name {
	case "find_prompt_input":
		return b.methodFindPromptInput, true
	case "type_prompt":
		return b.methodTypePrompt, true
	case "click_send":
		return b.methodClickSend, true
	case "wait_for_response":
		return b.methodWaitForResponse, true
	case "wait_for_image_response":
		return b.methodWaitForImageResponse, true
	case "extract_text_response":
		return b.methodExtractTextResponse, true
	case "extract_image_response":
		return b.methodExtractImageResponse, true
	case "download_images":
		return b.methodDownloadImages, true
	default:
		return nil, false
	}
}

func (b *GeminiBot) methodFindPromptInput(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("find_prompt_input: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("find_prompt_input: first arg must be *rod.Page")
	}
	selectors := []string{
		"div[contenteditable='true']",
		"rich-textarea",
		"textarea[aria-label*='prompt' i]",
		".ql-editor",
	}
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err == nil && el != nil {
			return map[string]interface{}{"success": true, "selector": sel}, nil
		}
	}
	return nil, fmt.Errorf("find_prompt_input: could not find prompt input")
}

func (b *GeminiBot) methodTypePrompt(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("type_prompt: requires (page, promptText)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("type_prompt: first arg must be *rod.Page")
	}
	prompt, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("type_prompt: second arg must be string")
	}
	// Find and focus the input
	selectors := []string{
		"div[contenteditable='true']",
		"rich-textarea",
		"textarea[aria-label*='prompt' i]",
	}
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err == nil && el != nil {
			_ = el.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(300 * time.Millisecond)
			// Type with small delays to mimic human input
			for _, ch := range prompt {
				_ = page.Keyboard.Type(input.Key(ch))
				time.Sleep(30 * time.Millisecond)
			}
			return map[string]interface{}{"success": true, "typed": len(prompt)}, nil
		}
	}
	return nil, fmt.Errorf("type_prompt: could not find input to type into")
}

func (b *GeminiBot) methodClickSend(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("click_send: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("click_send: first arg must be *rod.Page")
	}
	selectors := []string{
		"button[aria-label*='Send' i]",
		"button[data-testid='send-button']",
		"button .send-button-icon",
		"mat-icon[data-mat-icon-name='send']",
	}
	for _, sel := range selectors {
		el, err := page.Timeout(5 * time.Second).Element(sel)
		if err == nil && el != nil {
			_ = el.Click(proto.InputMouseButtonLeft, 1)
			return map[string]interface{}{"success": true}, nil
		}
	}
	// Fallback: press Enter
	_ = page.Keyboard.Press(input.Enter)
	return map[string]interface{}{"success": true, "method": "enter_key"}, nil
}

func (b *GeminiBot) methodWaitForResponse(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("wait_for_response: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("wait_for_response: first arg must be *rod.Page")
	}
	maxWait := 60
	if len(args) >= 2 {
		if v, ok := args[1].(string); ok {
			fmt.Sscanf(v, "%d", &maxWait)
		} else if v, ok := args[1].(float64); ok {
			maxWait = int(v)
		}
	}

	deadline := time.Now().Add(time.Duration(maxWait) * time.Second)
	responseSelectors := []string{
		".response-container .markdown-content",
		"message-content .model-response-text",
		".response-container model-response",
		"div[data-content-type='text']",
	}

	for time.Now().Before(deadline) {
		// Check if still loading
		loading := false
		loadSelectors := []string{".loading-indicator", "[aria-busy='true']", ".generating-animation"}
		for _, sel := range loadSelectors {
			has, _, _ := page.Has(sel)
			if has {
				loading = true
				break
			}
		}
		if !loading {
			// Check if response text appeared
			for _, sel := range responseSelectors {
				el, err := page.Timeout(1 * time.Second).Element(sel)
				if err == nil && el != nil {
					text, _ := el.Text()
					if strings.TrimSpace(text) != "" {
						return map[string]interface{}{"success": true, "ready": true}, nil
					}
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("wait_for_response: timed out after %ds", maxWait)
}

func (b *GeminiBot) methodWaitForImageResponse(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("wait_for_image_response: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("wait_for_image_response: first arg must be *rod.Page")
	}
	maxWait := 120
	if len(args) >= 2 {
		if v, ok := args[1].(string); ok {
			fmt.Sscanf(v, "%d", &maxWait)
		} else if v, ok := args[1].(float64); ok {
			maxWait = int(v)
		}
	}

	deadline := time.Now().Add(time.Duration(maxWait) * time.Second)
	imageSelectors := []string{
		".response-container img[src*='blob:']",
		".generated-image img",
		"img[data-image-id]",
	}

	for time.Now().Before(deadline) {
		for _, sel := range imageSelectors {
			has, _, _ := page.Has(sel)
			if has {
				// Wait a bit for all images to render
				time.Sleep(3 * time.Second)
				return map[string]interface{}{"success": true, "ready": true}, nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return nil, fmt.Errorf("wait_for_image_response: timed out after %ds", maxWait)
}

func (b *GeminiBot) methodExtractTextResponse(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("extract_text_response: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("extract_text_response: first arg must be *rod.Page")
	}
	selectors := []string{
		".response-container .markdown-content",
		"message-content .model-response-text",
		"div[data-content-type='text']",
		".response-container model-response",
	}
	for _, sel := range selectors {
		els, err := page.Elements(sel)
		if err != nil || len(els) == 0 {
			continue
		}
		// Get the last response (most recent)
		el := els[len(els)-1]
		text, _ := el.Text()
		text = strings.TrimSpace(text)
		if text != "" {
			return map[string]interface{}{"success": true, "response_text": text}, nil
		}
	}
	return nil, fmt.Errorf("extract_text_response: no response text found")
}

func (b *GeminiBot) methodExtractImageResponse(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("extract_image_response: requires (page)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("extract_image_response: first arg must be *rod.Page")
	}
	selectors := []string{
		".response-container img[src*='blob:']",
		".generated-image img",
		"img[data-image-id]",
		".response-container img:not([alt='avatar']):not([width='24'])",
	}
	var urls []string
	for _, sel := range selectors {
		els, err := page.Elements(sel)
		if err != nil || len(els) == 0 {
			continue
		}
		for _, el := range els {
			src, err := el.Attribute("src")
			if err == nil && src != nil && *src != "" {
				urls = append(urls, *src)
			}
		}
		if len(urls) > 0 {
			break
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("extract_image_response: no images found")
	}
	return map[string]interface{}{"success": true, "image_urls": urls}, nil
}

func (b *GeminiBot) methodDownloadImages(ctx context.Context, args ...interface{}) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("download_images: requires (page, imageUrls)")
	}
	page, ok := args[0].(*rod.Page)
	if !ok {
		return nil, fmt.Errorf("download_images: first arg must be *rod.Page")
	}

	// Parse image URLs from args
	var imageURLs []string
	switch v := args[1].(type) {
	case []string:
		imageURLs = v
	case []interface{}:
		for _, u := range v {
			if s, ok := u.(string); ok {
				imageURLs = append(imageURLs, s)
			}
		}
	case map[string]interface{}:
		if urls, ok := v["image_urls"].([]interface{}); ok {
			for _, u := range urls {
				if s, ok := u.(string); ok {
					imageURLs = append(imageURLs, s)
				}
			}
		}
	}

	downloadDir := filepath.Join(os.Getenv("HOME"), ".monoes", "downloads")
	if len(args) >= 3 {
		if dir, ok := args[2].(string); ok && dir != "" {
			downloadDir = dir
		}
	}
	_ = os.MkdirAll(downloadDir, 0700)

	timestamp := time.Now().Unix()
	var downloaded []map[string]interface{}

	for i, imgURL := range imageURLs {
		filename := fmt.Sprintf("gemini_%d_%d.png", timestamp, i)
		filePath := filepath.Join(downloadDir, filename)

		// For blob: URLs, use page.Evaluate to fetch as base64
		var data []byte
		if strings.HasPrefix(imgURL, "blob:") {
			b64, err := page.Eval(`(url) => {
				return fetch(url)
					.then(r => r.blob())
					.then(b => new Promise((resolve, reject) => {
						const reader = new FileReader();
						reader.onloadend = () => resolve(reader.result.split(',')[1]);
						reader.onerror = reject;
						reader.readAsDataURL(b);
					}));
			}`, imgURL)
			if err != nil {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(b64.Value.Str())
			if err != nil {
				continue
			}
			data = decoded
		} else {
			// For regular URLs, download directly
			b64, err := page.Eval(`(url) => {
				return fetch(url)
					.then(r => r.blob())
					.then(b => new Promise((resolve, reject) => {
						const reader = new FileReader();
						reader.onloadend = () => resolve(reader.result.split(',')[1]);
						reader.onerror = reject;
						reader.readAsDataURL(b);
					}));
			}`, imgURL)
			if err != nil {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(b64.Value.Str())
			if err != nil {
				continue
			}
			data = decoded
		}

		if err := os.WriteFile(filePath, data, 0600); err != nil {
			continue
		}

		downloaded = append(downloaded, map[string]interface{}{
			"path":       filePath,
			"filename":   filename,
			"size_bytes": len(data),
		})
	}

	if len(downloaded) == 0 {
		return nil, fmt.Errorf("download_images: failed to download any images")
	}

	return map[string]interface{}{
		"success":     true,
		"images":      downloaded,
		"image_count": len(downloaded),
	}, nil
}
```

- [ ] **Step 3: Add required import for proto**

Add `"github.com/go-rod/rod/lib/proto"` and `"github.com/go-rod/rod/lib/input"` to the import block.

- [ ] **Step 4: Verify build**

Run: `go build ./internal/bot/gemini/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bot/gemini/bot.go
git commit -m "feat(gemini): add GeminiBot adapter with browser automation methods"
```

---

### Task 2: Platform Registration + Config Schemas

**Files:**
- Modify: `internal/connections/registry.go`
- Modify: `internal/config/schemas.go`

- [ ] **Step 1: Add gemini to connection registry**

In `internal/connections/registry.go`, find the platform map (around the `"google_drive"` entry) and add:

```go
"gemini": {
	ID:         "gemini",
	Name:       "Gemini",
	Category:   "social",
	ConnectVia: "UI",
	Methods:    []AuthMethod{MethodBrowser},
	Fields:     map[AuthMethod][]CredentialField{},
	IconEmoji:  "✨",
},
```

- [ ] **Step 2: Add Tier 3 config schemas**

In `internal/config/schemas.go`, add before the `schemas` map:

```go
var geminiGenerateTextSchema = schema("gemini_text", "Gemini text generation elements",
	field("prompt_input", "Prompt input textarea or contenteditable"),
	field("send_button", "Send/submit button"),
	field("loading_indicator", "Loading/generating spinner"),
	field("response_container", "Response message container"),
	field("response_text", "Text content within the response"),
)

var geminiGenerateImageSchema = schema("gemini_image", "Gemini image generation elements",
	field("prompt_input", "Prompt input textarea or contenteditable"),
	field("send_button", "Send/submit button"),
	field("loading_indicator", "Loading/generating spinner"),
	field("response_image", "Generated image element(s)"),
)
```

Then add to the `schemas` map:

```go
// Gemini
"GEMINI_GENERATE_TEXT":  geminiGenerateTextSchema,
"GEMINI_GENERATE_IMAGE": geminiGenerateImageSchema,
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/connections/registry.go internal/config/schemas.go
git commit -m "feat(gemini): register platform + add Tier 3 config schemas"
```

---

### Task 3: Action JSONs

**Files:**
- Create: `data/actions/gemini/generate_text.json`
- Create: `data/actions/gemini/generate_image.json`

- [ ] **Step 1: Create generate_text.json**

Create directory and file `data/actions/gemini/generate_text.json`:

```json
{
  "actionType": "generate_text",
  "platform": "GEMINI",
  "version": "1.0.0",
  "description": "Send a prompt to Gemini and extract the text response (3-tier fallback)",
  "metadata": {
    "requiresAuth": true,
    "supportsPagination": false,
    "supportsRetry": true
  },
  "inputs": {
    "required": [
      { "name": "prompt", "type": "string", "description": "The text prompt to send to Gemini" }
    ],
    "optional": [
      { "name": "maxWaitSeconds", "type": "number", "default": 60, "min": 10, "max": 300, "description": "Max seconds to wait for response" }
    ]
  },
  "outputs": {
    "success": ["response_text", "generation_time_ms"],
    "failure": ["error"]
  },
  "steps": [
    {
      "id": "navigate",
      "type": "navigate",
      "url": "https://gemini.google.com",
      "timeout": 15
    },
    {
      "id": "t1_find_input",
      "type": "call_bot_method",
      "methodName": "find_prompt_input",
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_input",
      "type": "condition",
      "condition": { "variable": "promptInput", "operator": "not_exists" },
      "then": ["t2_find_input", "check_t2_input"]
    },
    {
      "id": "t2_find_input",
      "type": "find_element",
      "selector": "div[contenteditable='true']",
      "alternatives": ["rich-textarea", "textarea[aria-label*='prompt' i]", ".ql-editor"],
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_input",
      "type": "condition",
      "condition": { "variable": "promptInput", "operator": "not_exists" },
      "then": ["t3_find_input"]
    },
    {
      "id": "t3_find_input",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_TEXT.prompt_input",
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_type_prompt",
      "type": "call_bot_method",
      "methodName": "type_prompt",
      "args": ["{{prompt}}"],
      "variable_name": "typeResult",
      "timeout": 15,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_type",
      "type": "condition",
      "condition": { "variable": "typeResult", "operator": "not_exists" },
      "then": ["t2_type_prompt"]
    },
    {
      "id": "t2_type_prompt",
      "type": "type_text",
      "target": "{{promptInput}}",
      "text": "{{prompt}}",
      "humanize": true,
      "timeout": 15,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_click_send",
      "type": "call_bot_method",
      "methodName": "click_send",
      "variable_name": "sendResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_send",
      "type": "condition",
      "condition": { "variable": "sendResult", "operator": "not_exists" },
      "then": ["t2_click_send", "check_t2_send"]
    },
    {
      "id": "t2_click_send",
      "type": "find_element",
      "selector": "button[aria-label*='Send' i]",
      "alternatives": ["button[data-testid='send-button']", "button .send-button-icon"],
      "action": "click",
      "variable_name": "sendResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_send",
      "type": "condition",
      "condition": { "variable": "sendResult", "operator": "not_exists" },
      "then": ["t3_click_send"]
    },
    {
      "id": "t3_click_send",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_TEXT.send_button",
      "action": "click",
      "timeout": 10,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_wait_response",
      "type": "call_bot_method",
      "methodName": "wait_for_response",
      "args": ["{{maxWaitSeconds or 60}}"],
      "variable_name": "responseReady",
      "timeout": 90,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_wait",
      "type": "condition",
      "condition": { "variable": "responseReady", "operator": "not_exists" },
      "then": ["t2_wait_response"]
    },
    {
      "id": "t2_wait_response",
      "type": "wait",
      "duration": "{{maxWaitSeconds or 60}}",
      "condition": "element_visible",
      "selector": ".response-container .markdown-content",
      "alternatives": ["message-content .model-response-text"],
      "pollInterval": 2000,
      "timeout": 90,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_extract_text",
      "type": "call_bot_method",
      "methodName": "extract_text_response",
      "variable_name": "extractResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_extract",
      "type": "condition",
      "condition": { "variable": "extractResult", "operator": "not_exists" },
      "then": ["t2_extract_text", "check_t2_extract"]
    },
    {
      "id": "t2_extract_text",
      "type": "find_element",
      "selector": ".response-container .markdown-content",
      "alternatives": ["message-content .model-response-text", "div[data-content-type='text']"],
      "extract": "text",
      "variable_name": "extractResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_extract",
      "type": "condition",
      "condition": { "variable": "extractResult", "operator": "not_exists" },
      "then": ["t3_extract_text"]
    },
    {
      "id": "t3_extract_text",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_TEXT.response_text",
      "extract": "text",
      "variable_name": "extractResult",
      "timeout": 10,
      "onError": { "action": "mark_failed" }
    }
  ],
  "errorHandling": {
    "globalRetries": 1,
    "retryDelay": 3000,
    "onFinalFailure": "log_and_continue"
  }
}
```

- [ ] **Step 2: Create generate_image.json**

Create `data/actions/gemini/generate_image.json`:

```json
{
  "actionType": "generate_image",
  "platform": "GEMINI",
  "version": "1.0.0",
  "description": "Send a prompt to Gemini and download generated images (3-tier fallback)",
  "metadata": {
    "requiresAuth": true,
    "supportsPagination": false,
    "supportsRetry": true
  },
  "inputs": {
    "required": [
      { "name": "prompt", "type": "string", "description": "Image generation prompt" }
    ],
    "optional": [
      { "name": "maxWaitSeconds", "type": "number", "default": 120, "min": 30, "max": 600, "description": "Max seconds to wait for image generation" },
      { "name": "downloadDir", "type": "string", "default": "~/.monoes/downloads", "description": "Directory to save generated images" }
    ]
  },
  "outputs": {
    "success": ["images", "image_count", "generation_time_ms"],
    "failure": ["error"]
  },
  "steps": [
    {
      "id": "navigate",
      "type": "navigate",
      "url": "https://gemini.google.com",
      "timeout": 15
    },
    {
      "id": "t1_find_input",
      "type": "call_bot_method",
      "methodName": "find_prompt_input",
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_input",
      "type": "condition",
      "condition": { "variable": "promptInput", "operator": "not_exists" },
      "then": ["t2_find_input", "check_t2_input"]
    },
    {
      "id": "t2_find_input",
      "type": "find_element",
      "selector": "div[contenteditable='true']",
      "alternatives": ["rich-textarea", "textarea[aria-label*='prompt' i]", ".ql-editor"],
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_input",
      "type": "condition",
      "condition": { "variable": "promptInput", "operator": "not_exists" },
      "then": ["t3_find_input"]
    },
    {
      "id": "t3_find_input",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_IMAGE.prompt_input",
      "variable_name": "promptInput",
      "timeout": 10,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_type_prompt",
      "type": "call_bot_method",
      "methodName": "type_prompt",
      "args": ["{{prompt}}"],
      "variable_name": "typeResult",
      "timeout": 15,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_type",
      "type": "condition",
      "condition": { "variable": "typeResult", "operator": "not_exists" },
      "then": ["t2_type_prompt"]
    },
    {
      "id": "t2_type_prompt",
      "type": "type_text",
      "target": "{{promptInput}}",
      "text": "{{prompt}}",
      "humanize": true,
      "timeout": 15,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_click_send",
      "type": "call_bot_method",
      "methodName": "click_send",
      "variable_name": "sendResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_send",
      "type": "condition",
      "condition": { "variable": "sendResult", "operator": "not_exists" },
      "then": ["t2_click_send", "check_t2_send"]
    },
    {
      "id": "t2_click_send",
      "type": "find_element",
      "selector": "button[aria-label*='Send' i]",
      "alternatives": ["button[data-testid='send-button']", "button .send-button-icon"],
      "action": "click",
      "variable_name": "sendResult",
      "timeout": 10,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_send",
      "type": "condition",
      "condition": { "variable": "sendResult", "operator": "not_exists" },
      "then": ["t3_click_send"]
    },
    {
      "id": "t3_click_send",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_IMAGE.send_button",
      "action": "click",
      "timeout": 10,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_wait_images",
      "type": "call_bot_method",
      "methodName": "wait_for_image_response",
      "args": ["{{maxWaitSeconds or 120}}"],
      "variable_name": "imagesReady",
      "timeout": 150,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_wait",
      "type": "condition",
      "condition": { "variable": "imagesReady", "operator": "not_exists" },
      "then": ["t2_wait_images"]
    },
    {
      "id": "t2_wait_images",
      "type": "wait",
      "duration": "{{maxWaitSeconds or 120}}",
      "condition": "element_visible",
      "selector": ".response-container img[src*='blob:']",
      "alternatives": [".generated-image img", "img[data-image-id]"],
      "pollInterval": 3000,
      "timeout": 150,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "t1_extract_images",
      "type": "call_bot_method",
      "methodName": "extract_image_response",
      "variable_name": "imageUrls",
      "timeout": 15,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t1_extract",
      "type": "condition",
      "condition": { "variable": "imageUrls", "operator": "not_exists" },
      "then": ["t2_extract_images", "check_t2_extract"]
    },
    {
      "id": "t2_extract_images",
      "type": "find_element",
      "selector": ".response-container img[src*='blob:']",
      "alternatives": [".generated-image img", "img[data-image-id]"],
      "extract": "attribute:src",
      "multiple": true,
      "variable_name": "imageUrls",
      "timeout": 15,
      "onError": { "action": "skip" }
    },
    {
      "id": "check_t2_extract",
      "type": "condition",
      "condition": { "variable": "imageUrls", "operator": "not_exists" },
      "then": ["t3_extract_images"]
    },
    {
      "id": "t3_extract_images",
      "type": "find_element",
      "configKey": "GEMINI_GENERATE_IMAGE.response_image",
      "extract": "attribute:src",
      "multiple": true,
      "variable_name": "imageUrls",
      "timeout": 15,
      "onError": { "action": "mark_failed" }
    },
    {
      "id": "download_images",
      "type": "call_bot_method",
      "methodName": "download_images",
      "args": ["{{imageUrls}}", "{{downloadDir or ~/.monoes/downloads}}"],
      "variable_name": "downloadedImages",
      "timeout": 30,
      "onError": { "action": "mark_failed" }
    }
  ],
  "errorHandling": {
    "globalRetries": 1,
    "retryDelay": 5000,
    "onFinalFailure": "log_and_continue"
  }
}
```

- [ ] **Step 3: Verify the actions are embedded**

Run: `go build ./...`
Expected: PASS (the `data/embed.go` uses `go:embed` to include all files under `data/actions/`)

- [ ] **Step 4: Commit**

```bash
git add data/actions/gemini/generate_text.json data/actions/gemini/generate_image.json
git commit -m "feat(gemini): add action JSONs with 3-tier fallback (generate_text + generate_image)"
```

---

### Task 4: UI Schemas + Palette Entry

**Files:**
- Create: `internal/workflow/schemas/gemini.generate_text.json`
- Create: `internal/workflow/schemas/gemini.generate_image.json`
- Modify: `wails-app/app.go` (GetWorkflowNodeTypes)

- [ ] **Step 1: Create UI schema for generate_text**

Create `internal/workflow/schemas/gemini.generate_text.json`:

```json
{
  "credential_platform": "gemini",
  "fields": [
    {"key": "prompt", "label": "Prompt", "type": "textarea", "required": true, "rows": 4, "placeholder": "Ask Gemini anything..."},
    {"key": "maxWaitSeconds", "label": "Max Wait (seconds)", "type": "number", "default": 60, "help": "Maximum time to wait for Gemini to respond."}
  ]
}
```

- [ ] **Step 2: Create UI schema for generate_image**

Create `internal/workflow/schemas/gemini.generate_image.json`:

```json
{
  "credential_platform": "gemini",
  "fields": [
    {"key": "prompt", "label": "Prompt", "type": "textarea", "required": true, "rows": 4, "placeholder": "Describe the image to generate..."},
    {"key": "maxWaitSeconds", "label": "Max Wait (seconds)", "type": "number", "default": 120, "help": "Maximum time to wait for image generation."},
    {"key": "downloadDir", "label": "Download Directory", "type": "text", "default": "~/.monoes/downloads", "help": "Where to save generated images."}
  ]
}
```

- [ ] **Step 3: Add to GetWorkflowNodeTypes**

In `wails-app/app.go`, find the `"browser"` category in `GetWorkflowNodeTypes`. Add:

```go
mkNode("gemini.generate_text", "Gemini Text", "browser", "Send a prompt to Gemini and get a text response"),
mkNode("gemini.generate_image", "Gemini Image", "browser", "Send a prompt to Gemini and download generated images"),
```

- [ ] **Step 4: Add Gemini bot import to app.go**

In `wails-app/app.go`, add to the blank imports:

```go
_ "github.com/monoes/mono-agent/internal/bot/gemini"
```

- [ ] **Step 5: Add Gemini bot import to cmd/monoes/node.go**

In `cmd/monoes/node.go`, find the existing bot imports and add:

```go
_ "github.com/monoes/mono-agent/internal/bot/gemini"
```

- [ ] **Step 6: Verify full build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/workflow/schemas/gemini.generate_text.json \
       internal/workflow/schemas/gemini.generate_image.json \
       wails-app/app.go \
       cmd/monoes/node.go
git commit -m "feat(gemini): UI schemas, palette entries, bot imports"
```

---

### Task 5: Build + Push

**Files:** None new.

- [ ] **Step 1: Full build and install**

```bash
make build && cp bin/monoes ~/go/bin/monoes
~/go/bin/monoes version
```

- [ ] **Step 2: Verify nodes are registered**

```bash
~/go/bin/monoes node list 2>&1 | grep gemini
```

Expected: `gemini.generate_text` and `gemini.generate_image` appear in the list.

- [ ] **Step 3: Push**

```bash
git push origin master
```

---

### E2E Test Recipe

After all tasks, to test end-to-end:

```bash
# 1. Login to Gemini (requires browser interaction)
monoes login gemini

# 2. Test text generation
monoes node run gemini.generate_text --config '{"prompt":"What is 2+2?"}'

# 3. Test image generation
monoes node run gemini.generate_image --config '{"prompt":"A cat wearing a top hat"}'

# 4. In the Wails UI: Workflow Editor → add "Gemini Text" node → configure prompt → run
```
