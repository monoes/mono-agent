import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Minimize2, X, ChevronUp, ChevronDown, Pause, Play, Square, Network, ExternalLink } from 'lucide-react'
import { confirm } from '../ConfirmDialog.jsx'
import { notify } from '../../services/api.js'
import { shouldCollapseOnBackdrop } from '../../lib/coderBubbles.js'
import { orgStageOf } from '../../lib/orgBubble.js'
import { runtimeLabel } from '../../lib/runtimeLabels.js'
import { OrgStage } from '../stage/OrgStage.jsx'
import { StageDrawer } from '../stage/StageDrawer.jsx'
import { useRolesAccess } from '../orgdesigner/fullAccess.jsx'
import { OrgChatView } from './OrgChatView.jsx'
import { useOrgBubble } from './useOrgBubble.js'
import './bubbles.css'

const mono = 'var(--font-mono)'
const CLOSE_MS = 170

function isModalOpen() {
  return !!document.querySelector('[aria-modal="true"]')
}

function prefersReducedMotion() {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

// OrgBubbleOverlay is an expanded running-org bubble (#229): the org's
// stage on top (lib/orgBubble.js over its bus), and the chat with its boss
// below, over a dimmed backdrop. It closes back into its bubble the way a
// coder bubble does (backdrop, Esc, the collapse button), and collapsing
// never touches the org. Pause, resume and stop each ask first. Full
// access shows as badges; granting it stays on the Orgs page.
export function OrgBubbleOverlay({ bubble, store, originRect, onCollapse, onClose, onNavigate }) {
  const { t } = useTranslation()
  const orgName = bubble.orgName
  const view = store.getView(bubble.key)
  const [closing, setClosing] = useState(false)
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth < 640)
  const [stageRatio, setStageRatio] = useState(view.stageRatio ?? 0.4)
  const [stageCollapsed, setStageCollapsed] = useState(!!view.stageCollapsed)
  const [draft, setDraft] = useState(view.draft || '')
  const [selected, setSelected] = useState(null)
  const selectedRef = useRef(null)
  const bodyRef = useRef(null)
  const panelRef = useRef(null)
  const pressOnBackdrop = useRef(false)
  const conv = useOrgBubble(orgName)
  const { byRole: access } = useRolesAccess(orgName)

  useEffect(() => { store.setView(bubble.key, { draft }) }, [store, bubble.key, draft])
  useEffect(() => { store.setView(bubble.key, { stageRatio, stageCollapsed }) }, [store, bubble.key, stageRatio, stageCollapsed])

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 640)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const collapse = useCallback(() => {
    if (closing) return
    if (prefersReducedMotion()) { onCollapse(); return }
    setClosing(true)
    setTimeout(onCollapse, CLOSE_MS)
  }, [closing, onCollapse])

  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape' || isModalOpen()) return
      e.stopImmediatePropagation()
      if (selectedRef.current) { setSelected(null); return }
      collapse()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [collapse])

  useLayoutEffect(() => {
    const el = panelRef.current
    if (!el || !originRect) return
    const box = el.getBoundingClientRect()
    el.style.setProperty('--origin-x', `${originRect.left + originRect.width / 2 - box.left}px`)
    el.style.setProperty('--origin-y', `${originRect.top + originRect.height / 2 - box.top}px`)
  }, [originRect])

  useEffect(() => {
    const prev = document.activeElement
    panelRef.current?.querySelector('textarea:not([disabled])')?.focus?.()
    return () => prev?.focus?.()
  }, [])

  const startDrag = (e) => {
    e.preventDefault()
    const body = bodyRef.current?.getBoundingClientRect()
    if (!body) return
    const onMove = (ev) => setStageRatio(Math.min(0.7, Math.max(0.15, (ev.clientY - body.top) / body.height)))
    const onUp = () => { window.removeEventListener('pointermove', onMove); window.removeEventListener('pointerup', onUp) }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }
  const onDividerKey = (e) => {
    if (e.key === 'ArrowUp') { e.preventDefault(); setStageRatio(r => Math.max(0.15, r - 0.05)) }
    if (e.key === 'ArrowDown') { e.preventDefault(); setStageRatio(r => Math.min(0.7, r + 0.05)) }
  }

  const stage = useMemo(() => (conv.bubble ? orgStageOf(conv.bubble, access) : null), [conv.bubble, access])
  const selectedNode = selected ? (stage?.nodes?.[selected] || null) : null
  selectedRef.current = selectedNode ? selected : null
  const selectNode = useCallback(id => setSelected(cur => (cur === id ? null : id)), [])

  const history = conv.history
  const roleTitles = useMemo(() => Object.fromEntries((history?.roles || []).map(r => [r.id, r.title || r.id])), [history])
  const nameOf = useCallback(id => roleTitles[id] || id || t('orgBubble.boss'), [roleTitles, t])
  const boss = (history?.roles || []).find(r => r.id === history?.boss)
  const leadInfo = { runtime: boss?.runtime || '', model: boss?.model || '', effort: '' }

  const status = history?.status || ''
  const running = status === 'running'
  const paused = running && !!history?.paused
  const statusKey = paused ? 'paused' : running ? 'running' : status === 'never run' ? 'neverRun' : status === 'crashed' ? 'crashed' : status ? 'stopped' : 'loading'

  const control = async (verb) => {
    const ok = await confirm(t(`orgBubble.confirm.${verb}Body`, { name: orgName }), {
      title: t(`orgBubble.confirm.${verb}Title`, { name: orgName }),
      confirmLabel: t(`orgBubble.${verb}`), cancelLabel: t('orgBubble.cancel'), danger: verb === 'stop',
    })
    if (!ok) return
    const res = await conv.control(verb)
    if (res?.error) notify('orgs', t('orgBubble.couldNotControl', { error: res.error }))
  }

  return (
    <>
      <div className={`bubble-backdrop${closing ? ' closing' : ''}`} data-testid="bubble-backdrop"
        onPointerDown={e => { pressOnBackdrop.current = e.target === e.currentTarget }}
        onClick={e => {
          const collapseIt = e.target === e.currentTarget && shouldCollapseOnBackdrop({
            pressStartedOnBackdrop: pressOnBackdrop.current,
            selectionText: window.getSelection?.()?.toString() || '',
            modalOpen: isModalOpen(),
          })
          pressOnBackdrop.current = false
          if (collapseIt) collapse()
        }} />
      <div ref={panelRef} role="dialog" aria-label={t('orgBubble.chatLabel', { name: orgName })} data-testid="org-bubble-overlay"
        className={`bubble-overlay${closing ? ' closing' : ''}${narrow ? ' narrow' : ''}`}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '9px 12px', borderBottom: '1px solid var(--border)', flexShrink: 0, flexWrap: 'wrap' }}>
          <Network size={13} style={{ color: 'var(--cyan)' }} aria-hidden="true" />
          <span style={{ fontFamily: 'var(--font-display)', fontSize: 13, fontWeight: 600, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{orgName}</span>
          <span data-testid="org-bubble-status" data-status={statusKey} title={statusKey === 'crashed' ? history?.status_error || undefined : undefined}
            style={{ fontFamily: mono, fontSize: 9.5, color: running && !paused ? 'var(--teal, #00f5d4)' : statusKey === 'crashed' ? 'var(--red, #ef4444)' : 'var(--text-muted)', border: '1px solid var(--border-bright)', borderRadius: 999, padding: '1px 7px' }}>
            {t(`orgBubble.status.${statusKey}`)}
          </span>
          {(boss?.runtime || boss?.model) && (
            <span style={{ fontFamily: mono, fontSize: 9.5, color: 'var(--cyan)' }}>
              {t('orgBubble.bossIs', { name: boss.title || boss.id })} · {[boss.runtime && runtimeLabel(boss.runtime), boss.model].filter(Boolean).join(' · ')}
            </span>
          )}
          <span style={{ flex: 1 }} />
          {running && (paused ? (
            <button type="button" className="btn btn-ghost btn-sm" data-testid="org-bubble-resume" disabled={!!conv.controlling} onClick={() => control('resume')} style={{ gap: 4, fontSize: 10 }}>
              <Play size={11} /> {t('orgBubble.resume')}
            </button>
          ) : (
            <button type="button" className="btn btn-ghost btn-sm" data-testid="org-bubble-pause" disabled={!!conv.controlling} onClick={() => control('pause')} style={{ gap: 4, fontSize: 10 }}>
              <Pause size={11} /> {t('orgBubble.pause')}
            </button>
          ))}
          {running && (
            <button type="button" className="btn btn-ghost btn-sm" data-testid="org-bubble-stop" disabled={!!conv.controlling} onClick={() => control('stop')} style={{ gap: 4, fontSize: 10 }}>
              <Square size={10} /> {t('orgBubble.stop')}
            </button>
          )}
          {onNavigate && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => onNavigate('orgs', { org: orgName })} title={t('orgBubble.openInOrgs')} aria-label={t('orgBubble.openInOrgs')} style={{ padding: '3px 6px' }}>
              <ExternalLink size={12} />
            </button>
          )}
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => setStageCollapsed(c => !c)}
            aria-expanded={!stageCollapsed} title={stageCollapsed ? t('bubbles.showOrg') : t('bubbles.hideOrg')} style={{ gap: 4, fontSize: 10 }}>
            {stageCollapsed ? <ChevronDown size={12} /> : <ChevronUp size={12} />} {t('bubbles.org')}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={collapse} title={t('bubbles.collapse')} aria-label={t('bubbles.collapse')} style={{ padding: '3px 6px' }}>
            <Minimize2 size={13} />
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} title={t('orgBubble.close')} aria-label={t('orgBubble.close')} style={{ padding: '3px 6px' }}>
            <X size={13} />
          </button>
        </div>
        <div ref={bodyRef} style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', position: 'relative' }}>
          {!stageCollapsed && (
            <>
              <div style={{ height: `${Math.round(stageRatio * 100)}%`, flexShrink: 0 }}>
                <OrgStage stage={stage} turnId={conv.bubble?.run || ''} working={running && !paused}
                  leadInfo={leadInfo} leadIdle={running ? t('orgBubble.bossIdle') : t('orgBubble.status.stopped')}
                  emptyHint={t('orgBubble.teamHint')}
                  selectedId={selected} onSelect={selectNode} />
              </div>
              <div className="bubble-divider" role="separator" aria-orientation="horizontal" tabIndex={0}
                aria-label={t('bubbles.resizeOrg')} aria-valuenow={Math.round(stageRatio * 100)} aria-valuemin={15} aria-valuemax={70}
                onPointerDown={startDrag} onKeyDown={onDividerKey} />
            </>
          )}
          <OrgChatView conv={conv} nameOf={nameOf} draft={draft} onDraftChange={setDraft} />
          {selectedNode && (
            <StageDrawer node={selectedNode} calls={conv.bubble?.calls} leadInfo={leadInfo} turnId={conv.bubble?.run || ''} isLive={running}
              canStop={false} onClose={() => setSelected(null)} />
          )}
        </div>
      </div>
    </>
  )
}

