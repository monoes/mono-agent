// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
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
