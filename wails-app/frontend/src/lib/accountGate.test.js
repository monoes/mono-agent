// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { createAccountGate, viewOf, FOCUS_GAP_MS, LOGIN_REQUIRED_GAP_MS, RETRY_MS } from './accountGate.js'

const DATE = '2026-10-26T00:00:00Z'
const st = (over = {}) => ({ status: { v: 1, state: 'ok', reason: '', plan: 'free', enforced: true, enforce_from: DATE, ...over } })
const FAIL = (cause = 'cli_failed', over = {}) => ({ failure: { cause, message: 'x', enforced: true, ...over } })
const view = (answer) => viewOf({ phase: 'ready', status: answer.status ?? null, failure: answer.failure ?? null })
const locked = (gate) => viewOf(gate.getSnapshot()).locked

afterEach(() => { vi.useRealTimers() })

// A gate whose clock the test moves and whose answers it queues.
function gateWith(...answers) {
  let t = 1_000_000
  const status = vi.fn(() => Promise.resolve(answers.length > 1 ? answers.shift() : answers[0]))
  return { gate: createAccountGate({ status, now: () => t }), status, advance: (ms) => { t += ms } }
}

describe('viewOf', () => {
  it('is checking until the first answer', () => {
    expect(viewOf({ phase: 'checking', status: null, failure: null })).toEqual({ phase: 'checking', locked: false })
  })

  it('locks only a locked status that is enforced', () => {
    expect(view(st({ state: 'locked', reason: 'not_logged_in' })).locked).toBe(true)
    for (const over of [{ state: 'locked', enforced: false }, { state: 'ok' }, { state: 'grace' }]) expect(view(st(over)).locked).toBe(false)
  })

  it('locks a failure only once this build enforces; before the date it warns with the date', () => {
    for (const cause of ['cli_not_found', 'cli_too_old', 'cli_failed']) expect(view(FAIL(cause)).locked, cause).toBe(true)
    expect(view({ failure: { cause: 'cli_too_old', message: '', enforced: undefined } }).locked).toBe(true) // silent: fail closed
    const early = view(FAIL('cli_too_old', { enforced: false, enforce_from: DATE }))
    expect(early).toMatchObject({ locked: false, warn: true, enforceFrom: DATE })
  })

  it('warns in grace and before the date, and says nothing for a build with no date (dormant, D22)', () => {
    expect(view(st({ state: 'grace', reason: 'unreachable' })).grace).toBe(true)
    expect(view(st({ state: 'ok' })).grace).toBe(false)
    expect(view(st({ state: 'locked', enforced: false })).warn).toBe(true)
    expect(view(st({ state: 'ok', enforced: false })).warn).toBe(false)               // already signed in
    expect(view(st({ state: 'locked', enforced: true })).warn).toBe(false)
    const dormant = { enforced: false, enforce_from: undefined }
    for (const state of ['ok', 'grace', 'locked']) {
      expect(view(st({ state, ...dormant })), state).toMatchObject({ locked: false, grace: false, warn: false })
    }
  })
})

describe('the gate', () => {
  it('asks once at start and publishes the answer', async () => {
    const { gate, status } = gateWith(st())
    const seen = vi.fn()
    gate.subscribe(seen)
    expect(gate.getSnapshot().phase).toBe('checking')
    await gate.check('start')
    expect(status).toHaveBeenCalledTimes(1)
    expect(gate.getSnapshot()).toMatchObject({ phase: 'ready', failure: null, status: { state: 'ok' } })
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it('fails closed at the very first answer, with no retry', async () => {
    const { gate, status } = gateWith(FAIL('cli_failed'))
    await gate.check('start')
    expect(status).toHaveBeenCalledTimes(1)
    expect(locked(gate)).toBe(true)
  })

  it('shares one ask between overlapping checks, and drops a focus or a login_required answer that follows an answer closely', async () => {
    const { gate, status, advance } = gateWith(st())
    await Promise.all([gate.check('start'), gate.check('login_required'), gate.check('manual')])
    expect(status).toHaveBeenCalledTimes(1)
    advance(FOCUS_GAP_MS - 1)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(1)
    advance(1)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(2)
    advance(LOGIN_REQUIRED_GAP_MS - 1)
    await gate.check('login_required')
    expect(status).toHaveBeenCalledTimes(2)
    await gate.check('manual')
    expect(status).toHaveBeenCalledTimes(3)
    advance(LOGIN_REQUIRED_GAP_MS)
    await gate.check('login_required')
    expect(status).toHaveBeenCalledTimes(4)
  })

  it('locks when a later ask says locked, and unlocks when it says ok', async () => {
    const { gate, advance } = gateWith(st(), st({ state: 'locked', reason: 'refused' }), st())
    await gate.check('start')
    advance(10_000)
    await gate.check('focus')
    expect(locked(gate)).toBe(true)
    await gate.check('manual')
    expect(locked(gate)).toBe(false)
  })

  it('repeats a failed ask once while the app is up, and locks only if it fails again', async () => {
    vi.useFakeTimers()
    const hiccup = gateWith(st(), FAIL(), st())
    await hiccup.gate.check('start')
    hiccup.advance(10_000)
    const first = hiccup.gate.check('focus')
    await vi.advanceTimersByTimeAsync(RETRY_MS)
    await first
    expect(hiccup.status).toHaveBeenCalledTimes(3)
    expect(locked(hiccup.gate)).toBe(false)

    const down = gateWith(st(), FAIL(), FAIL())
    await down.gate.check('start')
    down.advance(10_000)
    const second = down.gate.check('focus')
    expect(locked(down.gate)).toBe(false) // still up during the wait
    await vi.advanceTimersByTimeAsync(RETRY_MS)
    await second
    expect(locked(down.gate)).toBe(true)
  })

  it('does not retry a failure that cannot be a hiccup', async () => {
    const { gate, status, advance } = gateWith(st(), FAIL('cli_too_old'))
    await gate.check('start')
    advance(10_000)
    await gate.check('focus')
    expect(status).toHaveBeenCalledTimes(2)
    expect(locked(gate)).toBe(true)
  })

  it('start() asks now and on the window focus, and stop() ends it', async () => {
    const { gate, status, advance } = gateWith(st())
    const stop = gate.start()
    await vi.waitFor(() => expect(gate.getSnapshot().phase).toBe('ready'))
    advance(FOCUS_GAP_MS)
    window.dispatchEvent(new Event('focus'))
    await vi.waitFor(() => expect(status).toHaveBeenCalledTimes(2))
    stop()
    advance(FOCUS_GAP_MS)
    window.dispatchEvent(new Event('focus'))
    await Promise.resolve()
    expect(status).toHaveBeenCalledTimes(2)
  })
})
