import { describe, it, expect } from 'vitest'
import { computeNextStep } from './ApplicationsProcessPendingFlow.jsx'

describe('computeNextStep', () => {
  it('in auto mode, always proceeds to apply after evaluate', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'evaluated' })).toBe('apply')
  })

  it('in confirm mode, waits for a user decision after evaluate', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated' })).toBe('await-decision')
  })

  it('an explicit "apply" decision in confirm mode proceeds to apply', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'apply' })).toBe('apply')
  })

  it('an explicit "skip" decision in confirm mode moves to next without applying', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'skip' })).toBe('next')
  })

  it('an explicit "not-interested" decision cancels the application', () => {
    expect(computeNextStep({ mode: 'confirm', stage: 'evaluated', decision: 'not-interested' })).toBe('cancel')
  })

  it('after a successful apply, the stage is "ready-to-send" regardless of mode', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'applied' })).toBe('ready-to-send')
    expect(computeNextStep({ mode: 'confirm', stage: 'applied' })).toBe('ready-to-send')
  })

  it('already-prepared applications skip straight to ready-to-send without re-applying', () => {
    expect(computeNextStep({ mode: 'auto', stage: 'evaluated', alreadyPrepared: true })).toBe('ready-to-send')
  })
})
