// @vitest-environment jsdom
import { describe, it, expect, afterEach, vi } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { MessageBubble, newTurnId, composeLiveAnnouncement } from './AIChatPanel.jsx'

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

// composeLiveAnnouncement builds the one text fired into the panel's
// persistent aria-live region when a turn finalizes — the single moment
// TurnStatus's own per-turn "role=status" region is guaranteed silent to
// screen readers, since it unmounts at exactly the instant this content
// would appear in a freshly-mounted replacement instead of mutating an
// already-present node (a well-known AT limitation, not a jsdom-testable
// one — this only verifies the text composed, not that it is announced).
describe('composeLiveAnnouncement', () => {
  const base = { terminal: { status: 'completed', reason: 'end_turn' }, calls: {}, notices: [] }

  it('announces plain completion', () => {
    expect(composeLiveAnnouncement(base)).toBe('Response completed.')
  })

  it('announces failure and stop with their own honest labels', () => {
    expect(composeLiveAnnouncement({ ...base, terminal: { status: 'failed', reason: 'boom' } })).toBe('Response failed.')
    expect(composeLiveAnnouncement({ ...base, terminal: { status: 'cancelled', reason: 'stopped' } })).toBe('Response stopped.')
  })

  it('adds a singular tool-failure summary', () => {
    const calls = { c1: { callId: 'c1', status: 'completed', ok: false } }
    expect(composeLiveAnnouncement({ ...base, calls })).toBe('Response completed. 1 tool call failed.')
  })

  it('adds a plural tool-failure summary and ignores successful calls', () => {
    const calls = {
      c1: { callId: 'c1', status: 'completed', ok: false },
      c2: { callId: 'c2', status: 'completed', ok: true },
      c3: { callId: 'c3', status: 'completed', ok: false },
    }
    expect(composeLiveAnnouncement({ ...base, calls })).toBe('Response completed. 2 tool calls failed.')
  })

  it('appends any notices already present at finalize time, e.g. historySaved:false', () => {
    const notices = [{ code: 'history_not_saved', message: 'History may not have saved.', severity: 'warning' }]
    expect(composeLiveAnnouncement({ ...base, notices })).toBe('Response completed. History may not have saved.')
  })

  it('combines a tool failure and a notice in one announcement', () => {
    const calls = { c1: { callId: 'c1', status: 'completed', ok: false } }
    const notices = [{ code: 'x', message: 'Something else happened.', severity: 'info' }]
    expect(composeLiveAnnouncement({ ...base, calls, notices })).toBe('Response completed. 1 tool call failed. Something else happened.')
  })

  // alreadyAnnouncedCount: notices the panel already spoke individually
  // mid-turn (see the live-turn effect in AIChatPanel.jsx) must not be
  // repeated in this finalize-time summary.
  it('excludes notices already individually announced mid-turn, given alreadyAnnouncedCount', () => {
    const notices = [
      { code: 'a', message: 'First mid-turn notice.', severity: 'info' },
      { code: 'b', message: 'Second mid-turn notice.', severity: 'info' },
    ]
    expect(composeLiveAnnouncement({ ...base, notices }, 1)).toBe('Response completed. Second mid-turn notice.')
    expect(composeLiveAnnouncement({ ...base, notices }, 2)).toBe('Response completed.')
  })

  it('defaults alreadyAnnouncedCount to 0 when omitted, unchanged from before', () => {
    const notices = [{ code: 'a', message: 'First mid-turn notice.', severity: 'info' }]
    expect(composeLiveAnnouncement({ ...base, notices })).toBe('Response completed. First mid-turn notice.')
  })
})
