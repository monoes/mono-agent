import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { Clock } from 'lucide-react'
import { execStatus } from '../../lib/execStatus.js'
import { duration, relTime } from './format.js'
import { isAgentNotSetup, withoutAgentSetupMarker } from '../../lib/agentSetup.js'
import AgentSetupLink from '../../components/AgentSetupLink.jsx'
import ProfileChip from './ProfileChip.jsx'
import { ownRow } from './scope.js'
import { switchToProfile } from './profileSwitch.js'

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

const ExecRow = memo(function ExecRow({ exec, onNavigate, currentId, onSwitch }) {
  const { t } = useTranslation()
  const dur = duration(exec.started_at, exec.finished_at)
  const own = ownRow(exec, currentId)
  const open = own
    ? () => onNavigate('noderunner', { executionId: exec.id, workflowId: exec.workflow_id })
    : () => onSwitch(exec.profile_id)
  const error = withoutAgentSetupMarker(exec.error)
  const row = (
    <button className="dash-exec-row" onClick={open}
      title={own ? t('dashboard.recentRuns.openTitle') : t('dashboard.profiles.switchTo', { name: exec.profile_name || exec.profile_id })}>
      <ExecStatusDot status={exec.status} />
      <span style={{ flex: 1, minWidth: 0, textAlign: 'left', display: 'block' }}>
        <span className="dash-ellipsis" style={{ display: 'block', fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text)' }}>
          {exec.workflow_name || (exec.workflow_id || '').slice(0, 8)}
        </span>
        <ProfileChip row={exec} />
        {error && (
          <span className="dash-ellipsis" style={{ display: 'block', fontSize: 10, color: '#ef4444', marginTop: 1 }} title={error}>
            {error}
          </span>
        )}
      </span>
      <span style={{ textAlign: 'right', flexShrink: 0, display: 'block' }}>
        {dur && <span style={{ display: 'block', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--cyan-dim)' }}>{dur}</span>}
        <span style={{ display: 'block', fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-dim)' }}>{relTime(exec.created_at, t)}</span>
      </span>
    </button>
  )
  // A run that failed because the AI agent is not set up links to the AI
  // agents page — beside the row, since a button cannot hold another.
  if (!isAgentNotSetup(exec.error)) return row
  return (
    <div>
      {row}
      <div style={{ padding: '0 0 6px 17px' }}><AgentSetupLink onNavigate={onNavigate} compact /></div>
    </div>
  )
})

export default function RecentRunsCard({ executions, onNavigate, currentId = '', onSwitch = switchToProfile }) {
  const { t } = useTranslation()
  return (
    <div className="card">
      <div className="section-header">
        <div className="section-title"><Clock size={12} /> {t('dashboard.recentRuns.title')}</div>
      </div>
      {executions.length === 0 ? (
        <div className="dash-empty">{t('dashboard.recentRuns.empty')}</div>
      ) : (
        <div>{executions.slice(0, 15).map(e => <ExecRow key={e.id} exec={e} onNavigate={onNavigate} currentId={currentId} onSwitch={onSwitch} />)}</div>
      )}
    </div>
  )
}
