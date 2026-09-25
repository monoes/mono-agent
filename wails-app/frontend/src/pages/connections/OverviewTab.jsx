// Overview tab: manifest summary, site domains, permissions, trust and
// validation issues for one automation package.
import { ExternalLink } from 'lucide-react'
import { api } from '../../services/api.js'
import { Chip, KV, ErrorBox, SOURCE_LABELS, body, label, mono, muted, panel, fmtDate } from './ui.jsx'

function List({ title, items, empty = 'none', color }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
      <span style={label}>{title}</span>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
        {items && items.length
          ? items.map(x => <Chip key={x} color={color}>{x}</Chip>)
          : <span style={muted}>{empty}</span>}
      </div>
    </div>
  )
}

export function IssueList({ issues }) {
  if (!issues || !issues.length) return null
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={label}>Validation</span>
      {issues.map((i, n) => (
        <div key={n} style={{ display: 'flex', gap: 8, alignItems: 'baseline' }}>
          <Chip color={i.severity === 'error' ? 'var(--red)' : 'var(--yellow)'}>{i.severity}</Chip>
          <span style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', wordBreak: 'break-word' }}>
            {i.file ? `${i.file}${i.stepId ? `#${i.stepId}` : ''}: ` : ''}{i.message} <span style={{ color: 'var(--text-dim)' }}>({i.code})</span>
          </span>
        </div>
      ))}
    </div>
  )
}

export default function OverviewTab({ info, manifest, issues, fragments }) {
  const perms = manifest.permissions || {}
  const site = manifest.site || {}
  const publisher = manifest.publisher
  return (
    <>
      {info.unavailableReason && <ErrorBox>Unavailable: {info.unavailableReason}</ErrorBox>}
      {info.pendingUpdate && (
        <div style={{ ...panel, ...body, fontSize: 11 }}>
          Version {info.pendingUpdate} ships with this app but is held back because you modified this automation. Uninstall and restore it to take the new version (your changes are lost).
        </div>
      )}
      {manifest.description && <div style={body}>{manifest.description}</div>}
      <KV rows={[
        ['Source', SOURCE_LABELS[info.source] || info.source],
        ['Trust', info.trust],
        ['Version', info.version + (info.previousVersion ? ` (previous ${info.previousVersion})` : '')],
        ['Publisher', publisher ? publisher.name : '—'],
        manifest.license && ['License', manifest.license],
        ['Category', manifest.category || info.category || '—'],
        ['Policy tier', manifest.policy?.tier || info.tier || 'standard'],
        manifest.requires?.native && ['Native bot', manifest.requires.native],
        ['Installed', fmtDate(info.installedAt)],
        ['Location', info.dir],
      ]} />
      {site.startUrl && (
        <button className="btn btn-ghost btn-sm" onClick={() => api.openURL(site.startUrl)} style={{ alignSelf: 'flex-start', gap: 5 }}>
          <ExternalLink size={11} /> {site.startUrl}
        </button>
      )}
      <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 12 }}>
        <List title="Domains it may visit" items={site.domains || info.domains} color="var(--cyan)" empty="unrestricted" />
        <List title="Step types allowed" items={perms.steps} empty="all (built-in)" />
        <List title="Page scripts" items={perms.scripts} color="var(--orange)" />
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span style={label}>Downloads</span>
          <span style={{ ...mono, fontSize: 11, color: perms.downloads ? 'var(--yellow)' : 'var(--text-muted)' }}>{perms.downloads ? 'allowed' : 'not allowed'}</span>
        </div>
        {fragments.length > 0 && <List title="Fragments" items={fragments} />}
      </div>
      <IssueList issues={issues} />
    </>
  )
}
