// Realistic documents of the server settings for the API section's tests: the shapes of `api config show --json`
// and of `api config set|unset --json` (internal/apiconfig/report.go, apply.go) as the app's bindings pass them on
// (app_api_config.go). A document is computed the way the CLI computes it, so that a fixture is consistent with
// itself: what is saved gives `effective` and `source`, what the daemon started with gives `state`, and one state
// pending makes `restart_needed`. A field that does not apply is absent, as in the CLI's JSON.

export const KEYS = [
  'v1_addr', 'tls_cert_file', 'tls_key_file', 'confinement', 'context_confinement', 'auto_confinement',
  'max_concurrent', 'turn_timeout', 'image_runtimes', 'tool_runtimes',
]

const SPECS = {
  v1_addr: { flag: '--v1-addr', env: 'MONOAGENT_API_V1_ADDR', def: '' },
  tls_cert_file: { flag: '', env: 'MONOAGENT_API_TLS_CERT', def: '' },
  tls_key_file: { flag: '', env: 'MONOAGENT_API_TLS_KEY', def: '' },
  confinement: { flag: '--confinement', env: 'MONOAGENT_API_CONFINEMENT', def: '' },
  context_confinement: { flag: '--context-confinement', env: 'MONOAGENT_API_CONTEXT_CONFINEMENT', def: 'chat-only' },
  auto_confinement: { flag: '--auto-confinement', env: 'MONOAGENT_API_AUTO_CONFINEMENT', def: 'chat-only' },
  max_concurrent: { flag: '--max-concurrent', env: 'MONOAGENT_API_MAX_CONCURRENT', def: '4' },
  turn_timeout: { flag: '', env: 'MONOAGENT_API_TURN_TIMEOUT', def: '10m' },
  image_runtimes: { flag: '', env: 'MONOAGENT_API_IMAGE_RUNTIMES', def: 'codex,antigravity' },
  tool_runtimes: { flag: '', env: 'MONOAGENT_API_TOOL_RUNTIMES', def: 'claude,codex' },
}

export const DEFAULTS = Object.fromEntries(KEYS.map(k => [k, SPECS[k].def]))

/**
 * `api config show --json`.
 * @param {object} o
 * @param {Object<string,string>} [o.saved] key to saved text (an invalid text is kept as stored, as the CLI shows it)
 * @param {null|'old'|Object<string,[string,string]>} [o.running] null: no daemon runs. 'old': one runs and predates the
 *   report. An object: one runs and reports; it started with what is saved now (the saved value, else the default)
 *   unless the object says otherwise for a key: [value, source] with a source of flag, env, saved or default.
 * @param {boolean} [o.autostart] the daemon is registered for auto-start
 * @param {Array<{key: string, message: string}>} [o.problems]
 */
export function configDoc({ saved = {}, running = null, autostart = false, problems = [], environment = 'shell' } = {}) {
  const settings = KEYS.map((key) => {
    const { flag, env, def } = SPECS[key]
    const s = saved[key] || ''
    const effective = s || def
    const row = { key, server_flag: flag, env, ...(s ? { saved: s } : {}), default: def, effective, source: s ? 'saved' : 'default' }
    if (running === null) return { ...row, state: 'not_running' }
    if (running === 'old') return { ...row, state: 'unknown' }
    const [value, source] = running[key] || [effective, s ? 'saved' : 'default']
    const state = source === 'flag' || source === 'env' ? 'overridden' : value === effective ? 'applied' : 'pending_restart'
    return { ...row, running: value, running_source: source, state }
  })
  return {
    v: 1, environment, settings,
    daemon: { running: running !== null, reports_settings: running !== null && running !== 'old', autostart },
    restart_needed: settings.some(s => s.state === 'pending_restart'),
    problems,
  }
}

/** `api config set|unset --json`: the document of the state after (or, with applied false, the state a dry run would give) and three fields. */
export function changeDoc(doc, { applied = true, changed = [], widening = [] } = {}) {
  return { ...doc, applied, changed, widening }
}

/** What the CLI says of a change that makes the server reach further: its words, one sentence each (internal/apiconfig/widen.go). */
export const WIDENING = {
  v1_addr: { key: 'v1_addr', reason: 'The dedicated /v1 listener would listen on 0.0.0.0:9443, beyond this machine, and serve runtimes up to chat-only; it did not listen beyond this machine before.' },
  confinementNetwork: { key: 'confinement.network', reason: 'A /v1 listener beyond this machine (a dedicated listener from v1_addr, --v1-addr or MONOAGENT_API_V1_ADDR) would serve runtimes up to any, where it served up to chat-only.' },
  contextLoopback: { key: 'context_confinement.loopback', reason: 'A key created with --context could use runtimes up to sandboxed on the /v1 listener on this machine, where it could use up to chat-only.' },
  autoNetwork: { key: 'auto_confinement.network', reason: 'The auto model could pick runtimes up to sandboxed on a /v1 listener beyond this machine, where it could pick up to chat-only.' },
  imageRuntimes: { key: 'image_runtimes', reason: 'Image generation would be served by copilot, beyond the default list (codex, antigravity).' },
  toolRuntimes: { key: 'tool_runtimes', reason: 'Tool calling, which is switched off, would be served by claude, codex.' },
}
