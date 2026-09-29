import { useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { api, notify } from '../../services/api.js'
import { confirm } from '../ConfirmDialog.jsx'
import { BubbleDock } from './BubbleDock.jsx'
import { CoderChatOverlay } from './CoderChatOverlay.jsx'
import { useCoderStatus } from '../chat/useCoderMode.js'

// CoderBubbles puts the bubble dock and the expanded coder chat on screen
// (#227). store is useCoderBubbles(), held by App so the assistant panel
// can open coder chats into it. Only the expanded chat is mounted as a
// full chat; collapsed ones live as summaries in the store.
export default function CoderBubbles({ store, onNavigate }) {
  const { t } = useTranslation()
  // The dock's "+" only shows while coder mode is on in Settings.
  const { status } = useCoderStatus(true)
  const bubbleEls = useRef({})
  const registerEl = useCallback((key, el) => {
    if (el) bubbleEls.current[key] = el
    else delete bubbleEls.current[key]
  }, [])

  const collapseRef = useRef(() => {})
  const open = useCallback((key) => {
    if (store.expandedKey === key) collapseRef.current()
    else store.expand(key)
  }, [store])

  // Closing a chat whose turn is running asks first, and then stops the
  // turn: a closed bubble has nowhere left to show it. Collapsing keeps a
  // chat running instead.
  // A new chat with an unsent draft asks too: closing it loses the text.
  const closeChat = useCallback(async (key) => {
    const summary = store.summaryOf(key)
    const bubble = store.bubbles.find(b => b.key === key)
    if (bubble && !bubble.conversationId && String(store.getView(key).draft || '').trim()) {
      const ok = await confirm(t('bubbles.confirmDiscardBody'), {
        title: t('bubbles.confirmDiscardTitle'), confirmLabel: t('bubbles.discard'), cancelLabel: t('bubbles.keepOpen'), danger: true,
      })
      if (!ok) return
    } else if (summary.activeTurnId && bubble?.conversationId) {
      const ok = await confirm(t('bubbles.confirmCloseBody'), {
        title: t('bubbles.confirmCloseTitle'), confirmLabel: t('bubbles.stopAndClose'), cancelLabel: t('bubbles.keepOpen'), danger: true,
      })
      if (!ok) return
      try {
        await api.stopChatTurn(bubble.conversationId, summary.activeTurnId)
      } catch (err) {
        notify('chat', t('bubbles.couldNotStop', { error: String(err?.message || err) }))
      }
    }
    store.close(key)
  }, [store, t])

  // Collapsing a new chat that never got a message or any typed text
  // discards it, so trying Coder and changing your mind leaves no bubble.
  const collapse = useCallback(() => {
    const b = store.bubbles.find(x => x.key === store.expandedKey)
    if (b && !b.conversationId && !String(store.getView(b.key).draft || '').trim()) store.close(b.key)
    else store.collapse()
  }, [store])

  collapseRef.current = collapse
  const expanded = store.bubbles.find(b => b.key === store.expandedKey)
  const originRect = expanded ? bubbleEls.current[expanded.key]?.getBoundingClientRect() : null

  return (
    <>
      {expanded && (
        <CoderChatOverlay
          key={expanded.key}
          bubble={expanded}
          store={store}
          originRect={originRect}
          onCollapse={collapse}
          onCloseChat={() => closeChat(expanded.key)}
          onNavigate={(page) => { collapse(); onNavigate?.(page) }}
        />
      )}
      <BubbleDock store={store} onOpen={open} onClose={closeChat} registerEl={registerEl} canCreate={!!status?.enabled} />
    </>
  )
}
