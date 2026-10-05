// Realistic CLI documents for the API section's tests: the shapes of
// `api status --json` and `api models --json` (cmd/monoagentcli/api_status.go and
// api_models.go) as the app's bindings pass them on, with the models of a real
// machine. The flags are computed the way the CLI computes them, so a fixture is
// consistent with its policy, and so is its spelling: a policy says chat-only,
// sandboxed or any, a model's class says chat-only, sandboxed or unconfined, and
// the CLI's auto.confinement is a class while its policy.auto_confinement is a policy.

const WEIGHT = { 'chat-only': 1, sandboxed: 2, unconfined: 3, any: 3 }
const asClass = (policy) => (policy === 'any' ? 'unconfined' : policy)

export const MISSING_SURFACE = 'the api_auto surface switched on for the profile (monoagentcli jev enable api_auto)'
export const MISSING_KEY = 'a Jev key for the profile (monoagentcli jev key set, or TYPESAFE_API_KEY)'

// A listener as a current CLI reports it: the scheme that answered, and none when nothing did.
function listener(base, over) {
  const l = { ...base, ...over }
  if (!l.reachable) delete l.scheme
  return l
}

export const mainListener = (over = {}) => listener({
  name: 'main', addr: '127.0.0.1:9322', loopback: true, v1: true,
  confinement: 'any', context_confinement: 'chat-only', auto_confinement: 'chat-only',
  confinement_source: 'environment', scheme: 'http', reachable: true, v1_answers: true,
}, over)

export const dedicatedListener = (over = {}) => listener({
  name: 'v1', addr: '0.0.0.0:9443', loopback: false, v1: true,
  confinement: 'chat-only', context_confinement: 'chat-only', auto_confinement: 'chat-only',
  confinement_source: 'daemon', scheme: 'https', reachable: true, v1_answers: true,
}, over)

// The same listener from a CLI that predates the scheme.
export const withoutScheme = (l) => { const o = { ...l }; delete o.scheme; return o }

export const statusOf = (listeners, over = {}) => ({
  v: 1, profile: 'default', keys: { active: 2 }, daemon: { running: false },
  auto: { available: false, missing: MISSING_SURFACE }, listeners, ...over,
})

// [id, class, label, validated, capabilities], from `api models` on a machine with claude, codex, antigravity, copilot and pi.
// The capabilities are what the gateway's rules give with MONOAGENT_API_IMAGE_RUNTIMES and MONOAGENT_API_TOOL_RUNTIMES at their
// defaults (codex,antigravity and claude,codex): text for every model, image for a codex or antigravity one that is not
// chat-only, tools for claude and for a codex model monomind can run read-only (here one of the two).
const MODELS = [
  ['claude/default', 'chat-only', 'Default (Use the default model (currently Opus 5 (1M context)))', false, ['text', 'tools']],
  ['claude/sonnet', 'chat-only', 'Sonnet 5', true, ['text', 'tools']],
  ['claude/haiku', 'chat-only', 'Haiku 4.5', false, ['text', 'tools']],
  ['codex/default', 'sandboxed', 'codex default model', false, ['text', 'image']],
  ['codex/gpt-6-astra', 'sandboxed', 'GPT-6-Astra', false, ['text', 'image', 'tools']],
  ['copilot/default', 'sandboxed', 'copilot default model', false, ['text']],
  ['antigravity/default', 'unconfined', 'antigravity default model', false, ['text', 'image']],
  ['pi/openrouter/nvidia/nemotron-3-super-120b-a12b:free', 'unconfined', 'Nemotron 3 Super (free, OpenRouter)', false, ['text']],
]

// modelsDoc is `api models --json` for a listener serving up to `confinement`,
// whose context keys and auto may use up to `context` and `auto`.
export function modelsDoc({ confinement = 'any', context = 'chat-only', auto = 'chat-only', forListener = 'loopback', autoState } = {}) {
  const served = (cls, cap) => WEIGHT[cls] <= Math.min(WEIGHT[confinement], WEIGHT[cap])
  const models = MODELS.map(([id, cls, label, validated, capabilities]) => ({
    id, runtime: id.split('/')[0], model: id.split('/').slice(1).join('/'), label, confinement: cls, validated,
    allowed: served(cls, 'any'), context_allowed: served(cls, context), auto_allowed: served(cls, auto),
    capabilities: [...capabilities],
  }))
  const candidates = models.filter(m => m.auto_allowed).length
  const heldBack = models.filter(m => m.allowed).length - candidates
  const cap = (p) => (WEIGHT[p] < WEIGHT[confinement] ? p : confinement)
  // The CLI leaves a count of 0 out, and spells auto.confinement as a class.
  const counts = { ...(candidates ? { candidates } : {}), ...(heldBack ? { held_back: heldBack } : {}) }
  const autoDoc = autoState || { available: true, key_source: 'vault', confinement: asClass(cap(auto)), ...counts }
  return {
    v: 1,
    policy: { for: forListener, confinement, context_confinement: cap(context), auto_confinement: cap(auto), source: 'shell' },
    models,
    auto: autoDoc,
  }
}

// The same document from a CLI that predates --auto-confinement and the capabilities: no auto_allowed and
// no capabilities, no policy.auto_confinement, and an auto object without confinement, candidates or held_back.
export function oldModelsDoc(opts) {
  const doc = modelsDoc(opts)
  delete doc.policy.auto_confinement
  doc.models.forEach(m => { delete m.auto_allowed; delete m.capabilities })
  doc.auto = doc.auto.available ? { available: true, key_source: doc.auto.key_source } : { available: false, missing: doc.auto.missing }
  return doc
}

export const keyList = (now = Date.now()) => [
  { id: 'key_abcdefghijkl', profile_id: 'default', name: 'my-app', prefix: 'sk-ma-AbCdEf', context: false,
    created_at: '2026-09-20T12:00:00Z', last_used_at: new Date(now - 3 * 60 * 1000).toISOString(), revoked_at: '' },
  { id: 'key_mnopqrstuvwx', profile_id: 'default', name: 'notes bot', prefix: 'sk-ma-GhIjKl', context: true,
    created_at: '2026-10-01T12:00:00Z', last_used_at: '', revoked_at: '' },
]
