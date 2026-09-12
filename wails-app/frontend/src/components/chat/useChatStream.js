import { useEffect, useReducer, useRef } from 'react'
import { api, onChatEvent } from '../../services/api.js'
import { chatReducer, initialChatState } from './chatReducer.js'

const PAGE_SIZE = 200

// useChatStream observes one turn's chat:event stream: hydrate the durable
// backlog, subscribe live, merge the two without gaps or duplicates, and
// hand the result through chatReducer. Purely observational — starting or
// stopping a turn is the caller's job (api.startChatTurn/stopChatTurn); this
// hook only ever reads.
//
// Subscribe-before-fetch, buffered hydration, gap catch-up and a
// generation-counter stale-request guard all live here so chatReducer stays
// a plain, synchronous, fully unit-testable function.
export function useChatStream({ conversationId, turnId }) {
  const [state, dispatch] = useReducer(chatReducer, initialChatState())

  useEffect(() => {
    if (!conversationId || !turnId) {
      dispatch({ type: 'reset' })
      return
    }
    dispatch({ type: 'scope', scope: { conversationId, turnId } })

    let cancelled = false
    let hydrating = true
    let localLastSeq = 0
    let pendingLive = []

    function belongsHere(ev) {
      return ev.conversationId === conversationId && ev.turnId === turnId
    }

    function dispatchEvent(ev) {
      if (typeof ev.seq === 'number' && ev.seq > localLastSeq) localLastSeq = ev.seq
      dispatch({ type: 'event', event: ev })
    }

    // Fetches [afterSeq+1 .. beforeSeq-1] before applying the event that
    // jumped ahead — "fetch missing gaps; never infer sequence continuity
    // from the highest event seen."
    async function fillGap(beforeSeq) {
      for (;;) {
        const startSeq = localLastSeq
        const res = await api.getChatEvents(conversationId, turnId, startSeq, PAGE_SIZE)
        if (cancelled) return
        const items = Array.isArray(res?.items) ? res.items : []
        for (const e of items) dispatchEvent(e)
        if (items.length === 0) return
        if (localLastSeq >= beforeSeq - 1 || !res?.hasMore) return
        if (localLastSeq === startSeq) return // no forward progress — avoid looping forever
      }
    }

    async function dispatchLive(ev) {
      if (typeof ev.seq === 'number' && ev.seq > localLastSeq + 1) {
        await fillGap(ev.seq)
        if (cancelled) return
      }
      dispatchEvent(ev)
    }

    const unsubscribe = onChatEvent((raw) => {
      if (cancelled || !belongsHere(raw)) return
      if (hydrating) {
        pendingLive.push(raw)
        return
      }
      dispatchLive(raw)
    })

    async function hydrate() {
      let afterSeq = 0
      for (;;) {
        const res = await api.getChatEvents(conversationId, turnId, afterSeq, PAGE_SIZE)
        if (cancelled) return
        const items = Array.isArray(res?.items) ? res.items : []
        for (const e of items) dispatchEvent(e)
        if (items.length === 0) break
        afterSeq = localLastSeq
        if (!res?.hasMore) break
      }
      if (cancelled) return
      hydrating = false
      const buffered = pendingLive.slice().sort((a, b) => a.seq - b.seq)
      pendingLive = []
      for (const e of buffered) {
        if (cancelled) return
        // eslint-disable-next-line no-await-in-loop
        await dispatchLive(e)
      }
    }

    hydrate()

    return () => {
      cancelled = true
      unsubscribe()
    }
  }, [conversationId, turnId])

  return state
}
