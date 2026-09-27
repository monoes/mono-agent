// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({ api: { openURL: vi.fn() } }))

import { ChatTimeline } from './ChatTimeline.jsx'
import { chatReducer, initialChatState } from './chatReducer.js'
import { diffLines } from './unifiedDiff.js'

// Coder turns (#202/#203): native Claude Code tool calls arrive as
// tool.started {native: true, parentCallId?} / tool.completed {truncated?,
// durationMs?, denied?, cancelled?} and render as per-tool cards.

afterEach(() => { cleanup() })

let seq = 0
function ev(type, payload) {
  seq++
  return { version: 1, profileId: 'default', conversationId: 'c1', turnId: 't1', seq, at: new Date(Date.UTC(2026, 8, 27, 10, 0, seq)).toISOString(), type, payload }
}
const started = (callId, name, args, extra = {}) => ev('tool.started', { callId, name, arguments: args, native: true, ...extra })
const completed = (callId, extra = {}) => ev('tool.completed', { callId, ok: true, result: '', ...extra })
const notice = (code, message, severity = 'info') => ev('notice', { code, message, severity })

// seq only ever grows, so events built later always apply after earlier ones.
function reduce(events) {
  return events.reduce((s, e) => chatReducer(s, { type: 'event', event: e }), initialChatState())
}
function renderEvents(events, props = {}) {
  const state = reduce(events())
  return render(<ChatTimeline state={state} turnId="t1" isLive={true} {...props} />)
}
const card = (tool) => screen.getAllByTestId('native-tool-card').find(el => el.dataset.tool === tool)

describe('native tool cards', () => {
  it('Bash: shows the command and description, spins while running, then shows the output and duration', () => {
    const bashStart = () => [started('b1', 'Bash', { command: 'go test ./...', description: 'Run the tests' })]
    const { rerender } = renderEvents(bashStart)
    let el = card('Bash')
    expect(el).toHaveAttribute('data-status', 'running')
    expect(within(el).getByText('$ go test ./...')).toBeInTheDocument()
    expect(within(el).getByText('Run the tests')).toBeInTheDocument()
    expect(within(el).getByText('Running')).toBeInTheDocument()
    expect(el.querySelector('.chat-spin')).not.toBeNull()

    const done = reduce([...bashStart(), completed('b1', { result: 'ok  \tpkg\t0.2s', durationMs: 2300 })])
    rerender(<ChatTimeline state={done} turnId="t1" isLive={true} />)
    el = card('Bash')
    expect(el).toHaveAttribute('data-status', 'done')
    expect(within(el).getByText('· 2.3s')).toBeInTheDocument()
    // Output is collapsed until the header is clicked.
    const header = within(el).getAllByRole('button')[0]
    expect(header).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(header)
    expect(header).toHaveAttribute('aria-expanded', 'true')
    expect(within(el).getByText(/ok\s+pkg/)).toBeVisible()
  })

  it('Edit: shows the file path and a unified diff of old → new', () => {
    renderEvents(() => [
      started('e1', 'Edit', { file_path: '/w/main.go', old_string: 'a\nb\nc\n', new_string: 'a\nB\nc\n' }),
      completed('e1'),
    ])
    const el = card('Edit')
    expect(within(el).getByText('/w/main.go')).toBeInTheDocument()
    expect(within(el).getByText('+1 −1')).toBeInTheDocument()
    const diff = within(el).getByTestId('edit-diff')
    expect(diff).toBeVisible()
    const rows = [...diff.children].map(r => [r.dataset.diff, r.textContent])
    expect(rows).toEqual([['context', ' a'], ['del', '-b'], ['add', '+B'], ['context', ' c']])
  })

  it('MultiEdit: one diff per edit', () => {
    renderEvents(() => [
      started('m1', 'MultiEdit', { file_path: '/w/x.js', edits: [{ old_string: 'x', new_string: 'y' }, { old_string: 'p', new_string: 'q' }] }),
    ])
    expect(within(card('MultiEdit')).getAllByTestId('edit-diff')).toHaveLength(2)
  })

  it('Write: path and line count, content collapsed', () => {
    renderEvents(() => [started('w1', 'Write', { file_path: '/w/hello.py', content: 'print(1)\nprint(2)' }), completed('w1', { durationMs: 90 })])
    const el = card('Write')
    expect(within(el).getByText('· 90ms')).toBeInTheDocument()
    expect(within(el).getByText('/w/hello.py')).toBeInTheDocument()
    expect(within(el).getByText('2 lines')).toBeInTheDocument()
    expect(within(el).getByText(/print\(1\)/)).not.toBeVisible()
  })

  it('Read/Glob/Grep render as compact one-liners', () => {
    renderEvents(() => [
      started('r1', 'Read', { file_path: '/w/a.go', offset: 10, limit: 5 }),
      started('g1', 'Glob', { pattern: '**/*.go' }),
      started('g2', 'Grep', { pattern: 'TODO', path: '/w' }),
    ])
    expect(within(card('Read')).getByText('Read /w/a.go (lines 10–14)')).toBeInTheDocument()
    expect(within(card('Glob')).getByText('Glob **/*.go')).toBeInTheDocument()
    expect(within(card('Grep')).getByText('Grep TODO in /w')).toBeInTheDocument()
  })

  it('WebFetch/WebSearch are flagged as external content', () => {
    renderEvents(() => [
      started('f1', 'WebFetch', { url: 'https://example.com', prompt: 'summarize' }),
      started('s1', 'WebSearch', { query: 'go generics' }),
    ])
    expect(within(card('WebFetch')).getByText('https://example.com')).toBeInTheDocument()
    expect(within(card('WebFetch')).getByTestId('external-content')).toHaveTextContent('external content')
    expect(within(card('WebSearch')).getByText('go generics')).toBeInTheDocument()
    expect(within(card('WebSearch')).getByTestId('external-content')).toBeInTheDocument()
  })

  it('Task nests the calls made inside it (parentCallId), not at the top level', () => {
    renderEvents(() => [
      started('task1', 'Task', { description: 'Explore the repo', prompt: 'find main', subagent_type: 'Explore' }),
      started('in1', 'Grep', { pattern: 'func main' }, { parentCallId: 'task1' }),
      started('in2', 'Read', { file_path: '/w/main.go' }, { parentCallId: 'task1' }),
      completed('in1'),
      started('top', 'Bash', { command: 'ls' }),
    ])
    const task = card('Task')
    expect(within(task).getByText('Explore the repo')).toBeInTheDocument()
    expect(within(task).getByText('2 calls')).toBeInTheDocument()
    const nested = within(task).getByTestId('subagent-calls')
    expect(within(nested).getAllByTestId('native-tool-card').map(c => c.dataset.tool)).toEqual(['Grep', 'Read'])
    // Top level: the Task and the later Bash only.
    const timeline = screen.getByTestId('chat-timeline')
    const topLevel = [...timeline.children].filter(c => c.dataset.testid === 'native-tool-card')
    expect(topLevel.map(c => c.dataset.tool)).toEqual(['Task', 'Bash'])
  })

  it('shows truncated, denied and cancelled clearly', () => {
    renderEvents(() => [
      started('b1', 'Bash', { command: 'cat big.log' }), completed('b1', { result: 'lots…', truncated: true }),
      started('b2', 'Bash', { command: 'rm -r /' }), completed('b2', { ok: false, denied: true, result: 'denied' }),
      started('b3', 'Bash', { command: 'sleep 60' }), completed('b3', { ok: false, cancelled: true }),
    ])
    const [b1, b2, b3] = screen.getAllByTestId('native-tool-card')
    expect(within(b1).getByTestId('truncated')).toHaveTextContent('output truncated')
    expect(b2).toHaveAttribute('data-status', 'denied')
    expect(within(b2).getByText('Denied')).toBeInTheDocument()
    expect(b3).toHaveAttribute('data-status', 'cancelled')
    expect(within(b3).getByText('Cancelled')).toBeInTheDocument()
  })

  it('a started call of a finished turn reads Interrupted, not Running', () => {
    renderEvents(() => [started('b1', 'Bash', { command: 'sleep 60' })], { isLive: false })
    expect(card('Bash')).toHaveAttribute('data-status', 'interrupted')
  })

  it('arguments that arrived as a truncated string still render', () => {
    renderEvents(() => [started('w1', 'Write', '{"file_path":"/w/big.txt","content":"aaaa…')])
    expect(card('Write')).toBeInTheDocument()
    expect(screen.getByText('Input (truncated)')).toBeInTheDocument()
  })

  it('non-native tool calls keep the generic card', () => {
    const state = reduce([ev('tool.started', { callId: 'x1', name: 'workflow_list', arguments: { q: 1 } })])
    render(<ChatTimeline state={state} turnId="t1" />)
    expect(screen.queryByTestId('native-tool-card')).not.toBeInTheDocument()
    expect(screen.getByText('workflow_list')).toBeInTheDocument()
  })
})

describe('coder notices', () => {
  it('shows the latest coder.status as a transient startup line, gone once output arrives', () => {
    const early = [notice('coder.status', 'Starting Claude Code…'), notice('coder.status', 'Loading MCP servers (3/5)…')]
    const { rerender } = renderEvents(() => early)
    expect(screen.getByTestId('coder-status-line')).toHaveTextContent('Loading MCP servers (3/5)…')
    expect(screen.queryByText('Starting Claude Code…')).not.toBeInTheDocument()

    const later = reduce([...early, ev('assistant.delta', { partId: 'p1', text: 'Hi' })])
    rerender(<ChatTimeline state={later} turnId="t1" isLive={true} />)
    expect(screen.queryByTestId('coder-status-line')).not.toBeInTheDocument()
  })

  it('shows coder.background as a warning banner and coder.workspace as a folder line', () => {
    renderEvents(() => [
      notice('coder.workspace', 'Working in /w/20260927-brisk-otter'),
      ev('assistant.delta', { partId: 'p1', text: 'Started the server.' }),
      notice('coder.background', '2 background processes still running: 1234, 5678', 'warning'),
    ])
    expect(screen.getByTestId('coder-workspace-line')).toHaveTextContent('Working in /w/20260927-brisk-otter')
    expect(screen.getByTestId('coder-background-banner')).toHaveTextContent('2 background processes still running: 1234, 5678')
  })
})

describe('diffLines', () => {
  it('diffs by line', () => {
    expect(diffLines('a\nb', 'a\nc\nb')).toEqual([{ type: ' ', text: 'a' }, { type: '+', text: 'c' }, { type: ' ', text: 'b' }])
    expect(diffLines('', 'new')).toEqual([{ type: '+', text: 'new' }])
    expect(diffLines('old', '')).toEqual([{ type: '-', text: 'old' }])
  })
})
