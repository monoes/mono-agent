// App-level events between pages that stay mounted (App.jsx keeps visited
// pages alive): a DOM CustomEvent on window, so no page needs a reference to
// another. Each on* helper returns its unsubscribe function.

const AUTOMATIONS_CHANGED = 'monoagent:automations-changed'

// emitAutomationsChanged: installed automations (and so the node catalog)
// changed — install, uninstall, restore, rollback, a recorded action saved,
// or a workflow import that installed bundled packages.
export function emitAutomationsChanged(detail = {}) {
  window.dispatchEvent(new CustomEvent(AUTOMATIONS_CHANGED, { detail }))
}

export function onAutomationsChanged(cb) {
  const h = (e) => cb(e.detail)
  window.addEventListener(AUTOMATIONS_CHANGED, h)
  return () => window.removeEventListener(AUTOMATIONS_CHANGED, h)
}
