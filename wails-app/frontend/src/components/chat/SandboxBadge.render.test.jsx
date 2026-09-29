// @vitest-environment jsdom
// The sandbox badge: the CLI journals its verdict on a turn's sandbox (an
// agent.sandbox notice when the runtime starts, and turn.finished's
// sandbox), the reducer keeps it, and the timeline shows it as a badge.
import { describe, it, expect, afterEach, beforeEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup } from '@testing-library/react'
import i18n from '../../i18n.js'
import { SandboxBadge } from './SandboxBadge.jsx'
import { ChatTimeline } from './ChatTimeline.jsx'
import { chatReducer, initialChatState } from './chatReducer.js'

function reduce(events) {
  let state = initialChatState()
  for (const ev of events) state = chatReducer(state, { type: 'event', event: ev })
  return state
}

let seq = 0
const ev = (type, payload) => ({ seq: ++seq, type, payload, at: new Date().toISOString(), conversationId: 'c1', turnId: 't1' })

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(cleanup)

describe('SandboxBadge', () => {
  it.each([
    ['sandboxed', 'sandboxed'],
    ['unsupported', 'no sandbox (runtime unsupported)'],
    ['needs-monomind', 'no sandbox (update monomind)'],
  ])('renders %s in English', (status, text) => {
    render(<SandboxBadge status={status} />)
    const badge = screen.getByTestId('sandbox-badge')
    expect(badge).toHaveTextContent(text)
    expect(badge).toHaveAttribute('data-status', status)
    expect(badge.getAttribute('title')).toBeTruthy()
  })

  it('renders Spanish strings', async () => {
    await i18n.changeLanguage('es')
    render(<SandboxBadge status="needs-monomind" />)
    expect(screen.getByTestId('sandbox-badge')).toHaveTextContent('sin sandbox (actualiza monomind)')
  })

  it('renders nothing for no status or an unknown one', () => {
    const { container } = render(<><SandboxBadge status={null} /><SandboxBadge status="bogus" /></>)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('sandbox in the chat reducer and timeline', () => {
  it('takes the agent.sandbox notice as the badge, not a banner', () => {
    const state = reduce([
      ev('turn.started', { backend: 'agent', runtime: 'codex', text: 'hi' }),
      ev('notice', { code: 'agent.sandbox', message: 'sandboxed', severity: 'info' }),
    ])
    expect(state.sandbox).toBe('sandboxed')
    expect(state.notices).toEqual([])
    render(<ChatTimeline state={state} />)
    expect(screen.getByTestId('sandbox-badge')).toHaveTextContent('sandboxed')
  })

  it('takes turn.finished sandbox (history replay)', () => {
    const state = reduce([
      ev('turn.started', { backend: 'agent', runtime: 'qwen', text: 'hi' }),
      ev('assistant.delta', { partId: 'part-1', text: 'hello' }),
      ev('turn.finished', { status: 'completed', exitCode: 0, historySaved: true, sandbox: 'unsupported' }),
    ])
    expect(state.sandbox).toBe('unsupported')
    render(<ChatTimeline state={state} />)
    expect(screen.getByTestId('sandbox-badge')).toHaveTextContent('no sandbox (runtime unsupported)')
  })

  it('shows no badge for a turn that asked for no sandbox (coder mode)', () => {
    const state = reduce([
      ev('turn.started', { backend: 'agent', runtime: 'claude', text: 'hi' }),
      ev('assistant.delta', { partId: 'part-1', text: 'hello' }),
      ev('turn.finished', { status: 'completed', exitCode: 0, historySaved: true }),
    ])
    expect(state.sandbox).toBeNull()
    render(<ChatTimeline state={state} />)
    expect(screen.queryByTestId('sandbox-badge')).toBeNull()
  })
})
