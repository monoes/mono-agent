// Pieces of the workflow import dialog that render the CLI's report on
// bundled automations: per-package status, the review of what would be
// installed, packages the file does not carry, and packages whose bundled
// copy differs from the installed one.
import { Package } from 'lucide-react'
import { Chip, body, label, mono, muted, panel } from '../connections/ui.jsx'

export const STATUS_TEXT = {
  created: (n) => `Imported “${n}”.`,
  updated: (n) => `Updated “${n}” — it was imported before; this version replaced it.`,
  unchanged: () => 'Already imported — nothing changed.',
}
const ITEM_COLORS = { present: 'var(--green-neon)', installed: 'var(--green-neon)', replaced: 'var(--green-neon)', missing: 'var(--yellow)', differs: 'var(--yellow)', conflict: 'var(--red)', failed: 'var(--red)' }

// installSummary describes the --yes re-run: the workflow itself comes back
// unchanged, so the news is what happened to the bundled packages.
export function installSummary(r) {
  const items = r.automations || []
  const done = items.filter(i => i.status === 'installed').length
  const replaced = items.filter(i => i.status === 'replaced').length
  const failed = items.filter(i => i.status === 'failed').length
  const parts = []
  if (done || !replaced) parts.push(`Installed ${done} bundled automation${done === 1 ? '' : 's'} for “${r.name || r.id}”.`)
  if (replaced) parts.push(`Replaced ${replaced} installed automation${replaced === 1 ? '' : 's'} with the file's version.`)
  if (failed) parts.push(`${failed} failed — see below.`)
  return parts.join(' ')
}

// ReviewDetail: what installing one bundled package means, from the CLI's
// dry-run review (reviewDetail), before anything is installed.
export function ReviewDetail({ item }) {
  const d = item.reviewDetail
  const line = { fontFamily: 'var(--font-mono)', fontSize: 10.5 }
  return (
    <div style={{ border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: '6px 8px', display: 'flex', flexDirection: 'column', gap: 3 }}>
      <span style={{ ...line, fontSize: 11.5, color: 'var(--text)' }}>{item.id} {item.version}</span>
      {d ? (
        <>
          <span style={line}>Publisher: {d.publisher || 'unknown'}</span>
          <span style={line}>Domains: {(d.domains || []).join(', ') || 'unrestricted'}</span>
          {/* The capabilities list already carries the visibility lines
              ("visits profiles — the profile's owner can see your visit…"),
              as in the automation import review. */}
          {(d.capabilities || []).length > 0 && (
            <ul style={{ margin: 0, paddingLeft: 16 }}>{d.capabilities.map(c => <li key={c} style={line}>{c}</li>)}</ul>
          )}
          {d.replaceRequired && d.replaces && <span style={{ ...line, color: 'var(--red)' }}>Replaces {d.replaces.source} {d.replaces.id} {d.replaces.version}</span>}
          {d.trustChange && <span style={{ ...line, color: 'var(--yellow)' }}>⚠ Trust drops from {d.trustChange.from} to {d.trustChange.to}: page scripts stay off and real runs of actions that change things need confirmation again.</span>}
        </>
      ) : item.review ? <span style={line}>{item.review}</span> : null}
      {item.error && <span style={{ ...line, color: 'var(--red)' }}>{item.error}</span>}
    </div>
  )
}

// ReviewNotes: what the one-line review leaves out — the replacement, a
// trust drop, and the plain-language capabilities (visibility included);
// the technical ones (steps, scripts, tier…) are already in the review line.
const TECHNICAL_CAP = /^(steps|scripts|tier):|^(downloads|policy-blocked)$/
function ReviewNotes({ d }) {
  if (!d) return null
  const note = { ...muted, fontSize: 10, paddingLeft: 19, wordBreak: 'break-word' }
  return (
    <>
      {d.replaceRequired && d.replaces && <span style={{ ...note, color: 'var(--red)' }}>Replaces {d.replaces.source} {d.replaces.id} {d.replaces.version}</span>}
      {d.trustChange && <span style={{ ...note, color: 'var(--yellow)' }}>⚠ Trust drops from {d.trustChange.from} to {d.trustChange.to}: page scripts stay off and real runs of actions that change things need confirmation again.</span>}
      {(d.capabilities || []).filter(c => !TECHNICAL_CAP.test(c)).map(c => <span key={c} style={note}>• {c}</span>)}
    </>
  )
}

// copyOfExisting returns the id of the same-named workflow an import was
// kept apart from (the CLI's "imported as a copy" warning). A structured
// field wins when the CLI sends one.
export const COPY_WARNING = /already exists:\s*([^\s;]+);\s*imported as a copy/i
export function copyOfExisting(res) {
  if (res?.copyOf) return res.copyOf
  for (const w of res?.warnings || []) {
    const m = COPY_WARNING.exec(w)
    if (m) return m[1]
  }
  return null
}

// splitBundle separates packages the file carries (installable when
// missing) from ones the exporter could not include (notBundled).
export function splitBundle(items) {
  const all = items || []
  const notIncluded = all.filter(i => i.notBundled && i.status === 'missing')
  const differs = all.filter(i => i.status === 'differs')
  const listed = all.filter(i => !notIncluded.includes(i) && !differs.includes(i))
  const installable = listed.filter(i => i.status === 'missing')
  // A bundle never replaces a built-in; only other packages can be.
  const replaceable = differs.filter(i => !isBuiltinCopy(i))
  return { listed, installable, notIncluded, differs, replaceable }
}

// isBuiltinCopy: the installed copy a "differs" package would replace is a
// built-in, which a workflow file never replaces.
export function isBuiltinCopy(item) {
  const r = item?.reviewDetail?.replaces
  return r?.source === 'builtin' || r?.trust === 'builtin'
}

export function NotIncluded({ items }) {
  if (!items.length) return null
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={label}>Not included in this file</span>
      <span style={{ ...body, fontSize: 11 }}>
        The workflow uses {items.length === 1 ? 'an automation' : 'automations'} the file does not carry. {items.length === 1 ? 'Its nodes' : 'Their nodes'} will not run until {items.length === 1 ? 'it is' : 'they are'} installed some other way.
      </span>
      {items.map(it => (
        <div key={it.id} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <span style={{ ...mono, fontSize: 11, color: 'var(--text)' }}><Package size={11} color="var(--text-muted)" style={{ verticalAlign: -1, marginRight: 6 }} />{it.id} <span style={muted}>{it.version}</span></span>
          {it.localOnly
            ? <span style={{ ...muted, fontSize: 10, paddingLeft: 19 }}>Only works on the sender's machine — recreate it here or ask the sender.</span>
            : it.error && <span style={{ ...muted, fontSize: 10, paddingLeft: 19, wordBreak: 'break-word' }}>{it.error.replace(/^not in the bundle:\s*/i, '')}</span>}
        </div>
      ))}
    </div>
  )
}

export function Automations({ items }) {
  if (!items?.length) return null
  return (
    <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
      <span style={label}>Bundled automations</span>
      {items.map(it => (
        <div key={it.id} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <Package size={11} color="var(--text-muted)" />
            <span style={{ ...mono, fontSize: 11, color: 'var(--text)', flex: 1 }}>{it.id} <span style={muted}>{it.version}</span></span>
            <Chip color={ITEM_COLORS[it.status] || 'var(--text-muted)'}>{it.status}{it.installedVersion && it.status === 'present' ? ` ${it.installedVersion}` : ''}</Chip>
          </div>
          {it.error && <span style={{ ...muted, fontSize: 10, color: 'var(--red)', paddingLeft: 19 }}>{it.error}</span>}
          {it.review && <span style={{ ...muted, fontSize: 10, paddingLeft: 19, wordBreak: 'break-word' }}>{it.review}</span>}
          <ReviewNotes d={it.reviewDetail} />
        </div>
      ))}
    </div>
  )
}

// CHANGE_LABELS names each ReviewChanges field in plain words.
const CHANGE_LABELS = [
  ['addedDomains', 'New sites it may open'],
  ['addedSteps', 'New step types'],
  ['addedCallActions', 'New actions it may call'],
  ['addedScripts', 'New page scripts'],
  ['changedScripts', 'Changed page scripts'],
]

export function ChangeList({ changes }) {
  const rows = CHANGE_LABELS.filter(([k]) => (changes?.[k] || []).length)
  if (!rows.length) return <span style={{ ...muted, fontSize: 10 }}>Different files, but nothing it is allowed to do changes.</span>
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {rows.map(([k, text]) => (
        <span key={k} style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', wordBreak: 'break-word' }}>{text}: {changes[k].join(', ')}</span>
      ))}
    </div>
  )
}

// Differs: installed packages whose bundled copy has the same version but
// other content. The installed copy is kept; replacing it is an explicit
// choice (the dialog's "Replace with the file's version").
export function Differs({ items }) {
  if (!items.length) return null
  return (
    <div style={{ ...panel, borderColor: 'var(--yellow)', display: 'flex', flexDirection: 'column', gap: 8 }}>
      <span style={label}>Different from what is installed</span>
      <span style={{ ...body, fontSize: 11 }}>
        The file carries {items.length === 1 ? 'an automation' : 'automations'} with the same version as {items.length === 1 ? 'one' : 'ones'} installed here, but different contents. Your installed copy was kept.
      </span>
      {items.map(it => (
        <div key={it.id} style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <span style={{ ...mono, fontSize: 11, color: 'var(--text)' }}><Package size={11} color="var(--text-muted)" style={{ verticalAlign: -1, marginRight: 6 }} />{it.id} <span style={muted}>{it.version}</span></span>
          <div style={{ paddingLeft: 19 }}><ChangeList changes={it.changes} /></div>
          {/^(not replaced|could not compare)/i.test(it.error || '') && <span style={{ ...muted, fontSize: 10, paddingLeft: 19, color: 'var(--red)' }}>{it.error}</span>}
          {isBuiltinCopy(it) && <span style={{ ...muted, fontSize: 10, paddingLeft: 19 }}>The installed copy is a built-in; a workflow file never replaces it.</span>}
        </div>
      ))}
    </div>
  )
}
