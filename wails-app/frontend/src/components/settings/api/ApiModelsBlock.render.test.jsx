// @vitest-environment jsdom
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import '@testing-library/jest-dom/vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import i18n from '../../../i18n.js'
import es from '../../../locales/es.json'
import ApiModelsBlock from './ApiModelsBlock.jsx'
import { mainListener, dedicatedListener, modelsDoc, oldModelsDoc, MISSING_SURFACE, MISSING_KEY } from './__fixtures__/apiFixtures.js'

beforeEach(async () => { await i18n.changeLanguage('en') })
afterEach(cleanup)

function mount(models, props = {}) {
  const onOpenJev = vi.fn(); const onRetry = vi.fn()
  render(<ApiModelsBlock models={models} err="" listener={mainListener()} onOpenJev={onOpenJev} onRetry={onRetry} {...props} />)
  return { onOpenJev, onRetry }
}
const row = (id) => screen.getByRole('row', { name: new RegExp(id.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')) })
const cells = (r) => within(r).getAllByRole('cell').map(c => c.textContent)
const autoRow = () => screen.getByRole('row', { name: /^auto/ })

describe('ApiModelsBlock: the table', () => {
  it('lists every model with its class, validation, and what the policy lets it do', () => {
    mount(modelsDoc())
    const table = screen.getByRole('table', { name: 'Models' })
    for (const h of ['Model', 'Confinement', 'Validated', 'Served', 'Context key', 'Auto']) {
      expect(within(table).getByRole('columnheader', { name: h })).toBeInTheDocument()
    }
    // id and label, class, validated, served, context key, auto
    expect(cells(row('claude/sonnet'))).toEqual(['claude/sonnetSonnet 5', 'chat-only', 'validated', 'yes', 'yes', 'yes'])
    expect(cells(row('codex/gpt-6-astra'))).toEqual(['codex/gpt-6-astraGPT-6-Astra', 'sandboxed', '–', 'yes', 'no', 'no'])
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
    mount(modelsDoc({ confinement: 'chat-only', forListener: 'network' }), { listener: dedicatedListener() })
    expect(cells(row('claude/default')).slice(3)).toEqual(['yes', 'yes', 'yes'])
    expect(cells(row('codex/default')).slice(3)).toEqual(['no (policy)', 'no', 'no'])
    expect(cells(row('pi/openrouter/nvidia/nemotron-3-super-120b-a12b:free'))[3]).toBe('no (policy)')
  })

  it('names the full label of a long model on hover', () => {
    mount(modelsDoc())
    expect(screen.getByTitle('Default (Use the default model (currently Opus 5 (1M context)))')).toBeInTheDocument()
  })

  it('says whose policy it shows', () => {
    mount(modelsDoc())
    expect(screen.getByText("Policy of the listener at 127.0.0.1:9322, assumed from this app's environment.")).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { listener: mainListener({ confinement_source: 'daemon' }) })
    expect(screen.getByText('Policy of the listener at 127.0.0.1:9322, as the running daemon reports it.')).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { listener: null })
    expect(screen.getByText(/No listener serves \/v1 right now: this is what this app's own settings would serve on a loopback listener\./)).toBeInTheDocument()
    cleanup()
    mount(modelsDoc(), { listener: mainListener({ v1: false }) })
    expect(screen.getByText(/No listener serves \/v1 right now/)).toBeInTheDocument()
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

  it('speaks of one model in the singular, and of none held back not at all', () => {
    mount({ ...modelsDoc(), auto: { available: true, key_source: 'vault', confinement: 'chat-only', candidates: 1, held_back: 1 } })
    expect(screen.getByText('On. Jev picks among the 1 model it may use here (up to chat-only).')).toBeInTheDocument()
    expect(screen.getByText(/^1 more model is served here but is above chat-only, so auto may not pick it\./)).toBeInTheDocument()
    cleanup()
    mount({ ...modelsDoc(), auto: { available: true, key_source: 'vault', confinement: 'chat-only', candidates: 3 } })
    expect(screen.queryByText(/more model/)).not.toBeInTheDocument()
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

describe('ApiModelsBlock: documents of other CLI versions', () => {
  it('renders a CLI that predates --auto-confinement: dashes, and an auto line without numbers', () => {
    mount(oldModelsDoc())
    expect(cells(row('claude/sonnet')).slice(3)).toEqual(['yes', 'yes', '–'])
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
    expect(cells(row('claude/sonnet'))).toEqual(['claude/sonnetSonnet 5', 'chat-only', 'validated', 'yes', 'yes', 'yes'])
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
})
