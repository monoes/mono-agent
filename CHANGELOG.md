# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.75.0] - 2026-09-26

### Fixed

- **Upgraded legacy actions work as before again.** v0.74 restricted
  actions from `~/.monoagent/actions` to the sites written in them, which
  broke templated URLs and redirects (e.g. google.com → www.google.com).
  They run unrestricted again. The sites found in them are kept as a
  suggestion for export.
- **Importing a workflow never overwrites your own work.** A same-named
  workflow you made or edited is left alone, and the file is imported as a
  copy with a warning (`--replace <id>` replaces it explicitly). Re-importing
  an identical file still reports "unchanged".
- **Workflow bundles export partially.** Automations that can't be exported
  are listed with the reason instead of failing the whole export.
  `--automation-domains <id>=<sites>` and `--use-suggested-domains` include
  legacy ones. `automation export` and `action export` take `--domains` and
  `--use-suggested-domains` too.
- **Browser nodes have a session picker** in the editor for every
  automation, including Hacker News, Product Hunt and imported or recorded
  ones.
- **Keyword search nodes** run with only their required fields on every
  platform.
- **The extension's side panel** shows a failed verify's step report instead
  of the previous run's steps.
- `record save --keep-package-selectors` keeps selectors you re-recorded.
- **Re-importing an identical action** is a no-op. Upgrading your own local
  package to a newer version no longer needs `--replace`.
- **`node run instagram.list_post_comments`** saves comments with form-style
  targets.
- **Clearer error** when the bridge rejects a client (pairing mismatch).
- **Workflow editor:** the node palette refreshes after installs, and the
  image picker only appears on media fields.

### Note

The desktop app's in-app update on Linux could fail in v0.73.0 when `/tmp`
is a separate filesystem. If you're on the v0.73.0 desktop app, download
this release manually once. Updates from v0.74.0 onward work from inside the
app.

## [0.74.0] - 2026-09-26

### Fixed

- **The desktop app's update button** now verifies every download against
  the release's SHA256SUMS and HTTP status before installing, stages files
  next to the app (no more "invalid cross-device link" on Linux), and
  updates the bundled CLI together with the app. The 0.73.0 entry claimed
  this for a code path the button never used; that unused updater is
  removed.
- **Upgrading from older versions:**
  - Old actions whose platform name contains `_` (e.g. `google_maps`) keep
    their node names.
  - Packages generated from `~/.monoagent/actions` get their allowed sites
    derived from their actions and no longer show validation errors.
  - Re-importing a workflow imported by an older version no longer
    duplicates it.
- **Workflows bundled with legacy automations** can be exported and
  imported on another machine.
- **Node forms:**
  - Hacker News and Product Hunt nodes have forms.
  - Instagram `list_user_posts` asks for the profile to read, separately
    from the session.
  - Every built-in form now asks for its action's required inputs.
  - Workflows saved with older forms still fill the new fields.
- **Actions declare the fields they actually return.** A test now checks
  every flow's output against the declaration.
- **TikTok and other comment results** no longer get their text copied into
  `full_name`.
- **Replacing a package you created** keeps the old copy for rollback and
  needs confirmation (`--replace`; `--replace-builtin` still works). The
  install review lists what the site can see and says when trust drops.
  `automation new --install` refuses an existing id.
- **Failed `--json` commands** (`automation validate`, `record verify`,
  `ai provider test`) exit 1.
- **Safe-mode verify** refuses an upload that a real run would refuse.
- **`record verify` paths:** errors no longer reveal whether a path exists.
- **`install.sh`** uses sudo only when the install directory isn't writable.
- **Connections page:**
  - The Health tab fits the drawer.
  - Replacements are labelled as such.
  - The workflow count refreshes after an import.
  - Updates warn that script and live-run permissions are reset.

## [0.73.0] - 2026-09-26

### Added

- **Import workflows in the desktop app.** Workflows › Import workflow
  takes a file or pasted JSON. It reports whether the workflow was imported,
  updated or already there, and opens it in the editor. Automations bundled
  in the file show their status, and missing ones can be installed after a
  review of what each can do. "Import as a new copy" forces a copy.
  `workflow import --json` now includes that review for missing bundled
  automations.
- **Actions state what the site can see.** A new `visibility` field lists
  side effects that come from just visiting pages: profile views shown to
  the owner, searches that may be saved, video views, story views, and
  Instagram's notification prompt. Each affected action's description says
  so in plain words, and `automation show` lists it. LinkedIn profile views
  can only be hidden with LinkedIn's private browsing mode.

### Changed

- **TikTok read actions** keep videos paused and muted while they read a
  page.
- **LinkedIn and X searches** no longer tell the site the query was typed
  into the search box.

### Fixed

- **The desktop app's updater** now verifies the download against the
  release's SHA256SUMS, as `monoagentcli update` does, and refuses to
  install on any mismatch. It also no longer installs an HTTP error page as
  the CLI when a download fails.
- **The desktop app finds its bundled CLI** on Linux and Windows without it
  being on PATH. The Linux tarball now ships the CLI as `monoagentcli` next
  to the app, and the Windows release notes link both files. The updater
  replaces the same CLI the app runs.

## [0.72.0] - 2026-09-26

### Added

- **`maxComments`** on Hacker News and Product Hunt `list_comments` caps
  how many comments are read (default: all, as before).
- **Action inputs can declare aliases.** Instagram and X `export_followers`
  now take the profile as a required `target_url` (shown as "Profile" in
  the node form). Configs using `profileUrl`, `targetUsername` or a
  selected list keep working.

### Fixed

- **`monoagentcli update`** and the desktop app's updater failed with
  "invalid cross-device link" when `/tmp` is a separate filesystem. The new
  binary is now staged next to the old one.
- **`node run` of a browser action** failed when the action saves its
  results (for example Hacker News `get_post_metrics` and `list_comments`),
  because standalone runs have no workflow execution to attach them to.
- **LinkedIn "Show more"** now clicks only a button that says show, see or
  load more. Before, it could hit a look-alike Follow button.
- **Instagram and X `export_followers` built from the node form** never
  received the chosen profile.
- **TikTok `share_video`, `duet_video` and `stitch_video`** are labelled as
  write actions (they copy a link or open the creator). A test now fails
  any action labelled read-only that clicks, types or uploads.

## [0.71.0] - 2026-09-25

### Added

- **Browser automation packages.** Every browser automation (Hacker News,
  LinkedIn, … or your own) is now a package: an `automation.json` manifest
  (site, allowed domains, login detection, permissions), action files,
  reusable fragments, named selectors with ranked fallbacks, optional forms
  and fixture tests. Packages install, update, roll back, export and import
  as `.mpkg` files. Built-ins ship with the app as packages too, so they can
  be updated or removed (`automation restore` brings one back).
  - CLI: `automation list|show|new|validate|test|pack|install|export|
    uninstall|restore|enable|disable|rollback|trust|doctor`,
    `action export|import`, and `workflow export --bundle-automations`.
  - `automation new --template` scaffolds a package from five templates.
    JSON Schemas in `data/schemas/` let editors validate package files.
  - Node types keep their `<automation>.<action>` names, so existing
    workflows keep working. Actions installed with the old
    `action template install` are wrapped into a `local-<platform>` package.
  - Package actions without a hand-written form get one generated from their
    inputs.
- **Declarative steps.** Actions no longer need compiled Go code:
  - `call_fragment`, `call_action`, `for_each`, `wait_for`, `assert`,
    `select_option`, `press_key`, `extract_table` and `extract_json`;
  - `transform`, with the ops map, filter, flag, dedupe, regex_extract,
    parse_date, parse_number, join, split, pick, limit, lower, replace and
    tree_parent;
  - `http_fetch_in_page` and `download`;
  - `page_script`, only as an opt-in escape hatch.

  The built-in Hacker News automation is now fully declarative, the
  reference package to copy.
- **Record an action in the browser.** In the extension's side panel,
  press Record, do the task (pick data to extract with Alt+click), then Stop.
  - `record analyze` has AI (through the monomind runner) turn the recording
    into an action with named inputs, outputs, selectors with fallbacks, and
    side-effect flags.
  - `record verify` replays it in your browser, stopping before anything
    that writes or sends. It heals broken selectors from their fallbacks.
  - `record save` saves it as a node, as a fragment, or as a workflow draft.
  - The same flow is in Connections › Recordings.
  - Passwords and card numbers are never recorded, and tokens are stripped
    from recorded URLs.
- **Connections page** is split into **Browser Automations** (cards with
  login state, actions, health, recordings, import/export) and **API
  Connections**.
- **Selector health.** Every run records which selector worked. A selector
  that needed a fallback is promoted, and `automation doctor` flags decaying
  or broken ones.
- **Re-record a single selector.** `automation rerecord <id> <key>`, or
  Re-record on a broken row in Connections › Health, opens the page and asks
  you to click the element once. The selector is rebuilt from that click
  (written into your own packages, or kept as a local overlay for built-in
  and imported ones), and its health history is reset.
- `automation install <dir> --local` and `automation new … --install` install
  a package you wrote as your own (trusted, scripts allowed).
- **Workflow import is idempotent.** Importing the same workflow again
  reports `unchanged` or `updated` instead of creating a copy; `--as-new`
  forces a copy. A workflow whose bundled automations are missing is still
  imported, and the output lists what to install and the command to run.

### Security

- Imported packages are contained:
  - They run only on their declared domains, with only their declared step
    types.
  - They read only secrets filed under their own id
    (`automation:<id>/<name>`).
  - They upload only files you give them.
  - They run page scripts only after `automation trust <id> --scripts`, and
    their writing actions only after a one-time
    `automation trust <id> --live`.
- The install review shows every capability and the full source of any
  script. Installs are pinned to the reviewed bytes (`--expect-sha256`), URL
  installs are https-only, and replacing a built-in needs `--replace-builtin`.
- Recordings are stored in `~/.monoagent/recordings/`, not the capture
  inbox, so they never reach Documents or the knowledge brain. Recording
  content is fenced as untrusted data in the AI prompt, and AI-drafted
  actions can't use scripts, fetches, uploads or cross-package calls
  without `--allow-advanced`.

### Fixed

- **Typing through the extension:** `type` steps sent through the extension
  typed nothing into ordinary inputs while reporting success. The element is
  now focused, and the typed text is read back.
- **Unknown step types** in an action now fail validation instead of being
  skipped silently.
- **Workflows created with `workflow create`, or saved from the desktop
  editor,** now also store their nodes in the database, so `workflow run
  --json` shows node types. Existing workflows are backfilled automatically.
- **Commands relayed through a running bridge** are no longer cut off after
  90 seconds.
- **Browser logins:** the `connect <platform>` error now points to
  `login <platform>`.

## [0.70.0] - 2026-09-25

### Added

- **Jev in the org view.** When the org decider is `jev`, the autonomy bar
  shows a Jev threshold field, with a pointer to Settings › TypeSafe Jev and
  a note that the model decider takes over below the threshold. The
  decisions feed shows Jev's confidence: "Jev p=0.93", or "model decided
  (Jev p=0.62 below 0.8)". Expanding a decision shows the per-verdict
  distribution. `org autonomy decisions` rows carry a `jev` summary, and the
  MCP and chat autonomy tools know the `jev` kind and its threshold.

### Fixed

- **Workflow editor:**
  - Opening an `ai.choose` or switch node whose cases are objects no longer
    crashes the page.
  - Renaming a handle keeps its connections.
  - Switch cases edited as JSON are saved as a list. Before, the engine
    ignored them and every item went to the default handle.
- **Hosts without an OS keyring** (`MONOAGENT_ALLOW_FILE_KEYRING=1`):
  commands that read their input from stdin (`jev key set`, `secret add`,
  `secret add|update --stdin-json`, `secret import`, and saving the TypeSafe
  key from the app) no longer fail with "empty file-keyring passphrase". The
  passphrase is asked for on the terminal, or read from the chmod-600 file
  named by the new `MONOAGENT_FILE_KEYRING_PASSPHRASE_FILE`.
- **X DMs** match the current X Chat inbox (`/i/chat`). Requests and
  settings links are no longer counted as conversations, and unread rows are
  detected. An account that hasn't set an X Chat passcode now gets a clear
  "set up or enter your X Chat passcode" error.
- **TikTok:** follower lists include each account's follow state, and
  comments are read from the current comment panel layout.

## [0.69.0] - 2026-09-25

### Changed

- **Social platforms are in the main build.** Instagram, LinkedIn, X,
  TikTok, Hacker News and Product Hunt nodes and actions now ship in every
  release and in a plain `go build`. `node list` shows 167 node types. To
  build without them, use `go build -tags nosocial`. The old `-tags social`
  flag is gone. The usage policy, README and help text are updated to match.

## [0.68.0] - 2026-09-25

### Added

- **Settings › TypeSafe Jev** in the app: store, test or remove the API key,
  switch each Jev feature on or off with its confidence threshold (the app
  shows what each one sends to TypeSafe and asks before turning it on), and
  see the last 7 days' usage. It drives the new
  `monoagentcli jev key set|test|remove` (key read from stdin) and
  `jev status`, which now names the vault entry and describes each feature.

## [0.67.0] - 2026-09-25

### Added

- **TypeSafe Jev decisions.** Jev picks one of a fixed set of options,
  answers yes/no, or scores against a rubric. It never writes text. Each
  answer comes back in about 0.3 s with probabilities attached. Every
  feature below is off until you turn it on for a profile (`monoagentcli jev
  enable <surface>`, which first lists what gets sent to TypeSafe), or until
  you use the node that relies on it. Below its confidence threshold, a
  feature falls back to what it did before. The one difference is inbox
  classification, which stores the message as "unsure" so it isn't sent
  again. The key is looked up in the
  node's config, then in the vault entry `typesafe` (or one named like
  "Jev API key"), then in `TYPESAFE_API_KEY`.
  - `monoagentcli jev status|enable|disable|usage|ask|models`, plus the
    `jev.key` and `jev.api` doctor checks. `jev usage` shows calls, tokens
    and estimated cost; no request content is stored.
  - **`browser.jev` node:** give it a URL and a goal, and it clicks, types
    and selects its way there in your own browser (ported from
    browser-use/jev-ultrafast). A `values` map, which can hold `@secret:`
    entries, fills fields without a local-agent turn. Jev only ever sees
    those values as `<value:NAME>`.
  - **`ai.choose` node:** routes each item to one of your cases, or to a
    `low_confidence` output. It can also answer extra yes/no, choice or
    score questions. `ai.classify` stays deprecated and now points to it.
  - **Orgs:** `org autonomy set --decider jev` lets Jev approve, deny or
    escalate. Questions, low-confidence cases and errors go to the model
    decider. Each decision records Jev's probabilities. Tiers and routing
    are unchanged.
  - **Job fit:** `application evaluate --runtime jev` scores gates and
    rubric dimensions in one request. The weighted score and verdict are
    computed in Go.
  - **Action steps** can declare an `intent`. With `action_fallback`
    enabled, Jev finds the element when a selector no longer matches. Intents
    ship for the Gemini, Instagram, LinkedIn, X and TikTok actions. In social
    builds, LinkedIn likes use the same fallback.
  - **Classification:** `capture classify` / `capture list --suggested`
    label captured pages, and `people messages classify` / `list --intent`
    label inbound messages.
  - **Cross-platform people:** `people links suggest|list|confirm|dismiss`
    proposes pairs of profiles that look like the same person. Rows are
    never merged.
  - **Human-in-Loop `auto_decide`** (`jev enable hil`): items Jev approves
    with high confidence and doesn't rate as high-risk pass without
    stopping. The rest wait, with Jev's suggestion shown. Runs started by
    orgs only get suggestions. `hil list --suggest` and `people review list
    --suggest` show the suggestions, and the Human in Loop page shows them
    as chips sorted by confidence.
  - **Org asks:** a reply that lost its `ask:` token is linked to the
    waiting ask it answers, and the match is logged.
  - **Retries:** with `retry` enabled, node failures are classified after
    their error text is redacted. Rate limits back off longer, and auth or
    permanent errors stop retrying.

### Fixed

- **Social actions work in your browser again (social builds).** The app
  only drives your real browser through the extension. Most Instagram bot
  code and several LinkedIn, X and TikTok paths needed a different browser
  driver, so they failed or quietly did nothing. All Instagram, LinkedIn, X,
  TikTok, Hacker News and Product Hunt actions now run through the
  extension. Scripts no longer break on sites whose security policy blocks
  them (LinkedIn, Hacker News). Every write action (like, comment, reply,
  DM, follow, publish) checks that its result appeared and fails if it
  didn't.
- Harmful fallbacks are gone:
  - un-liking posts that were already liked;
  - liking the wrong comment;
  - following "Suggested for you" accounts;
  - sending a LinkedIn connection request when a Message button was missing;
  - DMs going to whichever conversation was open;
  - clicking Send twice;
  - comments reported as posted when they never reached the editor;
  - double posts on Hacker News.
- List actions (comments, posts, followers, search results) now return one
  item per record. Before, only the last record survived the node.
- Engine fixes that apply to every action:
  - XPath lists work in the extension.
  - Selector alternatives are honoured.
  - `Eval` reports failures instead of returning nothing.
  - `Has` and `WaitStable` work.
  - Condition branches in the social actions are fixed.
  - A wait of 0 seconds no longer waits 10.
  - `linkedin.list_user_posts` accepts `targets`.
- The desktop app's Human in Loop approve and reject now go through
  `monoagentcli hil` instead of running SQL inside the app.
- Workflow retries no longer re-run a paused Human-in-Loop node or
  invalid configuration, cancelled runs, or errors marked permanent. Before,
  a paused node was executed again under `retry_policy`. A node's own
  timeout is still retried.
- LinkedIn likes with a named reaction (social builds) fail when that
  reaction is missing. Before, they silently clicked plain Like. An unknown
  reaction name is now an error.
- The workflow editor's switch, filter and choose output ports now match
  the handles the engine emits. The filter showed `pass/fail`, but the
  engine emits `main/rejected`.

## [0.66.0] - 2026-09-25

### Added

- **`monoagentcli people tag list|add|remove|color`**. The People page now
  makes every tag change through it, including the new tag colour picker.
- **People page:** rework of the page and its tag editor. Human in Loop
  usernames and URLs open the profile in the browser.
- **`monoagentcli people review list|approve|reject`** drives the review
  queue for people staged as `pending_approval`. `approve --send` runs the
  dispatch workflow for the person.

### Fixed

- People saved without a post count, following count or verified flag no
  longer break `people list`, `people get`, export or the app's people
  lookups. They failed with "converting NULL to int is unsupported".
- A newly created tag shows its colour straight away.

- The app prefers the `monoagentcli` shipped next to it over an older one on
  PATH.
- **Human in Loop's people review goes through the CLI.** Approve/reject
  no longer run SQL inside the app, and "send now" no longer looks for one
  fixed workflow id. It finds the profile's workflow named like "Send
  Approved DMs", including ones stored as files. When there is none, when
  several match, or when it's inactive, you now get an error and the person
  stays in the queue. Before, the person was approved and nothing was sent.

## [0.65.0] - 2026-09-25

This entry also covers work that shipped in 0.50–0.64, which were cut
automatically on every push to master without changelog entries of their
own.

### Added

- **`monoagentcli doctor`** checks everything monoagent needs and fixes what it
  can. The groups are core (data folder, database, profile, vault, PATH,
  disk), monomind (Node.js, monomind, its version and features, the profile's
  `monomind init`, and monomind's own checks per profile folder and
  `--projects`), AI agent runtimes, browser and extension, background
  services, Claude Code integrations, and accounts.
  - `--json` prints a versioned report.
  - `--fix` applies fixes, repeating passes until nothing is left to fix.
  - `doctor fix <id>` runs one fix and streams NDJSON progress.
  - `--deep` adds the network checks.
  - `--skip-group` leaves groups (and their dependents) out.
  - Fixes are auto, confirm or manual. Optional ones (installing a runtime,
    start-at-login, MCP registration) run only when asked for by id.
- **`monoagentcli setup`** takes a machine from nothing to ready: it applies
  the fixes (asking before it installs software, or accepting with `--yes`),
  offers the optional extras, and reports what is left to do by hand.
- **Managed Node.js.** `monoagentcli nodejs install|update|remove|status`
  downloads Node LTS into `~/.monoagent/node` when there is no suitable
  system Node.
  - The download's checksum file is checked against the Node release team's
    signature, the archive's size is capped, and installs are serialized.
  - It is used only by the processes monoagent starts. Workflow commands keep
    the user's own PATH.
- **`monoagentcli agent install <runtime>`** installs Claude Code, Codex,
  OpenCode, Copilot, Qwen, Pi and others from monomind's install recipe:
  npm packages, or a vendor https script from an allow-listed host. Anything
  else is shown as steps to do by hand.
- **Settings › System health** in the app shows the doctor report.
  - It has a Finish setup step list, a Fix / Copy steps button per row and
    Update/Remove actions for the managed Node.
  - Deep and per-project checks, cancelling a running check or fix, and a
    status-bar dot fed by a background check. That check runs on start and
    every 30 minutes, and writes nothing outside `~/.monoagent`.
- **AI agents** is back in the sidebar, with Install / Update / Copy steps on
  every runtime.
- **Human in Loop** has a review queue for leads staged as
  `pending_approval`: edit the introduction, then approve or reject.

### Changed

- **AI Providers is now "AI connections (legacy)".** Agent runtimes on the AI
  agents page are the recommended way to use AI.
- A doctor account test offers the silent token refresh only when the
  service refused the credentials (401/403) or the token expired. Other
  failures ask first. Connections are tested in parallel, and AI keys are
  checked with a free model-list call where the provider has one.
- `people.save` evaluates `introduction`, `category` and `job_title` per
  item. The browser nodes parse LinkedIn search-card text.

- Loops through webhooks now stop. A run on an org chain sends that chain
  on its HTTP requests to this machine as a signed `X-Monoagent-Trace`
  header, and a webhook it reaches continues the chain and is refused with
  HTTP 429 past the hop limit. A workflow that posts to its own webhook
  used to run without end; it now stops after 9 runs. Sending many requests
  from one run (a fan-out) is not a loop and is not limited.
- Webhook runs have a `monoagent_trace` field in their trigger item: the
  chain the run is on, set by mono-agent. A workflow that stores or
  forwards the whole webhook payload will see it. A `monoagent_trace` field
  in a request body is dropped; a body's own `trace` field is kept.
- The HTTP request node has a `propagate_trace` option, off by default, to
  send the signed chain to hosts other than this machine too (for a system
  that calls a webhook here back with it).
- Loop control follow-ups (#132): `trigger.org` runs are recorded in the
  ledger (`org_event`, `event_start`), so a loop through a role's tool
  events climbs, including one started by a Bash tool event; a run signs
  the deeper of its hop and the hop its item reached; a replayed token
  starts at most 200 runs of a workflow a minute, then gets HTTP 429;
  with a wildcard `MONOAGENT_WEBHOOK_ADDR` a request to this machine's LAN
  address or host name keeps its trace; tokens expire after an hour; and a
  webhook crossing uses the `max_hops` of the org its chain started in.
- The signing key is `~/.monoagent/trace.key` (mode 0600; a key other
  users can read is refused). To rotate it, delete the file and restart
  `monoagentcli daemon`.

### Fixed

- **First launch on a new machine.** The app created no `~/.monoagent`, so
  its database never opened and the app never finished starting.
- **monomind's stray output.** monomind's "update available" notice, which it
  printed on stdout ahead of its JSON, no longer breaks runtime detection.
- **`install.sh` on linux-arm64.** It no longer refuses a linux-arm64 machine.
- **Saved AI keys.** Leaving the key blank when editing an AI connection keeps
  the stored key.
- **Docs.** README, `--help` and the `ref` manual now match the code: node
  counts, deprecated `ai.*` nodes, and ref entries for every node type.

## [0.49.0] - 2026-09-20

This entry also covers work that shipped in 0.32–0.48, which were cut
automatically on every push to master without changelog entries of
their own. Dates in that range belong to those releases, not to this
one.

### Fixed

- The Org tab no longer fails with `JSON Parse error: Unexpected identifier
  "Observe"`. A `monoagentcli` on PATH older than the GUI answered an unknown
  subcommand with its parent's help — on stdout, exit code 0 — which the page
  then parsed as JSON. Grouping commands now reject unknown subcommands, and
  the GUI refuses non-JSON stdout with an error naming the stale binary.
- The Chrome extension finds the bridge when another program holds port 9222.
  The pairing page now tells the extension which port the bridge actually
  bound, rotation alternates candidates once retries are alarm-driven, and the
  CLI's timeout message names the port instead of blaming the extension.
- A workflow node given nothing to work with fails instead of reporting
  success. A required input that resolves to an empty collection or to the
  string `"null"` — what `{{ json $json.field }}` renders for a missing field
  — is now missing, and a loop over a value that cannot be iterated is an
  error rather than silent zero work. A Gemini image workflow used to finish
  green having generated nothing.
- A node's output no longer carries its steps' bookkeeping: a skipped step
  contributes nothing, so a run that saved an image stops reporting
  `"skipped": true` beside `"image_count": 1`.
- `org list` and `org status` drop entries that could never be an org, so a
  `.mcp.json` sitting in the orgs folder no longer appears as an org named
  `.mcp` that the designer flags as having no root role.
- The workflow list counts nodes instead of always reporting `0 nodes`.
- `org autonomy set` rejects a decider model the runtime does not offer,
  listing the ones it does, instead of failing at the first decision with
  `runner-error: done reported nonzero exit_code 1`.
- `org autonomy needs-you` reports the idle-watchdog deadline and the reason
  there is none (`idle_hold`), where it previously reported `null` for every
  item. Requires a monomind advertising `org-idle-deadline`.
- `OrgEvents` delivers every line the subprocess wrote. `cmd.Wait` closes the
  stdout pipe as soon as the process exits, and it ran concurrently with the
  reader, so a fast-exiting `org events` could have its output closed out
  from under the scanner — the tail of a run's bus, the part that says how it
  ended, was the most likely to go missing.
- Spreadsheet columns are ordered deterministically, and the GUI's node
  inspector reads a node's schema from the running build rather than the copy
  saved inside the workflow, so schema improvements reach existing workflows.

### Added

- Run a workflow with trigger input from the GUI. A workflow whose nodes read
  `{{ $json.<field> }}` was unrunnable from the run button; the editor now
  asks for the fields it reads, pre-filled with the reading node's example
  values, and remembers what was run last time. What a workflow reads is
  reported by `monoagentcli workflow inputs <id>`, so a script, a test and the
  dialog all get the same answer.
- Node schema fields can carry `examples` — ready-made values the editor
  offers under the field, click to insert. Gemini's prompt fields ship a pair
  each, written so the shape is as clear as the wording.
- Releases take their notes from this file when the version has a section
  here, falling back to the generated commit summary when it does not.

### Fixed

- `core.switch`'s primary `field` config key now resolves per-item (like
  `expression` already did), so items with different `$json` values in the
  same batch route to different output handles instead of all following
  item 0's route. This is a behavior change for any workflow that relied
  on the old batch-wide routing.
- `workflow import` now remaps node and connection ids that collide with
  ids already used by other workflows (ids are globally unique in the
  store), so multiple examples can be imported into one database; remapped
  ids are reported in `--json` output. Importing a workflow whose nodes
  have no type is rejected instead of silently persisting a broken graph.
- `workflow export` now emits the documented workflow-file format, making
  export → import roundtrips lossless; the legacy export shape is still
  accepted on import.
- Example workflows (`examples/`) fixed and re-validated (all pass
  `workflow validate`); `examples/README.md` quickstart now includes the
  required `workflow activate` step before starting the daemon.
- `node schema core.set` output corrected to match the node's actual
  configuration fields.
- `workflow validate --file` now runs the same legacy-format
  normalization as `workflow import` (legacy `node_type` /
  `source_node_id` keys converted) and defaults unset connection handles
  to `main`, so any file that imports cleanly also validates — including
  the README flagship example, whose connections omit handles.
- Error routing is honest: `on_error=error_branch` with no edge wired to
  the node's `error` handle is reported instead of silently discarding
  the failure output, and runs that continue past per-node failures (via
  `on_error=continue`/`skip`) end `SUCCESS_WITH_ERRORS` rather than
  `SUCCESS`.
- `core.filter` now surfaces evaluation errors from its condition instead
  of passing items through silently.
- Store-level node saves preserve edges: `SaveWorkflowNodes` updates nodes
  in place instead of delete+reinsert (which cascaded deletes through
  `workflow_connections`); the CLI workaround that re-saved connections
  afterwards is removed.
- `workflow list` performance: workflow JSON files are parsed once and
  cached, and a SQLite expression index (migration 024) speeds up
  expression-based lookups.
- Migration healing on upgrade: a Go-side schema reconcile repairs
  drifted SQLite schemas; the vault migrations are renumbered 027/028;
  and the CLI and MCP entry points now run the vault migration, so
  databases from older installs converge without manual steps.
- Executions are stamped with their owning profile as they run, and a
  migration backfills the profile stamp onto existing execution rows.
- Browser-node helper matches more pgrep process-name variants
  (Chromium, Brave, Edge alongside Chrome) and fails fast with a clear
  error when no supported browser is running, instead of hanging.
- GUI: background polling is gated on page visibility and the assistant
  agent scan runs on page open; the legacy duplicate panel is removed;
  an empty active profile is normalized instead of erroring.
- Extension bridge port resilience: the server honors
  `MONOAGENT_EXTENSION_PORT` and the extension tries that port before
  falling back to 9323, so a custom port no longer strands the
  extension.

Repository hygiene and packaging wave: social bot implementations moved
behind the opt-in `social` build tag (default builds exclude them),
documentation restructured around the core workflow engine, repository
hygiene fixes (tracked secrets removed, dead action templates deleted,
license and community files added), and review-driven accuracy fixes to
CLI behavior and docs.

### Added

- `LICENSE` (MIT), `SECURITY.md` (vulnerability reporting, telemetry and
  crash-reporting statement, secrets-vault scope), `CONTRIBUTING.md`, and
  this `CHANGELOG.md`.
- CI workflow (`.github/workflows/ci.yml`): build, vet, and race-enabled
  tests in both default and `-tags social` build modes, plus a Wails job
  that builds the `wails-app/` desktop module.
- MCP server (stdio JSON-RPC 2.0) so AI agents can list/run workflows,
  inspect node schemas, and resolve human-in-the-loop approvals.
- CLI agent-experience improvements: `workflow run --json/--dry-run/--no-wait`,
  `workflow validate`, `node schema`, enriched `--json` output, and granular
  exit codes.
- `workflow run --no-wait` enqueues the run (status `QUEUED`), prints the
  execution id with a hint that a live engine (e.g. `monoagentcli daemon`)
  completes it, and exits 0 immediately — adopted in the docs as the
  agent pattern for fire-and-forget runs. A run that pauses at a
  human-in-the-loop node ends the wait with status `WAITING`, exit 0, and
  a `hint` field pointing at the approval queue.
- Example workflows (`examples/`) and Docker/install distribution files.
- `workflow templates run` now honors `--json`.
- Sandbox resource caps: HTTP node bodies capped at 64 MB by default
  (configurable); `core.code` nodes run with a 30 s default timeout
  (configurable via `timeout_seconds`) and return at most 10,000 items of
  16 MB each per execution — an engine-level memory ceiling is not yet
  enforced by the vendored JS runtime; `system.execute_command` output
  capped at 10 MB per channel (stdout and stderr). Stored outputs are
  persisted in full but display-truncated at 4 KB.
- Webhook server environment overrides: `MONOAGENT_WEBHOOK_ADDR` sets the
  bind address (`host:port`, default `127.0.0.1:9321`) and
  `MONOAGENT_WEBHOOK_ALLOWED_ORIGINS` sets a comma-separated CORS
  allowlist (default: no CORS headers). Docker port mappings for webhook
  triggers now work without host networking.
- Opt-in file-based keyring fallback for headless environments:
  `MONOAGENT_ALLOW_FILE_KEYRING=1` stores the vault key-encryption key in
  a per-profile file `~/.monoagent/vault/.file-keyring-<profileID>`
  (0600) with a loud warning, so `secret add` works in CI/containers;
  without the variable the vault fails closed on machines with no OS
  keyring.
- Node schema coverage completed: every default-build node type now
  resolves a schema — 16 added (`image.*`, `service.reddit`,
  `service.devto`, `service.discord`, `service.bluesky`,
  `service.mastodon`, `service.hashnode`, `service.producthunt`,
  `gemini.chat_session`, `gemini.chat_session_many`).
- GUI (`wails-app`): ImportWorkflow/ExportWorkflow bindings, subprocess
  PID verification, and a cancellable RunNode.
- `update` verifies downloads against the release `SHA256SUMS.txt` and
  hard-fails on a checksum mismatch or a missing checksums file.
- Per-profile encrypted vault: each profile's secrets are sealed with
  their own key-encryption key under per-profile vault folders, entries
  are re-encrypted when they move between profiles, and the file-keyring
  fallback is likewise per profile
  (`~/.monoagent/vault/.file-keyring-<profileID>`).
- Profile folders: the GUI settings gain per-profile folder management.
- Assistant chat sessions: `chat --history-id <session>` persists and
  resumes named sessions.
- Assistant tools (explicit opt-in): `chat --tools monoagent` exposes
  workflow/vault/people/actions/comms tooling to the model;
  `--tools monoagent,runs` additionally opts in to run/execution tools.
  Off by default; the GUI settings toggle mirrors the gate and also
  defaults to off. `get_workflow` output is redacted, delete-class tools
  write sidecar backups, synced message content carries provenance
  fences, and tool-call timeouts derive from the caller's context.

### Changed

- Go module path renamed to `github.com/monoes/mono-agent`.
- README and agent docs restructured around the core workflow engine;
  social-platform actions documented as an opt-in integration for your
  own accounts.
- Social engagement bots (browser automation for social platforms) moved
  behind the opt-in `social` build tag; default builds exclude them.
- Legacy social CLI commands (`message`, `comment`, `search`, `list`,
  `template`) are hidden from default `--help` output; they remain
  invokable and appear in `--help` in `-tags social` builds.
- Crash reporting defaults to local files under `~/.monoagent/crashes/`;
  filing to GitHub requires `MONOAGENT_CRASH_REPORT=1` and the `monomind`
  CLI on PATH (the `npx` fallback was removed).
- `{{ $env.* }}` template access now requires `MONOAGENT_ALLOW_ENV_TEMPLATES=1`.
- Output items are redacted in `--json` and MCP output (values of keys
  such as token/secret/password/authorization/cookie are masked);
  `--full-outputs` opts out on the CLI.
- `secret add` stdin now accepts values with or without a trailing newline.
- Exit codes aligned with behavior: `hil approve`/`hil reject`,
  `secret rm`/`secret update`, and `workflow delete` return 2 on unknown
  ids; a run ending `CANCELLED` exits 1.
- `wails-app` Go module fixed so the desktop app builds as its own module.
- `monoes_apis/` development scripts extracted to the private repository
  `monoes/monoes-apis`; this tree no longer carries them.

### Removed

- Dead browser-action templates for platforms with no live implementation
  (alternativeto, betalist, capterra, facebook, futurepedia, g2,
  indiehackers, lobsters, medium, pinterest, quora, threads, tildes) from
  `data/actions/`.
- Hardcoded deployment credentials and identifiers from
  `monoes_apis/deploy_full.sh` (now required environment variables).
- Message-variant rotation removed from social DM actions.
- Typo simulation removed from humanized typing in social actions; the
  randomized pacing between keystrokes is kept.
- `go-rod/stealth` dependency dropped (unused).

### Security

- `scripts/import_edge_passwords.py` now pipes passwords via stdin to
  `monoagentcli secret add` instead of exposing them as command-line
  arguments (visible in process lists).
- Output redaction (above) keeps credential-shaped values out of `--json`
  and MCP results unless `--full-outputs` is passed.
- Assistant tool surface is gated and value-safe: tools are off unless
  explicitly enabled, vault tools return metadata only (values never
  reach the model), deletes are sidecar-backed, and synced message
  content is provenance-fenced (see SECURITY.md for the residual
  injection risk).

Review-fix rounds 2–4 are itemized in
[docs/plans/2026-08-28-trust-hygiene-record.md](docs/plans/2026-08-28-trust-hygiene-record.md).

## [0.31.0] - 2026-08-25

### Added

- Orgs UI (Phase 3) and an Agents-first rework of the AI settings page.
- `agent.ask` workflow node; `ai.*` nodes deprecated in favor of it (Phase 2).
- Phase 1 of delegating AI chat to local agent runtimes.

### Fixed

- Agent scan now performs a handshake first; canvas mode disabled for
  general chat.
- Frontend lockfile reconciliation after merging origin/master.

## [0.30.2] - 2026-08-19

### Fixed

- Refreshed Wails build checksum after the Windows frontend build.

## [0.30.1] - 2026-08-09

### Fixed

- Workflow engine: per-item config fields now resolve correctly for
  `core.set` and `http.request`.
- Secrets vault: KEK/DEK bootstrap serialized across concurrent processes.
- Connections: Salesforce `instance_url`, Reddit/Notion OAuth exchange, and
  Linear `Bearer` prefix.
- Browser extension: local relay endpoint now requires a shared-secret token.
- CI: build the frontend before the Wails Go backend; npm install workaround
  for an upstream npm bug; lockfile regeneration.
- `monoes_apis`: patched a path traversal, disabled debug mode, and made
  `GEMINI_API_KEY` a required environment variable.

### Changed

- Release pipeline is now gated on tests passing.

## [0.30.0] - 2026-08-08

### Added

- New social/service node implementations and action templates.

## [0.29.0] - 2026-08-07

### Added

- Secrets-vault credential unification: AI provider API keys, crawler
  session cookies, and connection credentials are routed through the
  encrypted vault instead of plaintext columns.
- Vault entries cascade-delete their dependent system rows, are badged in
  the Vault UI, and are rematerialized on import.

### Fixed

- Import now preserves non-secret connection Data and provider Status.

[unreleased]: https://github.com/monoes/mono-agent/compare/v0.31.0...HEAD
[0.31.0]: https://github.com/monoes/mono-agent/compare/v0.30.2...v0.31.0
[0.30.2]: https://github.com/monoes/mono-agent/compare/v0.30.1...v0.30.2
[0.30.1]: https://github.com/monoes/mono-agent/compare/v0.30.0...v0.30.1
[0.30.0]: https://github.com/monoes/mono-agent/compare/v0.29.0...v0.30.0
[0.29.0]: https://github.com/monoes/mono-agent/compare/v0.28.1...v0.29.0
