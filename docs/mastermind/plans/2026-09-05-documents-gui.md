# Documents GUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `Skill("mastermind-taskdev")` (recommended) or `Skill("mastermind-execute")` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. (Not installed in this project — the controlling session dispatches tasks directly via the Agent tool, per this project's established practice.)

**Goal:** A Wails GUI "Documents" page exposing Phase 3's profile-document backend (`monoagentcli profile upload-document/documents list/rm/search-knowledge`), which shipped CLI-only and was never given a GUI surface — closing a gap against the original request ("profile enable user to upload files also... chat... can always access that").

**Architecture:** `wails-app/app_documents.go` (new file, sibling to `app_vault.go`/`app_applications.go`) shells out to `monoagentcli --json`, no internal packages imported. `wails-app/frontend/src/pages/Documents.jsx` — a table of uploaded documents with upload/delete, plus a knowledge-search box.

**Tech Stack:** Go (`main` package, Wails v2.15.0), React 19 + `lucide-react`, Vite + Vitest. No new dependencies.

## Global Constraints

- `app_documents.go` never imports `internal/vault` or `internal/monomind` — shells out to `monoagentcli` only, mirroring `app_vault.go`/`app_applications.go`.
- Verified JSON shapes (checked against `cmd/monoagentcli/profile_documents.go`, `internal/vault/documents.go`, `internal/monomind/docsync.go`):
  - `profile upload-document <path> [--source X] --json` → `{"id": "..."}`
  - `profile documents list --json` → `[]DocumentEntry`, **no json tags → PascalCase**: `ID, Path, Filename, SizeBytes, Source, ApplicationID, CreatedAt`.
  - `profile documents rm <id> --json` → `{"id": "..."}`
  - `profile search-knowledge <query> --json` → `[]KnowledgeResult`, **no json tags → PascalCase**: `Path, Excerpt, Score`.
- Sidebar locale path is `sidebar.nav.<labelKey>` (verified in the Applications GUI phase).
- File upload from a Wails webview requires the native file picker (`runtime.OpenFileDialog`) — the browser cannot read local file paths directly. `OpenJSONFilePicker` already exists in `app_applications.go` but is JSON-filtered; this task needs an unfiltered variant.
- Binding regeneration: `cd wails-app && wails generate module` (requires `frontend/dist/` to exist — `npm run build` first if missing). Pinned version `v2.15.0`.
- Commit messages end with:
  ```
  Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
  ```

---

### Task A: Backend — `wails-app/app_documents.go`

**Files:** Create `wails-app/app_documents.go`

**Interfaces:**
- Consumes: `findMonoAgentCLI()`, `a.ctx`, `a.getActiveProfileID()`, `a.runMonoCLI` (already defined in `app_applications.go` in the same `main` package — reuse it, do not redefine).
- Produces: `ProfileDocument{ID, Path, Filename, SizeBytes, Source, ApplicationID, CreatedAt}`, `KnowledgeSearchResult{Path, Excerpt, Score}`; methods `ListProfileDocuments() ([]ProfileDocument, error)`, `UploadProfileDocument(path, source string) (string, error)`, `DeleteProfileDocument(id string) error`, `SearchProfileKnowledge(query string) ([]KnowledgeSearchResult, error)`, `OpenAnyFilePicker(title string) string` (unfiltered native picker, distinct from the existing JSON-only `OpenJSONFilePicker`).

- [ ] **Step 1: Write the file**

```go
// wails-app/app_documents.go
package main

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ProfileDocument mirrors internal/vault.DocumentEntry's fields (that
// struct has no json tags -> PascalCase on the wire), remapped to a
// stable lowercase contract for the frontend.
type ProfileDocument struct {
	ID            string `json:"id"`
	Filename      string `json:"filename"`
	Path          string `json:"path"`
	SizeBytes     int64  `json:"size_bytes"`
	Source        string `json:"source"`
	ApplicationID string `json:"application_id"`
	CreatedAt     string `json:"created_at"`
}

// KnowledgeSearchResult mirrors internal/monomind.KnowledgeResult (also
// tagless -> PascalCase on the wire).
type KnowledgeSearchResult struct {
	Path    string  `json:"path"`
	Excerpt string  `json:"excerpt"`
	Score   float64 `json:"score"`
}

// ListProfileDocuments lists every document uploaded to the active
// profile's vault.
func (a *App) ListProfileDocuments() ([]ProfileDocument, error) {
	var raw []struct {
		ID, Path, Filename, Source, ApplicationID, CreatedAt string
		SizeBytes                                            int64
	}
	if err := a.runMonoCLI("", &raw, "profile", "documents", "list"); err != nil {
		return nil, err
	}
	out := make([]ProfileDocument, 0, len(raw))
	for _, d := range raw {
		out = append(out, ProfileDocument{
			ID: d.ID, Filename: d.Filename, Path: d.Path, SizeBytes: d.SizeBytes,
			Source: d.Source, ApplicationID: d.ApplicationID, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

// UploadProfileDocument registers path in the vault and indexes it for
// knowledge search. source defaults to "upload" (the CLI's own default)
// if empty.
func (a *App) UploadProfileDocument(path, source string) (string, error) {
	args := []string{"profile", "upload-document", path}
	if source != "" {
		args = append(args, "--source", source)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := a.runMonoCLI("", &result, args...); err != nil {
		return "", err
	}
	return result.ID, nil
}

// DeleteProfileDocument removes a document from the vault.
func (a *App) DeleteProfileDocument(id string) error {
	return a.runMonoCLI("", nil, "profile", "documents", "rm", id)
}

// SearchProfileKnowledge searches indexed profile documents (the same
// search chat uses automatically).
func (a *App) SearchProfileKnowledge(query string) ([]KnowledgeSearchResult, error) {
	var raw []struct {
		Path, Excerpt string
		Score         float64
	}
	if err := a.runMonoCLI("", &raw, "profile", "search-knowledge", query); err != nil {
		return nil, err
	}
	out := make([]KnowledgeSearchResult, 0, len(raw))
	for _, r := range raw {
		out = append(out, KnowledgeSearchResult{Path: r.Path, Excerpt: r.Excerpt, Score: r.Score})
	}
	return out, nil
}

// OpenAnyFilePicker opens a native file picker with no extension filter
// (profile documents can be PDF, DOCX, plain text, etc.) and returns the
// selected path (empty string if cancelled).
func (a *App) OpenAnyFilePicker(title string) string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil {
		return ""
	}
	return path
}
```

- [ ] **Step 2: Verify it builds**

Run: `export PATH="$HOME/.local/go/bin:$PATH" && cd wails-app && go build ./... && go vet ./...` (wails-app is its own Go module — root-level `go build ./...` does not compile it; confirmed during the Applications GUI phase).
Expected: both succeed with no output.

- [ ] **Step 3: Commit**

```bash
git add wails-app/app_documents.go
git commit -m "$(cat <<'EOF'
feat(gui): add profile Documents backend methods

Shells out to monoagentcli profile upload-document/documents/
search-knowledge, exactly matching app_applications.go's convention.
Closes a gap where Phase 3's document-vault + knowledge-search backend
shipped CLI-only with no GUI surface.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task B: `Documents.jsx` — list, upload, delete, knowledge search

**Files:**
- Create: `wails-app/frontend/src/pages/Documents.jsx`
- Create: `wails-app/frontend/src/pages/Documents.test.js`

**Interfaces:**
- Consumes: `WailsApp.{ListProfileDocuments,UploadProfileDocument,DeleteProfileDocument,SearchProfileKnowledge,OpenAnyFilePicker}` (Task A, via bindings regenerated in Task C — write against the designed signatures now), `confirm` (existing `components/ConfirmDialog.jsx`), `notify` (existing `services/api.js`).
- Produces: `formatBytes(n)` (pure, exported, unit-tested), `export default function Documents()`.

- [ ] **Step 1: Write the failing test**

```js
// wails-app/frontend/src/pages/Documents.test.js
import { describe, it, expect } from 'vitest'
import { formatBytes } from './Documents.jsx'

describe('formatBytes', () => {
  it('formats bytes under 1KB as-is', () => {
    expect(formatBytes(512)).toBe('512 B')
  })
  it('formats kilobytes with one decimal', () => {
    expect(formatBytes(2048)).toBe('2.0 KB')
  })
  it('formats megabytes with one decimal', () => {
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
  })
  it('handles zero', () => {
    expect(formatBytes(0)).toBe('0 B')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run (from `wails-app/frontend/`): `npm test -- Documents.test.js`
Expected: FAIL — `Documents.jsx` doesn't exist yet.

- [ ] **Step 3: Write the page component**

```jsx
// wails-app/frontend/src/pages/Documents.jsx
import { useState, useEffect, useCallback } from 'react'
import { Upload, Search, Trash2 } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'
import { notify } from '../services/api.js'

export function formatBytes(n) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

const btnStyle = {
  background: 'rgba(0,180,216,0.1)', border: '1px solid rgba(0,180,216,0.3)',
  borderRadius: 6, padding: '6px 12px', color: '#00b4d8',
  fontFamily: 'var(--font-mono)', fontSize: 11, cursor: 'pointer',
  display: 'flex', alignItems: 'center', gap: 5,
}
const inputStyle = {
  background: '#060b11', border: '1px solid #1e3a4f', borderRadius: 5,
  padding: '6px 8px', color: '#e2e8f0', fontFamily: 'var(--font-mono)', fontSize: 11,
}

export default function Documents() {
  const [docs, setDocs] = useState([])
  const [error, setError] = useState(null)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState(null)
  const [searching, setSearching] = useState(false)

  const load = useCallback(async () => {
    try {
      setDocs((await WailsApp.ListProfileDocuments()) || [])
    } catch (e) {
      setError('Failed to load documents: ' + e)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const handleUpload = async () => {
    const path = await WailsApp.OpenAnyFilePicker('Select a document to upload')
    if (!path) return
    try {
      await WailsApp.UploadProfileDocument(path, '')
      notify('upload', 'Document uploaded and indexed for search.')
      load()
    } catch (e) {
      notify('upload', 'Upload failed: ' + e)
    }
  }

  const handleDelete = async (id, filename) => {
    if (!(await confirm(`Delete "${filename}"? This removes it from the knowledge index too.`, { title: 'Delete Document', confirmLabel: 'Delete', danger: true }))) return
    try {
      await WailsApp.DeleteProfileDocument(id)
      load()
    } catch (e) {
      notify('delete', 'Delete failed: ' + e)
    }
  }

  const handleSearch = async (e) => {
    e.preventDefault()
    if (!query.trim()) return
    setSearching(true)
    setError(null)
    try {
      setResults(await WailsApp.SearchProfileKnowledge(query))
    } catch (e) {
      setError('Search failed: ' + e)
    } finally {
      setSearching(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: 16, gap: 12, overflow: 'hidden' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <h2 style={{ margin: 0, fontSize: 16, color: 'var(--text-primary)' }}>Documents</h2>
        <div style={{ flex: 1 }} />
        <button style={btnStyle} onClick={handleUpload}><Upload size={13} /> Upload</button>
      </div>

      <form onSubmit={handleSearch} style={{ display: 'flex', gap: 8 }}>
        <input style={{ ...inputStyle, flex: 1 }} placeholder="Search your uploaded documents..." value={query} onChange={e => setQuery(e.target.value)} />
        <button type="submit" style={btnStyle} disabled={searching}><Search size={13} /> Search</button>
      </form>

      {error && <div style={{ color: '#ff6b6b', fontSize: 12 }}>{error}</div>}

      {results && (
        <div style={{ maxHeight: '30%', overflow: 'auto', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 6, padding: 8 }}>
          {results.length === 0 && <div style={{ color: 'var(--text-muted)', fontSize: 12 }}>No matching content found.</div>}
          {results.map((r, i) => (
            <div key={i} style={{ fontSize: 11, padding: '4px 0', borderBottom: i < results.length - 1 ? '1px solid rgba(255,255,255,0.05)' : 'none' }}>
              <span style={{ color: '#00b4d8' }}>[{r.score.toFixed(2)}]</span> <span style={{ color: 'var(--text-muted)' }}>{r.path}</span>
              <div>{r.excerpt}</div>
            </div>
          ))}
        </div>
      )}

      <div style={{ flex: 1, overflow: 'auto' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
          <thead>
            <tr style={{ textAlign: 'left', color: 'var(--text-muted)', fontSize: 10, textTransform: 'uppercase' }}>
              <th style={{ padding: '6px 8px' }}>Filename</th>
              <th style={{ padding: '6px 8px' }}>Source</th>
              <th style={{ padding: '6px 8px' }}>Size</th>
              <th style={{ padding: '6px 8px' }}>Uploaded</th>
              <th style={{ padding: '6px 8px' }} />
            </tr>
          </thead>
          <tbody>
            {docs.map(d => (
              <tr key={d.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '8px' }}>{d.filename}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.source}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{formatBytes(d.size_bytes)}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.created_at}</td>
                <td style={{ padding: '8px' }}>
                  <button style={{ ...btnStyle, color: '#ef4444', border: '1px solid rgba(239,68,68,0.3)', padding: '4px 8px' }} onClick={() => handleDelete(d.id, d.filename)}>
                    <Trash2 size={12} />
                  </button>
                </td>
              </tr>
            ))}
            {docs.length === 0 && (
              <tr><td colSpan={5} style={{ padding: 16, textAlign: 'center', color: 'var(--text-muted)' }}>No documents uploaded.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (from `wails-app/frontend/`): `npm test -- Documents.test.js`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add wails-app/frontend/src/pages/Documents.jsx wails-app/frontend/src/pages/Documents.test.js
git commit -m "$(cat <<'EOF'
feat(gui): add Documents page (upload, list, delete, knowledge search)

Table of uploaded profile documents plus a search box over the
Second Brain knowledge index. formatBytes is extracted pure and
separately tested per this codebase's established convention.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```

---

### Task C: Navigation registration, binding regeneration, final verification

**Files:**
- Modify: `wails-app/frontend/src/components/Sidebar.jsx`
- Modify: `wails-app/frontend/src/App.jsx`
- Modify: `wails-app/frontend/src/locales/en.json`, `es.json`
- Regenerate: `wails-app/frontend/src/wailsjs/go/main/App.js`, `.d.ts`, `models.ts`

**Interfaces:**
- Consumes: `Documents` default export (Task B).
- Produces: the page reachable from the sidebar. Terminal task.

- [ ] **Step 1: Add the nav item**

In `Sidebar.jsx`, add `FileText` to the lucide-react import list, and add to `NAV_ITEMS` (DATA section, after `applications`):
```js
  { id: 'documents', labelKey: 'documents', icon: FileText, section: 'DATA' },
```

- [ ] **Step 2: Add locale keys**

`en.json`: add `"documents": "Documents"` inside `sidebar.nav` (alongside the just-added `"applications"` key).
`es.json`: read the file first to match exact key style/position, add `"documents": "Documentos"`.

- [ ] **Step 3: Register in App.jsx**

Add `import Documents from './pages/Documents.jsx'` and `documents: <Documents />` to `persistentPages`.

- [ ] **Step 4: Regenerate bindings**

```bash
cd wails-app/frontend
ls node_modules >/dev/null 2>&1 || npm install
npm run build
cd ..
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
wails generate module
```
Expected: succeeds (harmless `Not found: time.Time` diagnostics only). Verify: `grep -c ListProfileDocuments frontend/src/wailsjs/go/main/App.js` >= 1.

- [ ] **Step 5: Full verification**

```bash
cd wails-app/frontend && npm test && npm run build
cd .. && export PATH="$HOME/.local/go/bin:$PATH" && go build ./... && go vet ./...
cd .. && go build ./... && go test ./... 2>&1 | grep -Ev "^ok|no test files"
```
Expected: all tests pass, both builds succeed, final grep produces no output.

- [ ] **Step 6: Commit**

```bash
git add wails-app/frontend/src/components/Sidebar.jsx wails-app/frontend/src/App.jsx wails-app/frontend/src/locales/en.json wails-app/frontend/src/locales/es.json wails-app/frontend/src/wailsjs/go/main/App.js wails-app/frontend/src/wailsjs/go/main/App.d.ts wails-app/frontend/src/wailsjs/go/models.ts
git commit -m "$(cat <<'EOF'
feat(gui): register the Documents page in navigation

New sidebar nav item, locale keys (en/es), App.jsx routing entry, and
regenerated wailsjs bindings for the Task A backend methods.

Claude-Session: https://claude.ai/code/session_01HAwZTzyfgDKwHy6QgekkzH
EOF
)"
```
(If `wails-app/frontend/src/wailsjs/runtime/*` shows mode-only changes too, include them — a known harmless side effect of `wails generate module`.)

---

## Self-Review

**1. Spec coverage:** Upload (native picker → `UploadProfileDocument`) ✅. List with delete ✅. Knowledge search ✅. Standalone page per user's chosen placement ✅.

**2. Placeholder scan:** none — every step has complete code.

**3. Type consistency:** `ProfileDocument`'s json tags (`size_bytes`, `application_id`, `created_at`) match `Documents.jsx`'s field access (`d.size_bytes`, `d.created_at`) exactly. `KnowledgeSearchResult`'s tags (`score`, `path`, `excerpt`) match `r.score`/`r.path`/`r.excerpt`.
