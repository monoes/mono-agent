// The task service (services/tasks.js) run against mocked bindings. Every call
// resolves to the CLI's parsed JSON or to {error, code?} and never rejects:
// the board hook awaits these calls to settle an optimistic move and has no
// catch of its own (phase 3 plan, Task 5), so a rejection would strand the
// move it laid over the board. Each member also hands its arguments to its
// binding exactly. That the binding names exist in the generated module is
// checked in tasks.test.js.
import { describe, it, expect, vi, beforeEach } from 'vitest'

const { App, subscribeEvent } = vi.hoisted(() => ({
  App: {
    TaskBoard: vi.fn(), TaskShow: vi.fn(), TaskAdd: vi.fn(), TaskEdit: vi.fn(), TaskMove: vi.fn(),
    TaskApprove: vi.fn(), TaskArchive: vi.fn(), TaskUnarchive: vi.fn(), TaskComment: vi.fn(),
    TaskPulse: vi.fn(), TaskAgentShell: vi.fn(),
  },
  subscribeEvent: vi.fn(),
}))
vi.mock('../wailsjs/go/main/App', () => App)
vi.mock('./api.js', () => ({ subscribeEvent }))

import { tasksApi, DONE_LIMIT, onTasksChanged } from './tasks.js'

describe('the task service', () => {
  beforeEach(() => {
    Object.values(App).forEach(fn => fn.mockReset())
    subscribeEvent.mockReset()
  })

  it('asks for the board with DONE_LIMIT and parses the JSON it gets', async () => {
    expect(DONE_LIMIT).toBe(50)
    App.TaskBoard.mockResolvedValue('{"profile_id":"default","columns":{}}')
    expect(await tasksApi.board()).toEqual({ profile_id: 'default', columns: {} })
    expect(App.TaskBoard).toHaveBeenCalledWith(50)
  })

  it('hands each argument to its binding exactly, text that starts with a dash included', async () => {
    for (const binding of ['TaskShow', 'TaskAdd', 'TaskEdit', 'TaskApprove', 'TaskArchive', 'TaskUnarchive', 'TaskComment']) {
      App[binding].mockResolvedValue('{"ok":true}')
    }
    await tasksApi.show(3)
    await tasksApi.add({ title: '-x fix', notes: '--help me' })
    await tasksApi.edit(3, { notes: '' })
    await tasksApi.approve([1, 2])
    await tasksApi.approve([1], true)
    await tasksApi.archive([5])
    await tasksApi.unarchive([6])
    await tasksApi.comment(3, '-looks odd')
    expect(App.TaskShow.mock.calls).toEqual([[3]])
    expect(App.TaskAdd.mock.calls).toEqual([['{"title":"-x fix","notes":"--help me"}']])
    expect(App.TaskEdit.mock.calls).toEqual([[3, '{"notes":""}']])
    expect(App.TaskApprove.mock.calls).toEqual([[[1, 2], false], [[1], true]])
    expect(App.TaskArchive.mock.calls).toEqual([[[5]]])
    expect(App.TaskUnarchive.mock.calls).toEqual([[[6]]])
    expect(App.TaskComment.mock.calls).toEqual([[3, '-looks odd']])
  })

  it('moves with the column default place unless one is named', async () => {
    App.TaskMove.mockResolvedValue('{"id":5}')
    await tasksApi.move(5, 'ready')
    await tasksApi.move(5, 'review', { where: 'before', ref: 7 })
    await tasksApi.move(5, 'done', { where: 'top' })
    expect(App.TaskMove.mock.calls).toEqual([[5, 'ready', '', 0], [5, 'review', 'before', 7], [5, 'done', 'top', 0]])
  })

  it('never rejects: a CLI refusal comes back parsed, a failed or unreadable answer becomes {error}', async () => {
    App.TaskMove.mockResolvedValue('{"error":"nope","code":"operator_only"}')
    expect(await tasksApi.move(1, 'ready')).toEqual({ error: 'nope', code: 'operator_only' })
    App.TaskShow.mockRejectedValue(new Error('boom'))
    expect(await tasksApi.show(1)).toEqual({ error: 'boom' })
    App.TaskShow.mockRejectedValue('plain string')
    expect(await tasksApi.show(1)).toEqual({ error: 'plain string' })
    App.TaskBoard.mockResolvedValue('not json')
    expect(typeof (await tasksApi.board()).error).toBe('string')
  })

  it('turns a binding that throws at once, or is missing from the build, into {error}', async () => {
    App.TaskComment.mockImplementation(() => { throw new Error('sync') })
    expect(await tasksApi.comment(1, 'x')).toEqual({ error: 'sync' })
    const show = App.TaskShow
    delete App.TaskShow
    try {
      expect(typeof (await tasksApi.show(1)).error).toBe('string')
    } finally {
      App.TaskShow = show
    }
  })

  it('returns the pulse as it is, {} included', async () => {
    App.TaskPulse.mockResolvedValue({ profile_id: 'default', rev: 4, inbox: 1, review: 2 })
    expect(await tasksApi.pulse()).toEqual({ profile_id: 'default', rev: 4, inbox: 1, review: 2 })
    App.TaskPulse.mockResolvedValue({})
    expect(await tasksApi.pulse()).toEqual({})
  })

  it('answers the agent shell with a plain string, "" when there is none or the call fails', async () => {
    App.TaskAgentShell.mockResolvedValue('CLAUDECODE')
    expect(await tasksApi.agentShell()).toBe('CLAUDECODE')
    App.TaskAgentShell.mockResolvedValue('')
    expect(await tasksApi.agentShell()).toBe('')
    App.TaskAgentShell.mockResolvedValue(undefined)
    expect(await tasksApi.agentShell()).toBe('')
    App.TaskAgentShell.mockRejectedValue(new Error('x'))
    expect(await tasksApi.agentShell()).toBe('')
  })

  it('subscribes onTasksChanged to tasks:changed and returns the unsubscribe function', () => {
    const off = () => {}
    const callback = () => {}
    subscribeEvent.mockReturnValue(off)
    expect(onTasksChanged(callback)).toBe(off)
    expect(subscribeEvent.mock.calls).toEqual([['tasks:changed', callback]])
  })
})
