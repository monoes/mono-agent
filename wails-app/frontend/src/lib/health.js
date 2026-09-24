// System health: state shared by Settings › System health and the status-bar
// dot. Everything it knows comes from `monoagentcli doctor --json` (via the
// RunHealthCheck/RunHealthFix bindings); this module only stores, sorts and
// re-requests it.
import { RunHealthCheck, RunHealthFix } from '../wailsjs/go/main/App'
import { subscribeEvent } from '../services/api.js'

export const BACKGROUND_INTERVAL_MS = 30 * 60 * 1000

// ── pure helpers (tested) ───────────────────────────────────────────────────

const PROBLEM = new Set(['warn', 'fail'])

/** Rows of a report nested under their parent row (report.results is flat). */
export function groupReport(report) {
  const groups = []
  const byGroup = new Map()
  const byId = new Map()
  for (const r of report?.results || []) {
    byId.set(r.id, { ...r, children: [] })
  }
  for (const r of report?.results || []) {
    const row = byId.get(r.id)
    if (r.parent && byId.has(r.parent)) {
      byId.get(r.parent).children.push(row)
      continue
    }
    if (!byGroup.has(r.group)) {
      const g = { group: r.group, rows: [] }
      byGroup.set(r.group, g)
      groups.push(g)
    }
    byGroup.get(r.group).rows.push(row)
  }
  for (const g of groups) {
    g.problems = countProblems(g.rows)
  }
  return groups
}

function countProblems(rows) {
  let n = 0
  for (const r of rows) {
    if (PROBLEM.has(r.status)) n++
    n += countProblems(r.children || [])
  }
  return n
}

/** Overall verdict for the banner and the status-bar dot. */
export function summarize(report) {
  const results = report?.results || []
  const required = results.filter(r => r.required && r.status === 'fail').length
  const fails = results.filter(r => r.status === 'fail').length
  const warns = results.filter(r => r.status === 'warn').length
  let level = 'ok'
  if (required > 0) level = 'broken'
  else if (fails + warns > 0) level = 'issues'
  return { level, required, fails, warns, issues: fails + warns, fixable: fixPlan(report).length }
}

/**
 * The fixes "Fix issues" applies, in report order, one per fix id:
 * auto and confirm ones that aren't optional. Manual and optional fixes are
 * only ever applied from their own row.
 */
export function fixPlan(report) {
  const seen = new Set()
  const plan = []
  for (const r of report?.results || []) {
    const f = r.fix
    if (!f || f.optional || f.safety === 'manual' || seen.has(f.id)) continue
    if (!PROBLEM.has(r.status)) continue
    seen.add(f.id)
    plan.push({ ...f, rowTitle: r.title })
  }
  return plan
}

/** True when two release builds differ (dev/git-describe builds never do). */
export function versionSkew(guiVersion, cliVersion) {
  const norm = v => String(v || '').trim().replace(/^v/, '')
  const a = norm(guiVersion)
  const b = norm(cliVersion)
  const dev = v => !v || v === 'dev' || /-g[0-9a-f]+/.test(v)
  if (dev(a) || dev(b)) return false
  return a !== b
}

// ── shared state ────────────────────────────────────────────────────────────

let state = { report: null, error: null, cliMissing: false, loading: false, lastRun: null, mode: null }
const listeners = new Set()

export function getHealth() { return state }

export function subscribeHealth(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function set(patch) {
  state = { ...state, ...patch }
  for (const fn of listeners) fn(state)
}

const inflight = new Map() // mode key → promise

/**
 * Run the checks. deep adds network checks; projects adds monomind's checks
 * per project. Concurrent calls for the same mode share one run.
 */
export function runHealth({ deep = false, projects = false } = {}) {
  const key = `${deep ? 'deep' : ''}${projects ? 'projects' : ''}` || 'local'
  if (inflight.has(key)) return inflight.get(key)
  set({ loading: true })
  const p = RunHealthCheck(deep, projects)
    .then(s => {
      const data = JSON.parse(s)
      if (data.error) set({ error: data.error, cliMissing: !!data.cli_missing })
      else set({ report: data, error: null, cliMissing: false, lastRun: Date.now(), mode: key })
    })
    .catch(e => set({ error: String(e) }))
    .finally(() => {
      inflight.delete(key)
      set({ loading: inflight.size > 0 })
    })
  inflight.set(key, p)
  return p
}

let activeFixes = 0

/** True while a fix runs — lets the app hold back navigation it would
 * otherwise do (a fix like `monomind init` creates a sample org). */
export function isFixing() { return activeFixes > 0 }

/**
 * Apply one fix; onLine gets each progress line. Resolves to
 * { ok: true } or { ok: false, message }.
 */
export function runFix(fixId, onLine) {
  activeFixes++
  return new Promise(resolve => {
    const off = subscribeEvent('health:fixProgress', ev => {
      if (!ev || ev.fix_id !== fixId) return
      if (ev.kind === 'line') { onLine?.(ev.message || ''); return }
      off()
      resolve(ev.kind === 'done' ? { ok: true } : { ok: false, message: ev.message || 'failed' })
    })
    RunHealthFix(fixId)
      .then(s => {
        const r = JSON.parse(s)
        if (r.error) { off(); resolve({ ok: false, message: r.error }) }
      })
      .catch(e => { off(); resolve({ ok: false, message: String(e) }) })
  }).finally(() => { activeFixes-- })
}

let timer = null

/** Start the startup + every-30-minutes local check (idempotent). */
export function startBackgroundHealth() {
  if (timer) return () => {}
  runHealth()
  timer = setInterval(() => { runHealth() }, BACKGROUND_INTERVAL_MS)
  return () => { clearInterval(timer); timer = null }
}
