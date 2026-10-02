// Pure helpers of the API settings section: which listener the header describes,
// what state it is in, the base URL, the arguments of `api models` and the
// time formats. No React and no calls: everything is a function of what the CLI said.

// The values of --confinement, --context-confinement and --auto-confinement.
export const CLASSES = ['chat-only', 'sandboxed', 'any']

// pickListener is the listener the header describes: the main one when it is
// loopback and serves /v1, else the dedicated --v1-addr one. With neither, the
// main one (off loopback, or not mounted by the daemon), only to say why /v1 is
// not served; null when `api status` lists no listener.
export function pickListener(status) {
  const ls = Array.isArray(status?.listeners) ? status.listeners : []
  const main = ls.find(l => l?.name === 'main')
  const dedicated = ls.find(l => l?.name === 'v1')
  if (main?.loopback && main?.v1) return main
  return dedicated || main || null
}

// listenerState says what `api status` found at a listener: the cases of its
// listenerNote, in its order. The daemon's refusal to mount /v1 on a loopback
// main listener reaches the JSON as v1:false on a loopback listener.
//   none               no listener at all
//   down               nothing answers /health
//   no-v1-daemon       reachable, but the daemon does not serve /v1 on it
//   no-v1-offloopback  reachable, bound off loopback: /v1 needs --v1-addr
//   stale              answers /health but not /v1: a server that predates the API
//   serving            serves /v1
export function listenerState(l) {
  if (!l) return 'none'
  if (!l.reachable) return 'down'
  if (!l.v1) return l.loopback ? 'no-v1-daemon' : 'no-v1-offloopback'
  return l.v1_answers ? 'serving' : 'stale'
}

// baseURL is where a client points its base_url, or null for a listener that is
// not meant to serve /v1. `api status` reports no scheme, so it is derived: the
// main listener is plain HTTP, a dedicated one is TLS off loopback and plain on
// loopback (unless the server has MONOAGENT_API_TLS_CERT set, which `api status`
// cannot tell). A wildcard host is not an address a client can use: it is shown
// as localhost, and `wildcard` says so.
export function baseURL(l) {
  if (!l?.v1 || !l.addr) return null
  const m = /^(\[[^\]]*\]|[^:[\]]*):(\d+)$/.exec(String(l.addr).trim())
  if (!m) return null
  const bare = m[1].replace(/^\[|\]$/g, '')
  const wildcard = bare === '' || bare === '0.0.0.0' || bare === '::'
  const tls = l.name === 'v1' && !l.loopback
  return { url: `${tls ? 'https' : 'http'}://${wildcard ? 'localhost' : m[1]}:${m[2]}/v1`, tls, wildcard }
}

// modelsArgs are the arguments of APIModels: [for, confinement, contextConfinement,
// autoConfinement], from the listener the header describes, so that the table
// shows what the running server applies and not this app's environment. Without
// such a listener, or for a value that is missing or not a class (a CLI that
// predates --auto-confinement), the CLI's own default stands ('').
export function modelsArgs(l) {
  if (!l?.v1) return ['', '', '', '']
  const known = v => (CLASSES.includes(v) ? v : '')
  return [l.loopback ? 'loopback' : 'network', known(l.confinement), known(l.context_confinement), known(l.auto_confinement)]
}

// missingIsAboutJev: what `api models` says auto is missing is for the Jev
// section to fix (the api_auto surface, a Jev key), not the server's flags.
export function missingIsAboutJev(missing) {
  return /jev|api_auto/i.test(missing || '')
}

const UNITS = [['year', 365 * 86400], ['month', 30 * 86400], ['week', 7 * 86400], ['day', 86400], ['hour', 3600], ['minute', 60]]

function relativeFormat(lng) {
  try { return new Intl.RelativeTimeFormat(lng, { numeric: 'auto' }) } catch { return new Intl.RelativeTimeFormat('en', { numeric: 'auto' }) }
}

// relativeTime is "3 minutes ago" in the language, '' for no date. A key's
// last use is recorded at most once a minute, so under 45 seconds is just "now".
export function relativeTime(iso, lng = 'en', now = Date.now()) {
  const t = Date.parse(iso)
  if (!iso || Number.isNaN(t)) return ''
  const diff = Math.round((t - now) / 1000)
  const rtf = relativeFormat(lng)
  if (Math.abs(diff) < 45) return rtf.format(0, 'second')
  const [unit, secs] = UNITS.find(([, s]) => Math.abs(diff) >= s) || ['minute', 60]
  return rtf.format(Math.round(diff / secs), unit)
}

// formatDate is a short date ("Oct 1, 2026") in the language, '' for no date.
export function formatDate(iso, lng = 'en') {
  const t = Date.parse(iso)
  if (!iso || Number.isNaN(t)) return ''
  try { return new Date(t).toLocaleDateString(lng, { year: 'numeric', month: 'short', day: 'numeric' }) } catch { return new Date(t).toLocaleDateString('en', { year: 'numeric', month: 'short', day: 'numeric' }) }
}
