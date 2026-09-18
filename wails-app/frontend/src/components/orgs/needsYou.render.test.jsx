// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within } from '@testing-library/react'
import NeedsYouPanel from './NeedsYouPanel.jsx'
import DecisionsFeed from './DecisionsFeed.jsx'
import { pendingRawItems, joinNeedsYou } from './needsYouModel.js'
import { api, notify } from '../../services/api.js'

vi.mock('../../services/api.js', () => ({
  api: {
    listNeedsYou: vi.fn(),
    getOrgQuestions: vi.fn(),
    getOrgGates: vi.fn(),
    getOrgApprovals: vi.fn(),
    answerOrgQuestion: vi.fn(),
    approveOrgAction: vi.fn(),
    denyOrgAction: vi.fn(),
    gateApproveOrgAction: vi.fn(),
    gateRejectOrgAction: vi.fn(),
    listOrgDecisionLog: vi.fn(),
  },
  notify: vi.fn(),
}))

const NOW = Date.now()
const QUESTIONS = { questions: [{ questionId: 'q-1', role: 'lead', question: 'Which market first?', ts: NOW - 120_000, answer: null }] }
const GATES = { gates: [{ id: 'gate-1', name: 'publish-launch-post', roleId: 'judge', status: 'pending', createdAt: NOW - 30 * 60_000 }] }
const APPROVALS = {
  approvals: [
    { roleId: 'dev', action: 'Bash', ts: NOW - 50_000, approved: null, fingerprint: 'go test ./...' },
    { roleId: 'dev', action: 'Bash', ts: NOW - 40_000, approved: null, fingerprint: 'go vet ./...' },
    { roleId: 'lead', action: 'monoagent__automation_publish_post', ts: NOW - 10_000, approved: null, requestId: 'apr-1' },
    { roleId: 'qa', action: 'Bash', ts: NOW - 99_000, approved: true },
  ],
}
const NEEDS_YOU = {
  v: 1, org: 'growth', items: [
    { kind: 'gate', ref: 'gate-1', requester: 'judge', class: 'gate', tier: 'irreversible', summary: 'judge wants to publish the launch post', waiting_since: new Date(NOW - 30 * 60_000).toISOString(), idle_stop_in_seconds: 240 },
    { kind: 'approval', ref: 'apr-1', requester: 'lead', class: 'grant:publish_post', tier: 'irreversible', summary: 'lead wants to run Publish post', waiting_since: new Date(NOW - 10_000).toISOString(), idle_stop_in_seconds: null },
  ],
}

beforeEach(() => {
  vi.clearAllMocks()
  api.listNeedsYou.mockResolvedValue(NEEDS_YOU)
  api.getOrgQuestions.mockResolvedValue(QUESTIONS)
  api.getOrgGates.mockResolvedValue(GATES)
  api.getOrgApprovals.mockResolvedValue(APPROVALS)
  for (const fn of ['answerOrgQuestion', 'approveOrgAction', 'denyOrgAction', 'gateApproveOrgAction', 'gateRejectOrgAction']) {
    api[fn].mockResolvedValue({ ok: true })
  }
})
afterEach(() => cleanup())

describe('needsYouModel', () => {
  it('groups approvals by role and action and skips resolved ones', () => {
    const raw = pendingRawItems(QUESTIONS, GATES, APPROVALS)
    expect(raw.map(r => r.key)).toEqual(['gate:gate-1', 'question:q-1', 'approval:dev:Bash', 'approval:lead:monoagent__automation_publish_post'])
    const bash = raw.find(r => r.key === 'approval:dev:Bash')
    expect(bash.count).toBe(2)
    expect(bash.since).toBe(NOW - 50_000)
  })

  it('joins needs-you items by gate id, request id, and class', () => {
    const raw = pendingRawItems(QUESTIONS, GATES, APPROVALS)
    const j = joinNeedsYou(NEEDS_YOU, raw)
    expect(j.available).toBe(true)
    expect(j.mine.map(m => m.raw?.key)).toEqual(['gate:gate-1', 'approval:lead:monoagent__automation_publish_post'])
    expect(j.others.map(r => r.key)).toEqual(['question:q-1', 'approval:dev:Bash'])

    const byClass = joinNeedsYou({ items: [{ kind: 'approval', ref: 'dev+Bash', requester: 'dev', class: 'tool:Bash' }] }, raw)
    expect(byClass.mine[0].raw.key).toBe('approval:dev:Bash')
  })

  it('treats every raw item as needing you when needs-you is unavailable', () => {
    const raw = pendingRawItems(QUESTIONS, GATES, APPROVALS)
    const j = joinNeedsYou({ error: 'unknown command' }, raw)
    expect(j.available).toBe(false)
    expect(j.mine).toHaveLength(4)
    expect(j.others).toEqual([])
  })
})

describe('NeedsYouPanel', () => {
  it('lists items routed to you with tier, waited time, and idle-stop countdown', async () => {
    const onCount = vi.fn()
    render(<NeedsYouPanel orgName="growth" onCountChange={onCount} />)
    const items = await screen.findAllByTestId('pending-item')
    expect(items).toHaveLength(4)
    const gate = items[0]
    expect(within(gate).getByText('judge wants to publish the launch post')).toBeInTheDocument()
    expect(within(gate).getByText('irreversible')).toBeInTheDocument()
    expect(within(gate).getByText(/waiting 30m/)).toBeInTheDocument()
    expect(within(gate).getByText(/idle stop in 4m|idle stop in 3m/)).toBeInTheDocument()
    expect(within(items[1]).queryByText(/idle stop/)).not.toBeInTheDocument()
    expect(screen.getByText('Being decided by autonomy (2)')).toBeInTheDocument()
    expect(onCount).toHaveBeenCalledWith(2)
  })

  it('resolves a gate and a grant approval only on click', async () => {
    render(<NeedsYouPanel orgName="growth" />)
    const items = await screen.findAllByTestId('pending-item')
    expect(api.gateApproveOrgAction).not.toHaveBeenCalled()
    expect(api.approveOrgAction).not.toHaveBeenCalled()
    fireEvent.click(within(items[0]).getByRole('button', { name: /Reject/ }))
    await waitFor(() => expect(api.gateRejectOrgAction).toHaveBeenCalledWith('growth', 'gate-1', ''))
    fireEvent.click(within(items[1]).getByRole('button', { name: /Approve/ }))
    await waitFor(() => expect(api.approveOrgAction).toHaveBeenCalledWith('growth', 'lead', 'monoagent__automation_publish_post'))
  })

  it('answers a question with typed text', async () => {
    render(<NeedsYouPanel orgName="growth" />)
    const input = await screen.findByLabelText('Answer')
    fireEvent.change(input, { target: { value: 'EU first' } })
    fireEvent.click(screen.getByRole('button', { name: 'Answer' }))
    await waitFor(() => expect(api.answerOrgQuestion).toHaveBeenCalledWith('growth', 'q-1', 'EU first'))
  })

  it('toasts a failed resolve', async () => {
    api.denyOrgAction.mockResolvedValue({ error: 'No pending approval found' })
    render(<NeedsYouPanel orgName="growth" />)
    const items = await screen.findAllByTestId('pending-item')
    fireEvent.click(within(items[3]).getByRole('button', { name: /Deny/ }))
    await waitFor(() => expect(notify).toHaveBeenCalledWith('org decision', 'No pending approval found'))
  })

  it('falls back to all pending items when needs-you fails', async () => {
    api.listNeedsYou.mockResolvedValue(null)
    const onCount = vi.fn()
    render(<NeedsYouPanel orgName="growth" onCountChange={onCount} />)
    expect(await screen.findByText(/every pending item is listed/)).toBeInTheDocument()
    expect(screen.getAllByTestId('pending-item')).toHaveLength(4)
    expect(onCount).toHaveBeenCalledWith(4)
  })

  it('shows an empty state', async () => {
    api.listNeedsYou.mockResolvedValue({ v: 1, items: [] })
    api.getOrgQuestions.mockResolvedValue({ questions: [] })
    api.getOrgGates.mockResolvedValue({ gates: [] })
    api.getOrgApprovals.mockResolvedValue({ approvals: [] })
    render(<NeedsYouPanel orgName="growth" />)
    expect(await screen.findByText('Nothing needs you.')).toBeInTheDocument()
  })
})

describe('DecisionsFeed', () => {
  const row = (id, verdict, extra = {}) => ({
    id, org: 'growth', run_id: 'run-1', item_kind: 'approval', item_ref: 'dev:Bash', requester: 'dev',
    class: 'tool:Bash', tier: 'routine', level: 'mid', resolver: 'rule', verdict, answer_text: null,
    rationale: null, cost_usd: 0, latency_ms: 12, chain_id: null, created_at: new Date(NOW - Number(id) * 1000).toISOString(), ...extra,
  })

  it('renders rows for all five verdicts with resolver, tier, rationale, cost, latency', async () => {
    api.listOrgDecisionLog.mockResolvedValue({
      v: 1, org: 'growth', decisions: [
        row('1', 'approved'),
        row('2', 'denied', { resolver: 'model:claude-fable-5-1', tier: 'irreversible', class: 'grant:publish_post', rationale: 'Spend over policy limit.', cost_usd: 0.0123, latency_ms: 2400 }),
        row('3', 'answered', { item_kind: 'question', class: 'question', tier: 'consequential', resolver: 'boss:lead', answer_text: 'EU first' }),
        row('4', 'escalated', { class: 'gate', tier: 'irreversible', resolver: 'model:x' }),
        row('5', 'failed', { resolver: 'model:x', rationale: 'decider timed out' }),
      ],
    })
    render(<DecisionsFeed orgName="growth" run="run-1" />)
    const rows = await screen.findAllByTestId('decision-row')
    expect(api.listOrgDecisionLog).toHaveBeenCalledWith('growth', 'run-1')
    expect(rows).toHaveLength(5)
    expect(rows.map(r => within(r).getByText(/^(approved|denied|answered|escalated|failed)$/).textContent))
      .toEqual(['approved', 'denied', 'answered', 'escalated', 'failed'])
    expect(within(rows[1]).getByText('model:claude-fable-5-1')).toBeInTheDocument()
    expect(within(rows[1]).getByText('irreversible')).toBeInTheDocument()
    expect(within(rows[1]).getByText('Spend over policy limit.')).toBeInTheDocument()
    expect(within(rows[1]).getByText('$0.0123')).toBeInTheDocument()
    expect(within(rows[1]).getByText('2s')).toBeInTheDocument()
    expect(within(rows[2]).getByText('Answer: EU first')).toBeInTheDocument()
    expect(within(rows[0]).getByText('rule')).toBeInTheDocument()
  })

  it('filters by verdict and shows empty and error states', async () => {
    api.listOrgDecisionLog.mockResolvedValue({ v: 1, decisions: [row('1', 'approved')] })
    render(<DecisionsFeed orgName="growth" />)
    await screen.findAllByTestId('decision-row')
    fireEvent.change(screen.getByLabelText('Filter by verdict'), { target: { value: 'failed' } })
    expect(screen.getByText('No failed decisions.')).toBeInTheDocument()
    cleanup()

    api.listOrgDecisionLog.mockResolvedValue({ error: 'daemon database locked' })
    render(<DecisionsFeed orgName="growth" />)
    expect(await screen.findByText('daemon database locked')).toBeInTheDocument()
  })
})
