// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/react'
import CaptureViewerModal, { initialCaptureTab, summaryStatusText } from './CaptureViewerModal.jsx'
import * as WailsApp from '../wailsjs/go/main/App'

vi.mock('../wailsjs/go/main/App', () => ({
  GetCaptureView: vi.fn(),
  OpenURL: vi.fn(),
}))

beforeEach(() => vi.clearAllMocks())
afterEach(() => cleanup())

const longText = '# Article\n\n' + 'word '.repeat(80)
const shot = 'data:image/png;base64,AAAA'

describe('initialCaptureTab', () => {
  it('opens on the text when there is an article', () => {
    expect(initialCaptureTab({ readable: longText, screenshot: shot })).toBe('text')
  })
  it('opens on the screenshot when the text is only a scrap', () => {
    expect(initialCaptureTab({ readable: 'Gastown is a place.', screenshot: shot })).toBe('screenshot')
  })
  it('opens on the screenshot when there is no text', () => {
    expect(initialCaptureTab({ readable: '', screenshot: shot })).toBe('screenshot')
  })
  it('keeps short text when there is nothing else', () => {
    expect(initialCaptureTab({ readable: 'short', screenshot: '' })).toBe('text')
  })
  it('opens on the summary when one was written', () => {
    expect(initialCaptureTab({ readable: longText, screenshot: shot, summary: '## TL;DR\nx' })).toBe('summary')
  })
  it('does not open on a summary that is not there yet', () => {
    expect(initialCaptureTab({ readable: longText, screenshot: shot, summary: '', summary_status: { status: 'pending' } })).toBe('text')
  })
})

describe('summaryStatusText', () => {
  it('says where the summary is', () => {
    expect(summaryStatusText({ status: 'done', runtime: 'claude' })).toBe('Summary by claude')
    expect(summaryStatusText({ status: 'running', runtime: 'claude' })).toMatch(/being written by claude/)
    expect(summaryStatusText({ status: 'error', error: 'claude: not installed' })).toBe('Summary failed: claude: not installed')
    expect(summaryStatusText(null)).toBe('')
  })
})

describe('CaptureViewerModal summary and transcript tabs', () => {
  const doc = { id: 'doc-2', filename: 'Me at the zoo', url: 'https://www.youtube.com/watch?v=jNQXAC9IVRw' }
  const video = {
    title: 'Me at the zoo', url: doc.url, readable: longText, screenshot: shot,
    summary: '# Summary: Me at the zoo\n\n## TL;DR\n\nElephants have long trunks.',
    transcript: '# Transcript: Me at the zoo\n\n[0:01](https://www.youtube.com/watch?v=jNQXAC9IVRw&t=1s) All right, so here we are',
    summary_status: { status: 'done', runtime: 'claude' },
  }

  it('opens on the summary, and the transcript is a tab away', async () => {
    WailsApp.GetCaptureView.mockResolvedValue(video)
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    expect(await screen.findByText('Elephants have long trunks.')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Summary' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(screen.getByRole('tab', { name: 'Transcript' }))
    expect(screen.getByRole('link', { name: '0:01' })).toHaveAttribute('href', 'https://www.youtube.com/watch?v=jNQXAC9IVRw&t=1s')
    expect(screen.getByText(/All right, so here we are/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: /^Text/ }))
    expect(screen.getByRole('heading', { level: 1, name: 'Article' })).toBeInTheDocument()
  })

  it('has no summary or transcript tab for a plain capture', async () => {
    WailsApp.GetCaptureView.mockResolvedValue({ title: 'Article', url: doc.url, readable: longText, screenshot: shot })
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    await screen.findByRole('heading', { level: 1, name: 'Article' })
    expect(screen.queryByRole('tab', { name: /Summary/ })).toBeNull()
    expect(screen.queryByRole('tab', { name: 'Transcript' })).toBeNull()
  })

  it('says why a summary is missing instead of showing nothing', async () => {
    WailsApp.GetCaptureView.mockResolvedValue({
      title: 'Article', url: doc.url, readable: longText, screenshot: shot, summary: '',
      summary_status: { status: 'error', runtime: 'claude', error: 'claude: not installed' },
    })
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('tab', { name: 'Summary (not ready)' }))
    expect(screen.getByRole('status')).toHaveTextContent('Summary failed: claude: not installed')
  })
})

describe('CaptureViewerModal', () => {
  const doc = { id: 'doc-1', filename: 'Search Results', url: 'https://www.google.com/search?q=gastown', created_at: '2026-09-22 11:00:08' }

  it('shows the screenshot of a page with no readable text, and never asks the OS', async () => {
    WailsApp.GetCaptureView.mockResolvedValue({ title: 'Jev Picker Plan', url: 'https://claude.ai/artifact/x', readable: '', screenshot: shot })
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    const img = await screen.findByRole('img', { name: /Screenshot of Jev Picker Plan/ })
    expect(img).toHaveAttribute('src', shot)
    expect(screen.getByRole('tab', { name: /Text \(none found\)/ })).toBeDisabled()
  })

  it('renders the article as markdown and switches to the screenshot', async () => {
    WailsApp.GetCaptureView.mockResolvedValue({ title: 'Article', url: doc.url, readable: longText, screenshot: shot })
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Article' })).toBeInTheDocument())
    fireEvent.click(screen.getByRole('tab', { name: 'Screenshot' }))
    expect(screen.getByRole('img')).toHaveAttribute('src', shot)
  })

  it('opens the original page in the browser', async () => {
    WailsApp.GetCaptureView.mockResolvedValue({ title: 'Search Results', url: doc.url, readable: '', screenshot: shot })
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: /Open original/ }))
    expect(WailsApp.OpenURL).toHaveBeenCalledWith(doc.url)
  })

  it('says what went wrong when the capture cannot be read', async () => {
    WailsApp.GetCaptureView.mockRejectedValue(new Error('reading capture: gone'))
    render(<CaptureViewerModal doc={doc} onClose={() => {}} />)
    expect(await screen.findByText(/reading capture: gone/)).toBeInTheDocument()
  })
})
