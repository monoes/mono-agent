import { describe, it, expect, beforeEach } from 'vitest'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { describeConfigError } from './configError.js'

// What the page receives when a call of the server settings fails: the CLI's last stderr line, led by the class of its
// exit code (app_api.go): "invalid_input: " for exit 3, nothing for the others. These are the CLI's own words
// (cmd/monoagentcli/api_config.go, internal/apiconfig, internal/openaiapi, internal/autostart).
const invalid = (msg) => new Error(`invalid_input: ${msg}`)
const WIDENING = 'this change makes the server reach further: The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine, and serve runtimes up to chat-only; it did not listen beyond this machine before. Pass --yes to make the change anyway.'
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

  it('speaks the chosen language, with the same numbers', async () => {
    await i18n.changeLanguage('es')
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
