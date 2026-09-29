import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ArrowDown, Loader } from 'lucide-react'
import { api } from '../../services/api.js'
import { ChatTimeline } from '../chat/ChatTimeline.jsx'
import { TurnStatus } from '../chat/TurnStatus.jsx'
import { ChatComposer } from '../chat/ChatComposer.jsx'
import { useChatScroll } from '../chat/useChatScroll.js'
import { CoderModePicker } from '../chat/CoderModePicker.jsx'
import { CoderInitNote } from '../chat/CoderHeader.jsx'
import { useCoderStatus, useRecentWorkspaces, useCoderRuntimeChoice, coderRuntimes, coderReady, fidelityNote, missingText } from '../chat/useCoderMode.js'
import { runtimeLabel } from '../../lib/runtimeLabels.js'
import { MessageBubble } from '../AIChatPanel.jsx'
import { isAgentNotSetup } from '../../lib/agentSetup.js'
import AgentSetupLink from '../AgentSetupLink.jsx'
import '../chat/chat.css'

const mono = 'var(--font-mono)'
const selectStyle = {
  background: '#020509', border: '1px solid rgba(0,180,216,0.15)', borderRadius: 6,
  padding: '4px 8px', color: '#e2e8f0', fontFamily: mono, fontSize: 10, outline: 'none', minWidth: 0,
}

// CoderSetup is a new coder bubble's choices before its first message: the
// folder (coder root, a picked folder, or a recent one), the runtime, the
// model and the effort. The runtimes offered are the ones `coder status`
// reports ready (useCoderRuntimeChoice); the models are the chosen
// runtime's own.
function CoderSetup({ status, setup, onChange, onNavigate }) {
  const { t } = useTranslation()
  const [models, setModels] = useState(null)
  const recent = useRecentWorkspaces(!!status?.enabled)
  const setRuntime = useCallback(id => onChange({ runtime: id, model: '', effort: '' }), [onChange])
  const choice = useCoderRuntimeChoice({
    active: !!status?.enabled, status, runtimes: coderRuntimes(status), selectedRuntime: setup.runtime, setSelectedRuntime: setRuntime,
  })
  const runtime = choice.readyIds.includes(setup.runtime) ? setup.runtime : ''
  useEffect(() => {
    if (!runtime) return
    let current = true
    setModels(null)
    Promise.resolve().then(() => api.getAgentRuntimeModels(runtime, '')).then(list => {
      if (!current) return
      const items = Array.isArray(list) ? list : []
      setModels(items)
      if (!setup.model && items.length) onChange({ model: items[0].id })
    }).catch(() => { if (current) setModels([]) })
    return () => { current = false }
    // Loaded once per runtime; onChange/setup.model only seed the default.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runtime])

  if (!status) {
    return <div style={{ padding: 16, fontFamily: mono, fontSize: 10, color: 'var(--text-muted)' }}>{t('bubbles.loading')}</div>
  }
  if (!status.enabled) {
    return (
      <div data-testid="coder-disabled" style={{ padding: 16, fontFamily: mono, fontSize: 10.5, color: '#fbbf24', lineHeight: 1.6 }}>
        {t('bubbles.coderOff')}{' '}
        <button type="button" onClick={() => onNavigate?.('settings')} className="btn btn-ghost btn-sm" style={{ fontSize: 10 }}>
          {t('bubbles.openSettings')}
        </button>
      </div>
    )
  }
  if (!coderReady(status)) {
    return <div style={{ padding: 16, fontFamily: mono, fontSize: 10.5, color: '#fbbf24' }}>{t('bubbles.coderNotReady', { why: missingText(status) })}</div>
  }
  const model = (models || []).find(m => m.id === setup.model)
  const efforts = choice.info?.effort !== false && Array.isArray(model?.effort_levels) ? model.effort_levels : []
  const note = fidelityNote(choice.info)
  const pickFolder = async () => {
    const dir = await Promise.resolve(api.pickCoderFolder()).catch(() => '')
    if (dir) onChange({ workspace: { kind: 'folder', path: dir } })
  }
  return (
    <div data-testid="coder-setup">
      <CoderModePicker status={status} mode="coder" onModeChange={() => {}} workspaceOnly
        workspace={setup.workspace} onWorkspaceChange={workspace => onChange({ workspace })}
        recent={recent} onPickFolder={pickFolder} runtimeId={runtime} />
      <div style={{ display: 'flex', gap: 6, padding: '8px 12px', borderBottom: '1px solid rgba(0,180,216,0.06)' }}>
        <select aria-label={t('bubbles.runtime')} value={runtime} onChange={e => setRuntime(e.target.value)} style={{ ...selectStyle, minWidth: 90 }}>
          {choice.options.map(r => <option key={r.id} value={r.id}>{runtimeLabel(r.id)}</option>)}
        </select>
        {models === null ? (
          <span style={{ ...selectStyle, flex: 1, display: 'flex', alignItems: 'center', gap: 6 }}>
            <Loader size={11} className="chat-spin" /> {t('bubbles.loadingModels')}
          </span>
        ) : (
          <select aria-label={t('bubbles.model')} value={setup.model} onChange={e => onChange({ model: e.target.value, effort: '' })} style={{ ...selectStyle, flex: 1 }}>
            {models.map(m => <option key={m.id} value={m.id}>{m.label || m.id}</option>)}
          </select>
        )}
        {efforts.length > 0 && (
          <select aria-label={t('bubbles.effort')} value={setup.effort} onChange={e => onChange({ effort: e.target.value })} style={{ ...selectStyle, minWidth: 72 }}>
            <option value="">{t('bubbles.effortAuto')}</option>
            {efforts.map(eff => <option key={eff} value={eff}>{eff}</option>)}
          </select>
        )}
      </div>
      {note && (
        <div style={{ padding: '0 12px 8px', fontFamily: mono, fontSize: 9, color: 'var(--text-muted)' }}>{note}</div>
      )}
    </div>
  )
}

// CoderChatView is the chat half of an expanded coder bubble: the
// transcript, the live turn and the composer. conv is useCoderConversation's
// result; draft/onDraftChange keep the unsent text across collapses.
export function CoderChatView({ conv, isDraft, setup, onSetupChange, draft, onDraftChange, initialScrollTop, onScroll, onNavigate }) {
  const { t } = useTranslation()
  const { status } = useCoderStatus(isDraft)
  const scroll = useChatScroll(`${conv.messages.length}:${conv.liveTurn.lastSeq}`)

  // Put the transcript back where it was when the bubble collapsed.
  useEffect(() => {
    const el = scroll.containerRef.current
    if (el && initialScrollTop != null) el.scrollTop = initialScrollTop
    // Only on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const send = useCallback(async () => {
    const text = draft
    if (!text.trim()) return
    onDraftChange('')
    const ok = await conv.send(text)
    if (ok === false && !conv.conversationId) onDraftChange(text)
  }, [draft, conv, onDraftChange])

  const blocked = isDraft && (!status?.enabled || !coderReady(status) || !setup.runtime || !setup.model)
  const empty = conv.messages.length === 0 && !conv.streaming

  return (
    <div style={{ display: 'flex', flexDirection: 'column', minHeight: 0, flex: 1 }}>
      {isDraft && empty && <CoderSetup status={status} setup={setup} onChange={onSetupChange} onNavigate={onNavigate} />}
      <div style={{ flex: 1, position: 'relative', minHeight: 0 }}>
        <div ref={scroll.containerRef} onScroll={e => onScroll?.(e.currentTarget.scrollTop)} data-testid="bubble-transcript"
          style={{ height: '100%', overflowY: 'auto', padding: 12, display: 'flex', flexDirection: 'column' }}>
          {conv.loading && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontFamily: mono, fontSize: 10, color: 'var(--text-muted)' }}>
              <Loader size={11} className="chat-spin" /> {t('bubbles.loadingChat')}
            </div>
          )}
          {conv.loadError && <MessageBubble role="error" isError content={conv.loadError} />}
          {empty && !conv.loading && !isDraft && (
            <div style={{ margin: 'auto', fontFamily: mono, fontSize: 11, color: 'var(--text-muted)' }}>{t('bubbles.emptyChat')}</div>
          )}
          {conv.messages.map((msg, i) => (
            msg.role === 'coder-init' ? <CoderInitNote key={i} workspace={msg.workspace} />
              : msg.role === 'turn' ? (
                <div key={i} className="chat-assistant-turn">
                  <ChatTimeline state={msg.state} turnId={msg.turnId} isLive={false} />
                  <TurnStatus state={msg.state} stopRequested={false} ownedByThisInstance={msg.ownedByThisInstance} />
                  {isAgentNotSetup(msg.state.terminal?.code) && <AgentSetupLink onNavigate={onNavigate} />}
                </div>
              ) : (
                <MessageBubble key={i} role={msg.role} content={msg.content} isError={msg.role === 'error'} code={msg.code} onNavigate={onNavigate} />
              )
          ))}
          {conv.streaming && (
            <div className="chat-assistant-turn">
              <ChatTimeline state={conv.liveTurn} turnId={conv.activeTurnId} isLive />
              <TurnStatus state={conv.liveTurn} stopRequested={conv.stopRequested} />
            </div>
          )}
        </div>
        {!scroll.isFollowing && scroll.unreadCount > 0 && (
          <button onClick={scroll.jumpToLatest} className="bubble-jump">
            <ArrowDown size={11} /> {t('bubbles.jumpToLatest')}
          </button>
        )}
      </div>
      <ChatComposer value={draft} onChange={onDraftChange} onSend={send} onStop={conv.stop}
        streaming={conv.streaming} disabled={blocked || conv.loading}
        disabledReason={blocked ? t('bubbles.setupFirst') : ''} />
    </div>
  )
}
