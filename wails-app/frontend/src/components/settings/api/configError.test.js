import { describe, it, expect, beforeEach } from 'vitest'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { describeConfigError, isDamagedRow, isNotRegistered } from './configError.js'
import { DAMAGED, INVALID_SAVED, NEWER } from './__fixtures__/configFixtures.js'

// What the page receives when a call of the server settings fails: the CLI's last stderr line, led by the class of its
// exit code (app_api.go): "invalid_input: " for exit 3, nothing for the others. These are the CLI's own words
// (cmd/monoagentcli/api_config.go, internal/apiconfig, internal/openaiapi, internal/autostart).
const invalid = (msg) => new Error(`invalid_input: ${msg}`)
const WIDENING = 'this change makes the server reach further: The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine, and serve runtimes up to chat-only; it did not listen beyond this machine before. Pass --yes to make the change anyway.'
// A saved row the CLI cannot read is exit 3, and its message always starts the same way (apiconfig.DamagedMessage); one in a
// newer format is exit 1, with a message of its own (internal/apiconfig/store.go): DAMAGED and NEWER, in the fixtures.
const NOT_REGISTERED = 'the daemon is not registered for auto-start, so nothing can restart it: stop it and start `monoagentcli daemon` again, or run `monoagentcli daemon install` to have the system manage it'

const t = (k, o) => i18n.t(k, o)
const e = en.settings.api.config.errors
const s = es.settings.api.config.errors
beforeEach(async () => { await i18n.changeLanguage('en') })

describe('describeConfigError: the rules the CLI states, in the language of the page', () => {
  it('words each rule of a setting, taking the numbers from what the CLI says and not from this page', () => {
    expect(describeConfigError(invalid('max_concurrent must be an integer from 1 to 64'), t))
      .toEqual({ text: e.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'), verbatim: false })
    expect(describeConfigError(invalid('max_concurrent must be an integer from 2 to 32'), t).text)
      .toBe(e.maxConcurrent.replace('{{min}}', '2').replace('{{max}}', '32'))
    expect(describeConfigError(invalid('turn_timeout must be a duration of at least 10s, such as 15m'), t))
      .toEqual({ text: e.turnTimeout.replace('{{min}}', '10s').replace('{{example}}', '15m'), verbatim: false })
    expect(describeConfigError(invalid('turn_timeout must be a duration of at least 30s, such as 1h'), t).text)
      .toBe(e.turnTimeout.replace('{{min}}', '30s').replace('{{example}}', '1h'))
    for (const key of ['confinement', 'context_confinement', 'auto_confinement']) {
      expect(describeConfigError(invalid(`${key} must be chat-only, sandboxed or any`), t), key).toEqual({ text: e.class, verbatim: false })
    }
    expect(describeConfigError(invalid('image_runtimes must be a comma-separated list of runtime ids, such as codex,antigravity, or none alone to switch image generation off'), t).text).toBe(e.imageRuntimes)
    expect(describeConfigError(invalid('tool_runtimes must be a comma-separated list of runtime ids, such as claude,codex, or none alone to switch tool calling off'), t).text).toBe(e.toolRuntimes)
  })

  it('leaves out of an address what only Go can say about it: the page words the rule, and the address is in the field', () => {
    const r = describeConfigError(invalid('v1_addr must be host:port, such as 127.0.0.1:9443 or :9443: address nonsense: missing port in address'), t)
    expect(r).toEqual({ text: e.addr, verbatim: false })
    expect(r.text).not.toMatch(/nonsense|missing port/)
  })

  it('words a value that is missing, a TLS file without the other, a change that widened meanwhile, and a daemon nothing can restart', () => {
    expect(describeConfigError(invalid('tool_runtimes must not be empty: remove a saved value with unset'), t).text).toBe(e.empty)
    expect(describeConfigError(invalid('tls_cert_file and tls_key_file must be set together'), t).text).toBe(e.tlsPair)
    expect(describeConfigError(invalid(WIDENING), t).text).toBe(e.widenedMeanwhile)
    expect(describeConfigError(invalid(NOT_REGISTERED), t).text).toBe(e.notRegistered)
  })

  it('words a TLS file that is not an absolute path and a value with a control character, which the CLI refuses', () => {
    for (const key of ['tls_cert_file', 'tls_key_file']) {
      expect(describeConfigError(invalid(`${key} must be an absolute path (the daemon starts in another folder, and ~ is not expanded)`), t), key)
        .toEqual({ text: e.tlsAbsolute, verbatim: false })
    }
    for (const key of ['v1_addr', 'turn_timeout', 'tls_key_file']) {
      expect(describeConfigError(invalid(`${key} must not contain control characters`), t), key).toEqual({ text: e.controlChars, verbatim: false })
    }
    // the same words for a setting that has no such rule, from another class, or from the middle of a message, are not words for this page
    expect(describeConfigError(invalid('max_concurrent must be an absolute path'), t).verbatim).toBe(true)
    expect(describeConfigError(new Error('tls_cert_file must be an absolute path (the daemon starts in another folder)'), t).verbatim).toBe(true)
    expect(describeConfigError(invalid('a refusal: tls_cert_file must be an absolute path'), t).verbatim).toBe(true)
  })

  it('speaks the chosen language, with the same numbers', async () => {
    await i18n.changeLanguage('es')
    expect(describeConfigError(invalid('tls_cert_file must be an absolute path (the daemon starts in another folder, and ~ is not expanded)'), t).text).toBe(s.tlsAbsolute)
    expect(describeConfigError(invalid('v1_addr must not contain control characters'), t).text).toBe(s.controlChars)
    expect(s.tlsAbsolute).not.toBe(e.tlsAbsolute)
    expect(s.controlChars).not.toBe(e.controlChars)
    expect(describeConfigError(invalid('max_concurrent must be an integer from 1 to 64'), t).text).toBe(s.maxConcurrent.replace('{{min}}', '1').replace('{{max}}', '64'))
    expect(describeConfigError(invalid('turn_timeout must be a duration of at least 10s, such as 15m'), t).text).toBe(s.turnTimeout.replace('{{min}}', '10s').replace('{{example}}', '15m'))
    expect(describeConfigError(invalid(WIDENING), t).text).toBe(s.widenedMeanwhile)
    expect(describeConfigError(invalid(NOT_REGISTERED), t).text).toBe(s.notRegistered)
    expect(s.maxConcurrent).not.toBe(e.maxConcurrent)
    expect(s.tlsPair).not.toBe(e.tlsPair)
  })

  it('knows a rule only under the class of an invalid input: the same words from another failure are not that rule', () => {
    const words = 'max_concurrent must be an integer from 1 to 64'
    expect(describeConfigError(new Error(words), t)).toEqual({ text: words, verbatim: true })
    expect(describeConfigError(new Error(`not_found: ${words}`), t)).toEqual({ text: words, verbatim: true })
    expect(describeConfigError(new Error(WIDENING), t)).toEqual({ text: WIDENING, verbatim: true })
  })
})

describe('describeConfigError: what it does not know', () => {
  it('gives the CLI\'s own words, without the class, and says they are the CLI\'s', () => {
    expect(describeConfigError(invalid('not a setting: the settings are v1_addr, tls_cert_file'), t)).toEqual({ text: 'not a setting: the settings are v1_addr, tls_cert_file', verbatim: true })
    const damaged = 'the saved API settings (settings table, key api_gateway_config) are not a JSON object'
    expect(describeConfigError(new Error(damaged), t)).toEqual({ text: damaged, verbatim: true })
    expect(describeConfigError('database is locked', t)).toEqual({ text: 'database is locked', verbatim: true })
  })

  it('gives the CLI\'s own words for a saved value that fails its rule, which is the restart\'s refusal too: not the words of the rule it names', () => {
    expect(describeConfigError(invalid(INVALID_SAVED), t)).toEqual({ text: INVALID_SAVED, verbatim: true })
  })

  it('still words the cases of the keys, which share the CLI\'s classes', () => {
    expect(describeConfigError(invalid('an active key with that name already exists in this profile'), t))
      .toEqual({ text: en.settings.api.errors.nameTaken, verbatim: false })
  })

  it('is never empty', () => {
    for (const bad of [undefined, null, '', '   ', {}, { message: '' }, 42]) {
      expect(describeConfigError(bad, t), JSON.stringify(bad)).toEqual({ text: en.settings.api.errors.unknown, verbatim: false })
    }
  })
})

describe('isDamagedRow: a saved row the CLI cannot read, as the page tells it from the rest', () => {
  it('is the exit class of an invalid input and the start of the CLI\'s message, whatever is wrong with the row', () => {
    expect(isDamagedRow(invalid(DAMAGED))).toBe(true)
    expect(isDamagedRow(invalid('the saved settings are damaged (settings table, key api_gateway_config: max_concurrent must be a number or a string); `monoagentcli api config unset --all --yes` removes them'))).toBe(true)
    expect(isDamagedRow({ message: `invalid_input: ${DAMAGED}` })).toBe(true)
    expect(isDamagedRow(`invalid_input: ${DAMAGED}`)).toBe(true)
  })

  it('is not a row in a newer format (exit 1, which is never offered a reset), nor the same words from another class or place', () => {
    expect(isDamagedRow(new Error(NEWER))).toBe(false)
    expect(isDamagedRow(new Error(DAMAGED))).toBe(false) // no class: not exit 3
    expect(isDamagedRow(new Error(`not_found: ${DAMAGED}`))).toBe(false)
    expect(isDamagedRow(invalid(`a change refused: ${DAMAGED}`))).toBe(false) // the start of the message
    expect(isDamagedRow(invalid('The saved settings are damaged'))).toBe(false) // the CLI's words, not a lookalike
    expect(isDamagedRow(invalid(NEWER))).toBe(false)
  })

  it('is not any other failure, or nothing at all', () => {
    for (const other of [invalid('max_concurrent must be an integer from 1 to 64'), new Error('database is locked'), invalid(WIDENING), undefined, null, '', {}, 42]) {
      expect(isDamagedRow(other), JSON.stringify(other)).toBe(false)
    }
  })

  it('is said by describeConfigError, with the CLI\'s words as they are and no more than that, and only for damage', () => {
    expect(describeConfigError(invalid(DAMAGED), t)).toEqual({ text: DAMAGED, verbatim: true, damaged: true })
    for (const other of [new Error(NEWER), invalid('max_concurrent must be an integer from 1 to 64'), new Error('database is locked'), invalid(NOT_REGISTERED)]) {
      expect(describeConfigError(other, t)).not.toHaveProperty('damaged')
    }
  })

  it('keeps the CLI\'s words as the CLI said them in Spanish too: the page words what is around them', async () => {
    await i18n.changeLanguage('es')
    expect(describeConfigError(invalid(DAMAGED), t)).toEqual({ text: DAMAGED, verbatim: true, damaged: true })
  })
})

describe('isNotRegistered: the one refusal of the restart that says nothing is registered to restart', () => {
  it('is the exit class of an invalid input and the start of the CLI\'s message', () => {
    expect(isNotRegistered(invalid(NOT_REGISTERED))).toBe(true)
    expect(isNotRegistered({ message: `invalid_input: ${NOT_REGISTERED}` })).toBe(true)
    expect(isNotRegistered(`invalid_input: ${NOT_REGISTERED}`)).toBe(true)
  })

  it('is not any other refusal of that command, which are about the saved settings: a damaged row, a value that fails its rule', () => {
    expect(isNotRegistered(invalid(DAMAGED))).toBe(false)
    expect(isNotRegistered(invalid(INVALID_SAVED))).toBe(false)
    expect(isNotRegistered(invalid(WIDENING))).toBe(false)
    expect(isNotRegistered(invalid('max_concurrent must be an integer from 1 to 64'))).toBe(false)
    expect(isNotRegistered(new Error(NEWER))).toBe(false) // exit 1, no class
  })

  it('is not the same words from another class or from the middle of another message, nor nothing at all', () => {
    expect(isNotRegistered(new Error(NOT_REGISTERED))).toBe(false) // no class: not exit 3
    expect(isNotRegistered(new Error(`not_found: ${NOT_REGISTERED}`))).toBe(false)
    expect(isNotRegistered(invalid(`restart refused: ${NOT_REGISTERED}`))).toBe(false) // the start of the message
    for (const other of [undefined, null, '', '   ', {}, { message: '' }, 42]) expect(isNotRegistered(other), JSON.stringify(other)).toBe(false)
  })
})
