// What the "Needs you" strip shows: derived only from CLI output (summary,
// org summary, the shared health report) — ordering and labels, no facts.
const RANK = { danger: 0, warn: 1, info: 2 }

export function attentionItems(summary, orgs, health) {
  const s = summary || {}
  const items = []
  const add = (id, count, severity, target) => {
    if (count > 0) items.push({ id, count, severity, labelKey: `dashboard.attention.${id}`, target })
  }
  const scheduled = (s.schedules?.upcoming || []).length
  add('daemonOffline', s.schedules && !s.schedules.daemon_running ? scheduled : 0, 'danger', { page: 'settings', data: { section: 'health' } })
  add('failedRuns', s.executions?.last_24h?.failed || 0, 'danger', { page: 'noderunner' })
  const firstBroken = s.automations?.broken?.[0]?.automation_id
  add('brokenSelectors', s.automations?.selectors?.broken || 0, 'danger',
    { page: 'connections', data: firstBroken ? { automationId: firstBroken, tab: 'health' } : undefined })
  add('expiredLogins', s.accounts?.expired || 0, 'danger', { page: 'connections' })
  add('hilApprovals', s.hil?.workflow_pending || 0, 'warn', { hil: true })
  add('orgNeedsYou', orgs?.totals?.needs_you || 0, 'warn', { page: 'orgs' })
  add('leadsToReview', s.hil?.people_review || 0, 'warn', { hil: true })
  add('drafts', s.hil?.drafts || 0, 'warn', { hil: true })
  add('expiringLogins', s.accounts?.expiring_soon || 0, 'warn', { page: 'connections' })
  const healthIssues = health && health.level !== 'ok' ? health.issues || 0 : 0
  add('health', healthIssues, health?.level === 'broken' ? 'danger' : 'warn', { page: 'settings', data: { section: 'health' } })
  add('unreadMessages', s.activity?.messages_unread || 0, 'info', { page: 'communications' })
  add('linkSuggestions', s.hil?.link_suggestions || 0, 'info', { page: 'people' })
  add('unsavedRecordings', s.recordings?.unsaved || 0, 'info', { page: 'connections' })
  add('automationUpdates', s.automations?.pending_update || 0, 'info', { page: 'connections' })
  add('unevaluatedApplications', s.applications?.unevaluated_pending || 0, 'info', { page: 'applications' })
  // Stable sort: declaration order within a severity.
  return items.sort((a, b) => RANK[a.severity] - RANK[b.severity])
}

// Sections the strip counts from; one that reported an error is unknown,
// not zero, so "All clear" must not be claimed while it is unread.
const COUNTED = ['schedules', 'executions', 'automations', 'accounts', 'hil', 'recordings', 'applications']

export function unreadSections(summary) {
  if (!summary) return []
  return COUNTED.filter(k => summary[k]?.error)
}
