// The task board bindings (wails-app/app_tasks.go, app_tasks_watch.go).
// Each call resolves to the CLI's JSON (`monoagentcli task … --json`; the
// board, read in process, comes in the same shape) or to {error, code} and
// never rejects: the board shows a refusal on the card it
// concerns instead of a page-wide failure.
import * as GoApp from '../wailsjs/go/main/App'
import { subscribeEvent } from './api.js'

// The Done column shows the most recent DONE_LIMIT cards (spec §10).
export const DONE_LIMIT = 50

// run takes a thunk so a binding missing from an older app build (or from a
// test's mock) becomes {error} too, not a synchronous throw.
const run = (call) => Promise.resolve()
  .then(call)
  .then(r => (typeof r === 'string' ? JSON.parse(r) : r))
  .catch(e => ({ error: e?.message || String(e) }))

export const tasksApi = {
  board:     () => run(() => GoApp.TaskBoard(DONE_LIMIT)),
  show:      (id) => run(() => GoApp.TaskShow(id)),
  // spec: {title, notes} or {text}; ready: straight to Ready.
  add:       (spec) => run(() => GoApp.TaskAdd(JSON.stringify(spec))),
  // change: {title} and/or {notes}; empty notes clear them.
  edit:      (id, change) => run(() => GoApp.TaskEdit(id, JSON.stringify(change))),
  // place: {where: '' | 'top' | 'bottom' | 'before' | 'after', ref}
  move:      (id, status, place = {}) => run(() => GoApp.TaskMove(id, status, place.where || '', place.ref || 0)),
  approve:   (ids, top = false) => run(() => GoApp.TaskApprove(ids, top)),
  archive:   (ids) => run(() => GoApp.TaskArchive(ids)),
  unarchive: (ids) => run(() => GoApp.TaskUnarchive(ids)),
  comment:   (id, text) => run(() => GoApp.TaskComment(id, text)),
  // {profile_id, rev, inbox, review}, or {} before the database is open or for a profile that is not there.
  pulse:     () => run(() => GoApp.TaskPulse()),
  // The agent-context marker the app inherited, or '' (a plain string, not JSON).
  agentShell: () => Promise.resolve().then(() => GoApp.TaskAgentShell()).then(m => m || '').catch(() => ''),
}

// onTasksChanged: the active profile's board revision moved. Payload
// {profile_id, rev, inbox, review}. Returns the unsubscribe function.
export function onTasksChanged(callback) {
  return subscribeEvent('tasks:changed', callback)
}
