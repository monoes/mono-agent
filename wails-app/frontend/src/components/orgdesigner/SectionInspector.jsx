// Right-panel editors for sections: SectionInspector (the selected section's
// budget, writes, rework rounds, lead, role caps, document edges, delete) and
// RoleSectionControls (a selected role's section and lead mark). Both only
// report intent; OrgDesigner calls the backend and shows its refusals.

import { useEffect, useState } from 'react'
import { sectionOfRole } from './sectionsGraph.js'
import { budgetLine, budgetBadge, budgetTitle, usd } from './budgetModel.js'

const field = { display: 'flex', flexDirection: 'column', gap: 3, fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }
const input = { fontFamily: 'var(--font-mono)', fontSize: 11, padding: '4px 6px', background: 'var(--elevated)', border: '1px solid var(--border)', borderRadius: 5, color: 'var(--text)' }
const selectStyle = { fontSize: 11, padding: '3px 22px 3px 6px' }
const btn = { fontFamily: 'var(--font-mono)', fontSize: 10, padding: '3px 8px', background: 'transparent', border: '1px solid var(--border)', borderRadius: 5, color: 'var(--text-secondary)', cursor: 'pointer' }

// A text input that commits on blur / Enter, and re-syncs when the saved value changes.
function Committed({ label, value, onCommit, type = 'text', placeholder, testId }) {
  const [v, setV] = useState(value)
  useEffect(() => setV(value), [value])
  const commit = () => { if (v !== value) onCommit(v) }
  return (
    <label style={field}>
      {label}
      <input
        data-testid={testId} type={type} value={v} placeholder={placeholder} style={input}
        min={type === 'number' ? 0 : undefined} step={type === 'number' ? 'any' : undefined}
        onChange={e => setV(e.target.value)} onBlur={commit}
        onKeyDown={e => { if (e.key === 'Enter') e.currentTarget.blur() }}
      />
    </label>
  )
}

const num = (s) => (s === '' ? null : Number(s))

export function SectionInspector({ section, budget, nodes, sections, edges, onUpdate, onPatchRole, onRemoveEdge, onDelete }) {
  const [moveTo, setMoveTo] = useState('')
  const others = sections.filter(s => s.name !== section.name)
  useEffect(() => setMoveTo(others[0]?.name || ''), [section.name]) // eslint-disable-line react-hooks/exhaustive-deps
  const members = section.roster.map(id => nodes.find(n => n.id === id)).filter(Boolean)
  const caps = members.reduce((t, n) => t + (typeof n.rest?.budget_usd === 'number' ? n.rest.budget_usd : 0), 0)
  const mine = edges.filter(e => e.from === section.name || e.to === section.name)
  return (
    <div data-testid="section-inspector" style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <strong style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: section.color }}>Section {section.name}</strong>

      <label style={field}>
        Lead
        <select data-testid="section-lead" value={section.leadId} className="filter-select" style={selectStyle} onChange={e => onUpdate({ lead: e.target.value })}>
          {section.roster.map(id => <option key={id} value={id}>{id}</option>)}
        </select>
      </label>
      {budget && (
        <div data-testid="section-live-budget" title={budgetTitle(budget)} style={{ ...field, color: budgetBadge(budget)?.color || 'var(--text-secondary)' }}>
          Spend this run: {budgetLine(budget)}{budgetBadge(budget) ? ` — ${budgetBadge(budget).label}` : ''}
          {budget.roles?.filter(r => r.cap_usd != null).map(r => <span key={r.id}>{r.id} {usd(r.spent_usd)} of {usd(r.cap_usd)}</span>)}
        </div>
      )}
      <Committed testId="section-budget" label="Budget (USD)" type="number" placeholder="no section budget" value={section.budgetUsd ?? ''} onCommit={v => onUpdate({ budget_usd: num(v) })} />
      <Committed testId="section-rework" label="Max rework rounds" type="number" placeholder="runtime default" value={section.maxReworkRounds ?? ''} onCommit={v => onUpdate({ max_rework_rounds: num(v) })} />
      <Committed testId="section-writes" label="Writes (paths, comma separated)" placeholder="src/**, docs/**" value={section.writes.join(', ')}
        onCommit={v => onUpdate({ writes: v.split(',').map(x => x.trim()).filter(Boolean) })} />

      <div style={field}>
        Role caps (USD){section.budgetUsd != null && <span> — {caps} of {section.budgetUsd} allocated</span>}
        {members.map(n => (
          <Committed key={n.id} testId={`cap-${n.id}`} label={`${n.id}${n.id === section.leadId ? ' (lead)' : ''}`} type="number" placeholder="no cap"
            value={n.rest?.budget_usd ?? ''} onCommit={v => v !== '' && onPatchRole(n.id, { budget_usd: Number(v) })} />
        ))}
      </div>

      <div style={field}>
        Document edges
        {mine.length === 0 && <span>none — drag from a section header's link handle to another section</span>}
        {mine.map(e => (
          <div key={`${e.type}${e.from}${e.to}`} style={{ display: 'flex', alignItems: 'center', gap: 6, color: 'var(--text-secondary)' }}>
            <span style={{ flex: 1 }}>{e.from} → {e.to} <em>{e.type}</em></span>
            <button style={btn} aria-label={`Remove ${e.type} edge`} onClick={() => onRemoveEdge(e)}>Remove</button>
          </div>
        ))}
      </div>

      <div style={{ ...field, borderTop: '1px solid var(--border)', paddingTop: 8 }}>
        Delete section
        {others.length > 0 && section.roster.length > 0 && (
          <select aria-label="Move its roles to" value={moveTo} className="filter-select" style={selectStyle} onChange={e => setMoveTo(e.target.value)}>
            {others.map(s => <option key={s.name} value={s.name}>move roles to {s.name}</option>)}
          </select>
        )}
        <button data-testid="section-delete" style={{ ...btn, color: 'var(--red)', borderColor: 'var(--red)' }} onClick={() => onDelete(moveTo)}>Delete {section.name}</button>
      </div>
    </div>
  )
}

export function RoleSectionControls({ node, sections, onAssign, onMakeLead }) {
  if (!node) return null
  const current = sectionOfRole(sections, node.id)
  const isEndpoint = node.rest?.kind === 'endpoint'
  const isRoot = node.parentId == null
  const lead = sections.find(s => s.name === current)?.leadId === node.id
  return (
    <div data-testid="role-section-controls" style={{ padding: '10px 12px', borderBottom: '1px solid var(--border)', display: 'flex', flexDirection: 'column', gap: 6 }}>
      {isRoot ? (
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)' }}>Root role — in no section; routes documents between sections.</span>
      ) : isEndpoint ? null : (
        <>
          <label style={field}>
            Section
            <select data-testid="role-section" value={current} className="filter-select" style={selectStyle} onChange={e => e.target.value && onAssign(e.target.value)}>
              {!current && <option value="">(none — only the root may be outside a section)</option>}
              {sections.map(s => <option key={s.name} value={s.name}>{s.name}</option>)}
            </select>
          </label>
          {current && (lead
            ? <span data-testid="role-is-lead" style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--yellow, #eab308)' }}>Lead of {current}</span>
            : <button data-testid="make-lead" style={btn} onClick={() => onMakeLead(current)}>Make lead of {current}</button>)}
        </>
      )}
    </div>
  )
}
