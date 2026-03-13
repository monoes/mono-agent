# Workflow JSON Storage & Smart Node Configuration Design

## Goal

Replace SQLite-based workflow storage with self-contained JSON files. Every workflow is a single portable `.json` file that includes its nodes, connections, and per-node config schemas. Add a smart Inspector UI with resource pickers so users can browse/create external assets (spreadsheets, channels, repos) directly from node configuration.

## Architecture

**Workflow files** live in `~/.monoes/workflows/<id>.json`. Each node carries its schema inline — the file is fully portable, git-friendly, and human-readable. SQLite is retained only for connections, sessions, executions, and people data.

**Default schemas** are embedded in the Go binary (`internal/workflow/schemas/*.json`, one per node type). When a user adds a node to a workflow, its default schema is copied into the node JSON. From that point the schema belongs to the workflow — it travels with the file, can be customized per-workflow, and never needs the binary's defaults again.

**Resource fetching** is done server-side. Two new Wails-bound functions — `ListResources` and `CreateResource` — authenticate using the stored connection credentials and call external APIs (Google, Slack, GitHub, etc.). Tokens never reach the browser.

**Inspector UI** renders fields entirely from the node's embedded schema. No hardcoded `NODE_CONFIG_FIELDS` in the frontend. A `resource_picker` field type renders a searchable inline dropdown with an expand button that opens a paginated list with a "Create New" action.

---

## Workflow JSON Format

File: `~/.monoes/workflows/<uuid>.json`

```json
{
  "id": "baf48911-aee6-434e-ad28-14694858dd97",
  "name": "L1",
  "description": "",
  "version": 1,
  "is_active": true,
  "created_at": "2026-03-13T09:00:00Z",
  "updated_at": "2026-03-13T09:00:00Z",
  "nodes": [
    {
      "id": "node-1",
      "type": "trigger.manual",
      "name": "Manual Trigger",
      "position": { "x": 100, "y": 200 },
      "disabled": false,
      "config": {},
      "schema": {
        "credential_platform": null,
        "fields": []
      }
    },
    {
      "id": "node-2",
      "type": "linkedin.find_by_keyword",
      "name": "Find Investors",
      "position": { "x": 300, "y": 200 },
      "disabled": false,
      "config": {
        "keyword": "startup investor",
        "location": "Berlin"
      },
      "schema": {
        "credential_platform": null,
        "fields": [
          { "key": "keyword",  "label": "Search Keywords", "type": "text",   "required": true,  "help": "Keywords to search LinkedIn for" },
          { "key": "location", "label": "Location Filter", "type": "text",   "required": false, "help": "Filter results by location" },
          { "key": "limit",    "label": "Max Results",     "type": "number", "required": false, "default": 20 }
        ]
      }
    },
    {
      "id": "node-3",
      "type": "service.google_sheets",
      "name": "Store in Google Sheet",
      "position": { "x": 550, "y": 200 },
      "disabled": false,
      "config": {
        "credential_id": "63e07eb1-b899-4682-9b0f-c9e30adaad7f",
        "spreadsheet_id": "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms",
        "sheet_name": "Investors",
        "operation": "append_rows"
      },
      "schema": {
        "credential_platform": "google_sheets",
        "fields": [
          {
            "key": "spreadsheet_id",
            "label": "Spreadsheet",
            "type": "resource_picker",
            "required": true,
            "resource": { "type": "spreadsheets", "create_label": "Create New Spreadsheet" },
            "help": "Select or create a Google Sheets spreadsheet"
          },
          {
            "key": "sheet_name",
            "label": "Sheet Name",
            "type": "text",
            "required": false,
            "default": "Sheet1",
            "help": "Name of the sheet tab. Defaults to Sheet1."
          },
          {
            "key": "operation",
            "label": "Operation",
            "type": "select",
            "required": true,
            "options": ["read_rows", "append_rows", "update_rows", "clear_range"],
            "default": "append_rows"
          },
          {
            "key": "range",
            "label": "Range",
            "type": "text",
            "required": false,
            "placeholder": "e.g. A1:D100",
            "help": "Leave empty to auto-detect from data",
            "depends_on": { "field": "operation", "values": ["read_rows", "update_rows", "clear_range"] }
          }
        ]
      }
    }
  ],
  "connections": [
    { "id": "edge-1", "source": "node-1", "source_handle": "main", "target": "node-2", "target_handle": "in" },
    { "id": "edge-2", "source": "node-2", "source_handle": "main", "target": "node-3", "target_handle": "in" }
  ]
}
```

---

## Field Type Specification

| Type | Widget | Extra properties |
|------|--------|-----------------|
| `text` | Text input | `placeholder`, `default` |
| `number` | Number input | `default`, `min`, `max` |
| `password` | Masked input | — |
| `textarea` | Multi-line text | `rows` (default 3) |
| `select` | Dropdown | `options: string[]`, `default` |
| `boolean` | Toggle | `default: false` |
| `array` | Tag/chip input | `item_type` ("text"), `default: []` |
| `code` | Code editor | `language` ("javascript", "sql", "python") |
| `resource_picker` | Searchable dropdown + expand | `resource: { type, create_label, param_field }` |

All fields also support:
- `required: bool`
- `help: string` — grey hint below field
- `placeholder: string`
- `depends_on: { field: string, values: string[] }` — hide unless condition met

---

## Resource Picker UX

### Compact (default)
```
Spreadsheet   [ Search spreadsheets...         ▼ ] [⊞]
```

### Inline expanded (after ⊞)
```
┌─ Select Spreadsheet ─────────────────────────────────┐
│ 🔍 Search...                       [+ Create New]   │
│                                                      │
│ ● My Investor List          Updated 2h ago          │
│   Berlin Startups 2026      Updated yesterday        │
│   Q1 Outreach               Updated 3 days ago      │
│   [Load more...]                                     │
└──────────────────────────────────────────────────────┘
```

"Create New" shows an inline name input → calls `CreateResource` → auto-selects result.

---

## Backend Resource API

### New Wails-bound functions

```go
type ResourceItem struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type ResourceListResult struct {
    Items      []ResourceItem `json:"items"`
    NextCursor string         `json:"next_cursor,omitempty"`
    Error      string         `json:"error,omitempty"`
}

type ResourceItemResult struct {
    Item  *ResourceItem `json:"item,omitempty"`
    Error string        `json:"error,omitempty"`
}

func (a *App) ListResources(platform, resourceType, credentialID, query string) ResourceListResult
func (a *App) CreateResource(platform, resourceType, credentialID, name string) ResourceItemResult
```

### Supported resource types

| Platform | `resourceType` | API call |
|---------|---------------|---------|
| `google_sheets` | `spreadsheets` | Drive API — list Sheets files |
| `google_sheets` | `sheets` | Sheets API — list tabs in a spreadsheet |
| `google_drive` | `folders` | Drive API — list folders |
| `gmail` | `labels` | Gmail API — list labels |
| `slack` | `channels` | Slack conversations.list |
| `slack` | `users` | Slack users.list |
| `discord` | `channels` | Discord API — list guild channels |
| `github` | `repos` | GitHub API — list user/org repos |
| `github` | `branches` | GitHub API — list branches for repo |
| `notion` | `databases` | Notion API — list databases |
| `notion` | `pages` | Notion API — list pages |
| `airtable` | `bases` | Airtable API — list bases |
| `airtable` | `tables` | Airtable API — list tables in base |
| `linear` | `teams` | Linear API — list teams |
| `linear` | `projects` | Linear API — list projects |
| `asana` | `workspaces` | Asana API — list workspaces |
| `asana` | `projects` | Asana API — list projects |
| `jira` | `projects` | Jira API — list projects |
| `db.postgres` | `tables` | information_schema.tables query |
| `db.mysql` | `tables` | information_schema.tables query |

---

## Default Schema Embedding

```
internal/workflow/schemas/
  trigger.manual.json
  trigger.schedule.json
  trigger.webhook.json
  core.if.json
  core.switch.json
  core.code.json
  core.filter.json
  core.sort.json
  core.limit.json
  core.set.json
  core.aggregate.json
  core.merge.json
  core.wait.json
  core.stop_error.json
  core.split_in_batches.json
  core.remove_duplicates.json
  core.compare_datasets.json
  http.request.json
  http.ftp.json
  http.ssh.json
  system.execute_command.json
  system.rss_read.json
  data.datetime.json
  data.crypto.json
  data.html.json
  data.xml.json
  data.markdown.json
  data.spreadsheet.json
  data.compression.json
  data.write_binary_file.json
  db.postgres.json
  db.mysql.json
  db.mongodb.json
  db.redis.json
  comm.slack.json
  comm.discord.json
  comm.telegram.json
  comm.email_send.json
  comm.email_read.json
  comm.twilio.json
  comm.whatsapp.json
  service.google_sheets.json
  service.google_drive.json
  service.gmail.json
  service.github.json
  service.notion.json
  service.airtable.json
  service.jira.json
  service.linear.json
  service.asana.json
  service.stripe.json
  service.shopify.json
  service.salesforce.json
  service.hubspot.json
  browser.generic.json
```

Go loader:
```go
//go:embed schemas/*.json
var embeddedSchemas embed.FS

func LoadDefaultSchema(nodeType string) (*NodeSchema, error)
```

---

## File Storage & Migration

### Storage layout
```
~/.monoes/
  workflows/
    baf48911-aee6-434e-ad28-14694858dd97.json   ← L1
    68c9b38b-c351-46cd-8e82-373bad7ebc81.json   ← LinkedIn to Google Sheets
    ...
  connections/  (stays in SQLite)
  monoes.db     (sessions, executions, people — SQLite stays)
```

### App startup sequence
1. Scan `~/.monoes/workflows/*.json`
2. Build in-memory index: `id → WorkflowMeta` (name, active, updated_at)
3. Watch directory with `fsnotify` for live updates

### Migration command
```bash
monoes workflow migrate
```
Reads all SQLite `workflows` + `workflow_nodes` + `workflow_connections`, embeds default schemas per node type, writes `~/.monoes/workflows/<id>.json`. Non-destructive — SQLite data remains untouched.

### Wails workflow functions updated
- `SaveWorkflow` → writes JSON file
- `LoadWorkflow` → reads JSON file
- `ListWorkflows` → scans directory
- `DeleteWorkflow` → removes JSON file
- `GetWorkflowNodeTypes` → returns all registered types + embedded schemas

---

## Inspector Changes

The Inspector becomes fully schema-driven:

```jsx
// Before: hardcoded NODE_CONFIG_FIELDS lookup
const fields = NODE_CONFIG_FIELDS[node.subtype] || []

// After: schema comes from the node itself
const fields = node.schema?.fields || []
```

New components needed:
- `ResourcePickerField` — compact dropdown + expand button
- `ResourceBrowser` — expanded panel with search, list, pagination, create
- `CodeEditorField` — syntax-highlighted textarea for JS/SQL
- `ArrayField` — tag/chip input for array values
- `DependsOnWrapper` — hides field when `depends_on` condition not met

---

## Implementation Sequence

16 tasks in order:

1. Write all 50 default schema JSON files (`internal/workflow/schemas/`)
2. Go schema loader with `//go:embed` + `LoadDefaultSchema(nodeType)`
3. Update `WorkflowNode` model to include `Schema` field
4. `WorkflowFileStore` — read/write JSON files for workflows
5. `monoes workflow migrate` CLI command
6. Update `SaveWorkflow` / `LoadWorkflow` / `ListWorkflows` in app.go to use file store
7. `ListResources` backend function (start with google_sheets + slack)
8. `CreateResource` backend function
9. Add remaining platform handlers for ListResources
10. Update `GetWorkflowNodeTypes` to return schemas
11. Update `addNode` in NodeRunner.jsx to embed schema from node type
12. Schema-driven Inspector rendering (replace NODE_CONFIG_FIELDS)
13. `ResourcePickerField` component (compact state)
14. `ResourceBrowser` component (expanded state + create)
15. `DependsOnWrapper`, `ArrayField`, `CodeEditorField` components
16. End-to-end test: L1 workflow with Google Sheets resource picker
