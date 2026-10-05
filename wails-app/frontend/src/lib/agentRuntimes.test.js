import { describe, it, expect, vi, beforeEach } from 'vitest'

const scanAgentRuntimes = vi.fn()
vi.mock('../services/api.js', () => ({ api: { scanAgentRuntimes: (...args) => scanAgentRuntimes(...args) } }))

import { cachedAgentScan, invalidateAgentScan, isMonomindNotFound, installRecipe, recipeCommand } from './agentRuntimes.js'

const worked = { agents: [{ id: 'claude', installed: true }] }
const failed = { error: 'handshake with /opt/homebrew/bin/monomind failed: signal: killed' }

beforeEach(() => {
  vi.clearAllMocks()
  invalidateAgentScan()
})

describe('cachedAgentScan', () => {
  it('keeps a scan that worked, so every consumer shares one scan per window', async () => {
    scanAgentRuntimes.mockResolvedValue(worked)
    expect(await cachedAgentScan()).toBe(worked)
    expect(await cachedAgentScan()).toBe(worked)
    expect(scanAgentRuntimes).toHaveBeenCalledTimes(1)
  })

  // A scan that failed (monomind killed by a timeout while the machine was overloaded, say) is
  // not worth keeping for five minutes: it would be shown again and again after the cause is gone.
  it('does not keep a scan that failed: the next call scans again', async () => {
    scanAgentRuntimes.mockResolvedValueOnce(failed).mockResolvedValueOnce(worked)
    expect(await cachedAgentScan()).toBe(failed)
    expect(await cachedAgentScan()).toBe(worked)
    expect(scanAgentRuntimes).toHaveBeenCalledTimes(2)
    // and the one that worked is kept from then on
    expect(await cachedAgentScan()).toBe(worked)
    expect(scanAgentRuntimes).toHaveBeenCalledTimes(2)
  })

  it('does not keep an empty answer either', async () => {
    scanAgentRuntimes.mockResolvedValueOnce(null).mockResolvedValueOnce(worked)
    expect(await cachedAgentScan()).toBeNull()
    expect(await cachedAgentScan()).toBe(worked)
    expect(scanAgentRuntimes).toHaveBeenCalledTimes(2)
  })
})

describe('isMonomindNotFound', () => {
  it('matches only the backend "binary not found" error', () => {
    expect(isMonomindNotFound('monomind not found (AI engine) — install it with `npm install -g @monoes/monomindcli`')).toBe(true)
    expect(isMonomindNotFound('monomind 2.1.0 is too old (need >= 2.9.0)')).toBe(false)
    expect(isMonomindNotFound('handshake with /x/monomind failed: exit status 127')).toBe(false)
    expect(isMonomindNotFound('')).toBe(false)
    expect(isMonomindNotFound(undefined)).toBe(false)
  })
})

// Mirrors monoagentcli's agentinstall.ForEntry/Parse (#146 item 9).
describe('installRecipe', () => {
  it('uses the structured recipe when monomind sends one', () => {
    expect(installRecipe({ install: { kind: 'npm', packages: ['@openai/codex'] } })).toEqual({ kind: 'npm', packages: ['@openai/codex'] })
    expect(installRecipe({ install: { kind: 'script', url: 'https://x.example/i.sh', shell: 'bash' }, install_hint: 'see docs' }))
      .toEqual({ kind: 'script', url: 'https://x.example/i.sh', shell: 'bash' })
    expect(installRecipe({ install: { kind: 'script', url: 'http://x.example/i.sh', shell: 'bash' } }).kind).toBe('manual')
    expect(installRecipe({ install: { kind: 'manual' }, install_hint: 'npm install -g x' }).kind).toBe('manual')
  })
  it('parses the hint without a recipe: npm, curl | bash, else manual', () => {
    expect(installRecipe({ install_hint: 'npm install -g @openai/codex' })).toEqual({ kind: 'npm', packages: ['@openai/codex'] })
    const script = installRecipe({ install_hint: 'curl -fsSL https://kimi.example/install.sh | bash' })
    expect(script).toEqual({ kind: 'script', url: 'https://kimi.example/install.sh', shell: 'bash' })
    expect(recipeCommand(script)).toBe('curl -fsSL https://kimi.example/install.sh | bash')
    expect(installRecipe({ install_hint: 'curl -fsSL http://insecure.example/i.sh | bash' }).kind).toBe('manual')
    expect(installRecipe({ install_hint: 'npm install -g x; echo pwned' }).kind).toBe('manual')
    expect(installRecipe({ install_hint: 'install it per https://docs.example' }).kind).toBe('manual')
    expect(installRecipe({}).kind).toBe('manual')
  })
})
