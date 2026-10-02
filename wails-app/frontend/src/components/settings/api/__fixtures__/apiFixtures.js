// Realistic CLI documents for the API section's tests: the shapes of
// `api status --json` and `api models --json` (cmd/monoagentcli/api_status.go and
// api_models.go), with the models of a real machine. The flags are computed the
// way the CLI computes them, so a fixture is consistent with its policy.

const WEIGHT = { 'chat-only': 1, sandboxed: 2, unconfined: 3, any: 3 }

export const MISSING_SURFACE = 'the api_auto surface switched on for the profile (monoagentcli jev enable api_auto)'
export const MISSING_KEY = 'a Jev key for the profile (monoagentcli jev key set, or TYPESAFE_API_KEY)'

export const mainListener = (over = {}) => ({
  name: 'main', addr: '127.0.0.1:9322', loopback: true, v1: true,
  confinement: 'any', context_confinement: 'chat-only', auto_confinement: 'chat-only',
  confinement_source: 'environment', reachable: true, v1_answers: true, ...over,
})

export const dedicatedListener = (over = {}) => ({
  name: 'v1', addr: '0.0.0.0:9443', loopback: false, v1: true,
  confinement: 'chat-only', context_confinement: 'chat-only', auto_confinement: 'chat-only',
  confinement_source: 'daemon', reachable: true, v1_answers: true, ...over,
})

export const statusOf = (listeners, over = {}) => ({
  v: 1, profile: 'default', keys: { active: 2 }, daemon: { running: false },
  auto: { available: false, missing: MISSING_SURFACE }, listeners, ...over,
})

// [id, class, label, validated], from `api models` on a machine with claude, codex, antigravity, copilot and pi.
const MODELS = [
  ['claude/default', 'chat-only', 'Default (Use the default model (currently Opus 5 (1M context)))', false],
  ['claude/sonnet', 'chat-only', 'Sonnet 5', true],
  ['claude/haiku', 'chat-only', 'Haiku 4.5', false],
  ['codex/default', 'sandboxed', 'codex default model', false],
  ['codex/gpt-6-astra', 'sandboxed', 'GPT-6-Astra', false],
  ['copilot/default', 'sandboxed', 'copilot default model', false],
  ['antigravity/default', 'unconfined', 'antigravity default model', false],
  ['pi/openrouter/nvidia/nemotron-3-super-120b-a12b:free', 'unconfined', 'Nemotron 3 Super (free, OpenRouter)', false],
]

// modelsDoc is `api models --json` for a listener serving up to `confinement`,
// whose context keys and auto may use up to `context` and `auto`.
export function modelsDoc({ confinement = 'any', context = 'chat-only', auto = 'chat-only', forListener = 'loopback', autoState } = {}) {
  const served = (cls, cap) => WEIGHT[cls] <= Math.min(WEIGHT[confinement], WEIGHT[cap])
  const models = MODELS.map(([id, cls, label, validated]) => ({
    id, runtime: id.split('/')[0], model: id.split('/').slice(1).join('/'), label, confinement: cls, validated,
    allowed: served(cls, 'any'), context_allowed: served(cls, context), auto_allowed: served(cls, auto),
  }))
  const candidates = models.filter(m => m.auto_allowed).length
  const heldBack = models.filter(m => m.allowed).length - candidates
  const autoDoc = autoState || { available: true, key_source: 'vault', confinement: WEIGHT[auto] < WEIGHT[confinement] ? auto : confinement, candidates, held_back: heldBack }
  return {
    v: 1,
    policy: { for: forListener, confinement, context_confinement: WEIGHT[context] < WEIGHT[confinement] ? context : confinement, auto_confinement: WEIGHT[auto] < WEIGHT[confinement] ? auto : confinement, source: 'shell' },
    models,
    auto: autoDoc,
  }
}

// The same document from a CLI that predates --auto-confinement: no auto_allowed,
// no policy.auto_confinement, and an auto object without confinement, candidates or held_back.
export function oldModelsDoc(opts) {
  const doc = modelsDoc(opts)
  delete doc.policy.auto_confinement
  doc.models.forEach(m => { delete m.auto_allowed })
  doc.auto = doc.auto.available ? { available: true, key_source: doc.auto.key_source } : { available: false, missing: doc.auto.missing }
  return doc
}

export const keyList = (now = Date.now()) => [
  { id: 'key_abcdefghijkl', profile_id: 'default', name: 'my-app', prefix: 'sk-ma-AbCdEf', context: false,
    created_at: '2026-09-20T12:00:00Z', last_used_at: new Date(now - 3 * 60 * 1000).toISOString(), revoked_at: '' },
  { id: 'key_mnopqrstuvwx', profile_id: 'default', name: 'notes bot', prefix: 'sk-ma-GhIjKl', context: true,
    created_at: '2026-10-01T12:00:00Z', last_used_at: '', revoked_at: '' },
]
