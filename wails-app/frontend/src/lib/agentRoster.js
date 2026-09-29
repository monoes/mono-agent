// Pure helpers for the validated agent roster (monoes/mono-agent#225). The
// roster itself comes from `monoagentcli agent roster --json`; a running
// validation streams `agent validate --json` lines ("agents:validate"),
// which applyValidateLine folds in so rows update live.

export const rowKey = (runtime, model) => `${runtime}\u0000${model}`

export const emptyRun = { running: false, runId: '', plan: null, active: {}, results: {}, summary: null, error: '' }

// applyValidateLine folds one validate progress line into the run state.
export function applyValidateLine(run, line) {
  if (!line || typeof line !== 'object') return run
  switch (line.type) {
    case 'validate.plan':
      return { ...emptyRun, running: true, runId: line.run_id || '', plan: line.plan || null }
    case 'validate.started': {
      const t = line.target || {}
      return { ...run, running: true, active: { ...run.active, [rowKey(t.runtime, t.model)]: true } }
    }
    case 'validate.result': {
      const r = line.result || {}
      const key = rowKey(r.runtime, r.model)
      const active = { ...run.active }
      delete active[key]
      return { ...run, active, results: { ...run.results, [key]: r } }
    }
    case 'validate.done':
      return { ...run, running: false, active: {}, summary: line.summary || null }
    default:
      return run
  }
}

const works = status => status === 'ok' || status === 'ok_unexpected'

// withLiveResults overlays results that arrived during a run on the stored
// roster, so a row changes the moment its test finishes.
export function withLiveResults(runtimes, results) {
  const live = Object.values(results || {})
  if (!live.length) return runtimes
  const out = (runtimes || []).map(rr => ({ ...rr, models: [...(rr.models || [])] }))
  for (const r of live) {
    let rr = out.find(x => x.runtime === r.runtime)
    if (!rr) {
      rr = { runtime: r.runtime, installed: true, models: [], ready: 0 }
      out.push(rr)
    }
    const entry = { ...r, state: works(r.status) ? 'ready' : 'failed', stale_reason: '' }
    const i = rr.models.findIndex(m => m.model === r.model)
    if (i >= 0) rr.models[i] = { ...rr.models[i], ...entry }
    else rr.models.push(entry)
    rr.ready = rr.models.filter(m => m.state === 'ready').length
  }
  return out
}

// chipFor names a model row's status for the UI: a translation key suffix,
// a tone (ok, warn, bad, muted), and the value shown with it.
export function chipFor(entry) {
  if (!entry) return { key: 'untested', tone: 'muted' }
  if (entry.state === 'untested') return { key: 'untested', tone: 'muted' }
  if (entry.state === 'stale') return { key: entry.stale_reason === 'version' ? 'staleVersion' : 'stale', tone: 'warn' }
  if (entry.state === 'ready') return { key: entry.status === 'ok_unexpected' ? 'okUnexpected' : 'ok', tone: entry.status === 'ok_unexpected' ? 'warn' : 'ok' }
  const known = ['auth', 'quota', 'model_unavailable', 'timeout', 'missing_binary']
  return { key: known.includes(entry.status) ? entry.status : 'error', tone: 'bad' }
}

export function formatLatency(ms) {
  if (!ms || ms <= 0) return '—'
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`
}

export function formatCost(entry) {
  if (!entry || !entry.has_cost) return ''
  const c = entry.cost_usd || 0
  // cost_estimated: monomind priced the turn from its table because the
  // runtime reported no cost.
  const approx = entry.cost_estimated ? '≈' : ''
  return approx + (c < 0.0001 ? '<$0.0001' : `$${c.toFixed(4)}`)
}

// ageParts gives how long ago a validation ran as {n, unit} for i18n, or
// null when it never ran (a manual model, or Go's zero time).
export function ageParts(iso, now = Date.now()) {
  const t = Date.parse(iso || '')
  if (!Number.isFinite(t) || t <= 0 || new Date(t).getUTCFullYear() < 2000) return null
  const s = Math.max(0, Math.round((now - t) / 1000))
  if (s < 60) return { n: s, unit: 's' }
  if (s < 3600) return { n: Math.round(s / 60), unit: 'm' }
  if (s < 86400) return { n: Math.round(s / 3600), unit: 'h' }
  return { n: Math.round(s / 86400), unit: 'd' }
}

// planSummary counts a dry-run plan for the confirmation dialog.
export function planSummary(plan) {
  const p = plan || {}
  return {
    calls: p.calls || 0,
    cost: p.est_cost_usd || 0,
    unknown: p.unknown_cost || 0,
    runtimes: new Set((p.targets || []).map(t => t.runtime)).size,
  }
}
