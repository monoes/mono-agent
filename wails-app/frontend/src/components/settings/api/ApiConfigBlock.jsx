import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight, Loader2, Settings2 } from 'lucide-react'
import { APIConfigReset, APIConfigSet, APIConfigUnset } from '../../../wailsjs/go/main/App'
import { describeConfigError } from './configError.js'
import { ROWS, byKey, otherProblems, savePayload, summary } from './configModel.js'
import ApiConfigRestart from './ApiConfigRestart.jsx'
import ApiConfigRow from './ApiConfigRow.jsx'
import ApiWideningDialog from './ApiWideningDialog.jsx'
import useRestart from './useRestart.js'
import { Badge, Said, block, errText, hint, label, okText } from './ui.jsx'

// The server's settings (internal/apiconfig): each setting with what is saved, what the running daemon started with,
// where that came from and where it stands. Folded, like the section's other parts: it is for the one who runs the
// server. Everything comes from `api config show --json` and changes through `api config set|unset` (Go side:
// app_api_config.go); this block shows it and asks the CLI, and never judges a value, a widening change or a state itself.
//
// A change is made in two calls. The first is a dry run: it says what the change would do and whether it makes the
// server reach further (the CLI's answer, with its reasons), and writes nothing. If it does not, the second makes it,
// unconfirmed, so that a change that began to widen meanwhile is refused and not slipped through. If it does, the
// dialog lists the reasons, and only its confirmation makes the second call, confirmed. What the section is handed
// (onAdopt) is the document of a change that was made, never of a dry run, which describes a state that does not exist.
//
// A saved row the CLI cannot read is an error of every read (exit 3, a message that starts "the saved settings are damaged":
// configError.js), and there is nothing to show or change until it is removed. The block then says what the CLI said and
// offers the one way out the CLI names, "Reset the saved settings" (`api config unset --all`), as a change that widens: a
// dry run first (it says whether there is such a row, and why removing it needs a yes), the dialog, and only then the
// call with --yes. A row in a newer format is not damage and is never offered a reset.

const rowsBox = { listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 10 }

const call = (kind, payload, confirm, dryRun) => (kind === 'set' ? APIConfigSet(payload, confirm, dryRun) : APIConfigUnset(payload, confirm, dryRun))

/**
 * @param {object|null} config `api config show --json` (or the document of the last change), null while it loads.
 * @param {{text: string, verbatim: boolean, damaged?: boolean}|null} err Why the settings could not be read (`damaged`: the
 *   saved row cannot be read). What was on screen stays.
 * @param {() => void} onRetry Reads the settings again.
 * @param {(doc: object) => void} onAdopt Takes the document of a change that was made, and only that.
 * @param {() => Promise<object|null>} onReload Reads the settings again: the document, or null when the read failed or a
 *   newer one took its place. It is what the re-reads after a restart go through.
 * @param {() => void} [onApplied] The daemon came back from a restart, or the saved settings were reset: what else the section
 *   shows may have changed (the status and the models failed to be read for the same row).
 */
export default function ApiConfigBlock({ config, err, onRetry, onAdopt, onReload, onApplied }) {
  const { t } = useTranslation()
  const tRef = useRef(t) // a failure is worded when it happens, in the language of the moment
  tRef.current = t
  const bodyId = useId()
  const errId = `${bodyId}-error`
  const box = useRef(null)
  const [open, setOpen] = useState(false)
  const [drafts, setDrafts] = useState({}) // key → what was typed
  const [busy, setBusy] = useState(null) // the change a call is running for: {id, kind}
  const [errs, setErrs] = useState({}) // row id → what the last call for it said when it failed
  const [notes, setNotes] = useState({}) // row id → what the last call for it did
  const [pending, setPending] = useState(null) // a change that widens, waiting for the dialog's answer
  const [resetAsk, setResetAsk] = useState(null) // a reset of the saved settings, waiting for the dialog's answer: {widening}
  const [resetFail, setResetFail] = useState(null) // what the last reset said when it failed, and the failure of reading it came after: {for, said}
  const [resetDone, setResetDone] = useState(null) // the document a reset gave: {for}
  const running = useRef(false) // a call is running: a second one cannot start, not even in the same tick
  const focusAfter = useRef('') // the row (or 'reset', 'reset-done') whose control gets the keyboard back when a call is over
  const errNow = useRef(err) // the failure of reading that is on show, for a call that ends after another read
  errNow.current = err
  const restart = useRestart({ config, onReload, onApplied })
  const settings = byKey(config)
  const sum = summary(config)
  const problems = otherProblems(config)
  // What a reset said when it failed belongs to the failure of reading it was made against: a read since replaces that.
  const resetErr = resetFail && resetFail.for === err ? resetFail.said : null
  // Nothing can be edited or started while a call runs: one for a setting, or the daemon's restart.
  const working = busy || (restart.phase === 'restarting' ? { id: '', kind: 'restart' } : null)

  // A control that was disabled while a call ran has lost the focus: it goes back to the row that was being used.
  useEffect(() => {
    if (busy || pending || !focusAfter.current) return
    const id = focusAfter.current
    focusAfter.current = ''
    box.current?.querySelector(`[data-focus-row="${id}"]`)?.focus()
  })

  const say = (id, said) => setErrs(e => ({ ...e, [id]: said }))
  const remark = (id, n) => setNotes(x => ({ ...x, [id]: n }))
  // A change that failed says why next to its setting. When it found the saved row damaged (it was read fine, and was
  // damaged since), the settings are read again too, which is what brings the way out of the damage.
  const failed = (id, e) => {
    const said = describeConfigError(e, tRef.current)
    say(id, said)
    if (said.damaged) onRetry()
  }
  const edit = (row, key, text) => { setDrafts(d => ({ ...d, [key]: text })); say(row.id, null); remark(row.id, null) }

  // The change itself: the state it gave is adopted, what was typed for the row is dropped (the row shows what is saved,
  // in the spelling the CLI stored it in), and a way of reaching further that the dialog did not list is said.
  async function apply(row, kind, payload, confirm, shown) {
    const doc = await call(kind, payload, confirm, false)
    onAdopt?.(doc)
    setDrafts(d => { const next = { ...d }; for (const k of row.keys) delete next[k]; return next })
    const unlisted = confirm ? (doc.widening || []).filter(x => !shown.some(s => s.key === x.key && s.reason === x.reason)) : []
    remark(row.id, { kind: doc.changed?.length ? (kind === 'set' ? 'saved' : 'removed') : 'unchanged', extra: unlisted })
  }

  async function start(row, kind) {
    if (running.current) return
    running.current = true
    const payload = kind === 'set' ? savePayload(row, drafts, settings) : row.keys
    setBusy({ id: row.id, kind }); say(row.id, null); remark(row.id, null)
    let waiting = false
    try {
      const preview = await call(kind, payload, false, true) // what would it do, and does it widen?
      if (preview.widening?.length) { waiting = true; setPending({ row, kind, payload, widening: preview.widening }); return }
      await apply(row, kind, payload, false, [])
    } catch (e) {
      failed(row.id, e)
    } finally {
      running.current = false
      setBusy(null)
      if (!waiting) focusAfter.current = row.id
    }
  }

  async function confirmPending() {
    const p = pending
    if (!p || running.current) return
    running.current = true
    setPending(null); setBusy({ id: p.row.id, kind: p.kind })
    try {
      await apply(p.row, p.kind, p.payload, true, p.widening)
    } catch (e) {
      failed(p.row.id, e)
    } finally {
      running.current = false
      setBusy(null)
      focusAfter.current = p.row.id
    }
  }

  function cancelPending() {
    if (pending) focusAfter.current = pending.row.id
    setPending(null)
  }

  // Resetting the saved settings is two calls, like a change that widens. The dry run says whether a row that cannot be
  // read would be removed: when it does not, someone fixed the row meanwhile, and removing the saved settings would take
  // what was fixed with it, so nothing is asked and the settings are read again. Otherwise the dialog lists the CLI's
  // reason, and only its yes makes the call with --yes. What that gave is the document on show (the section drops a read
  // that began before it), what was typed is dropped with the settings, and the status and the models are read again.
  async function startReset() {
    if (running.current) return
    running.current = true
    setBusy({ id: '', kind: 'reset' }); setResetFail(null)
    let waiting = false
    try {
      const preview = await APIConfigReset(false, true)
      if (!preview.removed_unreadable_row) { onRetry(); return }
      waiting = true
      setResetAsk({ widening: preview.widening })
    } catch (e) {
      setResetFail({ for: errNow.current, said: describeConfigError(e, tRef.current) })
    } finally {
      running.current = false
      setBusy(null)
      if (!waiting) focusAfter.current = 'reset'
    }
  }

  async function confirmReset() {
    if (!resetAsk || running.current) return
    // The settings were read while the dialog was open and the row is not damaged now (it was fixed, or the read failed some
    // other way): the yes was for a row that cannot be read, and --yes would remove what can be. The CLI has no form of the
    // call that removes the row only if it still cannot be read, so a row fixed after this point is removed with the rest.
    if (!errNow.current?.damaged) { setResetAsk(null); return }
    running.current = true
    setResetAsk(null); setBusy({ id: '', kind: 'reset' })
    try {
      const doc = await APIConfigReset(true, false)
      onAdopt?.(doc)
      setDrafts({}); setErrs({}); setNotes({})
      setResetDone({ for: doc })
      focusAfter.current = 'reset-done' // the button it was pressed on is gone
      onApplied?.()
    } catch (e) {
      setResetFail({ for: errNow.current, said: describeConfigError(e, tRef.current) })
      focusAfter.current = 'reset'
    } finally {
      running.current = false
      setBusy(null)
    }
  }

  function cancelReset() {
    focusAfter.current = 'reset'
    setResetAsk(null)
  }

  return (
    <div data-testid="api-config-block" style={block}>
      <button
        type="button" data-testid="api-config-toggle" aria-expanded={open} aria-controls={bodyId} onClick={() => setOpen(v => !v)}
        style={{
          width: '100%', textAlign: 'left', font: 'inherit', color: 'inherit', background: 'transparent', border: 'none', padding: 0,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, cursor: 'pointer', userSelect: 'none',
        }}
      >
        <span style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
          <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <Settings2 size={13} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
            <span style={label}>{t('settings.api.config.title')}</span>
          </span>
          {!config && !err && <Badge data-testid="api-config-chip-loading" tone="info"><Loader2 size={11} className="spin" /> {t('settings.api.loading')}</Badge>}
          {err && (err.damaged
            ? <Badge data-testid="api-config-chip-damaged" tone="bad">{t('settings.api.config.chipDamaged')}</Badge>
            : <Badge data-testid="api-config-chip-error" tone="bad">{t('settings.api.config.chipError')}</Badge>)}
          {config && sum.restart && <Badge data-testid="api-config-chip-restart" tone="warn">{t('settings.api.config.chipRestart')}</Badge>}
          {config && sum.problems > 0 && <Badge data-testid="api-config-chip-problems" tone="bad">{t('settings.api.config.chipProblems', { count: sum.problems })}</Badge>}
          {config && sum.saved > 0 && <Badge data-testid="api-config-chip-saved" tone="muted">{t('settings.api.config.chipSaved', { count: sum.saved })}</Badge>}
        </span>
        {open ? <ChevronDown size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} /> : <ChevronRight size={14} style={{ color: 'var(--text-muted)', flexShrink: 0 }} />}
      </button>

      {open && (
        <div id={bodyId} ref={box} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div style={hint}>{t('settings.api.config.hint')}</div>

          {err && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                <div id={errId} data-testid="api-config-load-error" style={{ ...errText, flex: '1 1 260px' }}>
                  {t('settings.api.config.loadError')} <Said said={err} />
                </div>
                <button type="button" className="btn btn-secondary btn-sm" disabled={!!working} onClick={onRetry}>{t('settings.api.retry')}</button>
                {err.damaged && (
                  <button
                    type="button" className="btn btn-primary btn-sm" disabled={!!working} aria-describedby={errId}
                    data-focus-row="reset" onClick={startReset}
                  >
                    {busy?.kind === 'reset' ? t('settings.api.config.reset.working') : t('settings.api.config.reset.button')}
                  </button>
                )}
              </div>
              {resetErr && <div role="alert" style={errText}><Said said={resetErr} /></div>}
            </div>
          )}
          {!config && !err && <div style={hint}>{t('settings.api.config.loading')}</div>}

          {config && (
            <>
              {resetDone?.for === config && (
                <div data-testid="api-config-reset-done" data-focus-row="reset-done" tabIndex={-1} role="status" style={okText}>
                  {t('settings.api.config.reset.done')}
                </div>
              )}
              <ApiConfigRestart config={config} restart={restart} disabled={!!busy} />
              {problems.length > 0 && (
                <div data-testid="api-config-problems" style={{ ...errText, display: 'flex', flexDirection: 'column', gap: 4 }}>
                  {problems.map((p, i) => <div key={i}><Said said={describeConfigError(`invalid_input: ${p.message}`, t)} /></div>)}
                </div>
              )}
              <ul aria-label={t('settings.api.config.title')} style={rowsBox}>
                {ROWS.map(row => (
                  <ApiConfigRow
                    key={row.id} row={row} doc={config} settings={settings} drafts={drafts} busy={working}
                    err={errs[row.id]} note={notes[row.id]}
                    onChange={(key, text) => edit(row, key, text)} onSave={() => start(row, 'set')} onUseDefault={() => start(row, 'unset')}
                  />
                ))}
              </ul>
            </>
          )}
        </div>
      )}

      {pending && <ApiWideningDialog widening={pending.widening} kind={pending.kind} onCancel={cancelPending} onConfirm={confirmPending} />}
      {resetAsk && <ApiWideningDialog widening={resetAsk.widening} kind="reset" onCancel={cancelReset} onConfirm={confirmReset} />}
    </div>
  )
}
