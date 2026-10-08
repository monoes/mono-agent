import { useEffect, useState } from 'react'
import { api } from '../../services/api.js'

// Which runtimes a sections org may use (#341). The list and the reasons come
// from `monoagentcli org sections-runtimes`, i.e. monomind's isolation
// registry as `org validate` reports it; nothing is hardcoded here.

// sectionsOrgEnabled: whether the org is a sections org is decided in Go
// (Doc.SectionsEnabled) and arrives as sections_enabled next to the org doc;
// designMeta folds it into the meta OrgDesigner keeps.
export const designMeta = (res) => {
  const { roles, ...meta } = res.org
  return { ...meta, sectionsEnabled: res.sections_enabled === true }
}
export const sectionsOrgEnabled = (meta) => meta?.sectionsEnabled === true

// runtimeChoice says how the picker treats a runtime: 'refused' (disabled,
// with monomind's reason), 'unverified' (allowed, with a warning) or 'ok'.
export function runtimeChoice(policy, runtime) {
  const hit = (policy?.runtimes || []).find(r => r.runtime === runtime)
  return hit ? { status: hit.status, reason: hit.reason || '' } : { status: 'ok', reason: '' }
}

// pickerRuntimes is the runtimes to offer: the usual ones plus any runtime
// monomind refuses in a sections org, so the picker can show it disabled.
export function pickerRuntimes(base, policy) {
  const extra = (policy?.runtimes || []).filter(r => r.status === 'refused' && !base.includes(r.runtime)).map(r => r.runtime)
  return [...base, ...extra]
}

// The policy only changes with monomind, so it is asked once per session;
// a failed ask is not kept.
let cached = null
export function resetSectionsRuntimePolicyCache() { cached = null }
function loadPolicy() {
  if (!cached) {
    cached = Promise.resolve().then(() => api.orgSectionsRuntimes())
    cached.catch(() => { cached = null })
  }
  return cached
}

// useSectionsRuntimePolicy loads the policy while enabled; null until then
// (and when the CLI cannot say, in which case nothing is blocked and
// `org validate` still reports a refused runtime).
export function useSectionsRuntimePolicy(enabled) {
  const [policy, setPolicy] = useState(null)
  useEffect(() => {
    if (!enabled) { setPolicy(null); return undefined }
    let alive = true
    loadPolicy()
      .then(p => { if (alive) setPolicy(p) })
      .catch(() => { if (alive) setPolicy(null) })
    return () => { alive = false }
  }, [enabled])
  return policy
}
