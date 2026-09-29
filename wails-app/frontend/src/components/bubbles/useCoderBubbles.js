import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, onChatEvent } from '../../services/api.js'
import {
  newDraftKey, emptySummary, applyChatEvent, summaryFromTurns, moveBubble, loadState, saveState,
} from '../../lib/coderBubbles.js'

// useCoderBubbles owns the open coder chat bubbles (#227): the list and its
// order, which one is expanded, each one's collapsed summary, and the
// per-chat things that must survive collapsing (the unsent draft, scroll
// position, the stage divider). It holds one app-wide chat:event
// subscription that updates every open bubble's summary, so a collapsed
// chat costs a few fields, not a mounted transcript.
//
// A bubble is { key, conversationId, cwd, model }. A new coder chat is a
// draft (conversationId '') until its first message creates the
// conversation; its key stays the same after that.
export function useCoderBubbles() {
  const initial = useMemo(() => loadState(), [])
  const [bubbles, setBubbles] = useState(initial.bubbles)
  const [side, setSideState] = useState(initial.side)
  const [expandedKey, setExpandedKey] = useState('')
  const [summaries, setSummaries] = useState({})
  // Per-chat UI state that outlives a collapse: { draft, scrollTop,
  // stageRatio, stageCollapsed }. A ref, not state: writing it never needs a
  // re-render, and reading it happens once, when a chat expands.
  const viewState = useRef({})

  const expandedRef = useRef('')
  expandedRef.current = expandedKey
  const byConversation = useRef({})
  byConversation.current = Object.fromEntries(bubbles.filter(b => b.conversationId).map(b => [b.conversationId, b.key]))

  useEffect(() => { saveState({ bubbles, side }) }, [bubbles, side])

  // Restored bubbles: whether each one's latest turn is still running.
  useEffect(() => {
    for (const b of initial.bubbles) {
      Promise.resolve().then(() => api.getChatTurns(b.conversationId, '', 1))
        .then(res => {
          const items = Array.isArray(res?.items) ? res.items : []
          setSummaries(s => ({ ...s, [b.key]: summaryFromTurns(items) }))
        })
        .catch(() => {})
    }
  }, [initial])

  useEffect(() => onChatEvent(ev => {
    const key = byConversation.current[ev?.conversationId]
    if (!key) return
    setSummaries(s => ({ ...s, [key]: applyChatEvent(s[key], ev, expandedRef.current === key) }))
  }), [])

  const expand = useCallback((key) => {
    setExpandedKey(key)
    setSummaries(s => (s[key]?.unread ? { ...s, [key]: { ...s[key], unread: 0 } } : s))
  }, [])

  const collapse = useCallback(() => setExpandedKey(''), [])

  const openDraft = useCallback(() => {
    const key = newDraftKey()
    setBubbles(list => [...list, { key, conversationId: '', cwd: '', model: '' }])
    expand(key)
    return key
  }, [expand])

  // openConversation shows an existing coder conversation as a bubble,
  // reusing its bubble when it already has one.
  const openConversation = useCallback((conv) => {
    if (!conv?.id) return ''
    const existing = byConversation.current[conv.id]
    if (existing) { expand(existing); return existing }
    const key = conv.id
    setBubbles(list => [...list, { key, conversationId: conv.id, cwd: conv.cwd || '', model: conv.model || '' }])
    Promise.resolve().then(() => api.getChatTurns(conv.id, '', 1))
      .then(res => setSummaries(s => ({ ...s, [key]: summaryFromTurns(Array.isArray(res?.items) ? res.items : []) })))
      .catch(() => {})
    expand(key)
    return key
  }, [expand])

  // bindConversation turns a draft into a real conversation once its first
  // message created one.
  const bindConversation = useCallback((key, conv) => {
    byConversation.current = { ...byConversation.current, [conv.id]: key }
    setBubbles(list => list.map(b => (b.key === key ? { ...b, conversationId: conv.id, cwd: conv.cwd || b.cwd, model: conv.model || b.model } : b)))
  }, [])

  const close = useCallback((key) => {
    setBubbles(list => list.filter(b => b.key !== key))
    setSummaries(s => { const next = { ...s }; delete next[key]; return next })
    delete viewState.current[key]
    setExpandedKey(k => (k === key ? '' : k))
  }, [])

  const reorder = useCallback((fromKey, toKey) => setBubbles(list => moveBubble(list, fromKey, toKey)), [])
  const setSide = useCallback((next) => setSideState(next === 'left' ? 'left' : 'right'), [])

  const getView = useCallback((key) => viewState.current[key] || {}, [])
  const setView = useCallback((key, patch) => {
    viewState.current[key] = { ...(viewState.current[key] || {}), ...patch }
  }, [])

  return {
    bubbles, side, expandedKey, summaries,
    summaryOf: key => summaries[key] || emptySummary(),
    expand, collapse, openDraft, openConversation, bindConversation, close, reorder, setSide,
    getView, setView,
  }
}
