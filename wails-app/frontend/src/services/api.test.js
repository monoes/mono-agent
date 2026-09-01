import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock the generated Wails bindings and runtime so api.js can be tested in
// isolation. Each binding is a vi.fn we can make resolve or reject per test.
vi.mock('../wailsjs/go/main/App', () => ({
  GetActions: vi.fn(),
  GetDashboardStats: vi.fn(),
  ListWorkflows: vi.fn(),
  CreateAction: vi.fn(),
}))
// Real Wails EventsOn semantics: each call registers its own listener under
// eventName and returns a disposer that removes only that listener.
// EventsOff(eventName), by contrast, would remove ALL listeners for the
// name — that's the footgun issue #15 is about, so the mock below models
// EventsOn faithfully rather than stubbing it out.
const eventListeners = new Map()
function fakeEventsOn(eventName, callback) {
  const listeners = eventListeners.get(eventName) ?? []
  listeners.push(callback)
  eventListeners.set(eventName, listeners)
  return () => {
    const current = eventListeners.get(eventName) ?? []
    eventListeners.set(eventName, current.filter((cb) => cb !== callback))
  }
}
function fakeEventsEmit(eventName, ...args) {
  for (const cb of eventListeners.get(eventName) ?? []) cb(...args)
}

vi.mock('../wailsjs/runtime/runtime', () => ({
  EventsOn: vi.fn((name, cb) => fakeEventsOn(name, cb)),
  EventsOff: vi.fn(),
}))

import * as GoApp from '../wailsjs/go/main/App'
import { api, onApiError, onActionComplete, onLogEntry } from './api.js'

describe('api error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('degrades a failed read to its safe default', async () => {
    GoApp.GetActions.mockRejectedValueOnce(new Error('db locked'))
    const result = await api.getActions()
    expect(result).toBeNull() // getActions falls back to null
  })

  it('broadcasts a failure on the error bus instead of swallowing it silently', async () => {
    const events = []
    const off = onApiError((detail) => events.push(detail))

    GoApp.GetDashboardStats.mockRejectedValueOnce(new Error('boom'))
    await api.getDashboardStats()

    expect(events).toHaveLength(1)
    expect(events[0].op).toBe('dashboard stats')
    expect(events[0].message).toContain('boom')
    off()
  })

  it('passes through a successful read unchanged', async () => {
    GoApp.ListWorkflows.mockResolvedValueOnce([{ id: 'wf1' }])
    const result = await api.listWorkflows()
    expect(result).toEqual([{ id: 'wf1' }])
  })

  it('does not intercept write-path methods (they propagate to the caller)', async () => {
    GoApp.CreateAction.mockRejectedValueOnce(new Error('validation'))
    await expect(api.createAction({})).rejects.toThrow('validation')
  })
})

// Regression test for issue #15: EventsOff(eventName) removes every listener
// registered under that name, not just the caller's own. App.jsx and
// Actions.jsx both subscribe to 'action:complete' independently; unmounting
// or re-subscribing one must never silence the other.
describe('event subscription independence (issue #15)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    eventListeners.clear()
    globalThis.window = { runtime: {} }
  })

  it('disposing one subscription leaves other subscribers to the same event intact', () => {
    const appReceived = []
    const actionsPageReceived = []

    const disposeAppListener = onActionComplete((data) => appReceived.push(data))
    const disposeActionsPageListener = onActionComplete((data) => actionsPageReceived.push(data))

    // Simulate navigating away from Actions.jsx (or a filter-driven re-subscribe)
    disposeActionsPageListener()

    fakeEventsEmit('action:complete', { action_id: '1' })

    expect(appReceived).toEqual([{ action_id: '1' }])
    expect(actionsPageReceived).toEqual([])

    disposeAppListener()
  })

  it('does not cross-cancel independently subscribed events with different names', () => {
    const logReceived = []
    const actionReceived = []

    const disposeLog = onLogEntry((entry) => logReceived.push(entry))
    onActionComplete((data) => actionReceived.push(data))

    disposeLog()

    fakeEventsEmit('log:entry', 'should not arrive')
    fakeEventsEmit('action:complete', { action_id: '2' })

    expect(logReceived).toEqual([])
    expect(actionReceived).toEqual([{ action_id: '2' }])
  })
})
