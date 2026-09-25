// Session tab: the automation's browser login. Reuses the existing
// `login <automation>` / `login confirm <automation>` flow (LoginSocial /
// ConfirmSocialLogin + conn:* events) and the session test/delete bindings.
import { useCallback, useEffect, useState } from 'react'
import { LogIn, LogOut, CheckCircle, RefreshCw } from 'lucide-react'
import { api, onConnectionProgress, onConnectionDone, onConnectionOpened } from '../../services/api.js'
import { confirm } from '../../components/ConfirmDialog.jsx'
import { KV, ErrorBox, OkBox, Busy, Dot, body, mono, panel, fmtDate } from './ui.jsx'

export default function SessionTab({ automation, manifest, onChanged }) {
  const id = automation.id
  const s = automation.session || {}
  const [rows, setRows] = useState([])
  const [steps, setSteps] = useState([])
  const [phase, setPhase] = useState('idle') // idle | opening | awaiting | saving
  const [msg, setMsg] = useState(null) // {ok, text}

  const loadRows = useCallback(async () => {
    const all = await api.getSessions()
    setRows((all || []).filter(r => (r.platform || '').toLowerCase() === id.toLowerCase()))
  }, [id])

  useEffect(() => { loadRows() }, [loadRows])

  useEffect(() => {
    const offP = onConnectionProgress(d => { if (d?.platform === id) setSteps(p => [...p, d]) })
    const offO = onConnectionOpened(d => { if (d?.platform === id) setPhase('awaiting') })
    const offD = onConnectionDone(async d => {
      if (d?.platform !== id) return
      if (d.success) {
        setPhase('idle'); setMsg({ ok: true, text: `Logged in as ${d.accountID}` })
        await Promise.all([loadRows(), onChanged?.()])
      } else {
        // A failed confirm (not logged in yet) keeps the confirm button.
        setPhase(p => (p === 'saving' ? 'awaiting' : 'idle'))
        setMsg({ ok: false, text: d.error || 'Login failed' })
      }
    })
    return () => { offP(); offO(); offD() }
  }, [id, loadRows, onChanged])

  const start = async () => {
    setSteps([]); setMsg(null); setPhase('opening')
    const r = await api.loginSocial(id)
    if (r && String(r).startsWith('error')) { setPhase('idle'); setMsg({ ok: false, text: String(r).replace(/^error:?\s*/, '') }) }
  }
  const confirmLogin = async () => {
    setMsg(null); setPhase('saving')
    const r = await api.confirmSocialLogin(id)
    if (r && String(r).startsWith('error')) { setPhase('awaiting'); setMsg({ ok: false, text: String(r).replace(/^error:?\s*/, '') }) }
  }
  const test = async (row) => {
    const r = await api.testSession(row.id)
    setMsg(r === 'ok' ? { ok: true, text: `Session for ${row.username} is valid` } : { ok: false, text: String(r || 'error').replace(/^error:?\s*/, '') })
  }
  const logout = async (row) => {
    if (!(await confirm(`Log out ${row.username} from ${automation.name || id}? The stored cookies are deleted.`))) return
    try { await api.deleteSession(row.id) } catch (e) { setMsg({ ok: false, text: e?.message || String(e) }); return }
    await Promise.all([loadRows(), onChanged?.()])
  }

  const loginUrl = manifest?.login?.url || manifest?.site?.startUrl
  return (
    <>
      <div style={{ ...panel, display: 'flex', alignItems: 'center', gap: 8 }}>
        <Dot on={s.loggedIn} warn={!s.loggedIn && !!s.username} />
        <span style={{ ...mono, fontSize: 12, color: s.loggedIn ? 'var(--green-neon)' : 'var(--text-secondary)' }}>
          {s.loggedIn ? `Logged in${s.username ? ` as ${s.username}` : ''}` : s.username ? `Session for ${s.username} expired` : 'Not logged in'}
        </span>
        {s.expiresAt && <span style={{ ...mono, fontSize: 10, color: 'var(--text-muted)', marginLeft: 'auto' }}>expires {fmtDate(s.expiresAt)}</span>}
      </div>

      {rows.map(r => (
        <div key={r.id} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <KV rows={[['Account', r.username || '—'], ['Status', r.active ? 'active' : 'expired'], ['Added', fmtDate(r.added_at)], ['Expires', fmtDate(r.expiry)]]} />
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="btn btn-secondary btn-sm" onClick={() => test(r)} style={{ gap: 5 }}><CheckCircle size={11} /> Test</button>
            <button className="btn btn-danger btn-sm" onClick={() => logout(r)} style={{ gap: 5 }}><LogOut size={11} /> Log out</button>
          </div>
        </div>
      ))}

      <div style={{ ...body, fontSize: 11 }}>
        {phase === 'awaiting'
          ? `Log in to ${automation.name || id} in the browser tab that opened (including any "verify you're human" step), then confirm here.`
          : `A tab opens in your browser${loginUrl ? ` at ${loginUrl}` : ''}. Log in by hand, then come back and confirm.`}
      </div>
      <div style={{ display: 'flex', gap: 8 }}>
        {phase === 'awaiting' ? (
          <button className="btn btn-primary btn-sm" onClick={confirmLogin} style={{ gap: 5 }}><CheckCircle size={11} /> I've logged in — Save session</button>
        ) : (
          <button className="btn btn-primary btn-sm" onClick={start} disabled={phase !== 'idle'} style={{ gap: 5 }}>
            {rows.length ? <RefreshCw size={11} /> : <LogIn size={11} />} {rows.length ? 'Log in again' : 'Log in'}
          </button>
        )}
      </div>
      {phase === 'opening' && <Busy text="Opening browser…" />}
      {phase === 'saving' && <Busy text="Saving session…" />}
      {msg && (msg.ok ? <OkBox>{msg.text}</OkBox> : <ErrorBox>{msg.text}</ErrorBox>)}
      {steps.length > 0 && (
        <div style={{ ...panel, maxHeight: 140, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 4 }}>
          {steps.map((st, i) => (
            <span key={i} style={{ ...mono, fontSize: 10, color: st.kind === 'error' ? 'var(--red)' : 'var(--text-secondary)' }}>{st.message}</span>
          ))}
        </div>
      )}
    </>
  )
}
