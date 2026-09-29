import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Users, ChevronRight, ChevronDown, Loader, Check, X, Lock } from 'lucide-react'
import { ChatMarkdown } from './ChatMarkdown.jsx'

// AgentRow is one dynamic-org worker (#226) in the lead's timeline: a
// compact line with its role, model and status, expandable to its brief and
// report. The live org stage (#228) shows the same workers graphically.

const mono = 'var(--font-mono)'
const STATUSES = ['queued', 'starting', 'working', 'waiting_lease', 'done', 'failed', 'cancelled', 'idle']

function StatusIcon({ status }) {
  if (status === 'done') return <Check size={10} color="var(--green-neon)" />
  if (status === 'failed') return <X size={10} color="var(--red)" />
  if (status === 'cancelled') return <X size={10} color="var(--text-muted)" />
  if (status === 'waiting_lease') return <Lock size={10} color="var(--yellow)" />
  return <Loader size={10} className="chat-spin" color="var(--cyan)" />
}

export function AgentRow({ agent }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  if (!agent) return null
  const model = [agent.runtime, agent.model].filter(Boolean).join(' · ')
  const status = STATUSES.includes(agent.status) ? t(`agentRow.status.${agent.status}`) : agent.status
  return (
    <div data-testid="agent-row" data-agent={agent.agentId} data-status={agent.status}
      style={{ margin: '4px 0', border: '1px solid rgba(0,180,216,0.15)', borderRadius: 6, background: 'rgba(0,180,216,0.03)' }}>
      <button type="button" onClick={() => setOpen(o => !o)} aria-expanded={open}
        style={{ display: 'flex', alignItems: 'center', gap: 6, width: '100%', padding: '5px 8px', background: 'transparent', border: 'none', cursor: 'pointer', color: 'inherit', textAlign: 'left' }}>
        {open ? <ChevronDown size={10} /> : <ChevronRight size={10} />}
        <Users size={11} color="var(--cyan)" />
        <span style={{ fontFamily: mono, fontSize: 10.5, color: '#e2e8f0', fontWeight: 600 }}>{agent.role || agent.agentId}</span>
        {model && <span style={{ fontFamily: mono, fontSize: 9, color: 'var(--cyan)' }}>{model}</span>}
        {agent.access && <span style={{ fontFamily: mono, fontSize: 8.5, color: 'var(--text-muted)' }}>{agent.access}</span>}
        <span style={{ flex: 1 }} />
        {agent.lastTool && agent.status === 'working' && <span style={{ fontFamily: mono, fontSize: 9, color: 'var(--text-muted)' }}>{agent.lastTool}</span>}
        <StatusIcon status={agent.status} />
        <span style={{ fontFamily: mono, fontSize: 9, color: 'var(--text-muted)' }}>{status}</span>
      </button>
      {!open && agent.summary && (
        <div style={{ padding: '0 8px 5px 30px', fontFamily: mono, fontSize: 9.5, color: 'var(--text-secondary)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{agent.summary}</div>
      )}
      {open && (
        <div data-testid="agent-row-details" style={{ padding: '2px 10px 8px 30px', fontFamily: mono, fontSize: 9.5, color: 'var(--text-secondary)', display: 'flex', flexDirection: 'column', gap: 4 }}>
          {agent.brief && <div><b>{t('agentRow.brief')}</b> {agent.brief}</div>}
          {agent.why && <div style={{ color: 'var(--text-muted)' }}>{t('agentRow.staffing', { why: agent.why })}</div>}
          {agent.reassigned && <div style={{ color: 'var(--yellow)' }}>{t('agentRow.switchedFrom', { from: agent.reassigned })}</div>}
          {agent.skills?.length > 0 && <div>{t('agentRow.skills', { skills: agent.skills.join(', ') })}</div>}
          {agent.filesChanged?.length > 0 && <div>{t('agentRow.files', { files: agent.filesChanged.join(', ') })}</div>}
          {agent.tools > 0 && (
            <div>
              {t('agentRow.toolCalls', { count: agent.tools })}
              {agent.costUsd != null ? ` · $${agent.costUsd.toFixed(4)}` : ''}
            </div>
          )}
          {agent.report && <ChatMarkdown content={agent.report} />}
        </div>
      )}
    </div>
  )
}
