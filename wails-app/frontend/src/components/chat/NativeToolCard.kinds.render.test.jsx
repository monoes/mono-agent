// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react'

vi.mock('../../services/api.js', () => ({ api: { openURL: vi.fn(), coderStopBackground: vi.fn() } }))

import { ChatTimeline } from './ChatTimeline.jsx'
import { chatReducer, initialChatState } from './chatReducer.js'
import { kindOf } from './nativeToolDescribe.jsx'
import { patchLines } from './nativeToolParts.jsx'

// Coder turns on any runtime: tool.started carries a normalized kind
// (shell, edit, write, read, search, web, mcp, task, todo, patch, other)
// with canonical input keys, and the card renders by kind; Claude's tool
// names are the fallback for events without one.

afterEach(() => { cleanup() })

let seq = 0
function ev(type, payload) {
  seq++
  return { version: 1, profileId: 'default', conversationId: 'c1', turnId: 't1', seq, at: new Date(Date.UTC(2026, 8, 29, 10, 0, seq)).toISOString(), type, payload }
}
const started = (callId, name, kind, args, extra = {}) => ev('tool.started', { callId, name, kind, arguments: args, native: true, ...extra })
const completed = (callId, extra = {}) => ev('tool.completed', { callId, ok: true, result: '', ...extra })

function renderEvents(events) {
  const state = events.reduce((s, e) => chatReducer(s, { type: 'event', event: e }), initialChatState())
  render(<ChatTimeline state={state} turnId="t1" isLive={true} />)
  return state
}
const card = (tool) => screen.getAllByTestId('native-tool-card').find(el => el.dataset.tool === tool)
const open = (el) => fireEvent.click(within(el).getAllByRole('button')[0])

describe('native tool cards by kind', () => {
  it('shell: a codex command_execution shows the command and its exit code', () => {
    const state = renderEvents([
      started('s1', 'command_execution', 'shell', { command: 'npm test', cwd: '/w/app' }),
      completed('s1', { ok: false, result: '2 failing', exit_code: 1 }),
    ])
    expect(state.calls.s1.kind).toBe('shell')
    expect(state.calls.s1.exitCode).toBe(1)
    const el = card('command_execution')
    expect(el).toHaveAttribute('data-kind', 'shell')
    expect(within(el).getByText('$ npm test')).toBeInTheDocument()
    expect(within(el).getByText('in /w/app')).toBeInTheDocument()
    expect(within(el).getByTestId('exit-code')).toHaveTextContent('exit 1')
    expect(within(el).getByText('2 failing')).toBeVisible() // a failed call opens itself
  })

  it('edit: an opencode edit shows a unified diff of old → new', () => {
    renderEvents([started('e1', 'edit', 'edit', { file_path: '/w/a.go', old_string: 'x\ny\n', new_string: 'x\nz\n' }), completed('e1')])
    const el = card('edit')
    expect(within(el).getByText('/w/a.go')).toBeInTheDocument()
    expect(within(el).getByText('+1 −1')).toBeInTheDocument()
    const rows = [...within(el).getByTestId('edit-diff').children].map(r => [r.dataset.diff, r.textContent])
    expect(rows).toEqual([['context', ' x'], ['del', '-y'], ['add', '+z']])
  })

  it('write: shows the line count and the content', () => {
    renderEvents([started('w1', 'write_file', 'write', { file_path: '/w/new.txt', content: 'a\nb\n' }, { fileExisted: false })])
    const el = card('write_file')
    expect(within(el).getByText('/w/new.txt')).toBeInTheDocument()
    expect(within(el).getByText('2 lines')).toBeInTheDocument()
    expect(within(el).getByTestId('file-state')).toHaveTextContent('new file')
  })

  it('read and search: one compact line each', () => {
    renderEvents([
      started('r1', 'read_file', 'read', { file_path: '/w/README.md' }),
      started('g1', 'grep', 'search', { pattern: 'TODO', path: 'src' }),
    ])
    expect(within(card('read_file')).getByText('Read /w/README.md')).toBeInTheDocument()
    expect(within(card('grep')).getByText('Search TODO in src')).toBeInTheDocument()
  })

  it('patch: one diff per file with its action', () => {
    renderEvents([
      started('p1', 'apply_patch', 'patch', { files: [
        { file_path: 'src/a.js', action: 'update', diff: '--- a/src/a.js\n+++ b/src/a.js\n@@ -1,2 +1,2 @@\n keep\n-old\n+new\n' },
        { file_path: 'src/b.js', action: 'add', diff: '@@ -0,0 +1 @@\n+hello\n' },
        { file_path: 'src/c.js', action: 'delete' },
      ] }),
      completed('p1'),
    ])
    const el = card('apply_patch')
    expect(el).toHaveAttribute('data-kind', 'patch')
    expect(within(el).getByText('3 files')).toBeInTheDocument()
    expect(within(el).getByText('+2 −1')).toBeInTheDocument()
    const files = within(el).getAllByTestId('patch-file')
    expect(files.map(f => within(f).getByTestId('patch-action').textContent)).toEqual(['update', 'add', 'delete'])
    const rows = [...within(files[0]).getByTestId('patch-diff').children].map(r => [r.dataset.diff, r.textContent])
    expect(rows).toEqual([['hunk', '@@ -1,2 +1,2 @@'], ['context', ' keep'], ['del', '-old'], ['add', '+new']])
    expect(within(files[2]).queryByTestId('patch-diff')).not.toBeInTheDocument()
  })

  it('mcp: canonical {server, tool, arguments}, and Claude\'s mcp__server__tool name', () => {
    renderEvents([
      started('m1', 'mcp_tool_call', 'mcp', { server: 'github', tool: 'create_issue', arguments: { title: 'Bug' } }),
      started('m2', 'mcp__monomind__memory_search', 'mcp', { query: 'auth' }),
    ])
    const a = card('mcp_tool_call')
    expect(within(a).getByText('github · create_issue')).toBeInTheDocument()
    open(a)
    expect(within(a).getByText(/"title": "Bug"/)).toBeVisible()
    const b = card('mcp__monomind__memory_search')
    expect(within(b).getByText('monomind · memory_search')).toBeInTheDocument()
    open(b)
    expect(within(b).getByText(/"query": "auth"/)).toBeVisible()
  })

  it('web: a search query, marked as external content', () => {
    renderEvents([started('x1', 'web_search', 'web', { query: 'vite 7 release notes' })])
    const el = card('web_search')
    expect(within(el).getByText('vite 7 release notes')).toBeInTheDocument()
    expect(within(el).getByTestId('external-content')).toBeInTheDocument()
  })

  it('todo: items as {text, completed} from other runtimes', () => {
    renderEvents([started('t1', 'todo_list', 'todo', { items: [{ text: 'plan', completed: true }, { text: 'build', completed: false }] })])
    expect(within(card('todo_list')).getByText('Todos (1/2 done)')).toBeInTheDocument()
  })

  it('task: a subagent with its prompt', () => {
    renderEvents([started('k1', 'spawn_agent', 'task', { description: 'Review the diff', prompt: 'look for bugs' })])
    expect(within(card('spawn_agent')).getByText('Review the diff')).toBeInTheDocument()
  })

  it('other: the tool name and its raw arguments', () => {
    renderEvents([started('o1', 'custom_thing', 'other', { a: 1 })])
    const el = card('custom_thing')
    expect(el).toHaveAttribute('data-kind', 'other')
    open(el)
    expect(within(el).getByText(/"a": 1/)).toBeVisible()
  })

  it('a start-only call closed with ok unknown reads as completed, not failed', () => {
    renderEvents([started('s2', 'bash', 'shell', { command: 'ls' }), completed('s2', { ok: null, result: null })])
    const el = card('bash')
    expect(el).toHaveAttribute('data-status', 'completed')
    open(el)
    expect(within(el).getByText('(no output)')).toBeVisible()
  })
})

describe('kindOf', () => {
  it('prefers the reported kind, else maps Claude\'s tool names', () => {
    expect(kindOf({ name: 'Bash', kind: 'shell' })).toBe('shell')
    expect(kindOf({ name: 'Bash' })).toBe('shell')
    expect(kindOf({ name: 'MultiEdit' })).toBe('edit')
    expect(kindOf({ name: 'Grep' })).toBe('search')
    expect(kindOf({ name: 'WebFetch' })).toBe('web')
    expect(kindOf({ name: 'Agent' })).toBe('task')
    expect(kindOf({ name: 'mcp__s__t' })).toBe('mcp')
    expect(kindOf({ name: 'Frobnicate' })).toBe('other')
  })
})

describe('patchLines', () => {
  it('drops the file headers and keeps hunks, adds, removes and context', () => {
    expect(patchLines('diff --git a/x b/x\nindex 1..2\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n')).toEqual([
      { type: '@', text: '@@ -1 +1 @@' }, { type: '-', text: 'a' }, { type: '+', text: 'b' },
    ])
  })
})
