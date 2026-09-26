# Package JSON Schemas

JSON Schemas (draft 2020-12) for the files of a browser automation package
(spec: `docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md` §4.5):

| File | Validates | Go type |
|---|---|---|
| `automation.v1.schema.json` | `automation.json` | `automation.Manifest` |
| `action.v1.schema.json` | `actions/<name>.json` | `action.ActionDef` |
| `fragment.v1.schema.json` | `fragments/<name>.json` | `action.FragmentDef` |
| `selectors.v1.schema.json` | `selectors.json` | `map[string]action.SelectorEntry` |

**Generated — do not edit by hand.** They are produced from the Go structs
(descriptions from struct comments and `internal/schemagen/fielddocs.go`):

```bash
go generate ./internal/schemagen
```

Regenerate after changing any of those structs. `TestSchemasUpToDate` in
`internal/schemagen` fails when these files are stale. They are embedded as
`data.SchemasFS` and referenced by `"$schema"` in package files.
