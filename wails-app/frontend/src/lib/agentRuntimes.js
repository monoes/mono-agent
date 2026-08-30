import { api } from '../services/api.js'

// `agent scan` is a genuinely slow operation (~6-7s: it spawns monomind,
// which itself spawns a handshake + a parallel probe of every known agent
// CLI binary). This module-level cache lets every consumer in the app
// (Agents page refresh, AI chat panels' first open) share one scan per TTL
// window instead of each paying it independently on mount.
const TTL_MS = 5 * 60 * 1000

let cached = { res: null, at: 0 }

export function cachedAgentScan() {
  if (cached.res && Date.now() - cached.at < TTL_MS) {
    return Promise.resolve(cached.res)
  }
  return api.scanAgentRuntimes().then(res => {
    cached = { res, at: Date.now() }
    return res
  })
}
