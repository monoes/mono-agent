// Icon manifest access + role -> icon suggestion logic for the Org Designer.
// Pure(ish) helpers: the only side effects are the manifest fetch (memoized)
// and localStorage for "recently used icons".

let _manifestPromise = null

/**
 * Fetch + parse the vendored icon manifest, memoized at module level so it
 * only ever fetches once no matter how many callers ask for it. Returns the
 * `agents` array — the manifest's `count` field is stale (120 vs 118 real
 * entries/files) and must never be trusted.
 */
export function loadIconManifest() {
  if (!_manifestPromise) {
    _manifestPromise = fetch('/org-avatars/agent-avatars.json')
      .then(res => res.json())
      .then(data => data.agents || [])
  }
  return _manifestPromise
}

export function iconUrl(id) {
  return `/org-avatars/avatars/${id}.svg`
}

// Sensible default archetype per canonical role type — ids verified to
// exist in the real agent-avatars.json manifest.
export const TYPE_ICON = {
  boss: 'studio-producer',
  specialist: 'coder',
  researcher: 'researcher',
  reviewer: 'reviewer',
}

// Manifest `category` -> suggested role `type`. Purely a cosmetic label
// suggestion for a palette-driven add — never "boss" here, since that
// string is reserved for the org root (see orgdesign.Doc.PromoteToRoot);
// every palette add is non-root by construction, and the backend would
// downgrade a stray "boss" from here anyway (AddRole's own guard), but
// there's no reason to produce a value that's just going to be discarded.
export const CATEGORY_TYPE = {
  core: 'specialist',
  ai: 'researcher',
  blockchain: 'specialist',
  consensus: 'reviewer',
  content: 'specialist',
  creative: 'specialist',
  data: 'researcher',
  devops: 'specialist',
  frontend: 'specialist',
  github: 'specialist',
  infra: 'specialist',
  legal: 'specialist',
  management: 'specialist',
  monoswarm: 'specialist',
  perf: 'specialist',
  product: 'specialist',
  sales: 'specialist',
  security: 'reviewer',
  sparc: 'specialist',
  testing: 'reviewer',
}

// Distinct color per category, for palette section headers and
// edge-color-by-category. Follows the same flat-hex style as NodeRunner's
// CAT_COLOR map (src/pages/NodeRunner.jsx).
export const CAT_COLOR = {
  core: '#00b4d8',
  ai: '#7c3aed',
  blockchain: '#d97706',
  consensus: '#1d4ed8',
  content: '#e1306c',
  creative: '#a855f7',
  data: '#0891b2',
  devops: '#64748b',
  frontend: '#0a66c2',
  github: '#24292e',
  infra: '#0f766e',
  legal: '#8899aa',
  management: '#9333ea',
  monoswarm: '#ff6b35',
  perf: '#f59e0b',
  product: '#10b981',
  sales: '#ef4444',
  security: '#dc2626',
  sparc: '#6366f1',
  testing: '#16a765',
}

const _suggestCache = new Map()

function tokenize(str) {
  return (str || '').toLowerCase().split(/[-\s]+/).filter(Boolean)
}

/**
 * Suggest an icon id for a role. Purely advisory — never mutates the role
 * or writes anything back; the caller decides whether to persist it.
 * Resolution order:
 *   1. role.ui.icon, if set and present in the manifest
 *   2. role.type, if it exactly matches a manifest agent id
 *   3. TYPE_ICON[role.type], if present
 *   4. token-overlap scoring between `${title} ${type}` and each agent's
 *      `label`+`id` (score >= 1 required, else fall through)
 *   5. fallback: 'coder'
 * Memoized per role id via a simple Map cache.
 */
export async function suggestIcon(role) {
  if (role?.id && _suggestCache.has(role.id)) return _suggestCache.get(role.id)

  const agents = await loadIconManifest()
  const byId = new Map(agents.map(a => [a.id, a]))

  let result
  if (role?.ui?.icon && byId.has(role.ui.icon)) {
    result = role.ui.icon
  } else if (role?.type && byId.has(role.type)) {
    result = role.type
  } else if (role?.type && TYPE_ICON[role.type]) {
    result = TYPE_ICON[role.type]
  } else {
    const roleTokens = new Set(tokenize(`${role?.title || ''} ${role?.type || ''}`))
    let bestScore = 0
    let bestId = null
    agents.forEach(a => {
      const agentTokens = new Set(tokenize(`${a.label} ${a.id}`))
      let score = 0
      roleTokens.forEach(t => { if (agentTokens.has(t)) score++ })
      if (score > bestScore) {
        bestScore = score
        bestId = a.id
      }
    })
    result = bestScore >= 1 ? bestId : 'coder'
  }

  if (role?.id) _suggestCache.set(role.id, result)
  return result
}

// ── Recently used icons (localStorage) ───────────────────────────────────
// Mirrors the try/catch JSON.stringify/parse style already used elsewhere
// in this codebase (e.g. `nr2-palette-open` in NodeRunner.jsx).

const RECENT_KEY = 'od-recent-icons'
const RECENT_MAX = 8

export function getRecentIcons() {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]')
  } catch {
    return []
  }
}

export function pushRecentIcon(id) {
  if (!id) return
  try {
    const next = [id, ...getRecentIcons().filter(x => x !== id)].slice(0, RECENT_MAX)
    localStorage.setItem(RECENT_KEY, JSON.stringify(next))
  } catch {
    // ignore
  }
}
