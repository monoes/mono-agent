import { api } from '../services/api.js'

// `agent scan` is a genuinely slow operation (~6-7s: it spawns monomind,
// which itself spawns a handshake + a parallel probe of every known agent
// CLI binary). This module-level cache lets every consumer in the app
// (Agents page refresh, AI chat panels' first open) share one scan per TTL
// window instead of each paying it independently on mount.
const TTL_MS = 5 * 60 * 1000

// True only for the backend's "monomind binary not found" error. Anything
// else (too old, handshake failed, exit 127) means monomind IS installed but
// can't be used, and the real message is what the user needs to see.
export function isMonomindNotFound(err) {
  return /monomind not found/i.test(err || '')
}

let cached = { res: null, at: 0 }

/** Forget the cached scan (after an install, or an explicit Refresh). */
export function invalidateAgentScan() {
  cached = { res: null, at: 0 }
}

export function cachedAgentScan() {
  if (cached.res && Date.now() - cached.at < TTL_MS) {
    return Promise.resolve(cached.res)
  }
  return api.scanAgentRuntimes().then(res => {
    cached = { res, at: Date.now() }
    return res
  })
}

// The same patterns as monoagentcli's agentinstall.Parse: with no
// structured recipe (monomind before protocol rev 9), the CLI installs from
// the hint only when it is exactly one of these, and treats anything else as
// a manual step.
const NPM_PKG = /^(@[a-z0-9][\w.-]*\/)?[a-z0-9][\w.-]*(@[\w.^~<>=*-]+)?$/
const CURL_INSTALL = /^curl\s+-fsSL\s+(https:\/\/\S+)\s*\|\s*(bash|sh)$/

function isWindows() {
  return typeof navigator !== 'undefined' && /^win/i.test(navigator.platform || '')
}

/**
 * What installing a runtime would run, as monoagentcli decides it: the
 * structured recipe when monomind sent one, else the parsed hint.
 * { kind: 'npm', packages } | { kind: 'script', url, shell } | { kind: 'manual' }.
 */
export function installRecipe(agent) {
  const r = agent?.install
  const noScripts = isWindows() // the CLI runs no vendor scripts on Windows
  if (r?.kind) {
    if (r.kind === 'npm' && r.packages?.length && r.packages.every(p => NPM_PKG.test(p))) return { kind: 'npm', packages: r.packages }
    if (r.kind === 'script' && /^https:\/\/[^/\s]+/.test(r.url || '') && (r.shell === 'bash' || r.shell === 'sh') && !noScripts) {
      return { kind: 'script', url: r.url, shell: r.shell }
    }
    return { kind: 'manual' }
  }
  const hint = String(agent?.install_hint || '').trim()
  const f = hint.split(/\s+/)
  if (f.length >= 4 && f[0] === 'npm' && f[1] === 'install' && (f[2] === '-g' || f[2] === '--global')) {
    const packages = f.slice(3)
    return packages.every(p => NPM_PKG.test(p)) ? { kind: 'npm', packages } : { kind: 'manual' }
  }
  const m = CURL_INSTALL.exec(hint)
  if (m && !noScripts) return { kind: 'script', url: m[1], shell: m[2] }
  return { kind: 'manual' }
}

/** The command a recipe runs, for the confirmation dialog. */
export function recipeCommand(recipe, agent) {
  if (recipe.kind === 'script') return `curl -fsSL ${recipe.url} | ${recipe.shell}`
  if (recipe.kind === 'npm') return `npm install -g ${recipe.packages.join(' ')}`
  return agent?.install_hint || ''
}
