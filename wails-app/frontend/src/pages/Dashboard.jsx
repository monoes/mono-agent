// The at-a-glance home. Every number comes from the CLI — `summary`,
// `org summary`, `workflow list` and `workflow executions --all` (see
// dashboard/useDashboardData.js); this page only lays the cards out.
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { RefreshCw, GitBranch } from 'lucide-react'
import { api } from '../services/api.js'
import { GetVersion } from '../wailsjs/go/main/App'
import { getHealth, subscribeHealth, summarize } from '../lib/health.js'
import { useDashboardData } from './dashboard/useDashboardData.js'
import { attentionItems, unreadSections } from './dashboard/attention.js'
import AttentionStrip from './dashboard/AttentionStrip.jsx'
import StatRow from './dashboard/StatRow.jsx'
import WorkflowsCard from './dashboard/WorkflowsCard.jsx'
import RecentRunsCard from './dashboard/RecentRunsCard.jsx'
import ActivityCard from './dashboard/ActivityCard.jsx'
import SystemCard from './dashboard/SystemCard.jsx'
import OrgsCard from './dashboard/OrgsCard.jsx'
import AutomationsCard from './dashboard/AutomationsCard.jsx'
import AccountsCard from './dashboard/AccountsCard.jsx'

export default function Dashboard({ isActive = true, onRefresh, onNavigate, onOpenHil }) {
  const { t } = useTranslation()
  const { summary, summaryFailed, orgs, workflows, executions, loading, refresh, setExecutions, reloadLists } = useDashboardData({ active: isActive })
  const [ver, setVer] = useState(null)
  const [refreshing, setRefreshing] = useState(false)
  const [health, setHealth] = useState(getHealth())
  useEffect(() => { GetVersion().then(setVer).catch(() => {}) }, [])
  useEffect(() => subscribeHealth(setHealth), [])
  const items = useMemo(
    () => attentionItems(summary, orgs, health?.report ? summarize(health.report) : null),
    [summary, orgs, health],
  )

  const handleRefresh = async () => {
    setRefreshing(true)
    await Promise.all([onRefresh?.(), refresh()])
    setTimeout(() => setRefreshing(false), 400)
  }
  // Optimistic status flips so the row reacts at once; the next poll corrects it.
  const handleRun = async (id) => {
    setExecutions(prev => prev.map(e => (e.workflow_id === id && e.status !== 'RUNNING' ? { ...e, status: 'RUNNING' } : e)))
    await api.runWorkflow(id)
    setTimeout(reloadLists, 1500)
  }
  const handleStop = async (executionId) => {
    setExecutions(prev => prev.map(e => (e.id === executionId ? { ...e, status: 'CANCELLED' } : e)))
    await api.cancelWorkflow(executionId)
    setTimeout(reloadLists, 500)
  }
  const handleToggle = async (id, active) => {
    await api.setWorkflowActive(id, active)
    await reloadLists()
  }

  return (
    <>
      <div className="page-header">
        <div className="page-header-left">
          <div className="page-title">{t('dashboard.title')}</div>
          <div className="page-subtitle">{t('dashboard.subtitle')}{ver ? ` · v${ver.version.replace(/^v/, '')}` : ''}</div>
        </div>
        <div className="page-header-right">
          <button className="btn btn-ghost btn-sm" onClick={handleRefresh} style={{ gap: 5 }}>
            <RefreshCw size={13} style={{ animation: refreshing ? 'spin 0.7s linear infinite' : 'none' }} />
            {t('dashboard.refresh')}
          </button>
          <button className="btn btn-secondary btn-sm" onClick={() => onNavigate('noderunner')} style={{ gap: 5 }}>
            <GitBranch size={13} /> {t('dashboard.workflowEditor')}
          </button>
        </div>
      </div>

      <div className="page-body dash-page">
        <AttentionStrip items={items} loading={loading} failed={summaryFailed} unread={unreadSections(summary)}
          onNavigate={onNavigate} onOpenHil={onOpenHil} />
        <StatRow summary={summary} orgs={orgs} loading={loading} onNavigate={onNavigate} />
        <div className="dashboard-grid">
          <div className="dash-col">
            <WorkflowsCard workflows={workflows} executions={executions} schedules={summary?.schedules}
              onRun={handleRun} onStop={handleStop} onToggle={handleToggle} onNavigate={onNavigate} />
            <RecentRunsCard executions={executions} onNavigate={onNavigate} />
            <ActivityCard summary={summary} onNavigate={onNavigate} />
          </div>
          <div className="dash-col">
            <SystemCard summary={summary} onNavigate={onNavigate} />
            <OrgsCard orgs={orgs} onNavigate={onNavigate} />
            <AutomationsCard summary={summary} onNavigate={onNavigate} />
            <AccountsCard summary={summary} onNavigate={onNavigate} />
          </div>
        </div>
      </div>
    </>
  )
}
