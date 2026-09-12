// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'

// Mocked the same way useChatStream.test.jsx mocks services/api.js — a
// small per-method vi.fn() rather than the whole api surface, so each test
// only wires the one or two methods resolveArtifact actually calls for the
// artifact type under test. detectArtifactCandidate/resolveArtifact
// themselves are NOT mocked — chatArtifacts.test.js already covers them
// exhaustively; these tests exercise the hook's own caching/retry
// behavior on top of the real pure functions.
const getWorkflow = vi.fn()
const listOrgDesigns = vi.fn()
const getProfileDocument = vi.fn()
vi.mock('../../services/api.js', () => ({
  api: {
    getWorkflow: (...args) => getWorkflow(...args),
    listOrgDesigns: (...args) => listOrgDesigns(...args),
    getProfileDocument: (...args) => getProfileDocument(...args),
  },
}))

import { useResolvedArtifacts } from './useResolvedArtifacts.js'

function call(overrides) {
  return { callId: 'c1', name: 'create_workflow', status: 'completed', ok: true, result: JSON.stringify({ workflow_id: 'wf-1' }), ...overrides }
}

beforeEach(() => {
  getWorkflow.mockReset()
  listOrgDesigns.mockReset()
  getProfileDocument.mockReset()
})

describe('useResolvedArtifacts', () => {
  it('resolves a completed call and caches it under "turnId:callId"', async () => {
    getWorkflow.mockResolvedValue({ id: 'wf-1', name: 'My Workflow' })
    const { result } = renderHook(() => useResolvedArtifacts([{ turnId: 't1', call: call() }]))

    await waitFor(() => expect(result.current['t1:c1']).toEqual({ type: 'workflow', id: 'wf-1', name: 'My Workflow' }))
    expect(getWorkflow).toHaveBeenCalledWith('wf-1')
  })

  it('does not resolve or look up a call that is still running', async () => {
    const { result } = renderHook(() => useResolvedArtifacts([{ turnId: 't1', call: call({ status: 'started', result: null }) }]))
    await new Promise(r => setTimeout(r, 0))
    expect(result.current['t1:c1']).toBeUndefined()
    expect(getWorkflow).not.toHaveBeenCalled()
  })

  it('caches null immediately, with no backend lookup, for a completed call with no artifact candidate', async () => {
    const { result } = renderHook(() => useResolvedArtifacts([{ turnId: 't1', call: call({ name: 'list_vault_items' }) }]))
    await waitFor(() => expect(result.current['t1:c1']).toBeNull())
    expect(getWorkflow).not.toHaveBeenCalled()
  })

  it('keys the cache by the (turnId, callId) pair, not callId alone — two turns sharing a callId resolve independently', async () => {
    getWorkflow.mockResolvedValue({ id: 'wf-1', name: 'Workflow One' })
    listOrgDesigns.mockResolvedValue([{ name: 'Acme' }])
    const entries = [
      { turnId: 'turn-a', call: call({ callId: 'c1', name: 'create_workflow', result: JSON.stringify({ workflow_id: 'wf-1' }) }) },
      { turnId: 'turn-b', call: call({ callId: 'c1', name: 'create_org', result: JSON.stringify({ org_name: 'Acme' }) }) },
    ]
    const { result } = renderHook(() => useResolvedArtifacts(entries))

    await waitFor(() => {
      expect(result.current['turn-a:c1']).toEqual({ type: 'workflow', id: 'wf-1', name: 'Workflow One' })
      expect(result.current['turn-b:c1']).toEqual({ type: 'org', name: 'Acme' })
    })
  })

  it('retries exactly once after a transient (null) failure and resolves on the second attempt', async () => {
    getProfileDocument.mockResolvedValueOnce(null).mockResolvedValueOnce({ id: 'doc-1', filename: 'a.md', path: '/x/a.md', size_bytes: 10 })
    const docCall = call({ name: 'save_document', result: JSON.stringify({ vault_document_id: 'doc-1' }) })
    const { result } = renderHook(() => useResolvedArtifacts([{ turnId: 't1', call: docCall }]))

    await waitFor(() => expect(getProfileDocument).toHaveBeenCalledTimes(2), { timeout: 2000 })
    await waitFor(() => expect(result.current['t1:c1']).toEqual({ type: 'document', id: 'doc-1', filename: 'a.md', path: '/x/a.md', sizeBytes: 10 }))
  })

  it('gives up after exactly one retry and permanently caches null — no third lookup even across later re-renders', async () => {
    getProfileDocument.mockResolvedValue(null)
    const docCall = call({ name: 'save_document', result: JSON.stringify({ vault_document_id: 'doc-gone' }) })
    const { result, rerender } = renderHook(({ entries }) => useResolvedArtifacts(entries), { initialProps: { entries: [{ turnId: 't1', call: docCall }] } })

    await waitFor(() => expect(getProfileDocument).toHaveBeenCalledTimes(2), { timeout: 2000 })
    await waitFor(() => expect(result.current['t1:c1']).toBeNull())

    rerender({ entries: [{ turnId: 't1', call: docCall }] })
    rerender({ entries: [{ turnId: 't1', call: docCall }] })
    expect(getProfileDocument).toHaveBeenCalledTimes(2)
  })

  it('cancels a pending retry on unmount — the retry lookup never fires after the component is gone', async () => {
    getProfileDocument.mockResolvedValue(null)
    const docCall = call({ name: 'save_document', result: JSON.stringify({ vault_document_id: 'doc-x' }) })
    const { unmount } = renderHook(() => useResolvedArtifacts([{ turnId: 't1', call: docCall }]))

    await waitFor(() => expect(getProfileDocument).toHaveBeenCalledTimes(1))
    // Give the first attempt's .then() handler time to actually schedule the
    // retry timer (a microtask after the mocked promise resolves) before
    // unmounting — otherwise this could pass trivially without ever
    // exercising the cleanup effect's clearTimeout call.
    await new Promise(r => setTimeout(r, 50))
    unmount()

    // Past the 300ms retry delay: if the pending timer weren't cleared, a
    // second lookup would have fired by now.
    await new Promise(r => setTimeout(r, 500))
    expect(getProfileDocument).toHaveBeenCalledTimes(1)
  })
})
