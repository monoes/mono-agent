package main

// Reference entries for node types registered in the default build that
// ref.go's nodeDocs catalogue did not cover. Kept in a separate file so
// ref.go stays readable; init() appends them to the shared catalogue.

func init() {
	nodeDocs = append(nodeDocs, moreNodeDocs...)
}

var moreNodeDocs = []nodeDoc{
	// ── Triggers ──────────────────────────────────────────────────────────────
	{
		Type:     "trigger.manual",
		Category: "trigger",
		Short:    "Run workflow on demand (CLI, GUI, MCP, or HTTP API)",
		Description: `Starts the workflow only when someone runs it: "monoagentcli workflow run <id>",
the GUI run button, the MCP workflow_run tool, or the HTTP API. Trigger data
passed with --input '{"key":"value"}' becomes the first item's $json.`,
		Config:  `{}`,
		Inputs:  "none",
		Outputs: "one item carrying the --input JSON (empty when none given)",
	},
	{
		Type:     "trigger.webhook",
		Category: "trigger",
		Short:    "Run workflow when an HTTP request hits /webhook/<path>",
		Description: `Serves http://127.0.0.1:9321/webhook/<path> while "monoagentcli daemon" runs.
The bind address is loopback by default; set MONOAGENT_WEBHOOK_ADDR (e.g.
0.0.0.0:9321) to accept requests from other machines.`,
		Config: `{
  "path":        "stripe-events",   // required
  "method":      "POST",            // GET | POST | PUT | PATCH | DELETE
  "hmac_secret": "",                // optional: require a valid HMAC signature
  "auth_header": "X-Token",         // optional: require this header ...
  "auth_token":  "@secret:hook"     // ... to carry this value
}`,
		Inputs:  "none",
		Outputs: "one item with the request body (and request metadata)",
		Notes:   `The workflow must be activated ("workflow activate <id>") and the daemon running.`,
	},
	{
		Type:     "trigger.org",
		Category: "trigger",
		Short:    "Run workflow on an agent-org event or message",
		Description: `event mode: fires when something happens in an org (status, message, gate,
question, tool, usage, …). endpoint mode: fires when a message reaches this
workflow's automation role in an org chart (see "monoagentcli org automation-role").`,
		Config: `{
  "mode":        "event",           // event | endpoint
  "org_name":    "growth",          // required in event mode
  "event_types": "question,gate",   // comma-separated; empty = all
  "role":        "",                // only events from/to this role
  "subject_match": "",
  "question_kind": "ask_human"      // ask_human | approval | any
}`,
		Inputs:  "none",
		Outputs: "one item describing the org event or message",
		Notes:   "Requires monomind (the agent engine) — see \"monoagentcli ref org\".",
	},

	// ── Core ──────────────────────────────────────────────────────────────────
	{
		Type:     "core.human_in_loop",
		Category: "core",
		Short:    "Pause for a human to review, edit, then approve or reject",
		Description: `Pauses the run for each incoming item and puts it in a durable review queue
(SQLite — survives restarts). The reviewer sees readonly_fields as context and
can edit editable_fields before approving. Once every item is approved they
continue downstream with the edits applied. A rejection (or a timeout) makes the
node fail, which the node's on_error setting then handles (default: the run fails).`,
		Config: `{
  "readonly_fields": ["name", "company", "email"],
  "editable_fields": ["subject", "body"],   // empty = whole item editable
  "timeout_minutes": 60                     // 0 = wait forever; expiry counts as a rejection
}`,
		Inputs:  "any items",
		Outputs: "the approved (possibly edited) items",
		Notes: `While waiting the run's status is WAITING (exit code 0 from "workflow run").
Review with "monoagentcli hil list" / "hil approve <id>" / "hil reject <id>",
the GUI review panel, the MCP hil_* tools, or the HTTP API.`,
	},

	// ── Agents & orgs ─────────────────────────────────────────────────────────
	{
		Type:     "agent.ask",
		Category: "agent",
		Short:    "Ask a locally-installed AI agent (claude, codex, …) a question",
		Description: `The workflow node for AI steps. Hands the prompt to an agent CLI installed on
this machine through monomind, and stores the answer on the item. Replaces the
deprecated ai.chat / ai.extract / ai.classify / ai.transform / ai.agent nodes —
ask for JSON, a category, or a rewrite in the prompt itself.`,
		Config: `{
  "runtime":       "claude",                       // required; see "agent scan --installed"
  "prompt":        "Summarize: {{ $json.text }}",  // required
  "model":         "",                             // empty = runtime default
  "system_prompt": "",
  "output_key":    "agent_response",
  "timeout":       300                             // seconds
}`,
		Inputs:  "any items (prompt placeholders read from $json)",
		Outputs: "input item + <output_key> with the agent's answer",
		Notes: `Needs monomind and at least one agent runtime. Check with
"monoagentcli agent scan --installed" and "monoagentcli agent test <runtime>";
install one with "monoagentcli agent install <runtime>".`,
	},
	{
		Type:     "browser.jev",
		Category: "agent",
		Short:    "Browser agent: reach a goal on any website in your own browser (TypeSafe Jev)",
		Description: `Opens the URL in a new tab of your connected browser (the extension bridge,
so your logins apply) and works toward the goal one action at a time. Each
cycle reads the page as a numbered table of visible controls, and one TypeSafe
Jev request picks the operation (CLICK, TYPE_TEXT, SELECT, SCROLL_UP/DOWN,
WAIT, DONE, BLOCKED) and its target. For TYPE_TEXT a second Jev request picks
one of the configured values by name (or NONE); only when none fits does a
local agent (text_runtime) write the text. No selectors, no site scripts.
Port of browser-use/jev-ultrafast.`,
		Config: `{
  "url":          "https://www.google.com/travel/flights",  // required
  "goal":         "One-way Zurich to London on 2026-10-20…", // required
  "api_key":      "@secret:typesafe",   // else vault secret typesafe, then TYPESAFE_API_KEY
  "model":        "jev-latest",
  "values": {                           // typed into matching fields, no text turn
    "From":     "Zurich",
    "To":       "London",
    "email":    "@secret:airline-email" // vault-backed: stricter field match
  },
  "text_runtime": "claude",             // local agent for TYPE_TEXT values
  "text_model":   "",                   // e.g. haiku
  "max_actions":  40,                   // decisions are capped at 2×
  "timeout":      300,                  // seconds
  "fail_on_blocked": false
}`,
		Inputs: "any items (one run per item; none = one run)",
		Outputs: `input item + status (done|blocked|budget|timeout), reason, url, title,
page_text, steps[] (action, operation, text, value_name, probability,
confidence, latency_ms, page_changed), decisions, value_requests, text_turns,
low_confidence_steps (steps with confidence < 0.5), jev_input_tokens, elapsed_ms`,
		Notes: `DONE is the model's claim, not proof: check page_text downstream when it
matters. Page text is treated as data, never instructions, but the agent acts
in your logged-in browser, so give it goals you would trust a person with.
values: Jev only ever sees value NAMES; any configured value (4+ chars) is
replaced with <value:NAME> in page text, field values and history before every
Jev request. A plain value is used when Jev picks it with p >= 0.6; an
@secret: value needs p >= 0.9 AND an email/tel input or a field label
containing the value's name, else the text runtime is asked. Secret values are
shown as <value:NAME> in steps[].text; an unresolvable @secret: ref is a config
error. Password inputs are never observed (the snapshot skips them), so no
value is ever typed into one. Gate on low_confidence_steps downstream when a
shaky run matters.
Skips file inputs too; frames, shadow DOM, canvas and uploads can block.
Get a key at console.typesafe.ai and store it: "monoagentcli secret add --kind secret --name typesafe".`,
	},
	{
		Type:     "ai.choose",
		Category: "agent",
		Short:    "Route each item to one of your cases with a TypeSafe Jev choice (replaces ai.classify)",
		Description: `One TypeSafe Jev request per item picks which of the configured cases fits,
with probabilities and a confidence (~0.1–0.3 s, no text generation). Items go
out on the chosen case's handle; when the top probability is below
min_confidence they go to "low_confidence" instead. Extra typed questions
(noul = yes/no, choice, score = ordered rubric) ride along in the same request.
The item content is sent as "untrusted_input" and treated as data, never
instructions.`,
		Config: `{
  "cases": [                               // required, same shapes as core.switch
    "billing",
    { "value": "tech", "handle": "support",
      "description": "a technical problem or bug report" }
  ],
  "input":          "{{$json.subject}}\n{{$json.body}}",  // or "fields": ["subject","body"]; neither = whole item
  "instructions":   "Customer emails to a SaaS company.",
  "extra_questions": { "urgent": { "type": "noul", "criteria": "Does it need an answer today?" },
                       "tone":   { "type": "choice", "criteria": ["calm", "angry"] } },
  "min_confidence": 0.6,                   // top probability gate
  "output_key":     "choice",
  "api_key":        "@secret:typesafe",    // or TYPESAFE_API_KEY
  "model":          "jev-latest",
  "concurrency":    4                      // requests in flight, max 8
}`,
		Inputs: "any items (input capped at 6,000 characters)",
		Outputs: `one handle per case handle (in config order, empty ones included) plus
"low_confidence"; each item keeps its fields and gains <output_key>: {choice,
handle, probability, probabilities, confidence, low_confidence, model,
extra: {name: {noul} | {choice, probabilities, confidence} | {score, probabilities, confidence}}}`,
		Notes: `Items keep input order within each handle. A failed request fails the node.
No key => invalid-config error naming the missing vault secret. Get a key at
console.typesafe.ai: "monoagentcli secret add --kind secret --name typesafe".
Jev is weak at arithmetic, counting and date comparison — compute those first.`,
	},
	{
		Type:     "org.run",
		Category: "org",
		Short:    "Start (or join) a run of an agent org",
		Config: `{
  "org_name":   "growth",          // required
  "task":       "Plan next week's posts for {{ $json.topic }}",
  "wait":       true,              // false = continue once the run has started
  "exclusive":  false,             // true = fail if another run is already going
  "output_key": "org_result"
}`,
		Inputs:  "any item",
		Outputs: "input item + <output_key> with the org's run report (when wait is true)",
		Notes:   "Orgs are run by monomind — see \"monoagentcli ref org\".",
	},
	{
		Type:     "org.ask",
		Category: "org",
		Short:    "Ask an org role a question and wait for its reply",
		Config: `{
  "org_name":        "growth",     // required
  "role":            "",           // empty = the org's coordinator
  "question":        "Approve this draft? {{ $json.body }}",   // required
  "timeout_seconds": 600,
  "output_key":      "org_reply"
}`,
		Inputs:  "any item",
		Outputs: "input item + <output_key> with the role's reply",
		Notes:   "This workflow must be one of the org's automation roles so the reply has somewhere to go.",
	},
	{
		Type:     "org.send",
		Category: "org",
		Short:    "Send a message to an org role (live or queued)",
		Config: `{
  "org_name":   "growth",          // required
  "role":       "",                // empty = the org's coordinator
  "subject":    "New lead",
  "message":    "{{ $json.name }} signed up",   // required
  "output_key": "org_send"
}`,
		Inputs:  "any item",
		Outputs: "input item + <output_key> with the delivery receipt (live or queued)",
	},

	// ── Communication ─────────────────────────────────────────────────────────
	{
		Type:     "comm.outlook_read",
		Category: "comm",
		Short:    "Read messages from an Outlook mailbox (app password)",
		Config:   `{ "email": "you@outlook.com", "app_password": "@secret:outlook", "mailbox": "INBOX", "limit": 20, "unread_only": true }`,
		Inputs:   "none",
		Outputs:  "one item per message",
		Notes:    "For Microsoft Graph (OAuth) access use service.outlook_mail instead.",
	},
	{
		Type:     "comm.outlook_send",
		Category: "comm",
		Short:    "Send an email from an Outlook account (app password)",
		Config:   `{ "email": "you@outlook.com", "app_password": "@secret:outlook", "to": "{{ $json.email }}", "subject": "Hello", "body": "{{ $json.body }}" }`,
		Inputs:   "item with recipient/body fields",
		Outputs:  "send result",
	},
	{
		Type:     "comm.bluesky",
		Category: "comm",
		Short:    "Post to Bluesky or read a post's metrics",
		Config:   `{ "credential_id": "bsky", "operation": "create_post", "text": "{{ $json.text }}" }`,
		Inputs:   "item with text (create_post) or post_uri (get_post_metrics)",
		Outputs:  "post URI / metrics",
		Notes:    "Operations: create_post, get_post_metrics.",
	},
	{
		Type:     "comm.mastodon",
		Category: "comm",
		Short:    "Post a Mastodon status or read its metrics",
		Config:   `{ "credential_id": "masto", "operation": "create_status", "text": "{{ $json.text }}", "visibility": "public" }`,
		Inputs:   "item with text (create_status) or status_id (get_status_metrics)",
		Outputs:  "status id / metrics",
		Notes:    "Operations: create_status, get_status_metrics. Visibility: public, unlisted, private, direct.",
	},
	{
		Type:     "comm.reddit",
		Category: "comm",
		Short:    "Submit, reply, and read comments/metrics on Reddit",
		Config:   `{ "credential_id": "reddit", "operation": "submit_post", "subreddit": "golang", "title": "…", "text": "…" }`,
		Inputs:   "item with post fields",
		Outputs:  "operation result",
		Notes:    "Operations: submit_post, reply_to_comment, list_comments, get_post_metrics.",
	},

	// ── Services ──────────────────────────────────────────────────────────────
	{
		Type:     "service.bluesky",
		Category: "service",
		Short:    "Bluesky (ATProto): post, timeline, profile, like, repost",
		Config:   `{ "credential_id": "bsky", "operation": "get_timeline", "limit": 20 }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: create_post, get_timeline, get_profile, like_post, repost.",
	},
	{
		Type:     "service.mastodon",
		Category: "service",
		Short:    "Mastodon (ActivityPub): publish, timeline, account, favourite, boost",
		Config:   `{ "credential_id": "masto", "operation": "publish_status", "text": "{{ $json.text }}" }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: publish_status, get_timeline, get_account, favourite, boost.",
	},
	{
		Type:     "service.reddit",
		Category: "service",
		Short:    "Reddit: submit posts, read hot, comment, upvote",
		Config:   `{ "credential_id": "reddit", "operation": "get_hot", "subreddit": "golang", "limit": 10 }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: submit_post (kind self|link), get_hot, comment, upvote. Set a descriptive user_agent.",
	},
	{
		Type:     "service.devto",
		Category: "service",
		Short:    "Dev.to: publish and read articles and comments",
		Config:   `{ "credential_id": "devto", "operation": "publish_article", "title": "…", "body_markdown": "…", "tags": "go,automation", "published": false }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: publish_article, list_articles, get_article, list_comments, create_comment.",
	},
	{
		Type:     "service.hashnode",
		Category: "service",
		Short:    "Hashnode: publish and read posts",
		Config:   `{ "credential_id": "hashnode", "operation": "publish_post", "publication_id": "…", "title": "…", "content_markdown": "…" }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: publish_post, list_posts, get_post.",
	},
	{
		Type:     "service.producthunt",
		Category: "service",
		Short:    "Product Hunt API: posts, metrics, comments",
		Config:   `{ "credential_id": "ph", "operation": "get_post_metrics", "slug": "mono-agent" }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: get_post, list_posts, get_post_metrics, create_comment.",
	},
	{
		Type:     "service.discord",
		Category: "service",
		Short:    "Discord bot API: send/list messages, react, list channels",
		Config:   `{ "credential_id": "discord-bot", "operation": "send_message", "channel_id": "123", "text": "{{ $json.text }}" }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: send_message, list_messages, add_reaction, list_channels. For a simple webhook post use comm.discord.",
	},
	{
		Type:     "service.youtube",
		Category: "service",
		Short:    "YouTube: upload, stats, comments, search",
		Config:   `{ "credential_id": "youtube", "operation": "get_video_stats", "video_id": "dQw4w9WgXcQ" }`,
		Inputs:   "item",
		Outputs:  "operation result",
		Notes:    "Operations: upload_video, get_video_stats, list_comments, reply_to_comment, search_videos.",
	},

	// ── Hacker News / Product Hunt (social build, browser) ────────────────────
	{
		Type:     "hackernews.submit_post",
		Category: "hackernews",
		Short:    "Submit a link or text (Show HN) post to Hacker News",
		Config:   `{ "title": "Show HN: …", "url": "https://…" }   // or "text" instead of "url"`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "hackernews.reply_to_comment",
		Category: "hackernews",
		Short:    "Reply to a Hacker News item or comment thread",
		Config:   `{ "itemID": "41234567", "text": "{{ $json.reply }}" }`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "hackernews.list_comments",
		Category: "hackernews",
		Short:    "List the top-level comments on a Hacker News item",
		Config:   `{ "itemID": "41234567" }`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "hackernews.get_post_metrics",
		Category: "hackernews",
		Short:    "Read a Hacker News item's points and comment count",
		Config:   `{ "itemID": "41234567" }`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "producthunt.comment_on_launch",
		Category: "producthunt",
		Short:    "Post a comment on a Product Hunt launch page",
		Config:   `{ "launchURL": "https://www.producthunt.com/posts/…", "text": "{{ $json.comment }}" }`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "producthunt.list_comments",
		Category: "producthunt",
		Short:    "List the visible comments on a Product Hunt launch page",
		Config:   `{ "launchURL": "https://www.producthunt.com/posts/…" }`,
		Notes:    socialBrowserNote,
	},
	{
		Type:     "producthunt.get_launch_metrics",
		Category: "producthunt",
		Short:    "Read a Product Hunt launch's upvotes and comment count",
		Config:   `{ "launchURL": "https://www.producthunt.com/posts/…" }`,
		Notes:    socialBrowserNote,
	},
}

const socialBrowserNote = `Social build only (go build -tags social). Runs in your own logged-in
browser through the extension bridge, so a saved session for the site is needed.
For API access use the service.producthunt node instead of producthunt.*.`
