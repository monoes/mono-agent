// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, cleanup } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({
  api: {
    getSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'profile' })),
    getGlobalSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'global', profiles: [] })),
    getOrgSummary: vi.fn(() => Promise.resolve({ v: 1, orgs: [], totals: {} })),
    getGlobalOrgSummary: vi.fn(() => Promise.resolve({ v: 1, scope: 'global', orgs: [], totals: {} })),
    listWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }])),
    listAllWorkflows: vi.fn(() => Promise.resolve([{ id: 'w1' }, { id: 'w2', profile_id: 'p-work' }])),
    getRecentExecutions: vi.fn(() => Promise.resolve([])),
    getAllRecentExecutions: vi.fn(() => Promise.resolve([])),
  },
  subscribeEvent: vi.fn(() => () => {}),
}))
vi.mock('../../lib/usePageVisible.js', () => ({
  usePageVisibleRef: () => ({ current: true }), useVisibleCatchUp: () => {},
}))
import { api } from '../../services/api.js'
import { useDashboardData } from './useDashboardData.js'

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('useDashboardData scope', () => {
  it('reads the global forms in the All profiles view', async () => {
    const { result } = renderHook(() => useDashboardData({ scope: 'global' }))
    await waitFor(() => expect(result.current.summary?.scope).toBe('global'))
    expect(result.current.workflows).toHaveLength(2)
    expect(api.getSummary).not.toHaveBeenCalled()
    expect(api.getGlobalOrgSummary).toHaveBeenCalledWith(false)
  })

  it('switching scope reloads everything from the other scope', async () => {
    const { result, rerender } = renderHook(({ scope }) => useDashboardData({ scope }), { initialProps: { scope: 'profile' } })
    await waitFor(() => expect(result.current.summary?.scope).toBe('profile'))
    rerender({ scope: 'global' })
    await waitFor(() => expect(result.current.summary?.scope).toBe('global'))
    expect(api.listAllWorkflows).toHaveBeenCalled()
    expect(api.getAllRecentExecutions).toHaveBeenCalledWith(30)
    rerender({ scope: 'profile' })
    await waitFor(() => expect(result.current.summary?.scope).toBe('profile'))
    expect(result.current.workflows).toHaveLength(1)
  })
})
