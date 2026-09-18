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
    expect(collectTriggerFields(geminiNodes)).toEqual([{ name: 'prompts', structured: true, examples: [] }])
  })

  it('marks a bare reference as a plain value and a json-wrapped one as a collection', () => {
    const fields = collectTriggerFields([
      { config: { subject: '{{ $json.topic }}', items: '{{ json $json.rows }}' } },
    ])
    expect(fields).toEqual([
      { name: 'topic', structured: false, examples: [] },
      { name: 'rows', structured: true, examples: [] },
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
    expect(triggerInputSkeleton([{ name: 'topic', structured: false, examples: [] }])).toBe('{\n  "topic": ""\n}')
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

// The point of the whole exercise: the box the user is asked to fill should
// show them what to write, not an empty array. The examples come from the
// node schema of the very field the trigger data feeds.
describe('examples carried from the node schema', () => {
  const nodeWithExamples = {
    type: 'gemini.chat_session_many',
    config: { prompts: '{{ json $json.prompts }}', mode: 'image' },
    schema: {
      fields: [
        {
          key: 'prompts',
          type: 'array',
          examples: [
            'generate an image that: a wizard in a blue robe casting a fire spell',
            'generate an image that: the same wizard riding a red dragon over a burning castle',
          ],
        },
        { key: 'mode', type: 'select' },
      ],
    },
  }

  it('attaches the schema examples of the config key the reference sits in', () => {
    const [field] = collectTriggerFields([nodeWithExamples])
    expect(field.name).toBe('prompts')
    expect(field.examples).toHaveLength(2)
    expect(field.examples[0]).toMatch(/^generate an image that: /)
  })

  it('prefills the modal with two editable sample prompts', () => {
    const skeleton = triggerInputSkeleton(collectTriggerFields([nodeWithExamples]))
    expect(JSON.parse(skeleton)).toEqual({
      prompts: [
        'generate an image that: a wizard in a blue robe casting a fire spell',
        'generate an image that: the same wizard riding a red dragon over a burning castle',
      ],
    })
  })

  it('uses a single example as the value for a non-collection field', () => {
    const fields = collectTriggerFields([{
      config: { prompt: '{{ $json.subject }}' },
      schema: { fields: [{ key: 'prompt', examples: ['a red bicycle in Paris'] }] },
    }])
    expect(JSON.parse(triggerInputSkeleton(fields))).toEqual({ subject: 'a red bicycle in Paris' })
  })

  it('still produces an empty skeleton when the schema offers nothing', () => {
    const fields = collectTriggerFields([{ config: { prompts: '{{ json $json.prompts }}' } }])
    expect(JSON.parse(triggerInputSkeleton(fields))).toEqual({ prompts: [] })
  })
})

// A workflow file stores the schema each node had when it was saved. The one
// that matters is the running build's — otherwise the workflow saved before a
// field grew examples, whose user most needs them, is the one that never sees
// them.
describe('live schemas override the copy saved in the workflow', () => {
  const savedWorkflowNode = {
    subtype: 'gemini.chat_session_many',
    config: { prompts: '{{ json $json.prompts }}' },
    schema: { fields: [{ key: 'prompts', type: 'array' }] }, // saved before examples existed
  }
  const liveSchemas = {
    'gemini.chat_session_many': {
      fields: [{ key: 'prompts', type: 'array', examples: ['generate an image that: a wizard', 'generate an image that: a dragon'] }],
    },
  }

  it('prefills from the catalog even when the stored snapshot has none', () => {
    const fields = collectTriggerFields([savedWorkflowNode], liveSchemas)
    expect(fields[0].examples).toHaveLength(2)
    expect(JSON.parse(triggerInputSkeleton(fields)).prompts).toEqual([
      'generate an image that: a wizard',
      'generate an image that: a dragon',
    ])
  })

  it('falls back to the stored snapshot for a type the catalog does not know', () => {
    const fields = collectTriggerFields([{
      subtype: 'some.retired_node',
      config: { prompt: '{{ $json.text }}' },
      schema: { fields: [{ key: 'prompt', examples: ['from the snapshot'] }] },
    }], liveSchemas)
    expect(fields[0].examples).toEqual(['from the snapshot'])
  })

  it('works with no catalog at all', () => {
    expect(collectTriggerFields([savedWorkflowNode], undefined)[0].examples).toEqual([])
  })
})
