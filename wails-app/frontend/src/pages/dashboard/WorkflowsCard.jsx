import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { GitBranch, Play, ChevronRight, ToggleLeft, ToggleRight, Loader, StopCircle, Clock } from 'lucide-react'
import { execStatus } from '../../lib/execStatus.js'
import { relTime, untilTime } from './format.js'
import { ExecStatusDot } from './RecentRunsCard.jsx'

function ScheduleChip({ sched, invalid, daemonRunning }) {
  const { t } = useTranslation()
  if (invalid) {
    return <span className="dash-chip dash-chip-warn" title={invalid.error}><Clock size={10} /> {t('dashboard.workflows.scheduleInvalid')}</span>
  }
  if (!sched) return null
  if (!daemonRunning) {
    return <span className="dash-chip" title={t('dashboard.workflows.schedulePausedTitle')}><Clock size={10} /> {t('dashboard.workflows.schedulePaused')}</span>
  }
  return <span className="dash-chip" title={new Date(sched.next_run).toLocaleString()}><Clock size={10} /> {untilTime(sched.next_run, t)}</span>
}

function WorkflowRow({ wf, last, sched, invalid, daemonRunning, onRun, onStop, onToggle, onNavigate }) {
  const { t } = useTranslation()
  const [running, setRunning] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [toggling, setToggling] = useState(false)
  const st = execStatus(last?.status)

  const handleRun = async () => {
    setRunning(true)
    try { await onRun(wf.id) } finally { setTimeout(() => setRunning(false), 2000) }
  }
  const handleStop = async () => {
    if (!last?.id) return
    setStopping(true)
    try { await onStop(last.id) } finally { setStopping(false) }
  }
  const handleToggle = async () => {
    setToggling(true)
    try { await onToggle(wf.id, !wf.is_active) } finally { setToggling(false) }
  }

  return (
    <div className="wf-row" style={{ opacity: wf.is_active ? 1 : 0.55 }}>
      <button
        className="btn btn-ghost btn-icon"
        onClick={handleToggle}
        disabled={toggling}
        title={wf.is_active ? t('dashboard.workflows.deactivate') : t('dashboard.workflows.activate')}
        aria-label={wf.is_active ? t('dashboard.workflows.deactivate') : t('dashboard.workflows.activate')}
        style={{ color: wf.is_active ? 'var(--cyan)' : 'var(--text-dim)', padding: 2 }}
      >
        {wf.is_active ? <ToggleRight size={18} /> : <ToggleLeft size={18} />}
      </button>

      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="dash-ellipsis" style={{ fontWeight: 600, fontSize: 13, color: 'var(--text)' }}>{wf.name}</div>
        {wf.description && (
          <div className="dash-ellipsis" style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 1 }}>{wf.description}</div>
        )}
      </div>

      <ScheduleChip sched={sched} invalid={invalid} daemonRunning={daemonRunning} />

      <div style={{ textAlign: 'right', minWidth: 80 }}>
        {last ? (
          <>
            <div style={{ display: 'flex', alignItems: 'center', gap: 5, justifyContent: 'flex-end' }}>
              <ExecStatusDot status={last.status} />
              <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: st.tone, textTransform: 'uppercase' }}>{last.status}</span>
            </div>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-dim)', marginTop: 2 }}>{relTime(last.created_at, t)}</div>
          </>
        ) : (
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-dim)' }}>{t('dashboard.workflows.neverRun')}</span>
        )}
      </div>

      {st.live ? (
        <button className="btn btn-sm dash-stop-btn" onClick={handleStop} disabled={stopping} title={t('dashboard.workflows.stopTitle')}>
          {stopping ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} /> : <StopCircle size={11} />}
          {t('dashboard.workflows.stop')}
        </button>
      ) : (
        <button className="btn btn-secondary btn-sm" onClick={handleRun} disabled={running} style={{ gap: 4, minWidth: 60, flexShrink: 0 }}>
          {running ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} /> : <Play size={11} />}
          {running ? t('dashboard.workflows.starting') : t('dashboard.workflows.run')}
        </button>
      )}

      <button
        className="btn btn-ghost btn-icon"
        onClick={() => onNavigate('noderunner', last ? { workflowId: wf.id, executionId: last.id } : { workflowId: wf.id })}
        style={{ padding: 3, color: 'var(--text-dim)' }}
        title={t('dashboard.workflows.openEditor')}
        aria-label={`${t('dashboard.workflows.openEditor')}: ${wf.name || ''}`}
      >
        <ChevronRight size={14} />
      </button>
    </div>
  )
}

export default function WorkflowsCard({ workflows, executions, schedules, onRun, onStop, onToggle, onNavigate }) {
  const { t } = useTranslation()
  const lastByWf = useMemo(() => {
    const m = {}
    for (const e of executions || []) if (!m[e.workflow_id]) m[e.workflow_id] = e // newest first
    return m
  }, [executions])
  const schedByWf = useMemo(() => {
    const m = {}
    for (const s of schedules?.upcoming || []) if (!m[s.workflow_id]) m[s.workflow_id] = s // soonest first
    return m
  }, [schedules])
  const invalidByWf = useMemo(() => {
    const m = {}
    for (const s of schedules?.invalid || []) if (!m[s.workflow_id]) m[s.workflow_id] = s
    return m
  }, [schedules])

  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><GitBranch size={12} /> {t('dashboard.workflowsSection.title')}</div>
        <button className="btn btn-ghost btn-sm" onClick={() => onNavigate('noderunner')} style={{ fontSize: 11, gap: 3 }}>
          {t('dashboard.workflowsSection.openEditor')} <ChevronRight size={11} />
        </button>
      </div>
      {workflows.length === 0 ? (
        <div className="empty-state" style={{ padding: '32px 0' }}>
          <GitBranch size={28} style={{ color: 'var(--text-dim)', marginBottom: 8 }} />
          <div className="empty-state-title" style={{ fontSize: 13 }}>{t('dashboard.workflowsSection.emptyTitle')}</div>
          <div className="empty-state-desc" style={{ marginBottom: 12 }}>{t('dashboard.workflowsSection.emptyDesc')}</div>
          <button className="btn btn-secondary btn-sm" onClick={() => onNavigate('noderunner')} style={{ gap: 5 }}>
            <Play size={12} /> {t('dashboard.workflowsSection.openEditor')}
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {workflows.map(wf => (
            <WorkflowRow key={wf.id} wf={wf} last={lastByWf[wf.id]} sched={schedByWf[wf.id]} invalid={invalidByWf[wf.id]}
              daemonRunning={!!schedules?.daemon_running}
              onRun={onRun} onStop={onStop} onToggle={onToggle} onNavigate={onNavigate} />
          ))}
        </div>
      )}
    </div>
  )
}
