// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import en from '../../../locales/en.json'
import es from '../../../locales/es.json'
import ApiModelsBlock from './ApiModelsBlock.jsx'
import { mainListener, dedicatedListener, statusOf, modelsDoc, oldModelsDoc, MISSING_SURFACE, MISSING_KEY } from './__fixtures__/apiFixtures.js'

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(cleanup)

function mount(models, props = {}) {
  const onOpenJev = vi.fn(); const onRetry = vi.fn()
  render(<ApiModelsBlock models={models} err="" status={statusOf([mainListener()])} statusErr="" onOpenJev={onOpenJev} onRetry={onRetry} {...props} />)
  return { onOpenJev, onRetry }
}
const row = (id) => screen.getByRole('row', { name: new RegExp(id.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')) })
const cells = (r) => within(r).getAllByRole('cell').map(c => c.textContent)
const autoRow = () => screen.getByRole('row', { name: /^auto/ })

describe('ApiModelsBlock: the table', () => {
  it('lists every model with its class, validation, and what the policy lets it do', () => {
    mount(modelsDoc())
    const table = screen.getByRole('table', { name: 'Models' })
    for (const h of ['Model', 'Confinement', 'Capabilities', 'Validated', 'Served', 'Context key', 'Auto']) {
      expect(within(table).getByRole('columnheader', { name: h })).toBeInTheDocument()
    }
    // id and label, class, capabilities, validated, served, context key, auto
    expect(cells(row('claude/sonnet'))).toEqual(['claude/sonnetSonnet 5', 'chat-only', 'texttools', 'validated', 'yes', 'yes', 'yes'])
    expect(cells(row('codex/gpt-6-astra'))).toEqual(['codex/gpt-6-astraGPT-6-Astra', 'sandboxed', 'textimagetools', '–', 'yes', 'no', 'no'])
    expect(cells(row('antigravity/default'))[1]).toBe('unconfined')
    expect(screen.getAllByRole('row')).toHaveLength(1 + 8 + 1) // header, the models, the auto entry
  })

  it('puts the auto entry first, where a long list does not hide it', () => {
    mount(modelsDoc())
    const rows = screen.getAllByRole('row')
    expect(rows[1]).toBe(autoRow()) // right under the header row
    expect(rows[2]).toBe(row('claude/default'))
  })

  it('says a model the listener does not serve is not served by policy', () => {
    mount(modelsDoc({ confinement: 'chat-only', forListener: 'network' }), { status: statusOf([dedicatedListener()]) })
    expect(cells(row('claude/default')).slice(4)).toEqual(['yes', 'yes', 'yes'])
    expect(cells(row('codex/default')).slice(4)).toEqual(['no (policy)', 'no', 'no'])
    expect(cells(row('pi/openrouter/nvidia/nemotron-3-super-120b-a12b:free'))[4]).toBe('no (policy)')
  })

  it('names the full label of a long model on hover', () => {
    mount(modelsDoc())
    expect(screen.getByTitle('Default (Use the default model (currently Opus 5 (1M context)))')).toBeInTheDocument()
  })

  it('says whose policy it shows', () => {
    mount(modelsDoc())
    expect(screen.getByText("Policy of the listener at 127.0.0.1:9322, assumed from this app's environment.")).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { status: statusOf([mainListener({ confinement_source: 'daemon' })]) })
    expect(screen.getByText('Policy of the listener at 127.0.0.1:9322, as the running daemon reports it.')).toBeInTheDocument()
  })

  it('says no listener serves /v1 only when the status says so', () => {
    mount(modelsDoc(), { status: statusOf([]) })
    expect(screen.getByText(/No listener serves \/v1 right now: this is what this app's own settings would serve on a loopback listener\./)).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { status: statusOf([mainListener({ v1: false })]) })
    expect(screen.getByText(/No listener serves \/v1 right now/)).toBeInTheDocument()
  })

  it('does not say that when the status could not be read: it says it does not know which listeners serve', () => {
    mount(modelsDoc(), { status: null, statusErr: 'boom' })
    expect(screen.queryByText(/No listener serves \/v1/)).not.toBeInTheDocument()
    expect(screen.getByText(/Couldn't read which listeners serve \/v1, so this is what this app's own settings would serve on a loopback listener, not necessarily what the running server serves\./)).toBeInTheDocument()
    cleanup()
    // An older status that is still on screen does not outvote the failure to read a new one.
    mount(modelsDoc(), { status: statusOf([mainListener()]), statusErr: 'boom' })
    expect(screen.queryByText(/Policy of the listener at/)).not.toBeInTheDocument()
    expect(screen.getByText(/Couldn't read which listeners serve \/v1/)).toBeInTheDocument()
  })

  it('says nothing about a policy while the status is still loading', () => {
    mount(modelsDoc(), { status: null, statusErr: '' })
    expect(screen.queryByText(/No listener serves \/v1/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Policy of the listener/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Couldn't read which listeners/)).not.toBeInTheDocument()
  })

  it('says which listener the table is for when several serve /v1, each with its own policy', () => {
    mount(modelsDoc(), { status: statusOf([mainListener(), dedicatedListener()]) })
    expect(screen.getByText(/Policy of the listener at 127\.0\.0\.1:9322, assumed from this app's environment\./)).toBeInTheDocument()
    expect(screen.getByText(/2 listeners serve \/v1, each with its own policy: this table is the one for 127\.0\.0\.1:9322\./)).toBeInTheDocument()
    cleanup()
    // The first one that answers: here the dedicated one, as the main one is down.
    mount(modelsDoc({ confinement: 'chat-only', forListener: 'network' }), {
      status: statusOf([mainListener({ reachable: false, v1_answers: false }), dedicatedListener()]),
    })
    expect(screen.getByText(/this table is the one for 0\.0\.0\.0:9443\./)).toBeInTheDocument()
    cleanup()
    mount(modelsDoc())
    expect(screen.queryByText(/each with its own policy/)).not.toBeInTheDocument()
    cleanup()
    // Listed is not serving: a main listener that does not serve /v1 is not one of them.
    mount(modelsDoc(), { status: statusOf([mainListener({ v1: false }), dedicatedListener()]) })
    expect(screen.queryByText(/each with its own policy/)).not.toBeInTheDocument()
  })

  it('has an empty state, a loading state, and a load error with a retry', () => {
    mount({ ...modelsDoc(), models: [] })
    expect(screen.getByText(/No models found\. Install an agent runtime on the AI agents page\./)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    cleanup()
    mount(null)
    expect(screen.getByText('Loading models…')).toBeInTheDocument()
    cleanup()
    const { onRetry } = mount(null, { err: 'monomind not found' })
    expect(screen.getByText("Couldn't list the models: monomind not found")).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
})

describe('ApiModelsBlock: the auto entry', () => {
  it('says what Jev picks among, and how many served models it may not pick', () => {
    mount(modelsDoc())
    const r = autoRow()
    expect(within(r).getByText('Jev picks the model for each request')).toBeInTheDocument()
    expect(within(r).getByText('On. Jev picks among the 3 models it may use here (up to chat-only).')).toBeInTheDocument()
    expect(within(r).getByText('5 more models are served here but are above chat-only, so auto may not pick them. Raise it with --auto-confinement (MONOAGENT_API_AUTO_CONFINEMENT) on the server.')).toBeInTheDocument()
    expect(within(r).queryByRole('button')).not.toBeInTheDocument()
  })

  it('with one model to pick from says the rule uses it and Jev is not asked, and with none held back says nothing of it', () => {
    mount({ ...modelsDoc(), auto: { available: true, key_source: 'vault', confinement: 'chat-only', candidates: 1, held_back: 1 } })
    expect(screen.getByText('On. Only 1 model is allowed here (up to chat-only), so auto uses it without asking Jev.')).toBeInTheDocument()
    expect(screen.queryByText(/Jev picks among/)).not.toBeInTheDocument()
    expect(screen.getByText(/^1 more model is served here but is above chat-only, so auto may not pick it\./)).toBeInTheDocument()
    cleanup()
    mount({ ...modelsDoc(), auto: { available: true, key_source: 'vault', confinement: 'chat-only', candidates: 3 } })
    expect(screen.getByText('On. Jev picks among the 3 models it may use here (up to chat-only).')).toBeInTheDocument()
    expect(screen.queryByText(/more model/)).not.toBeInTheDocument()
  })

  it('spells the class of auto the way the flag and the status do: any, where the CLI says unconfined', () => {
    // The CLI's auto.confinement is a class (unconfined), the listeners' auto_confinement a policy (any).
    const doc = modelsDoc({ confinement: 'any', context: 'any', auto: 'any' })
    expect(doc.auto.confinement).toBe('unconfined')
    mount(doc, { status: statusOf([mainListener({ auto_confinement: 'any' })]) })
    expect(within(autoRow()).getByText('On. Jev picks among the 8 models it may use here (up to any).')).toBeInTheDocument()
    expect(within(autoRow()).queryByText(/unconfined/)).not.toBeInTheDocument()
  })

  it('says when the Jev key is this app\'s environment, which a running server does not share', () => {
    mount({ ...modelsDoc(), auto: { ...modelsDoc().auto, key_source: 'env' } })
    expect(screen.getByText(/The Jev key is this app's TYPESAFE_API_KEY: a running server reads its own environment\./)).toBeInTheDocument()
  })

  it('says what is missing and jumps to the Jev settings when that is where it is fixed', () => {
    for (const missing of [MISSING_SURFACE, MISSING_KEY]) {
      const { onOpenJev } = mount({ ...modelsDoc(), auto: { available: false, missing } })
      expect(within(autoRow()).getByText(`Off. It needs ${missing}.`)).toBeInTheDocument()
      fireEvent.click(within(autoRow()).getByRole('button', { name: 'Open the Jev settings' }))
      expect(onOpenJev).toHaveBeenCalledTimes(1)
      cleanup()
    }
  })

  it('does not point at Jev when the policy is what holds auto back', () => {
    const missing = 'a model within --auto-confinement (chat-only), which holds back all 3 the listener serves'
    mount({ ...modelsDoc(), auto: { available: false, missing } })
    expect(within(autoRow()).getByText(`Off. It needs ${missing}.`)).toBeInTheDocument()
    expect(within(autoRow()).queryByRole('button')).not.toBeInTheDocument()
  })

  it('has no jump without somewhere to jump', () => {
    mount({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } }, { onOpenJev: undefined })
    expect(screen.queryByRole('button', { name: 'Open the Jev settings' })).not.toBeInTheDocument()
  })
})

describe('ApiModelsBlock: what each model can do', () => {
  // The badges of a model's row as the page shows them, and the cell they are in.
  const caps = (id) => within(row(id)).queryAllByRole('listitem').map(li => li.textContent)
  const capsCell = (id) => within(row(id)).getAllByRole('cell')[2]

  it('has a column for it, between the class and the validation', () => {
    mount(modelsDoc())
    const headers = within(screen.getByRole('table', { name: 'Models' })).getAllByRole('columnheader').map(h => h.textContent)
    expect(headers).toEqual(['Model', 'Confinement', 'Capabilities', 'Validated', 'Served', 'Context key', 'Auto'])
  })

  it('shows one badge for each capability the CLI lists, in its order: text; text and image; text and tools; all three', () => {
    mount(modelsDoc())
    expect(caps('copilot/default')).toEqual(['text'])
    expect(caps('antigravity/default')).toEqual(['text', 'image'])
    expect(caps('claude/sonnet')).toEqual(['text', 'tools'])
    expect(caps('codex/gpt-6-astra')).toEqual(['text', 'image', 'tools'])
  })

  it('says what each one is in words, not in colour: the label is the text and the title says what it means', () => {
    mount(modelsDoc())
    const r = within(row('codex/gpt-6-astra'))
    expect(r.getByText('text')).toHaveAttribute('title', en.settings.api.models.capTextHint)
    expect(r.getByText('image')).toHaveAttribute('title', expect.stringMatching(/^Image generation: /))
    expect(r.getByText('tools')).toHaveAttribute('title', expect.stringMatching(/^Tool calling \(function calling\): /))
    // The words are the same on every row: a model's tools mean what another's do.
    expect(within(row('claude/sonnet')).getByText('tools')).toHaveAttribute('title', r.getByText('tools').getAttribute('title'))
  })

  it('names the badges of a row for the model, for a screen reader', () => {
    mount(modelsDoc())
    const list = within(row('codex/gpt-6-astra')).getByRole('list', { name: 'Capabilities of codex/gpt-6-astra' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(3)
    expect(within(row('claude/sonnet')).getByRole('list', { name: 'Capabilities of claude/sonnet' })).toBeInTheDocument()
  })

  it('shows a dash for a model the CLI listed no capabilities for: the field missing, null, empty or not a list', () => {
    const doc = modelsDoc()
    delete doc.models[1].capabilities // claude/sonnet: the CLI predates the field
    doc.models[2].capabilities = null // claude/haiku
    doc.models[3].capabilities = [] // codex/default
    doc.models[4].capabilities = 'text' // codex/gpt-6-astra: not a list
    mount(doc)
    for (const id of ['claude/sonnet', 'claude/haiku', 'codex/default', 'codex/gpt-6-astra']) {
      expect(capsCell(id).textContent, id).toBe('–')
      expect(caps(id), id).toEqual([])
    }
    expect(caps('claude/default')).toEqual(['text', 'tools']) // the others are as they were
  })

  it('renders the document of a CLI that predates the field: a dash for every model, and nothing fails', () => {
    mount(oldModelsDoc())
    const modelRows = screen.getAllByRole('row').slice(2) // under the header and the auto entry
    expect(modelRows).toHaveLength(8)
    for (const r of modelRows) expect(within(r).getAllByRole('cell')[2].textContent).toBe('–')
    expect(screen.getByRole('columnheader', { name: 'Capabilities' })).toBeInTheDocument()
  })

  it('shows a capability it does not know as the CLI named it, without a meaning to give it', () => {
    const doc = modelsDoc()
    doc.models[0].capabilities = ['text', 'audio', 'constructor'] // a name that is also a property of every object
    mount(doc)
    expect(caps('claude/default')).toEqual(['text', 'audio', 'constructor'])
    expect(within(row('claude/default')).getByText('audio')).not.toHaveAttribute('title')
    expect(within(row('claude/default')).getByText('constructor')).not.toHaveAttribute('title')
  })

  it('shows only the entries of the list that name a capability', () => {
    const doc = modelsDoc()
    doc.models[0].capabilities = [null, 7, '', 'text', { x: 1 }, 'tools']
    mount(doc)
    expect(caps('claude/default')).toEqual(['text', 'tools'])
  })

  it('leaves the auto entry alone, which has no capabilities: it spans the columns after the model', () => {
    mount(modelsDoc())
    const autoCells = within(autoRow()).getAllByRole('cell')
    expect(autoCells).toHaveLength(2)
    expect(autoCells[1]).toHaveAttribute('colspan', '6') // the six that follow the model's
    expect(within(autoRow()).queryAllByRole('listitem')).toHaveLength(0)
    expect(within(autoRow()).getByText('On. Jev picks among the 3 models it may use here (up to chat-only).')).toBeInTheDocument()
  })

  it('speaks the chosen language: the header, the labels, what they mean and the name of the list', async () => {
    await act(() => i18n.changeLanguage('es'))
    mount(modelsDoc())
    const m = es.settings.api.models
    expect(screen.getByRole('columnheader', { name: m.colCapabilities })).toBeInTheDocument()
    expect(caps('codex/gpt-6-astra')).toEqual([m.capText, m.capImage, m.capTools])
    expect(m.capText).not.toBe(en.settings.api.models.capText) // really translated
    const r = within(row('codex/gpt-6-astra'))
    expect(r.getByText(m.capText)).toHaveAttribute('title', m.capTextHint)
    expect(r.getByText(m.capImage)).toHaveAttribute('title', expect.stringMatching(/^Generación de imágenes: /))
    expect(r.getByText(m.capTools)).toHaveAttribute('title', expect.stringMatching(/\(function calling\)/))
    expect(r.getByRole('list', { name: m.capsOf.replace('{{model}}', 'codex/gpt-6-astra') })).toBeInTheDocument()
  })
})

describe('ApiModelsBlock: documents of other CLI versions', () => {
  it('renders a CLI that predates --auto-confinement: dashes, and an auto line without numbers', () => {
    mount(oldModelsDoc())
    expect(cells(row('claude/sonnet')).slice(4)).toEqual(['yes', 'yes', '–'])
    expect(within(autoRow()).getByText('On. Jev picks among the models it may use here.')).toBeInTheDocument()
    expect(screen.queryByText(/more model/)).not.toBeInTheDocument()
    cleanup()
    mount(oldModelsDoc({ autoState: { available: false, missing: MISSING_SURFACE } }))
    expect(within(autoRow()).getByRole('button', { name: 'Open the Jev settings' })).toBeInTheDocument()
  })

  it('ignores fields it does not know', () => {
    const doc = modelsDoc()
    doc.models[0].a_field_from_the_future = { x: 1 }
    doc.auto.another = ['y']
    doc.policy.and_one_more = true
    doc.v = 2
    mount(doc)
    expect(cells(row('claude/sonnet'))).toEqual(['claude/sonnetSonnet 5', 'chat-only', 'texttools', 'validated', 'yes', 'yes', 'yes'])
    expect(within(autoRow()).getByText(/On\. Jev picks among the 3 models/)).toBeInTheDocument()
  })

  it('shows no auto entry when the CLI sends none', () => {
    const doc = modelsDoc(); delete doc.auto
    mount(doc)
    expect(screen.queryByRole('row', { name: /^auto/ })).not.toBeInTheDocument()
  })
})

describe('ApiModelsBlock: language', () => {
  it('speaks the chosen language', async () => {
    await act(() => i18n.changeLanguage('es'))
    mount({ ...modelsDoc(), auto: { available: false, missing: MISSING_SURFACE } })
    const m = es.settings.api.models
    expect(screen.getByRole('table', { name: m.title })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: m.colContext })).toBeInTheDocument()
    expect(screen.getByText(m.policyAssumed.replace('{{addr}}', '127.0.0.1:9322'))).toBeInTheDocument()
    expect(within(autoRow()).getByText(m.autoOff.replace('{{missing}}', MISSING_SURFACE))).toBeInTheDocument()
    expect(within(autoRow()).getByRole('button', { name: m.openJev })).toBeInTheDocument()
  })

  it('speaks the chosen language in the sentences about several listeners, an unreadable status and a single candidate', async () => {
    await act(() => i18n.changeLanguage('es'))
    const m = es.settings.api.models
    mount({ ...modelsDoc(), auto: { available: true, key_source: 'vault', confinement: 'chat-only', candidates: 1 } }, {
      status: statusOf([mainListener(), dedicatedListener()]),
    })
    expect(screen.getByText(m.policyMany.replace('{{count}}', '2').replace('{{addr}}', '127.0.0.1:9322'), { exact: false })).toBeInTheDocument()
    expect(within(autoRow()).getByText(m.autoOn_one.replace('{{count}}', '1').replace('{{class}}', 'chat-only'))).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { status: null, statusErr: 'boom' })
    expect(screen.getByText(m.policyUnknown)).toBeInTheDocument()
  })
})
