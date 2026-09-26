import { describe, it, expect } from 'vitest'
import { attentionItems } from './attention.js'

describe('attentionItems', () => {
  it('returns only non-zero items, most severe first', () => {
    const summary = {
      hil: { workflow_pending: 2, people_review: 3, drafts: 0, link_suggestions: 1 },
      executions: { last_24h: { failed: 1 } },
      automations: { selectors: { broken: 1 }, unavailable: 0, pending_update: 2 },
      accounts: { expired: 1, expiring_soon: 0 },
      schedules: { daemon_running: false, upcoming: [{}] },
      recordings: { unsaved: 0 },
      applications: { unevaluated_pending: 0 },
    }
    const items = attentionItems(summary, { totals: { needs_you: 4 } }, { level: 'issues', issues: 2 })
    expect(items.map(i => i.id)).toEqual(['daemonOffline', 'failedRuns', 'brokenSelectors', 'expiredLogins',
      'hilApprovals', 'orgNeedsYou', 'leadsToReview', 'health', 'linkSuggestions', 'automationUpdates'])
    expect(items.find(i => i.id === 'hilApprovals').target).toEqual({ hil: true })
    expect(items.find(i => i.id === 'brokenSelectors').target).toEqual({ page: 'connections', data: { tab: 'health' } })
    expect(items.find(i => i.id === 'health').target).toEqual({ page: 'settings', data: { section: 'health' } })
    expect(items.find(i => i.id === 'orgNeedsYou').count).toBe(4)
  })
  it('a broken health report is danger', () => {
    const [h] = attentionItems({}, null, { level: 'broken', issues: 1 })
    expect(h).toMatchObject({ id: 'health', severity: 'danger' })
  })
  it('is empty when everything is fine or data is missing', () => {
    expect(attentionItems(null, null, null)).toEqual([])
    expect(attentionItems({ schedules: { daemon_running: false, upcoming: [] } }, null, { level: 'ok', issues: 0 })).toEqual([])
  })
})
