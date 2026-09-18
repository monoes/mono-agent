// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { collectTriggerFields, triggerInputSkeleton, parseTriggerInput } from './triggerInput.js'

// The workflow that started this: its Generate Images node reads
// {{ json $json.prompts }}, the run button sent nothing, and the run finished
// green with no images.
const geminiNodes = [
  { id: 'trigger', type: 'trigger.manual', config: {} },
  { id: 'gen', type: 'gemini.chat_session_many', config: { prompts: '{{ json $json.prompts }}', mode: 'image' } },
]

describe('collectTriggerFields', () => {
  it('finds the fields a node reads from the trigger', () => {
    expect(collectTriggerFields(geminiNodes)).toEqual([{ name: 'prompts', structured: true }])
  })

  it('marks a bare reference as a plain value and a json-wrapped one as a collection', () => {
    const fields = collectTriggerFields([
      { config: { subject: '{{ $json.topic }}', items: '{{ json $json.rows }}' } },
    ])
    expect(fields).toEqual([
      { name: 'topic', structured: false },
      { name: 'rows', structured: true },
    ])
  })

  it('reads bracket references and nested config values', () => {
    const fields = collectTriggerFields([
      { config: { headers: { auth: '{{ $json["apiKey"] }}' }, list: ['{{ $json.first }}'] } },
    ])
    expect(fields.map(f => f.name).sort()).toEqual(['apiKey', 'first'])
  })

  it('does not confuse a node reference with a trigger reference', () => {
    const fields = collectTriggerFields([
      { config: { a: '{{ $node["Fetch"].json.total }}', b: '{{ $json.count }}' } },
    ])
    expect(fields.map(f => f.name)).toContain('count')
  })

  it('returns nothing for a workflow that needs no input', () => {
    expect(collectTriggerFields([{ config: { url: 'https://example.com' } }])).toEqual([])
    expect(collectTriggerFields(undefined)).toEqual([])
  })
})

describe('triggerInputSkeleton', () => {
  it('offers an array for a json-wrapped field and a string otherwise', () => {
    expect(triggerInputSkeleton(collectTriggerFields(geminiNodes))).toBe('{\n  "prompts": []\n}')
    expect(triggerInputSkeleton([{ name: 'topic', structured: false }])).toBe('{\n  "topic": ""\n}')
    expect(triggerInputSkeleton([])).toBe('{}')
  })
})

describe('parseTriggerInput', () => {
  it('accepts an object and sends it compacted', () => {
    expect(parseTriggerInput('{\n "prompts": ["a wizard"]\n}')).toEqual({ ok: true, value: '{"prompts":["a wizard"]}' })
  })

  it('treats empty and {} as no trigger data', () => {
    expect(parseTriggerInput('')).toEqual({ ok: true, value: '' })
    expect(parseTriggerInput('  {}  ')).toEqual({ ok: true, value: '' })
  })

  it('rejects malformed JSON and non-objects with a usable message', () => {
    expect(parseTriggerInput('{"prompts": [}').ok).toBe(false)
    const arr = parseTriggerInput('["a"]')
    expect(arr.ok).toBe(false)
    expect(arr.error).toMatch(/must be a JSON object/)
    expect(parseTriggerInput('"hello"').ok).toBe(false)
    expect(parseTriggerInput('null').ok).toBe(false)
  })
})

describe('remembering trigger input', () => {
  it('round-trips the last input and pretty-prints it back', async () => {
    const { rememberTriggerInput, rememberedTriggerInput } = await import('./triggerInput.js')
    rememberTriggerInput('wf-1', '{"prompts":["a wizard"]}')
    expect(rememberedTriggerInput('wf-1', [{ name: 'prompts', structured: true }]))
      .toBe('{\n  "prompts": [\n    "a wizard"\n  ]\n}')
  })

  it('falls back to a skeleton with nothing remembered, and survives broken storage', async () => {
    const { rememberTriggerInput, rememberedTriggerInput } = await import('./triggerInput.js')
    const fields = [{ name: 'prompts', structured: true }]
    expect(rememberedTriggerInput('wf-unknown', fields)).toBe('{\n  "prompts": []\n}')

    rememberTriggerInput('wf-2', '{"a":1}')
    localStorage.setItem('monoagent:wf-trigger-input:wf-2', 'not json')
    expect(rememberedTriggerInput('wf-2', fields)).toBe('{\n  "prompts": []\n}')

    const getItem = Storage.prototype.getItem
    Storage.prototype.getItem = () => { throw new Error('blocked') }
    try {
      expect(rememberedTriggerInput('wf-1', fields)).toBe('{\n  "prompts": []\n}')
    } finally {
      Storage.prototype.getItem = getItem
    }
  })
})
