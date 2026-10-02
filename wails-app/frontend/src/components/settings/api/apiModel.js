// Pure helpers of the API settings section: which listener the header describes,
// what state it is in, the base URL, the arguments of `api models` and the
// time formats. No React and no calls: everything is a function of what the CLI said.

// The values of --confinement, --context-confinement and --auto-confinement.
export const CLASSES = ['chat-only', 'sandboxed', 'any']

// policyClass spells a model class as the policy that allows it. The CLI says
// unconfined for the class and any for the policy: a listener's confinement is
// "any", while auto.confinement of `api models` is "unconfined" for the same thing.
export const policyClass = (c) => (c === 'unconfined' ? 'any' : c)

const listenersOf = (status) => (Array.isArray(status?.listeners) ? status.listeners.filter(l => l && typeof l === 'object') : [])

// servingListeners is every listener that is meant to serve /v1, in the order
// `api status` lists them (the main one first). Each is shown: one that is
// bound beyond loopback is never left out in favour of one that is not.
export function servingListeners(status) {
  return listenersOf(status).filter(l => l.v1)
}

// pickListener is the one listener the page evaluates the models and the keys'
// context hint against, and the header's chip describes: of the listeners that
// serve /v1, one that answers /v1, else one that answers /health, else the first
// listed. With none serving, the main one (off loopback, or not mounted by the
// daemon), only to say why /v1 is not served; null when `api status` lists no listener.
export function pickListener(status) {
  const rank = (l) => (l.reachable && l.v1_answers ? 2 : l.reachable ? 1 : 0)
  let best = null
  for (const l of servingListeners(status)) if (!best || rank(l) > rank(best)) best = l
  if (best) return best
  const ls = listenersOf(status)
  return ls.find(l => l.name === 'main') || ls[0] || null
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

const LABEL = '[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?'
const OCTET = '(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)'
const HOSTNAME = new RegExp(`^${LABEL}(?:\\.${LABEL})*$`)
const IPV4 = new RegExp(`^${OCTET}(?:\\.${OCTET}){3}$`)

// isIPv6 is the text of an IPv6 address without zone: up to eight groups of hex
// digits, one "::" at most, and an IPv4 address allowed in the last two groups.
function isIPv6(s) {
  if (!/^[0-9A-Fa-f:.]+$/.test(s)) return false
  let groups = s
  if (s.includes('.')) {
    const at = s.lastIndexOf(':')
    if (!IPV4.test(s.slice(at + 1))) return false
    groups = s.slice(0, at + 1) + '0:0' // the two groups the IPv4 address takes
  }
  const halves = groups.split('::')
  if (halves.length > 2) return false
  const parts = (h) => (h === '' ? [] : h.split(':'))
  const all = [...parts(halves[0]), ...(halves.length === 2 ? parts(halves[1]) : [])]
  if (!all.every(g => /^[0-9A-Fa-f]{1,4}$/.test(g))) return false
  return halves.length === 2 ? all.length <= 7 : all.length === 8
}

// isHost: a host name (letters, digits, hyphens and dots, whose last label is not a
// number: a number there is an IPv4 address, and then a valid one), an IPv4
// address, or an IPv6 address in brackets. Nothing else is part of a base URL, so
// that what is copied never names a host the listener does not bind.
function isHost(h) {
  if (h.startsWith('[')) return h.endsWith(']') && isIPv6(h.slice(1, -1))
  if (IPV4.test(h)) return true
  return h.length <= 253 && HOSTNAME.test(h) && !/^\d+$/.test(h.slice(h.lastIndexOf('.') + 1))
}

// baseURL is where a client points its base_url, or null for a listener that is
// not meant to serve /v1 and for an address that is not a host and a port (1 to
// 65535): the page copies it, so it never copies a URL the address could have
// bent to another host. The scheme is the one `api status` saw answer; for a
// listener it could not reach (or a CLI that predates it) the scheme is derived:
// the main listener is plain HTTP, a dedicated one is TLS off loopback and plain
// on loopback (unless the server has MONOAGENT_API_TLS_CERT set, which only a
// probe can tell). A wildcard host is not an address a client can use: it is
// shown as localhost, and `wildcard` says so.
export function baseURL(l) {
  if (!l?.v1 || !l.addr) return null
  const m = /^(\[[^\]]*\]|[^:[\]]*):(\d{1,5})$/.exec(String(l.addr))
  if (!m) return null
  const [, host, p] = m
  const port = Number(p)
  if (port < 1 || port > 65535) return null
  const wildcard = host === '' || host === '0.0.0.0' || host === '[::]'
  if (!wildcard && !isHost(host)) return null
  const scheme = l.scheme === 'http' || l.scheme === 'https' ? l.scheme : (l.name === 'v1' && !l.loopback ? 'https' : 'http')
  return { url: `${scheme}://${wildcard ? 'localhost' : host}:${port}/v1`, tls: scheme === 'https', wildcard }
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
