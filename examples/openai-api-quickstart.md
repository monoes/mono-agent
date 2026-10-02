# OpenAI-compatible API quickstart

`monoagentcli` can serve the agent runtimes installed on your machine
(claude, codex, antigravity, …) through standard OpenAI endpoints, so any
OpenAI SDK or tool works by changing its base URL and key. This guide
creates a key, starts the server, calls it with curl and the SDKs, and runs
it on a headless Linux server. Paths and schemas:
`internal/httpapi/openapi.yaml`. Threat model: `SECURITY.md`.

Today it serves `GET /v1/models`, `GET /v1/models/{id}` and
`POST /v1/chat/completions` (JSON and `"stream": true`). Images and tool
calling are not available yet. The model `auto` lets Jev pick the model of
each request once you switch it on (see "Let Jev pick" below); until then
asking for it is a 404 `model_not_found` that says what is missing. A `404 page
not found` in plain text instead means the server running is older than this
API and has no `/v1` at all (`monoagentcli daemon` and `httpapi` serve it only
from a build that includes it).

## 1. Create a key

```bash
monoagentcli api key create --name my-app
# sk-ma-…            ← printed once; only its SHA-256 is stored
```

A key belongs to the active profile (`--profile`, or `profile switch`): its
requests run as that profile, add only that profile's knowledge and work in that
profile's folder (what a runtime can read on the machine is set by its
confinement class: see `SECURITY.md`). The commands below read the key from
`$KEY`. In a script, stdout is the key alone, so capture it instead (a name
is unique among a profile's active keys: use this form or the one above, not
both):

```bash
KEY=$(monoagentcli api key create --name my-app) && export KEY
```

Add `--context` to give requests made with the key excerpts of the profile's
own knowledge (its documents and captures); without it the key reaches a
plain model. By default a context key is served only by the chat-only models
(see `api models`), because the excerpts include captured web pages that
nobody vetted and a runtime with native tools could be steered by them. When
you want a coding agent on your own machine to have your notes, raise the
limit on purpose when you start the server: `--context-confinement
sandboxed` (or `any`, or `MONOAGENT_API_CONTEXT_CONFINEMENT`). It never goes
above what the listener itself serves. Manage keys with `api key list`,
`show`, `update` and `revoke` (revoking takes effect on the next request). No
GUI and no keyring are needed.

## 2. Start the server

```bash
monoagentcli httpapi        # or: monoagentcli daemon
```

The API is served at `http://127.0.0.1:9322/v1` while the HTTP API listener
is loopback, which is the default. Only one process per home (`~/.monoagent`)
serves `/v1` at a time, because the working folders are emptied around every
turn: a second `httpapi` says so and serves its other routes without `/v1`, and
a second `daemon` is refused outright (one daemon per home). Check what it
serves:

```bash
monoagentcli api status     # listeners, key count, whether they answer
monoagentcli api models     # every model, with its confinement class
```

`api models` prints the policy, a note on whose settings those are, and then
one row per model (shortened here; the ids come from each runtime's own model
list, so yours will differ):

```
Confinement policy for a loopback listener: any (keys created with --context: chat-only)
From this shell's flags and environment: a running server may be set up differently (`monoagentcli api status` shows what a running daemon applies).

MODEL                    CONFINEMENT  VALIDATED  SERVED  CONTEXT KEY
claude/default           chat-only    false      yes     yes
claude/sonnet            chat-only    false      yes     yes
codex/gpt-6-astra        sandboxed    false      yes     no
antigravity/default      unconfined   false      yes     no
```

`api models` works out what a listener with the flags and environment of
*this shell* would serve; it does not ask a running server (`api status`
shows what a running daemon applies). `SERVED` is whether the model is served
under that policy and `CONTEXT KEY` whether a key created with `--context`
may use it. The ids are `<runtime>/<model>`, as each runtime lists them, so
use the ones this prints. A bare runtime (`codex`) is its default model, and
`agy` is accepted for `antigravity`. A model that is neither listed by the
runtime nor in your agent roster is a 404, although a runtime's aliases (such
as `claude/opus[1m]`) resolve without being listed. If a runtime's own listing
fails, a built-in list stands in for it until a later listing works, so its
ids can differ meanwhile. The listing is tried again after 30 seconds, then
after 1, 2 and 4 minutes, and every 5 minutes after that, each time a request
arrives.

## 3. Call it

```bash
curl -s http://127.0.0.1:9322/v1/models -H "Authorization: Bearer $KEY" | jq '.data[].id'

curl -s http://127.0.0.1:9322/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"claude","messages":[{"role":"user","content":"Say hello in five words."}]}' \
  | jq -r '.choices[0].message.content'

# streaming: server-sent events, ending with "data: [DONE]"
curl -sN http://127.0.0.1:9322/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"codex","stream":true,"messages":[{"role":"user","content":"Count to five."}]}'
```

Python:

```python
import os

from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:9322/v1", api_key=os.environ["KEY"])

reply = client.chat.completions.create(
    model="claude", messages=[{"role": "user", "content": "Say hello in five words."}]
)
print(reply.choices[0].message.content)

for chunk in client.chat.completions.create(
    model="codex", stream=True, messages=[{"role": "user", "content": "Count to five."}]
):
    print(chunk.choices[0].delta.content or "", end="")
```

JavaScript:

```js
import OpenAI from "openai";

const client = new OpenAI({ baseURL: "http://127.0.0.1:9322/v1", apiKey: process.env.KEY });
const reply = await client.chat.completions.create({
  model: "claude",
  messages: [{ role: "user", content: "Say hello in five words." }],
});
console.log(reply.choices[0].message.content);
```

What to expect:

- Each request is one agent turn on your own subscription or account:
  a few seconds to a minute, and billed or rate limited like any use of that
  runtime. There are no per-key quotas; at most 4 turns run at once (a full
  server answers 429 with `Retry-After`).
- Requests are stateless. Sampling parameters (`temperature`, `max_tokens`,
  `stop`, …) are accepted and ignored, because the agent runtimes have none.
  `n` above 1, a non-empty `tools`, `logprobs: true`, `response_format` of
  type `json_schema`, tool messages and parts that are not text (images, audio,
  files) are rejected with 400 `unsupported_parameter`
  (`response_format: {"type":"json_object"}` works, as a best-effort
  instruction; `tools: []` and `tool_choice: "none"` are accepted).
- `curl -i` shows `X-Monoagent-Model` (the model that answered),
  `X-Monoagent-Sandbox` (how monomind sandboxed that turn: `sandboxed`,
  `scoped`, `unsupported`, …; sent on a successful non-streaming response
  only), for a `--context`
  key, `X-Monoagent-Context` (how many knowledge excerpts were added) and, for
  the model `auto`, `X-Monoagent-Auto` (who picked the model).

### Let Jev pick: the `auto` model

With `"model": "auto"`, [TypeSafe Jev](https://docs.typesafe.ai/api) chooses the
model of each request among the ones this listener serves: a cheaper, faster
one for a simple prompt, a stronger one for a hard or long one. It is off until
you switch it on for the key's profile, and it needs that profile's Jev key:

```bash
printf '%s' "$TYPESAFE_KEY" | monoagentcli jev key set   # or TYPESAFE_API_KEY in the server's environment
monoagentcli jev enable api_auto    # prints what is sent to TypeSafe, then asks (--yes in a script)
monoagentcli api models             # its last line says whether auto works
```

On a headless server, where the vault needs the file keyring and its passphrase
file, `TYPESAFE_API_KEY` in the server's environment is the simple way: the
server's agent turns do not inherit it. `api models` and `api status` run in
your shell, so for a key from the environment they report this shell's, not the
server's.

`jev enable` lists what leaves the machine: to TypeSafe, besides the prompt
going to the runtime that answers, the first 4,000 characters of the last user
message and the names, descriptions and validated cost and latency of the
models Jev picks among. Then:

```bash
curl -si http://127.0.0.1:9322/v1/chat/completions \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"auto","messages":[{"role":"user","content":"What is 17 * 23?"}]}' \
  | grep -iE '^(HTTP|x-monoagent-(model|auto))'
```

The response's `model` and `X-Monoagent-Model` name the pick, and
`X-Monoagent-Auto` says who chose: `jev`, or `rule` when Jev could not (no
answer within 8 seconds, an answer that is not one of the options, or one under
the surface's threshold, which `jev enable api_auto --threshold 0.8` raises) or
when only one model was left to pick. Of the models that passed
`monoagentcli agent validate` (which records the cost and latency to compare),
the rule takes the most confined, then the cheapest, then the fastest; with none
validated, a runtime's default model, claude first.

Jev picks only among models the key may use: under `--confinement chat-only`, or
for a `--context` key held to its own cap, a model above the cap is not an
option. Mind that on loopback the default policy allows every runtime, so a
prompt can steer `auto` to an unconfined one (see `SECURITY.md`): start the
server with `--confinement chat-only` or `sandboxed` to keep `auto` within a
class. `GET /v1/models` lists `auto` after the other models, so a client that
takes the first one is not moved to it, and leaves it out while it does not work;
`monoagentcli api status` says what is missing.

## 4. Serve it beyond this machine

The main listener serves `/v1` only on loopback. To reach it from another
machine, give it its own listener. It serves nothing but `/v1` and `/health`
and, off loopback, is TLS only:

```bash
export MONOAGENT_API_TLS_CERT=/etc/monoagent/fullchain.pem
export MONOAGENT_API_TLS_KEY=/etc/monoagent/privkey.pem
monoagentcli daemon --v1-addr 0.0.0.0:9443
```

- Without a certificate in the environment, a self-signed one valid for
  `localhost` only is generated and cached under `~/.monoagent/api-tls/`;
  remote clients must trust it explicitly (`curl -k` for a test). For real use
  set the two variables, or terminate TLS in a reverse proxy.
- An off-loopback listener defaults to `--confinement chat-only`, so it
  lists and serves only the chat-only runtimes (claude here).
  `--confinement sandboxed` adds the sandboxed ones (codex and copilot here)
  and `--confinement any` adds the rest. Read `SECURITY.md` first: a key then
  lets its holder use that runtime's native tools on this machine.
- Behind a reverse proxy, bind the listener to loopback
  (`--v1-addr 127.0.0.1:9443`) and set `--confinement` explicitly, since a
  loopback bind defaults to `any`. `--confinement` is one value for the whole
  process, so it limits the loopback main listener too. Unset the two
  certificate variables for a proxy that forwards plain HTTP: while they are
  set, the listener speaks TLS even on a loopback bind. Turn proxy buffering
  off for streaming (the server already sends `X-Accel-Buffering: no`).

## 5. A headless Linux server

1. Install monomind and the agent CLIs you want to serve, and sign them in as
   the user that will run the service. `monoagentcli doctor` shows what is
   missing. Use a dedicated unprivileged OS user: a key is the runtime's
   capabilities, and a runtime with a shell runs it as that user.
2. Create a key: `monoagentcli api key create --name app`. It needs no
   keyring and no GUI.
3. Install the service: `monoagentcli daemon install` writes the systemd user
   unit `monoagent-daemon.service`. The unit has no environment or flags, so
   add a drop-in that survives a reinstall:

   ```bash
   systemctl --user edit monoagent-daemon
   ```

   ```ini
   [Service]
   Environment=MONOAGENT_API_V1_ADDR=0.0.0.0:9443
   Environment=MONOAGENT_API_TLS_CERT=/etc/monoagent/fullchain.pem
   Environment=MONOAGENT_API_TLS_KEY=/etc/monoagent/privkey.pem
   Environment=MONOAGENT_API_CONFINEMENT=chat-only
   ```

   If monomind or an agent CLI is not found under systemd's minimal `PATH`, add
   an `Environment=PATH=…` line too. `MONOAGENT_API_CONFINEMENT` is one value
   for the whole process, so `chat-only` here also limits `/v1` on the
   daemon's loopback main listener. Then `systemctl --user restart
   monoagent-daemon`, and as root `loginctl enable-linger <user>` so the
   service starts at boot. Logs: `journalctl --user -u monoagent-daemon -f`.
4. Check that the listener came up: `monoagentcli api status`, run as the same
   user, lists it and says whether it answers `/v1`. The daemon only logs a
   warning when it cannot start the listener (an unreadable certificate, a
   port in use) and keeps running, so the unit looks healthy either way. A
   listener it could not start is missing from the list, and `api status` says
   so (`v1 none: the daemon reports no dedicated /v1 listener …`): read
   `journalctl --user -u monoagent-daemon` for the reason.
5. From another machine: `curl https://server:9443/v1/models -H "Authorization:
   Bearer $KEY"`.

## Errors

| Status | `code` | Meaning |
|---|---|---|
| 400 | `invalid_json`, `invalid_value`, `missing_required_parameter`, `unsupported_parameter` | The body is not JSON, or a parameter is missing, invalid or not supported (non-empty `tools`, `n > 1`, `json_schema` output, parts that are not text, …) |
| 401 | `invalid_api_key` | Missing, unknown or revoked key. The legacy HTTP API token is not a key |
| 403 | `policy_denied` | A completion names a model whose confinement class is above the listener's `--confinement`, or above `--context-confinement` for a key created with `--context`; or its sandbox could not be applied; or the runtime started with less confinement than the policy allows |
| 404 | `model_not_found` | Unknown model. `GET /v1/models/{id}` also answers 404 for a model the listener does not serve (a completion for it is a 403). `auto` is a 404 too while it is not set up for the key's profile: the message says what is missing (the `api_auto` Jev surface, a Jev key, or a model the policy allows) |
| 413 | `request_too_large` | Body over 2 MiB |
| 429 | `rate_limit_exceeded`, `insufficient_quota` | The server is full (`Retry-After: 2`), or the runtime is rate limited or out of quota |
| 500 | `internal_error` | An internal failure. The message is generic; the detail is in the server log under the response's `X-Request-Id` |
| 502 | `runtime_error` | The runtime reported an error, or ended the turn without finishing it. The message is generic too, and the runtime's own words reach you only for a sign-in hint, a rate limit, quota or a timeout, on one line and at most 300 characters |
| 503 | `runtime_not_available` | monomind or the runtime is not installed or not signed in, or the server is shutting down |
| 504 | `timeout` | The turn exceeded 10 minutes (`MONOAGENT_API_TURN_TIMEOUT`) |

Every error of the three routes has the body
`{"error":{"message","type","param","code"}}` and an `X-Request-Id` header. A
path or method the API does not have (for example `GET /v1/embeddings`) gets
Go's plain-text 404 or 405 instead. A stream starts (status 200) on its first
content or after 5 seconds of silence, and from then on an error arrives as a
`data: {"error": …}` event followed by `data: [DONE]`, not as a status from
this table: a runtime that does not stream (codex, for one) is usually past
that point before it fails.
