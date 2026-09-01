# HTTP API quickstart

`monoagentcli httpapi` starts a REST/JSON server for external agents that
can't speak the stdio MCP protocol (`monoagentcli mcp`). Read-only by
default; loopback-only bind (`127.0.0.1:9322`). Full endpoint list:
`internal/httpapi/openapi.yaml`.

## Start the server

```bash
monoagentcli httpapi
# HTTP API listening on 127.0.0.1:9322 (mutations disabled — read-only). Press Ctrl+C to stop.
```

The bearer token is generated on first start and stored in the active
profile's secrets vault:

```bash
monoagentcli secret list | grep httpapi-token
```

There's no CLI command to print a stored secret's value directly (by
design — see `ref connections`); read it via the vault UI, or `secret
export` for a scripted flow.

## List workflows, then run one

With the token in `$TOKEN`:

```bash
# 1. List saved workflows
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:9322/workflows | jq .

# [
#   { "id": "wf-001", "name": "Morning briefing", "active": true, "node_count": 5 }
# ]

# 2. Run one (mutating endpoints need --allow-mutations on the server)
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"input": {"prompt": "a red bicycle"}, "timeout_seconds": 60}' \
  http://127.0.0.1:9322/workflows/wf-001/run | jq .

# {
#   "execution_id": "exec-042",
#   "status": "SUCCESS",
#   "nodes": [ ... ]
# }
```

Output items are redacted the same way as `workflow run --json` and the
MCP tools (credential-shaped keys like `token`/`password`/`api_key` are
masked). To see unredacted values for one call, add
`-H "X-Full-Outputs: 1"`, mirroring `workflow run --full-outputs`.

## Unauthenticated / unauthorized requests

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9322/workflows
# 401

curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer wrong" http://127.0.0.1:9322/workflows
# 401

curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9322/health
# 200 — /health is the only unauthenticated route
```
