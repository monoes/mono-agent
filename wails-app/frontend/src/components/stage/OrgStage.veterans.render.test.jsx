// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, cleanup, within } from '@testing-library/react'
import '../../i18n.js'
import i18n from 'i18next'
import { stageReducer } from '../../lib/orgStage.js'
import { OrgStage, layoutStage } from './OrgStage.jsx'

const node = (id) => document.querySelector(`[data-testid="stage-node"][data-agent="${id}"]`)
const lead = { runtime: 'claude', model: 'opus', effort: 'high' }

beforeEach(async () => {
  await i18n.changeLanguage('en')
  window.matchMedia = vi.fn().mockImplementation(q => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }))
})
afterEach(() => cleanup())

describe('OrgStage spawn trees and veterans', () => {
  it('greys out idle veterans until the lead messages one, and draws sub-workers under their parent (#230)', () => {
    const at = seq => `2026-09-30T09:00:${String(seq).padStart(2, '0')}.000Z`
    let s = [
      { seq: 1, at: at(1), type: 'agent.spawned', payload: { agentId: 'w1', role: 'Researcher', runtime: 'claude', model: 'opus', veteran: true } },
      { seq: 2, at: at(2), type: 'agent.status', payload: { agentId: 'w1', to: 'idle', detail: 'veteran' } },
      { seq: 3, at: at(3), type: 'agent.spawned', payload: { agentId: 'w2', role: 'Coder', allowSpawn: true } },
      { seq: 4, at: at(4), type: 'agent.spawned', payload: { agentId: 'w3', parentId: 'w2', role: 'Reviewer' } },
    ].reduce(stageReducer, null)
    render(<OrgStage stage={s} leadInfo={lead} onSelect={() => {}} />)
    expect(node('w1')).toHaveClass('veteran')
    expect(node('w1')).toHaveAttribute('data-status', 'idle')
    expect(within(node('w1')).getByTestId('stage-doing')).toHaveTextContent('from an earlier turn')
    expect(node('w2')).not.toHaveClass('veteran')
    const pos = layoutStage(s)
    expect(pos.w3.y).toBeGreaterThan(pos.w2.y)
    cleanup()
    s = stageReducer(s, { seq: 5, at: at(5), type: 'agent.status', payload: { agentId: 'w1', from: 'idle', to: 'working' } })
    render(<OrgStage stage={s} leadInfo={lead} onSelect={() => {}} />)
    expect(node('w1')).not.toHaveClass('veteran')
    expect(node('w1')).toHaveAttribute('data-status', 'working')
  })
})
