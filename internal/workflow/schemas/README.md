# Node schemas

Each `<node-type>.json` file here is the UI/config schema for one workflow
node type, embedded via `//go:embed schemas/*.json` in
[`schema_loader.go`](../schema_loader.go) and served as-is by
`monoagentcli node schema <type>`.

## Generated vs. hand-written

A generated file is marked by a top-level `"_generated": true` key:

```json
{
  "_generated": true,
  "credential_platform": null,
  "fields": [ ... ]
}
```

That key is not part of `workflow.NodeSchema` — `schema_loader.go` ignores
unknown JSON fields when it unmarshals a schema file, so its presence has no
runtime effect. It exists purely so a contributor (or `grep`) can tell at a
glance which files come from the generator and must not be hand-edited —
edits to a generated file are silently discarded the next time
`go run ./cmd/schemagen` runs.

As of this writing, **75 of 110** node types are generated (see
`Manifest` in
[`internal/tools/schemagen/manifest.go`](../../tools/schemagen/manifest.go)
for the exact list). The remaining **35** are hand-written, and — after the
full-migration pass that converted the other 71 — that's now an
intentional, permanent state for two distinct reasons, not "not gotten to
yet":

1. **No real per-type `Execute` to introspect (27 node types).** These are
   all dynamically dispatched through one generic executor, not a
   hand-written `Execute(ctx, input, config)` per node type, so there's no
   single source of truth for a companion struct to describe:
   - `action.*` (15: `auto_reply_dms`, `comment_on_posts`,
     `engage_user_posts`, `engage_with_posts`, `export_followers`,
     `extract_post_data`, `find_by_keyword`, `follow_users`,
     `like_comments_on_posts`, `like_posts`, `publish_post`,
     `scrape_profile_info`, `send_dms`, `unfollow_users`, `watch_stories`),
     `instagram.publish_post`, and `browser.generic` — all served by the
     single generic `nodes.BrowserNode.Execute` in
     `internal/nodes/browser_adapter.go`, registered per platform/action
     pair at runtime by `internal/nodes/browser_register.go`. Its config
     surface (`username`, `credential_id`, `message`, `keywords`,
     `targets`, ...) is shared across all of them; the per-action
     differences the old hand-written files captured (which fields are
     actually relevant/required for *this* action) are cosmetic curation
     on top of that shared surface, not something `Execute` itself encodes.
   - `gemini.*` (4: `chat_session`, `chat_session_many`, `generate_image`,
     `generate_text`) — same generic `BrowserNode` path, platform
     `"gemini"`.
   - `ai.agent`, `ai.chat`, `ai.classify`, `ai.embed`, `ai.extract`,
     `ai.transform` (6) — registered via
     `ainodes.RegisterDeprecated` (`internal/ai/nodes/deprecated.go`) as
     fail-fast stubs for the local-agent transition; their `Execute` reads
     zero config and always returns an error. Generating from actual
     behavior would produce an empty schema, which would be a UI
     regression versus the legacy config shape still shown today. Left
     hand-written until these are actually removed
     (see `docs/plans/local-agent-monomind-delegation.md`).
   - `trigger.manual` — genuinely has no config; `workflow/execution.go`
     documents it as a pure pass-through. A companion struct would have
     zero tagged fields, so it stays hand-written (its JSON is already
     just `{"fields": []}`).
2. **`resource_picker` fields (7 node types): `comm.discord`,
   `comm.slack`, `service.airtable`, `service.asana`, `service.github`,
   `service.google_drive`, `service.google_sheets`.** Their hand-written
   schemas use a `type: "resource_picker"` field with a nested
   `resource: {type, create_label, param_field}` object (e.g. Discord's
   channel picker, Airtable's base/table pickers, GitHub's repo picker).
   `workflow.NodeSchemaField.Resource` exists on the target struct, but the
   `schema:"..."` tag grammar in `internal/tools/schemagen/schemagen.go`
   has no key that populates it — only `type`, `options`,
   `depends_on_key`/`depends_on_values`, etc. Generating these today would
   silently degrade the field to a plain text box, losing the "list live
   resources from the connected account" UI. Fixing this requires adding a
   `resource=`/`resource_type=` (or similar) tag key to `schemagen.go`
   first — worth doing as a follow-up, at which point converting these 7
   is mechanical.

## Converting another node type to generated

1. In the node's package, add a `<Node>Schema` struct with a `schema:"..."`
   tag on each field that should appear in the JSON schema. See
   `internal/nodes/control/set_schema.go` for a worked example and
   `internal/tools/schemagen`'s package doc comment for the full tag
   grammar. This struct is never constructed at runtime — it exists solely
   to describe the schema; the node's `Execute` keeps reading its
   `map[string]interface{}` config exactly as before.
2. Add an entry to `Manifest` in
   [`internal/tools/schemagen/manifest.go`](../../tools/schemagen/manifest.go)
   mapping the node type string to that struct.
3. Run `go run ./cmd/schemagen` from the repo root. It (re)writes
   `internal/workflow/schemas/<node-type>.json`.
4. Diff the new file against the old hand-written one you're replacing.
   They are not expected to match byte-for-byte (field ordering, and any
   fields the generator surfaces that the hand-written file was missing,
   will differ) — verify the diff is either a no-op or an intentional
   improvement, then delete the file's old content in favor of the
   generated output (i.e. just commit what the generator wrote).
5. That's it for CI — the `schemagen-check` job in
   `.github/workflows/ci.yml` runs `go run ./cmd/schemagen -check`, which
   already covers every entry in `Manifest`, so step 2 above is what scopes
   the check to your new node type. It fails if someone edits a generated
   file by hand without regenerating it, or if the struct tags and the
   committed JSON drift apart.
6. `go test ./internal/workflow/...` — `schema_loader_test.go` and
   `schema_raw_test.go` exercise every embedded schema file, generated or
   not.
