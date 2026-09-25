// Shared presentational bits for the Connections page (browser automations
// and API connections). Inline-style language matches the rest of the app:
// mono labels, CSS vars, small bordered chips.
import { Loader } from 'lucide-react'

export const mono = { fontFamily: 'var(--font-mono)' }
export const label = { fontFamily: 'var(--font-mono)', fontSize: 9.5, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1.2 }
export const muted = { fontFamily: 'var(--font-mono)', fontSize: 10.5, color: 'var(--text-muted)' }
export const body = { fontFamily: 'var(--font-body)', fontSize: 12, color: 'var(--text-secondary)', lineHeight: 1.5 }
export const panel = { background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', padding: '10px 14px' }
export const spin = { animation: 'spin .7s linear infinite' }

export const SOURCE_LABELS = { builtin: 'built-in', imported: 'imported', local: 'local' }

// Colours for an action's declared side effects (spec §4.3).
export const EFFECT_COLORS = {
  none: 'var(--text-muted)',
  read: 'var(--cyan)',
  write: 'var(--yellow)',
  message: 'var(--orange)',
  destructive: 'var(--red)',
}

export function Chip({ children, color = 'var(--text-muted)', title, style }) {
  return (
    <span
      title={title}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 3,
        fontFamily: 'var(--font-mono)', fontSize: 9, color, lineHeight: '15px',
        border: `1px solid ${color}`, borderRadius: 8, padding: '0 6px', whiteSpace: 'nowrap',
        ...style,
      }}
    >
      {children}
    </span>
  )
}

export function EffectChip({ effect }) {
  const e = effect || 'none'
  return <Chip color={EFFECT_COLORS[e] || 'var(--text-muted)'} title={`Side effects: ${e}`}>{e}</Chip>
}

export function Dot({ on, warn }) {
  const color = on ? 'var(--green-neon)' : warn ? 'var(--yellow)' : 'var(--text-muted)'
  return <span aria-hidden="true" style={{ width: 7, height: 7, borderRadius: '50%', flexShrink: 0, background: color, boxShadow: on ? '0 0 5px var(--green-neon)' : 'none' }} />
}

export function ErrorBox({ children }) {
  if (!children) return null
  return (
    <div role="alert" style={{ ...mono, fontSize: 11, padding: '7px 12px', borderRadius: 'var(--radius)', background: 'rgba(239,68,68,.08)', border: '1px solid rgba(239,68,68,.25)', color: 'var(--red)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
      {children}
    </div>
  )
}

export function OkBox({ children }) {
  if (!children) return null
  return (
    <div role="status" style={{ ...mono, fontSize: 11, padding: '7px 12px', borderRadius: 'var(--radius)', background: 'rgba(74,222,128,.08)', border: '1px solid rgba(74,222,128,.25)', color: 'var(--green-neon)', wordBreak: 'break-word' }}>
      {children}
    </div>
  )
}

export function Busy({ text = 'Working…' }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, ...muted }}>
      <Loader size={11} style={spin} /> {text}
    </div>
  )
}

export function SectionHeader({ title, hint, count }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
      <span style={{ ...mono, fontSize: 11, fontWeight: 700, color: 'var(--text)', textTransform: 'uppercase', letterSpacing: 2 }}>{title}</span>
      {hint && <span style={{ ...muted, fontSize: 10 }}>{hint}</span>}
      <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
      {count && <span style={{ ...mono, fontSize: 9.5, color: 'var(--text-muted)' }}>{count}</span>}
    </div>
  )
}

export function KV({ rows }) {
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 7 }}>
      {rows.filter(Boolean).map(([k, v]) => (
        <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 12 }}>
          <span style={label}>{k}</span>
          <span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)', textAlign: 'right', wordBreak: 'break-all' }}>{v ?? '—'}</span>
        </div>
      ))}
    </div>
  )
}

export function fmtDate(s) {
  if (!s || s.startsWith('0001-')) return '—'
  try { return new Date(s).toLocaleString('en-US', { month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit' }) }
  catch { return s.slice(0, 16) }
}

export function fmtShort(s) {
  if (!s || s.startsWith('0001-')) return '—'
  try { return new Date(s).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) }
  catch { return s.slice(0, 10) }
}

// copyText copies via the Wails runtime when present, else the browser API.
export async function copyText(text) {
  try {
    if (window.runtime?.ClipboardSetText) return await window.runtime.ClipboardSetText(text)
    await navigator.clipboard?.writeText(text)
    return true
  } catch { return false }
}

// fileSize renders bytes for the install review file list.
export function fileSize(n) {
  if (n == null) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
