// Shared org event tails. The app runs one `org events --follow` per org
// (StreamOrgEvents keys them by org, and a new one for the same org
// supersedes the last), and every tail's lines reach every `org:event`
// subscriber. So views that want an org's events (the Orgs page, a group
// view, an org bubble) share one tail through here: the first acquire
// starts it, the last release stops it, and restart re-resolves which run
// it follows without disturbing the other holders.
//
// Every id a tail was started under is stopped on the last release, not
// just the newest: a restart's StreamOrgEvents may not have registered yet
// when the last holder lets go, and a stop for the newest id alone would
// then miss the older process, which would follow its run until the app
// quits (#239). Stopping an id that already ended is harmless.
import { api, newOrgEventsStreamId } from '../services/api.js'

const streams = new Map() // org → { count, ids }

// call runs a binding and drops its failure: a tail that can't start or
// stop has nothing else to do about it here.
function call(fn) {
  try {
    fn()?.catch?.(() => {})
  } catch {
    // No Wails runtime (tests, plain browser).
  }
}

// start runs a new tail for entry. Its id joins every earlier one: the Go
// side registers tails in whatever order their calls land, so an older
// one can register after (and kill) a newer one, and only stopping every
// id on the last release is sure to end whichever runs.
function start(org, entry) {
  const id = newOrgEventsStreamId()
  entry.ids.push(id)
  call(() => api.streamOrgEvents(org, id))
}

// acquireOrgEvents makes sure org's events flow and returns the release.
// Releasing twice is harmless.
export function acquireOrgEvents(org) {
  if (!org) return () => {}
  let entry = streams.get(org)
  if (!entry) {
    entry = { count: 0, ids: [] }
    streams.set(org, entry)
  }
  entry.count += 1
  if (entry.count === 1) start(org, entry)
  let released = false
  return () => {
    if (released) return
    released = true
    entry.count -= 1
    if (entry.count === 0 && streams.get(org) === entry) {
      streams.delete(org)
      for (const id of entry.ids) call(() => api.stopOrgEvents(org, id))
    }
  }
}

// restartOrgEvents replaces org's tail with a new one (which then follows
// the current run); a no-op when nobody holds it.
export function restartOrgEvents(org) {
  const entry = streams.get(org)
  if (entry && entry.count > 0) start(org, entry)
}

// orgEventHolders is how many views hold org's tail.
export function orgEventHolders(org) {
  return streams.get(org)?.count || 0
}
