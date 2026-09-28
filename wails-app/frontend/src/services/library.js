// monoes.me library bindings. Each call resolves to the CLI's JSON
// (`monoagentcli library … --json`) or to {error, code} — never rejects, so
// the library UI shows failures inline instead of as toasts.
import * as GoApp from '../wailsjs/go/main/App'
import { subscribeEvent } from './api.js'

// run takes a thunk so a binding missing from an older app build (or a
// test stub) becomes {error} too, not a synchronous throw.
const run = (call) => Promise.resolve()
  .then(call)
  .then(r => (typeof r === 'string' ? JSON.parse(r) : r))
  .catch(e => ({ error: e?.message || String(e) }))

export const library = {
  status:      (offline = false) => run(() => GoApp.LibraryStatus(offline)),
  login:       () => run(() => GoApp.LibraryLogin()),
  cancelLogin: () => run(() => GoApp.LibraryLoginCancel()),
  sendCode:    (email) => run(() => GoApp.LibraryLoginEmailSend(email)),
  verifyCode:  (email, code) => run(() => GoApp.LibraryLoginEmailVerify(email, code)),
  logout:      () => run(() => GoApp.LibraryLogout()),
  list:        (kind, scope, search = '', page = 1) => run(() => GoApp.LibraryList(kind, scope, search, page)),
  show:        (id) => run(() => GoApp.LibraryShow(id)),
  install:     (kind, id, { rename = '', yes = false } = {}) => run(() => GoApp.LibraryInstall(kind, id, rename, yes)),
  publish:     (kind, localId, { isPublic = false, name = '', description = '', tags = '', version = '' } = {}) =>
    run(() => GoApp.LibraryPublish(kind, localId, isPublic, name, description, tags, version)),
  update:      (id = '') => run(() => GoApp.LibraryUpdate(id)),
  installed:   (kind = '') => run(() => GoApp.LibraryInstalled(kind)),
}

// "library:login" carries the CLI's login progress ({kind:"url", url} first).
export function onLibraryLogin(callback) {
  return subscribeEvent('library:login', callback)
}

// An org install refused because the name is taken: the CLI says so with
// invalid_input and names the --rename way out.
export function isNameCollision(res) {
  return !!res?.error && res.code === 'invalid_input' && /--rename/.test(res.error)
}

// Scope a library tab lists: Official → official, Community → public (which
// the server also fills with official items; the tab hides those), Mine →
// mine.
export const LIBRARY_TABS = [
  { id: 'official', scope: 'official' },
  { id: 'community', scope: 'public' },
  { id: 'mine', scope: 'mine' },
]
