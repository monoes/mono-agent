// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, afterEach, vi } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import '../../i18n.js'
import { ChatTimeline } from './ChatTimeline.jsx'

afterEach(cleanup)

describe('AgentRow in the timeline', () => {
  it('shows a worker compactly and expands to its brief and report', () => {
    const state = {
      parts: [{ kind: 'agent', agentId: 'w1' }], calls: {}, notices: [],
      agents: { w1: { agentId: 'w1', role: 'Researcher', runtime: 'claude', model: 'haiku', access: 'research', status: 'done', brief: 'find the cache', report: 'It is in **cache.go**.', summary: 'It is in cache.go.', tools: 2, costUsd: 0.002, why: 'model by rule' } },
    }
    render(<ChatTimeline state={state} isLive={false} />)
    const row = screen.getByTestId('agent-row')
    expect(row).toHaveAttribute('data-status', 'done')
    expect(row).toHaveTextContent('Researcher')
    expect(row).toHaveTextContent('claude · haiku')
    expect(row).toHaveTextContent('It is in cache.go.')
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.getByTestId('agent-row-details')).toHaveTextContent('find the cache')
    expect(screen.getByTestId('agent-row-details')).toHaveTextContent('2 tool calls · $0.0020')
  })
})

describe('AgentRow cost', () => {
  it('marks an estimated cost with "≈" and says why (#230)', async () => {
    const state = {
      parts: [{ kind: 'agent', agentId: 'w1' }], calls: {}, notices: [],
      agents: { w1: { agentId: 'w1', role: 'Coder', runtime: 'codex', model: 'gpt-5', status: 'done', tools: 3, costUsd: 0.35, costEstimated: true } },
    }
    render(<ChatTimeline state={state} isLive={false} />)
    fireEvent.click(screen.getByRole('button', { expanded: false }))
    expect(screen.getByTestId('agent-row-details')).toHaveTextContent('3 tool calls · ≈$0.3500')
    expect(screen.getByTitle('Estimated from the tokens used: this runtime reports no cost')).toBeInTheDocument()
  })
})

describe('AgentRow question', () => {
  it('shows a worker question and sends the answer through the CLI binding', async () => {
    const { api } = await import('../../services/api.js')
    api.answerAgentQuestion = vi.fn().mockResolvedValue({ answered: true })
    const state = {
      scope: { conversationId: 'c1', turnId: 't1' },
      parts: [{ kind: 'agent', agentId: 'w1' }], calls: {}, notices: [],
      agents: { w1: { agentId: 'w1', role: 'Coder', status: 'waiting_user', question: { id: 'q1', text: 'Which database?' } } },
    }
    render(<ChatTimeline state={state} isLive />)
    expect(screen.getByTestId('agent-question')).toHaveTextContent('Which database?')
    expect(screen.getByTestId('agent-row')).toHaveTextContent('waiting for you')
    fireEvent.change(screen.getByLabelText('Your answer'), { target: { value: 'Postgres' } })
    fireEvent.click(screen.getByText('Answer'))
    await waitFor(() => expect(api.answerAgentQuestion).toHaveBeenCalledWith('c1', 't1', 'w1', 'q1', 'Postgres'))
  })
})
