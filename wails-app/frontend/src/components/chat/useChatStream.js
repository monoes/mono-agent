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
      // fillGap has no upper bound on what it fetches, so the same event
      // can legitimately reach this function twice — once via a gap-fill
      // page that happens to already include it, once via the live
      // delivery that triggered that gap-fill (or any other redundant
      // re-delivery). The reducer's own 'event' case already no-ops a
      // seq it has seen before; alreadyApplied lets the side effect below
      // share that exact same notion of "did this call actually advance
      // anything," instead of firing once per dispatchEvent call.
      const alreadyApplied = typeof ev.seq === 'number' && ev.seq <= localLastSeq
      if (typeof ev.seq === 'number' && ev.seq > localLastSeq) localLastSeq = ev.seq
      dispatch({ type: 'event', event: ev })
      // historySaved:false does NOT mean this hook's own transcript is
      // wrong — everything applied above is the authoritative live record.
      // It means the store's durable write fell behind, so a *future*
      // reopen of this conversation (loadConversation -> getChatTurns) may
      // not show everything just shown here. Nothing to reconcile against
      // (a refetch now could only return LESS than what's already applied)
      // — the only correct response is telling the user, via the same
      // notice banner real backend 'notice' events already render.
      if (!alreadyApplied && ev.type === 'turn.finished' && ev.payload?.historySaved === false) {
        dispatch({
          type: 'localNotice',
          notice: {
            code: 'history_not_saved',
            message: 'This turn finished, but its history may not have saved — reopening this conversation later might not show everything.',
            severity: 'warning',
          },
        })
      }
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
