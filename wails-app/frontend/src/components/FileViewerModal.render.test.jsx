// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import FileViewerModal from './FileViewerModal.jsx'
import * as WailsApp from '../wailsjs/go/main/App'

// Regression test for the reported bug: opening a .md document showed raw
// markdown source (literal '#', '**', etc.) in a monospace <pre> instead of
// rendered formatting. Confirms actual rendered elements appear, not just
// that a predicate function returns the right boolean.

vi.mock('../wailsjs/go/main/App', () => ({
  GetProfileDocumentText: vi.fn(),
  GetProfileDocumentData: vi.fn(),
  OpenPathWithOS: vi.fn(),
}))

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

describe('FileViewerModal markdown rendering', () => {
  it('renders .md content as formatted markdown, not raw source', async () => {
    WailsApp.GetProfileDocumentText.mockResolvedValue('# Hello\n\n**bold** text')

    render(<FileViewerModal doc={{ id: 'doc-1', filename: 'notes.md', size_bytes: 100 }} onClose={() => {}} />)

    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Hello' })).toBeInTheDocument())
    expect(screen.getByText('bold').tagName).toBe('STRONG')

    // The raw, unrendered markdown source must not appear anywhere.
    expect(screen.queryByText(/^# Hello/)).not.toBeInTheDocument()
    expect(screen.queryByText(/\*\*bold\*\*/)).not.toBeInTheDocument()
  })

  it('still renders non-markdown text files as raw <pre> content, unchanged', async () => {
    WailsApp.GetProfileDocumentText.mockResolvedValue('plain text, not markdown: # not a heading')

    render(<FileViewerModal doc={{ id: 'doc-2', filename: 'notes.txt', size_bytes: 100 }} onClose={() => {}} />)

    await waitFor(() => expect(screen.getByText(/plain text, not markdown/)).toBeInTheDocument())
    expect(screen.queryByRole('heading')).not.toBeInTheDocument()
  })

  it('renders a GFM table for markdown containing one', async () => {
    WailsApp.GetProfileDocumentText.mockResolvedValue('| A | B |\n| - | - |\n| 1 | 2 |\n')

    render(<FileViewerModal doc={{ id: 'doc-3', filename: 'table.md', size_bytes: 100 }} onClose={() => {}} />)

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    expect(screen.getByText('A')).toBeInTheDocument()
  })
})
