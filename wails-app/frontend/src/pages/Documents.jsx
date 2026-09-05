import { useState, useEffect, useCallback, useRef } from 'react'
import { Upload, Search, Trash2, Sparkles, CheckCircle2, XCircle } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'
import { confirm } from '../components/ConfirmDialog.jsx'
import { api, notify, onMonomindInitEvent } from '../services/api.js'

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

  const load = useCallback(async () => {
    try {
      setDocs((await WailsApp.ListProfileDocuments()) || [])
    } catch (e) {
      setError('Failed to load documents: ' + e)
    }
  }, [])

  useEffect(() => { load() }, [load])
  useEffect(() => { api.isMonomindInitialized().then(v => setNotInitialized(!v)) }, [])

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
              <tr key={d.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                <td style={{ padding: '8px' }}>{d.filename}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.source}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{formatBytes(d.size_bytes)}</td>
                <td style={{ padding: '8px', color: 'var(--text-muted)' }}>{d.created_at}</td>
                <td style={{ padding: '8px' }}>
                  {d.indexed ? (
                    <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#10b981', fontSize: 10 }}>
                      <CheckCircle2 size={12} /> Indexed
                    </span>
                  ) : (
                    <span
                      title={d.index_error || 'Not yet indexed'}
                      style={{ display: 'flex', alignItems: 'center', gap: 4, color: '#64748b', fontSize: 10, cursor: d.index_error ? 'help' : 'default' }}
                    >
                      <XCircle size={12} /> {isMonomindMissing(d.index_error) ? 'Monomind not installed' : 'Not indexed'}
                    </span>
                  )}
                </td>
                <td style={{ padding: '8px' }}>
                  <button style={{ ...btnStyle, color: '#ef4444', border: '1px solid rgba(239,68,68,0.3)', padding: '4px 8px' }} onClick={() => handleDelete(d.id, d.filename)}>
                    <Trash2 size={12} />
                  </button>
                </td>
              </tr>
            ))}
            {docs.length === 0 && (
              <tr><td colSpan={6} style={{ padding: 16, textAlign: 'center', color: 'var(--text-muted)' }}>No documents uploaded.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
