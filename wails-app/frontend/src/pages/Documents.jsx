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
