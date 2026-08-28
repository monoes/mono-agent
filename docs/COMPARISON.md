# Mono Agent vs. the field

An honest comparison. Where we lose, we say so — you're going to find out
anyway, and a comparison that never loses isn't a comparison, it's an ad.

The short version: **n8n, Activepieces, Windmill, and Node-RED are mature
tools with large communities and, in n8n's case, an enormous integration
catalog. Mono Agent is a young project that trades breadth for a different
shape: one static binary, fully local data, a CLI an AI agent can actually
drive, and human approval built into the engine.**

## Comparison table

| | **Mono Agent** | **n8n** | **Activepieces** | **Windmill** | **Node-RED** |
|---|---|---|---|---|---|
| **License** | MIT | Sustainable Use ("fair-code", not OSI) + Enterprise | MIT core + commercial EE | AGPL-3.0 / Apache-2.0 (source builds; binary "Community Edition" contains proprietary code) | Apache-2.0 (OpenJS Foundation) |
| **Runtime** | Single static Go binary. No Node, no Docker, no DB server — SQLite is built in | Node.js app + database, typically Docker | Node.js/Bun + Postgres + Redis, Docker | Rust backend + **required Postgres**, language runtimes, Docker/K8s | Node.js process (`npm i -g node-red`) |
| **Local-first data** | SQLite + files under `~/.monoagent/`, works fully offline, no analytics/telemetry (opt-in crash reporting aside — see [SECURITY.md](../SECURITY.md)) | Self-hosted or cloud; data stays on your host when self-hosted | Self-hosted or cloud | Self-hosted (needs Postgres) or cloud | Runs local, flows stored as JSON files |
| **GUI** | Desktop app (Wails) + web canvas | Web canvas | Web builder | Web IDE + app builder | Web editor |
| **CLI-first** | 70+ commands, `--json` everywhere, documented exit codes, offline `ref` manual | `n8n` CLI (start/import/export; limited workflow ops) | CLI mainly for piece development | `wmill` CLI (sync, run) | Minimal (admin only) |
| **AI-agent native** | MCP server over stdio, `AGENTS.md`, offline reference manual, HIL queue approvable by agents | MCP trigger/client nodes, AI workflow nodes | Pieces exposed as MCP servers (not verified here) | AI code generation; MCP support not verified here | — |
| **Human-in-the-loop primitive** | Core `core.human_in_loop` node; durable queue in SQLite **survives restarts**; per-item edit/approve/reject; timeout auto-reject | Approval steps / Wait node patterns | Delay/approval pieces | Not a core primitive | — (roll your own) |
| **Encrypted secrets vault** | Built in: OS-keyring-wrapped key, AES-256-GCM payloads, `@secret:` references never expose plaintext in configs/logs | Credentials in n8n's store (encrypted at rest) | Similar per-piece credentials | Workspace K/V with per-workspace encryption key | Credential nodes stored locally |
| **Node count (honest)** | 90 node types in the default build; 150 with the opt-in `-tags social` build | 1500+ integrations | 200+ pieces (community-contributable) | Scripts in 10+ languages + hub | Thousands of community palette nodes |
| **Extensibility** | Write nodes in Go | Write nodes in TypeScript/JS | Pieces in TypeScript (npm) | Scripts in Python/TS/Go/Bash/Rust/... | Nodes in JS |
| **Community & maturity** | **Young project, small community — we lose this row today** | ~203k stars, ~60k forks, 9,000+ templates, huge forum | ~24k stars, active Discord | ~18k stars, commercial backing | Est. 2013, OpenJS governance, massive install base |

Figures for competitor projects were taken from their GitHub repositories in
August 2026 and will drift; check them before quoting.

## When to use n8n instead

Often, honestly. Use n8n if you:

- **Need the integration catalog.** 1500+ integrations vs. our 90 nodes.
  If your stack touches many SaaS products, n8n simply has the connectors.
- **Want a hosted/cloud option** with someone else handling upgrades,
  availability, and scaling. Mono Agent is self-run by design.
- **Work in a team** that needs RBAC, SSO, audit trails, and collaboration
  features. n8n's enterprise tier exists for exactly this.
- **Want thousands of ready-made templates** (9,000+) and a large community
  forum to search when stuck.
- **Rely on a rich JS/TS ecosystem** for custom nodes.

Choosing n8n here is a correct decision, not a failure.

## When Mono Agent wins

- **A single binary on a laptop or any random box.** No Node, no Docker, no
  Postgres, no Redis — copy one static file, run it. That machine in the
  closet with nothing installed? It works there.
- **Fully local data, no telemetry by default.** Everything in SQLite and
  files under `~/.monoagent/`. Nothing phones home on its own; the only
  network traffic is the API calls your own workflows make, commands you
  explicitly invoke that talk to an external service, and opt-in crash
  reporting (see [SECURITY.md](../SECURITY.md)).
- **CLI- and agent-driven automation.** 70+ commands with `--json` output,
  documented exit codes, an offline `ref` manual, and a stdio MCP server.
  An AI agent can discover, validate, run, and inspect workflows without
  touching a GUI — and handle human-approval queue items on your behalf.
- **Human approval as an engine primitive.** `core.human_in_loop` is a
  durable queue that survives restarts, supports per-item editing, and
  auto-rejects on timeout — not a bolt-on pattern.
- **Encrypted secrets vault.** AES-256-GCM payloads, key wrapped in the OS
  keyring, secrets referenced as `@secret:name` and never printed to logs,
  configs, or `--json` output.
- **A native desktop app** (Wails) when you want a GUI without running a
  web stack.

## Ongoing n8n parity tracking

We maintain a detailed feature-by-feature map of n8n — 5,780 lines covering
nodes, triggers, expressions, credentials, and behavior — as the working
parity tracker for what Mono Agent covers and what it doesn't yet:
**[FEATURE_n8n.md](../FEATURE_n8n.md)**. If a comparison claim here and that
document ever disagree, trust the tracker and file an issue.

---

*This document compares Mono Agent with n8n, Activepieces, Windmill, and
Node-RED solely to help you pick the right tool. No affiliation with or
endorsement by any of those projects is implied.*
