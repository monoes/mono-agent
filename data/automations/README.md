# Built-in automation packages (data/automations/)

Each directory is one **browser automation package**: a manifest plus its
actions, in the package format described in
[`docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md`](../../docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md)
(§4 format, §5 registry/install/export, §6 safety). They are embedded at
compile time (`data.AutomationsFS`, see `data/embed.go`) and **seeded** into
the installed registry (`~/.monoagent/automations/`) on first use; the
registry, not this directory, is what runs.

## Layout

```
data/automations/<id>/
├── automation.json          manifest: site, login, permissions, actions, policy
├── actions/<action>.json    one action definition per file (file name = action name)
├── fragments/<name>.json    optional reusable step sequences (call_fragment)
├── selectors.json           optional named selectors (configKey)
├── scripts/<name>.js        optional page scripts (page_script) — avoid
└── tests/                   optional fixtures: fixtures/<action>.html + <action>.expect.json
```

Every file carries a `"$schema"` pointer to the JSON Schemas in
[`data/schemas/`](../schemas) (generated from the Go structs by
`internal/schemagen`; run `go generate ./internal/schemagen` after changing
`automation.Manifest` or `action.ActionDef`/`StepDef`), so editors validate
and autocomplete them.

## Packages

| id | tier | native bot | actions |
|---|---|---|---|
| `gemini` | standard | `gemini` | 4 |
| `hackernews` | social | `hackernews` | 4 |
| `instagram` | social | `instagram` | 18 |
| `linkedin` | social | `linkedin` | 12 |
| `producthunt` | social | `producthunt` | 3 |
| `tiktok` | social | `tiktok` | 16 |
| `x` | social | `x` | 7 |

All built-ins set `requires.native`: their actions call a compiled Go bot
(`internal/bot/<id>`) through `call_bot_method`. Social-tier packages follow
the usage policy (`docs/USAGE_POLICY.md`): their bots are left out of a
`-tags nosocial` build, where the package installs as unavailable with that
reason. Each action's `description` is its authoritative summary;
`monoagentcli automation show <id>` lists them with their side effects.

## Manifest essentials

- `site.startUrl` / `site.domains` — the page a session opens and the hosts
  steps may navigate to (`"*.example.com"` also matches `example.com`).
  Enforced by the action engine.
- `login` — login page, the logged-in probe (`loggedIn.selector`, a CSS list
  meaning "any of", and/or a session `cookie`), optional `usernameFrom`, and
  `sessionTtlDays`. Read by `monoagentcli login <id>` and the Connections page.
- `permissions.steps` — every step type the package's actions use (entries
  may be prefix globs such as `extract_*`). A step type not listed fails
  validation with `step_not_permitted`.
- `policy.tier` — `standard` or `social`.

## Side effects

Every action declares `sideEffects` — its strongest effect, one of
`none | read | write | message | destructive`:

- `read` — scrapes, lists, searches, metrics
- `write` — likes, follows, publishing, submitting, watching stories, sending
  a prompt
- `message` — anything that sends text another person reads: DMs, comments,
  replies
- `destructive` — unfollowing, deleting

The step that actually performs the effect (usually the `call_bot_method`
step, or the click on the submit/send button) is marked
`"sideEffect": true`. Safe-mode verification (`record verify`) stops right
before it; the install review and the Connections page show the level.

## Adding an action

1. Add `actions/<name>.json` (copy a sibling, or scaffold from a template in
   [`data/automation-templates/`](../automation-templates)). Set
   `actionType` to `<name>`, `sideEffects`, and `"sideEffect": true` on the
   step that writes/sends.
2. List `<name>` in `automation.json` → `actions` (keep the list sorted).
3. Add any new step type it uses to `permissions.steps`.
4. If it uses `call_bot_method`, implement the method in
   `internal/bot/<id>/`. New packages should use declarative steps only.
5. Bump the manifest `version` so installed copies pick up the change on the
   next seed (a user-modified copy is kept and the update is reported as
   pending).
6. `go test ./internal/schemagen/ ./internal/action/` — checks that the
   `actions` list matches the files, every action declares `sideEffects`,
   and `permissions.steps` covers every step used.

## Installing and exporting packages

```bash
monoagentcli automation list                     # installed packages (seeds built-ins first)
monoagentcli automation new acme --template list-scrape --dir ./acme
monoagentcli automation validate ./acme          # schema + lint + fixtures
monoagentcli automation install ./acme --yes     # directory, .mpkg file or https URL
monoagentcli automation export acme -o acme.mpkg # deterministic zip with CHECKSUMS
monoagentcli automation uninstall|restore|enable|disable|rollback <id>
```

An `.mpkg` is the package directory zipped, with a generated `CHECKSUMS`
file. Installing shows a review first (publisher, domains, permissions,
scripts, each action's side effects); social-tier packages are held to the
same build gate as the built-ins.
