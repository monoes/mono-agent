# Gemini Browser Nodes — Design Spec

**Date:** 2026-04-03  
**Status:** Approved  
**Scope:** Two new browser-automation workflow nodes (`gemini.generate_text`, `gemini.generate_image`) that interact with gemini.google.com via Rod, following the 3-tier fallback pattern used by Instagram/LinkedIn/X/TikTok.

---

## Problem

Users want to use Google Gemini for text and image generation within workflows without needing an API key. Gemini's web interface is free-tier accessible with any Google account. By automating the browser interaction, users can integrate Gemini into automation pipelines using their existing Google session.

## Solution

Two action JSON files + a GeminiBot adapter, following the exact pattern of social platform browser nodes:

- **`gemini.generate_text`** — Types a prompt into Gemini, waits for text response, extracts it
- **`gemini.generate_image`** — Types a prompt, waits for image generation, downloads images locally

Both use 3-tier fallback selectors at every DOM interaction step. Auto-registered via `RegisterBrowserNodes`.

---

## Platform Registration

### Connection Registry (`internal/connections/registry.go`)

```go
"gemini": {
    ID:         "gemini",
    Name:       "Gemini",
    Category:   "social",
    ConnectVia: "UI",
    Methods:    []AuthMethod{MethodBrowser},
    Fields:     map[AuthMethod][]CredentialField{},
    IconEmoji:  "✨",
}
```

### Session Handling

Login flow uses `LoginSocial("gemini")` which opens `gemini.google.com` in the system Chrome browser. User logs into their Google account. Cookies are saved to `crawler_sessions`.

**Fallback session lookup order:**
1. `crawler_sessions WHERE platform = 'gemini'` — dedicated Gemini session
2. `crawler_sessions WHERE platform IN ('google_sheets', 'gmail')` — reuse Google cookies from other logins (same Google account domain)
3. If nothing found — prompt browser login

---

## Bot Adapter (`internal/bot/gemini/bot.go`)

Implements `BotAdapter` interface:

| Method | Implementation |
|--------|---------------|
| `Platform()` | `"GEMINI"` |
| `LoginURL()` | `"https://gemini.google.com"` |
| `IsLoggedIn(page)` | Returns `true` if prompt input exists (`div[contenteditable]`, `rich-textarea`) AND no sign-in button (`a[href*="accounts.google.com/ServiceLogin"]`) |
| `ExtractUsername(url)` | Returns `"gemini-user"` (Gemini doesn't expose username in URL) |
| `ResolveURL(input)` | Returns `"https://gemini.google.com"` |
| `SearchURL(query)` | Not applicable, returns empty |
| `SendMessage(...)` | Not applicable for Gemini |
| `GetProfileData(...)` | Not applicable for Gemini |

### Bot Methods via `GetMethodByName`

| Method Name | Purpose |
|-------------|---------|
| `find_prompt_input` | Locate the prompt textarea/contenteditable |
| `type_prompt` | Focus input and type the prompt text with human-like delays |
| `click_send` | Find and click the send button |
| `wait_for_response` | Poll until spinner disappears and response text appears (text mode, 60s default) |
| `wait_for_image_response` | Poll until images appear in the response (image mode, 120s default) |
| `extract_text_response` | Extract the markdown/text content from Gemini's response |
| `extract_image_response` | Find all generated images, capture blob URLs |
| `download_images` | Download blob-URL images via page.Evaluate, save to disk |

---

## Action JSON: `generate_text.json`

**File:** `data/actions/gemini/generate_text.json`

### Steps with 3-Tier Fallback

#### Step 1: Navigate to Gemini
```json
{
  "id": "navigate_gemini",
  "type": "navigate",
  "url": "https://gemini.google.com",
  "timeout": 15
}
```

#### Step 2: Find prompt input (Tier 1 → 2 → 3)
```json
{
  "id": "find_input_t1",
  "type": "call_bot_method",
  "methodName": "find_prompt_input",
  "variable_name": "promptInput",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "find_input_t2",
  "type": "find_element",
  "selector": "div[contenteditable='true']",
  "alternatives": ["rich-textarea", "textarea[aria-label*='prompt']", ".ql-editor"],
  "variable_name": "promptInput",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "find_input_t3",
  "type": "find_element",
  "configKey": "GEMINI_GENERATE_TEXT.prompt_input",
  "variable_name": "promptInput",
  "onError": "mark_failed",
  "timeout": 10
}
```

#### Step 3: Type prompt
```json
{
  "id": "type_prompt_t1",
  "type": "call_bot_method",
  "methodName": "type_prompt",
  "args": ["{{prompt}}"],
  "onError": "skip",
  "timeout": 15
},
{
  "id": "type_prompt_t2",
  "type": "type_text",
  "target": "{{promptInput}}",
  "text": "{{prompt}}",
  "humanize": true,
  "onError": "mark_failed",
  "timeout": 15
}
```

#### Step 4: Click send (Tier 1 → 2 → 3)
```json
{
  "id": "click_send_t1",
  "type": "call_bot_method",
  "methodName": "click_send",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "click_send_t2",
  "type": "find_element",
  "selector": "button[aria-label*='Send']",
  "alternatives": ["button[data-testid='send-button']", "button .send-button-icon", "mat-icon[data-mat-icon-name='send']"],
  "action": "click",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "click_send_t3",
  "type": "find_element",
  "configKey": "GEMINI_GENERATE_TEXT.send_button",
  "action": "click",
  "onError": "mark_failed",
  "timeout": 10
}
```

#### Step 5: Wait for response (Tier 1 → 2 → 3)
```json
{
  "id": "wait_response_t1",
  "type": "call_bot_method",
  "methodName": "wait_for_response",
  "args": ["{{maxWaitSeconds or 60}}"],
  "variable_name": "responseReady",
  "onError": "skip",
  "timeout": 90
},
{
  "id": "wait_response_t2",
  "type": "wait",
  "condition": "element_visible",
  "selector": ".response-container .markdown-content",
  "alternatives": ["message-content .model-response-text", ".response-container model-response"],
  "pollInterval": 2000,
  "onError": "skip",
  "timeout": 90
},
{
  "id": "wait_response_t3",
  "type": "wait",
  "condition": "element_visible",
  "configKey": "GEMINI_GENERATE_TEXT.response_container",
  "pollInterval": 2000,
  "onError": "mark_failed",
  "timeout": 90
}
```

#### Step 6: Extract text response (Tier 1 → 2 → 3)
```json
{
  "id": "extract_text_t1",
  "type": "call_bot_method",
  "methodName": "extract_text_response",
  "variable_name": "responseText",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "extract_text_t2",
  "type": "find_element",
  "selector": ".response-container .markdown-content",
  "alternatives": ["message-content .model-response-text", "div[data-content-type='text']"],
  "extract": "text",
  "variable_name": "responseText",
  "onError": "skip",
  "timeout": 10
},
{
  "id": "extract_text_t3",
  "type": "find_element",
  "configKey": "GEMINI_GENERATE_TEXT.response_text",
  "extract": "text",
  "variable_name": "responseText",
  "onError": "mark_failed",
  "timeout": 10
}
```

### Inputs
- `prompt` (required) — the text prompt to send
- `maxWaitSeconds` (optional, default 60)

### Output
```json
{
  "prompt": "Explain quantum computing",
  "response_text": "Quantum computing is...",
  "generation_time_ms": 4500
}
```

---

## Action JSON: `generate_image.json`

**File:** `data/actions/gemini/generate_image.json`

Steps 1–4 are identical to `generate_text.json` (navigate, find input, type, send).

#### Step 5: Wait for image response (Tier 1 → 2 → 3)
```json
{
  "id": "wait_image_t1",
  "type": "call_bot_method",
  "methodName": "wait_for_image_response",
  "args": ["{{maxWaitSeconds or 120}}"],
  "variable_name": "imagesReady",
  "onError": "skip",
  "timeout": 150
},
{
  "id": "wait_image_t2",
  "type": "wait",
  "condition": "element_visible",
  "selector": ".response-container img[src*='blob:']",
  "alternatives": [".generated-image img", "img[data-image-id]", ".response-container img:not([alt='avatar']):not([width='24'])"],
  "pollInterval": 3000,
  "onError": "skip",
  "timeout": 150
},
{
  "id": "wait_image_t3",
  "type": "wait",
  "condition": "element_visible",
  "configKey": "GEMINI_GENERATE_IMAGE.response_image",
  "pollInterval": 3000,
  "onError": "mark_failed",
  "timeout": 150
}
```

#### Step 6: Extract images (Tier 1 → 2 → 3)
```json
{
  "id": "extract_images_t1",
  "type": "call_bot_method",
  "methodName": "extract_image_response",
  "variable_name": "imageUrls",
  "onError": "skip",
  "timeout": 15
},
{
  "id": "extract_images_t2",
  "type": "find_element",
  "selector": ".response-container img[src*='blob:']",
  "alternatives": [".generated-image img", "img[data-image-id]"],
  "extract": "attribute:src",
  "multiple": true,
  "variable_name": "imageUrls",
  "onError": "skip",
  "timeout": 15
},
{
  "id": "extract_images_t3",
  "type": "find_element",
  "configKey": "GEMINI_GENERATE_IMAGE.response_image",
  "extract": "attribute:src",
  "multiple": true,
  "variable_name": "imageUrls",
  "onError": "mark_failed",
  "timeout": 15
}
```

#### Step 7: Download images
```json
{
  "id": "download_images",
  "type": "call_bot_method",
  "methodName": "download_images",
  "args": ["{{imageUrls}}", "{{downloadDir or ~/.monoes/downloads}}"],
  "variable_name": "downloadedImages",
  "onError": "mark_failed",
  "timeout": 30
}
```

The `download_images` bot method:
1. For each image URL (blob: or https:), uses `page.Evaluate` to fetch the blob as base64
2. Decodes and writes to `{downloadDir}/gemini_{timestamp}_{index}.png`
3. Returns array of `{path, filename, size_bytes}`

### Inputs
- `prompt` (required) — image generation prompt
- `maxWaitSeconds` (optional, default 120)
- `downloadDir` (optional, default `~/.monoes/downloads`)

### Output
```json
{
  "prompt": "A sunset over mountains in watercolor style",
  "images": [
    {"path": "/Users/me/.monoes/downloads/gemini_1712345678_0.png", "filename": "gemini_1712345678_0.png", "size_bytes": 245000},
    {"path": "/Users/me/.monoes/downloads/gemini_1712345678_1.png", "filename": "gemini_1712345678_1.png", "size_bytes": 238000}
  ],
  "image_count": 2,
  "generation_time_ms": 15000
}
```

---

## Config Schemas (Tier 3 configKey)

Added to `internal/config/schemas.go`:

```go
var geminiGenerateTextSchema = schema("generate_text", "Gemini text generation elements",
    field("prompt_input", "Prompt input textarea/contenteditable"),
    field("send_button", "Send/submit button"),
    field("loading_indicator", "Loading/generating spinner"),
    field("response_container", "Response message container"),
    field("response_text", "Text content within the response"),
)

var geminiGenerateImageSchema = schema("generate_image", "Gemini image generation elements",
    field("prompt_input", "Prompt input textarea/contenteditable"),
    field("send_button", "Send/submit button"),
    field("loading_indicator", "Loading/generating spinner"),
    field("response_image", "Generated image element(s)"),
)
```

Registered as `"GEMINI_GENERATE_TEXT"` and `"GEMINI_GENERATE_IMAGE"` in the schemas map.

---

## UI Schemas

**`internal/workflow/schemas/gemini.generate_text.json`:**
```json
{
  "credential_platform": "gemini",
  "fields": [
    {"key": "prompt", "label": "Prompt", "type": "textarea", "required": true, "rows": 4, "placeholder": "Ask Gemini anything..."},
    {"key": "maxWaitSeconds", "label": "Max Wait (seconds)", "type": "number", "default": 60, "help": "Maximum time to wait for Gemini to respond."}
  ]
}
```

**`internal/workflow/schemas/gemini.generate_image.json`:**
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

---

## File Structure

```
internal/bot/gemini/
└── bot.go                          # GeminiBot adapter + methods

data/actions/gemini/
├── generate_text.json              # Text generation action (3-tier fallback)
└── generate_image.json             # Image generation action (3-tier fallback)

internal/workflow/schemas/
├── gemini.generate_text.json       # UI schema
└── gemini.generate_image.json      # UI schema

internal/config/schemas.go          # Add GEMINI_GENERATE_TEXT, GEMINI_GENERATE_IMAGE schemas
internal/connections/registry.go    # Add "gemini" platform entry
wails-app/app.go                    # Add gemini nodes to GetWorkflowNodeTypes browser category
```

**Auto-registration:** `RegisterBrowserNodes` in `browser_register.go` scans `data/actions/` at compile time via `go:embed`. Adding files to `data/actions/gemini/` automatically registers `gemini.generate_text` and `gemini.generate_image` as node types. No manual registration code needed.

---

## Error Handling

| Error | Behavior |
|-------|----------|
| Not logged in (no Google session) | Action fails at step 1 with "not logged in — run Login first" |
| Prompt input not found (all 3 tiers fail) | Action marked failed, returns error with last selector attempted |
| Gemini rate limit / "try again later" | Extract error text from page, return in error output |
| Response timeout (spinner never stops) | Action fails after maxWaitSeconds, returns partial output if any |
| Image download fails (blob expired) | Return image URLs without downloaded files, warning in output |
| Gemini refuses prompt (safety filter) | Extract refusal text, return as `response_text` with `refused: true` flag |

---

## Dependencies

No new Go modules. Uses existing:
- `github.com/go-rod/rod` — browser automation
- `github.com/go-rod/stealth` — anti-detection
- `data/embed.go` — embeds action JSONs at compile time
