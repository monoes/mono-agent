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
//
// What an attempt came to (it did not happen, the CLI refused, the daemon did not report back in time) is kept with the
// document the page had when it came to it, and says nothing about a newer one: the person may have registered the
// daemon since, or saved something else, or pressed Refresh and found it back.

export const RESTART_REREAD_MS = [1000, 2000, 3000, 5000, 5000]

/**
 * @param {object|null} config The document the page shows now.
 * @param {() => Promise<object|null>} onReload Reads the settings again: the document, or null when the read failed or a
 *   newer one took its place.
 * @param {() => void} [onApplied] Called when the daemon is back and runs what is saved.
 * @returns {{phase: 'idle'|'confirming'|'restarting'|'checking'|'back',
 *   outcome: null|{late?: boolean, err?: {text: string, verbatim: boolean}, fallback?: boolean, doc: object|null},
 *   ask: () => void, cancel: () => void, confirm: () => Promise<void>}} `outcome.fallback`: the CLI said the daemon is not
 *   registered, so the commands to run are what is left; `outcome.late`: the last read had gone by and the daemon had not
 *   reported back; `outcome.doc` is the document it belongs to.
 */
export default function useRestart({ config, onReload, onApplied }) {
  const { t } = useTranslation()
  const tRef = useRef(t)
  tRef.current = t
  const latest = useRef({ config, onReload, onApplied })
  latest.current = { config, onReload, onApplied }
  const [phase, setPhase] = useState('idle')
  const [outcome, setOutcome] = useState(null)
  const alive = useRef(true)
  const timer = useRef(null)
  const running = useRef(false)

  // `alive` is set again on every mount: React's strict mode runs an effect and its cleanup once before the real mount.
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false; clearTimeout(timer.current) }
  }, [])

  const ask = useCallback(() => { setOutcome(null); setPhase('confirming') }, [])
  const cancel = useCallback(() => setPhase('idle'), [])

  const confirm = useCallback(async () => {
    if (running.current) return
    running.current = true
    setPhase('restarting'); setOutcome(null)
    try {
      const res = await DaemonRestart()
      if (!alive.current) return
      if (!res?.restarted) {
        setOutcome({ err: { text: tRef.current('settings.api.config.restart.notDone'), verbatim: false }, doc: latest.current.config })
        setPhase('idle')
        return
      }
      setPhase('checking')
      let seen = latest.current.config // the last document that was read, or the one the page had
      for (const ms of RESTART_REREAD_MS) {
        await new Promise(resolve => { timer.current = setTimeout(resolve, ms) })
        if (!alive.current) return
        const doc = await latest.current.onReload()
        if (!alive.current) return
        if (doc) seen = doc
        if (restartSettled(doc)) {
          setPhase('back')
          latest.current.onApplied?.()
          return
        }
      }
      setOutcome({ late: true, doc: seen })
      setPhase('idle')
    } catch (e) {
      if (!alive.current) return
      // What the page last read may be out of date, and the failed call may have been done in part: read again, and
      // then say what came of it (the document it belongs to is the one that read gave, if it gave one).
      const err = describeConfigError(e, tRef.current)
      const fallback = classify(e).cls === 'invalid_input' // the CLI refused: nothing is registered to restart
      const doc = await latest.current.onReload()
      if (!alive.current) return
      setOutcome({ err, fallback, doc: doc || latest.current.config })
      setPhase('idle')
    } finally {
      running.current = false
    }
  }, [])

  return { phase, outcome, ask, cancel, confirm }
}
