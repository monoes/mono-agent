import { useState, useEffect, useCallback, useRef } from 'react'
import { Upload, Search, Trash2, Sparkles, CheckCircle2, AlertTriangle, XCircle, Eye, PlayCircle } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'
import { api, notify, onMonomindInitEvent, onDocumentsChanged } from '../services/api.js'
import FileViewerModal, { fileViewerKind } from '../components/FileViewerModal.jsx'

// maxInlinePreviewBytes mirrors the backend's own GetProfileDocumentData
// cap (wails-app/app_files.go) so an oversized file is routed straight to
// OpenPathWithOS instead of round-tripping to Go just to be rejected.
const maxInlinePreviewBytes = 25 * 1024 * 1024

// documentBadgeState classifies a document row into exactly one of four
// Knowledge Graph badge states. The 4th state (monomind_not_set_up) only
// overrides a NEVER-indexed row: a document that was successfully indexed
// before keeps showing indexed/stale even if monomind later becomes
// un-initialized for this profile, since that fact doesn't retroactively
// change what already happened.
export function documentBadgeState(doc, monomindNotInitialized) {
  if (!doc.indexed && monomindNotInitialized) return 'monomind_not_set_up'
  if (doc.indexed && doc.stale) return 'stale'
  if (doc.indexed) return 'indexed'
  return 'not_indexed'
}

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

// Not indexed for the survivable reasons (never attempted, or a transient
// backend hiccup) vs. because monomind genuinely isn't installed at all --
// mirrors the fallback-copy pattern in Agents.jsx ("/not found/i.test(...)")
// against internal/monomind/find.go's ErrNotFound message.
export function isMonomindMissing(indexError) {
  return /monomind not found/i.test(indexError || '')
}

export default function Documents() {
  const [docs, setDocs] = useState([])
  const [error, setError] = useState(null)
  const [query, setQuery] = useState('')
  const [results, setResults] = useState(null)
  const [searching, setSearching] = useState(false)
  // Per-profile check, independent of any single document's own
  // indexed/index_error outcome -- lets us suggest setup BEFORE the user
  // uploads anything, not just after a failed upload. Mirrors Agents.jsx's
  // identical notInitialized check.
  const [notInitialized, setNotInitialized] = useState(false)
  const [initStatus, setInitStatus] = useState('idle') // idle | running | error
  const initRunningRef = useRef(false)
  const [viewingDoc, setViewingDoc] = useState(null)
  const [indexingIds, setIndexingIds] = useState(() => new Set())
  const [selectedIds, setSelectedIds] = useState(() => new Set())
  // null | 'index' | 'delete' — gates re-entrancy on the bulk buttons (and
  // the per-row Index button, to avoid a race with an in-flight bulk run)
  // and drives the running bulk button's live progress label.
  const [bulkBusy, setBulkBusy] = useState(null)
  const [bulkProgress, setBulkProgress] = useState({ done: 0, total: 0 })

  const load = useCallback(async () => {
    try {
      setDocs((await WailsApp.ListProfileDocuments()) || [])
    } catch (e) {
      setError('Failed to load documents: ' + e)
    }
  }, [])

  useEffect(() => { load() }, [load])
  useEffect(() => { api.isMonomindInitialized().then(v => setNotInitialized(!v)) }, [])

  // Live-refresh: the background document watcher (wails-app/app_documents_watch.go)
  // discovers files added to the profile folder from outside the app (e.g.
  // dropped in via Finder) and reconciles them into the same list this
  // page reads — reload whenever it signals a change.
  useEffect(() => onDocumentsChanged(() => load()), [load])

  useEffect(() => onMonomindInitEvent((payload) => {
    if (!initRunningRef.current) return
    if (payload.kind === 'error') {
      initRunningRef.current = false
      setInitStatus('error')
      notify('initialize monomind', payload.message || 'failed')
    } else if (payload.kind === 'done') {
      initRunningRef.current = false
      setInitStatus('idle')
      setNotInitialized(false)
    }
  }), [])

  const startMonomindInit = async () => {
    initRunningRef.current = true
    setInitStatus('running')
    await api.initializeMonomindProfile()
  }

  const handleUpload = async () => {
    const path = await WailsApp.OpenAnyFilePicker('Select a document to upload')
    if (!path) return
    try {
      const result = await WailsApp.UploadProfileDocument(path, '')
      if (!result.indexed) {
        notify('upload', isMonomindMissing(result.index_error)
          ? 'Uploaded, but not indexed for search — monomind isn\'t installed.'
          : 'Uploaded, but indexing failed: ' + result.index_error)
      }
      load()
    } catch (e) {
      notify('upload', 'Upload failed: ' + e)
    }
  }

  // Returns whether indexing succeeded, so bulk callers (runBulkIndex) can
  // tally failures for their own summary notify() without duplicating this
  // function's own per-item notify/spinner/reload handling.
  const handleIndex = async (id) => {
    setIndexingIds(s => new Set(s).add(id))
    try {
      const result = await WailsApp.IndexProfileDocument(id)
      if (!result.indexed) {
        notify('index', isMonomindMissing(result.index_error)
          ? "Not indexed — monomind isn't installed."
          : 'Indexing failed: ' + result.index_error)
      }
      load()
      return result.indexed
    } catch (e) {
      notify('index', 'Indexing failed: ' + e)
      return false
    } finally {
      setIndexingIds(s => { const n = new Set(s); n.delete(id); return n })
    }
  }

  const toggleSelected = (id) => {
    setSelectedIds(s => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id); else n.add(id)
      return n
    })
  }

  const allVisibleSelected = docs.length > 0 && docs.every(d => selectedIds.has(d.id))
  const toggleSelectAll = () => {
    setSelectedIds(allVisibleSelected ? new Set() : new Set(docs.map(d => d.id)))
  }

  // Shared by the always-available "Index all" button and the
  // selection-scoped "Index selected" action -- both filter to the exact
  // predicate documentBadgeState/the per-row Index button already use
  // (!indexed || stale), so neither ever redundantly re-indexes an
  // already-fresh document. Reuses handleIndex per-item, so bulk runs get
  // the same live per-row spinner and per-item error toast it already has,
  // for free.
  const runBulkIndex = async (candidateDocs) => {
    const toIndex = candidateDocs.filter(d => !d.indexed || d.stale)
    if (toIndex.length === 0) {
      notify('index', 'Nothing to index — already up to date.')
      return
    }
    setBulkBusy('index')
    setBulkProgress({ done: 0, total: toIndex.length })
    let failed = 0
    for (const d of toIndex) {
      const ok = await handleIndex(d.id)
      if (!ok) failed++
      setBulkProgress(p => ({ ...p, done: p.done + 1 }))
    }
    notify('index', failed > 0
      ? `Indexed ${toIndex.length - failed} of ${toIndex.length} document${toIndex.length === 1 ? '' : 's'} (${failed} failed).`
      : `Indexed ${toIndex.length} document${toIndex.length === 1 ? '' : 's'}.`)
    setBulkBusy(null)
  }

  // "discovered" documents aren't owned by the app (see handleDelete's
  // single-item sibling and the per-row Delete button's own source check)
  // -- silently skipped here rather than erroring, with the skip count
  // surfaced in the confirm prompt so it's never a silent no-op.
  const runBulkDelete = async (candidateDocs) => {
    const toDelete = candidateDocs.filter(d => d.source !== 'discovered')
    const skipped = candidateDocs.length - toDelete.length
    if (toDelete.length === 0) {
      notify('delete', "Nothing to delete — the selected documents are all auto-discovered and can't be deleted.")
      return
    }
    const label = toDelete.length === 1 ? `"${toDelete[0].filename}"` : `${toDelete.length} documents`
    const suffix = skipped > 0 ? ` (${skipped} auto-discovered document${skipped === 1 ? '' : 's'} in your selection will be skipped)` : ''
    const ok = await confirm(
      `Delete ${label}? This removes ${toDelete.length === 1 ? 'it' : 'them'} from the knowledge index too.${suffix}`,
      { title: 'Delete Documents', confirmLabel: 'Delete', danger: true },
    )
    if (!ok) return

    setBulkBusy('delete')
    setBulkProgress({ done: 0, total: toDelete.length })
    let failed = 0
    const deletedIds = new Set()
    for (const d of toDelete) {
      try {
        await WailsApp.DeleteProfileDocument(d.id)
        deletedIds.add(d.id)
      } catch (e) {
        failed++
        notify('delete', `Failed to delete "${d.filename}": ${e}`)
      }
      setBulkProgress(p => ({ ...p, done: p.done + 1 }))
    }
    load()
    setSelectedIds(s => { const n = new Set(s); deletedIds.forEach(id => n.delete(id)); return n })
    if (failed === 0) {
      notify('delete', `Deleted ${toDelete.length} document${toDelete.length === 1 ? '' : 's'}.`)
    }
    setBulkBusy(null)
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

  // Double-click / View: preview in-app when a viewer exists for this file's
  // extension; otherwise tell the user and hand the file to the OS's own
  // default application, the same as double-clicking it in a file manager.
  const handleOpenDocument = (d) => {
    if (fileViewerKind(d.filename) && d.size_bytes <= maxInlinePreviewBytes) {
      setViewingDoc(d)
      return
    }
    const reason = d.size_bytes > maxInlinePreviewBytes
      ? `"${d.filename}" is too large to preview in-app`
      : (() => {
          const ext = d.filename.split('.').pop()?.toUpperCase() || 'this'
          return `No built-in viewer for ${ext} files`
        })()
    notify('open', `${reason} — opening "${d.filename}" with your system's default application.`)
    WailsApp.OpenPathWithOS(d.path).catch(e => notify('open', `Could not open "${d.filename}": ${e}`))
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
        {selectedIds.size > 0 && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{selectedIds.size} selected</span>
            <button
              style={btnStyle}
              disabled={bulkBusy !== null}
              onClick={() => runBulkIndex(docs.filter(d => selectedIds.has(d.id)))}
            >
              <PlayCircle size={13} /> {bulkBusy === 'index' ? `Indexing… (${bulkProgress.done}/${bulkProgress.total})` : `Index selected (${selectedIds.size})`}
            </button>
            <button
              style={{ ...btnStyle, color: '#ef4444', border: '1px solid rgba(239,68,68,0.3)' }}
              disabled={bulkBusy !== null}
              onClick={() => runBulkDelete(docs.filter(d => selectedIds.has(d.id)))}
            >
              <Trash2 size={13} /> {bulkBusy === 'delete' ? `Deleting… (${bulkProgress.done}/${bulkProgress.total})` : `Delete selected (${selectedIds.size})`}
            </button>
          </div>
        )}
        <button style={btnStyle} disabled={bulkBusy !== null} onClick={() => runBulkIndex(docs)}>
          <PlayCircle size={13} /> {bulkBusy === 'index' ? `Indexing… (${bulkProgress.done}/${bulkProgress.total})` : 'Index all'}
        </button>
        <button style={btnStyle} onClick={handleUpload}><Upload size={13} /> Upload</button>
      </div>

      <form onSubmit={handleSearch} style={{ display: 'flex', gap: 8 }}>
        <input style={{ ...inputStyle, flex: 1 }} placeholder="Search your uploaded documents..." value={query} onChange={e => setQuery(e.target.value)} />
        <button type="submit" style={btnStyle} disabled={searching}><Search size={13} /> Search</button>
      </form>

      {error && <div style={{ color: '#ff6b6b', fontSize: 12 }}>{error}</div>}

      {notInitialized && (
        <div style={{
          display: 'flex', alignItems: 'center', gap: 10, padding: '8px 12px',
          background: 'rgba(245,158,11,0.08)', border: '1px solid rgba(245,158,11,0.25)', borderRadius: 6,
        }}>
          <Sparkles size={14} color="#f59e0b" style={{ flexShrink: 0 }} />
          <div style={{ fontSize: 11, color: 'var(--text-secondary)', flex: 1 }}>
            {initStatus === 'running'
              ? 'Setting up monomind for this profile — this can take a minute or two…'
              : 'Monomind isn\'t set up for this profile yet — uploaded documents won\'t be searchable until it is.'}
          </div>
          {initStatus === 'running' ? (
            <div className="spinner" style={{ width: 14, height: 14 }} />
          ) : (
            <button style={{ ...btnStyle, padding: '4px 10px', flexShrink: 0 }} onClick={startMonomindInit}>Initiate monomind</button>
          )}
        </div>
      )}

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
              <th style={{ padding: '6px 8px' }}>
                <input
                  type="checkbox"
                  aria-label="Select all documents"
                  style={{ accentColor: '#00b4d8', width: 14, height: 14 }}
                  checked={allVisibleSelected}
                  onChange={toggleSelectAll}
                />
              </th>
              <th style={{ padding: '6px 8px' }}>Filename</th>
              <th style={{ padding: '6px 8px' }}>Source</th>
              <th style={{ padding: '6px 8px' }}>Size</th>
              <th style={{ padding: '6px 8px' }}>Uploaded</th>
              <th style={{ padding: '6px 8px' }}>Knowledge Graph</th>
              <th style={{ padding: '6px 8px' }} />
            </tr>
          </thead>
          <tbody>
            {docs.map(d => (
              <tr
                key={d.id}
                onDoubleClick={() => handleOpenDocument(d)}
                style={{ borderTop: '1px solid rgba(255,255,255,0.05)', cursor: 'pointer' }}
                title="Double-click to view"
              >
                <td style={{ padding: '8px' }}>
                  <input
                    type="checkbox"
                    aria-label={`Select ${d.filename}`}
                    style={{ accentColor: '#00b4d8', width: 14, height: 14 }}
                    checked={selectedIds.has(d.id)}
                    onClick={(e) => e.stopPropagation()}
                    onChange={() => toggleSelected(d.id)}
                  />
                </td>
                <td style={{ padding: '8px' }}>{d.filename}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.source}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{formatBytes(d.size_bytes)}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.created_at}</td>
                <td style={{ padding: '8px' }}>
                  {(() => {
                    const state = documentBadgeState(d, notInitialized)
                    if (state === 'indexed') {
                      return (
                        <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#10b981', fontSize: 10 }}>
                          <CheckCircle2 size={12} /> Indexed
                        </span>
                      )
                    }
                    if (state === 'stale') {
                      return (
                        <span
                          title={d.index_error || 'Content changed on disk since last index'}
                          style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#f59e0b', fontSize: 10, cursor: 'help' }}
                        >
                          <AlertTriangle size={12} /> Stale
                        </span>
                      )
                    }
                    if (state === 'monomind_not_set_up') {
                      return (
                        <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#f59e0b', fontSize: 10 }}>
                          <Sparkles size={12} /> Monomind not set up
                        </span>
                      )
                    }
                    return (
                      <span
                        title={d.index_error || 'Not yet indexed'}
                        style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#64748b', fontSize: 10, cursor: d.index_error ? 'help' : 'default' }}
                      >
                        <XCircle size={12} /> {isMonomindMissing(d.index_error) ? 'Monomind not installed' : 'Not indexed'}
                      </span>
                    )
                  })()}
                </td>
                <td style={{ padding: '8px', display: 'flex', gap: 6 }}>
                  <button style={{ ...btnStyle, padding: '4px 8px' }} title="View" onClick={(e) => { e.stopPropagation(); handleOpenDocument(d) }}>
                    <Eye size={12} />
                  </button>
                  {(d.stale || !d.indexed) && (
                    <button
                      style={{ ...btnStyle, padding: '4px 8px' }}
                      title={d.stale ? 'Re-index' : 'Index'}
                      disabled={indexingIds.has(d.id) || bulkBusy !== null}
                      onClick={(e) => { e.stopPropagation(); handleIndex(d.id) }}
                    >
                      {indexingIds.has(d.id) ? <div className="spinner" style={{ width: 12, height: 12 }} /> : <PlayCircle size={12} />}
                    </button>
                  )}
                  {d.source !== 'discovered' && (
                    <button style={{ ...btnStyle, color: '#ef4444', border: '1px solid rgba(239,68,68,0.3)', padding: '4px 8px' }} onClick={(e) => { e.stopPropagation(); handleDelete(d.id, d.filename) }}>
                      <Trash2 size={12} />
                    </button>
                  )}
                </td>
              </tr>
            ))}
            {docs.length === 0 && (
              <tr><td colSpan={7} style={{ padding: 16, textAlign: 'center', color: 'var(--text-muted)' }}>No documents uploaded.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {viewingDoc && <FileViewerModal doc={viewingDoc} onClose={() => setViewingDoc(null)} />}
    </div>
  )
}
