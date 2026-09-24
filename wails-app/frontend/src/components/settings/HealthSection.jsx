import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, Globe, FolderTree, Loader2, TerminalSquare } from 'lucide-react'
import { GetVersion } from '../../wailsjs/go/main/App'
import { confirm } from '../ConfirmDialog.jsx'
import HealthRow, { STATUS_STYLE } from './HealthRow.jsx'
import SetupSteps from './SetupSteps.jsx'
import {
  getHealth, subscribeHealth, runHealth, runFix,
  groupReport, summarize, fixPlan, versionSkew,
} from '../../lib/health.js'

// Settings › System health — renders `monoagentcli doctor --json` and runs
// its fixes (docs/plans/2026-09-24-setup-and-health-check.md §5).

const mono = { fontFamily: 'var(--font-mono)' }
const MAX_FIX_LINES = 40
const MAX_FIX_PASSES = 3

const LEVEL_COLOR = { ok: 'var(--green-neon)', issues: '#fbbf24', broken: 'var(--red)' }

function ToolbarButton({ icon: Icon, label, onClick, disabled, primary }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      style={{
        ...mono, fontSize: 10, display: 'inline-flex', alignItems: 'center', gap: 6,
        padding: '5px 12px', borderRadius: 5, cursor: disabled ? 'default' : 'pointer',
        background: primary ? '#00b4d8' : 'transparent', color: primary ? '#fff' : 'var(--text-secondary)',
        border: primary ? 'none' : '1px solid var(--border)', opacity: disabled ? 0.5 : 1,
      }}
    >
      <Icon size={12} />{label}
    </button>
  )
}

function ago(ts, t) {
  if (!ts) return t('settings.health.never')
  const s = Math.round((Date.now() - ts) / 1000)
  if (s < 60) return t('settings.health.justNow')
  const m = Math.round(s / 60)
  return m < 60 ? t('settings.health.minutesAgo', { n: m }) : t('settings.health.hoursAgo', { n: Math.round(m / 60) })
}

export default function HealthSection({ onNavigate }) {
  const { t } = useTranslation()
  const [h, setH] = useState(getHealth())
  const [guiVersion, setGuiVersion] = useState(null)
  const [fixStates, setFixStates] = useState({})
  const [fixingAll, setFixingAll] = useState(false)
  const [, tick] = useState(0)
  const modeRef = useRef({ deep: false, projects: false })

  useEffect(() => subscribeHealth(setH), [])
  useEffect(() => {
    if (!getHealth().report && !getHealth().loading) runHealth()
    GetVersion().then(v => setGuiVersion(v?.version)).catch(() => {})
    const id = setInterval(() => tick(n => n + 1), 30000) // keep "checked … ago" fresh
    return () => clearInterval(id)
  }, [])

  const recheck = useCallback(() => runHealth(modeRef.current), [])

  const patchFix = (id, patch) => setFixStates(s => ({ ...s, [id]: { ...(s[id] || {}), ...patch } }))

  // applyFix runs one fix: manual ones copy their command, confirm ones ask
  // first. Resolves true when the fix ran and succeeded.
  const applyFix = useCallback(async (fix, { ask = true } = {}) => {
    if (fix.safety === 'manual') {
      try { await navigator.clipboard.writeText(fix.command || '') } catch { /* clipboard may be blocked */ }
      patchFix(fix.id, { lines: [`${t('settings.health.copied')}: ${fix.command || ''}`], error: null, done: false })
      return false
    }
    if (fix.safety === 'confirm' && ask) {
      const ok = await confirm(
        <span>
          {t('settings.health.confirmBody')}
          <code style={{ display: 'block', marginTop: 8, padding: '6px 8px', background: 'rgba(0,0,0,.35)', borderRadius: 4, wordBreak: 'break-all' }}>
            {fix.command || fix.id}
          </code>
        </span>,
        { title: fix.label, confirmLabel: t('settings.health.run'), danger: false },
      )
      if (!ok) return false
    }
    patchFix(fix.id, { running: true, lines: [], error: null, done: false })
    const res = await runFix(fix.id, line => setFixStates(s => {
      const cur = s[fix.id] || {}
      return { ...s, [fix.id]: { ...cur, lines: [...(cur.lines || []), line].slice(-MAX_FIX_LINES) } }
    }))
    patchFix(fix.id, { running: false, done: res.ok, error: res.ok ? null : res.message })
    return res.ok
  }, [t])

  const onRowFix = useCallback(async fix => {
    const ran = await applyFix(fix)
    if (ran || fix.safety !== 'manual') recheck()
  }, [applyFix, recheck])

  // "Fix issues": every non-optional auto/confirm fix, in order, asking
  // before each confirm one; re-check between passes because a fix can
  // unblock checks that were waiting on it (like `monoagentcli setup`).
  const fixAll = useCallback(async () => {
    setFixingAll(true)
    const tried = new Set()
    try {
      for (let pass = 0; pass < MAX_FIX_PASSES; pass++) {
        const plan = fixPlan(getHealth().report).filter(f => !tried.has(f.id))
        if (plan.length === 0) break
        for (const fix of plan) {
          tried.add(fix.id)
          await applyFix(fix)
        }
        await runHealth(modeRef.current)
      }
    } finally {
      setFixingAll(false)
    }
  }, [applyFix])

  const run = mode => { modeRef.current = mode; runHealth(mode) }

  if (h.cliMissing) {
    return (
      <div style={{ background: 'var(--surface)', border: '1px solid rgba(239,68,68,.35)', borderRadius: 'var(--radius-lg)', padding: '16px 20px', marginBottom: 28 }}>
        <div style={{ ...mono, fontSize: 12, color: 'var(--red)', marginBottom: 6 }}>✗ {t('settings.health.cliMissingTitle')}</div>
        <div style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)', lineHeight: 1.6 }}>{t('settings.health.cliMissingBody')}</div>
        <div style={{ ...mono, fontSize: 10, color: 'var(--text-muted)', marginTop: 8 }}>{h.error}</div>
      </div>
    )
  }

  const report = h.report
  const sum = summarize(report)
  const groups = groupReport(report)
  const skew = report && guiVersion && versionSkew(guiVersion, report.monoagent_version)
  const bannerText = !report
    ? (h.loading ? t('settings.health.checking') : t('settings.health.notChecked'))
    : sum.level === 'broken' ? t('settings.health.broken', { n: sum.required })
      : sum.level === 'issues' ? t('settings.health.issues', { n: sum.issues })
        : t('settings.health.ready')

  return (
    <div data-testid="system-health" style={{ marginBottom: 28 }}>
      <div style={{ background: 'var(--surface)', border: `1px solid ${report ? LEVEL_COLOR[sum.level] : 'var(--border)'}33`, borderRadius: 'var(--radius-lg)', padding: '14px 18px', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
        <span style={{ width: 10, height: 10, borderRadius: '50%', flexShrink: 0, background: report ? LEVEL_COLOR[sum.level] : 'var(--text-muted)', boxShadow: report ? `0 0 6px ${LEVEL_COLOR[sum.level]}` : 'none' }} />
        <div style={{ flex: 1, minWidth: 200 }}>
          <div style={{ ...mono, fontSize: 12.5, color: 'var(--text)' }}>{bannerText}</div>
          <div style={{ ...mono, fontSize: 10, color: 'var(--text-muted)', marginTop: 3 }}>
            {h.loading ? t('settings.health.checking') : `${t('settings.health.lastChecked')} ${ago(h.lastRun, t)}`}
            {report?.profile_id && ` · ${t('settings.health.profile')} ${report.profile_id}`}
          </div>
        </div>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <ToolbarButton icon={h.loading ? Loader2 : RefreshCw} label={t('settings.health.checkAgain')} onClick={() => run({ deep: false, projects: false })} disabled={h.loading || fixingAll} />
          <ToolbarButton icon={Globe} label={t('settings.health.deepCheck')} onClick={() => run({ deep: true, projects: false })} disabled={h.loading || fixingAll} />
          <ToolbarButton icon={FolderTree} label={t('settings.health.checkProjects')} onClick={() => run({ deep: false, projects: true })} disabled={h.loading || fixingAll} />
        </div>
      </div>

      {report && (
        <SetupSteps report={report} level={sum.level} fixStates={fixStates} fixingAll={fixingAll || h.loading}
          onRunAll={fixAll} onFix={onRowFix} onNavigate={onNavigate} />
      )}

      {h.error && (
        <div style={{ ...mono, fontSize: 10.5, color: 'var(--red)', marginBottom: 10 }}>{h.error}</div>
      )}
      {skew && (
        <div style={{ ...mono, fontSize: 10.5, color: '#fbbf24', marginBottom: 10 }}>
          ⚠ {t('settings.health.versionSkew', { gui: guiVersion, cli: report.monoagent_version })}
        </div>
      )}

      {groups.map(g => (
        <div key={g.group} style={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', padding: '8px 18px 6px', marginBottom: 10 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 0 6px' }}>
            <span style={{ ...mono, fontSize: 10, fontWeight: 700, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: 1.5 }}>
              {t(`settings.health.group.${g.group}`, { defaultValue: g.group })}
            </span>
            {g.problems > 0 && (
              <span style={{ ...mono, fontSize: 9, color: '#fbbf24', border: '1px solid rgba(251,191,36,.3)', borderRadius: 8, padding: '0 6px' }}>
                {t('settings.health.problemCount', { n: g.problems })}
              </span>
            )}
            {g.group === 'runtimes' && onNavigate && (
              <button onClick={() => onNavigate('ai')} style={{ ...mono, fontSize: 10, marginLeft: 'auto', background: 'none', border: 'none', color: '#00b4d8', cursor: 'pointer', padding: 0 }}>
                {t('settings.health.openAgents')} →
              </button>
            )}
          </div>
          {g.rows.map(r => <HealthRow key={r.id} row={r} fixStates={fixStates} onFix={onRowFix} />)}
        </div>
      ))}

      {report && (
        <div style={{ ...mono, fontSize: 10, color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: 6 }}>
          <TerminalSquare size={11} />{t('settings.health.cliHint')}
          <span style={{ color: STATUS_STYLE.info.color }}>monoagentcli doctor · monoagentcli setup</span>
        </div>
      )}
    </div>
  )
}
