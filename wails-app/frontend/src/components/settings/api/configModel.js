// Pure helpers of the "Server settings" block: which rows it has, what each setting's state and running value are,
// what a row has been edited to, and what the banner about a restart says. No React and no calls: everything is a
// function of what `api config show` said (the CLI is the only judge of a value, of what widens the server and of
// where a setting stands against the daemon: this file reads those answers and decides nothing of them).
//
// Strings are named in full ('settings.api.config…'), never built from parts, which is what lets the test of the
// locales see which of them are used.

const hasOwn = (o, k) => Object.prototype.hasOwnProperty.call(o, k)

// One row for each setting, in the order of the CLI, except that the two TLS files are one: `set` refuses a result with one
// and not the other (they are one setting in two keys), so they are saved, and put back to the default, together.
// kind: what the control is (a select for the three classes, a number for a count, a text for an address, a path,
// a duration or a list). `empty` is what the field says while nothing is saved, for the settings whose default is
// no value (the others say what the default is).
export const ROWS = [
  { id: 'v1_addr', keys: ['v1_addr'], kind: 'text', label: 'settings.api.config.rows.v1_addr.label', hint: 'settings.api.config.rows.v1_addr.hint', empty: 'settings.api.config.rows.v1_addr.empty' },
  {
    id: 'tls', keys: ['tls_cert_file', 'tls_key_file'], kind: 'pair', label: 'settings.api.config.rows.tls.label', hint: 'settings.api.config.rows.tls.hint', empty: 'settings.api.config.rows.tls.empty',
    parts: [
      { key: 'tls_cert_file', label: 'settings.api.config.rows.tls.certLabel' },
      { key: 'tls_key_file', label: 'settings.api.config.rows.tls.keyLabel' },
    ],
  },
  { id: 'confinement', keys: ['confinement'], kind: 'class', label: 'settings.api.config.rows.confinement.label', hint: 'settings.api.config.rows.confinement.hint', empty: 'settings.api.config.rows.confinement.empty' },
  { id: 'context_confinement', keys: ['context_confinement'], kind: 'class', label: 'settings.api.config.rows.context_confinement.label', hint: 'settings.api.config.rows.context_confinement.hint' },
  { id: 'auto_confinement', keys: ['auto_confinement'], kind: 'class', label: 'settings.api.config.rows.auto_confinement.label', hint: 'settings.api.config.rows.auto_confinement.hint' },
  { id: 'max_concurrent', keys: ['max_concurrent'], kind: 'number', label: 'settings.api.config.rows.max_concurrent.label', hint: 'settings.api.config.rows.max_concurrent.hint' },
  { id: 'turn_timeout', keys: ['turn_timeout'], kind: 'text', label: 'settings.api.config.rows.turn_timeout.label', hint: 'settings.api.config.rows.turn_timeout.hint' },
  { id: 'image_runtimes', keys: ['image_runtimes'], kind: 'text', label: 'settings.api.config.rows.image_runtimes.label', hint: 'settings.api.config.rows.image_runtimes.hint' },
  { id: 'tool_runtimes', keys: ['tool_runtimes'], kind: 'text', label: 'settings.api.config.rows.tool_runtimes.label', hint: 'settings.api.config.rows.tool_runtimes.hint' },
]

/** The settings of a document by key. */
export function byKey(doc) {
  const out = {}
  for (const s of Array.isArray(doc?.settings) ? doc.settings : []) if (s && typeof s.key === 'string') out[s.key] = s
  return out
}

// Where a setting stands against the running daemon (the CLI's `state`), as the badge says it. A state this page does
// not know (a later CLI's) is unknown: nothing is claimed.
const STATES = {
  applied: { id: 'applied', tone: 'ok', text: 'settings.api.config.state.applied', hint: 'settings.api.config.stateHint.applied' },
  pending_restart: { id: 'pending', tone: 'warn', text: 'settings.api.config.state.pending', hint: 'settings.api.config.stateHint.pending' },
  overridden: { id: 'overridden', tone: 'hot', text: 'settings.api.config.state.overridden', hint: 'settings.api.config.stateHint.overridden' },
  // The daemon took the value, which is what is saved, but the dedicated listener it describes is not up (the CLI says it for
  // v1_addr and the two TLS files only): the daemon's log says why.
  not_serving: { id: 'notServing', tone: 'bad', text: 'settings.api.config.state.notServing', hint: 'settings.api.config.stateHint.notServing' },
  not_running: { id: 'notRunning', tone: 'muted', text: 'settings.api.config.state.notRunning', hint: 'settings.api.config.stateHint.notRunning' },
  unknown: { id: 'unknown', tone: 'muted', text: 'settings.api.config.state.unknown', hint: 'settings.api.config.stateHint.unknown' },
}

export function stateInfo(setting) {
  const s = setting?.state
  return typeof s === 'string' && hasOwn(STATES, s) ? STATES[s] : STATES.unknown
}

// The states, most in need of attention first: a row of two settings (the TLS files) shows the first of theirs. A listener that
// is not up needs about the attention of a pending restart, which still comes first (the restart is what is to be done, and
// may bring the listener up), and more than the states that say nothing is wrong or that nothing is known.
const ATTENTION = ['overridden', 'pending_restart', 'not_serving', 'unknown', 'not_running', 'applied']

/** The setting of a row whose state the badge shows: the only one, or of the TLS pair the one that needs most attention. undefined when the CLI listed none. */
export function rowState(row, settings) {
  const rank = (s) => { const i = ATTENTION.indexOf(s.state); return i < 0 ? ATTENTION.indexOf('unknown') : i }
  return row.keys.map(k => settings?.[k]).filter(Boolean).sort((a, b) => rank(a) - rank(b))[0]
}

/**
 * What the running daemon says it started with, and where that came from: null when no live daemon reports the
 * setting. `value` may be '' (it runs the setting with none). `name` is the flag or the variable it came from,
 * '' when the source is the saved value or the default.
 */
export function runningInfo(setting) {
  if (typeof setting?.running !== 'string') return null
  const kind = setting.running_source || ''
  return { value: setting.running, kind, name: kind === 'flag' ? setting.server_flag || '' : kind === 'env' ? setting.env || '' : '' }
}

/** What a daemon's own flag or variable is doing to a setting (its saved value has no effect), or null when nothing is. */
export function overriddenInfo(setting) {
  if (setting?.state !== 'overridden') return null
  if (setting.running_source === 'flag') return { kind: 'flag', name: setting.server_flag || '' }
  if (setting.running_source === 'env') return { kind: 'env', name: setting.env || '' }
  return { kind: '', name: '' }
}

/** The keys of the settings whose dedicated listener the running daemon could not bring up (the CLI's `not_serving`), in the CLI's order. */
export function notServingKeys(doc) {
  return (Array.isArray(doc?.settings) ? doc.settings : []).filter(s => s?.state === 'not_serving').map(s => s.key)
}

/**
 * What the banner about applying the settings says, from the document alone, as the CLI's own text does:
 *   restart     some setting is pending: the daemon started before it was saved (keys says which)
 *   notServing  the daemon runs what is saved, but a dedicated listener it was given is not up (keys says which)
 *   older       a daemon runs that predates the report, so what it runs is unknown: restart it to be sure
 *   idle        no daemon runs: the server reads the settings when it starts
 *   none        the daemon runs what is saved (or is given its own flags)
 * A pending restart is said before a listener that is not up, as the rows rank them.
 */
export function bannerOf(doc) {
  if (!doc?.daemon?.running) return { kind: 'idle', keys: [] }
  const pending = (Array.isArray(doc.settings) ? doc.settings : []).filter(s => s.state === 'pending_restart').map(s => s.key)
  if (doc.restart_needed || pending.length) return { kind: 'restart', keys: pending }
  const down = notServingKeys(doc)
  if (down.length) return { kind: 'notServing', keys: down }
  if (!doc.daemon.reports_settings) return { kind: 'older', keys: [] }
  return { kind: 'none', keys: [] }
}

/**
 * The daemon is back and took what is saved: it runs, reports its settings, and nothing is pending. That says nothing of
 * the dedicated listeners it was given, which may not be up (see notServingKeys): what the restart came to says that.
 */
export function restartSettled(doc) {
  return !!doc?.daemon?.running && !!doc.daemon.reports_settings && !doc.restart_needed
}

const savedOf = (key, settings) => settings?.[key]?.saved ?? ''

/** What a key's field shows: what was typed when something was, else what is saved, else nothing. */
export function textOf(key, drafts, settings) {
  return hasOwn(drafts, key) ? drafts[key] : savedOf(key, settings)
}

/** The keys of a row whose draft differs from what is saved. */
export function dirtyKeys(row, drafts, settings) {
  return row.keys.filter(k => hasOwn(drafts, k) && drafts[k] !== savedOf(k, settings))
}

// Saving needs a change, and no changed field empty: an emptied field is not a value (the CLI says to use unset),
// which is what "Use the default" is for.
export function canSave(row, drafts, settings) {
  const dirty = dirtyKeys(row, drafts, settings)
  return dirty.length > 0 && dirty.every(k => String(drafts[k]).trim() !== '')
}

/** What a save sends: the text of each changed key, as typed (the Go side trims it; the CLI judges it). */
export function savePayload(row, drafts, settings) {
  return Object.fromEntries(dirtyKeys(row, drafts, settings).map(k => [k, drafts[k]]))
}

/** The row has something saved to go back from. A value that fails its rule counts: it is shown as stored. */
export function hasSaved(row, settings) {
  return row.keys.some(k => savedOf(k, settings) !== '')
}

const rowKeys = new Set(ROWS.flatMap(r => r.keys))

/** The problems of the saved settings that belong to a row's settings. */
export function rowProblems(row, doc) {
  return (Array.isArray(doc?.problems) ? doc.problems : []).filter(p => row.keys.includes(p.key))
}

/** The problems that are the document's own (no setting named) or of a setting this page has no row for. */
export function otherProblems(doc) {
  return (Array.isArray(doc?.problems) ? doc.problems : []).filter(p => !rowKeys.has(p.key))
}

/** What the folded header says: how many settings are saved, whether a restart is needed, how many problems. */
export function summary(doc) {
  const settings = Array.isArray(doc?.settings) ? doc.settings : []
  return {
    saved: settings.filter(s => typeof s.saved === 'string' && s.saved !== '').length,
    restart: bannerOf(doc).kind === 'restart',
    problems: Array.isArray(doc?.problems) ? doc.problems.length : 0,
  }
}

// What each way of reaching further is about, in plain words, above the CLI's own sentence about it. The CLI's keys are
// a kind, and for the three classes the listener it is about after a dot (confinement.network). saved_settings is the
// removal of a saved row that cannot be read: what it limited cannot be told.
const KINDS = {
  v1_addr: 'settings.api.config.widening.kind.v1_addr',
  confinement: 'settings.api.config.widening.kind.confinement',
  context_confinement: 'settings.api.config.widening.kind.context_confinement',
  auto_confinement: 'settings.api.config.widening.kind.auto_confinement',
  image_runtimes: 'settings.api.config.widening.kind.image_runtimes',
  tool_runtimes: 'settings.api.config.widening.kind.tool_runtimes',
  saved_settings: 'settings.api.config.widening.kind.saved_settings',
}

/** The string that names a kind of widening, or null for a kind this page does not know (its reason is shown all the same). */
export function wideningHeading(key) {
  if (typeof key !== 'string') return null
  const kind = key.split('.')[0]
  return hasOwn(KINDS, kind) ? KINDS[kind] : null
}
