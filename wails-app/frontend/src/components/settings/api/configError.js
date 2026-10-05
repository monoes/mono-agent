import { classify, describeApiError } from './apiError.js'

// describeConfigError is what the page shows for a failed call of the server settings or of the daemon's restart.
//
// The CLI judges every value, and says why in one English line (internal/apiconfig, internal/openaiapi,
// internal/autostart), which reaches the page led by the class of its exit code (see apiError.js). The ones a
// person meets while editing a setting are worded here in the language of the page. The numbers a rule names
// (the range of a count, the shortest timeout) are taken from what the CLI says and not written into the
// translations, so a CLI that changes a limit is not contradicted. An address is worded without what only Go says
// about it ("missing port in address"): the field holds what was typed. Anything else is the CLI's own words, and
// `verbatim` says so, so that the page can tell a reader of another language that they are English.

// [what the CLI says, the string that words it, what the string takes from the CLI's words]
const RULES = [
  [/^max_concurrent must be an integer from (\d+) to (\d+)/, 'settings.api.config.errors.maxConcurrent', m => ({ min: m[1], max: m[2] })],
  [/^turn_timeout must be a duration of at least (\S+), such as (\S+)/, 'settings.api.config.errors.turnTimeout', m => ({ min: m[1], example: m[2] })],
  [/^(?:confinement|context_confinement|auto_confinement) must be chat-only, sandboxed or any/, 'settings.api.config.errors.class'],
  [/^v1_addr must be host:port/, 'settings.api.config.errors.addr'],
  [/^image_runtimes must be a comma-separated list/, 'settings.api.config.errors.imageRuntimes'],
  [/^tool_runtimes must be a comma-separated list/, 'settings.api.config.errors.toolRuntimes'],
  [/^\w+ must not be empty/, 'settings.api.config.errors.empty'],
  [/^tls_cert_file and tls_key_file must be set together/, 'settings.api.config.errors.tlsPair'],
  [/^this change makes the server reach further/, 'settings.api.config.errors.widenedMeanwhile'],
  [/^the daemon is not registered for auto-start/, 'settings.api.config.errors.notRegistered'],
]

// A saved row the CLI cannot read (not JSON, a version that is not a whole number, a known field of the wrong type) is an
// error of every command that reads it, and the CLI maps it to exit 3 with a message that always starts the same way
// (apiconfig.DamagedMessage), whatever is wrong with the row, so that a caller that has only the exit class and the text
// can tell it from any other failure and offer the one repair: remove the saved settings (`unset --all`, with --yes). A row
// in a newer format is not this (exit 1, a message of its own, never removed), and neither is a failing database.
const DAMAGED = /^the saved settings are damaged/

/** Whether a failed call of the server settings says the saved row cannot be read (exit 3 and the CLI's words). */
export function isDamagedRow(e) {
  const { cls, msg } = classify(e)
  return cls === 'invalid_input' && DAMAGED.test(msg)
}

/**
 * `damaged` is present, and true, only for a saved row that cannot be read; its text is the CLI's own words.
 * @returns {{text: string, verbatim: boolean, damaged?: true}}
 */
export function describeConfigError(e, t) {
  const { cls, msg } = classify(e)
  if (cls === 'invalid_input') {
    for (const [re, key, params] of RULES) {
      const m = re.exec(msg)
      if (m) return { text: t(key, params ? params(m) : undefined), verbatim: false }
    }
  }
  const said = describeApiError(e, t)
  return isDamagedRow(e) ? { ...said, damaged: true } : said
}
