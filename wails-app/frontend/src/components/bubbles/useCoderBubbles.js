import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, onChatEvent, onOrgEvent } from '../../services/api.js'
import {
  newDraftKey, emptySummary, applyChatEvent, summaryFromTurns, moveBubble, loadState, saveState, orgBubble, isOrgBubble,
} from '../../lib/coderBubbles.js'
import {
  emptyOrgSummary, applyOrgSummaryEvent, orgBubbleStatus, orgNeedsCount,
} from '../../lib/orgBubble.js'
import { acquireOrgEvents } from '../../lib/orgEventStreams.js'

function logItems(res) {
  if (!res || res.error) return []
  if (Array.isArray(res.items)) return res.items
  if (Array.isArray(res.events)) return res.events
  return Array.isArray(res) ? res : []
}

// rootRoleOf is an org design's boss: its one role that reports to nobody.
function rootRoleOf(design) {
  const roles = Array.isArray(design?.roles) ? design.roles : Array.isArray(design?.org?.roles) ? design.org.roles : []
  const roots = roles.filter(r => r && !r.reports_to && !r.reportsTo)
  return roots.length === 1 ? roots[0].id : ''
}

// useCoderBubbles owns the open coder chat bubbles (#227): the list and its
// order, which one is expanded, each one's collapsed summary, and the
// per-chat things that must survive collapsing (the unsent draft, scroll
// position, the stage divider). It holds one app-wide chat:event
// subscription that updates every open bubble's summary, so a collapsed
// chat costs a few fields, not a mounted transcript.
//
// A bubble is { key, conversationId, cwd, model, runtime }. A new coder chat is a
// draft (conversationId '') until its first message creates the
// conversation; its key stays the same after that. A running org opened as
// a bubble (#229) is { key: 'org:<name>', kind: 'org', orgName }: its
// collapsed summary is folded from `org:event` (the orgActivity reducer),
// and the store holds the org's event tail while the bubble is open.
export function useCoderBubbles() {
  const initial = useMemo(() => loadState(), [])
  const [bubbles, setBubbles] = useState(initial.bubbles)
  const [side, setSideState] = useState(initial.side)
  const [expandedKey, setExpandedKey] = useState('')
  const [summaries, setSummaries] = useState({})
  const [orgSummaries, setOrgSummaries] = useState({})
  // org → its boss's role id, for counting the boss's replies as unread.
  const bosses = useRef({})
  // Per-chat UI state that outlives a collapse: { draft, scrollTop,
  // stageRatio, stageCollapsed }. A ref, not state: writing it never needs a
  // re-render, and reading it happens once, when a chat expands.
  const viewState = useRef({})

  const expandedRef = useRef('')
  expandedRef.current = expandedKey
  const byConversation = useRef({})
  byConversation.current = Object.fromEntries(bubbles.filter(b => b.conversationId).map(b => [b.conversationId, b.key]))
  const byOrg = useRef({})
  byOrg.current = Object.fromEntries(bubbles.filter(isOrgBubble).map(b => [b.orgName, b.key]))
  const orgKey = bubbles.filter(isOrgBubble).map(b => b.orgName).join('\u0000')

  useEffect(() => { saveState({ bubbles, side }) }, [bubbles, side])

  // Restored bubbles: whether each one's latest turn is still running. A
  // conversation deleted since (the CLI reports it not found) loses its
  // bubble.
  useEffect(() => {
    for (const b of initial.bubbles) {
      if (isOrgBubble(b)) continue
      Promise.resolve().then(() => api.getChatTurns(b.conversationId, '', 1))
        .then(res => {
          if (res?.notFound) { dropBubble(b.key); return }
          const items = Array.isArray(res?.items) ? res.items : []
          setSummaries(s => ({ ...s, [b.key]: summaryFromTurns(items) }))
        })
        .catch(() => {})
    }
    // dropBubble only uses state setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial])

  useEffect(() => onChatEvent(ev => {
    const key = byConversation.current[ev?.conversationId]
    if (!key) return
    setSummaries(s => ({ ...s, [key]: applyChatEvent(s[key], ev, expandedRef.current === key) }))
  }), [])

  // Every open org bubble holds its org's event tail, and its collapsed
  // summary starts from the current run's log.
  useEffect(() => {
    const names = orgKey ? orgKey.split('\u0000') : []
    const releases = names.map(name => acquireOrgEvents(name))
    return () => releases.forEach(release => release())
  }, [orgKey])

  const seedOrg = useCallback((name, key) => {
    Promise.resolve().then(() => api.getOrgDesign(name))
      .then(design => { const boss = rootRoleOf(design); if (boss) bosses.current[name] = boss })
      .catch(() => {})
    Promise.resolve().then(() => api.getOrgLogs(name, ''))
      .then(res => {
        setOrgSummaries(s => {
          let sum = s[key] || emptyOrgSummary(name)
          for (const ev of logItems(res)) sum = applyOrgSummaryEvent(sum, ev, { expanded: true })
          return { ...s, [key]: sum }
        })
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    for (const b of initial.bubbles) if (isOrgBubble(b)) seedOrg(b.orgName, b.key)
  }, [initial, seedOrg])

  useEffect(() => onOrgEvent(payload => {
    const key = byOrg.current[payload?.orgName]
    if (!key || !payload.event) return
    const boss = bosses.current[payload.orgName] || ''
    setOrgSummaries(s => {
      const next = applyOrgSummaryEvent(s[key] || emptyOrgSummary(payload.orgName), payload.event, { boss, expanded: expandedRef.current === key })
      return next === s[key] ? s : { ...s, [key]: next }
    })
  }), [])

  const dropBubble = (key) => {
    setBubbles(list => list.filter(b => b.key !== key))
    setSummaries(s => { const next = { ...s }; delete next[key]; return next })
    setOrgSummaries(s => { if (!s[key]) return s; const next = { ...s }; delete next[key]; return next })
    delete viewState.current[key]
    setExpandedKey(k => (k === key ? '' : k))
  }

  const expand = useCallback((key) => {
    setExpandedKey(key)
    setSummaries(s => (s[key]?.unread ? { ...s, [key]: { ...s[key], unread: 0 } } : s))
    setOrgSummaries(s => (s[key]?.unread ? { ...s, [key]: { ...s[key], unread: 0 } } : s))
  }, [])

  const collapse = useCallback(() => setExpandedKey(''), [])

  const openDraft = useCallback(() => {
    const key = newDraftKey()
    setBubbles(list => [...list, { key, conversationId: '', cwd: '', model: '', runtime: '' }])
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
    setBubbles(list => [...list, { key, conversationId: conv.id, cwd: conv.cwd || '', model: conv.model || '', runtime: conv.runtimeId || conv.runtime || '' }])
    Promise.resolve().then(() => api.getChatTurns(conv.id, '', 1))
      .then(res => setSummaries(s => ({ ...s, [key]: summaryFromTurns(Array.isArray(res?.items) ? res.items : []) })))
      .catch(() => {})
    expand(key)
    return key
  }, [expand])

  // openOrg shows a running org as a bubble, reusing its bubble when it has
  // one. expand: false adds it collapsed (an org that asks the person
  // something gets a bubble without taking over the screen).
  const openOrg = useCallback((name, { expand: open = true } = {}) => {
    if (!name) return ''
    const existing = byOrg.current[name]
    if (existing) { if (open) expand(existing); return existing }
    const b = orgBubble(name)
    byOrg.current = { ...byOrg.current, [name]: b.key }
    setBubbles(list => (list.some(x => x.key === b.key) ? list : [...list, b]))
    seedOrg(name, b.key)
    if (open) expand(b.key)
    return b.key
  }, [expand, seedOrg])

  // bindConversation turns a draft into a real conversation once its first
  // message created one.
  const bindConversation = useCallback((key, conv) => {
    byConversation.current = { ...byConversation.current, [conv.id]: key }
    setBubbles(list => list.map(b => (b.key === key ? { ...b, conversationId: conv.id, cwd: conv.cwd || b.cwd, model: conv.model || b.model, runtime: conv.runtime || b.runtime } : b)))
  }, [])

  // dropBubble only uses state setters and a ref.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const close = useCallback(dropBubble, [])

  const reorder = useCallback((fromKey, toKey) => setBubbles(list => moveBubble(list, fromKey, toKey)), [])
  const setSide = useCallback((next) => setSideState(next === 'left' ? 'left' : 'right'), [])

  const getView = useCallback((key) => viewState.current[key] || {}, [])
  const setView = useCallback((key, patch) => {
    viewState.current[key] = { ...(viewState.current[key] || {}), ...patch }
  }, [])

  // An org bubble's summary in the coder summary's shape (the dock reads
  // status, unread and cost), plus how many items wait for the person.
  const orgSummaryOf = (key) => {
    const sum = orgSummaries[key]
    if (!sum) return { ...emptySummary(), needs: 0 }
    const cost = sum.activity.usage.costUsd
    return {
      ...emptySummary(), status: orgBubbleStatus(sum), unread: sum.unread,
      costByTurn: cost ? { [sum.activity.run || 'run']: cost } : {}, needs: orgNeedsCount(sum),
    }
  }

  return {
    bubbles, side, expandedKey, summaries,
    summaryOf: key => (key.startsWith('org:') ? orgSummaryOf(key) : summaries[key] || emptySummary()),
    expand, collapse, openDraft, openConversation, openOrg, bindConversation, close, reorder, setSide,
    getView, setView,
    // bossOf is the org's boss once its design was read ('' before).
    bossOf: name => bosses.current[name] || '',
  }
}
