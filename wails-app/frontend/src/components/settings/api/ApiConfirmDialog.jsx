import { useEffect, useId, useRef } from 'react'
import { useDialog } from '../../../pages/connections/ui.jsx'

// A question to answer before something that reaches further, or interrupts what is running, is done. The app's own
// confirm() starts with the focus on its confirm button and takes an Enter anywhere for a yes; this one starts with
// the focus on Cancel, which comes first, and nothing but a click on the other button (or Space or Enter while it is
// focused, which is what a button does) confirms. Escape, Cancel and a click on the page behind cancel. Tab stays inside.

/**
 * @param {string} title
 * @param {React.ReactNode} children What the change does, in the dialog's body.
 * @param {string} cancelLabel
 * @param {string} confirmLabel What the other button does, in words.
 * @param {() => void} onCancel
 * @param {() => void} onConfirm
 */
export default function ApiConfirmDialog({ title, children, cancelLabel, confirmLabel, onCancel, onConfirm }) {
  const ids = useId()
  const titleId = `${ids}-title`, bodyId = `${ids}-body`
  const cancelRef = useRef(null)
  const dialog = useDialog(onCancel) // focus in on open and back to the opener on close, Escape, and Tab kept inside
  useEffect(() => { cancelRef.current?.focus() }, []) // Cancel is where it starts, whatever the body holds

  return (
    <div className="modal-overlay" onClick={e => { if (e.target === e.currentTarget) onCancel() }}>
      <div {...dialog} role="alertdialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={bodyId} className="modal" style={{ width: 480 }}>
        <div id={titleId} className="modal-title">{title}</div>
        <div id={bodyId} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>{children}</div>
        <div className="modal-actions">
          <button ref={cancelRef} type="button" className="btn btn-secondary" onClick={onCancel}>{cancelLabel}</button>
          <button type="button" className="btn btn-danger" onClick={onConfirm}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}
