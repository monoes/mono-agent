import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Minimize2, X, ChevronUp, ChevronDown, Code2 } from 'lucide-react'
import { api } from '../../services/api.js'
import { CoderHeader, CoderBadge } from '../chat/CoderHeader.jsx'
import { folderName } from '../chat/useCoderMode.js'
import { runtimeLabel } from '../../lib/runtimeLabels.js'
import { CoderChatView } from './CoderChatView.jsx'
import { useCoderConversation } from './useCoderConversation.js'
import { shouldCollapseOnBackdrop, nowDoing } from '../../lib/coderBubbles.js'
import './bubbles.css'

const mono = 'var(--font-mono)'
const CLOSE_MS = 170

// isModalOpen reports whether a dialog is open on top of the overlay
// (ConfirmDialog renders role="dialog" aria-modal="true").
function isModalOpen() {
  return !!document.querySelector('[aria-modal="true"]')
}

function prefersReducedMotion() {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

// Stage is the org at the top of an expanded coder bubble. Until the
// dynamic org (#226, #228) staffs workers, the org is the lead alone: its
// model, whether it is working, and what it is doing right now.
function Stage({ bubble, conv, runtime, model }) {
  const { t } = useTranslation()
  const working = conv.streaming
  const doing = working ? nowDoing(conv.liveTurn) : ''
  return (
    <div className="bubble-stage" data-testid="bubble-stage" style={{ height: '100%' }}>
      <div className={`stage-node${working ? ' working' : ''}`} data-testid="stage-lead" data-working={working ? 'true' : 'false'}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ width: 26, height: 26, borderRadius: 8, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,180,216,0.12)', color: 'var(--cyan)' }}>
            <Code2 size={14} />
          </span>
          <div style={{ minWidth: 0 }}>
            <div style={{ fontFamily: 'var(--font-display)', fontSize: 12.5, fontWeight: 600, color: 'var(--text)' }}>{t('bubbles.lead')}</div>
            <div style={{ fontFamily: mono, fontSize: 9.5, color: 'var(--text-muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {[runtime && runtimeLabel(runtime), model].filter(Boolean).join(' · ')}
            </div>
          </div>
          <span style={{ marginLeft: 'auto', width: 8, height: 8, borderRadius: '50%', background: working ? 'var(--cyan)' : 'var(--text-muted)', boxShadow: working ? '0 0 8px var(--cyan)' : 'none' }} />
        </div>
        <div style={{ fontFamily: mono, fontSize: 9.5, marginTop: 7, color: working ? 'var(--text-secondary)' : 'var(--text-muted)', minHeight: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {working ? (doing || t('bubbles.thinking')) : bubble.conversationId ? t('bubbles.idle') : t('bubbles.ready')}
        </div>
      </div>
      <div style={{ position: 'absolute', bottom: 8, left: 0, right: 0, textAlign: 'center', fontFamily: mono, fontSize: 9, color: 'var(--text-muted)', opacity: 0.7 }}>
        {t('bubbles.teamSoon')}
      </div>
    </div>
  )
}

// CoderChatOverlay is an expanded coder bubble (#227): the org stage on
// top, the chat below, over a dimmed backdrop. A click on the backdrop,
// Esc, or the collapse button shrinks it back into its bubble; none of
// them stop a running turn; the × closes the chat (onCloseChat, which
// asks first when a turn is running). originRect is the bubble's screen rectangle, so
// the overlay grows out of it.
export function CoderChatOverlay({ bubble, store, originRect, onCollapse, onCloseChat, onNavigate }) {
  const { t } = useTranslation()
  const view = store.getView(bubble.key)
  const [closing, setClosing] = useState(false)
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.innerWidth < 640)
  const [stageRatio, setStageRatio] = useState(view.stageRatio ?? 0.35)
  const [stageCollapsed, setStageCollapsed] = useState(!!view.stageCollapsed)
  const [draft, setDraft] = useState(view.draft || '')
  const [setup, setSetup] = useState(view.setup || { workspace: { kind: 'root' }, runtime: '', model: '', effort: '' })
  const bodyRef = useRef(null)
  const pressOnBackdrop = useRef(false)

  // create makes the coder conversation for a draft's first message, on the
  // runtime, model and effort picked in its setup (CoderSetup).
  const create = useCallback(async () => {
    const runtime = setup.runtime
    let cwd = setup.workspace?.kind === 'folder' ? setup.workspace.path : ''
    let workspace = null
    if (!cwd) {
      workspace = await api.coderWorkspaceRoot(runtime)
      cwd = workspace.path
    }
    const conv = await api.createCoderConversation(runtime, setup.model, setup.effort || '', cwd, false)
    const bound = { id: conv.id, cwd: conv.cwd || cwd, model: conv.model || setup.model, runtime: conv.runtimeId || runtime }
    store.bindConversation(bubble.key, bound)
    return { ...bound, workspace }
  }, [setup, store, bubble.key])
  const conv = useCoderConversation({ conversationId: bubble.conversationId, create })

  useEffect(() => { store.setView(bubble.key, { draft }) }, [store, bubble.key, draft])
  useEffect(() => { store.setView(bubble.key, { setup }) }, [store, bubble.key, setup])
  useEffect(() => { store.setView(bubble.key, { stageRatio, stageCollapsed }) }, [store, bubble.key, stageRatio, stageCollapsed])

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 640)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // collapse plays the shrink animation, then hands back to the bubble.
  const collapse = useCallback(() => {
    if (closing) return
    if (prefersReducedMotion()) { onCollapse(); return }
    setClosing(true)
    setTimeout(onCollapse, CLOSE_MS)
  }, [closing, onCollapse])

  // Esc collapses. Captured on window first, so the assistant panel under
  // the overlay (which also closes on Esc) doesn't react to the same key;
  // an open dialog handles its own Esc.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'Escape' || isModalOpen()) return
      e.stopImmediatePropagation()
      collapse()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [collapse])

  // Grow out of the bubble: the transform origin is the bubble's center,
  // relative to the overlay's own box.
  const panelRef = useRef(null)
  useLayoutEffect(() => {
    const el = panelRef.current
    if (!el || !originRect) return
    const box = el.getBoundingClientRect()
    el.style.setProperty('--origin-x', `${originRect.left + originRect.width / 2 - box.left}px`)
    el.style.setProperty('--origin-y', `${originRect.top + originRect.height / 2 - box.top}px`)
  }, [originRect])

  // Focus the composer on open; give focus back on close.
  useEffect(() => {
    const prev = document.activeElement
    const ta = panelRef.current?.querySelector('textarea:not([disabled])')
    ta?.focus?.()
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

  const title = bubble.cwd ? folderName(bubble.cwd) : t('bubbles.newChat')
  const runtime = bubble.conversationId ? bubble.runtime : setup.runtime
  const model = bubble.model || setup.model

  return (
    <>
      <div
        className={`bubble-backdrop${closing ? ' closing' : ''}`}
        data-testid="bubble-backdrop"
        onPointerDown={e => { pressOnBackdrop.current = e.target === e.currentTarget }}
        onClick={e => {
          const collapseIt = e.target === e.currentTarget && shouldCollapseOnBackdrop({
            pressStartedOnBackdrop: pressOnBackdrop.current,
            selectionText: window.getSelection?.()?.toString() || '',
            modalOpen: isModalOpen(),
          })
          pressOnBackdrop.current = false
          if (collapseIt) collapse()
        }}
      />
      <div ref={panelRef} role="dialog" aria-label={t('bubbles.chatLabel', { name: title })} data-testid="bubble-overlay"
        className={`bubble-overlay${closing ? ' closing' : ''}${narrow ? ' narrow' : ''}`}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '9px 12px', borderBottom: '1px solid var(--border)', flexShrink: 0 }}>
          {/* The folder row below carries the badge once there is a folder. */}
          {!bubble.cwd && <CoderBadge />}
          <span style={{ fontFamily: 'var(--font-display)', fontSize: 13, fontWeight: 600, color: 'var(--text)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{title}</span>
          {(runtime || model) && (
            <span style={{ fontFamily: mono, fontSize: 9.5, color: 'var(--cyan)', border: '1px solid var(--border-bright)', borderRadius: 999, padding: '1px 7px' }}>
              {[runtime && runtimeLabel(runtime), model].filter(Boolean).join(' · ')}
            </span>
          )}
          <span style={{ flex: 1 }} />
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => setStageCollapsed(c => !c)}
            aria-expanded={!stageCollapsed} title={stageCollapsed ? t('bubbles.showOrg') : t('bubbles.hideOrg')} style={{ gap: 4, fontSize: 10 }}>
            {stageCollapsed ? <ChevronDown size={12} /> : <ChevronUp size={12} />} {t('bubbles.org')}
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={collapse} title={t('bubbles.collapse')} aria-label={t('bubbles.collapse')} style={{ padding: '3px 6px' }}>
            <Minimize2 size={13} />
          </button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onCloseChat} title={t('bubbles.closeChat')} aria-label={t('bubbles.closeChat')} style={{ padding: '3px 6px' }}>
            <X size={13} />
          </button>
        </div>
        {bubble.cwd && <CoderHeader cwd={bubble.cwd} />}
        <div ref={bodyRef} style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}>
          {!stageCollapsed && (
            <>
              <div style={{ height: `${Math.round(stageRatio * 100)}%`, flexShrink: 0 }}>
                <Stage bubble={bubble} conv={conv} runtime={runtime} model={model} />
              </div>
              <div className="bubble-divider" role="separator" aria-orientation="horizontal" tabIndex={0}
                aria-label={t('bubbles.resizeOrg')} aria-valuenow={Math.round(stageRatio * 100)} aria-valuemin={15} aria-valuemax={70}
                onPointerDown={startDrag} onKeyDown={onDividerKey} />
            </>
          )}
          <CoderChatView
            conv={conv}
            isDraft={!bubble.conversationId}
            setup={setup}
            onSetupChange={patch => setSetup(s => ({ ...s, ...patch }))}
            draft={draft}
            onDraftChange={setDraft}
            initialScrollTop={view.scrollTop}
            onScroll={top => store.setView(bubble.key, { scrollTop: top })}
            onNavigate={onNavigate}
          />
        </div>
      </div>
    </>
  )
}
