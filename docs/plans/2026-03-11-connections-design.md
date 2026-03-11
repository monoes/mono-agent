# Connections Layer Design
Date: 2026-03-11

## Overview

A unified authentication and credential management layer that lets users connect every supported platform through the smoothest available method — OAuth, API key, browser session, connection string, or app password. Managed via both CLI and UI.

---

## Goals

- One entry point: `monoes connect <platform>` — the CLI figures out what methods are available and guides the user through the best one
- Full UI complement: two clearly separated pages — Sessions (UI/browser) and Credentials (API/OAuth)
- Every supported platform covered: ~25 platforms across social, services, communication, and databases
- Per-platform, offer every method that works — let the user choose
- Plaintext storage in SQLite for now; UX is the priority

---

## Architecture: Platform Registry

### Core Pattern

A single **platform registry** (`internal/connections/registry.go`) is the source of truth for every supported platform. Both the CLI and UI read from it. Adding a new platform = adding one registry entry.

### Data Structures

```go
type AuthMethod string

const (
    MethodOAuth    AuthMethod = "oauth"
    MethodAPIKey   AuthMethod = "apikey"
    MethodBrowser  AuthMethod = "browser"     // cookie capture via rod
    MethodConnStr  AuthMethod = "connstring"  // databases
    MethodAppPass  AuthMethod = "apppassword"
)

type CredentialField struct {
    Key      string // e.g. "api_key", "access_token"
    Label    string // e.g. "Personal Access Token"
    Secret   bool   // mask in UI/CLI
    Required bool
    HelpURL  string // link to where user gets this value
    HelpText string // step-by-step instruction
}

type OAuthConfig struct {
    AuthURL      string
    TokenURL     string
    ClientID     string
    ClientSecret string // from env or config
    Scopes       []string
    CallbackPort int    // local server port, default 9876
}

type PlatformDef struct {
    ID          string
    Name        string
    Category    string     // "social" | "service" | "database" | "communication"
    ConnectVia  string     // "UI" | "API"  — which page it appears on
    Methods     []AuthMethod // ordered: first = recommended
    Fields      map[AuthMethod][]CredentialField
    OAuthConfig *OAuthConfig
    ValidateFn  string     // name of validation function
    IconEmoji   string
}
```

### Platforms Covered

**Social — ConnectVia: UI (browser session)**
| Platform  | Methods          |
|-----------|-----------------|
| Instagram | browser          |
| LinkedIn  | browser          |
| X         | browser          |
| TikTok    | browser          |
| Telegram  | browser, apikey  |

**Services — ConnectVia: API**
| Platform     | Methods            |
|--------------|--------------------|
| GitHub       | oauth, apikey       |
| Notion       | oauth, apikey       |
| Airtable     | oauth, apikey       |
| Jira         | oauth, apikey       |
| Linear       | oauth, apikey       |
| Asana        | oauth, apikey       |
| Stripe       | apikey              |
| Shopify      | oauth, apikey       |
| Salesforce   | oauth               |
| HubSpot      | oauth, apikey       |
| Google Sheets| oauth               |
| Gmail        | oauth, apppassword  |
| Google Drive | oauth               |

**Communication — ConnectVia: API**
| Platform  | Methods  |
|-----------|----------|
| Slack     | oauth    |
| Discord   | apikey   |
| Twilio    | apikey   |
| WhatsApp  | apikey   |
| SMTP/IMAP | apppassword |

**Databases — ConnectVia: API**
| Platform   | Methods   |
|------------|-----------|
| PostgreSQL | connstring |
| MySQL      | connstring |
| MongoDB    | connstring |
| Redis      | connstring |

---

## CLI Design

### Entry Point

`monoes connect <platform>` — single command, platform drives the flow.

**OAuth flow:**
```
$ monoes connect github
┌─ Connect GitHub ─────────────────────────────────────────────────┐
│ How do you want to connect?                                      │
│   [1] OAuth  — opens browser, no copy-paste (recommended)       │
│   [2] API Key — paste a Personal Access Token                    │
└──────────────────────────────────────────────────────────────────┘
Choice: 1
→ Starting OAuth flow...
→ Opening: https://github.com/login/oauth/authorize?...
→ Waiting for callback on http://localhost:9876/callback
✓ Authorized. Fetching token...
✓ Connected as @morteza  (scopes: repo, read:user)
✓ Saved as "GitHub – morteza"
```

**API key flow:**
```
$ monoes connect stripe
→ Stripe supports: API Key only.
  Get your key at: https://dashboard.stripe.com/apikeys

Secret Key (sk_...): **********************
→ Validating...
✓ Connected  (account: Acme Corp, mode: live)
✓ Saved as "Stripe – Acme Corp"
```

**Browser session flow:**
```
$ monoes connect instagram
→ Instagram connects via browser session.
→ Opening browser — log in, then press Enter.
  [browser opens]
✓ Session captured for @morteza  (expires in 30 days)
```

### Supporting Commands

```
monoes connect list                     # all connections grouped by category
monoes connect list --platform github   # filter by platform
monoes connect test <id>                # re-validate a connection
monoes connect remove <id>              # delete
monoes connect refresh <id>             # re-auth / refresh OAuth token
```

### OAuth Local Server

OAuth platforms use a lightweight local HTTP server on `localhost:9876` (configurable). Flow:
1. Build authorization URL with state parameter (CSRF protection)
2. Open URL in default browser (`open` on macOS)
3. Listen for GET `/callback?code=...&state=...`
4. Validate state, exchange code for token via provider's token URL
5. Shut down server
6. Validate token by calling the platform's identity endpoint
7. Save to `connections` table

---

## UI Design

### Sessions Page (existing, enhanced)

**"Connect via UI"** label in header. Shows only browser/cookie-based social platforms.

- Platform cards: icon, username, expiry countdown, status dot (green/red)
- Add account: platform picker → shows `monoes connect <platform>` instruction
- Auto-refreshes when new session appears (polls every 10s when page is active)
- No functional change to underlying cookie-capture mechanism

### Credentials Page (new)

**"Connect via API"** label in header. Three grouped sections: Services & APIs, Communication, Databases.

Layout per platform row:
```
● GitHub    morteza    OAuth    [Test] [Remove]
○ Stripe    —          —        [+ Connect]
```

- Green dot = connected, grey = not connected
- **[+ Connect]** opens a slide-in panel with:
  - Method selector (tabs/buttons) if multiple methods available
  - For OAuth: single "Connect with GitHub" button
  - For API key: labeled input fields with HelpText + HelpURL link
  - "Test Connection" button before saving
  - On success: shows resolved account name
- **[Test]** re-validates and updates `last_tested` + `status`
- **[Remove]** deletes the connection after confirmation

Both pages listed in sidebar under **DATA** section.

---

## Storage

### New Table: `connections`

```sql
CREATE TABLE IF NOT EXISTS connections (
    id          TEXT PRIMARY KEY,
    platform    TEXT NOT NULL,
    method      TEXT NOT NULL,
    label       TEXT NOT NULL,
    account_id  TEXT,
    data        TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'active',
    last_tested TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_connections_platform ON connections(platform);
CREATE INDEX IF NOT EXISTS idx_connections_status   ON connections(status);
```

- `data`: JSON blob — fields are platform+method specific (token, refresh_token, expiry, api_key, connection_string, etc.)
- `account_id`: resolved identity (username, email, account name) from validation call
- Existing `crawler_sessions` and `workflow_credentials` tables unchanged for backward compatibility

### New Package: `internal/connections`

```
internal/connections/
├── registry.go      # PlatformDef map, all platform definitions
├── manager.go       # ConnectionManager: Connect(), List(), Test(), Remove(), Refresh()
├── oauth.go         # local OAuth callback server
├── validate.go      # per-platform validation functions
└── storage.go       # SQLite CRUD for connections table
```

---

## Workflow Node Integration

Nodes reference a connection by ID in their config:

```json
{ "credential_id": "abc-123-def", "repo": "monoes/agent" }
```

At execution time the engine resolves the credential:
1. Look up `connections` table by `credential_id`
2. Decrypt/deserialize `data` JSON
3. Inject platform-specific fields into node config map
4. Call `Execute()` with enriched config

The credential dropdown in the canvas node config panel is populated from `connections` filtered by platform, showing `label` + `account_id`.

Backward compat: if `credential_id` references a `workflow_credentials` entry (old format), fall back to that table.

---

## Implementation Sequence

1. `internal/connections/registry.go` — platform definitions for all ~25 platforms
2. `internal/connections/storage.go` — CRUD for `connections` table
3. `internal/connections/oauth.go` — local OAuth callback server
4. `internal/connections/validate.go` — per-platform validation functions
5. `internal/connections/manager.go` — ConnectionManager orchestrating all flows
6. `cmd/monoes/connect.go` — CLI: `monoes connect` + subcommands
7. Wails app.go — new bindings: `ListConnections`, `ConnectPlatform`, `TestConnection`, `RemoveConnection`
8. `wails-app/frontend/src/pages/Credentials.jsx` — new Credentials page
9. `wails-app/frontend/src/pages/Sessions.jsx` — enhance with "Connect via UI" label + auto-refresh
10. `wails-app/frontend/src/components/Sidebar.jsx` — add Credentials nav item
11. Canvas node config panel — credential dropdown per platform
12. Workflow engine — credential injection from `connections` table
