// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import '../i18n.js'

const mockSave = vi.fn()
vi.mock('../services/api.js', () => ({
  api: {
    // As the GUI gets it: the key is never included.
    listAIProviders: () => Promise.resolve([{ id: 'p1', name: 'My OpenAI', provider_id: 'openai', api_key: '', vault_ref: 'v1', status: 'active' }]),
    getAIRegistry: () => Promise.resolve([{ id: 'openai', name: 'OpenAI', tier: 'frontier', models: [] }]),
    saveAIProvider: (...a) => mockSave(...a),
    testAIProvider: vi.fn(),
    deleteAIProvider: vi.fn(),
    getAIModels: () => Promise.resolve([]),
  },
}))

import AIProviders from './AIProviders.jsx'

afterEach(() => { cleanup(); vi.clearAllMocks() })

async function openManage() {
  render(<AIProviders />)
  fireEvent.click(await screen.findByLabelText('OpenAI (configured)'))
  return screen.getByLabelText('API Key')
}

// #146 item 10: the edit form never holds the stored key; blank keeps it.
describe('AI connection edit form', () => {
  it('leaves the key field empty with a keep-it placeholder, and saves a blank key', async () => {
    mockSave.mockResolvedValue({ id: 'p1' })
    const key = await openManage()
    expect(key).toHaveValue('')
    expect(key).toHaveAttribute('type', 'password')
    expect(key).toHaveAttribute('placeholder', 'Leave blank to keep the saved key')
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockSave).toHaveBeenCalled())
    expect(mockSave.mock.calls[0][0]).toMatchObject({ id: 'p1', api_key: '' })
  })

  it('sends a newly typed key', async () => {
    mockSave.mockResolvedValue({ id: 'p1' })
    const key = await openManage()
    fireEvent.change(key, { target: { value: ' sk-new ' } })
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(mockSave).toHaveBeenCalled())
    expect(mockSave.mock.calls[0][0].api_key).toBe('sk-new')
  })

  it('shows the translated title and banner', async () => {
    render(<AIProviders />)
    expect(await screen.findByText('AI connections (legacy)')).toBeInTheDocument()
    expect(screen.getByText('AI agents').tagName).toBe('STRONG')
  })
})
