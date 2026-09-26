import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { Clock } from 'lucide-react'
import { execStatus } from '../../lib/execStatus.js'
import { duration, relTime } from './format.js'

export function ExecStatusDot({ status }) {
  const { tone, live } = execStatus(status)
  return (
    <span style={{
      display: 'inline-block', width: 7, height: 7, borderRadius: '50%',
      background: tone, flexShrink: 0,
      boxShadow: live ? `0 0 6px ${tone}` : 'none',
      animation: live ? 'pulse 1.4s ease-in-out infinite' : 'none',
    }} />
  )
}

const ExecRow = memo(function ExecRow({ exec, onNavigate }) {
  const { t } = useTranslation()
  const dur = duration(exec.started_at, exec.finished_at)
  const open = () => onNavigate('noderunner', { executionId: exec.id, workflowId: exec.workflow_id })
  return (
    <button className="dash-exec-row" onClick={open} title={t('dashboard.recentRuns.openTitle')}>
      <ExecStatusDot status={exec.status} />
      <div style={{ flex: 1, minWidth: 0, textAlign: 'left' }}>
        <div className="dash-ellipsis" style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text)' }}>
          {exec.workflow_name || (exec.workflow_id || '').slice(0, 8)}
        </div>
        {exec.error && (
          <div className="dash-ellipsis" style={{ fontSize: 10, color: '#ef4444', marginTop: 1 }} title={exec.error}>
            {exec.error}
          </div>
        )}
      </div>
      <div style={{ textAlign: 'right', flexShrink: 0 }}>
        {dur && <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--cyan-dim)' }}>{dur}</div>}
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-dim)' }}>{relTime(exec.created_at, t)}</div>
      </div>
    </button>
  )
})

export default function RecentRunsCard({ executions, onNavigate }) {
  const { t } = useTranslation()
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Clock size={12} /> {t('dashboard.recentRuns.title')}</div>
      </div>
      {executions.length === 0 ? (
        <div className="dash-empty">{t('dashboard.recentRuns.empty')}</div>
      ) : (
        <div>{executions.slice(0, 15).map(e => <ExecRow key={e.id} exec={e} onNavigate={onNavigate} />)}</div>
      )}
    </div>
  )
}
