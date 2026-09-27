// Which dashboard the user last looked at: this profile's, or every
// profile's. Remembered per window; never required, since a blocked
// storage just starts on "profile".
export const SCOPE_KEY = 'monoagent.dashboard.scope'
export const SCOPES = ['profile', 'global']

export function loadScope(storage = globalThis.localStorage) {
  try {
    const v = storage?.getItem(SCOPE_KEY)
    return SCOPES.includes(v) ? v : 'profile'
  } catch {
    return 'profile'
  }
}

export function saveScope(scope, storage = globalThis.localStorage) {
  try { storage?.setItem(SCOPE_KEY, scope) } catch { /* the toggle still works until the window closes */ }
}

// ownRow: does this row belong to the profile the app is on? Rows in the
// profile view carry no profile_id and are always its own.
export function ownRow(row, currentId) {
  return !row?.profile_id || !currentId || row.profile_id === currentId
}

export function currentProfileId(summary) {
  return summary?.profiles?.find(p => p.current)?.id || summary?.profile_id || ''
}
