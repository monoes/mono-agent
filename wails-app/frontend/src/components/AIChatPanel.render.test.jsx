// @vitest-environment jsdom
import { describe, it, expect, afterEach, vi } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { MessageBubble, newTurnId } from './AIChatPanel.jsx'

// Regression test for the reported bug: assistant chat messages showed raw
// markdown source (literal '#', '**', a plain-text URL) instead of rendered
// formatting, so links were never clickable — the same gap
// FileViewerModal.render.test.jsx already covers for saved documents.

afterEach(() => {
  cleanup()
})

describe('AIChatPanel MessageBubble markdown rendering', () => {
  it('renders error content as formatted markdown (via the shared, scheme-gated ChatMarkdown) with a real clickable link', () => {
    // MessageBubble is only ever reached with role 'user' or 'error' in the
    // running app (the reducer/ChatTimeline path fully replaced the old
    // assistant-bubble rendering) — 'error' exercises the same non-user
    // markdown branch 'assistant' used to.
    render(
      <MessageBubble
        role="error"
        content={'# Summary\n\nSee [the report](https://example.com/report) for **details**.'}
      />
    )
    expect(screen.getByRole('heading', { name: 'Summary' })).toBeInTheDocument()
    // ChatMarkdown routes clicks through api.openURL rather than a native
    // target="_blank" navigation — no target/rel attributes are set.
    const link = screen.getByRole('link', { name: 'the report' })
    expect(link).toHaveAttribute('href', 'https://example.com/report')
    expect(screen.getByText('details').tagName).toBe('STRONG')
  })

  it('leaves user-typed content as raw text, never reinterpreted as markdown', () => {
    render(<MessageBubble role="user" content={'# not a heading\n\n**not bold**'} />)
    expect(screen.queryByRole('heading')).not.toBeInTheDocument()
    expect(screen.getByText(/# not a heading/)).toBeInTheDocument()
  })
})

// crypto.randomUUID() is gated on a secure context, and Wails' custom-
// scheme webview origin isn't guaranteed to qualify on every platform —
// jsdom/Node always provide it, so the fallback path only gets exercised
// by explicitly removing it here.
describe('newTurnId', () => {
  const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

  it('uses crypto.randomUUID when available', () => {
    expect(UUID_RE.test(newTurnId())).toBe(true)
  })

  it('falls back to crypto.getRandomValues when randomUUID is unavailable, producing a valid v4-shaped id', () => {
    vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) })
    try {
      const id = newTurnId()
      expect(UUID_RE.test(id)).toBe(true)
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('never returns the same id twice in a row', () => {
    expect(newTurnId()).not.toBe(newTurnId())
  })
})
