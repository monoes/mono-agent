// Assistant tool access settings (GX2 contract): whether the AI chat panels
// pass monoagentTools / allowRuns to StreamAgentChat. Both default ON and
// are persisted to localStorage; toggled from Settings → "Assistant tool
// access" and read at render/send time by AIChatPanel. Unset (never
// touched) reads as ON — only an explicit '0' turns either off, so a user
// who deliberately disabled one keeps that choice across app updates.

const TOOLS_KEY = 'monoagent:assistantTools'
const RUNS_KEY = 'monoagent:assistantAllowRuns'

export function getAssistantTools() {
  try { return localStorage.getItem(TOOLS_KEY) !== '0' } catch { return true }
}

export function getAssistantAllowRuns() {
  try {
    return localStorage.getItem(RUNS_KEY) !== '0' && getAssistantTools()
  } catch { return true }
}

export function setAssistantTools(on) {
  try {
    localStorage.setItem(TOOLS_KEY, on ? '1' : '0')
    if (!on) localStorage.setItem(RUNS_KEY, '0') // sub-option can't outlive its parent
  } catch { /* localStorage unavailable — settings just won't persist */ }
}

export function setAssistantAllowRuns(on) {
  try { localStorage.setItem(RUNS_KEY, on ? '1' : '0') } catch { /* best-effort */ }
}
