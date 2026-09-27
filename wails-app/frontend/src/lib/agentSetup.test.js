import { describe, it, expect } from 'vitest'
import { AGENT_NOT_SETUP, isAgentNotSetup, withoutAgentSetupMarker } from './agentSetup.js'

describe('isAgentNotSetup', () => {
  it('recognises the code, the marker, and error objects carrying either', () => {
    expect(isAgentNotSetup(AGENT_NOT_SETUP)).toBe(true)
    expect(isAgentNotSetup('agent.ask (claude) turn failed: auth: 401 [agent_not_setup]')).toBe(true)
    expect(isAgentNotSetup({ code: 'agent_not_setup' })).toBe(true)
    expect(isAgentNotSetup({ message: 'x [agent_not_setup]' })).toBe(true)
    expect(isAgentNotSetup({ error: 'x [agent_not_setup]', code: '' })).toBe(true)
  })

  it('never guesses from wording alone', () => {
    for (const v of [null, undefined, '', 'Not logged in', 'monomind not found', { code: 'not_found' }, { message: 'boom' }]) {
      expect(isAgentNotSetup(v)).toBe(false)
    }
  })
})

describe('withoutAgentSetupMarker', () => {
  it('drops the marker and leaves other text alone', () => {
    expect(withoutAgentSetupMarker('turn failed: Not logged in [agent_not_setup]')).toBe('turn failed: Not logged in')
    expect(withoutAgentSetupMarker('plain')).toBe('plain')
    expect(withoutAgentSetupMarker(null)).toBe(null)
  })
})
