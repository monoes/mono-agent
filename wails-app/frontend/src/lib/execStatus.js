// One mapping from the engine's execution statuses (internal/workflow
// models.go plus SUCCESS_WITH_ERRORS and WAITING) to what the UI shows.
const MAP = {
  SUCCESS: 'success', COMPLETED: 'success', SUCCESS_WITH_ERRORS: 'partial',
  RUNNING: 'running', QUEUED: 'queued', PENDING: 'queued', WAITING: 'waiting',
  FAILED: 'failed', CANCELLED: 'cancelled',
}
const TONE = {
  success: 'var(--green-neon)', partial: '#fbbf24', running: 'var(--cyan)', queued: '#eab308',
  waiting: 'var(--purple-light)', failed: '#ef4444', cancelled: '#6b7280', unknown: 'var(--text-muted)',
}
const LIVE = new Set(['running', 'queued', 'waiting'])

/** Raw upper-case statuses that mean "still in progress". */
export const LIVE_STATUSES = new Set(Object.keys(MAP).filter(k => LIVE.has(MAP[k])))

export function execStatus(raw) {
  const key = MAP[(raw || '').toUpperCase()] || 'unknown'
  return { key, tone: TONE[key], live: LIVE.has(key) }
}
