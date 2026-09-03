package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/noderegistry"
	"github.com/monoes/mono-agent/internal/workflow"
)

// ─────────────────────────────────────────────────────────────────────────────
// Node types registry
// ─────────────────────────────────────────────────────────────────────────────

func (a *App) GetWorkflowNodeTypes() map[string]interface{} {
	type nodeDesc struct {
		Type        string               `json:"type"`
		Label       string               `json:"label"`
		Category    string               `json:"category"`
		Description string               `json:"description"`
		Schema      *workflow.NodeSchema `json:"schema,omitempty"`
	}
	mkNode := func(t, label, cat, desc string) nodeDesc {
		schema, _ := workflow.LoadDefaultSchema(t)
		return nodeDesc{Type: t, Label: label, Category: cat, Description: desc, Schema: schema}
	}

	catalog := map[string][]nodeDesc{
		"triggers": []nodeDesc{
			mkNode("trigger.manual", "Manual Trigger", "triggers", "Start a workflow run manually"),
			mkNode("trigger.schedule", "Schedule", "triggers", "Run workflow on a cron or interval schedule"),
			mkNode("trigger.webhook", "Webhook", "triggers", "Start workflow on incoming HTTP request"),
		},
		"control": []nodeDesc{
			mkNode("core.if", "If", "control", "Branch execution based on a condition"),
			mkNode("core.switch", "Switch", "control", "Route execution to one of multiple branches"),
			mkNode("core.merge", "Merge", "control", "Merge multiple input branches into one"),
			mkNode("core.split_in_batches", "Split In Batches", "control", "Process items in fixed-size batches"),
			mkNode("core.wait", "Wait", "control", "Pause execution for a specified duration"),
			mkNode("core.stop_error", "Stop And Error", "control", "Halt workflow with an error message"),
			mkNode("core.set", "Set", "control", "Set or transform data fields"),
			mkNode("core.code", "Code", "control", "Execute arbitrary JavaScript code"),
			mkNode("core.filter", "Filter", "control", "Keep only items matching a condition"),
			mkNode("core.sort", "Sort", "control", "Sort items by one or more fields"),
			mkNode("core.limit", "Limit", "control", "Limit the number of output items"),
			mkNode("core.remove_duplicates", "Remove Duplicates", "control", "Deduplicate items by key"),
			mkNode("core.compare_datasets", "Compare Datasets", "control", "Compare two datasets and output differences"),
			mkNode("core.aggregate", "Aggregate", "control", "Aggregate multiple items into one"),
			mkNode("core.human_in_loop", "Human in Loop", "control", "Pause and wait for a human to review, edit, and approve the data"),
		},
		"data": []nodeDesc{
			mkNode("data.datetime", "Date & Time", "data", "Parse, format, and manipulate date/time values"),
			mkNode("data.crypto", "Crypto", "data", "Hash, encrypt, or sign data"),
			mkNode("data.html", "HTML", "data", "Parse or generate HTML"),
			mkNode("data.xml", "XML", "data", "Parse or generate XML"),
			mkNode("data.markdown", "Markdown", "data", "Convert Markdown to HTML and vice-versa"),
			mkNode("data.spreadsheet", "Spreadsheet", "data", "Read or write spreadsheet data"),
			mkNode("data.compression", "Compression", "data", "Compress or decompress files"),
			mkNode("data.write_binary_file", "Write Binary File", "data", "Write binary data to disk"),
		},
		"http": []nodeDesc{
			mkNode("http.request", "HTTP Request", "http", "Make an HTTP/S request"),
			mkNode("http.ftp", "FTP", "http", "Transfer files via FTP/SFTP"),
			mkNode("http.ssh", "SSH", "http", "Execute commands over SSH"),
		},
		"system": []nodeDesc{
			mkNode("system.execute_command", "Execute Command", "system", "Run a shell command on the host"),
			mkNode("system.rss_read", "RSS Read", "system", "Fetch and parse an RSS/Atom feed"),
		},
		"db": []nodeDesc{
			mkNode("db.mysql", "MySQL", "db", "Query a MySQL/MariaDB database"),
			mkNode("db.postgres", "Postgres", "db", "Query a PostgreSQL database"),
			mkNode("db.mongodb", "MongoDB", "db", "Interact with a MongoDB collection"),
			mkNode("db.redis", "Redis", "db", "Read/write keys in a Redis store"),
		},
		"comm": []nodeDesc{
			mkNode("comm.email_send", "Send Email", "comm", "Send an email via SMTP"),
			mkNode("comm.email_read", "Read Email", "comm", "Read emails via IMAP"),
			mkNode("comm.slack", "Slack", "comm", "Send or read Slack messages"),
			mkNode("comm.telegram", "Telegram", "comm", "Send or receive Telegram messages"),
			mkNode("comm.discord", "Discord", "comm", "Send messages to a Discord channel"),
			mkNode("comm.twilio", "Twilio", "comm", "Send SMS or make calls via Twilio"),
			mkNode("comm.whatsapp", "WhatsApp", "comm", "Send WhatsApp messages"),
		},
		"service": []nodeDesc{
			mkNode("service.github", "GitHub", "service", "Interact with GitHub repositories and issues"),
			mkNode("service.airtable", "Airtable", "service", "Read/write Airtable bases"),
			mkNode("service.notion", "Notion", "service", "Read/write Notion pages and databases"),
			mkNode("service.jira", "Jira", "service", "Manage Jira issues and projects"),
			mkNode("service.linear", "Linear", "service", "Manage Linear issues and cycles"),
			mkNode("service.asana", "Asana", "service", "Manage Asana tasks and projects"),
			mkNode("service.stripe", "Stripe", "service", "Interact with Stripe payments"),
			mkNode("service.shopify", "Shopify", "service", "Manage Shopify orders and products"),
			mkNode("service.salesforce", "Salesforce", "service", "Read/write Salesforce records"),
			mkNode("service.hubspot", "HubSpot", "service", "Manage HubSpot CRM contacts and deals"),
			mkNode("service.google_sheets", "Google Sheets", "service", "Read/write Google Sheets"),
			mkNode("service.gmail", "Gmail", "service", "Send and read Gmail messages"),
			mkNode("service.outlook_mail", "Outlook", "service", "Send and read Outlook/Hotmail messages via Microsoft Graph"),
			mkNode("service.google_drive", "Google Drive", "service", "Manage Google Drive files"),
			mkNode("service.huggingface", "HuggingFace", "service", "Generate images or text via HuggingFace Inference API"),
			mkNode("service.openrouter", "OpenRouter", "service", "Generate text or images via OpenRouter AI API"),
		},
		"ai": []nodeDesc{
			mkNode("agent.ask", "Agent Ask", "ai", "Ask a locally-installed AI agent (claude, codex, …) one turn via the monomind engine"),
			mkNode("ai.chat", "AI Chat (deprecated)", "ai", "Deprecated — replace with Agent Ask"),
			mkNode("ai.extract", "AI Extract (deprecated)", "ai", "Deprecated — replace with Agent Ask"),
			mkNode("ai.classify", "AI Classify (deprecated)", "ai", "Deprecated — replace with Agent Ask"),
			mkNode("ai.transform", "AI Transform (deprecated)", "ai", "Deprecated — replace with Agent Ask"),
			mkNode("ai.embed", "AI Embed (deprecated)", "ai", "Deprecated — no local-agent equivalent"),
			mkNode("ai.agent", "AI Agent (deprecated)", "ai", "Deprecated — replace with Agent Ask"),
			mkNode("ai.read_page", "Read Page", "ai", "Crawl a webpage and return clean readable content (markdown, links, images)"),
			mkNode("ai.extract_page", "Extract Page", "ai", "Extract specific fields from a webpage using AI or CSS selectors"),
		},
		// Social-platform automation node types are grouped one category per
		// platform (rather than a single flat "browser" bucket) so the node
		// palette reads like the guided form the standalone Actions page
		// used to offer — this is what replaced that page (see Actions
		// removal below); browsing ~60 node types in one alphabetical list
		// would have been a real UX regression from it. Category ids here
		// (instagram, linkedin, x, tiktok, gemini, hackernews, producthunt)
		// must match groupOf's platform-prefix fallback below, which is also
		// what NodeRunner.jsx's CAT_COLOR keys off of.
		"instagram": []nodeDesc{
			mkNode("instagram.find_by_keyword", "Find By Keyword", "instagram", "Search Instagram users or posts by keyword"),
			mkNode("instagram.export_followers", "Export Followers", "instagram", "Export a profile's follower list"),
			mkNode("instagram.scrape_profile_info", "Scrape Profile Info", "instagram", "Collect profile metadata"),
			mkNode("instagram.engage_with_posts", "Engage With Posts", "instagram", "Like/comment on matched posts"),
			mkNode("instagram.send_dms", "Send DMs", "instagram", "Send direct messages"),
			mkNode("instagram.auto_reply_dms", "Auto Reply DMs", "instagram", "Automatically reply to incoming DMs"),
			mkNode("instagram.publish_post", "Publish Post", "instagram", "Publish a photo or reel"),
			mkNode("instagram.like_posts", "Like Posts", "instagram", "Like a list of posts"),
			mkNode("instagram.comment_on_posts", "Comment On Posts", "instagram", "Comment on a list of posts"),
			mkNode("instagram.like_comments_on_posts", "Like Comments On Posts", "instagram", "Like comments on posts"),
			mkNode("instagram.extract_post_data", "Extract Post Data", "instagram", "Extract structured data from posts"),
			mkNode("instagram.follow_users", "Follow Users", "instagram", "Follow a list of users"),
			mkNode("instagram.unfollow_users", "Unfollow Users", "instagram", "Unfollow a list of users"),
			mkNode("instagram.watch_stories", "Watch Stories", "instagram", "View stories for a list of users"),
			mkNode("instagram.engage_user_posts", "Engage User Posts", "instagram", "Engage with a specific user's posts"),
			mkNode("instagram.list_post_comments", "List Post Comments", "instagram", "List comments on a post"),
			mkNode("instagram.list_user_posts", "List User Posts", "instagram", "List posts from a user profile"),
			mkNode("instagram.reply_to_comments", "Reply To Comments", "instagram", "Reply to comments on posts"),
		},
		"linkedin": []nodeDesc{
			mkNode("linkedin.find_by_keyword", "Find By Keyword", "linkedin", "Search LinkedIn profiles by keyword"),
			mkNode("linkedin.export_followers", "Export Followers", "linkedin", "Export a profile's connections/followers"),
			mkNode("linkedin.scrape_profile_info", "Scrape Profile Info", "linkedin", "Collect LinkedIn profile metadata"),
			mkNode("linkedin.engage_with_posts", "Engage With Posts", "linkedin", "Like/comment on LinkedIn posts"),
			mkNode("linkedin.send_dms", "Send DMs", "linkedin", "Send LinkedIn direct messages"),
			mkNode("linkedin.auto_reply_dms", "Auto Reply DMs", "linkedin", "Automatically reply to LinkedIn messages"),
			mkNode("linkedin.publish_post", "Publish Post", "linkedin", "Publish a LinkedIn post"),
			mkNode("linkedin.comment_on_posts", "Comment On Posts", "linkedin", "Comment on LinkedIn posts"),
			mkNode("linkedin.like_comments", "Like Comments", "linkedin", "Like comments on LinkedIn posts"),
			mkNode("linkedin.like_posts", "Like Posts", "linkedin", "Like LinkedIn posts"),
			mkNode("linkedin.list_post_comments", "List Post Comments", "linkedin", "List comments on a LinkedIn post"),
			mkNode("linkedin.list_user_posts", "List User Posts", "linkedin", "List posts from a LinkedIn profile"),
		},
		"x": []nodeDesc{
			mkNode("x.find_by_keyword", "Find By Keyword", "x", "Search X/Twitter by keyword"),
			mkNode("x.export_followers", "Export Followers", "x", "Export a profile's followers on X"),
			mkNode("x.scrape_profile_info", "Scrape Profile Info", "x", "Collect X profile metadata"),
			mkNode("x.engage_with_posts", "Engage With Posts", "x", "Like/reply to X posts"),
			mkNode("x.send_dms", "Send DMs", "x", "Send X direct messages"),
			mkNode("x.auto_reply_dms", "Auto Reply DMs", "x", "Automatically reply to X DMs"),
			mkNode("x.publish_post", "Publish Post", "x", "Publish a post on X"),
		},
		"tiktok": []nodeDesc{
			mkNode("tiktok.find_by_keyword", "Find By Keyword", "tiktok", "Search TikTok by keyword"),
			mkNode("tiktok.export_followers", "Export Followers", "tiktok", "Export a TikTok profile's followers"),
			mkNode("tiktok.scrape_profile_info", "Scrape Profile Info", "tiktok", "Collect TikTok profile metadata"),
			mkNode("tiktok.engage_with_posts", "Engage With Posts", "tiktok", "Like/comment on TikTok posts"),
			mkNode("tiktok.send_dms", "Send DMs", "tiktok", "Send TikTok direct messages"),
			mkNode("tiktok.auto_reply_dms", "Auto Reply DMs", "tiktok", "Automatically reply to TikTok DMs"),
			mkNode("tiktok.publish_post", "Publish Post", "tiktok", "Publish a TikTok video"),
			mkNode("tiktok.comment_on_video", "Comment On Video", "tiktok", "Comment on a TikTok video"),
			mkNode("tiktok.duet_video", "Duet Video", "tiktok", "Create a duet with a TikTok video"),
			mkNode("tiktok.follow_user", "Follow User", "tiktok", "Follow a TikTok user"),
			mkNode("tiktok.like_comment", "Like Comment", "tiktok", "Like a comment on a TikTok video"),
			mkNode("tiktok.like_video", "Like Video", "tiktok", "Like a TikTok video"),
			mkNode("tiktok.list_user_videos", "List User Videos", "tiktok", "List videos from a TikTok profile"),
			mkNode("tiktok.list_video_comments", "List Video Comments", "tiktok", "List comments on a TikTok video"),
			mkNode("tiktok.share_video", "Share Video", "tiktok", "Share a TikTok video"),
			mkNode("tiktok.stitch_video", "Stitch Video", "tiktok", "Create a stitch with a TikTok video"),
		},
		"gemini": []nodeDesc{
			mkNode("gemini.generate_text", "Generate Text", "gemini", "Send a prompt to Gemini and get a text response"),
			mkNode("gemini.generate_image", "Generate Image", "gemini", "Send a prompt to Gemini and download generated images"),
			mkNode("gemini.chat_session", "Chat Session", "gemini", "Hold a multi-turn Gemini chat session"),
			mkNode("gemini.chat_session_many", "Chat Session (Batch)", "gemini", "Run a Gemini chat session over multiple inputs"),
		},
		"hackernews": []nodeDesc{
			mkNode("hackernews.submit_post", "Submit Post", "hackernews", "Submit a new post to Hacker News"),
			mkNode("hackernews.list_comments", "List Comments", "hackernews", "List comments on a Hacker News post"),
			mkNode("hackernews.reply_to_comment", "Reply To Comment", "hackernews", "Reply to a Hacker News comment"),
			mkNode("hackernews.get_post_metrics", "Get Post Metrics", "hackernews", "Get score/comment-count metrics for a post"),
		},
		"producthunt": []nodeDesc{
			mkNode("producthunt.comment_on_launch", "Comment On Launch", "producthunt", "Comment on a Product Hunt launch"),
			mkNode("producthunt.list_comments", "List Comments", "producthunt", "List comments on a Product Hunt launch"),
			mkNode("producthunt.get_launch_metrics", "Get Launch Metrics", "producthunt", "Get upvote/comment metrics for a launch"),
		},
		"people": []nodeDesc{
			mkNode("people.save", "Save to People", "people", "Upsert items into the People tab"),
			mkNode("people.sync_outlook_message", "Sync Email to People", "people", "Upsert the sender as a person and save the message to their history"),
		},
		"org": []nodeDesc{
			mkNode("org.run", "Run Org", "org", "Start a monomind agent organization and wait for its boss role to signal completion"),
		},
	}

	// Reconcile the hand-written catalog against the live node registry (the
	// same one the CLI runs) so the GUI always matches what can actually run:
	// stale entries drop out, newly registered types appear with a derived label.
	reg := noderegistry.Build(a.db)
	infraCategories := map[string]bool{
		"control": true, "data": true, "http": true, "system": true, "db": true,
		"comm": true, "service": true, "ai": true, "people": true, "image": true, "org": true,
	}
	groupOf := func(t string) string {
		prefix, _, _ := strings.Cut(t, ".")
		if prefix == "core" {
			return "control"
		}
		// Anything not an infra category is a platform bot (instagram,
		// linkedin, x, tiktok, gemini, hackernews, producthunt, ...) — its
		// own category, one per platform, rather than a shared "browser"
		// bucket (see the catalog above for why).
		return prefix
	}
	title := func(s string) string {
		words := strings.Split(strings.ReplaceAll(s, "_", " "), " ")
		for i, w := range words {
			if w != "" {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		return strings.Join(words, " ")
	}
	deriveLabel := func(t string) string {
		prefix, rest, _ := strings.Cut(t, ".")
		if infraCategories[groupOf(t)] {
			return title(rest)
		}
		return title(prefix) + ": " + title(rest)
	}

	seen := map[string]bool{}
	for group, list := range catalog {
		if group == "triggers" {
			continue // triggers run via the trigger manager, not the node registry
		}
		kept := list[:0]
		for _, n := range list {
			if reg.Has(n.Type) {
				kept = append(kept, n)
				seen[n.Type] = true
			}
		}
		catalog[group] = kept
	}
	for _, t := range reg.Types() { // Types() is sorted, so extras append deterministically
		if !seen[t] {
			g := groupOf(t)
			catalog[g] = append(catalog[g], mkNode(t, deriveLabel(t), g, ""))
		}
	}

	out := make(map[string]interface{}, len(catalog))
	for group, list := range catalog {
		out[group] = list
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Node runner — execute any node type directly
// ─────────────────────────────────────────────────────────────────────────────

// NodeRunRequest is the payload sent by the frontend to run a node.
type NodeRunRequest struct {
	NodeType string                   `json:"node_type"`
	Config   map[string]interface{}   `json:"config"`
	Items    []map[string]interface{} `json:"items"` // each element is a JSON object (the item's .json field)
}

// NodeRunResult is returned after running a node.
type NodeRunResult struct {
	Outputs    []NodeRunOutput `json:"outputs"`
	Error      string          `json:"error,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	RunID      string          `json:"run_id,omitempty"` // pass to StopNodeRun to cancel the run
}

// NodeRunOutput is one output handle's items.
type NodeRunOutput struct {
	Handle string                   `json:"handle"`
	Items  []map[string]interface{} `json:"items"`
}

// RunNode executes any registered node type directly via the CLI subprocess.
// Config and input items are passed as JSON; results are returned as structured data.
// legacyNodeTypes maps old short type names to their new prefixed equivalents.
var legacyNodeTypes = map[string]string{
	"if": "core.if", "switch": "core.switch", "merge": "core.merge",
	"split_in_batches": "core.split_in_batches", "wait": "core.wait",
	"stop_error": "core.stop_error", "set": "core.set", "code": "core.code",
	"filter": "core.filter", "sort": "core.sort", "limit": "core.limit",
	"remove_duplicates": "core.remove_duplicates", "compare_datasets": "core.compare_datasets",
	"aggregate": "core.aggregate",
	"datetime":  "data.datetime", "crypto": "data.crypto", "html": "data.html",
	"xml": "data.xml", "markdown": "data.markdown", "spreadsheet": "data.spreadsheet",
	"compression": "data.compression", "write_binary_file": "data.write_binary_file",
	"mysql": "db.mysql", "postgres": "db.postgres", "mongodb": "db.mongodb", "redis": "db.redis",
	"email_send": "comm.email_send", "email_read": "comm.email_read",
	"slack": "comm.slack", "telegram": "comm.telegram", "discord": "comm.discord",
	"twilio": "comm.twilio", "whatsapp": "comm.whatsapp",
	"github": "service.github", "airtable": "service.airtable", "notion": "service.notion",
	"jira": "service.jira", "linear": "service.linear", "asana": "service.asana",
	"stripe": "service.stripe", "shopify": "service.shopify", "salesforce": "service.salesforce",
	"hubspot": "service.hubspot", "google_sheets": "service.google_sheets",
	"gmail": "service.gmail", "google_drive": "service.google_drive",
}

func (a *App) RunNode(req NodeRunRequest) NodeRunResult {
	if mapped, ok := legacyNodeTypes[req.NodeType]; ok {
		req.NodeType = mapped
	}

	// Extract credential_id and pass it to the CLI via --credential so the
	// subprocess resolves the stored tokens internally against the same DB.
	// Never merge plaintext credential data into --config: process arguments
	// are world-readable to other local processes (ps / /proc/<pid>/cmdline).
	credID, _ := req.Config["credential_id"].(string)
	delete(req.Config, "credential_id")

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return NodeRunResult{Error: err.Error()}
	}

	configBytes, err := json.Marshal(req.Config)
	if err != nil {
		return NodeRunResult{Error: "invalid config: " + err.Error()}
	}

	items := req.Items
	if len(items) == 0 {
		items = []map[string]interface{}{}
	}
	inputItems := make([]map[string]interface{}, len(items))
	for i, it := range items {
		inputItems[i] = map[string]interface{}{"json": it}
	}
	inputBytes, err := json.Marshal(inputItems)
	if err != nil {
		return NodeRunResult{Error: "invalid input: " + err.Error()}
	}

	// Pass config + input via stdin, not argv: any secrets typed directly into
	// node fields (DB passwords, API keys) would otherwise be world-readable via
	// ps / /proc/<pid>/cmdline for the subprocess lifetime.
	payload, err := json.Marshal(map[string]json.RawMessage{
		"config": json.RawMessage(configBytes),
		"input":  json.RawMessage(inputBytes),
	})
	if err != nil {
		return NodeRunResult{Error: "encoding node payload: " + err.Error()}
	}

	start := time.Now()
	args := []string{
		"--profile", a.getActiveProfileID(),
		"node", "run", req.NodeType,
		"--stdin",
		"--output", "json",
	}
	if credID != "" {
		args = append(args, "--credential", credID)
	}
	cmd := exec.Command(cliBin, args...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Register the subprocess under a counter-based run id so the frontend
	// can cancel it with StopNodeRun (RA1-9). Namespaced "noderun:" in the
	// shared registry so shutdown reaps it too.
	runID := strconv.FormatInt(a.nodeRunCounter.Add(1), 10)
	runKey := "noderun:" + runID
	a.runningMu.Lock()
	a.runningCmds[runKey] = cmd
	a.runningMu.Unlock()

	startErr := cmd.Start()
	if startErr != nil {
		a.runningMu.Lock()
		delete(a.runningCmds, runKey)
		a.runningMu.Unlock()
		return NodeRunResult{Error: "failed to start node run: " + startErr.Error(), RunID: runID}
	}
	runErr := cmd.Wait()
	a.runningMu.Lock()
	delete(a.runningCmds, runKey)
	a.runningMu.Unlock()
	elapsed := time.Since(start).Milliseconds()
	out := stdout.Bytes()

	if runErr != nil {
		msg := runErr.Error()
		if _, ok := runErr.(*exec.ExitError); ok && stderr.Len() > 0 {
			msg = strings.TrimSpace(stderr.String())
		}
		return NodeRunResult{Error: msg, DurationMs: elapsed, RunID: runID}
	}

	var raw map[string][]struct {
		JSON map[string]interface{} `json:"json"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return NodeRunResult{Error: "failed to parse output: " + err.Error(), DurationMs: elapsed}
	}

	var outputs []NodeRunOutput
	for handle, rawItems := range raw {
		flat := make([]map[string]interface{}, len(rawItems))
		for i, ri := range rawItems {
			flat[i] = ri.JSON
		}
		outputs = append(outputs, NodeRunOutput{Handle: handle, Items: flat})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Handle < outputs[j].Handle })
	return NodeRunResult{Outputs: outputs, DurationMs: elapsed, RunID: runID}
}

// StopNodeRun kills the subprocess started by RunNode for the given run id
// (NodeRunResult.run_id). The interrupted RunNode call returns with its Error
// field set to the kill signal.
func (a *App) StopNodeRun(runID string) error {
	runKey := "noderun:" + runID
	a.runningMu.Lock()
	cmd, ok := a.runningCmds[runKey]
	if ok {
		delete(a.runningCmds, runKey)
	}
	a.runningMu.Unlock()
	if !ok || cmd.Process == nil {
		return fmt.Errorf("node run %s is not running", runID)
	}
	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("stop node run %s: %w", runID, err)
	}
	return nil
}
