// The account gate's one source of truth: what the machine's monoes.me sign-in
// allows right now (spec §6.5). It asks `account status` once at start, again
// when the window gets the focus, and whenever any binding answers
// login_required. A failed ask locks (the gate fails closed); one that fails
// while the app is up is repeated once first, so a CLI that was merely busy
// does not take a working session away.
import { useSyncExternalStore } from 'react'
import { watchBindings } from './accountBindingWatch.js'

export const FOCUS_GAP_MS = 5000           // a focus this soon after an answer does not ask again
export const LOGIN_REQUIRED_GAP_MS = 3000  // nor does a login_required answer
export const RETRY_MS = 2000               // the wait before a failed ask is repeated

// viewOf: what the page shows for one snapshot {phase, status, failure}. Locked
// is the gate; grace and warn are the banners over the app. A build with no
// enforcement date reaches here with no enforce_from: nothing locks and nothing
// warns (D22). Before the date a machine with no usable sign-in is told the date.
export function viewOf(snap) {
  if (snap.phase === 'checking') return { phase: 'checking', locked: false }
  const { status, failure } = snap
  if (failure) {
    const enforced = failure.enforced !== false
    return { phase: 'ready', failure, locked: enforced, warn: !enforced && !!failure.enforce_from, enforceFrom: failure.enforce_from }
  }
  const dated = !!status.enforce_from
  const enforced = status.enforced === true
  return {
    phase: 'ready',
    status,
    enforceFrom: status.enforce_from,
    locked: enforced && status.state === 'locked',
    grace: dated && status.state === 'grace',
    warn: dated && !enforced && status.state === 'locked',
  }
}

// createAccountGate({ status }): status() resolves to account.status()'s answer.
export function createAccountGate({ status, now = Date.now }) {
  let snap = { phase: 'checking', status: null, failure: null }
  let answeredAt = 0
  let inflight = null
  const listeners = new Set()

  const publish = (next) => { snap = next; listeners.forEach(l => l()) }

  async function ask() {
    let answer = await status()
    if (answer.failure?.cause === 'cli_failed' && snap.phase === 'ready' && !viewOf(snap).locked) {
      await new Promise(resolve => setTimeout(resolve, RETRY_MS))
      answer = await status()
    }
    answeredAt = now()
    publish({ phase: 'ready', status: answer.status ?? null, failure: answer.failure ?? null })
  }

  // check(why): why is 'start', 'focus', 'login_required' or 'manual'. Calls
  // that overlap share one ask; a focus or login_required answer that follows an
  // answer closely is dropped.
  function check(why = 'manual') {
    if (inflight) return inflight
    const since = now() - answeredAt
    if (why === 'focus' && since < FOCUS_GAP_MS) return Promise.resolve()
    if (why === 'login_required' && since < LOGIN_REQUIRED_GAP_MS) return Promise.resolve()
    inflight = ask().finally(() => { inflight = null })
    return inflight
  }

  // start asks now and keeps asking on focus and on binding answers; it returns stop.
  function start() {
    check('start')
    const onFocus = () => check('focus')
    const onVisible = () => { if (!document.hidden) check('focus') }
    window.addEventListener('focus', onFocus)
    document.addEventListener('visibilitychange', onVisible)
    const unwatch = watchBindings({
      onLoginRequired: () => check('login_required'),
      onSessionChange: () => check('manual'),
    })
    return () => {
      window.removeEventListener('focus', onFocus)
      document.removeEventListener('visibilitychange', onVisible)
      unwatch()
    }
  }

  return {
    check,
    start,
    subscribe: (listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    getSnapshot: () => snap,
  }
}

export function useAccountView(gate) {
  return viewOf(useSyncExternalStore(gate.subscribe, gate.getSnapshot))
}
