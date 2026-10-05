import { describe, it, expect } from 'vitest'
import {
  ROWS, bannerOf, byKey, canSave, dirtyKeys, hasSaved, notServingKeys, otherProblems, overriddenInfo, restartSettled, rowProblems, rowState,
  runningInfo, savePayload, stateInfo, summary, textOf, wideningHeading,
} from './configModel.js'
import { KEYS, configDoc } from './__fixtures__/configFixtures.js'

const row = (id) => ROWS.find(r => r.id === id)

describe('the rows', () => {
  it('cover the ten settings once each, in the order of the CLI, the two TLS files as one row', () => {
    expect(ROWS.flatMap(r => r.keys)).toEqual(KEYS)
    expect(ROWS.map(r => r.id)).toEqual(['v1_addr', 'tls', 'confinement', 'context_confinement', 'auto_confinement', 'max_concurrent', 'turn_timeout', 'image_runtimes', 'tool_runtimes'])
    expect(row('tls').keys).toEqual(['tls_cert_file', 'tls_key_file'])
  })

  it('give each setting the control that fits it: a select for a class, a number for a count, a text for the rest', () => {
    expect(Object.fromEntries(ROWS.map(r => [r.id, r.kind]))).toEqual({
      v1_addr: 'text', tls: 'pair', confinement: 'class', context_confinement: 'class', auto_confinement: 'class',
      max_concurrent: 'number', turn_timeout: 'text', image_runtimes: 'text', tool_runtimes: 'text',
    })
  })

  it('name each string they use in full, so that the locale test can see it', () => {
    for (const r of ROWS) {
      expect(r.label, r.id).toMatch(/^settings\.api\.config\.rows\.\w+\.label$/)
      expect(r.hint, r.id).toMatch(/^settings\.api\.config\.rows\.\w+\.hint$/)
    }
    expect(ROWS.filter(r => r.empty).map(r => r.id)).toEqual(['v1_addr', 'tls', 'confinement']) // the settings whose default is no value
    expect(row('tls').parts.map(p => p.key)).toEqual(['tls_cert_file', 'tls_key_file'])
  })
})

describe('the state of a setting', () => {
  const doc = configDoc({ saved: { max_concurrent: '8', tool_runtimes: 'claude' }, running: { max_concurrent: ['4', 'default'], tool_runtimes: ['claude,codex', 'env'], turn_timeout: ['15m', 'saved'] } })
  const by = byKey(doc)

  it('says each of the six, with a tone, a name and a hint of its own, and an unexpected one is unknown', () => {
    const states = ['applied', 'pending_restart', 'overridden', 'not_running', 'unknown', 'not_serving']
    const info = states.map(s => stateInfo({ state: s }))
    expect(info.map(i => i.id)).toEqual(['applied', 'pending', 'overridden', 'notRunning', 'unknown', 'notServing'])
    expect(new Set(info.map(i => i.text)).size).toBe(6)
    expect(new Set(info.map(i => i.hint)).size).toBe(6)
    expect(info.map(i => i.tone)).toEqual(['ok', 'warn', 'hot', 'muted', 'muted', 'bad'])
    expect(stateInfo({ state: 'something new' }).id).toBe('unknown')
    expect(stateInfo({}).id).toBe('unknown')
    expect(stateInfo({ state: 'constructor' }).id).toBe('unknown')
    expect(by.max_concurrent.state).toBe('pending_restart') // the fixture is what the CLI would say
    expect(stateInfo(by.max_concurrent).id).toBe('pending')
  })

  it('says what the daemon runs and where that came from, and nothing when it does not report it', () => {
    expect(runningInfo(by.max_concurrent)).toEqual({ value: '4', kind: 'default', name: '' })
    expect(runningInfo(by.tool_runtimes)).toEqual({ value: 'claude,codex', kind: 'env', name: 'MONOAGENT_API_TOOL_RUNTIMES' })
    expect(runningInfo(by.turn_timeout)).toEqual({ value: '15m', kind: 'saved', name: '' })
    expect(runningInfo({ key: 'max_concurrent', server_flag: '--max-concurrent', running: '8', running_source: 'flag' })).toEqual({ value: '8', kind: 'flag', name: '--max-concurrent' })
    // an empty running value is a value: the daemon runs it with none
    expect(runningInfo(by.v1_addr)).toEqual({ value: '', kind: 'default', name: '' })
    expect(runningInfo(byKey(configDoc()).v1_addr)).toBeNull() // no daemon
    expect(runningInfo(byKey(configDoc({ running: 'old' })).v1_addr)).toBeNull() // a daemon that predates the report
  })

  it('says what overrides a saved value, by flag or by variable, and nothing for a setting that is not overridden', () => {
    expect(overriddenInfo(by.tool_runtimes)).toEqual({ kind: 'env', name: 'MONOAGENT_API_TOOL_RUNTIMES' })
    expect(overriddenInfo({ state: 'overridden', server_flag: '--max-concurrent', env: 'X', running_source: 'flag' })).toEqual({ kind: 'flag', name: '--max-concurrent' })
    expect(overriddenInfo(by.max_concurrent)).toBeNull()
    expect(overriddenInfo({ state: 'overridden', env: 'X' })).toEqual({ kind: '', name: '' }) // overridden by something it cannot name
  })
})

describe('the state of a row', () => {
  const state = (row_, running, saved = {}) => stateInfo(rowState(row(row_), byKey(configDoc({ saved, running })))).id

  it('is the state of its setting, and for the TLS pair the one of its two files that needs most attention', () => {
    expect(state('max_concurrent', {}, { max_concurrent: '8' })).toBe('applied')
    expect(state('max_concurrent', { max_concurrent: ['4', 'default'] }, { max_concurrent: '8' })).toBe('pending')
    const pair = { tls_cert_file: '/a.pem', tls_key_file: '/a.key' }
    expect(state('tls', {}, pair)).toBe('applied')
    expect(state('tls', { tls_cert_file: ['', 'default'] }, pair)).toBe('pending')
    expect(state('tls', { tls_cert_file: ['', 'default'], tls_key_file: ['/b.key', 'env'] }, pair)).toBe('overridden') // overridden says more than pending
    expect(state('tls', { tls_key_file: ['/b.key', 'env'] }, pair)).toBe('overridden')
    expect(state('tls', null, pair)).toBe('notRunning')
    expect(state('tls', 'old', pair)).toBe('unknown')
  })

  it('is unknown for a setting the CLI did not list: nothing is claimed', () => {
    expect(stateInfo(rowState(row('turn_timeout'), {})).id).toBe('unknown')
    expect(stateInfo(rowState(row('tls'), { tls_cert_file: { state: 'applied' } })).id).toBe('applied') // the one it has
  })

  it('ranks a listener that is not up with the states that need attention: after a pending restart, before unknown and applied', () => {
    const pair = { tls_cert_file: '/a.pem', tls_key_file: '/a.key' }
    const stateOf = (running, notServing) => stateInfo(rowState(row('tls'), byKey(configDoc({ saved: pair, running, notServing })))).id
    expect(stateOf({}, ['tls_key_file'])).toBe('notServing') // one file whose listener is not up beats the other, which is applied
    expect(stateOf({}, ['tls_cert_file', 'tls_key_file'])).toBe('notServing')
    expect(stateOf({ tls_cert_file: ['', 'default'] }, ['tls_key_file'])).toBe('pending') // an actual pending restart still beats it
    expect(stateOf({ tls_cert_file: ['/b.pem', 'env'] }, ['tls_key_file'])).toBe('overridden') // and so does an override, which beats a pending one
    // against states that a document gives other rows, by the ranking alone
    const rank = (a, b) => stateInfo(rowState(row('tls'), { tls_cert_file: { state: a }, tls_key_file: { state: b } })).id
    expect(rank('not_serving', 'unknown')).toBe('notServing')
    expect(rank('unknown', 'not_serving')).toBe('notServing')
    expect(rank('something new', 'not_serving')).toBe('notServing') // a state this page does not know ranks as unknown
    expect(rank('not_serving', 'not_running')).toBe('notServing')
    expect(rank('applied', 'not_serving')).toBe('notServing')
  })
})

describe('the banner of what a restart is for', () => {
  it('asks for a restart when a setting is pending, naming them', () => {
    const doc = configDoc({ saved: { max_concurrent: '8', turn_timeout: '15m' }, running: { max_concurrent: ['4', 'default'], turn_timeout: ['10m', 'default'] }, autostart: true })
    expect(bannerOf(doc)).toEqual({ kind: 'restart', keys: ['max_concurrent', 'turn_timeout'] })
  })

  it('says a daemon that predates the report may not be running what is saved', () => {
    expect(bannerOf(configDoc({ saved: { max_concurrent: '8' }, running: 'old' }))).toEqual({ kind: 'older', keys: [] })
  })

  it('says only that settings apply at the start when no daemon runs', () => {
    expect(bannerOf(configDoc({ saved: { max_concurrent: '8' } }))).toEqual({ kind: 'idle', keys: [] })
  })

  it('says nothing when the daemon runs what is saved, or is overridden by its own flags', () => {
    expect(bannerOf(configDoc({ saved: { max_concurrent: '8' }, running: {} }))).toEqual({ kind: 'none', keys: [] })
    expect(bannerOf(configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['6', 'flag'] } }))).toEqual({ kind: 'none', keys: [] })
  })

  it('says a dedicated listener is not up when no setting is pending, naming the settings', () => {
    const doc = configDoc({
      saved: { v1_addr: '0.0.0.0:9443', tls_cert_file: '/a.pem', tls_key_file: '/a.key' }, running: {}, autostart: true,
      notServing: ['v1_addr', 'tls_cert_file', 'tls_key_file'],
    })
    expect(doc.restart_needed).toBe(false) // the CLI's: only a pending setting needs a restart
    expect(bannerOf(doc)).toEqual({ kind: 'notServing', keys: ['v1_addr', 'tls_cert_file', 'tls_key_file'] })
    expect(bannerOf(configDoc({ saved: { v1_addr: '0.0.0.0:9443' }, running: {}, notServing: ['v1_addr'] }))).toEqual({ kind: 'notServing', keys: ['v1_addr'] })
  })

  it('still asks for the restart first when a setting is pending too, and says nothing of a listener when no daemon runs', () => {
    const both = configDoc({ saved: { v1_addr: '0.0.0.0:9443', max_concurrent: '8' }, running: { max_concurrent: ['4', 'default'] }, notServing: ['v1_addr'] })
    expect(bannerOf(both)).toEqual({ kind: 'restart', keys: ['max_concurrent'] })
    expect(bannerOf(configDoc({ saved: { v1_addr: '0.0.0.0:9443' }, notServing: ['v1_addr'] }))).toEqual({ kind: 'idle', keys: [] }) // no daemon: every state is not_running
  })

  it('is settled when the daemon runs, reports, and has nothing pending', () => {
    const pending = { max_concurrent: ['4', 'default'] }
    expect(restartSettled(configDoc({ saved: { max_concurrent: '8' }, running: {} }))).toBe(true)
    // a listener that is not up does not keep it from settling: the daemon took what is saved, and what the restart came to is said apart
    expect(restartSettled(configDoc({ saved: { v1_addr: '0.0.0.0:9443' }, running: {}, notServing: ['v1_addr'] }))).toBe(true)
    expect(restartSettled(configDoc({ saved: { max_concurrent: '8' }, running: pending }))).toBe(false)
    expect(restartSettled(configDoc({ saved: { max_concurrent: '8' } }))).toBe(false) // not running yet
    expect(restartSettled(configDoc({ running: 'old' }))).toBe(false) // running, but does not say what with
    expect(restartSettled(null)).toBe(false)
  })
})

describe('the settings whose dedicated listener is not up', () => {
  it('are the ones the CLI says are not_serving, in its order, and none for a document without them', () => {
    const doc = configDoc({ saved: { v1_addr: '0.0.0.0:9443', tls_key_file: '/a.key' }, running: {}, notServing: ['tls_key_file', 'v1_addr'] })
    expect(notServingKeys(doc)).toEqual(['v1_addr', 'tls_key_file'])
    expect(notServingKeys(configDoc({ running: {} }))).toEqual([])
    expect(notServingKeys(configDoc())).toEqual([]) // no daemon
    expect(notServingKeys(null)).toEqual([])
    expect(notServingKeys({})).toEqual([])
    expect(notServingKeys({ settings: [null, { key: 'v1_addr', state: 'not_serving' }] })).toEqual(['v1_addr'])
  })
})

describe('what a row has been edited to', () => {
  const doc = configDoc({ saved: { max_concurrent: '8', tls_cert_file: '/etc/api.pem', tls_key_file: '/etc/api.key' } })
  const by = byKey(doc)
  const one = row('max_concurrent')
  const tls = row('tls')

  it('shows the draft when there is one, else what is saved, else nothing', () => {
    expect(textOf('max_concurrent', {}, by)).toBe('8')
    expect(textOf('max_concurrent', { max_concurrent: '12' }, by)).toBe('12')
    expect(textOf('max_concurrent', { max_concurrent: '' }, by)).toBe('') // an emptied field is empty, not the saved value
    expect(textOf('turn_timeout', {}, by)).toBe('')
    expect(textOf('turn_timeout', { turn_timeout: '15m' }, by)).toBe('15m')
    expect(textOf('turn_timeout', {}, {})).toBe('')
  })

  it('counts a key as changed only when its draft differs from what is saved', () => {
    expect(dirtyKeys(one, {}, by)).toEqual([])
    expect(dirtyKeys(one, { max_concurrent: '8' }, by)).toEqual([])
    expect(dirtyKeys(one, { max_concurrent: '9' }, by)).toEqual(['max_concurrent'])
    expect(dirtyKeys(tls, { tls_key_file: '/etc/other.key' }, by)).toEqual(['tls_key_file'])
    expect(dirtyKeys(tls, { tls_key_file: '/etc/other.key', tls_cert_file: '/etc/other.pem', max_concurrent: '1' }, by)).toEqual(['tls_cert_file', 'tls_key_file'])
    expect(dirtyKeys(row('turn_timeout'), { turn_timeout: '' }, by)).toEqual([]) // nothing saved, nothing typed
  })

  it('can be saved when something changed and nothing that changed is blank', () => {
    expect(canSave(one, {}, by)).toBe(false)
    expect(canSave(one, { max_concurrent: '9' }, by)).toBe(true)
    expect(canSave(one, { max_concurrent: '' }, by)).toBe(false) // removing a value is "use the default"
    expect(canSave(one, { max_concurrent: '   ' }, by)).toBe(false)
    expect(canSave(tls, { tls_cert_file: '/etc/other.pem' }, by)).toBe(true) // the pair is checked by the CLI against what is saved
    expect(canSave(tls, { tls_cert_file: '/etc/other.pem', tls_key_file: '' }, by)).toBe(false)
  })

  it('sends what was typed for each key that changed, and only those', () => {
    expect(savePayload(one, { max_concurrent: ' 9 ' }, by)).toEqual({ max_concurrent: ' 9 ' }) // the Go side trims
    expect(savePayload(tls, { tls_cert_file: '/a.pem', tls_key_file: '/etc/api.key' }, by)).toEqual({ tls_cert_file: '/a.pem' })
    expect(savePayload(tls, {}, by)).toEqual({})
  })

  it('knows whether a row has anything saved to go back from, an invalid value included', () => {
    expect(hasSaved(one, by)).toBe(true)
    expect(hasSaved(tls, by)).toBe(true)
    expect(hasSaved(row('turn_timeout'), by)).toBe(false)
    expect(hasSaved(tls, byKey(configDoc({ saved: { tls_key_file: '/etc/api.key' } })))).toBe(true) // one of the two is enough
  })
})

describe('the problems of the saved settings', () => {
  const problems = [
    { key: 'max_concurrent', message: 'max_concurrent must be an integer from 1 to 64' },
    { key: 'tls_cert_file', message: 'tls_cert_file and tls_key_file must be set together' },
    { key: 'tls_key_file', message: 'tls_key_file is odd' },
    // the contract allows a problem of the document as a whole (key ""); the CLI sends none today: a row that cannot be read is an error
    { key: '', message: 'a problem of the saved settings as a whole' },
    { key: 'a_setting_of_the_future', message: 'a_setting_of_the_future is odd' },
  ]
  const doc = configDoc({ saved: { max_concurrent: 'abc', tls_cert_file: '/x.pem' }, problems })

  it('belong to the row of their setting, the TLS pair\'s two keys included', () => {
    expect(rowProblems(row('max_concurrent'), doc).map(p => p.key)).toEqual(['max_concurrent'])
    expect(rowProblems(row('tls'), doc).map(p => p.key)).toEqual(['tls_cert_file', 'tls_key_file'])
    expect(rowProblems(row('turn_timeout'), doc)).toEqual([])
  })

  it('are the block\'s own when they are of the document, or of a setting this page has no row for', () => {
    expect(otherProblems(doc).map(p => p.message)).toEqual(['a problem of the saved settings as a whole', 'a_setting_of_the_future is odd'])
    expect(otherProblems(configDoc())).toEqual([])
    expect(otherProblems(null)).toEqual([])
    expect(rowProblems(row('tls'), null)).toEqual([])
  })

  it('make a summary for the folded header: how many settings are saved, whether a restart is needed, how many problems', () => {
    expect(summary(doc)).toEqual({ saved: 2, restart: false, problems: 5 })
    const pending = configDoc({ saved: { max_concurrent: '8' }, running: { max_concurrent: ['4', 'default'] } })
    expect(summary(pending)).toEqual({ saved: 1, restart: true, problems: 0 })
    expect(summary(null)).toEqual({ saved: 0, restart: false, problems: 0 })
  })
})

describe('what a widening is about', () => {
  it('names the kind of each reason the CLI gives, for the listener it is about too', () => {
    expect(wideningHeading('v1_addr')).toBe('settings.api.config.widening.kind.v1_addr')
    expect(wideningHeading('confinement.loopback')).toBe('settings.api.config.widening.kind.confinement')
    expect(wideningHeading('confinement.network')).toBe('settings.api.config.widening.kind.confinement')
    expect(wideningHeading('context_confinement.loopback')).toBe('settings.api.config.widening.kind.context_confinement')
    expect(wideningHeading('auto_confinement.network')).toBe('settings.api.config.widening.kind.auto_confinement')
    expect(wideningHeading('image_runtimes')).toBe('settings.api.config.widening.kind.image_runtimes')
    expect(wideningHeading('tool_runtimes')).toBe('settings.api.config.widening.kind.tool_runtimes')
    expect(wideningHeading('saved_settings')).toBe('settings.api.config.widening.kind.saved_settings') // removing a row that cannot be read
  })

  it('has no name for a kind of its own to come, and the reason is shown all the same', () => {
    for (const k of ['', 'something_new', 'something_new.network', 'constructor', '__proto__', 'toString.network', undefined]) {
      expect(wideningHeading(k), String(k)).toBeNull()
    }
  })
})
