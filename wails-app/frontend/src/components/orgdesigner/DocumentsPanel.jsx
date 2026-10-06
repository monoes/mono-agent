// Read-only Documents panel for one org run (#339): the document channel of a
// sections org. Documents by producing section and type, status, producer ->
// consumer, round N of the rework cap, the lineage of each revise cycle,
// deliverables, and a badge when a cap is hit. No messaging: documents are the
// only channel between sections, and this panel only shows them.
import { AlertTriangle, ArrowRight, FileText } from 'lucide-react'
import { Chip, mono, mutedText, sectionLabel } from '../orgs/ui.jsx'
import useOrgDocuments from './useOrgDocuments.js'
import { statusMeta, versionLabel } from './orgDocuments.js'

const box = { background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 'var(--radius-lg)', padding: '10px 12px' }

export function CapBadge({ doc }) {
  const t = (doc.threads || []).find(x => x.exhausted)
  if (!t) return null
  return (
    <span
      role="status"
      title={`${t.consumer} rejected ${t.rounds} versions, its cap of ${t.cap} rework rounds. ${t.frozen ? 'The thread is frozen until the root decides it or raises the cap.' : 'The root decided it.'}`}
      style={{ ...mono, display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 10, fontWeight: 700, color: '#fff', background: 'var(--red, #ef4444)', borderRadius: 8, padding: '0 8px', lineHeight: '16px' }}
    >
      <AlertTriangle size={10} aria-hidden="true" /> Rework cap hit{t.frozen ? ' · frozen' : ''}
    </span>
  )
}

function Lineage({ doc }) {
  const vs = doc.versions || []
  return (
    <ol aria-label={`Versions of ${doc.id}`} style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 3 }}>
      {vs.map(v => {
        const m = statusMeta(v.status)
        const decisions = Object.entries(v.decisions || {})
        return (
          <li key={v.version} style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'baseline' }}>
            <b style={{ color: 'var(--text)' }}>v{v.version}</b>
            <span style={{ color: m.color }}>{versionLabel(v)}</span>
            {v.supersedes ? <span style={mutedText}>replaces v{v.supersedes}</span> : null}
            {(v.waiting_on || []).length > 0 && <span style={mutedText}>waiting on {v.waiting_on.join(', ')}</span>}
            {decisions.map(([consumer, d]) => (
              <span key={consumer} style={mutedText}>
                {consumer}: {d.decision === 'accept' ? 'accepted' : 'rejected'}{d.override ? ' (root override)' : ''} by {d.by}{d.reason ? ` — ${d.reason}` : ''}{d.relayed_at ? ' · relayed to producer' : ''}
              </span>
            ))}
            {v.note && <span style={mutedText}>note: {v.note}</span>}
          </li>
        )
      })}
    </ol>
  )
}

function DocCard({ doc }) {
  const m = statusMeta(doc.status)
  const hasRounds = (doc.threads || []).length > 0
  return (
    <li style={{ ...box, listStyle: 'none', display: 'flex', flexDirection: 'column', gap: 6 }} data-doc={doc.id}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 6 }}>
        <FileText size={12} aria-hidden="true" style={{ color: 'var(--text-muted)' }} />
        <span style={{ ...mono, fontSize: 12, color: 'var(--text)', fontWeight: 600 }}>{doc.id}</span>
        <Chip>{doc.type}</Chip>
        <Chip color={m.color}>{m.label}</Chip>
        {hasRounds && <Chip color={doc.cap_hit ? 'var(--red, #ef4444)' : '#eab308'} title="Rejections by the consuming section, of its max_rework_rounds">round {doc.round} of {doc.cap}</Chip>}
        <CapBadge doc={doc} />
      </div>
      <div style={{ ...mutedText, display: 'flex', alignItems: 'center', gap: 4, flexWrap: 'wrap' }}>
        <span>{doc.producer}</span><ArrowRight size={10} aria-label="to" />
        <span>{(doc.consumers || []).join(', ') || 'no consumer'}</span>
        <span>· v{doc.head}</span>
      </div>
      <Lineage doc={doc} />
      {(doc.deliverables || []).length > 0 && (
        <div style={{ ...mutedText }}>Deliverables: {doc.deliverables.map(f => <code key={f} style={{ marginRight: 6 }}>{f}</code>)}</div>
      )}
    </li>
  )
}

function SummaryBar({ s, view }) {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
      <span style={sectionLabel}>Documents</span>
      <Chip>{s.documents || 0} total</Chip>
      {s.pending > 0 && <Chip color={statusMeta('pending').color}>{s.pending} published</Chip>}
      {s.accepted > 0 && <Chip color={statusMeta('accepted').color}>{s.accepted} accepted</Chip>}
      {s.rejected > 0 && <Chip color={statusMeta('rejected').color}>{s.rejected} rejected</Chip>}
      {s.reworking > 0 && <Chip color="#eab308">{s.reworking} in rework</Chip>}
      {s.cap_hit > 0 && <Chip color="var(--red, #ef4444)">{s.cap_hit} cap hit</Chip>}
      {view.run && <span style={{ ...mutedText, marginLeft: 'auto' }}>{view.run}</span>}
    </div>
  )
}

export default function DocumentsPanel({ orgName, run = '', live = false }) {
  const { view, loading, error } = useOrgDocuments({ orgName, run, live })
  if (!view) {
    if (error) return <div role="alert" style={{ ...mutedText, color: 'var(--red, #ef4444)' }}>{error}</div>
    return <div style={mutedText}>{loading ? 'Loading documents…' : 'No documents.'}</div>
  }
  const byId = Object.fromEntries(view.docs.map(d => [d.id, d]))
  const groups = view.sections.filter(g => (g.types || []).length > 0)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <SummaryBar s={view.summary} view={view} />
      {view.integrity && (
        <div role="alert" style={{ ...mono, fontSize: 10.5, color: '#eab308' }}>
          The document log failed its integrity check: {view.integrity}
        </div>
      )}
      {view.docs.length === 0 && (
        <div style={mutedText}>This run has published no documents. Documents appear here when a section publishes one.</div>
      )}
      {groups.map(g => (
        <section key={g.section} aria-label={`Section ${g.section}`} style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
            <span style={sectionLabel}>{g.section}</span>
            <span style={mutedText}>produces</span>
          </div>
          {g.types.map(tg => (
            <div key={tg.type} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ ...mutedText, color: 'var(--text-secondary)' }}>{tg.type}</span>
              <ul style={{ margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
                {tg.docs.map(id => byId[id] && <DocCard key={id} doc={byId[id]} />)}
              </ul>
            </div>
          ))}
        </section>
      ))}
    </div>
  )
}
