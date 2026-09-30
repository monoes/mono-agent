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
const row = id => document.querySelector(`[data-row="${id}"]`)

beforeEach(() => {
  vi.clearAllMocks()
  api.agentRoster.mockResolvedValue(roster)
  api.startAgentValidation.mockResolvedValue({ ok: true })
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

  // #231 phase 0: "Validate all" shows the plan's size and cost first, then
  // every row ticks live as its line arrives, and the progress bar follows.
  it('shows the calls and cost before validating all, then ticks every row live', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 3, est_cost_usd: 0.004, unknown_cost: 1,
      targets: [{ runtime: 'claude', model: 'haiku' }, { runtime: 'claude', model: 'opus' }, { runtime: 'crush', model: 'mine' }] } })
    mockConfirm.mockResolvedValue(true)
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/haiku')).toBeTruthy())
    fireEvent.click(screen.getByText('Validate all'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith([], [], false))
    expect(api.agentValidatePlan).toHaveBeenCalledWith([], [], false)
    const body = render(mockConfirm.mock.calls[0][0]).container.textContent
    expect(body).toContain('3 real model calls across 2 runtime(s)')
    expect(body).toContain('≈ $0.0040')
    expect(body).toContain('1 with unknown cost')

    const bar = () => screen.getByRole('progressbar')
    expect(bar()).toHaveAttribute('aria-valuemax', '3')
    act(() => listeners.line({ type: 'validate.started', target: { runtime: 'claude', model: 'opus' } }))
    act(() => listeners.line({ type: 'validate.started', target: { runtime: 'crush', model: 'mine' } }))
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveAttribute('data-tone', 'testing')
    expect(within(row('crush/mine')).getByTestId('chip')).toHaveAttribute('data-tone', 'testing')

    act(() => listeners.line({ type: 'validate.result', result: { runtime: 'crush', model: 'mine', status: 'quota' } }))
    expect(within(row('crush/mine')).getByTestId('chip')).toHaveTextContent('quota')
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveAttribute('data-tone', 'testing')
    expect(bar()).toHaveAttribute('aria-valuenow', '1')

    act(() => listeners.line({ type: 'validate.result', result: { runtime: 'claude', model: 'opus', status: 'ok', latency_ms: 700 } }))
    expect(within(row('claude/opus')).getByTestId('chip')).toHaveAttribute('data-tone', 'ok')
    expect(bar()).toHaveAttribute('aria-valuenow', '2')

    // A row the plan found that the stored roster did not have yet appears live.
    act(() => listeners.line({ type: 'validate.result', result: { runtime: 'codex', model: 'gpt-x', status: 'ok' } }))
    expect(row('codex/gpt-x')).toBeTruthy()

    act(() => listeners.line({ type: 'validate.done', summary: { ok: 2, failed: 1, cancelled: 0, planned: 3 } }))
    expect(screen.getByText('2 ok, 1 failed, 0 cancelled')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
  })

  it('re-validates one runtime from its card', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 1, targets: [{ runtime: 'crush', model: 'mine' }] } })
    render(<AgentRoster />)
    await waitFor(() => expect(row('crush/mine')).toBeTruthy())
    const card = document.querySelector('[data-roster-runtime="crush"]')
    fireEvent.click(within(card).getByText('Validate'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith(['crush'], [], false))
  })

  it('marks a model stale after a runtime update and re-checks only stale ones', async () => {
    api.agentRoster.mockResolvedValue({ v: 1, runtimes: [
      { runtime: 'codex', installed: true, version: '0.61', ready: 0, models: [
        { runtime: 'codex', model: 'gpt-x', state: 'stale', stale_reason: 'version', status: 'ok', runtime_version: '0.60', validated_at: new Date().toISOString(), source: 'listed' },
      ] },
    ] })
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 1, targets: [{ runtime: 'codex', model: 'gpt-x' }] } })
    render(<AgentRoster />)
    await waitFor(() => expect(row('codex/gpt-x')).toBeTruthy())
    const chip = within(row('codex/gpt-x')).getByTestId('chip')
    expect(chip).toHaveAttribute('data-tone', 'warn')
    expect(chip).toHaveTextContent('runtime updated')
    expect(screen.getByText('0 ready')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Re-check stale'))
    await waitFor(() => expect(api.startAgentValidation).toHaveBeenCalledWith([], [], true))
  })

  it('stops a running validation and reloads the roster when the process ends', async () => {
    api.agentValidatePlan.mockResolvedValue({ type: 'validate.plan', plan: { calls: 1, targets: [{ runtime: 'claude', model: 'opus' }] } })
    api.stopAgentValidation.mockResolvedValue({ ok: true })
    render(<AgentRoster />)
    await waitFor(() => expect(row('claude/opus')).toBeTruthy())
    fireEvent.click(within(row('claude/opus')).getByLabelText('Validate this model again'))
    fireEvent.click(await screen.findByText('Stop'))
    expect(api.stopAgentValidation).toHaveBeenCalled()
    const loads = api.agentRoster.mock.calls.length
    act(() => listeners.closed({ ok: false, error: 'validation cancelled after 0 of 1 tests' }))
    expect(screen.getByText('validation cancelled after 0 of 1 tests')).toBeInTheDocument()
    await waitFor(() => expect(api.agentRoster.mock.calls.length).toBe(loads + 1))
    expect(screen.getByText('Validate all')).toBeInTheDocument()
  })

  it('shows a roster error', async () => {
    api.agentRoster.mockResolvedValue({ error: 'monomind not found' })
    render(<AgentRoster />)
    expect(await screen.findByText('monomind not found')).toBeInTheDocument()
  })
})
