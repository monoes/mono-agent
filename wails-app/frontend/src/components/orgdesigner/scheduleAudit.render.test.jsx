// @vitest-environment jsdom
// Schedule audit list (#342) and the designer's schedule control. The audit
// entries are the Go view of monomind 2.24.1's recorded line plus two
// a synthetic unknown event; the recorded refusal and skipped views follow.
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'

let busListener = null
vi.mock('../../services/api.js', () => ({
  api: { getOrgScheduleAudit: vi.fn() },
  onOrgEvent: vi.fn((cb) => { busListener = cb; return () => { busListener = null } }),
}))
import { api } from '../../services/api.js'
import recorded from './__fixtures__/schedule-audit-view.json'
import recordedSkipped from './__fixtures__/schedule-audit-skipped-view.json'
import ScheduleAuditPanel from './ScheduleAuditPanel.jsx'
import ScheduleControl, { FRESH_RUN_NOTE, SECTIONS_NOTE } from './ScheduleControl.jsx'

const view = {
  v: 1, org: 'sched', total: 4,
  entries: [
    { ts: 4, at: '2026-10-05T18:50:00Z', event: 'scheduled-from-the-future', kind: 'other', msg: 'new' },
    { ts: 3, at: '2026-10-05T18:49:00Z', event: 'scheduled-start-refused', kind: 'refused', msg: 'preflight: host runtime is not set up' },
    { ts: 2, at: '2026-10-05T18:48:00Z', event: 'scheduled-tick-skipped', kind: 'skipped', msg: 'a run of "sched" is already live — this tick yields' },
    { ts: 1, at: '2026-10-05T18:47:19Z', event: 'scheduled-tick-deferred', kind: 'coalesced', msg: 'a tick landed while "sched" was still running — held for one catch-up run' },
  ],
}
beforeEach(() => { vi.clearAllMocks(); busListener = null; api.getOrgScheduleAudit.mockResolvedValue(view) })
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('ScheduleAuditPanel', () => {
  it('lists refused, skipped and coalesced ticks with their reasons, and an unknown event by name', async () => {
    render(<ScheduleAuditPanel orgName="sched" />)
    expect(await screen.findByText('preflight: host runtime is not set up')).toBeInTheDocument()
    for (const l of ['refused', 'skipped', 'coalesced', 'scheduled-from-the-future']) expect(screen.getByText(l)).toBeInTheDocument()
    expect(screen.getByText(/held for one catch-up run/)).toBeInTheDocument()
  })

  it('renders nothing when there are no entries or the read fails', async () => {
    api.getOrgScheduleAudit.mockResolvedValue({ v: 1, org: 'sched', entries: [], total: 0 })
    const { container } = render(<ScheduleAuditPanel orgName="sched" />)
    await waitFor(() => expect(api.getOrgScheduleAudit).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
    cleanup()
    api.getOrgScheduleAudit.mockResolvedValue({ error: 'boom' })
    const r = render(<ScheduleAuditPanel orgName="sched" />)
    await waitFor(() => expect(api.getOrgScheduleAudit).toHaveBeenCalledTimes(2))
    expect(r.container).toBeEmptyDOMElement()
  })

  it('reloads refused ticks without a run bus and stops polling when closed', async () => {
    vi.useFakeTimers()
    api.getOrgScheduleAudit.mockResolvedValue({ entries: [] })
    const { unmount } = render(<ScheduleAuditPanel orgName="sched" />)
    await act(async () => {})
    api.getOrgScheduleAudit.mockResolvedValue(view)
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(screen.getByText(/preflight: host runtime/)).toBeInTheDocument()
    expect(api.getOrgScheduleAudit).toHaveBeenCalledTimes(2)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(api.getOrgScheduleAudit).toHaveBeenCalledTimes(2)
  })

  it('ignores a response from the previously selected org', async () => {
    let finishOld
    api.getOrgScheduleAudit.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
    const { rerender } = render(<ScheduleAuditPanel orgName="old" />)
    api.getOrgScheduleAudit.mockResolvedValue({ entries: [] })
    rerender(<ScheduleAuditPanel orgName="new" />)
    await act(async () => {})
    await act(async () => { finishOld(view) })
    expect(screen.queryByText(/preflight: host runtime/)).not.toBeInTheDocument()
  })

  it('reloads when a live scheduled-* audit event arrives, and ignores other events', async () => {
    render(<ScheduleAuditPanel orgName="sched" live />)
    await screen.findByText(/preflight/)
    api.getOrgScheduleAudit.mockClear()
    act(() => busListener({ orgName: 'sched', event: { type: 'audit', reason: 'idle-nudge' } }))
    act(() => busListener({ orgName: 'other', event: { type: 'audit', reason: 'scheduled-tick-skipped' } }))
    expect(api.getOrgScheduleAudit).not.toHaveBeenCalled()
    act(() => busListener({ orgName: 'sched', event: { type: 'audit', reason: 'scheduled-tick-skipped' } }))
    expect(api.getOrgScheduleAudit).toHaveBeenCalledTimes(1)
  })
})

describe('ScheduleAuditPanel on recorded output', () => {
  it('shows the forced preflight refusal monomind 2.24.1 wrote (view of schedule-audit-refused.jsonl)', async () => {
    api.getOrgScheduleAudit.mockResolvedValue(recorded)
    render(<ScheduleAuditPanel orgName="sec" />)
    expect((await screen.findAllByText('refused')).length).toBe(2)
    expect(screen.getAllByText(/cannot start on this host: the authority mask \(bubblewrap\) is unavailable/).length).toBe(2)
  })
})

describe('ScheduleAuditPanel on a recorded skipped tick', () => {
  it('shows ticks that yielded to a live run and the one held for a catch-up run', async () => {
    api.getOrgScheduleAudit.mockResolvedValue(recordedSkipped)
    render(<ScheduleAuditPanel orgName="sec" />)
    expect((await screen.findAllByText('skipped')).length).toBe(4)
    expect(screen.getAllByText('a run of "sec" is already live — this tick yields').length).toBe(4)
    expect(screen.getByText('coalesced')).toBeInTheDocument()
  })
})

describe('ScheduleControl', () => {
  it('lets a sections org be scheduled and explains the fresh run per tick', async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true })
    render(<ScheduleControl schedule={null} sections onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: /Schedule/ }))
    expect(screen.getByTestId('schedule-note')).toHaveTextContent(FRESH_RUN_NOTE)
    expect(screen.getByTestId('schedule-note')).toHaveTextContent(SECTIONS_NOTE)
    fireEvent.change(screen.getByLabelText('Interval'), { target: { value: '30m' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith('30m'))
  })

  it('shows the interval, keeps the dialog open on an error, and clears', async () => {
    const onSave = vi.fn().mockResolvedValueOnce({ error: 'use an interval such as 30s, 15m or 2h' }).mockResolvedValue({ ok: true })
    render(<ScheduleControl schedule="15m" onSave={onSave} />)
    fireEvent.click(screen.getByRole('button', { name: /Every 15m/ }))
    expect(screen.getByTestId('schedule-note')).not.toHaveTextContent(SECTIONS_NOTE)
    fireEvent.change(screen.getByLabelText('Interval'), { target: { value: 'daily' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('use an interval')
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }))
    await waitFor(() => expect(onSave).toHaveBeenLastCalledWith(''))
  })
})
