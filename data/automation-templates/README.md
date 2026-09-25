# Automation package templates

`monoagentcli automation new <id> --template <name>` copies one of these
directories and fills in its placeholders. Each template is a complete,
valid package (see the package format in
`docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md` §4)
that uses **declarative steps only** — no `call_bot_method`, no scripts.

| Template | What it shows | Actions | Extras |
|---|---|---|---|
| `basic` | the smallest useful package: navigate, wait, read | `get_page_title` (read) | fragment `dismiss_banner` |
| `login-form` | a username/password login and the manifest `login` block | `log_in` (write) | `secret` input |
| `list-scrape` | `extract_table` over a list, cleaned with `transform` | `list_items` (read) | fixture + expected output |
| `post-content` | fill a form and publish, with `sideEffect` and `until` | `create_post` (write) | fragment `dismiss_banner` |
| `search-and-extract` | type a query, `press_key`, extract the results | `search` (read) | fixture + expected output |

## Placeholders

The scaffolder replaces exactly these four tokens, in every file of the
template (file names never contain placeholders):

| Placeholder | Value | Example |
|---|---|---|
| `{{id}}` | the package id given to `automation new` | `acme-crm` |
| `{{name}}` | display name (`--name`, default: the id) | `Acme CRM` |
| `{{startUrl}}` | the site's start URL, **ending in `/`** (templates append paths such as `login` or `new`) | `https://app.acme-crm.com/` |
| `{{domain}}` | the start URL's host | `app.acme-crm.com` |

Rules for the scaffolder:

- In `.json` files each value is inserted JSON-string-escaped (the
  placeholders only ever appear inside JSON strings), so every file stays
  valid JSON. `TestTemplatesSubstitute` in `internal/schemagen` checks this.
- Everything else in double braces — `{{query}}`, `{{rawItems}}`,
  `{{username}}` — is a run-time variable and must be left alone. For that
  reason no template uses `id`, `name`, `startUrl` or `domain` as an input
  or variable name.

## Layout of a template

```
<template>/
├── automation.json          manifest ("$schema" → automation.v1)
├── actions/<action>.json    ("$schema" → action.v1)
├── fragments/<name>.json    optional ("$schema" → fragment.v1)
├── selectors.json           named selectors, referenced by configKey
├── tests/fixtures/<action>.html   optional DOM snapshot
├── tests/<action>.expect.json     expected extraction output for it
├── tests/<action>.inputs.json     optional inputs for the fixture run
├── tests/<action>.routes.json     optional per-URL fixtures (multi-page actions)
└── README.md
```

`selectors.json` has no `"$schema"` key (every top-level key is a selector
name); point your editor at `data/schemas/selectors.v1.schema.json` for it.
The `"$schema"` URLs in the other files point at the schemas on the
`master` branch, so a scaffolded package validates wherever it lives.

## Fixture tests (`automation test`)

`monoagentcli automation test <id|dir> [action] [--full]` runs every test of
a package in a headless browser, through the real action engine. Nothing
leaves the machine: every request the page makes is answered in-process.

A test is named `<name>` = `<action>` or `<action>.<variant>`, and exists
when `tests/<name>.expect.json` exists (or `tests/fixtures/<name>.html` for
an action of the package). Its files:

| File | Purpose |
|---|---|
| `tests/fixtures/<name>.html` | the page served for every document URL no route matches |
| `tests/<name>.routes.json` | optional: `[{"match":"<regexp on URL>","fixture":"<file>","status":200}]`, first match wins. `fixture` is a file in `tests/fixtures/`, or a path under `tests/` when it contains a `/` (e.g. `pages/submit.html`). `status` defaults to 200. A route with `"after":"<regexp>"` applies only once a recorded non-GET request's URL matched it — the page as it looks after a form post; list it before the plain route for the same URL. |
| `tests/<name>.inputs.json` | optional: the action's inputs, e.g. `{"query":"widgets"}`. A required input not given here is taken from the same-named field of the first expected record. |
| `tests/<name>.expect.json` | the expectation (below) |

Non-document requests (scripts, images, XHR) get an empty body. Every
request other than GET/HEAD/OPTIONS is recorded with its method, URL and
body (a form POST's body is `a=1&b=2`).

`expect.json` is either the expected records — a list, one record, or
`{"<output>": [records]}` — or an object with `requests`:

```json
{
  "records": [ { "id": "123" } ],
  "requests": [
    { "method": "POST", "urlMatches": "/comment$", "bodyContains": "text=Nice+post" }
  ]
}
```

`records` is optional there; each `requests` entry must match at least one
recorded request (`method` case-insensitive, `urlMatches` a Go regexp,
`bodyContains` a substring; each field optional).

By default the run is in **safe mode** and stops before the first step
marked `sideEffect` — such a test reports `skipped`. `--full` runs past side
effects, which is safe here because the "site" is the fixtures: use it for
write actions (post, reply, submit) together with `requests` assertions.
A test is `pass`, `fail` or `skipped` (no browser, no expect file, a
required input without a value, or a safe-mode stop); only `fail` fails the
command.

