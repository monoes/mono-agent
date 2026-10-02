import { describe, it, expect } from 'vitest'
import { pickListener, listenerState, baseURL, modelsArgs, missingIsAboutJev, relativeTime, formatDate } from './apiModel.js'

// Listeners as `api status --json` reports them (cmd/monoagentcli/api_status.go).
const listener = (over = {}) => ({
  name: 'main', addr: '127.0.0.1:9322', loopback: true, v1: true,
  confinement: 'any', context_confinement: 'chat-only', auto_confinement: 'chat-only',
  confinement_source: 'environment', reachable: true, v1_answers: true, ...over,
})
const dedicated = (over = {}) => listener({
  name: 'v1', addr: '0.0.0.0:9443', loopback: false, confinement: 'chat-only', confinement_source: 'daemon', ...over,
})
const status = (...listeners) => ({ v: 1, profile: 'default', keys: { active: 0 }, daemon: { running: true }, listeners })

describe('pickListener', () => {
  it('takes the main listener when it is loopback and serves /v1, even with a dedicated one', () => {
    const main = listener(); const v1 = dedicated()
    expect(pickListener(status(main, v1))).toBe(main)
    expect(pickListener(status(main))).toBe(main)
  })
  it('takes the dedicated listener when the main one is off loopback', () => {
    const v1 = dedicated()
    expect(pickListener(status(listener({ addr: '0.0.0.0:9322', loopback: false, v1: false }), v1))).toBe(v1)
  })
  it('takes the dedicated listener when the daemon does not mount /v1 on the loopback main one', () => {
    const v1 = dedicated({ loopback: true, addr: '127.0.0.1:9443' })
    expect(pickListener(status(listener({ v1: false }), v1))).toBe(v1)
  })
  it('falls back to the main listener, to explain why nothing serves /v1', () => {
    const main = listener({ addr: '0.0.0.0:9322', loopback: false, v1: false })
    expect(pickListener(status(main))).toBe(main)
  })
  it('takes a dedicated listener alone', () => {
    const v1 = dedicated()
    expect(pickListener(status(v1))).toBe(v1)
  })
  it('is null with no listener, no status or a status without listeners', () => {
    expect(pickListener(status())).toBeNull()
    expect(pickListener({})).toBeNull()
    expect(pickListener(null)).toBeNull()
    expect(pickListener(undefined)).toBeNull()
    expect(pickListener({ listeners: null })).toBeNull()
  })
})

describe('listenerState (listenerNote of api_status.go, case by case)', () => {
  it('is none without a listener', () => expect(listenerState(null)).toBe('none'))
  it('is down when /health does not answer, whatever else is true', () => {
    expect(listenerState(listener({ reachable: false, v1_answers: false }))).toBe('down')
    expect(listenerState(listener({ reachable: false, v1: false }))).toBe('down')
  })
  it('says the daemon does not serve /v1 on a reachable loopback listener that is not meant to', () => {
    expect(listenerState(listener({ v1: false, v1_answers: false }))).toBe('no-v1-daemon')
  })
  it('says an off-loopback main listener does not serve /v1', () => {
    expect(listenerState(listener({ addr: '0.0.0.0:9322', loopback: false, v1: false, v1_answers: false }))).toBe('no-v1-offloopback')
  })
  it('is serving when /v1 answers', () => expect(listenerState(listener())).toBe('serving'))
  it('is stale when /health answers but /v1 does not (a server that predates the API)', () => {
    expect(listenerState(listener({ v1_answers: false }))).toBe('stale')
  })
})

describe('baseURL', () => {
  it('is plain http on the main listener', () => {
    expect(baseURL(listener())).toEqual({ url: 'http://127.0.0.1:9322/v1', tls: false, wildcard: false })
  })
  it('is plain http on a dedicated loopback listener, and https off loopback', () => {
    expect(baseURL(dedicated({ loopback: true, addr: '127.0.0.1:9443' }))).toEqual({ url: 'http://127.0.0.1:9443/v1', tls: false, wildcard: false })
    expect(baseURL(dedicated({ addr: 'api.example.com:9443' }))).toEqual({ url: 'https://api.example.com:9443/v1', tls: true, wildcard: false })
  })
  it('shows a wildcard host as localhost and says so', () => {
    for (const addr of ['0.0.0.0:9443', ':9443', '[::]:9443']) {
      expect(baseURL(dedicated({ addr }))).toEqual({ url: 'https://localhost:9443/v1', tls: true, wildcard: true })
    }
  })
  it('keeps the brackets of an IPv6 host', () => {
    expect(baseURL(listener({ addr: '[::1]:9322' })).url).toBe('http://[::1]:9322/v1')
  })
  it('has no URL for a listener that is not meant to serve /v1, or whose address is not host:port', () => {
    expect(baseURL(listener({ v1: false }))).toBeNull()
    expect(baseURL(listener({ addr: 'nonsense' }))).toBeNull()
    expect(baseURL(listener({ addr: '' }))).toBeNull()
    expect(baseURL(null)).toBeNull()
  })
})

describe('modelsArgs', () => {
  it('asks the CLI for its own defaults when no listener serves /v1', () => {
    expect(modelsArgs(null)).toEqual(['', '', '', ''])
    expect(modelsArgs(listener({ v1: false }))).toEqual(['', '', '', ''])
  })
  it('evaluates the listener the header describes, with the policy it reports', () => {
    expect(modelsArgs(listener())).toEqual(['loopback', 'any', 'chat-only', 'chat-only'])
    expect(modelsArgs(dedicated({ context_confinement: 'sandboxed', auto_confinement: 'sandboxed' }))).toEqual(['network', 'chat-only', 'sandboxed', 'sandboxed'])
  })
  it('drops what is missing (a CLI that predates --auto-confinement) or is not a class', () => {
    const l = listener({ confinement: 'none', context_confinement: '' })
    delete l.auto_confinement
    expect(modelsArgs(l)).toEqual(['loopback', '', '', ''])
  })
})

describe('missingIsAboutJev', () => {
  it('is true for what the Jev section fixes', () => {
    expect(missingIsAboutJev('the api_auto surface switched on for the profile (monoagentcli jev enable api_auto)')).toBe(true)
    expect(missingIsAboutJev('a Jev key for the profile (monoagentcli jev key set, or TYPESAFE_API_KEY)')).toBe(true)
  })
  it('is false for what only the server can fix, and for nothing', () => {
    expect(missingIsAboutJev("at least one model the listener's policy allows")).toBe(false)
    expect(missingIsAboutJev('a model within --auto-confinement (chat-only), which holds back all 3 the listener serves')).toBe(false)
    expect(missingIsAboutJev('')).toBe(false)
    expect(missingIsAboutJev(undefined)).toBe(false)
  })
})

describe('relativeTime', () => {
  const now = Date.parse('2026-10-02T12:00:00Z')
  const ago = (s) => new Date(now - s * 1000).toISOString()
  it('says now for a few seconds, then minutes, hours, days', () => {
    expect(relativeTime(ago(10), 'en', now)).toBe('now')
    expect(relativeTime(ago(3 * 60), 'en', now)).toBe('3 minutes ago')
    expect(relativeTime(ago(2 * 3600), 'en', now)).toBe('2 hours ago')
    expect(relativeTime(ago(24 * 3600), 'en', now)).toBe('yesterday')
    expect(relativeTime(ago(3 * 24 * 3600), 'en', now)).toBe('3 days ago')
    expect(relativeTime(ago(90 * 24 * 3600), 'en', now)).toBe('3 months ago')
  })
  it('speaks the chosen language', () => {
    expect(relativeTime(ago(3 * 60), 'es', now)).toBe('hace 3 minutos')
  })
  it('falls back to English for a language it does not know, and is empty for no date', () => {
    expect(relativeTime(ago(3 * 60), 'not a language!', now)).toBe('3 minutes ago')
    expect(relativeTime('', 'en', now)).toBe('')
    expect(relativeTime(null, 'en', now)).toBe('')
    expect(relativeTime('yesterday-ish', 'en', now)).toBe('')
  })
})

describe('formatDate', () => {
  it('gives a short date in the chosen language', () => {
    expect(formatDate('2026-10-01T12:00:00Z', 'en')).toMatch(/Oct 1, 2026/)
    expect(formatDate('2026-10-01T12:00:00Z', 'es')).toMatch(/1 oct\.? 2026/)
  })
  it('is empty for no or a bad date', () => {
    expect(formatDate('', 'en')).toBe('')
    expect(formatDate(undefined, 'en')).toBe('')
    expect(formatDate('not a date', 'en')).toBe('')
  })
})
