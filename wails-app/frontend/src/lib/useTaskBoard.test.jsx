// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor, cleanup } from '@testing-library/react'

const { api, handlers } = vi.hoisted(() => ({
  api: { board: vi.fn(), move: vi.fn(), approve: vi.fn(), archive: vi.fn(), add: vi.fn(), edit: vi.fn(), comment: vi.fn() },
  handlers: [],
}))
vi.mock('../services/tasks.js', () => ({
  tasksApi: api,
  onTasksChanged: (cb) => { handlers.push(cb); return () => { handlers.splice(handlers.indexOf(cb), 1) } },
}))
import { useTaskBoard } from './useTaskBoard.js'

const doc = (inbox, ready = []) => ({ rev: 1, counts: {}, tasks: { inbox, ready } })
const t = (id, status) => ({ id, title: `t${id}`, status, position: id })

beforeEach(() => { Object.values(api).forEach(f => f.mockReset()); handlers.length = 0 })
afterEach(cleanup)

describe('useTaskBoard', () => {
  it('reads the board, and reads again on tasks:changed', async () => {
    api.board.mockResolvedValue(doc([t(1, 'inbox')]))
    const { result } = renderHook(() => useTaskBoard())
    await waitFor(() => expect(result.current.board?.columns.inbox).toHaveLength(1))
    api.board.mockResolvedValue(doc([t(1, 'inbox'), t(2, 'inbox')]))
    await act(async () => { handlers.forEach(h => h()) })
    await waitFor(() => expect(result.current.board.columns.inbox).toHaveLength(2))
  })

  it('shows a move before the CLI answers and keeps it after the re-read', async () => {
    api.board.mockResolvedValueOnce(doc([t(1, 'inbox')]))
    const { result } = renderHook(() => useTaskBoard())
    await waitFor(() => expect(result.current.board).toBeTruthy())
    let finish
    api.move.mockReturnValue(new Promise(r => { finish = r }))
    api.board.mockResolvedValue(doc([], [t(1, 'ready')]))
    let p
    act(() => { p = result.current.move(1, 'ready', { where: '' }) })
    expect(result.current.board.columns.ready.map(x => x.id)).toEqual([1])
    await act(async () => { finish({ id: 1 }); await p })
    expect(result.current.board.columns.ready.map(x => x.id)).toEqual([1])
    expect(result.current.notice).toBeNull()
  })

  it('slides the card back and reports a refusal', async () => {
    api.board.mockResolvedValue(doc([t(1, 'inbox')]))
    const { result } = renderHook(() => useTaskBoard())
    await waitFor(() => expect(result.current.board).toBeTruthy())
    api.move.mockResolvedValue({ error: 'only a person can approve', code: 'human_gate' })
    await act(async () => { await result.current.move(1, 'ready', { where: '' }) })
    expect(result.current.board.columns.inbox.map(x => x.id)).toEqual([1])
    expect(result.current.notice).toEqual({ id: 1, error: 'only a person can approve', code: 'human_gate' })
  })

  it('reports a failed read', async () => {
    api.board.mockResolvedValue({ error: 'no database' })
    const { result } = renderHook(() => useTaskBoard())
    await waitFor(() => expect(result.current.error).toBe('no database'))
  })
})
