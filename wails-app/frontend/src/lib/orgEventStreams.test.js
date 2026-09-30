import { describe, it, expect, vi, beforeEach } from 'vitest'

const { api } = vi.hoisted(() => ({
  api: {
    streamOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
    stopOrgEvents: vi.fn(() => Promise.resolve({ ok: true })),
  },
}))
vi.mock('../services/api.js', () => ({
  api,
  newOrgEventsStreamId: (() => { let n = 0; return () => `s${++n}` })(),
}))

import { acquireOrgEvents, restartOrgEvents, orgEventHolders } from './orgEventStreams.js'

beforeEach(() => vi.clearAllMocks())

describe('shared org event tails', () => {
  it('starts one tail for many holders and stops it with the last release', () => {
    const a = acquireOrgEvents('acme')
    const b = acquireOrgEvents('acme')
    expect(api.streamOrgEvents).toHaveBeenCalledTimes(1)
    expect(orgEventHolders('acme')).toBe(2)
    a()
    a() // a second release of the same hold changes nothing
    expect(api.stopOrgEvents).not.toHaveBeenCalled()
    b()
    expect(api.stopOrgEvents).toHaveBeenCalledTimes(1)
    expect(api.stopOrgEvents.mock.calls[0]).toEqual(['acme', api.streamOrgEvents.mock.calls[0][1]])
    expect(orgEventHolders('acme')).toBe(0)
  })

  it('restarts a held tail under a new id, and the last release stops every id it ran under', () => {
    // The restart's stream never registers before the release (its promise
    // stays pending), so the Go side still runs the first id: both must be
    // stopped, or the first follower leaks.
    api.streamOrgEvents.mockImplementationOnce(() => Promise.resolve({ ok: true }))
      .mockImplementationOnce(() => new Promise(() => {}))
    const release = acquireOrgEvents('acme')
    restartOrgEvents('acme')
    expect(api.streamOrgEvents).toHaveBeenCalledTimes(2)
    const [first, second] = api.streamOrgEvents.mock.calls.map(c => c[1])
    expect(first).not.toBe(second)
    release()
    expect(api.stopOrgEvents.mock.calls).toEqual([['acme', first], ['acme', second]])
    restartOrgEvents('acme') // nobody holds it: nothing starts
    expect(api.streamOrgEvents).toHaveBeenCalledTimes(2)
  })
})
