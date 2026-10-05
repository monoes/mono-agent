import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { ShieldCheck, RefreshCw, Square, Loader2, Plus, X, ChevronDown, ChevronRight } from 'lucide-react'
import { api, onAgentValidate, onAgentValidateClosed } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'
import AutoRevalidate from './AutoRevalidate.jsx'
import {
  emptyRun, applyValidateLine, withLiveResults, rowKey, chipFor,
  formatLatency, formatCost, ageParts, planSummary, loginHintFor, trackRecord,
} from '../../lib/agentRoster.js'

// AgentRoster is the "Validated models" section of the AI agents page
// (monoes/mono-agent#225): which runtime × model pairs answered a one-word
// test, with live updates while a validation runs. All work is done by
// `monoagentcli agent validate|roster`; this only renders it.

const TONE = {
  ok: 'var(--green-neon)',
  warn: 'var(--yellow)',
  bad: 'var(--red)',
  muted: 'var(--text-muted)',
}

const mono = { fontFamily: 'var(--font-mono)' }

function StatusChip({ entry, testing }) {
  const { t } = useTranslation()
  if (testing) {
    return (
      <span data-testid="chip" data-tone="testing" style={{ ...mono, fontSize: 10, color: 'var(--cyan)', display: 'inline-flex', alignItems: 'center', gap: 4 }}>
        <Loader2 size={10} className="spin" /> {t('agents.roster.testing')}
      </span>
    )
  }
  const chip = chipFor(entry)
  const color = TONE[chip.tone]
  return (
    <span data-testid="chip" data-tone={chip.tone} title={entry?.detail || ''} style={{
      ...mono, fontSize: 10, color, border: `1px solid ${color}`, borderRadius: 999,
      padding: '1px 7px', whiteSpace: 'nowrap', opacity: chip.tone === 'muted' ? 0.8 : 1,
    }}>
      {t(`agents.roster.status.${chip.key}`)}
    </span>
  )
}

function ModelRow({ runtime, entry, testing, busy, onValidate, onRemove }) {
  const { t } = useTranslation()
  const age = ageParts(entry.validated_at)
  const track = trackRecord(entry)
  return (
    <div data-row={`${runtime}/${entry.model}`} style={{
      display: 'grid', gridTemplateColumns: 'minmax(0,1fr) auto auto auto auto auto', alignItems: 'center',
      gap: 10, padding: '6px 10px', borderTop: '1px solid var(--border)',
    }}>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 12, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={entry.model}>
          {entry.label || entry.model}
          {entry.source === 'manual' && <span style={{ ...mono, fontSize: 9, color: 'var(--text-muted)', marginLeft: 6 }}>{t('agents.roster.manual')}</span>}
        </div>
        <div style={{ ...mono, fontSize: 9.5, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {entry.model}
          {entry.effort_levels?.length > 0 && ` · ${t('agents.roster.effort', { count: entry.effort_levels.length })}`}
          {!testing && entry.detail && entry.state !== 'ready' && <span title={entry.detail}> · {entry.detail}</span>}
        </div>
      </div>
      <StatusChip entry={entry} testing={testing} />
      <span data-testid="track-record" style={{ ...mono, fontSize: 9.5, color: 'var(--text-secondary)', whiteSpace: 'nowrap' }}
        title={track.length ? t('agents.roster.trackRecord', { entries: track.map(r => t('agents.roster.trackEntry', r)).join(', ') }) : undefined}>
        {track.map((r, i) => (
          <span key={r.category} style={r.low ? { color: 'var(--red, #ef4444)' } : undefined}>
            {i > 0 && ' · '}{r.category} {r.pct}%{r.low && ` ${t('agents.roster.trackLow')}`}
          </span>
        ))}
      </span>
      <span style={{ ...mono, fontSize: 10, color: 'var(--text-secondary)', minWidth: 52, textAlign: 'right' }}>
        {formatLatency(entry.latency_ms)}
        {formatCost(entry) && <span style={{ color: 'var(--text-muted)' }}> · {formatCost(entry)}</span>}
      </span>
      <span style={{ ...mono, fontSize: 9.5, color: 'var(--text-muted)', minWidth: 56, textAlign: 'right' }}>
        {age ? t(`agents.roster.ago.${age.unit}`, { n: age.n }) : t('agents.roster.never')}
      </span>
      <span style={{ display: 'inline-flex', gap: 2 }}>
        <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => onValidate(runtime, entry.model)}
          title={t('agents.roster.revalidateModel')} aria-label={t('agents.roster.revalidateModel')} style={{ padding: '2px 5px' }}>
          <RefreshCw size={11} />
        </button>
        {entry.source === 'manual' && (
          <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => onRemove(runtime, entry.model)}
            title={t('agents.roster.remove')} aria-label={t('agents.roster.remove')} style={{ padding: '2px 5px' }}>
            <X size={11} />
          </button>
        )}
      </span>
    </div>
  )
}

function RuntimeCard({ rr, run, busy, onValidate, onAdd, onRemove }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(true)
  const [adding, setAdding] = useState('')
  const models = rr.models || []
  const authFailed = models.some(m => m.status === 'auth')
  const loginHint = loginHintFor(rr)
  const testingHere = models.some(m => run.active[rowKey(rr.runtime, m.model)])
  return (
    <div data-roster-runtime={rr.runtime} style={{
      border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', background: 'var(--surface)', overflow: 'hidden',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 10px' }}>
        <button className="btn btn-ghost btn-sm" onClick={() => setOpen(o => !o)} aria-expanded={open}
          aria-label={rr.runtime} style={{ padding: '2px 4px' }}>
          {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        </button>
        <span style={{ ...mono, fontSize: 12, fontWeight: 600, color: 'var(--text)' }}>{rr.runtime}</span>
        {rr.version && <span style={{ ...mono, fontSize: 9.5, color: 'var(--text-muted)' }}>{rr.version}</span>}
        <span style={{ ...mono, fontSize: 10, color: rr.ready > 0 ? 'var(--green-neon)' : 'var(--text-muted)' }}>
          {models.length ? t('agents.roster.readyOf', { ready: rr.ready || 0, total: models.length }) : t('agents.roster.notValidated')}
        </span>
        {testingHere && <Loader2 size={11} className="spin" style={{ color: 'var(--cyan)' }} />}
        <span style={{ flex: 1 }} />
        {authFailed && loginHint && (
          <span style={{ ...mono, fontSize: 9.5, color: 'var(--yellow)' }}>{t('agents.signIn', { hint: loginHint })}</span>
        )}
        <button className="btn btn-ghost btn-sm" disabled={busy || !rr.installed} onClick={() => onValidate(rr.runtime)} style={{ gap: 4, fontSize: 10 }}>
          <ShieldCheck size={11} /> {t('agents.roster.validate')}
        </button>
      </div>
      {open && (
        <>
          {models.map(m => (
            <ModelRow key={m.model} runtime={rr.runtime} entry={m} busy={busy}
              testing={!!run.active[rowKey(rr.runtime, m.model)]} onValidate={onValidate} onRemove={onRemove} />
          ))}
          <form onSubmit={e => { e.preventDefault(); const v = adding.trim(); if (v) { onAdd(rr.runtime, v); setAdding('') } }}
            style={{ display: 'flex', gap: 6, padding: '6px 10px', borderTop: '1px solid var(--border)' }}>
            <input value={adding} onChange={e => setAdding(e.target.value)} placeholder={t('agents.roster.addPlaceholder')}
              aria-label={t('agents.roster.addPlaceholder')} className="input"
              style={{ flex: 1, fontSize: 11, ...mono, padding: '3px 8px', background: 'var(--base)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', color: 'var(--text)' }} />
            <button type="submit" className="btn btn-ghost btn-sm" disabled={!adding.trim()} style={{ gap: 4, fontSize: 10 }}>
              <Plus size={11} /> {t('agents.roster.add')}
            </button>
          </form>
        </>
      )}
    </div>
  )
}

export default function AgentRoster() {
  const { t } = useTranslation()
  const [roster, setRoster] = useState(null)
  const [error, setError] = useState('')
  const [run, setRun] = useState(emptyRun)

  const load = useCallback(async () => {
    const res = await api.agentRoster()
    if (res?.error) { setError(res.error); return }
    setError('')
    setRoster(res.runtimes || [])
  }, [])

  useEffect(() => {
    load()
    const offLine = onAgentValidate(line => setRun(r => applyValidateLine(r, line)))
    const offClosed = onAgentValidateClosed(res => {
      setRun(r => ({ ...r, running: false, active: {}, error: res?.ok === false ? (res.error || '') : '' }))
      // The reloaded roster is the truth: a row cancelled by Stop keeps its
      // stored state instead of the live "cancelled" result (#294).
      load().then(() => setRun(r => (r.running ? r : { ...r, results: {} })))
    })
    return () => { offLine(); offClosed() }
  }, [load])

  // askFirst: a run of more than one call shows its size and cost first.
  const validate = useCallback(async (runtimes = [], models = [], staleOnly = false) => {
    const res = await api.agentValidatePlan(runtimes, models, staleOnly)
    if (res?.error) { setError(res.error); return }
    const p = planSummary(res.plan)
    if (p.calls === 0) { setRun({ ...emptyRun, summary: { ok: 0, failed: 0, cancelled: 0, planned: 0 } }); return }
    if (p.calls > 1) {
      const cost = p.unknown === p.calls
        ? t('agents.roster.costUnknown')
        : t('agents.roster.costEstimate', { cost: p.cost < 0.0001 ? '<$0.0001' : `$${p.cost.toFixed(4)}` }) +
          (p.table ? ' ' + t('agents.roster.costFromTable', { count: p.table }) : '') +
          (p.unknown ? ' ' + t('agents.roster.costPartlyUnknown', { count: p.unknown }) : '')
      const ok = await confirm(
        <span>
          {t('agents.roster.confirmBody', { calls: p.calls, runtimes: p.runtimes })} {cost}
          {p.signIn.map(n => (
            <span key={n.runtime} data-sign-in-note={n.runtime} style={{ display: 'block', marginTop: 6 }}>
              {t(n.login_hint ? 'agents.roster.signInNoteHint' : 'agents.roster.signInNote', { runtime: n.runtime, hint: n.login_hint })}
            </span>
          ))}
        </span>,
        { title: t('agents.roster.confirmTitle'), confirmLabel: t('agents.roster.validate'), cancelLabel: t('agents.cancel'), danger: false },
      )
      if (!ok) return
    }
    setRun({ ...emptyRun, running: true, plan: res.plan })
    const started = await api.startAgentValidation(runtimes, models, staleOnly)
    if (started?.error) { setRun(emptyRun); setError(started.error) }
  }, [t])

  const onValidate = useCallback((runtime, model) => validate([runtime], model ? [model] : []), [validate])
  const onAdd = useCallback(async (runtime, model) => {
    const res = await api.agentRosterAdd(runtime, model)
    if (res?.error) { setError(res.error); return }
    await load()
    validate([runtime], [model])
  }, [load, validate])
  const onRemove = useCallback(async (runtime, model) => {
    const res = await api.agentRosterRemove(runtime, model)
    if (res?.error) setError(res.error)
    load()
  }, [load])

  const shown = useMemo(() => withLiveResults(roster || [], run.results), [roster, run.results])
  const readyTotal = shown.reduce((n, rr) => n + (rr.ready || 0), 0)
  const planned = run.plan?.calls || 0
  const finished = Object.keys(run.results).length
  const busy = run.running

  return (
    <section data-testid="agent-roster" style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 8, paddingBottom: 24 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <ShieldCheck size={14} style={{ color: 'var(--cyan)' }} />
        <span style={{ fontFamily: 'var(--font-display)', fontSize: 14, fontWeight: 600, color: 'var(--text)' }}>{t('agents.roster.title')}</span>
        <span style={{ ...mono, fontSize: 10, color: 'var(--text-muted)' }}>
          {roster === null ? t('agents.loading') : t('agents.roster.readyTotal', { count: readyTotal })}
        </span>
        <span style={{ flex: 1 }} />
        {busy ? (
          <button className="btn btn-ghost btn-sm" onClick={() => api.stopAgentValidation()} style={{ gap: 4, fontSize: 10 }}>
            <Square size={10} /> {t('agents.roster.stop')}
          </button>
        ) : (
          <>
            <button className="btn btn-ghost btn-sm" onClick={() => validate([], [], true)} style={{ gap: 4, fontSize: 10 }}>
              <RefreshCw size={11} /> {t('agents.roster.recheckStale')}
            </button>
            <button className="btn btn-primary btn-sm" onClick={() => validate()} style={{ gap: 4, fontSize: 10 }}>
              <ShieldCheck size={11} /> {t('agents.roster.validateAll')}
            </button>
          </>
        )}
      </div>
      <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('agents.roster.subtitle')}</div>
      <AutoRevalidate refreshKey={roster} />
      {busy && planned > 0 && (
        <div role="progressbar" aria-valuemin={0} aria-valuemax={planned} aria-valuenow={finished}
          style={{ height: 3, background: 'var(--border)', borderRadius: 2, overflow: 'hidden' }}>
          <div style={{ width: `${Math.round((finished / planned) * 100)}%`, height: '100%', background: 'var(--cyan)', transition: 'width var(--transition)' }} />
        </div>
      )}
      <div role="status" aria-live="polite" style={{ ...mono, fontSize: 10, color: error || run.error ? 'var(--red)' : 'var(--text-muted)' }}>
        {error || run.error || (run.summary && !busy
          ? (run.summary.planned === 0 ? t('agents.roster.nothingToDo') : t('agents.roster.summary', { ok: run.summary.ok, failed: run.summary.failed, cancelled: run.summary.cancelled }))
          : '')}
      </div>
      {roster !== null && shown.length === 0 && (
        <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('agents.roster.empty')}</div>
      )}
      {shown.map(rr => (
        <RuntimeCard key={rr.runtime} rr={rr} run={run} busy={busy} onValidate={onValidate} onAdd={onAdd} onRemove={onRemove} />
      ))}
    </section>
  )
}
