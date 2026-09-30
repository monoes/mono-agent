// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, act, within } from '@testing-library/react'
import '../../i18n.js'
import i18n from 'i18next'
import journal from '../../lib/__fixtures__/orgStageJournal.json'
import { replayStage, stageReducer } from '../../lib/orgStage.js'
import { reduceTurnEvents } from '../chat/useChatStream.js'
import { OrgStage, layoutStage, fitStage } from './OrgStage.jsx'
import { StageDrawer } from './StageDrawer.jsx'
import { FLIGHT_MS, BUBBLE_MS } from './useStageMotion.js'

const T0 = Date.parse('2026-09-30T10:00:00.000Z')
const node = (id) => document.querySelector(`[data-testid="stage-node"][data-agent="${id}"]`)
const lead = { runtime: 'claude', model: 'opus', effort: 'high' }

function mockMotion(reduce) {
  window.matchMedia = vi.fn().mockImplementation(q => ({ matches: reduce && q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {} }))
}

beforeEach(async () => {
  await i18n.changeLanguage('en')
  mockMotion(false)
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('OrgStage', () => {
  it('draws the recorded 4-agent org with its quest log, leases and scoreboard', () => {
    render(<OrgStage stage={replayStage(journal)} leadInfo={lead} turnId="turn-1" onSelect={() => {}} />)
    expect(screen.getAllByTestId('stage-node')).toHaveLength(6)
    expect(node('lead')).toHaveAttribute('data-status', 'done')
    expect(node('w4')).toHaveAttribute('data-status', 'failed')
    expect(node('native:w1:t1')).toHaveClass('native')
    // The reassigned worker shows its old model crossed out.
    expect(within(node('w2')).getByTestId('stage-old-model')).toHaveTextContent('opus')
    expect(within(node('w2')).getByTestId('stage-model')).toHaveTextContent('gpt-5')
    expect(within(node('lead')).getByTestId('stage-model')).toHaveTextContent('opus')
    expect(node('w2')).toHaveAttribute('aria-label', expect.stringContaining('Coder, done'))
    expect(screen.getAllByTestId('stage-quest')).toHaveLength(5)
    expect(screen.getByTestId('stage-quests')).toHaveTextContent('5/5')
    expect(screen.getByTestId('stage-lease-pen')).toHaveTextContent('free')
    const score = screen.getByTestId('stage-scoreboard')
    expect(score).toHaveTextContent('Agents5 (1 failed)')
    expect(score).toHaveTextContent('Time15s')
    expect(score).toHaveTextContent('Cost≈$0.14')
    expect(score).toHaveTextContent('Files changed1')
    expect(score).toHaveTextContent('Tests1/2 passed')
    fireEvent.click(within(score).getByRole('button'))
    expect(screen.queryByTestId('stage-scoreboard')).toBeNull()
  })

  it('says what a working agent is doing and who holds the pen', () => {
    const upTo = seq => replayStage(journal.filter(e => e.seq <= seq))
    const s = upTo(journal.find(e => e.payload.callId === 'w2:s1').seq)
    render(<OrgStage stage={s} leadInfo={lead} onSelect={() => {}} />)
    expect(within(node('w2')).getByTestId('stage-doing')).toHaveTextContent('running go test ./internal/cache/...')
    expect(within(node('w3')).getByTestId('stage-doing')).toHaveTextContent('waiting for the pen')
    expect(screen.getByTestId('stage-lease-pen')).toHaveTextContent('Coder')
    expect(screen.queryByTestId('stage-scoreboard')).toBeNull()
  })

  it('shows the lead alone with its idle line before any turn', () => {
    render(<OrgStage stage={null} leadInfo={lead} leadIdle="ready for your first message" onSelect={() => {}} />)
    expect(screen.getAllByTestId('stage-node')).toHaveLength(1)
    expect(node('lead')).toHaveTextContent('ready for your first message')
    expect(screen.queryByTestId('stage-quests')).toBeNull()
  })

  it('selects a node by click and by keyboard', () => {
    const onSelect = vi.fn()
    render(<OrgStage stage={replayStage(journal)} leadInfo={lead} selectedId="w1" onSelect={onSelect} />)
    expect(node('w1')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(node('w3'))
    expect(onSelect).toHaveBeenCalledWith('w3')
    // A node is a real button: focusable, and Enter/Space click it natively.
    node('w2').focus()
    expect(document.activeElement).toBe(node('w2'))
    fireEvent.click(screen.getAllByTestId('stage-quest')[3])
    expect(onSelect).toHaveBeenCalledWith('w4')
  })

  it('flies a fresh brief, then a result that leaves a speech bubble for a few seconds', () => {
    vi.useFakeTimers({ now: T0 + 3000 })
    const upTo = seq => replayStage(journal.filter(e => e.seq <= seq))
    const briefSeq = journal.find(e => e.type === 'agent.message' && e.payload.agentId === 'w1').seq
    const { rerender } = render(<OrgStage stage={upTo(briefSeq)} leadInfo={lead} onSelect={() => {}} />)
    act(() => { vi.advanceTimersByTime(200) })
    expect(screen.getAllByTestId('stage-flight').map(f => f.dataset.kind)).toEqual(['brief'])
    act(() => { vi.advanceTimersByTime(FLIGHT_MS) })
    expect(screen.queryByTestId('stage-flight')).toBeNull()
    expect(screen.queryByTestId('stage-speech')).toBeNull()

    vi.setSystemTime(T0 + 10500)
    const resultSeq = journal.find(e => e.type === 'agent.message' && e.payload.agentId === 'w1' && e.payload.direction === 'result').seq
    rerender(<OrgStage stage={upTo(resultSeq)} leadInfo={lead} onSelect={() => {}} />)
    act(() => { vi.advanceTimersByTime(200) })
    // w2's reassignment and w1's native subagent are fresh too; the w1
    // result is the newest flight.
    expect(screen.getAllByTestId('stage-flight').some(f => f.dataset.kind === 'result')).toBe(true)
    act(() => { vi.advanceTimersByTime(FLIGHT_MS) })
    expect(screen.getAllByTestId('stage-speech').map(b => b.textContent).join(' ')).toContain('cache.go:88')
    act(() => { vi.advanceTimersByTime(BUBBLE_MS) })
    expect(screen.queryByTestId('stage-speech')).toBeNull()
  })

  it('lands every flight under StrictMode\'s dev re-mount', () => {
    vi.useFakeTimers({ now: T0 + 3000 })
    const briefSeq = journal.find(e => e.type === 'agent.message' && e.payload.agentId === 'w1').seq
    render(<React.StrictMode><OrgStage stage={replayStage(journal.filter(e => e.seq <= briefSeq))} leadInfo={lead} onSelect={() => {}} /></React.StrictMode>)
    act(() => { vi.advanceTimersByTime(200) })
    expect(screen.getAllByTestId('stage-flight')).toHaveLength(1)
    act(() => { vi.advanceTimersByTime(FLIGHT_MS) })
    expect(screen.queryByTestId('stage-flight')).toBeNull()
  })

  it('does not replay old flights when a finished turn is opened', () => {
    vi.useFakeTimers({ now: T0 + 3600_000 })
    render(<OrgStage stage={replayStage(journal)} leadInfo={lead} onSelect={() => {}} />)
    act(() => { vi.advanceTimersByTime(200) })
    expect(screen.queryByTestId('stage-flight')).toBeNull()
    expect(screen.queryByTestId('stage-speech')).toBeNull()
  })

  it('under reduced motion, lands results at once instead of flying them', () => {
    mockMotion(true)
    vi.useFakeTimers({ now: T0 + 10500 })
    const resultSeq = journal.find(e => e.type === 'agent.message' && e.payload.direction === 'result').seq
    render(<OrgStage stage={replayStage(journal.filter(e => e.seq <= resultSeq))} leadInfo={lead} onSelect={() => {}} />)
    act(() => { vi.advanceTimersByTime(200) })
    expect(screen.getByTestId('org-stage')).toHaveAttribute('data-reduced-motion', 'true')
    expect(screen.queryByTestId('stage-flight')).toBeNull()
    expect(screen.getAllByTestId('stage-speech').map(b => b.textContent).join(' ')).toContain('cache.go:88')
  })

  it('pins "needs you" on an agent asking a question', () => {
    let s = replayStage(journal.slice(0, 30))
    s = stageReducer(s, { seq: 500, at: '2026-09-30T10:00:05.000Z', type: 'agent.message', payload: { agentId: 'w1', direction: 'question', from: 'w1', to: 'user', text: 'Which cache?' } })
    render(<OrgStage stage={s} leadInfo={lead} onSelect={() => {}} />)
    expect(node('w1')).toHaveClass('needs')
    expect(within(node('w1')).getByTestId('stage-needs-you')).toHaveTextContent('needs you')
  })

  it('renders in Spanish', async () => {
    await i18n.changeLanguage('es')
    render(<OrgStage stage={replayStage(journal)} leadInfo={lead} onSelect={() => {}} />)
    expect(screen.getByTestId('stage-scoreboard')).toHaveTextContent('Turno completo')
    expect(node('lead')).toHaveTextContent('Líder')
  })
})

describe('stage layout', () => {
  it('puts the lead on top, workers below and a native subagent under its caller', () => {
    const pos = layoutStage(replayStage(journal))
    expect(pos.lead.y).toBe(0)
    expect(pos.w1.y).toBeGreaterThan(pos.lead.y)
    expect(pos['native:w1:t1'].y).toBeGreaterThan(pos.w1.y)
    expect(new Set(['w1', 'w2', 'w3', 'w4'].map(id => pos[id].y)).size).toBe(1)
  })

  it('fits the org into the viewport without blowing a lone lead up', () => {
    const cam = fitStage({ lead: { x: 0, y: 0 } }, 800, 300)
    expect(cam.zoom).toBeLessThanOrEqual(1.1)
    const all = fitStage(layoutStage(replayStage(journal)), 400, 200)
    expect(all.zoom).toBeLessThan(1)
  })
})

describe('StageDrawer', () => {
  const stage = replayStage(journal)
  const { agentCalls } = reduceTurnEvents(journal)

  it('shows the brief, why, model and access, messages and tool cards', () => {
    render(<StageDrawer node={stage.nodes.w2} calls={agentCalls} turnId="turn-1" isLive={false} onClose={() => {}} />)
    const drawer = screen.getByTestId('stage-drawer')
    expect(drawer).toHaveTextContent('Make the cache test deterministic')
    expect(screen.getByTestId('stage-why')).toHaveTextContent('lead chose the model')
    expect(screen.getByTestId('stage-why')).toHaveTextContent('role pick 64%')
    expect(drawer).toHaveTextContent('access: coding')
    expect(drawer).toHaveTextContent('Switched from claude/opus: quota: weekly limit reached')
    expect(screen.getByTestId('stage-transcript')).toHaveTextContent('Replaced the sleep with a fake clock')
    expect(within(screen.getByTestId('stage-tools')).getAllByRole('button').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByTestId('stage-tools')).toHaveTextContent('go test ./internal/cache/...')
  })

  it('shows a native subagent\'s own calls flat, outside its caller\'s Task card', () => {
    render(<StageDrawer node={stage.nodes['native:w1:t1']} calls={agentCalls} turnId="t" onClose={() => {}} />)
    expect(screen.getByTestId('stage-tools')).toHaveTextContent('cache_test.go')
  })

  it('nests a native subagent card in its caller and offers Stop only when it can', () => {
    const onClose = vi.fn()
    const onStop = vi.fn()
    const { rerender } = render(<StageDrawer node={stage.nodes.w1} calls={agentCalls} turnId="t" onClose={onClose} />)
    expect(screen.getByTestId('stage-stop')).toBeDisabled()
    expect(screen.getByTestId('stage-stop')).toHaveAttribute('title', expect.stringContaining('running turn'))
    fireEvent.click(screen.getByLabelText('Close the agent details'))
    expect(onClose).toHaveBeenCalled()

    const running = replayStage(journal.filter(e => e.seq <= 30)).nodes.w1
    rerender(<StageDrawer node={running} turnId="t" onClose={onClose} onStop={onStop} />)
    fireEvent.click(screen.getByTestId('stage-stop'))
    expect(onStop).toHaveBeenCalledWith('w1')
  })
})
