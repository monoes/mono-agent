import { describe, it, expect, beforeEach } from 'vitest'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import { apiError } from './apiError.js'

// What the app's Go side hands the page: the CLI's last stderr line, led by the class of its exit
// code (app_api.go): "not_found: " for exit 2, "invalid_input: " for exit 3, nothing for the rest.
const NAME_TAKEN = 'an active key with that name already exists in this profile'
const NAME_INVALID = "key name must be 1-64 characters: letters, digits, space, '.', '_' or '-', starting with a letter or digit, and not the shape of a key id (key_ followed by 12 characters from a-z and 2-7)"
const NOT_FOUND = 'api key not found'

const t = (k, o) => i18n.t(k, o)
beforeEach(async () => { await i18n.changeLanguage('en') })

describe('apiError', () => {
  it('words the failures the keys can meet, in the chosen language, instead of the CLI\'s English', async () => {
    expect(apiError(`invalid_input: ${NAME_TAKEN}`, t)).toBe(en.settings.api.errors.nameTaken)
    expect(apiError(`invalid_input: ${NAME_INVALID}`, t)).toBe(en.settings.api.errors.nameInvalid)
    expect(apiError(`not_found: ${NOT_FOUND}`, t)).toBe(en.settings.api.errors.keyNotFound)
    await i18n.changeLanguage('es')
    expect(apiError(`invalid_input: ${NAME_TAKEN}`, t)).toBe(es.settings.api.errors.nameTaken)
    expect(apiError(`invalid_input: ${NAME_INVALID}`, t)).toBe(es.settings.api.errors.nameInvalid)
    expect(apiError(`not_found: ${NOT_FOUND}`, t)).toBe(es.settings.api.errors.keyNotFound)
    expect(es.settings.api.errors.nameTaken).not.toBe(en.settings.api.errors.nameTaken)
  })

  it('takes an Error as well as the text Wails rejects with', () => {
    expect(apiError(new Error(`not_found: ${NOT_FOUND}`), t)).toBe(en.settings.api.errors.keyNotFound)
    expect(apiError({ message: `invalid_input: ${NAME_TAKEN}` }, t)).toBe(en.settings.api.errors.nameTaken)
  })

  it('tells exit code 2 from exit code 3: the same words under the other class are not a known case', () => {
    expect(apiError(`not_found: ${NAME_TAKEN}`, t)).toBe(NAME_TAKEN)
    expect(apiError(`invalid_input: ${NOT_FOUND}`, t)).toBe(NOT_FOUND)
    expect(apiError(`invalid_input: ${NAME_INVALID.replace('key name must be', 'key names must be')}`, t)).toMatch(/^key names must be/)
  })

  it('gives the CLI\'s own words, without the class, for what it does not know', () => {
    expect(apiError('not_found: no such thing', t)).toBe('no such thing')
    expect(apiError('invalid_input: bad flag --nope', t)).toBe('bad flag --nope')
  })

  it('gives what has no class as it is: the other exit codes, and the app\'s own errors', () => {
    expect(apiError('monoagentcli not found: install it or set MONOAGENTCLI_BIN', t)).toBe('monoagentcli not found: install it or set MONOAGENTCLI_BIN')
    expect(apiError('initializing database: disk full', t)).toBe('initializing database: disk full')
    expect(apiError(new Error('boom'), t)).toBe('boom')
  })

  it('reads a class only at the start of the message, and only the two the CLI names', () => {
    expect(apiError(`the call failed: not_found: ${NOT_FOUND}`, t)).toBe(`the call failed: not_found: ${NOT_FOUND}`)
    expect(apiError(`auth_failed: ${NOT_FOUND}`, t)).toBe(`auth_failed: ${NOT_FOUND}`)
    expect(apiError(`NOT_FOUND: ${NOT_FOUND}`, t)).toBe(`NOT_FOUND: ${NOT_FOUND}`)
  })

  it('trims, and is never empty', () => {
    expect(apiError(`  not_found: ${NOT_FOUND}\n`, t)).toBe(en.settings.api.errors.keyNotFound)
    for (const e of [undefined, null, '', '   ', {}, { message: '' }, 42]) {
      expect(apiError(e, t), JSON.stringify(e)).toBe(en.settings.api.errors.unknown)
    }
  })
})
