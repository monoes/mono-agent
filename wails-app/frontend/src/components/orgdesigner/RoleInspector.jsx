import { useEffect, useState } from 'react'
import { Crown } from 'lucide-react'
import { wouldCycle } from './orgGraph.js'
import { iconUrl } from './roleIcons.js'
import { KVBlock } from '../KVBlock.jsx'
import StringListField from '../StringListField.jsx'
import { api } from '../../services/api.js'

const RUNTIME_OPTIONS = [
  'claude', 'kimicode', 'opencode', 'vercel', 'codex', 'antigravity',
  'grok', 'qwen', 'crush', 'copilot', 'pi', 'pi-rpc', 'qwen-rpc',
]
const PROVIDER_KIND_OPTIONS = [
  'subscription', 'api-key', 'base-url', 'bedrock', 'vertex', 'vercel-api-key', 'codex', 'antigravity',
]
const PROVIDER_VENDOR_OPTIONS = [
  'openai', 'anthropic', 'google', 'xai', 'deepseek', 'glm', 'mistral', 'groq',
  'together', 'fireworks', 'cohere', 'perplexity', 'alibaba', 'openrouter', 'ollama', 'openai-compatible',
]
const GIT_LEVEL_OPTIONS = ['none', 'read', 'commit', 'push']
// Fields this component surfaces via dedicated controls even though
// orgGraph.js's hydrate() does not (yet) hoist them to top-level canvas
// node properties — they currently live in `node.rest`. Read from there,
// patched by wire field name, and excluded from the read-only Advanced
// KVBlock below so they aren't shown twice. `adapter_config` (not `model`)
// is the real wire key — the role's model/provider-name/max_tokens live
// nested under it, never as a flat top-level field.
const MODELED_REST_FIELDS = [
  'runtime', 'adapter_config', 'max_turns_per_message', 'budget_tokens', 'budget_usd',
  'policy', 'provider', 'instructions_file',
]

function omit(obj, keys) {
  if (!obj) return obj
  const out = {}
  Object.keys(obj).forEach(k => { if (!keys.includes(k)) out[k] = obj[k] })
  return out
}

// Drop keys whose value is '' or nullish — used before sending a merged
// object-patch (provider/policy) so unset fields don't get written as
// empty strings, while still allowing arrays (including empty ones, which
// are a meaningful "explicitly none" distinct from "unset") through as-is.
function cleanObj(obj) {
  const out = {}
  Object.entries(obj).forEach(([k, v]) => {
    if (Array.isArray(v) || (v !== '' && v != null)) out[k] = v
  })
  return out
}

/**
 * RoleInspector — the detail/edit panel for whichever role is currently
 * selected on the Org Designer canvas. Pure "leaf" component: it never
 * mutates `node` itself, it only reports intent upward.
 *
 * @param {object|null} node Canvas node shape from orgGraph.js's hydrate()
 *   ({id, title, type, parentId, responsibilities, icon, color, x, y, rest}),
 *   or null when nothing is selected (renders an empty-state placeholder).
 * @param {object[]} allNodes Full array of canvas nodes — used to populate
 *   the "reports to" dropdown and to exclude cycle-forming candidates.
 * @param {(patch: object) => void} onPatch Partial update, e.g. {title: 'x'}.
 *   The parent/container applies it (merging into node + persisting).
 *   Special key `_renameId: string` requests an id rename; the container
 *   must cascade that to every child's parentId, which this component does
 *   not (and cannot, from a single node's perspective) do itself.
 * @param {(parentId: string|null) => void} onSetReportsTo Reparents this role
 *   (or, with null, promotes it to root when the org has no other root —
 *   the "Reports to" dropdown's plain path; see onPromoteToRoot for the
 *   general "swap boss" case).
 * @param {() => void} onPromoteToRoot Called when the user clicks "Set as
 *   org boss" — makes this role the root even when a different root already
 *   exists (the parent reverses the whole path between them, see
 *   orgdesign.Doc.PromoteToRoot). Only shown/enabled when this role isn't
 *   already root.
 * @param {() => void} onOpenIconPicker Called when the avatar is clicked;
 *   opens the single shared IconPickerModal instance (owned by the parent).
 * @param {() => void} onDelete Called when the user asks to delete this role;
 *   the parent/container owns the actual DeleteRoleModal flow.
 */
export default function RoleInspector({ node, allNodes, onPatch, onSetReportsTo, onPromoteToRoot, onOpenIconPicker, onDelete }) {
  const [title, setTitle] = useState(node?.title || '')
  const [label, setLabel] = useState('')
  const [responsibilities, setResponsibilities] = useState(node?.responsibilities || [])
  const [renaming, setRenaming] = useState(false)
  const [renameValue, setRenameValue] = useState('')
  const [model, setModel] = useState('')
  const [providerName, setProviderName] = useState('')
  const [maxTurns, setMaxTurns] = useState('')
  const [budgetTokens, setBudgetTokens] = useState('')
  const [budgetUsd, setBudgetUsd] = useState('')
  const [instructionsFile, setInstructionsFile] = useState('')

  // Provider override (the inline `provider` block — distinct from
  // `providerName`/adapter_config.provider, a reference to a provider
  // configured elsewhere via `monomind providers configure`).
  const [pKind, setPKind] = useState('')
  const [pVendor, setPVendor] = useState('')
  const [pApiKeyEnv, setPApiKeyEnv] = useState('')
  const [pBaseUrl, setPBaseUrl] = useState('')
  const [pAuthTokenEnv, setPAuthTokenEnv] = useState('')

  // Tool policy.
  const [policyGit, setPolicyGit] = useState('read')
  const [policyMaxTokens, setPolicyMaxTokens] = useState('')
  const [policyMaxUsd, setPolicyMaxUsd] = useState('')
  const [allowTools, setAllowTools] = useState([])
  const [denyTools, setDenyTools] = useState([])
  const [fileWrite, setFileWrite] = useState([])
  const [fileRead, setFileRead] = useState([])
  const [webAllow, setWebAllow] = useState([])
  const [autoApproveTools, setAutoApproveTools] = useState([])

  useEffect(() => {
    setTitle(node?.title || '')
    setResponsibilities(node?.responsibilities || [])
    setRenaming(false)
    setRenameValue(node?.id || '')
    setLabel(node && node.parentId != null ? (node.type || '') : '')

    const adapterConfig = node?.rest?.adapter_config || {}
    setModel(adapterConfig.model ?? '')
    setProviderName(adapterConfig.provider ?? '')
    setMaxTurns(node?.rest?.max_turns_per_message ?? '')
    setBudgetTokens(node?.rest?.budget_tokens ?? '')
    setBudgetUsd(node?.rest?.budget_usd ?? '')
    setInstructionsFile(node?.rest?.instructions_file ?? '')

    const provider = node?.rest?.provider || {}
    setPKind(provider.kind ?? '')
    setPVendor(provider.vendor ?? '')
    setPApiKeyEnv(provider.apiKeyEnv ?? '')
    setPBaseUrl(provider.baseUrl ?? '')
    setPAuthTokenEnv(provider.authTokenEnv ?? '')

    const policy = node?.rest?.policy || {}
    setPolicyGit(policy.git ?? 'read')
    setPolicyMaxTokens(policy.maxTokens ?? '')
    setPolicyMaxUsd(policy.maxUsd ?? '')
    setAllowTools(policy.allowTools || [])
    setDenyTools(policy.denyTools || [])
    setFileWrite(policy.fileWrite || [])
    setFileRead(policy.fileRead || [])
    setWebAllow(policy.webAllow || [])
    setAutoApproveTools(policy.autoApproveTools || [])
  }, [node])

  if (!node) {
    return (
      <div style={{
        width: 280, flexShrink: 0, borderLeft: '1px solid rgba(0,180,216,0.1)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        padding: 24, textAlign: 'center',
      }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11.5, color: 'var(--text-muted, #8492a6)' }}>
          Select a role on the canvas to edit its details.
        </span>
      </div>
    )
  }

  const isRoot = node.parentId == null

  const commitTitle = () => {
    if (title !== (node.title || '')) onPatch({ title })
  }

  const commitLabel = () => {
    if (!isRoot && label !== (node.type || '')) onPatch({ type: label })
  }

  const commitResponsibilities = (next) => {
    setResponsibilities(next)
    onPatch({ responsibilities: next })
  }

  const commitRename = () => {
    const trimmed = renameValue.trim()
    if (trimmed && trimmed !== node.id) onPatch({ _renameId: trimmed })
    setRenaming(false)
  }

  const numOrUndefined = (v) => (v === '' || v === null || v === undefined ? undefined : Number(v))

  // Provider override — always sends the whole merged object (Go's patch
  // replaces `provider` wholesale, no deep-patching); `overrides` carries
  // whichever field just changed, since its own setState hasn't landed yet
  // when the commit fires.
  const commitProvider = (overrides) => {
    const merged = cleanObj({
      kind: pKind, vendor: pVendor, apiKeyEnv: pApiKeyEnv, baseUrl: pBaseUrl, authTokenEnv: pAuthTokenEnv,
      ...overrides,
    })
    onPatch({ provider: Object.keys(merged).length ? merged : null })
  }
  const clearProvider = () => {
    setPKind(''); setPVendor(''); setPApiKeyEnv(''); setPBaseUrl(''); setPAuthTokenEnv('')
    onPatch({ provider: null })
  }

  // Tool policy — same whole-object-replace shape as provider.
  const commitPolicy = (overrides) => {
    const merged = {
      git: policyGit,
      maxTokens: numOrUndefined(policyMaxTokens),
      maxUsd: numOrUndefined(policyMaxUsd),
      allowTools, denyTools, fileWrite, fileRead, webAllow, autoApproveTools,
      ...overrides,
    }
    onPatch({ policy: cleanObj(merged) })
  }
  const clearPolicy = () => {
    setPolicyGit('read'); setPolicyMaxTokens(''); setPolicyMaxUsd('')
    setAllowTools([]); setDenyTools([]); setFileWrite([]); setFileRead([]); setWebAllow([]); setAutoApproveTools([])
    onPatch({ policy: null })
  }

  const otherRoles = (allNodes || []).filter(n => n.id !== node.id && !wouldCycle(allNodes, node.id, n.id))
  const advancedRest = omit(node.rest, MODELED_REST_FIELDS)

  return (
    <div style={{
      width: 280, flexShrink: 0, borderLeft: '1px solid rgba(0,180,216,0.1)',
      overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 16, padding: 14,
      fontFamily: 'var(--font-mono)',
    }}>
      {/* Identity */}
      <section>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
          <button
            onClick={onOpenIconPicker}
            title="Change icon"
            style={{ padding: 0, border: '2px solid rgba(0,180,216,0.3)', borderRadius: '50%', cursor: 'pointer', background: 'transparent', flexShrink: 0 }}
          >
            <img src={iconUrl(node.icon || 'coder')} alt="" style={{ width: 44, height: 44, borderRadius: '50%', display: 'block' }} />
          </button>
          <div style={{ flex: 1, minWidth: 0 }}>
            <input
              className="form-input"
              value={title}
              onChange={e => setTitle(e.target.value)}
              onBlur={commitTitle}
              onKeyDown={e => { if (e.key === 'Enter') e.currentTarget.blur() }}
              placeholder="Role title"
              style={{ fontSize: 13, padding: '6px 8px' }}
            />
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 10 }}>
          {!renaming ? (
            <>
              <span style={{ fontSize: 10.5, color: 'var(--text-muted, #8492a6)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>
                id: {node.id}
              </span>
              <button
                onClick={() => setRenaming(true)}
                style={{ background: 'none', border: 'none', color: '#00b4d8', cursor: 'pointer', fontSize: 10, fontFamily: 'var(--font-mono)', padding: 0 }}
              >
                Rename id...
              </button>
            </>
          ) : (
            <div style={{ display: 'flex', gap: 4, width: '100%' }}>
              <input
                className="form-input"
                autoFocus
                value={renameValue}
                onChange={e => setRenameValue(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setRenaming(false) }}
                style={{ fontSize: 11, padding: '4px 6px', flex: 1 }}
              />
              <button className="btn btn-sm btn-primary" onClick={commitRename}>OK</button>
              <button className="btn btn-sm btn-ghost" onClick={() => setRenaming(false)}>Cancel</button>
            </div>
          )}
        </div>

        {/* "boss" is derived from tree position (see orgdesign.Doc.PromoteToRoot)
            — the root role always IS the boss, never a separately-typed
            label, so this is a fact display for root and a one-click action
            for everyone else, not another text field to fill in. */}
        {isRoot ? (
          <div style={{
            display: 'flex', alignItems: 'center', gap: 6, padding: '5px 10px',
            borderRadius: 6, background: 'rgba(234,179,8,0.1)', border: '1px solid rgba(234,179,8,0.35)',
            fontSize: 10.5, color: 'var(--yellow)',
          }}>
            <Crown size={12} color="var(--yellow)" fill="var(--yellow)" />
            Org boss (entry point)
          </div>
        ) : (
          <button
            onClick={onPromoteToRoot}
            title="Make this role the org's entry point — the current boss becomes its direct report"
            style={{
              display: 'flex', alignItems: 'center', gap: 6, padding: '5px 10px', width: '100%',
              borderRadius: 6, background: 'transparent', border: '1px solid var(--border, rgba(255,255,255,0.12))',
              fontSize: 10.5, color: 'var(--text-secondary, #8899aa)', cursor: 'pointer', fontFamily: 'var(--font-mono)',
            }}
          >
            <Crown size={12} />
            Set as org boss
          </button>
        )}
      </section>

      {/* Label (type) — free-form and purely cosmetic (feeds the agent's
          system-prompt line); no fixed options, since nothing in the org
          runtime branches on it except the literal string "boss", which is
          handled structurally above, not here. */}
      {!isRoot && (
        <section>
          <div className="form-label">Label</div>
          <input
            className="form-input"
            value={label}
            onChange={e => setLabel(e.target.value)}
            onBlur={commitLabel}
            onKeyDown={e => { if (e.key === 'Enter') e.currentTarget.blur() }}
            placeholder="specialist, researcher, reviewer, or anything descriptive"
            style={{ fontSize: 11 }}
          />
        </section>
      )}

      {/* Responsibilities */}
      <StringListField
        label="Responsibilities"
        values={responsibilities}
        onChange={commitResponsibilities}
        placeholder='e.g. "Review pull requests for security issues"'
        addLabel="+ Add responsibility"
      />

      {/* Runtime */}
      <section>
        <div className="form-label">Runtime</div>
        <select
          className="form-select"
          value={node.rest?.runtime ?? ''}
          onChange={e => onPatch({ runtime: e.target.value })}
        >
          <option value="">(inherit org default)</option>
          {RUNTIME_OPTIONS.map(r => <option key={r} value={r}>{r}</option>)}
        </select>
      </section>

      {/* Model / provider name / budgets */}
      <section>
        <div className="form-label">Model / budgets</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <input
            className="form-input"
            value={model}
            onChange={e => setModel(e.target.value)}
            onBlur={() => onPatch({ model: model === '' ? undefined : model })}
            placeholder="claude-sonnet-4-5"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            value={providerName}
            onChange={e => setProviderName(e.target.value)}
            onBlur={() => onPatch({ adapter_config_provider: providerName === '' ? undefined : providerName })}
            placeholder="provider name (from `monomind providers configure`)"
            title="References a provider already configured with `monomind providers configure`. Distinct from the Provider override section below, which defines one inline — an inline override wins if both are set."
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            type="number"
            value={maxTurns}
            onChange={e => setMaxTurns(e.target.value)}
            onBlur={() => onPatch({ max_turns_per_message: numOrUndefined(maxTurns) })}
            placeholder="max turns per message"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            type="number"
            value={budgetTokens}
            onChange={e => setBudgetTokens(e.target.value)}
            onBlur={() => onPatch({ budget_tokens: numOrUndefined(budgetTokens) })}
            placeholder="budget (tokens)"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            type="number"
            value={budgetUsd}
            onChange={e => setBudgetUsd(e.target.value)}
            onBlur={() => onPatch({ budget_usd: numOrUndefined(budgetUsd) })}
            placeholder="budget (USD)"
            style={{ fontSize: 11 }}
          />
        </div>
      </section>

      {/* Provider override — the inline block, distinct from "provider name"
          above; wins over it when both are set. */}
      <section>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div className="form-label" style={{ marginBottom: 0 }}>Provider override</div>
          {(pKind || pVendor || pApiKeyEnv || pBaseUrl || pAuthTokenEnv) && (
            <button
              onClick={clearProvider}
              style={{ background: 'none', border: 'none', color: 'var(--text-muted, #8492a6)', cursor: 'pointer', fontSize: 9.5, fontFamily: 'var(--font-mono)', padding: 0 }}
            >
              Clear
            </button>
          )}
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 6 }}>
          <select className="form-select" value={pKind} onChange={e => { setPKind(e.target.value); commitProvider({ kind: e.target.value || undefined }) }}>
            <option value="">(none)</option>
            {PROVIDER_KIND_OPTIONS.map(k => <option key={k} value={k}>{k}</option>)}
          </select>
          <select className="form-select" value={pVendor} onChange={e => { setPVendor(e.target.value); commitProvider({ vendor: e.target.value || undefined }) }}>
            <option value="">(vendor)</option>
            {PROVIDER_VENDOR_OPTIONS.map(v => <option key={v} value={v}>{v}</option>)}
          </select>
          <input
            className="form-input"
            value={pApiKeyEnv}
            onChange={e => setPApiKeyEnv(e.target.value)}
            onBlur={() => commitProvider({ apiKeyEnv: pApiKeyEnv || undefined })}
            placeholder="API key env var name"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            value={pBaseUrl}
            onChange={e => setPBaseUrl(e.target.value)}
            onBlur={() => commitProvider({ baseUrl: pBaseUrl || undefined })}
            placeholder="base URL (for base-url kind)"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            value={pAuthTokenEnv}
            onChange={e => setPAuthTokenEnv(e.target.value)}
            onBlur={() => commitProvider({ authTokenEnv: pAuthTokenEnv || undefined })}
            placeholder="auth token env var name"
            style={{ fontSize: 11 }}
          />
        </div>
      </section>

      {/* Tool policy */}
      <section>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <div className="form-label" style={{ marginBottom: 0 }}>Tool policy</div>
          <button
            onClick={clearPolicy}
            style={{ background: 'none', border: 'none', color: 'var(--text-muted, #8492a6)', cursor: 'pointer', fontSize: 9.5, fontFamily: 'var(--font-mono)', padding: 0 }}
          >
            Clear
          </button>
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 6 }}>
          <select className="form-select" value={policyGit} onChange={e => { setPolicyGit(e.target.value); commitPolicy({ git: e.target.value }) }}>
            {GIT_LEVEL_OPTIONS.map(g => <option key={g} value={g}>git: {g}</option>)}
          </select>
          <input
            className="form-input"
            type="number"
            value={policyMaxTokens}
            onChange={e => setPolicyMaxTokens(e.target.value)}
            onBlur={() => commitPolicy({ maxTokens: numOrUndefined(policyMaxTokens) })}
            placeholder="max tokens (this role's own cap)"
            style={{ fontSize: 11 }}
          />
          <input
            className="form-input"
            type="number"
            value={policyMaxUsd}
            onChange={e => setPolicyMaxUsd(e.target.value)}
            onBlur={() => commitPolicy({ maxUsd: numOrUndefined(policyMaxUsd) })}
            placeholder="max USD (this role's own cap)"
            style={{ fontSize: 11 }}
          />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 10 }}>
          <StringListField label="Allow tools" values={allowTools} onChange={v => { setAllowTools(v); commitPolicy({ allowTools: v }) }} addLabel="+ Add tool" />
          <StringListField label="Deny tools" values={denyTools} onChange={v => { setDenyTools(v); commitPolicy({ denyTools: v }) }} addLabel="+ Add tool" />
          <StringListField label="File write globs" values={fileWrite} onChange={v => { setFileWrite(v); commitPolicy({ fileWrite: v }) }} placeholder="default: **" addLabel="+ Add glob" />
          <StringListField label="File read globs" values={fileRead} onChange={v => { setFileRead(v); commitPolicy({ fileRead: v }) }} placeholder="default: **" addLabel="+ Add glob" />
          <StringListField label="Web allow (hosts)" values={webAllow} onChange={v => { setWebAllow(v); commitPolicy({ webAllow: v }) }} placeholder="unset: default web access" addLabel="+ Add host" />
          <StringListField label="Auto-approve tools" values={autoApproveTools} onChange={v => { setAutoApproveTools(v); commitPolicy({ autoApproveTools: v }) }} placeholder="skips the human-approval pause" addLabel="+ Add tool" />
        </div>
      </section>

      {/* Instructions file */}
      <section>
        <div className="form-label">Instructions file</div>
        <div style={{ display: 'flex', gap: 4 }}>
          <input
            className="form-input"
            value={instructionsFile}
            onChange={e => setInstructionsFile(e.target.value)}
            onBlur={() => onPatch({ instructions_file: instructionsFile })}
            placeholder="path to a .md/.txt file"
            style={{ fontSize: 11, flex: 1 }}
          />
          <button
            className="btn btn-sm btn-secondary"
            onClick={async () => {
              const path = await api.chooseInstructionsFile()
              if (path) {
                setInstructionsFile(path)
                onPatch({ instructions_file: path })
              }
            }}
          >
            Browse…
          </button>
        </div>
        <div style={{ fontSize: 9.5, color: 'var(--text-dim, #6c7b90)', marginTop: 4 }}>
          Saved with the role, but not yet read by the org runtime — reserved for a future release.
        </div>
      </section>

      {/* Reports to */}
      <section>
        <div className="form-label">Reports to</div>
        <select
          className="form-select"
          value={node.parentId ?? ''}
          onChange={e => onSetReportsTo(e.target.value === '' ? null : e.target.value)}
        >
          <option value="">— make this the root —</option>
          {otherRoles.map(n => <option key={n.id} value={n.id}>{n.title || n.id}</option>)}
        </select>
      </section>

      {/* Everything else */}
      {advancedRest && Object.keys(advancedRest).length > 0 && (
        <section>
          <div className="form-label">Advanced (read-only)</div>
          <KVBlock obj={advancedRest} />
        </section>
      )}

      <button className="btn btn-danger" onClick={onDelete} style={{ marginTop: 4 }}>
        Delete role
      </button>
    </div>
  )
}
