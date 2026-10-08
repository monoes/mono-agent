// @vitest-environment jsdom
// Documents panel (#339), rendered from the Go view of the scripted run
// (internal/orgbridge/testdata/documents-synthetic): documents, one accepted
// rework cycle and the exhausted-cap case, then live reloads from the bus.
import React from 'react'
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, waitFor, within, act } from '@testing-library/react'
import view from './__fixtures__/documents-view.json'
import busRaw from './__fixtures__/documents-bus.ndjson?raw'

let busListener = null
vi.mock('../../services/api.js', () => ({
  api: { getOrgDocuments: vi.fn() },
  onOrgEvent: vi.fn((cb) => { busListener = cb; return () => { busListener = null } }),
}))
import { api } from '../../services/api.js'
import DocumentsPanel from './DocumentsPanel.jsx'

const bus = busRaw.trim().split('\n').map(l => JSON.parse(l))
beforeEach(() => { vi.clearAllMocks(); busListener = null; api.getOrgDocuments.mockResolvedValue(view) })
afterEach(() => { cleanup(); vi.useRealTimers() })

const card = (id) => document.querySelector(`[data-doc="${id}"]`)

describe('DocumentsPanel', () => {
  it('lists documents by section and type with producer to consumer', async () => {
    render(<DocumentsPanel orgName="rework" />)
    expect(await screen.findByLabelText('Section drafting')).toBeInTheDocument()
    expect(api.getOrgDocuments).toHaveBeenCalledWith('rework', '')
    const c = card('note-4')
    expect(within(c).getAllByText('published')).toHaveLength(2) // status chip and the version line
    expect(within(c).getByText('writer')).toBeInTheDocument()
    expect(within(c).getByText(/waiting on review/)).toBeInTheDocument()
    expect(within(c).getByText('out/note.json')).toBeInTheDocument()
  })

  it('shows one rework cycle: round 1 of 2, the rejection reason and the accepted revision', async () => {
    render(<DocumentsPanel orgName="rework" />)
    await screen.findByLabelText('Section drafting')
    const c = card('note-1')
    expect(within(c).getByText('round 1 of 2')).toBeInTheDocument()
    expect(within(c).getByText('rejected, superseded by v2')).toBeInTheDocument()
    expect(within(c).getByText(/disagrees with the evidence/)).toBeInTheDocument()
    expect(within(c).getByText('replaces v1')).toBeInTheDocument()
    expect(within(c).getAllByText('accepted').length).toBeGreaterThan(0)
    expect(within(c).queryByText(/cap hit/i)).toBeNull()
  })

  it('badges the exhausted cap and the frozen thread', async () => {
    render(<DocumentsPanel orgName="rework" />)
    await screen.findByLabelText('Section drafting')
    const c = card('note-2')
    expect(within(c).getByRole('status')).toHaveTextContent('Rework cap hit · frozen')
    expect(within(c).getByText('round 2 of 2')).toBeInTheDocument()
    expect(screen.getByText('1 cap hit')).toBeInTheDocument()
  })

  it('shows a root override as thawed, with no cap badge', async () => {
    render(<DocumentsPanel orgName="rework" />)
    await screen.findByLabelText('Section drafting')
    const c = card('note-3')
    expect(within(c).getByText(/root override/)).toBeInTheDocument()
    expect(within(c).queryByRole('status')).toBeNull()
  })

  it('reloads once for a burst of document events, ignoring unknown kinds and loops', async () => {
    vi.useFakeTimers()
    render(<DocumentsPanel orgName="rework" live />)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getOrgDocuments).toHaveBeenCalledTimes(1)
    act(() => { for (const event of bus) busListener({ orgName: 'rework', event }) })
    act(() => { busListener({ orgName: 'other', event: bus[2] }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(api.getOrgDocuments).toHaveBeenCalledTimes(2)
    // only the unknown kinds and loops: no reload, no crash
    act(() => { busListener({ orgName: 'rework', event: bus[4] }); busListener({ orgName: 'rework', event: bus[5] }); busListener({ orgName: 'rework', event: null }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(400) })
    expect(api.getOrgDocuments).toHaveBeenCalledTimes(2)
  })

  it('does not subscribe to the stream for a past run', async () => {
    render(<DocumentsPanel orgName="rework" run="run-syn" />)
    await screen.findByLabelText('Section drafting')
    expect(api.getOrgDocuments).toHaveBeenCalledWith('rework', 'run-syn')
    expect(busListener).toBeNull()
  })

  it('shows an error and an empty run without crashing', async () => {
    api.getOrgDocuments.mockResolvedValueOnce({ error: 'boom' })
    const { unmount } = render(<DocumentsPanel orgName="rework" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('boom')
    unmount()
    api.getOrgDocuments.mockResolvedValueOnce({ v: 1, docs: [], sections: [], summary: { documents: 0 }, runs: [] })
    render(<DocumentsPanel orgName="rework" />)
    expect(await screen.findByText(/published no documents/)).toBeInTheDocument()
  })

  it('warns when the log failed its integrity check', async () => {
    api.getOrgDocuments.mockResolvedValueOnce({ ...view, integrity: 'event 5 does not chain' })
    render(<DocumentsPanel orgName="rework" />)
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('event 5 does not chain'))
  })
})
