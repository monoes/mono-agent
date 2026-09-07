import { useEffect, useRef, useState } from 'react'
import { X, ExternalLink, AlertTriangle } from 'lucide-react'
import * as WailsApp from '../wailsjs/go/main/App'

const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'ico'])
const PDF_EXTS = new Set(['pdf'])
const HTML_EXTS = new Set(['html', 'htm'])
const TEXT_EXTS = new Set([
  'txt', 'md', 'markdown', 'csv', 'json', 'log', 'yaml', 'yml',
  'js', 'jsx', 'ts', 'tsx', 'go', 'py', 'xml', 'ini', 'conf', 'sh', 'toml',
])

// Pure so it's directly unit-testable without rendering the component —
// mirrors Documents.jsx's own formatBytes/isMonomindMissing pattern.
export function fileViewerKind(filename) {
  const ext = (filename || '').split('.').pop()?.toLowerCase() || ''
  if (IMAGE_EXTS.has(ext)) return 'image'
  if (PDF_EXTS.has(ext)) return 'pdf'
  if (HTML_EXTS.has(ext)) return 'html'
  if (TEXT_EXTS.has(ext)) return 'text'
  return null
}

const fmtBytes = (b) => {
  if (!b || b < 1024) return (b || 0) + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  return (b / 1024 / 1024).toFixed(1) + ' MB'
}

// In-app preview for a vault document (image, PDF, HTML, or text/source) —
// used for double-click / "View" on Documents.jsx. Files with no supported
// kind never get here: the caller tells the user and hands off to
// WailsApp.OpenPathWithOS instead of opening this modal.
export default function FileViewerModal({ doc, onClose }) {
  const overlayRef = useRef(null)
  const [content, setContent] = useState(null) // data URL (image/pdf) or raw text (html/text)
  const [error, setError] = useState(null)
  const kind = fileViewerKind(doc?.filename)

  useEffect(() => {
    const handler = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  useEffect(() => {
    if (!doc || !kind) return
    setContent(null)
    setError(null)
    const load = kind === 'image' || kind === 'pdf'
      ? WailsApp.GetProfileDocumentData(doc.id)
      : WailsApp.GetProfileDocumentText(doc.id)
    load.then(setContent).catch(e => setError(String(e)))
  }, [doc?.id, kind])

  const handleOpenWithOS = () => {
    WailsApp.OpenPathWithOS(doc.path).catch(e => setError(String(e)))
  }

  if (!doc) return null

  return (
    <div
      ref={overlayRef}
      onClick={(e) => { if (e.target === overlayRef.current) onClose() }}
      style={{
        position: 'fixed', inset: 0, zIndex: 1000,
        background: 'rgba(0,0,0,0.75)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}
    >
      <div role="dialog" aria-modal="true" aria-label="File preview" style={{
        background: '#0d1a26', border: '1px solid #1e3a4f', borderRadius: 12,
        padding: 20, width: 760, maxWidth: '92vw', maxHeight: '88vh',
        display: 'flex', flexDirection: 'column', gap: 12,
        boxShadow: '0 20px 60px rgba(0,0,0,0.6)',
      }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div style={{ minWidth: 0 }}>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 13, color: '#00b4d8', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {doc.filename}
            </div>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: '#475569' }}>{fmtBytes(doc.size_bytes)}</div>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
            <button
              onClick={handleOpenWithOS}
              title="Open with system default application"
              style={{
                background: 'rgba(0,180,216,0.1)', border: '1px solid rgba(0,180,216,0.3)',
                borderRadius: 6, padding: '5px 10px', color: '#00b4d8',
                fontFamily: 'var(--font-mono)', fontSize: 11, cursor: 'pointer',
                display: 'flex', alignItems: 'center', gap: 5,
              }}
            >
              <ExternalLink size={12} /> Open Externally
            </button>
            <button onClick={onClose} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#475569', padding: 2 }}>
              <X size={16} />
            </button>
          </div>
        </div>

        <div style={{
          flex: 1, background: '#060b11', borderRadius: 8, overflow: 'auto',
          border: '1px solid #1e3a4f', minHeight: 200,
          display: 'flex', alignItems: error ? 'center' : 'stretch', justifyContent: error ? 'center' : 'stretch',
        }}>
          {error ? (
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8, color: '#fca5a5', padding: 24, textAlign: 'center' }}>
              <AlertTriangle size={24} />
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11 }}>{error}</span>
            </div>
          ) : content === null ? (
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', width: '100%', color: '#334155', fontFamily: 'var(--font-mono)', fontSize: 11 }}>
              Loading…
            </div>
          ) : kind === 'image' ? (
            <img src={content} alt={doc.filename} style={{ maxWidth: '100%', maxHeight: '70vh', objectFit: 'contain', margin: 'auto', display: 'block' }} />
          ) : kind === 'pdf' ? (
            <embed src={content} type="application/pdf" style={{ width: '100%', height: '70vh', border: 'none' }} />
          ) : kind === 'html' ? (
            <iframe
              srcDoc={content}
              sandbox=""
              title={doc.filename}
              style={{ width: '100%', height: '70vh', border: 'none', background: '#fff' }}
            />
          ) : (
            <pre style={{
              margin: 0, padding: 14, width: '100%', maxHeight: '70vh',
              overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              fontFamily: 'var(--font-mono)', fontSize: 11, color: '#e2e8f0',
            }}>{content}</pre>
          )}
        </div>
      </div>
    </div>
  )
}
