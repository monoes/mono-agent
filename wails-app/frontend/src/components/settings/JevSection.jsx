import { useState, useEffect, useCallback } from 'react'
import { ChevronRight, ChevronDown, KeyRound, ShieldAlert } from 'lucide-react'
import { JevStatus, JevSetKey, JevTestKey, JevRemoveKey, JevSetSurface, JevUsage } from '../../wailsjs/go/main/App'

// TypeSafe Jev settings. Everything goes through `monoagentcli jev …` (Go
// side: app_jev.go); the key is sent once on Save and never read back.
// Enabling a surface first shows what it sends to TypeSafe and needs an
// explicit confirm — the GUI counterpart of `jev enable`'s prompt.

const mono = 'var(--font-mono)'
const card = {
  background: 'var(--surface)',
  border: '1px solid var(--border)',
  borderRadius: 'var(--radius-lg)',
  padding: '16px 20px',
  display: 'flex', flexDirection: 'column', gap: 14,
  marginBottom: 16,
}
const label = { fontFamily: mono, fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }
const hint = { fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--text-muted)', lineHeight: 1.5 }
const errText = { fontFamily: mono, fontSize: 10.5, color: 'var(--red)', lineHeight: 1.5, wordBreak: 'break-word' }
const okText = { fontFamily: mono, fontSize: 10.5, color: 'var(--green-neon)', lineHeight: 1.5, wordBreak: 'break-word' }
const input = {
  background: 'var(--elevated)', border: '1px solid var(--border)', color: 'var(--text)',
  borderRadius: 'var(--radius)', fontFamily: mono, fontSize: 12, padding: '6px 10px', minWidth: 0,
}

const errMsg = (e) => String(e?.message || e || 'unknown error')

// testError shortens "jev: HTTP 401: {json body}" to "HTTP 401 — <message>".
export function testError(raw) {
  const m = /HTTP (\d+):\s*(\{.*\})\s*$/s.exec(raw || '')
  if (!m) return raw || 'unknown error'
  try {
    const body = JSON.parse(m[2])
    const d = body.detail ?? body.error ?? body
    const msg = typeof d === 'string' ? d : (d?.message || body.message)
    if (msg) return `HTTP ${m[1]} — ${msg}`
  } catch { /* keep the raw text */ }
  return raw
}

function keyChip(st) {
  switch (st?.key_source) {
    case 'vault': return { text: `Vault entry "${st.key_entry || 'typesafe'}"`, color: 'var(--green-neon)', bg: 'rgba(16,185,129,.1)', bd: 'rgba(74,222,128,.25)' }
    case 'env': return { text: 'Environment variable (TYPESAFE_API_KEY)', color: 'var(--cyan)', bg: 'rgba(0,180,216,.1)', bd: 'rgba(0,180,216,.25)' }
    case 'config': return { text: 'Set in configuration', color: 'var(--cyan)', bg: 'rgba(0,180,216,.1)', bd: 'rgba(0,180,216,.25)' }
    default: return { text: 'Not set', color: 'var(--yellow)', bg: 'rgba(234,179,8,.08)', bd: 'rgba(234,179,8,.22)' }
  }
}

function formatUSD(v) {
  if (!v) return '$0'
  if (v < 0.0001) return '<$0.0001'
  return `$${v.toFixed(v < 1 ? 4 : 2)}`
}

function Switch({ on, disabled, label: aria, onChange }) {
  return (
    <button
      type="button" role="switch" aria-checked={on} aria-label={aria} disabled={disabled}
      onClick={() => onChange(!on)}
      style={{
        width: 32, height: 18, borderRadius: 9, flexShrink: 0, position: 'relative', padding: 0,
        border: `1px solid ${on ? 'var(--cyan)' : 'var(--border-bright)'}`,
        background: on ? 'rgba(0,180,216,.35)' : 'var(--elevated)',
        cursor: disabled ? 'not-allowed' : 'pointer', opacity: disabled ? 0.5 : 1,
        transition: 'all var(--transition)',
      }}
    >
      <span style={{
        position: 'absolute', top: 2, left: on ? 16 : 2, width: 12, height: 12, borderRadius: '50%',
        background: on ? 'var(--cyan-bright)' : 'var(--text-dim)', transition: 'left var(--transition)',
      }} />
    </button>
  )
}

function EgressList({ items }) {
  if (!items?.length) return <div style={hint}>No details reported by the CLI.</div>
  return (
    <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 2 }}>
      {items.map((e, i) => (
        <li key={i} style={{ fontFamily: 'var(--font-body)', fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.5 }}>{e}</li>
      ))}
    </ul>
  )
}

function ThresholdInput({ value, disabled, onChange, id }) {
  return (
    <input
      id={id} type="number" min={0.05} max={1} step={0.05} value={value} disabled={disabled}
      onChange={e => onChange(e.target.value)}
      style={{ ...input, width: 76, padding: '4px 8px' }}
    />
  )
}

// 0.05–1, or the surface's own default (people_review defaults to 0: suggest only).
const validThreshold = (v, def) => {
  const n = Number(v)
  return v !== '' && Number.isFinite(n) && ((n >= 0.05 && n <= 1) || n === def)
}

function SurfaceRow({ s, busy, pending, onStartEnable, onCancelEnable, onConfirmEnable, onDisable, onThreshold }) {
  const [open, setOpen] = useState(false)
  const [thr, setThr] = useState(String(s.threshold ?? s.default_threshold ?? ''))
  useEffect(() => { setThr(String(s.threshold ?? s.default_threshold ?? '')) }, [s.threshold, s.default_threshold, s.enabled])
  const title = s.title || s.surface
  const changed = s.enabled && validThreshold(thr, s.default_threshold) && Number(thr) !== s.threshold
  const thrId = `jev-thr-${s.surface}`

  return (
    <div data-jev-surface={s.surface} style={{
      borderTop: '1px solid var(--border-dim)', paddingTop: 10, display: 'flex', flexDirection: 'column', gap: 6,
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap' }}>
            <span style={{ fontFamily: mono, fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>{title}</span>
            <span style={{ fontFamily: mono, fontSize: 9.5, color: 'var(--text-dim)' }}>{s.surface}</span>
          </div>
          {s.description && <div style={{ ...hint, marginTop: 2 }}>{s.description}</div>}
        </div>
        {s.enabled && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
            <label htmlFor={thrId} style={{ ...label, letterSpacing: 0.5 }}>Threshold</label>
            <ThresholdInput id={thrId} value={thr} disabled={busy} onChange={setThr} />
            {changed && (
              <button className="btn btn-secondary btn-sm" disabled={busy} onClick={() => onThreshold(s.surface, Number(thr))}>Apply</button>
            )}
          </div>
        )}
        <Switch
          on={s.enabled || pending} disabled={busy} label={`${title}: ${s.enabled ? 'on' : 'off'}`}
          onChange={on => (on ? onStartEnable(s.surface) : (pending ? onCancelEnable() : onDisable(s.surface)))}
        />
      </div>

      {pending ? (
        <div data-testid={`jev-confirm-${s.surface}`} style={{
          background: 'rgba(234,179,8,.05)', border: '1px solid rgba(234,179,8,.22)', borderRadius: 'var(--radius)',
          padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 8,
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontFamily: mono, fontSize: 11, color: 'var(--yellow)' }}>
            <ShieldAlert size={13} /> Turning this on sends the following to TypeSafe:
          </div>
          <EgressList items={s.egress} />
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <label htmlFor={thrId} style={{ ...label, letterSpacing: 0.5 }}>Threshold</label>
            <ThresholdInput id={thrId} value={thr} disabled={busy} onChange={setThr} />
            <span style={{ ...hint, flex: 1, minWidth: 140 }}>Jev only acts when its top answer is at least this likely.</span>
            <button className="btn btn-secondary btn-sm" disabled={busy} onClick={onCancelEnable}>Cancel</button>
            <button className="btn btn-primary btn-sm" disabled={busy || !validThreshold(thr, s.default_threshold)}
              onClick={() => onConfirmEnable(s.surface, Number(thr))}>
              Send this and enable
            </button>
          </div>
        </div>
      ) : (
        <div>
          <button
            type="button" onClick={() => setOpen(o => !o)} aria-expanded={open}
            style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', display: 'inline-flex', alignItems: 'center', gap: 4, fontFamily: mono, fontSize: 10, color: 'var(--cyan-dim)' }}
          >
            {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />} What is sent to TypeSafe
          </button>
          {open && <div style={{ marginTop: 6 }}><EgressList items={s.egress} /></div>}
        </div>
      )}
    </div>
  )
}

function UsageTable({ usage, titles }) {
  const rows = usage?.surfaces || []
  if (!rows.length) return <div style={hint}>No Jev calls in the last 7 days.</div>
  const th = { ...label, fontSize: 9, textAlign: 'right', padding: '0 0 6px 12px', fontWeight: 400 }
  const td = { fontFamily: mono, fontSize: 11, color: 'var(--text-secondary)', textAlign: 'right', padding: '4px 0 4px 12px', whiteSpace: 'nowrap' }
  const row = (u, total) => (
    <tr key={u.surface} style={total ? { borderTop: '1px solid var(--border)' } : undefined}>
      <td style={{ ...td, textAlign: 'left', paddingLeft: 0, color: total ? 'var(--text)' : 'var(--text-secondary)', whiteSpace: 'normal' }}>
        {total ? 'Total' : (titles[u.surface] || u.surface)}
      </td>
      <td style={td}>{u.calls}{u.failures ? <span style={{ color: 'var(--red)' }}> ({u.failures} failed)</span> : null}</td>
      <td style={td}>{(u.input_tokens || 0).toLocaleString()}</td>
      <td style={td}>{formatUSD(u.estimated_usd)}</td>
    </tr>
  )
  return (
    <div style={{ overflowX: 'auto' }}>
      <table style={{ width: '100%', maxWidth: 640, borderCollapse: 'collapse' }}>
        <thead>
          <tr>
            <th style={{ ...th, textAlign: 'left', paddingLeft: 0 }}>Feature</th>
            <th style={th}>Calls</th>
            <th style={th}>Input tokens</th>
            <th style={th}>Est. cost</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(u => row(u, false))}
          {rows.length > 1 && usage.total && row(usage.total, true)}
        </tbody>
      </table>
    </div>
  )
}

export default function JevSection() {
  const [status, setStatus] = useState(null)
  const [loadErr, setLoadErr] = useState('')
  const [usage, setUsage] = useState(null)
  const [usageErr, setUsageErr] = useState('')
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [keyInput, setKeyInput] = useState('')
  const [keyNote, setKeyNote] = useState('')
  const [test, setTest] = useState(null)
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [pending, setPending] = useState('')

  const load = useCallback(async () => {
    const [st, us] = await Promise.allSettled([JevStatus(), JevUsage('7d')])
    if (st.status === 'fulfilled') { setStatus(st.value); setLoadErr('') } else setLoadErr(errMsg(st.reason))
    if (us.status === 'fulfilled') { setUsage(us.value); setUsageErr('') } else setUsageErr(errMsg(us.reason))
  }, [])

  useEffect(() => { load() }, [load])

  // run wraps one CLI call: one in flight at a time, errors shown inline.
  const run = async (what, fn) => {
    setBusy(what); setErr('')
    try { return await fn() } catch (e) { setErr(errMsg(e)); return undefined } finally { setBusy('') }
  }

  const saveKey = () => run('save', async () => {
    const r = await JevSetKey(keyInput)
    setKeyInput(''); setTest(null); setConfirmRemove(false)
    setKeyNote(`${r?.replaced ? 'Replaced' : 'Saved to'} vault entry "${r?.key_entry || 'typesafe'}".`)
    await load()
  })
  const testKey = () => run('test', async () => { setTest(null); setTest(await JevTestKey()) })
  const removeKey = () => run('remove', async () => {
    const r = await JevRemoveKey()
    setConfirmRemove(false); setTest(null)
    setKeyNote(`Removed vault entry "${r?.removed || 'typesafe'}".`)
    await load()
  })
  const setSurface = (surface, enabled, threshold) => run(`surface:${surface}`, async () => {
    await JevSetSurface(surface, enabled, threshold)
    setPending('')
    await load()
  })

  const header = (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
      <span style={{ fontFamily: mono, fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 2 }}>
        TypeSafe Jev
      </span>
      <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
    </div>
  )

  if (!status) {
    return (
      <div data-testid="jev-section">
        {header}
        <div style={card}>
          {loadErr ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div style={{ ...errText, flex: 1 }}>Couldn't read Jev settings: {loadErr}</div>
              <button className="btn btn-secondary btn-sm" onClick={load}>Retry</button>
            </div>
          ) : (
            <div style={hint}>Loading Jev settings…</div>
          )}
        </div>
      </div>
    )
  }

  const chip = keyChip(status)
  const surfaces = status.surfaces || []
  const titles = Object.fromEntries(surfaces.map(s => [s.surface, s.title || s.surface]))
  const hasKey = status.key_source && status.key_source !== 'none'
  const disabled = !!busy

  return (
    <div data-testid="jev-section">
      {header}
      <div style={card}>
        <div style={hint}>
          Fast pick-one and yes/no decisions for a few monoagent features. Jev never writes text,
          and nothing is sent to TypeSafe until you turn a feature on below. Get an API key at console.typesafe.ai.
        </div>

        {/* API key */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <span style={label}>API key</span>
            <span data-testid="jev-key-chip" style={{
              display: 'inline-flex', alignItems: 'center', gap: 5, fontFamily: mono, fontSize: 10.5,
              color: chip.color, background: chip.bg, border: `1px solid ${chip.bd}`, borderRadius: 99, padding: '2px 9px',
            }}>
              <KeyRound size={11} /> {chip.text}
            </span>
            {status.model && <span style={{ fontFamily: mono, fontSize: 10, color: 'var(--text-dim)' }}>model {status.model}</span>}
          </div>
          <form
            onSubmit={e => { e.preventDefault(); if (keyInput.trim()) saveKey() }}
            style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}
          >
            <input
              type="password" autoComplete="off" spellCheck={false} aria-label="TypeSafe API key"
              placeholder={hasKey ? 'Paste a new key to replace it' : 'Paste your TypeSafe API key'}
              value={keyInput} onChange={e => setKeyInput(e.target.value)} disabled={disabled}
              style={{ ...input, flex: '1 1 220px' }}
            />
            <button type="submit" className="btn btn-primary btn-sm" disabled={disabled || !keyInput.trim()}>
              {busy === 'save' ? 'Saving…' : 'Save'}
            </button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={disabled || !hasKey} onClick={testKey}>
              {busy === 'test' ? 'Testing…' : 'Test'}
            </button>
            {status.key_source === 'vault' && !confirmRemove && (
              <button type="button" className="btn btn-danger btn-sm" disabled={disabled} onClick={() => setConfirmRemove(true)}>Remove</button>
            )}
          </form>
          {status.key_source === 'env' && (
            <div style={hint}>The key comes from TYPESAFE_API_KEY. A key saved here goes to the vault and is used instead.</div>
          )}
          {confirmRemove && (
            <div data-testid="jev-remove-confirm" style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
              <span style={{ fontFamily: mono, fontSize: 11, color: 'var(--red)', flex: 1, minWidth: 180 }}>
                Delete vault entry "{status.key_entry || 'typesafe'}"? Jev features stop working until a new key is added.
              </span>
              <button className="btn btn-secondary btn-sm" disabled={disabled} onClick={() => setConfirmRemove(false)}>Cancel</button>
              <button className="btn btn-danger btn-sm" disabled={disabled} onClick={removeKey}>
                {busy === 'remove' ? 'Deleting…' : 'Delete key'}
              </button>
            </div>
          )}
          {keyNote && <div style={okText}>{keyNote}</div>}
          {test && (test.ok ? (
            <div data-testid="jev-test-result" style={okText}>
              Key works{test.models?.length ? ` — models: ${test.models.join(', ')}` : ''}.
            </div>
          ) : (
            <div data-testid="jev-test-result" style={errText}>Key test failed: {testError(test.error)}</div>
          ))}
        </div>

        {err && <div role="alert" style={errText}>{err}</div>}

        {/* Features */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <span style={label}>Features</span>
          {!hasKey && surfaces.some(s => s.enabled) && (
            <div style={{ ...hint, color: 'var(--yellow)' }}>Enabled features stay inactive until a key is set.</div>
          )}
          {surfaces.length === 0 && <div style={hint}>This version of monoagentcli reports no Jev features.</div>}
          {surfaces.map(s => (
            <SurfaceRow
              key={s.surface} s={s} busy={disabled} pending={pending === s.surface}
              onStartEnable={name => { setErr(''); setPending(name) }}
              onCancelEnable={() => setPending('')}
              onConfirmEnable={(name, thr) => setSurface(name, true, thr)}
              onDisable={name => setSurface(name, false, 0)}
              onThreshold={(name, thr) => setSurface(name, true, thr)}
            />
          ))}
        </div>

        {/* Usage */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, borderTop: '1px solid var(--border-dim)', paddingTop: 12 }}>
          <span style={label}>Usage · last 7 days</span>
          {usageErr ? <div style={errText}>Couldn't read usage: {usageErr}</div> : <UsageTable usage={usage} titles={titles} />}
        </div>
      </div>
    </div>
  )
}
