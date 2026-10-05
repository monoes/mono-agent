// Display helpers for `monoagentcli org budget` (internal/monomind
// SectionBudgets). All numbers and states come from the CLI; nothing here
// recomputes spend, so the UI cannot disagree with `org report`.

export const BUDGET_COLORS = {
  ok: 'var(--green-neon)',
  warn: '#eab308',
  closed: 'var(--red)',
  unallocated: 'var(--text-muted)',
}

export const usd = (n) => (typeof n === 'number' ? `$${n.toFixed(2)}` : '—')

// {name: ScopeBudget} for the sections of a report; {} for an error or nothing.
export function bySection(report) {
  if (!report || report.error || !Array.isArray(report.sections)) return {}
  return Object.fromEntries(report.sections.map(s => [s.name, s]))
}

// One short line for a scope. A scope with no allocation shows only spend.
export function budgetLine(b) {
  if (!b) return ''
  if (b.allocation_usd == null) return `${usd(b.spent_usd)} spent`
  return `${usd(b.spent_usd)} / ${usd(b.allocation_usd)}`
}

// The state to show: monomind's soft-closure (from its own audit event) wins
// over the spend-derived state; a section at its allocation with no closure
// event is still "closed" by spend but not soft-closed.
export function budgetBadge(b) {
  if (!b) return null
  if (b.soft_closed) return { key: 'soft-closed', label: 'soft-closed', color: BUDGET_COLORS.closed }
  if (b.state === 'closed') return { key: 'over', label: 'at allocation', color: BUDGET_COLORS.closed }
  if (b.state === 'warn') return { key: 'warn', label: `${usd(b.remaining_usd)} left`, color: BUDGET_COLORS.warn }
  return null
}

export function budgetTitle(b) {
  if (!b) return ''
  const parts = [`${budgetLine(b)}${b.fraction != null ? ` (${Math.round(b.fraction * 100)}%)` : ''}`]
  if (b.role_cap_sum_usd) parts.push(`role caps total ${usd(b.role_cap_sum_usd)}`)
  if (b.soft_closed) {
    parts.push(`soft-closed at ${new Date(b.closed_at).toLocaleTimeString()}`)
    if (b.closed_roles?.length) parts.push(`closed: ${b.closed_roles.join(', ')}`)
    if (b.held_tasks?.length) parts.push(`held tasks: ${b.held_tasks.join(', ')}`)
  } else if (b.state === 'warn') parts.push('past 80% of its allocation: will soft-close at 100%')
  if (b.cost_unknown) parts.push('a usage event reported no cost: spend is a lower bound')
  return parts.join(' · ')
}
