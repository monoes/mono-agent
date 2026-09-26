// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, cleanup, act } from '@testing-library/react'

const handlers = {}
vi.mock('../../services/api.js', () => ({
  api: {
    getSummary: vi.fn(() => Promise.resolve({ v: 1, hil: { total: 2 } })),
    getOrgSummary: vi.fn(fast => Promise.resolve(fast
      ? { v: 1, fast: true, orgs: [{ name: 'acme', needs_you: null }], totals: { orgs: 1 } }
      : { v: 1, fast: false, orgs: [{ name: 'acme', needs_you: 3 }], totals: { orgs: 1, needs_you: 3 } })),
    listWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }])),
    getRecentExecutions: vi.fn(() => Promise.resolve([{ id: 'e1', status: 'SUCCESS' }])),
  },
  subscribeEvent: vi.fn((name, fn) => { handlers[name] = fn; return () => {} }),
}))
vi.mock('../../lib/usePageVisible.js', () => ({
  usePageVisibleRef: () => ({ current: true }), useVisibleCatchUp: () => {},
}))
import { api } from '../../services/api.js'
import { useDashboardData, mergeFast } from './useDashboardData.js'

afterEach(cleanup)

describe('useDashboardData', () => {
  it('loads everything and refreshes on workflow:complete', async () => {
    const { result } = renderHook(() => useDashboardData())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.summary.hil.total).toBe(2)
    expect(result.current.workflows).toHaveLength(1)
    expect(result.current.executions).toHaveLength(1)
    expect(api.getOrgSummary).toHaveBeenCalledWith(true)
    expect(api.getOrgSummary).toHaveBeenCalledWith(false)
    await waitFor(() => expect(result.current.orgs?.totals?.needs_you).toBe(3))
    const before = api.getSummary.mock.calls.length
    await act(async () => { handlers['workflow:complete']() })
    await waitFor(() => expect(api.getSummary.mock.calls.length).toBe(before + 1))
  })
})

describe('mergeFast', () => {
  it('keeps needs_you from the last full reply', () => {
    const prev = { orgs: [{ name: 'a', needs_you: 2 }, { name: 'b', needs_you: null, needs_you_error: 'x' }] }
    const fast = { fast: true, orgs: [{ name: 'a', queued: 5, needs_you: null }, { name: 'b', needs_you: null }, { name: 'c', needs_you: null }], totals: { orgs: 3, queued: 5 } }
    const m = mergeFast(prev, fast)
    expect(m.orgs.map(o => o.needs_you)).toEqual([2, null, null])
    expect(m.orgs[0].queued).toBe(5)
    expect(m.orgs[1].needs_you_error).toBe('x')
    expect(m.totals).toEqual({ orgs: 3, queued: 5, needs_you: 2 })
  })
})
