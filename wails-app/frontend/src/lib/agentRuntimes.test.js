import { describe, it, expect } from 'vitest'
import { isMonomindNotFound, installRecipe, recipeCommand } from './agentRuntimes.js'

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
