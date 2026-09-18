// @vitest-environment jsdom
// C-46: the grant dialog warns when the workflow's file nodes take their
// paths from the run's input, and says which of them cannot be confined to
// the role's workdir.
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'
import GrantDialog, { fileInputSummary } from './GrantDialog.jsx'
import en from '../../locales/en.json'
import es from '../../locales/es.json'

vi.mock('../../services/api.js', () => ({ api: { setOrgGrant: vi.fn() }, notify: vi.fn() }))
// Assert against keys and interpolation values rather than bootstrapping
// i18next (the convention of the other render tests).
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key, opts) => (opts ? `${key} ${JSON.stringify(opts)}` : key) }),
}))

afterEach(cleanup)

const role = { id: 'writer', title: 'Writer', type: 'specialist', parentId: 'lead', responsibilities: [], rest: { policy: { denyTools: ['Bash'] } } }
const SAVE = { node: 'save (data.write_binary_file)', type: 'data.write_binary_file', field: 'file_path', access: 'write', confined: true }
const SHELL = { node: 'cat (system.execute_command)', type: 'system.execute_command', field: 'args', access: 'exec', confined: false }
const automation = (nodes) => ({
  workflow_id: 'wf-1', alias: 'save_note', workflow_name: 'Save note', has_outbound_nodes: false,
  outbound_nodes: [], exists: true, file_input_nodes: nodes,
})

describe('GrantDialog file-input warning', () => {
  it('lists confined file nodes with their access', () => {
    render(<GrantDialog open orgName="growth" role={role} automation={automation([SAVE])} onClose={vi.fn()} />)
    const box = screen.getByTestId('grant-file-inputs')
    expect(box).toHaveTextContent('grantDialog.fileInput.title')
    expect(box).toHaveTextContent('grantDialog.fileInput.confined {"role":"Writer"}')
    expect(box).toHaveTextContent('save (data.write_binary_file)')
    expect(box).toHaveTextContent('grantDialog.fileInput.access.write')
    expect(box).not.toHaveTextContent('grantDialog.fileInput.unconfined')
  })

  it('calls out nodes that cannot be confined', () => {
    render(<GrantDialog open orgName="growth" role={role} automation={automation([SAVE, SHELL])} onClose={vi.fn()} />)
    expect(screen.getByTestId('grant-file-inputs')).toHaveTextContent(
      'grantDialog.fileInput.unconfined {"nodes":"cat (system.execute_command)"}')
  })

  it('shows nothing for workflows without file inputs or from an older CLI', () => {
    const { rerender } = render(<GrantDialog open orgName="growth" role={role} automation={automation([])} onClose={vi.fn()} />)
    expect(screen.queryByTestId('grant-file-inputs')).toBeNull()
    const legacy = automation(undefined)
    delete legacy.file_input_nodes
    rerender(<GrantDialog open orgName="growth" role={role} automation={legacy} onClose={vi.fn()} />)
    expect(screen.queryByTestId('grant-file-inputs')).toBeNull()
  })

  it('summarises nodes purely', () => {
    expect(fileInputSummary(null)).toEqual({ nodes: [], unconfined: [] })
    expect(fileInputSummary(automation([SAVE, SHELL]))).toEqual({ nodes: [SAVE, SHELL], unconfined: [SHELL] })
  })

  it('has every string in English and Spanish', () => {
    for (const locale of [en, es]) {
      const fi = locale.grantDialog.fileInput
      for (const key of ['title', 'confined', 'unconfined']) expect(fi[key]).toBeTruthy()
      for (const key of ['read', 'write', 'read_write', 'exec']) expect(fi.access[key]).toBeTruthy()
      expect(fi.confined).toContain('{{role}}')
      expect(fi.unconfined).toContain('{{nodes}}')
    }
  })
})
