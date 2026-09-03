import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock the generated Wails bindings and runtime so api.js can be tested in
// isolation. Each binding is a vi.fn we can make resolve or reject per test.
vi.mock('../wailsjs/go/main/App', () => ({
  GetPeople: vi.fn(),
  GetDashboardStats: vi.fn(),
  ListWorkflows: vi.fn(),
  DeleteSession: vi.fn(),
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
import { api, onApiError, onLogEntry, subscribeEvent } from './api.js'

describe('api error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('degrades a failed read to its safe default', async () => {
    GoApp.GetPeople.mockRejectedValueOnce(new Error('db locked'))
    const result = await api.getPeople()
    expect(result).toBeNull() // getPeople falls back to null
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
    GoApp.DeleteSession.mockRejectedValueOnce(new Error('validation'))
    await expect(api.deleteSession('s1')).rejects.toThrow('validation')
  })
})

// Regression test for issue #15: EventsOff(eventName) removes every listener
// registered under that name, not just the caller's own. App.jsx and
// People.jsx both subscribe to 'workflow:complete' independently; unmounting
// or re-subscribing one must never silence the other.
describe('event subscription independence (issue #15)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    eventListeners.clear()
    globalThis.window = { runtime: {} }
  })

  it('disposing one subscription leaves other subscribers to the same event intact', () => {
    const appReceived = []
    const peoplePageReceived = []

    const disposeAppListener = subscribeEvent('workflow:complete', (data) => appReceived.push(data))
    const disposePeoplePageListener = subscribeEvent('workflow:complete', (data) => peoplePageReceived.push(data))

    // Simulate navigating away from People.jsx (or a filter-driven re-subscribe)
    disposePeoplePageListener()

    fakeEventsEmit('workflow:complete', { workflow_id: '1' })

    expect(appReceived).toEqual([{ workflow_id: '1' }])
    expect(peoplePageReceived).toEqual([])

    disposeAppListener()
  })

  it('does not cross-cancel independently subscribed events with different names', () => {
    const logReceived = []
    const workflowReceived = []

    const disposeLog = onLogEntry((entry) => logReceived.push(entry))
    subscribeEvent('workflow:complete', (data) => workflowReceived.push(data))

    disposeLog()

    fakeEventsEmit('log:entry', 'should not arrive')
    fakeEventsEmit('workflow:complete', { workflow_id: '2' })

    expect(logReceived).toEqual([])
    expect(workflowReceived).toEqual([{ workflow_id: '2' }])
  })
})
