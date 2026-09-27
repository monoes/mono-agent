// The CLI classifies "the AI agent is not set up" (monomind missing or
// unusable, no runtime installed, the runtime missing or not logged in) as
// code agent_not_setup: on --json errors, on a chat turn's turn.finished,
// and — where only text is stored, like a workflow run's error_message —
// as the [agent_not_setup] marker at the end of the message. The app only
// recognises it; it never guesses from other wording.
export const AGENT_NOT_SETUP = 'agent_not_setup'
const MARKER = `[${AGENT_NOT_SETUP}]`

/** True when a code, an error message, or an error object says so. */
export function isAgentNotSetup(v) {
  if (!v) return false
  if (typeof v === 'string') return v === AGENT_NOT_SETUP || v.includes(MARKER)
  return v.code === AGENT_NOT_SETUP || isAgentNotSetup(v.message) || isAgentNotSetup(v.error)
}

/** The message without the marker, for display. */
export function withoutAgentSetupMarker(text) {
  return typeof text === 'string' ? text.split(MARKER).join('').replace(/\s+$/, '') : text
}
