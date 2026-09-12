import { describe, it, expect, vi } from 'vitest'
import { detectArtifactCandidate, resolveArtifact, openArtifact } from './chatArtifacts.js'

function call(overrides) {
  return { callId: 'c1', name: 'create_workflow', status: 'completed', ok: true, result: '{}', ...overrides }
}

describe('detectArtifactCandidate', () => {
  it('extracts a workflow candidate from a completed create_workflow call', () => {
    const c = call({ name: 'create_workflow', result: JSON.stringify({ workflow_id: 'wf-1' }) })
    expect(detectArtifactCandidate(c)).toEqual({ type: 'workflow', callId: 'c1', id: 'wf-1' })
  })

  it('extracts an org candidate from a completed create_org call', () => {
    const c = call({ name: 'create_org', result: JSON.stringify({ org_name: 'Acme', created: true }) })
    expect(detectArtifactCandidate(c)).toEqual({ type: 'org', callId: 'c1', name: 'Acme' })
  })

  it('extracts a document candidate from a completed save_document call', () => {
    const c = call({ name: 'save_document', result: JSON.stringify({ filename: 'a.md', path: '/x/a.md', size_bytes: 12, vault_document_id: 'doc-001' }) })
    expect(detectArtifactCandidate(c)).toEqual({ type: 'document', callId: 'c1', id: 'doc-001' })
  })

  it('returns null when save_document succeeded but vault registration was never attempted (vault_document_id omitted)', () => {
    const c = call({ name: 'save_document', result: JSON.stringify({ filename: 'a.md', path: '/x/a.md', size_bytes: 12 }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  // ── Unsupported results ──────────────────────────────────────────────

  it('returns null for a tool name outside the allowlist', () => {
    const c = call({ name: 'list_vault_items', result: JSON.stringify({ workflow_id: 'wf-1' }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('returns null for a still-running call', () => {
    const c = call({ status: 'started', result: null })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('returns null for a failed call even if the result happens to parse', () => {
    const c = call({ ok: false, result: JSON.stringify({ workflow_id: 'wf-1' }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('treats ok === null (adapter never reported an outcome) as not failed', () => {
    const c = call({ ok: null, result: JSON.stringify({ workflow_id: 'wf-1' }) })
    expect(detectArtifactCandidate(c)).toEqual({ type: 'workflow', callId: 'c1', id: 'wf-1' })
  })

  it('returns null for unparseable JSON', () => {
    const c = call({ result: 'not json{' })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('returns null for a JSON result that is not an object', () => {
    const c = call({ result: JSON.stringify('just a string') })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('returns null when call itself is missing or malformed', () => {
    expect(detectArtifactCandidate(null)).toBeNull()
    expect(detectArtifactCandidate(undefined)).toBeNull()
    expect(detectArtifactCandidate({})).toBeNull()
  })

  // ── Malformed IDs ────────────────────────────────────────────────────

  it('rejects a non-string workflow_id', () => {
    const c = call({ result: JSON.stringify({ workflow_id: 12345 }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('rejects an empty-string workflow_id', () => {
    const c = call({ result: JSON.stringify({ workflow_id: '   ' }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('rejects a null org_name', () => {
    const c = call({ name: 'create_org', result: JSON.stringify({ org_name: null, created: true }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })

  it('rejects a non-string vault_document_id', () => {
    const c = call({ name: 'save_document', result: JSON.stringify({ vault_document_id: { id: 'doc-001' } }) })
    expect(detectArtifactCandidate(c)).toBeNull()
  })
})

describe('resolveArtifact', () => {
  it('returns null for a null candidate', async () => {
    expect(await resolveArtifact(null, {})).toBeNull()
  })

  // ── workflow ─────────────────────────────────────────────────────────

  it('resolves a workflow candidate to trusted metadata (name + id) via api.getWorkflow', async () => {
    const api = { getWorkflow: async (id) => ({ id, name: 'My Workflow' }) }
    const result = await resolveArtifact({ type: 'workflow', id: 'wf-1' }, api)
    expect(result).toEqual({ type: 'workflow', id: 'wf-1', name: 'My Workflow' })
  })

  it('falls back to the id as the display name when the workflow has no name', async () => {
    const api = { getWorkflow: async (id) => ({ id, name: '' }) }
    const result = await resolveArtifact({ type: 'workflow', id: 'wf-1' }, api)
    expect(result).toEqual({ type: 'workflow', id: 'wf-1', name: 'wf-1' })
  })

  it('returns null for a deleted workflow (api.getWorkflow resolves null via its own guard)', async () => {
    const api = { getWorkflow: async () => null }
    expect(await resolveArtifact({ type: 'workflow', id: 'wf-gone' }, api)).toBeNull()
  })

  it('returns null for a cross-profile workflow id (GetWorkflow rejects it server-side, guard turns it into null)', async () => {
    const api = { getWorkflow: async () => null }
    expect(await resolveArtifact({ type: 'workflow', id: 'wf-other-profile' }, api)).toBeNull()
  })

  it('returns null for a malformed workflow lookup response missing an id', async () => {
    const api = { getWorkflow: async () => ({ name: 'Suspicious' }) }
    expect(await resolveArtifact({ type: 'workflow', id: 'wf-1' }, api)).toBeNull()
  })

  // ── org ──────────────────────────────────────────────────────────────

  it('resolves an org candidate when the name appears in the real org listing (array shape)', async () => {
    const api = { listOrgDesigns: async () => ([{ name: 'Acme' }, { name: 'Other' }]) }
    expect(await resolveArtifact({ type: 'org', name: 'Acme' }, api)).toEqual({ type: 'org', name: 'Acme' })
  })

  it('resolves an org candidate when the listing comes back as {items: [...]}', async () => {
    const api = { listOrgDesigns: async () => ({ items: [{ name: 'Acme' }] }) }
    expect(await resolveArtifact({ type: 'org', name: 'Acme' }, api)).toEqual({ type: 'org', name: 'Acme' })
  })

  it('returns null for a forged/hallucinated org name absent from the real listing', async () => {
    const api = { listOrgDesigns: async () => ([{ name: 'Acme' }]) }
    expect(await resolveArtifact({ type: 'org', name: 'Not Real Org' }, api)).toBeNull()
  })

  it('returns null when the org listing itself fails (api.js guard already degrades to null)', async () => {
    const api = { listOrgDesigns: async () => null }
    expect(await resolveArtifact({ type: 'org', name: 'Acme' }, api)).toBeNull()
  })

  // ── document ─────────────────────────────────────────────────────────

  it('resolves a document candidate to trusted metadata via the scoped by-id lookup', async () => {
    const api = { getProfileDocument: async (id) => (id === 'doc-001' ? { id: 'doc-001', filename: 'a.md', path: '/vault/a.md', size_bytes: 42 } : null) }
    const result = await resolveArtifact({ type: 'document', id: 'doc-001' }, api)
    expect(result).toEqual({ type: 'document', id: 'doc-001', filename: 'a.md', path: '/vault/a.md', sizeBytes: 42 })
  })

  it('returns null for a deleted document id the lookup no longer resolves', async () => {
    const api = { getProfileDocument: async () => null }
    expect(await resolveArtifact({ type: 'document', id: 'doc-001' }, api)).toBeNull()
  })

  it('returns null for a cross-profile document id (the scoped lookup never resolves it)', async () => {
    const api = { getProfileDocument: async () => null }
    expect(await resolveArtifact({ type: 'document', id: 'doc-other-profile' }, api)).toBeNull()
  })

  it('returns null for a forged path-traversal-shaped document id', async () => {
    const api = { getProfileDocument: async (id) => (id === 'doc-001' ? { id: 'doc-001', filename: 'a.md', path: '/vault/a.md', size_bytes: 1 } : null) }
    expect(await resolveArtifact({ type: 'document', id: '../../etc/passwd' }, api)).toBeNull()
  })

  it('returns null when getProfileDocument itself fails (guard degrades to null)', async () => {
    const api = { getProfileDocument: async () => null }
    expect(await resolveArtifact({ type: 'document', id: 'doc-001' }, api)).toBeNull()
  })

  it('returns null for an unsupported candidate type', async () => {
    expect(await resolveArtifact({ type: 'mystery', id: 'x' }, {})).toBeNull()
  })
})

// ── openArtifact: click-time revalidation before acting ─────────────────────
//
// useResolvedArtifacts.js's own cache never expires — once a card resolves
// it stays resolved for the life of the panel, even if the underlying
// workflow/org/document is deleted a minute later. openArtifact re-runs the
// same detect+resolve lookup right before actually acting on a click,
// rather than trusting the cached snapshot passed in as `artifact`. `api`,
// `notify` and `onOpen` are injected explicitly (same reasoning as
// resolveArtifact's own explicit `api` parameter above) so this stays a
// generic, panel-agnostic helper with no import of services/api.js.
describe('openArtifact', () => {
  function call(overrides) {
    return { callId: 'c1', name: 'create_org', status: 'completed', ok: true, result: JSON.stringify({ org_name: 'Acme' }), ...overrides }
  }

  it('re-validates and calls onOpen with the freshly-resolved artifact when it still exists', async () => {
    const api = { listOrgDesigns: async () => ([{ name: 'Acme' }]) }
    const notify = vi.fn()
    const onOpen = vi.fn()

    await openArtifact(call(), { type: 'org', name: 'Acme' }, { api, notify, onOpen })

    expect(onOpen).toHaveBeenCalledWith({ type: 'org', name: 'Acme' })
    expect(notify).not.toHaveBeenCalled()
  })

  it('refuses to open and notifies using the cached artifact\'s own type when the fresh lookup no longer finds it (deleted since the card resolved)', async () => {
    const api = { listOrgDesigns: async () => ([]) } // Acme no longer present
    const notify = vi.fn()
    const onOpen = vi.fn()

    await openArtifact(call(), { type: 'org', name: 'Acme' }, { api, notify, onOpen })

    expect(onOpen).not.toHaveBeenCalled()
    expect(notify).toHaveBeenCalledWith('chat', "Couldn't confirm this org still exists — not opening it.")
  })

  it('treats a rejected lookup the same as a null result (last-resort safety net, not the expected path)', async () => {
    const api = { listOrgDesigns: async () => { throw new Error('boom') } }
    const notify = vi.fn()
    const onOpen = vi.fn()

    await openArtifact(call(), { type: 'org', name: 'Acme' }, { api, notify, onOpen })

    expect(onOpen).not.toHaveBeenCalled()
    expect(notify).toHaveBeenCalledWith('chat', "Couldn't confirm this org still exists — not opening it.")
  })

  it('does nothing — no api call, no notify, no onOpen — when the call itself no longer yields a candidate', async () => {
    const api = { listOrgDesigns: vi.fn(async () => ([{ name: 'Acme' }])) }
    const notify = vi.fn()
    const onOpen = vi.fn()

    await openArtifact(call({ name: 'list_vault_items' }), { type: 'org', name: 'Acme' }, { api, notify, onOpen })

    expect(api.listOrgDesigns).not.toHaveBeenCalled()
    expect(notify).not.toHaveBeenCalled()
    expect(onOpen).not.toHaveBeenCalled()
  })

  it('does not throw when onOpen is not provided and the fresh lookup succeeds', async () => {
    const api = { listOrgDesigns: async () => ([{ name: 'Acme' }]) }
    const notify = vi.fn()

    await expect(openArtifact(call(), { type: 'org', name: 'Acme' }, { api, notify })).resolves.toBeUndefined()
  })

  it('uses the cached artifact\'s type in the notice, not the candidate\'s or the (absent) fresh result\'s', async () => {
    // Regression guard: the message must read `artifact.type` (the second
    // argument, the previously-cached/rendered artifact) — a rewrite that
    // swapped in `candidate.type` would happen to read the same value for
    // org/workflow/document today, but only because detectArtifactCandidate
    // and resolveArtifact always agree on `type`; asserting the literal
    // string here still pins the intended source.
    const api = { getProfileDocument: async () => null }
    const notify = vi.fn()
    const documentCall = { callId: 'c2', name: 'save_document', status: 'completed', ok: true, result: JSON.stringify({ vault_document_id: 'doc-1' }) }

    await openArtifact(documentCall, { type: 'document', id: 'doc-1', filename: 'a.md' }, { api, notify })

    expect(notify).toHaveBeenCalledWith('chat', "Couldn't confirm this document still exists — not opening it.")
  })
})
