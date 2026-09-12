import { describe, it, expect } from 'vitest'
import { detectArtifactCandidate, resolveArtifact } from './chatArtifacts.js'

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

  it('resolves a document candidate to trusted metadata from the profile-scoped document list', async () => {
    const api = { listProfileDocuments: async () => ([{ id: 'doc-001', filename: 'a.md', path: '/vault/a.md', size_bytes: 42 }]) }
    const result = await resolveArtifact({ type: 'document', id: 'doc-001' }, api)
    expect(result).toEqual({ type: 'document', id: 'doc-001', filename: 'a.md', path: '/vault/a.md', sizeBytes: 42 })
  })

  it('returns null for a deleted document id no longer in the list', async () => {
    const api = { listProfileDocuments: async () => ([{ id: 'doc-002', filename: 'b.md', path: '/vault/b.md', size_bytes: 1 }]) }
    expect(await resolveArtifact({ type: 'document', id: 'doc-001' }, api)).toBeNull()
  })

  it('returns null for a cross-profile document id (never appears in this profile-scoped list)', async () => {
    const api = { listProfileDocuments: async () => ([]) }
    expect(await resolveArtifact({ type: 'document', id: 'doc-other-profile' }, api)).toBeNull()
  })

  it('returns null for a forged path-traversal-shaped document id', async () => {
    const api = { listProfileDocuments: async () => ([{ id: 'doc-001', filename: 'a.md', path: '/vault/a.md', size_bytes: 1 }]) }
    expect(await resolveArtifact({ type: 'document', id: '../../etc/passwd' }, api)).toBeNull()
  })

  it('returns null when listProfileDocuments itself fails (guard degrades to [])', async () => {
    const api = { listProfileDocuments: async () => [] }
    expect(await resolveArtifact({ type: 'document', id: 'doc-001' }, api)).toBeNull()
  })

  it('returns null for an unsupported candidate type', async () => {
    expect(await resolveArtifact({ type: 'mystery', id: 'x' }, {})).toBeNull()
  })
})
