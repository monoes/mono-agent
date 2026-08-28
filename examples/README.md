# Example Workflows

Ten ready-to-import, structurally valid workflow JSONs. None of them use
social-platform nodes — they work with the default build
(`go build ./cmd/monoagentcli`, no `-tags social`).

Every node `type` matches a schema in `internal/workflow/schemas/` and every
config key matches that schema's fields. Values marked
`REPLACE_WITH_*` (and empty `credential_id`s) must be filled in before a run
will succeed — `workflow import` and `--dry-run` work without them.

## Catalog

| File | What it does | Trigger | Nodes |
|---|---|---|---|
| `morning-briefing.json` | Weekday 7am: RSS → filter → AI summary → human review → email digest | `trigger.schedule` | `system.rss_read` → `core.filter` → `service.openrouter` → `core.human_in_loop` → `comm.email_send` |
| `sheets-to-gmail.json` | Read pending rows from a Sheet and send a follow-up email per row | `trigger.manual` | `service.google_sheets` → `core.filter` → `comm.email_send` |
| `http-to-slack.json` | JSON POST in → formatted message → Slack channel | `trigger.webhook` | `core.set` → `comm.slack` |
| `github-issues-to-linear.json` | Every 6h: mirror new GitHub issues into Linear | `trigger.schedule` | `service.github` → `service.linear` |
| `webhook-to-postgres.json` | JSON event in → normalize → insert row into Postgres | `trigger.webhook` | `core.set` → `db.postgres` |
| `stripe-events-to-sheets.json` | Stripe-style event in → append row to a Sheets ledger | `trigger.webhook` | `service.google_sheets` |
| `image-gen-vault.json` | Generate an image with HuggingFace, lands in the local image vault | `trigger.manual` | `service.huggingface` |
| `outlook-inbox-sync.json` | Hourly: sync Outlook inbox + sent mail into People (adapted from the bundled template) | `trigger.schedule` | `service.outlook_mail` → `people.sync_outlook_message` (×2 branches) |
| `telegram-notify.json` | Every morning: RSS items pushed as Telegram bot messages | `trigger.schedule` | `system.rss_read` → `comm.telegram` |
| `data-pipeline.json` | Fetch API JSON → reshape in code → write CSV → sum totals | `trigger.manual` | `http.request` → `core.code` → `data.spreadsheet` → `core.aggregate` |

## Quickstart

```bash
# 1. Import any example (prints the new workflow id):
monoagentcli workflow import --file examples/http-to-slack.json

# 2. Check it without running anything:
monoagentcli workflow validate --file examples/http-to-slack.json
monoagentcli workflow run <id> --dry-run

# 3. Activate the workflow so its triggers are served:
monoagentcli workflow activate <id>

# 4. Start the daemon AFTER activation (or restart it) and keep it running:
monoagentcli daemon
```

Note: an activated workflow's triggers are only served by a daemon started
(or restarted) **after** the `workflow activate` step — if the daemon was
already running, restart it.

## Firing the webhook examples

The daemon serves webhook triggers at `http://127.0.0.1:9321/webhook/<path>`
(default bind is loopback-only — see `internal/workflow/engine.go`). The
`<path>` segment is the trigger node's `path` config value, e.g. `"alerts"`
below.

First activate the workflow (`monoagentcli workflow activate <id>`), then
run the daemon — triggers activated while a daemon is already running need
a daemon restart before they fire.

With `monoagentcli daemon` running in another terminal:

```bash
# http-to-slack.json — path "alerts", requires the X-Webhook-Secret header:
curl -s http://127.0.0.1:9321/webhook/alerts \
  -H 'Content-Type: application/json' \
  -H 'X-Webhook-Secret: REPLACE_WITH_SECRET' \
  -d '{"event":"deploy_failed","severity":"high","source":"ci"}'

# webhook-to-postgres.json — path "events", no auth configured:
curl -s http://127.0.0.1:9321/webhook/events \
  -H 'Content-Type: application/json' \
  -d '{"event":"signup","userId":"u_123","data":{"plan":"pro"}}'

# stripe-events-to-sheets.json — path "stripe":
curl -s http://127.0.0.1:9321/webhook/stripe \
  -H 'Content-Type: application/json' \
  -H 'X-Webhook-Secret: REPLACE_WITH_SECRET' \
  -d '{"id":"evt_1","type":"payment_intent.succeeded","amount":4200,"currency":"usd"}'
```

A `{"success":true}` response means the workflow was triggered; check the
execution with `monoagentcli workflow executions <id>`.

## Notes

- **Credentials**: nodes with a `credential_id` field need a connection first —
  see `monoagentcli ref connections`. Secrets can be piped in via stdin
  (`monoagentcli secret add --kind secret --name ...`), never argv.
- **Cron format** is 6-field (seconds first): `0 0 7 * * 1-5` = weekdays 07:00.
- **Docker**: see `docker-compose.yml` in the repo root for running the daemon
  as a container with persistent state.
