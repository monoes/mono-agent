<p align="center">
  <img src="assets/banner.png" alt="Mono Agent" width="600" />
</p>

<h3 align="center">Local-first n8n alternative in a single Go binary<br/>— visual workflows, CLI, human-in-the-loop.</h3>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-purple?style=for-the-badge)](LICENSE)
[![CI](https://github.com/monoes/mono-agent/actions/workflows/ci.yml/badge.svg)](https://github.com/monoes/mono-agent/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-macOS%20%7C%20Linux%20%7C%20Windows-333?style=for-the-badge)](#getting-started)

</div>

---

## What is Mono Agent?

**Mono Agent** is a production-grade, local-first automation platform for humans **and** AI agents:

- 🔁 **DAG workflow engine** — 90 built-in node types (150 with the optional social build): services (GitHub, Google Sheets / Gmail / Drive, Stripe, Salesforce, HubSpot, Jira, Linear, Notion, Airtable), databases, HTTP, data transforms, and comms (Gmail, Outlook, Slack, Telegram, Discord, and more)
- 📦 **Single static Go binary** — zero CGO, SQLite embedded, no Docker, no Node.js runtime, no telemetry. All data stays on your machine (crash reports default to local files — see [SECURITY.md](SECURITY.md))
- 🖥️ **Three ways to drive it** — a visual canvas editor (Wails desktop GUI), a 70+-command CLI with JSON output everywhere, and a built-in MCP server so AI agents can operate it safely
- 🤝 **Human-in-the-loop as a platform primitive** — pause any workflow for review, edit the payload, then approve or reject; the queue is durable and survives restarts
- 🌐 **Browser automation where no practical API exists** — drive *your own logged-in Chrome* via the bundled extension bridge, publishing to and reading your own accounts (same model as consumer RPA tools)
- 📣 **Social platform nodes** (Instagram, LinkedIn, X, TikTok, Hacker News, Product Hunt) are an **opt-in** compile-time build (`-tags social`) for managing your own accounts — see [Usage Policy](docs/USAGE_POLICY.md)

Think of it as an honest, self-hosted n8n you can carry in a single file — with human approval gates and first-class agent access built in.

> ### Scope & fair use
>
> Mono Agent is a general automation tool. The social/browser nodes exist to publish to and read **your own accounts**, not to mass-message, spam, or manipulate anyone.
>
> - **Own accounts only** — actions run against sessions and credentials you personally control
> - **Approval gates** — drop a `core.human_in_loop` node before any sensitive step so a human reviews (and can edit) what goes out
> - **Platform terms apply** — automating your own account may still be subject to the platform's Terms of Service; that's between you and the platform
> - **Read the full policy** — [docs/USAGE_POLICY.md](docs/USAGE_POLICY.md)
>
> Mono Agent is an independent, unofficial, MIT-licensed project. It is not affiliated with, endorsed by, or connected to any of the platforms it can talk to.

---

## Quick Start

```bash
# Build the CLI (Go 1.25+, no CGO)
git clone https://github.com/monoes/mono-agent.git
cd mono-agent
go build -o monoagentcli ./cmd/monoagentcli

# Orientation
./monoagentcli version
./monoagentcli ref                       # built-in offline docs: commands, nodes, expressions
./monoagentcli node list                 # all 90 node types

# Try the flagship example workflow (prints the new workflow id)
./monoagentcli workflow templates list
./monoagentcli workflow import --file examples/morning-briefing.json
./monoagentcli workflow activate <id>              # enable its triggers
./monoagentcli workflow run <id>                   # run it now
# Note: the full flagship run needs an OpenRouter API key (`connect openrouter`) — without one it fails cleanly at the summarize step.

# Run the scheduler daemon (keeps cron/webhook triggers alive)
./monoagentcli daemon
```

Prefer a one-line install? See [install.sh](install.sh) or [Docker](Dockerfile).

### Flagship example — "Morning Briefing"

Every weekday at 7am: read your favorite feeds, filter for AI news, summarize with an LLM, pause for a human to edit the summary, then email it to you.

```
[trigger.schedule: 0 0 7 * * 1-5]
        │
        ▼
[system.rss_read]        ← fetch items from an RSS/Atom feed
        │
        ▼
[core.filter]            ← keep items matching a condition
        │
        ▼
[service.openrouter]     ← generate_text: summarize titles into a brief
        │
        ▼
[core.human_in_loop]     ← PAUSE — you review & edit the draft
        │                   Approve → continue | Reject → drop
        ▼
[comm.email_send]        ← email the approved digest to you
```

```json
{
  "name": "Morning Briefing",
  "nodes": [
    { "id": "t1",  "type": "trigger.schedule",   "config": { "cron": "0 0 7 * * 1-5" } },
    { "id": "n1",  "type": "system.rss_read",    "config": { "url": "https://example.com/feed.xml", "limit": 25 } },
    { "id": "n2",  "type": "core.filter",        "config": { "condition": "{{item.title}} contains ai" } },
    { "id": "n3",  "type": "service.openrouter", "config": {
        "operation": "generate_text", "model": "anthropic/claude-3-haiku",
        "prompt": "Summarize these headlines into a 5-bullet briefing:\n{{item.title}}", "credential_id": "YOUR_OR_CRED" } },
    { "id": "n4",  "type": "core.human_in_loop", "config": {
        "readonly_fields": ["title", "link"],
        "editable_fields": ["summary"],
        "timeout_minutes": 120 } },
    { "id": "n5",  "type": "comm.email_send",    "config": {
        "to": "you@example.com", "subject": "Morning Briefing",
        "body": "{{item.summary}}", "credential_id": "YOUR_SMTP_CRED" } }
  ],
  "connections": [
    { "source": "t1", "target": "n1" }, { "source": "n1", "target": "n2" },
    { "source": "n2", "target": "n3" }, { "source": "n3", "target": "n4" },
    { "source": "n4", "target": "n5" }
  ]
}
```

More ready-to-run workflows (RSS→AI→email, Sheets→Gmail, Stripe→Sheets sync, GitHub→Linear, and more) live in [examples/](examples/).

---

## Feature Highlights

<table>
<tr>
<td width="50%">

### 🔄 Workflow Engine
- DAG execution with cycle detection (Kahn topological sort)
- Template expressions `{{variable.path}}` — dot notation, array indices, fallback chains
- Per-node `on_error` semantics: `stop`, `continue`, `skip`, `error_branch`
- Honest run statuses — partial failures surface as `SUCCESS_WITH_ERRORS`, never green
- Webhook, cron, and manual triggers
- Hybrid storage: JSON workflow files + SQLite; full execution history

</td>
<td width="50%">

### 🤝 Human-in-the-Loop (platform primitive)
- Drop `core.human_in_loop` anywhere to pause for human review
- **Durable, DB-backed queue** — pending approvals survive restarts
- **Edit-before-approve** — readonly fields show context; editable fields let the reviewer fix content before it proceeds
- Optional timeout with auto-reject
- Approve via CLI (`hil list` / `hil approve <id>`) or the GUI review panel

</td>
</tr>
<tr>
<td width="50%">

### 👤 Multi-Profile Workspaces
- Named workspaces (`default`, `work`, `client-a`, …) with full tenant isolation
- Workflows, connections, people, vault images, HIL items — all scoped per workspace
- Switch with `--profile <name>` on any CLI command or via the GUI sidebar
- Running workflows in one workspace are unaffected when you switch to another

</td>
<td width="50%">

### 🔐 Encrypted Secrets Vault
- Secrets stored in your **OS keyring** (macOS Keychain / Windows Credential Manager / Linux secret service)
- **AES-256-GCM envelope encryption** (DEK/KEK) for data at rest
- Encrypted, passphrase-protected portable export (`secret export` / `secret import`)
- One-command migration: `secret encrypt-connections` seals legacy plaintext credentials in place
- Manageable from the CLI (`secret`) or the GUI Vault page

</td>
</tr>
<tr>
<td width="50%">

### 📇 CRM: People + Communications
- Contact database with tags, notes, and full message history
- Unified inbox across sources (Gmail, Outlook sync built in)
- AI drafts an email → you edit/approve → one-click send (see [example below](#-human-in-the-loop-example-email-outreach-with-review))
- `people import` (JSON), people search, and per-person status timeline

</td>
<td width="50%">

### 🖼️ Image Vault
- Every workflow-generated image registered with provenance (which run, which node)
- Reference images in prompts and posts as `{{@img-001}}`
- Fullscreen editor — crop, resize, rotate, filters
- AI background removal (U2-Net) plus a full image-processing node set
- Profile-scoped: each workspace sees only its own vault

</td>
</tr>
<tr>
<td width="50%">

### 📣 Communication Nodes
- Email: `comm.email_send`, Outlook (`comm.outlook_read` / `comm.outlook_send`)
- Chat: Slack, Discord, Telegram, WhatsApp, Twilio (SMS)
- Open social protocols: Bluesky, Mastodon, Reddit — via official-style APIs
- `comm.email_read` is currently **experimental** (requires an IMAP dependency not yet vendored)

</td>
<td width="50%">

### 🤖 AI Canvas Chat + Desktop GUI
- Conversational workflow builder: describe the workflow in chat, AI wires the nodes
- OpenRouter (200+ models), HuggingFace, Gemini
- Wails 2 desktop app: canvas editor, HIL review panel, Vault, People, Image Vault
- Dark-themed, keyboard-navigable, fully local

</td>
</tr>
</table>

---

## 🤝 Human-in-the-Loop example: email outreach with review

The pattern Mono Agent recommends for *any* outbound communication — the AI drafts, a human decides. No message leaves the machine until a person approves it.

```
[service.google_sheets]      ← read prospect rows (name, company, email)
        │
        ▼
[service.openrouter]         ← generate_text: draft a personalized email
        │
        ▼
[core.human_in_loop]         ← PAUSE — reviewer sees:
        │                       Read-only: name, company, email
        │                       Editable:  subject, body
        │                     Approve → continue | Reject → drop item
        ▼
[comm.email_send]            ← send the approved (possibly edited) email
        │
        ▼
[service.google_sheets]      ← mark row as "sent"
```

```json
{
  "id": "n3",
  "type": "core.human_in_loop",
  "config": {
    "readonly_fields": ["name", "company", "email"],
    "editable_fields": ["subject", "body"],
    "timeout_minutes": 60
  }
}
```

Approve from the terminal while the workflow waits:

```bash
monoagentcli hil list               # show pending items
monoagentcli hil approve <id>       # resume the workflow
monoagentcli hil reject <id>        # drop the item
```

---

## Node Library

> 90 built-in node types (+ triggers) in the default build — 150 with the optional social build (below).

<details>
<summary><strong>⚙️ Core Control (15 nodes)</strong></summary>

| Node | Description |
|------|-------------|
| `core.if` | Conditional branching — route items by expression |
| `core.switch` | Multi-way routing — N output handles |
| `core.set` | Assign or transform fields on items |
| `core.filter` | Keep only items matching a predicate |
| `core.code` | Execute JavaScript (Goja engine) on item stream |
| `core.merge` | Combine multiple input streams |
| `core.split_in_batches` | Chunk items into N-size groups |
| `core.wait` | Pause execution for N seconds |
| `core.limit` | Keep first N items |
| `core.sort` | Sort items by key ascending/descending |
| `core.remove_duplicates` | Deduplicate items by key |
| `core.compare_datasets` | Diff two item streams |
| `core.aggregate` | Sum, avg, count, min, max over a field |
| `core.stop_error` | Halt workflow with a custom error message |
| `core.human_in_loop` | Pause execution — human reviews, edits, approves or rejects |

</details>

<details>
<summary><strong>🔗 Services (24 nodes)</strong></summary>

| Node | Description |
|------|-------------|
| `service.google_sheets` | Read rows, append, update, clear ranges |
| `service.gmail` | Send and read Gmail messages |
| `service.google_drive` | File operations on Google Drive |
| `service.outlook_mail` | Read/send Outlook via Microsoft Graph |
| `service.openrouter` | Generate text or images via 200+ AI models |
| `service.huggingface` | HuggingFace inference (text + images) |
| `service.github` | Issues, PRs, repos, and more |
| `service.notion` | Pages, databases, blocks |
| `service.airtable` | Records, bases, fields |
| `service.linear` | Issues, projects, teams |
| `service.jira` | Issues, sprints, projects |
| `service.asana` | Tasks, projects, teams |
| `service.stripe` | Payments, customers, subscriptions |
| `service.shopify` | Products, orders, customers |
| `service.salesforce` | CRM objects and records |
| `service.hubspot` | Contacts, deals, companies |
| `service.youtube` | Video and channel data |
| `service.bluesky` | ATProto — posts and profile data |
| `service.mastodon` | ActivityPub — toots and timelines |
| `service.reddit` | Posts, comments, subreddits |
| `service.devto` / `service.hashnode` / `service.producthunt` / `service.discord` | Dev community platforms |

</details>

<details>
<summary><strong>🗄️ Database (4 nodes)</strong></summary>

`db.postgres` · `db.mysql` · `db.mongodb` · `db.redis`

</details>

<details>
<summary><strong>🌐 HTTP & Network (3 nodes)</strong></summary>

`http.request` · `http.ftp` · `http.ssh`

</details>

<details>
<summary><strong>🔧 Data Transformation (8 nodes)</strong></summary>

`data.datetime` · `data.crypto` · `data.html` · `data.xml` · `data.markdown` · `data.spreadsheet` · `data.compression` · `data.write_binary_file`

</details>

<details>
<summary><strong>🖼️ Image Processing (7 nodes)</strong></summary>

`image.info` · `image.resize` · `image.crop` · `image.thumbnail` · `image.convert` · `image.adjust` · `image.remove_background` (U2-Net AI)

</details>

<details>
<summary><strong>📣 Communication (12 nodes)</strong></summary>

`comm.email_send` · `comm.email_read` *(experimental)* · `comm.outlook_read` · `comm.outlook_send` · `comm.slack` · `comm.discord` · `comm.telegram` · `comm.twilio` · `comm.whatsapp` · `comm.bluesky` · `comm.mastodon` · `comm.reddit`

</details>

<details>
<summary><strong>🧠 AI, Gemini, System & People (17 nodes)</strong></summary>

| Node | Description |
|------|-------------|
| `ai.read_page` / `ai.extract_page` | AI-assisted page reading and structured extraction |
| `agent.ask` | Ask an agent runtime a question mid-workflow |
| `system.execute_command` | Run a local shell command, capture output |
| `system.rss_read` | Fetch items from RSS / Atom feeds |
| `people.save` | Upsert a contact into the CRM (profile-scoped) |
| `people.sync_outlook_message` | Sync an Outlook message into People history |

Also: `ai.agent` · `ai.chat` · `ai.classify` · `ai.embed` · `ai.extract` · `ai.transform` (LLM utilities), and `gemini.chat_session` · `gemini.chat_session_many` · `gemini.generate_image` · `gemini.generate_text` (Gemini via your own logged-in browser session — no API key).

</details>

<details>
<summary><strong>⏰ Triggers (3 types)</strong></summary>

| Trigger | Description |
|---------|-------------|
| `trigger.schedule` | Cron expression (6 fields: sec min hour dom month dow) — `0 0 9 * * *` every day at 9am |
| `trigger.webhook` | HTTP endpoint — fire workflow on POST |
| `trigger.manual` | One-click run from CLI or GUI |

</details>

<details>
<summary><strong>📱 Social platform actions — <em>opt-in build</em> (<code>-tags social</code>)</strong></summary>

Publish to and read **your own accounts** on these platforms via the Chrome extension bridge. These node types are **not compiled into the default binary** — build with `go build -tags social ./cmd/monoagentcli` to include them. They exist for managing your own presence; platform terms apply — see the [Usage Policy](docs/USAGE_POLICY.md).

| Platform | Available actions |
|----------|-------------------|
| **Instagram** | `publish_post` · `like_posts` · `comment_on_posts` · `reply_to_comments` · `like_comments_on_posts` · `send_dms` · `auto_reply_dms` · `follow_users` · `unfollow_users` · `engage_with_posts` · `engage_user_posts` · `find_by_keyword` · `watch_stories` · `export_followers` · `scrape_profile_info` · `extract_post_data` · `list_user_posts` · `list_post_comments` |
| **LinkedIn** | `publish_post` · `like_posts` · `like_comments` · `comment_on_posts` · `send_dms` · `auto_reply_dms` · `engage_with_posts` · `find_by_keyword` · `export_followers` · `scrape_profile_info` · `list_user_posts` · `list_post_comments` |
| **X (Twitter)** | `publish_post` · `engage_with_posts` · `send_dms` · `auto_reply_dms` · `find_by_keyword` · `export_followers` · `scrape_profile_info` |
| **TikTok** | `publish_post` · `like_video` · `comment_on_video` · `like_comment` · `follow_user` · `engage_with_posts` · `find_by_keyword` · `send_dms` · `auto_reply_dms` · `duet_video` · `stitch_video` · `share_video` · `export_followers` · `scrape_profile_info` · `list_user_videos` · `list_video_comments` |
| **Hacker News** | `get_post_metrics` · `list_comments` · `reply_to_comment` · `submit_post` |
| **Product Hunt** | `comment_on_launch` · `get_launch_metrics` · `list_comments` |

</details>

---

## CLI Reference

The binary is `monoagentcli`. Most commands accept `--json` for machine-readable output and `--profile <name>` to scope to a workspace.

<details>
<summary><strong>Workflow</strong></summary>

```bash
monoagentcli workflow list                          # list workflows (--json)
monoagentcli workflow get <id>                      # print a workflow as JSON
monoagentcli workflow create <name>                 # new blank workflow
monoagentcli workflow import --file flow.json       # import (also accepts stdin)
monoagentcli workflow export <id>                   # export as JSON
monoagentcli workflow validate <id>                 # validate against node schemas (exit 3 on invalid)
monoagentcli workflow run <id>                      # run and wait
monoagentcli workflow run <id> --dry-run            # validate + print execution plan, no run
monoagentcli workflow run <id> --no-wait            # print execution id, exit immediately
monoagentcli workflow run <id> --json               # execution record + per-node outputs
monoagentcli workflow run <id> --input '{"key":1}'  # inject trigger data
monoagentcli workflow activate <id>                 # enable triggers
monoagentcli workflow deactivate <id>               # disable triggers
monoagentcli workflow executions <id>               # run history
monoagentcli workflow search [query]                # search workflows & templates
monoagentcli workflow templates list                # bundled ready-to-use templates
monoagentcli workflow node add <id> --type core.filter --name Filter
monoagentcli workflow node set <id> <node-id> --config '{"max":5}'
monoagentcli workflow node remove <id> <node-id>
monoagentcli workflow connect <id> --from n1:main --to n2:main
```

</details>

<details>
<summary><strong>Nodes</strong></summary>

```bash
monoagentcli node list                              # all node types (--json)
monoagentcli node schema core.if                    # JSON schema for a node type
monoagentcli node run http.request \
  --config '{"method":"GET","url":"https://httpbin.org/get"}'
```

</details>

<details>
<summary><strong>Secrets & Connections</strong></summary>

```bash
monoagentcli secret add --kind secret --name aws \
  --field access_key_id=... --field secret_access_key=...   # values via flags or stdin
monoagentcli secret list                            # metadata only — never values
monoagentcli secret get <name>                      # resolve a vault reference (no plaintext)
monoagentcli secret update <name> / secret rm <name>
monoagentcli secret export                          # encrypted, passphrase-protected bundle
monoagentcli secret import <file>                   # restore on another machine
monoagentcli secret encrypt-connections             # one-time seal of legacy plaintext creds

monoagentcli login <platform>                       # browser session login (saved locally)
monoagentcli login status
monoagentcli connect <platform>                     # add an API credential
monoagentcli connect list / connect test <id> / connect remove <id>
```

</details>

<details>
<summary><strong>Human-in-the-Loop</strong></summary>

```bash
monoagentcli hil list                               # pending review items
monoagentcli hil approve <id>                       # resume the workflow
monoagentcli hil reject <id>                        # drop the item (workflow errors out)
```

</details>

<details>
<summary><strong>People (CRM)</strong></summary>

```bash
monoagentcli people list                            # contacts (--json)
monoagentcli people import --file people.json --platform linkedin   # JSON array format
monoagentcli people messages list <person-id>       # message history
monoagentcli people messages compose <person-id>    # AI-assisted draft
monoagentcli people messages send-draft <message-id>   # send an approved draft
monoagentcli people status set <person-id> "text"   # status timeline
```

</details>

<details>
<summary><strong>For AI agents</strong></summary>

```bash
monoagentcli mcp                  # MCP server over stdio — tools/list, workflow_run, hil_approve, …
monoagentcli ref                  # built-in offline docs: commands, nodes, expressions, examples
monoagentcli ref node core.if     # detailed docs for one node type
```

Full agent documentation: [AGENTS.md](AGENTS.md).

</details>

<details>
<summary><strong>Exit codes</strong></summary>

| Code | Meaning |
|------|---------|
| `0` | Success |
| `0` | Run paused at a human-in-the-loop node — status `WAITING` (paused for human review); the output carries a `hint` field pointing at `hil list` |
| `1` | General error (including a run that ends `CANCELLED`) |
| `2` | Not found — e.g. `hil approve`/`hil reject`, `secret rm`/`secret update`, or `workflow delete` on an unknown id |
| `3` | Invalid input / validation failure |
| `4` | Auth or connection failure |

</details>

<details>
<summary><strong>Scheduling</strong></summary>

```bash
monoagentcli schedule add <action-id> --cron "0 0 9 * * *"
monoagentcli schedule list
monoagentcli daemon                # keep all workflow triggers alive; blocks until Ctrl+C
```

Workflow triggers (`trigger.schedule`, `trigger.webhook`) only fire while a process is serving them — run `monoagentcli daemon` as a persistent background process and activated workflows fire on time, across all profiles.

</details>

---

## Chrome Extension

`chrome-extension/` lets workflow nodes drive **your real, already-logged-in Chrome browser** — the same model as consumer RPA tools. No separate automation profile, no re-authenticating for sites (like Google) that invalidate sessions ported into a scripted browser.

- **Loopback by default** — the bridge server binds loopback, and the extension refuses non-loopback servers. A per-session "Allow non-loopback server (unsafe)" checkbox in the popup overrides this for one save; it is never persisted
- **Unauthenticated channel (same-user trust)** — the bridge carries no authentication: any process on your machine running as your user can connect and drive the browser through it. Don't expose or port-forward the bridge port
- **Broad host permissions by design** — workflows can target any site, so the extension needs access to all tabs; it acts only when a `monoagentcli` process you started requests it
- **Shared connection** — multiple CLI processes share one extension connection instead of fighting over the browser

Pairing-based authentication between `monoagentcli` and the extension is planned and [tracked in issues](https://github.com/monoes/mono-agent/issues); until it lands, treat the channel as accessible to anything running as your user.

Install (unpacked, not on the Web Store):

1. Open `chrome://extensions`
2. Enable **Developer mode** (top-right toggle)
3. Click **Load unpacked** and select the `chrome-extension/` folder (or the zip from the [latest release](https://github.com/monoes/mono-agent/releases/latest))

No configuration needed. Run any browser node and look for `Chrome extension connected -- using your browser` in the output. After pulling changes that touch `chrome-extension/`, reload it from `chrome://extensions`.

---

## Getting Started

### Prerequisites

- Go 1.25+ (`brew install go`)
- Chrome/Chromium (for browser nodes — optional for everything else)
- That's it — SQLite is embedded, no external database

### Install

```bash
git clone https://github.com/monoes/mono-agent.git
cd mono-agent
go build -o monoagentcli ./cmd/monoagentcli

# Or with the social platform nodes (opt-in):
go build -tags social -o monoagentcli ./cmd/monoagentcli
```

Windows: download `monoagentcli-windows-amd64.exe` from [releases](https://github.com/monoes/mono-agent/releases/latest).

### Desktop GUI

```bash
cd wails-app
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails dev      # development mode
wails build    # production build
```

### Docker

```bash
docker compose up -d --build   # daemon + persistent /data volume

# Webhook triggers: the server binds 127.0.0.1:9321 by default —
# set the bind address so the published port is reachable:
MONOAGENT_WEBHOOK_ADDR=0.0.0.0:9321 docker compose up -d --build
```

Browser-based webhook callers additionally need `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS` (a comma-separated CORS allowlist; unset by default — no CORS headers are sent). See [docker-compose.yml](docker-compose.yml) and the env-var table in [AGENTS.md](AGENTS.md).

---

## Architecture

```
mono-agent/
├── cmd/monoagentcli/        # CLI entry point (Cobra)
│   ├── workflow.go          # workflow subcommands + engine builder
│   ├── node.go              # node run + registry
│   ├── ref.go               # built-in offline reference docs
│   ├── secret.go            # encrypted secrets vault CLI
│   ├── hil.go               # human-in-the-loop approve/reject
│   ├── mcp.go               # MCP server for AI agents (stdio)
│   └── ...
│
├── internal/
│   ├── workflow/            # Core workflow engine
│   │   ├── engine.go        # orchestration + profile isolation
│   │   ├── dag.go           # Kahn topological sort, cycle detection
│   │   ├── execution.go     # run state machine, on_error → SUCCESS_WITH_ERRORS
│   │   ├── expression.go    # {{template}} evaluation
│   │   ├── trigger_manager.go  # cron / webhook trigger lifecycle
│   │   ├── webhook_server.go   # loopback webhook HTTP server
│   │   ├── templates/       # bundled ready-to-use workflows
│   │   └── schemas/         # 90+ embedded JSON node schemas
│   │
│   ├── nodes/               # Node executors
│   │   ├── control/         # if, filter, set, code, human_in_loop…
│   │   ├── service/         # google_sheets, openrouter, github, stripe…
│   │   ├── comm/            # email, slack, telegram, outlook, discord…
│   │   ├── db/ · http/ · data/ · image/ · system/ · people/ · ai/
│   │   └── browser_adapter.go   # action.* nodes → opt-in social build
│   │
│   ├── mcp/                 # JSON-RPC 2.0 MCP server (no dependencies)
│   ├── secrets/             # keyring + AES-256-GCM envelope encryption
│   ├── vault/               # Image Vault — register/resolve/provenance
│   ├── connections/         # unified credential storage + OAuth flows
│   ├── bot/                 # platform browser adapters (build tag: social)
│   ├── extension/           # Chrome extension bridge server (loopback :9222)
│   ├── ai/chat/             # AI Canvas Chat — conversational builder
│   └── scheduler/ · config/ · storage/
│
├── wails-app/               # Desktop GUI (Wails 2 + React)
├── examples/                # ready-to-run workflow JSONs
├── docs/                    # usage policy, comparison, screenshots
└── data/actions/            # embedded action definitions
```

---

## Docs & Resources

| Resource | What's inside |
|----------|---------------|
| [AGENTS.md](AGENTS.md) | Canonical entrypoint for AI agents: `ref`, `--json`, MCP, exit codes |
| [docs/USAGE_POLICY.md](docs/USAGE_POLICY.md) | Scope of use, platform ToS, rate caps, anti-spam commitments |
| [docs/COMPARISON.md](docs/COMPARISON.md) | Honest comparison vs n8n, Activepieces, Windmill, Node-RED |
| [FEATURE_n8n.md](FEATURE_n8n.md) | detailed n8n feature map used as our porting reference |
| [examples/](examples/) | Ready-to-run workflow JSONs with webhook trigger examples |
| [install.sh](install.sh) | One-line installer (macOS / Linux) |
| [SECURITY.md](SECURITY.md) | Reporting, supported versions, telemetry & crash-reporting statement |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Build/test commands (incl. `-tags social`), PR guidelines |
| [CHANGELOG.md](CHANGELOG.md) | Release history |
| [docs/screenshots/](docs/screenshots/) | GUI screenshots |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| **Language** | Go 1.25 (zero CGO) |
| **Database** | [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — pure Go, embedded |
| **CLI** | [spf13/cobra](https://github.com/spf13/cobra) |
| **Logging** | [rs/zerolog](https://github.com/rs/zerolog) |
| **Scheduling** | [robfig/cron](https://github.com/robfig/cron) |
| **JS Engine** | [dop251/goja](https://github.com/dop251/goja) — for `core.code` |
| **Browser** | [go-rod/rod](https://github.com/go-rod/rod) — Chrome DevTools Protocol |
| **Keyring** | [zalando/go-keyring](https://github.com/zalando/go-keyring) — OS secret storage |
| **Desktop GUI** | [Wails v2](https://wails.io) + React |
| **AI APIs** | OpenRouter · HuggingFace · Google Gemini |

---

## Roadmap

**Recently shipped**
- [x] Multi-profile workspaces — all user data scoped per named profile
- [x] Human-in-Loop node (`core.human_in_loop`) — durable, editable, timeout-aware
- [x] Image Vault — storage, labeling, fullscreen editor, provenance
- [x] AI Canvas Chat — conversational workflow builder
- [x] Outlook integration — read and send email via Microsoft Graph
- [x] Encrypted Secrets Vault — OS keyring + AES-256-GCM + portable encrypted export
- [x] MCP server — AI agents can list, run, and validate workflows
- [x] Bluesky/Mastodon publishing nodes (`comm.bluesky`, `comm.mastodon`) — official APIs (ATProto/ActivityPub)

**Coming next**
- [ ] More trigger types — email, file watcher, database change
- [ ] Workflow versioning and rollback
- [ ] Sub-workflow / reusable workflow node
- [ ] Visual debugger — step-through execution in GUI
- [ ] Marketplace — shareable workflow templates
- [ ] MCP registry listing + agent tool marketplace
- [ ] Metrics dashboard — success rates, throughput, latency per profile

---

## Contributing

Pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). By participating, you agree to keep Mono Agent a fair-use tool.

```bash
go test ./...                  # unit tests (no Chrome needed)
go test -tags "integration,social" ./... # integration tests (requires Chrome)
go vet ./...                   # lint
```

---

## License

Mono Agent is released under the [MIT License](LICENSE). In plain English:

- ✅ You may **use** it, commercially or personally
- ✅ You may **modify** it and build your own tools on it
- ✅ You may **distribute** copies and modified versions
- ❌ It comes with **no warranty** — the authors are not liable for anything it does or fails to do
- 📋 Keep the license and copyright notice with any copy you distribute

---

<div align="center">

**Mono Agent is in no way affiliated with Instagram, LinkedIn, X, TikTok, or any platform.<br/>Independent, unofficial, MIT-licensed. Use at your own risk and in accordance with each platform's terms.**

Made with ☕ by [nokhodian](https://github.com/nokhodian)

</div>
