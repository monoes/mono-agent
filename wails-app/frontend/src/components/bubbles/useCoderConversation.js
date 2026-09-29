import { useCallback, useEffect, useRef, useState } from 'react'
import { api, notify } from '../../services/api.js'
import { useChatStream, loadTurnState } from '../chat/useChatStream.js'
import { newTurnId } from '../AIChatPanel.jsx'

// useCoderConversation is one expanded coder bubble's conversation (#227):
// it loads the turns from the journal (`chat history`), re-attaches to a
// turn that is still running (a chat keeps working while its bubble is
// collapsed, so expanding it must pick the live turn back up), and sends
// and stops turns. Turns run in the CLI; this only starts, stops and reads
// them.
//
// create() is called for the first message of a draft: it creates the
// coder conversation and returns it ({ id, cwd, model }).
export function useCoderConversation({ conversationId, create }) {
  const [messages, setMessages] = useState([])
  const [activeTurnId, setActiveTurnId] = useState('')
  const [convId, setConvId] = useState(conversationId || '')
  const [loading, setLoading] = useState(!!conversationId)
  const [loadError, setLoadError] = useState('')
  const [stopRequested, setStopRequested] = useState(false)
  const liveTurn = useChatStream({ conversationId: convId, turnId: activeTurnId })
  const activeRef = useRef('')
  activeRef.current = activeTurnId

  useEffect(() => { if (conversationId) setConvId(conversationId) }, [conversationId])

  // Load the transcript once per conversation (re-expanding a bubble mounts
  // this again and reloads from the journal).
  const loadedFor = useRef('')
  useEffect(() => {
    if (!conversationId || loadedFor.current === conversationId) return
    loadedFor.current = conversationId
    let cancelled = false
    setLoading(true)
    setLoadError('')
    Promise.resolve().then(() => api.getChatTurns(conversationId, '', 50)).then(async (res) => {
      const turns = (Array.isArray(res?.items) ? res.items : []).slice().reverse() // oldest first
      const built = []
      let running = ''
      for (const turn of turns) {
        if (cancelled) return
        built.push({ role: 'user', content: turn.prompt })
        if (turn.status === 'active' && turn.ownedByThisInstance !== false) {
          // Still running: the live stream (useChatStream) shows it.
          running = turn.id
          continue
        }
        // eslint-disable-next-line no-await-in-loop
        const state = await loadTurnState(conversationId, turn)
        if (state) built.push({ role: 'turn', turnId: turn.id, state, ownedByThisInstance: turn.ownedByThisInstance !== false })
      }
      if (cancelled) return
      setMessages(built)
      setActiveTurnId(running)
    }).catch(err => {
      if (!cancelled) setLoadError(String(err?.message || err))
    }).finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true; loadedFor.current = '' }
  }, [conversationId])

  // A finished live turn joins the transcript.
  useEffect(() => {
    if (!activeTurnId || !liveTurn.terminal) return
    setMessages(msgs => [...msgs, { role: 'turn', turnId: activeTurnId, state: liveTurn }])
    setActiveTurnId('')
    setStopRequested(false)
  }, [activeTurnId, liveTurn])

  const send = useCallback(async (text) => {
    const prompt = String(text || '').trim()
    if (!prompt || activeRef.current) return false
    setMessages(msgs => [...msgs, { role: 'user', content: prompt }])
    setStopRequested(false)
    try {
      let id = convId
      if (!id) {
        const conv = await create()
        id = conv.id
        loadedFor.current = id // nothing to load: this chat starts here
        setConvId(id)
        if (conv.workspace) setMessages(msgs => [...msgs.slice(0, -1), { role: 'coder-init', workspace: conv.workspace }, ...msgs.slice(-1)])
      }
      const turnId = newTurnId()
      // Coder turns never get monoagent tools: the CLI refuses --tools for
      // a coder conversation.
      const res = await api.startChatTurn(id, turnId, prompt, false, false)
      if (res?.ok === false) {
        setMessages(msgs => [...msgs, { role: 'error', content: `Could not start: ${res.status}` }])
        return false
      }
      setActiveTurnId(turnId)
      return true
    } catch (err) {
      setMessages(msgs => [...msgs, { role: 'error', content: String(err?.message || err), code: err?.code || '' }])
      return false
    }
  }, [convId, create])

  const stop = useCallback(async () => {
    if (!convId || !activeRef.current) return
    setStopRequested(true)
    try {
      await api.stopChatTurn(convId, activeRef.current)
    } catch (err) {
      setStopRequested(false)
      notify('chat', `Could not stop: ${err}`)
    }
  }, [convId])

  return { messages, liveTurn, activeTurnId, streaming: !!activeTurnId, stopRequested, loading, loadError, send, stop, conversationId: convId }
}
