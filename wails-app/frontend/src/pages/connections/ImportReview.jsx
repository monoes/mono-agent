// The install review (spec §6.3, contracts §8): what `automation install
// --dry-run --json` reports, in plain language first — capabilities,
// what it replaces, changes on update, the full source of every script —
// then the detail (domains, steps, side effects, files).
import { AlertTriangle, FileCode, ShieldAlert } from 'lucide-react'
import { Chip, EffectChip, ErrorBox, SOURCE_LABELS, body, label, mono, muted, panel, fileSize } from './ui.jsx'
import { IssueList } from './OverviewTab.jsx'

function Row({ title, children }) {
  return (
    <div style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
      <span style={{ ...label, minWidth: 110, paddingTop: 2 }}>{title}</span>
      <div style={{ flex: 1, display: 'flex', flexWrap: 'wrap', gap: 4, minWidth: 0 }}>{children}</div>
    </div>
  )
}

const none = <span style={muted}>none</span>
// needsReplaceConfirm: the registry says this install replaces something
// the user must confirm (review.replaceRequired → --replace): a built-in, a
// local or recorded package, or different content in the user's own
// package. An ordinary update of an import does not.
export function needsReplaceConfirm(review) {
  return !!review?.replaceRequired
}

// replaceTarget names what is replaced: the replaces block when present,
// else the package itself.
export function replaceTarget(res) {
  const r = res?.review?.replaces
  return { id: r?.id || res?.id, source: r?.source || r?.trust || '' }
}

export const sourceLabel = (s) => SOURCE_LABELS[s] || s || 'installed'


const warnPanel = (color) => ({ ...panel, borderColor: color, display: 'flex', flexDirection: 'column', gap: 6 })

export function ScriptSources({ sources, title = 'Page scripts' }) {
  const entries = Object.entries(sources || {})
  if (!entries.length) return null
  return (
    <div style={warnPanel('var(--orange)')}>
      <span style={{ ...label, color: 'var(--orange)', display: 'flex', alignItems: 'center', gap: 5 }}>
        <FileCode size={11} /> {title} ({entries.length})
      </span>
      <div style={{ ...body, fontSize: 11 }}>
        These run inside the page with your login: they can read anything the site shows you and send it elsewhere. Read them before you trust this package.
      </div>
      {entries.map(([name, src]) => (
        <details key={name}>
          <summary style={{ ...mono, fontSize: 11, color: 'var(--text)', cursor: 'pointer' }}>{name} <span style={muted}>({src.split('\n').length} lines)</span></summary>
          <pre style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', background: 'var(--base)', border: '1px solid var(--border)', borderRadius: 'var(--radius)', padding: 10, margin: '6px 0 0', maxHeight: 280, overflow: 'auto', whiteSpace: 'pre' }}>{src}</pre>
        </details>
      ))}
    </div>
  )
}

// cliOnly: warnings that tell a terminal user to pass a flag the GUI sets
// itself (the replace tick box covers --replace-builtin).
const isReplaceFlagHint = (w) => /--replace(-builtin)?\b/.test(w)

// The CLI also words the trust drop as a warning ("trust drops from a to b:
// <what that means>"); the review shows it once, as its own line, using the
// CLI's explanation when there is one.
const isTrustDropWarning = (w) => /^trust drops from /i.test(w)
function trustDropDetail(warnings) {
  const w = (warnings || []).find(isTrustDropWarning)
  const rest = w && w.includes(':') ? w.slice(w.indexOf(':') + 1).trim() : ''
  return rest || 'page scripts stay off and real runs of actions that change things need confirmation again.'
}

export default function ImportReview({ res, hideCliConfirmHint = false, resetsTrust = false }) {
  const r = res.review || {}
  const ch = r.changes
  const effects = Object.entries(r.actionEffects || {})
  const hasChanges = ch && (ch.addedDomains?.length || ch.addedSteps?.length || ch.addedScripts?.length || ch.changedScripts?.length || ch.addedCallActions?.length)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ ...mono, fontSize: 13, color: 'var(--text)', fontWeight: 700 }}>
        {res.name || res.id} <span style={{ color: 'var(--text-muted)', fontWeight: 400 }}>{res.id} · {res.previousVersion ? `${res.previousVersion} → ${res.version}` : res.version}</span>
      </div>
      {needsReplaceConfirm(r) && (() => {
        const t = replaceTarget(res)
        return (
          <div role="alert" style={warnPanel('var(--red)')}>
            <span style={{ ...label, color: 'var(--red)', display: 'flex', alignItems: 'center', gap: 5 }}>
              <ShieldAlert size={12} /> Replaces {sourceLabel(t.source)} {t.id}{r.replaces?.version ? ` ${r.replaces.version}` : ''}
            </span>
            <div style={{ ...body, fontSize: 11 }}>
              Installing this overwrites the {sourceLabel(t.source)} package with the same id. Workflows that use {t.id} nodes will run this package's code instead.
            </div>
          </div>
        )
      })()}
      {r.trustChange && (
        <div role="note" style={{ ...muted, color: 'var(--yellow)' }}>
          ⚠ Trust drops from {r.trustChange.from} to {r.trustChange.to}: {trustDropDetail(res.warnings)}
        </div>
      )}
      {r.policyBlocked && (
        <ErrorBox>
          <AlertTriangle size={11} style={{ verticalAlign: -1 }} /> Installs disabled: {r.policyReason || 'blocked by the usage policy in this build.'}
        </ErrorBox>
      )}
      {(r.capabilities || []).length > 0 && (
        <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={label}>What it can do</span>
          <ul style={{ margin: 0, paddingLeft: 18, display: 'flex', flexDirection: 'column', gap: 3 }}>
            {r.capabilities.map(c => <li key={c} style={{ ...body, fontSize: 11.5 }}>{c}</li>)}
          </ul>
        </div>
      )}
      {hasChanges ? (
        <div style={warnPanel('var(--yellow)')}>
          <span style={{ ...label, color: 'var(--yellow)' }}>This update changes what it can do</span>
          {ch.addedDomains?.length > 0 && <Row title="New domains">{ch.addedDomains.map(d => <Chip key={d} color="var(--yellow)">+ {d}</Chip>)}</Row>}
          {ch.addedSteps?.length > 0 && <Row title="New step types">{ch.addedSteps.map(d => <Chip key={d} color="var(--yellow)">+ {d}</Chip>)}</Row>}
          {ch.addedCallActions?.length > 0 && <Row title="New action calls">{ch.addedCallActions.map(d => <Chip key={d} color="var(--yellow)">+ {d}</Chip>)}</Row>}
          {ch.addedScripts?.length > 0 && <Row title="New scripts">{ch.addedScripts.map(d => <Chip key={d} color="var(--orange)">+ {d}</Chip>)}</Row>}
          {ch.changedScripts?.length > 0 && <Row title="Changed scripts">{ch.changedScripts.map(d => <Chip key={d} color="var(--orange)">~ {d}</Chip>)}</Row>}
        </div>
      ) : null}
      <ScriptSources sources={r.scriptSources} />
      <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 8 }}>
        <Row title="Publisher"><span style={{ ...mono, fontSize: 11, color: r.publisher ? 'var(--text)' : 'var(--yellow)' }}>{r.publisher || 'unknown'}</span></Row>
        <Row title="Trust"><span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)' }}>{r.trust || r.source || 'imported'}</span></Row>
        <Row title="Domains">{(r.domains || []).length ? r.domains.map(d => <Chip key={d} color="var(--cyan)">{d}</Chip>) : <span style={{ ...muted, color: 'var(--yellow)' }}>unrestricted</span>}</Row>
        {r.loginURL && <Row title="Login page"><span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)', wordBreak: 'break-all' }}>{r.loginURL}</span></Row>}
        <Row title="Step types">{(r.steps || []).length ? r.steps.map(d => <Chip key={d}>{d}</Chip>) : none}</Row>
        {(r.callActions || []).length > 0 && <Row title="Calls actions">{r.callActions.map(d => <Chip key={d} color="var(--yellow)">{d}</Chip>)}</Row>}
        <Row title="Page scripts">{(r.scripts || []).length ? r.scripts.map(d => <Chip key={d} color="var(--orange)"><FileCode size={8} /> {d}</Chip>) : none}</Row>
        <Row title="Downloads"><span style={{ ...mono, fontSize: 11, color: r.downloads ? 'var(--yellow)' : 'var(--text-muted)' }}>{r.downloads ? 'allowed' : 'no'}</span></Row>
        <Row title="Policy tier"><span style={{ ...mono, fontSize: 11, color: 'var(--text-secondary)' }}>{r.computedTier || r.tier || 'standard'}{r.native ? ` · needs the ${r.native} bot` : ''}</span></Row>
      </div>
      {effects.length > 0 && (
        <div style={{ ...panel, display: 'flex', flexDirection: 'column', gap: 5 }}>
          <span style={label}>Actions and their side effects</span>
          {effects.map(([a, e]) => (
            <div key={a} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <span style={{ ...mono, fontSize: 11, color: 'var(--text)', flex: 1 }}>{a}</span><EffectChip effect={e} />
            </div>
          ))}
        </div>
      )}
      <IssueList issues={res.issues} />
      {resetsTrust && (
        <div role="note" style={{ ...muted, color: 'var(--yellow)' }}>⚠ Updating resets 'Allow scripts' and 'Allow live runs' — you'll need to allow them again.</div>
      )}
      {(res.warnings || []).filter(w => !(hideCliConfirmHint && isReplaceFlagHint(w)) && !(r.trustChange && isTrustDropWarning(w))).map((w, i) => <div key={i} style={{ ...muted, color: 'var(--yellow)' }}>⚠ {w}</div>)}
      {(r.files || []).length > 0 && (
        <details style={{ ...panel }}>
          <summary style={{ ...label, cursor: 'pointer' }}>Files ({r.files.length})</summary>
          <div style={{ marginTop: 6, display: 'flex', flexDirection: 'column', gap: 2 }}>
            {r.files.map(f => (
              <div key={f.path} style={{ display: 'flex', gap: 8 }}>
                <span style={{ ...mono, fontSize: 10.5, color: 'var(--text-secondary)', flex: 1, wordBreak: 'break-all' }}>{f.path}</span>
                <span style={{ ...muted, fontSize: 10 }}>{fileSize(f.size)}</span>
              </div>
            ))}
          </div>
        </details>
      )}
      {res.sha256 && <div style={{ ...muted, fontSize: 9.5, wordBreak: 'break-all' }}>sha256 {res.sha256} — the install uses exactly these bytes.</div>}
    </div>
  )
}
