# Node schemas

Each `<node-type>.json` file here is the UI/config schema for one workflow
node type, embedded via `//go:embed schemas/*.json` in
[`schema_loader.go`](../schema_loader.go) and served as-is by
`monoagentcli node schema <type>`.

## Generated vs. hand-written

Most files in this directory are **hand-written** — that's still the
default, and will be for the majority of the ~110 node types for the
foreseeable future. A generated file is marked by a top-level
`"_generated": true` key:

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

As of this writing, **4 of ~110** node types are generated:
`core.set`, `core.filter`, `core.if`, `http.request`. Everything else is
still hand-written, and that's an intentional, tracked state — see
[`internal/tools/schemagen`](../../tools/schemagen)'s package doc for why a
full migration is a separate, much larger effort (it requires adding a
schema-tagged companion struct per node type, not just running a tool).

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
