// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup, within, act } from '@testing-library/react'
import '../../i18n.js'

const { listeners, api } = vi.hoisted(() => ({
  listeners: {},
  api: {
    agentRoster: vi.fn(),
    agentValidatePlan: vi.fn(),
    startAgentValidation: vi.fn(),
    stopAgentValidation: vi.fn(),
    agentRosterAdd: vi.fn(),
    agentRosterRemove: vi.fn(),
    agentRosterAutoRevalidate: vi.fn(),
    setAgentRosterAutoRevalidate: vi.fn(),
  },
}))
vi.mock('../../services/api.js', () => ({
  api,
  onAgentValidate: cb => { listeners.line = cb; return () => {} },
  onAgentValidateClosed: cb => { listeners.closed = cb; return () => {} },
}))
const mockConfirm = vi.fn()
vi.mock('../ConfirmDialog.jsx', () => ({ confirm: (...a) => mockConfirm(...a) }))

import AgentRoster from './AgentRoster.jsx'

const roster = {
  v: 1,
  runtimes: [
    { runtime: 'claude', installed: true, version: '2.1', login_hint: 'claude /login', ready: 1, models: [
      { runtime: 'claude', model: 'haiku', label: 'Haiku 4.5', state: 'ready', status: 'ok', latency_ms: 1400, validated_at: new Date().toISOString(), source: 'listed' },
      { runtime: 'claude', model: 'opus', label: 'Opus', state: 'failed', status: 'auth', detail: 'Not logged in', validated_at: new Date().toISOString(), source: 'listed' },
    ] },
    { runtime: 'crush', installed: true, ready: 0, models: [
      { runtime: 'crush', model: 'mine', label: 'mine', state: 'untested', status: 'untested', source: 'manual', validated_at: '0001-01-01T00:00:00.000Z' },
    ] },
  ],
}
const autoOff = {
  enabled: false, max_runtimes_per_day: 1, max_models_per_run: 3, quiet_period: '15m0s', daemon_running: true,
  daily_max_usd: 0.15, priciest_model_usd: 0.05, cost_known: true,
  state: { runtimes_today: 0, spent_today_usd: 0, last_run_at: '0001-01-01T00:00:00Z', next_eligible_at: '0001-01-01T00:00:00Z' },
  next: { runtime: 'codex', targets: [{ model: 'a' }, { model: 'b' }], est_cost_usd: 0.0021, unknown_cost: 1 },
}
const row = id => document.querySelector(`[data-row="${id}"]`)

beforeEach(() => {
  vi.clearAllMocks()
  api.agentRoster.mockResolvedValue(roster)
  api.startAgentValidation.mockResolvedValue({ ok: true })
  api.agentRosterAutoRevalidate.mockResolvedValue(autoOff)
})
afterEach(cleanup)

describe('AgentRoster', () => {
  it('shows each model with its status and the sign-in hint for a failed login', async () => {
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/haiku')).toBeTruthy())
    expect(within(row('claude/haiku')).getByTestId('chip')).toHaveAttribute('data-tone', 'ok')
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveTextContent('sign in needed')
    expect(screen.getByText('sign in: claude /login')).toBeInTheDocument()
    expect(within(row('crush/mine')).getByText('never')).toBeInTheDocument()
    expect(within(row('crush/mine')).getByLabelText('Remove from roster')).toBeInTheDocument()
  })

  it('asks before a multi-call run, then starts it', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 3, unknown_cost: 3, targets: [{ runtime: 'claude' }] } })
    mockConfirm.mockResolvedValue(true)
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/haiku')).toBeTruthy())
    fireEvent.click(screen.getByText('Validate all'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith([], [], false))
    expect(mockConfirm).toHaveBeenCalledTimes(1)
  })

  it('does not run when the confirmation is declined', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 5, targets: [] } })
    mockConfirm.mockResolvedValue(false)
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/haiku')).toBeTruthy())
    fireEvent.click(screen.getByText('Validate all'))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalled())
    expect(api.startAgentValidation).not.toHaveBeenCalled()
  })

  it('re-validates one model without asking, and updates the row live', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 1, targets: [{ runtime: 'claude', model: 'opus' }] } })
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/opus')).toBeTruthy())
    fireEvent.click(within(row('claude/opus')).getByLabelText('Validate this model again'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith(['claude'], ['opus'], false))
    expect(mockConfirm).not.toHaveBeenCalled()

    act(() => listeners.line({ type: 'validate.started', target: { runtime: 'claude', model: 'opus' } }))
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveAttribute('data-tone', 'testing')
    act(() => listeners.line({ type: 'validate.result', result: { runtime: 'claude', model: 'opus', status: 'ok', latency_ms: 800 } }))
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveAttribute('data-tone', 'ok')
    expect(screen.getByText('Stop')).toBeInTheDocument()
    act(() => listeners.line({ type: 'validate.done', summary: { ok: 1, failed: 0, cancelled: 0, planned: 1 } }))
    expect(screen.getByText('1 ok, 0 failed, 0 cancelled')).toBeInTheDocument()
  })

  it('adds a model by hand and validates it', async () => {
    api.agentRosterAdd.mockResolvedValue({ added: true })
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 1, targets: [] } })
    render(<AgentRoster />)
    await waitFor(() => expect(row('crush/mine')).toBeTruthy())
    const card = document.querySelector('[data-roster-runtime="crush"]')
    fireEvent.change(within(card).getByLabelText("Add a model id this runtime doesn't list"), { target: { value: 'glm-5' } })
    fireEvent.click(within(card).getByText('Add'))
    await waitFor(() => expect(api.agentRosterAdd).toHaveBeenCalledWith('crush', 'glm-5'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith(['crush'], ['glm-5'], false))
  })

  it('shows a roster error', async () => {
    api.agentRoster.mockResolvedValue({ error: 'monomind not found' })
    render(<AgentRoster />)
    expect(await screen.findByText('monomind not found')).toBeInTheDocument()
  })

  it('shows automatic re-validation off by default with the next run\'s cost', async () => {
    render(<AgentRoster />)
    const box = await screen.findByLabelText(/Re-check stale models automatically/)
    expect(box).not.toBeChecked()
    const panel = screen.getByTestId('auto-revalidate')
    expect(within(panel).getByText(/costs money/)).toBeInTheDocument()
    expect(within(panel).getByText('Next run: 2 model(s) of codex, ≈ $0.0021 (+ 1 with unknown cost)')).toBeInTheDocument()
    expect(within(panel).getByText('Daily ceiling: up to 1 run(s) × 3 model(s), ≈ $0.1500/day at most (priciest known model ≈ $0.0500).')).toBeInTheDocument()
  })

  it('asks before turning automatic re-validation on, and stays off when declined', async () => {
    mockConfirm.mockResolvedValue(false)
    render(<AgentRoster />)
    fireEvent.click(await screen.findByLabelText(/Re-check stale models automatically/))
    await waitFor(() => expect(mockConfirm).toHaveBeenCalledTimes(1))
    render(mockConfirm.mock.calls[0][0])
    expect(screen.getAllByText(/≈ \$0\.1500\/day at most/).length).toBeGreaterThan(1)
    expect(api.setAgentRosterAutoRevalidate).not.toHaveBeenCalled()
  })

  it('turns automatic re-validation on after confirming, and off without asking', async () => {
    mockConfirm.mockResolvedValue(true)
    api.setAgentRosterAutoRevalidate.mockImplementation(async on => ({ ...autoOff, enabled: on }))
    render(<AgentRoster />)
    const box = await screen.findByLabelText(/Re-check stale models automatically/)
    fireEvent.click(box)
    await waitFor(() => expect(api.setAgentRosterAutoRevalidate).toHaveBeenCalledWith(true))
    await waitFor(() => expect(box).toBeChecked())
    fireEvent.click(box)
    await waitFor(() => expect(api.setAgentRosterAutoRevalidate).toHaveBeenCalledWith(false))
    expect(mockConfirm).toHaveBeenCalledTimes(1)
  })
})
