import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { DaemonRestart } from '../../../wailsjs/go/main/App'
import { classify } from './apiError.js'
import { describeConfigError } from './configError.js'
import { restartSettled } from './configModel.js'

// Restarting the daemon so that it reads the saved settings (`daemon restart`, through the service manager it is
// registered with). When the call returns the service manager has accepted the restart, but the daemon takes a moment
// to start again and report what it runs, so the settings are read again after a moment and then a few times, until it
// does (it runs, it reports its settings, and none is pending) or the last read has gone by. Reading at once would
// only find the daemon that was stopped. The reads are the caller's (they go through the section's guard, so an
// older answer never overwrites a newer one), and they stop when the component goes away.

export const RESTART_REREAD_MS = [1000, 2000, 3000, 5000, 5000]

/**
 * @param {() => Promise<object|null>} onReload Reads the settings again: the document, or null when the read failed or a
 *   newer one took its place.
 * @param {() => void} [onApplied] Called when the daemon is back and runs what is saved.
 * @returns {{phase: 'idle'|'confirming'|'restarting'|'checking'|'back'|'late', err: null|{text: string, verbatim: boolean},
 *   fallback: boolean, ask: () => void, cancel: () => void, confirm: () => Promise<void>}} `fallback`: the CLI said the
 *   daemon is not registered, so the commands to run are what is left.
 */
export default function useRestart({ onReload, onApplied }) {
  const { t } = useTranslation()
  const tRef = useRef(t)
  tRef.current = t
  const latest = useRef({ onReload, onApplied })
  latest.current = { onReload, onApplied }
  const [phase, setPhase] = useState('idle')
  const [err, setErr] = useState(null)
  const [fallback, setFallback] = useState(false)
  const alive = useRef(true)
  const timer = useRef(null)
  const running = useRef(false)

  // `alive` is set again on every mount: React's strict mode runs an effect and its cleanup once before the real mount.
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false; clearTimeout(timer.current) }
  }, [])

  const ask = useCallback(() => { setErr(null); setPhase('confirming') }, [])
  const cancel = useCallback(() => setPhase('idle'), [])

  const confirm = useCallback(async () => {
    if (running.current) return
    running.current = true
    setPhase('restarting'); setErr(null); setFallback(false)
    try {
      const res = await DaemonRestart()
      if (!alive.current) return
      if (!res?.restarted) {
        setErr({ text: tRef.current('settings.api.config.restart.notDone'), verbatim: false })
        setPhase('idle')
        return
      }
      setPhase('checking')
      for (const ms of RESTART_REREAD_MS) {
        await new Promise(resolve => { timer.current = setTimeout(resolve, ms) })
        if (!alive.current) return
        const doc = await latest.current.onReload()
        if (!alive.current) return
        if (restartSettled(doc)) {
          setPhase('back')
          latest.current.onApplied?.()
          return
        }
      }
      setPhase('late')
    } catch (e) {
      if (!alive.current) return
      setErr(describeConfigError(e, tRef.current))
      setFallback(classify(e).cls === 'invalid_input') // the CLI refused: nothing is registered to restart
      setPhase('idle')
      latest.current.onReload() // what the page last read may be out of date: the registration may be gone
    } finally {
      running.current = false
    }
  }, [])

  return { phase, err, fallback, ask, cancel, confirm }
}
