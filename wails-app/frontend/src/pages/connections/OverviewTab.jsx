// Overview tab: manifest summary, site domains, permissions, trust and
// validation issues for one automation package.
import { useState } from 'react'
import { ExternalLink } from 'lucide-react'
import { api } from '../../services/api.js'
import { confirm } from '../../components/ConfirmDialog.jsx'
import { Chip, KV, ErrorBox, Busy, SOURCE_LABELS, body, label, mono, muted, panel, fmtDate } from './ui.jsx'

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

const TRUST_TEXT = {
  builtin: 'Ships with the app.',
  local: 'Written on this machine.',
  recorded: 'Saved from one of your recordings.',
  imported: 'Installed from a package file or URL.',
}

// TrustToggles: per-package opt-ins for imported and recorded packages
// (`automation trust`). Built-in and local packages are always trusted.
function TrustToggles({ info, onChanged }) {
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const restricted = info.trust === 'imported' || info.trust === 'recorded'
  const flip = async (kind, on) => {
    const q = kind === 'scripts'
      ? (on ? `Allow ${info.name || info.id} to run its page scripts? They run inside the site with your login and can read and send anything the page shows.` : `Stop ${info.name || info.id} from running page scripts? Actions that use them will fail.`)
      : (on ? `Allow real (live) runs of ${info.name || info.id}'s actions that change things on the site?` : `Withdraw live runs for ${info.name || info.id}? Its write actions will refuse to run for real.`)
    if (!(await confirm(q))) return
    setBusy(kind); setErr('')
    try {
      const res = await api.setAutomationTrust(info.id, on ? kind : `no-${kind}`)
      if (!res || res.error) setErr(res?.error || 'Could not change trust.')
      else await onChanged?.()
    } finally { setBusy('') }
  }
  const Toggle = ({ kind, label: text, on, hint }) => (
    <label style={{ display: 'flex', gap: 8, alignItems: 'flex-start', cursor: restricted ? 'pointer' : 'default' }}>
      <input type="checkbox" role="switch" checked={!!on} disabled={!restricted || !!busy} onChange={e => flip(kind, e.target.checked)} style={{ marginTop: 2 }} aria-label={text} />
      <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
        <span style={{ ...mono, fontSize: 11, color: 'var(--text)' }}>{text}</span>
        <span style={{ ...muted, fontSize: 10 }}>{hint}</span>
      </span>
    </label>
  )
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <span style={label}>Trust</span>
        <Chip color={restricted ? 'var(--yellow)' : 'var(--green-neon)'}>{info.trust || 'imported'}</Chip>
        <span style={{ ...muted, fontSize: 10 }}>{TRUST_TEXT[info.trust] || ''}</span>
      </div>
      <Toggle kind="scripts" label="Allow scripts" on={info.scriptsAllowed} hint={restricted ? 'Page scripts and in-page requests. Off until you allow them.' : 'Always allowed for built-in and local packages.'} />
      <Toggle kind="live" label="Allow live runs" on={info.liveRunConfirmed} hint={restricted ? 'Actions that write, message or delete may run for real.' : 'Always allowed for built-in and local packages.'} />
      {busy && <Busy text="Saving…" />}
      <ErrorBox>{err}</ErrorBox>
    </div>
  )
}

export default function OverviewTab({ info, manifest, issues, fragments, onTrustChanged }) {
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
      <TrustToggles info={info} onChanged={onTrustChanged} />
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
