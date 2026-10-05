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

/**
 * @returns {{text: string, verbatim: boolean}}
 */
export function describeConfigError(e, t) {
  const { cls, msg } = classify(e)
  if (cls === 'invalid_input') {
    for (const [re, key, params] of RULES) {
      const m = re.exec(msg)
      if (m) return { text: t(key, params ? params(m) : undefined), verbatim: false }
    }
  }
  return describeApiError(e, t)
}
