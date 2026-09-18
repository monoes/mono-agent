import { useRef } from 'react'
import { Send, Square } from 'lucide-react'

// ChatComposer is the draft textarea + send/stop control, extracted from
// AIChatPanel so its own interaction rules (IME composition, auto-resize)
// are independently testable. Fully controlled — the draft itself lives in
// the parent's `value` state, so nothing here is lost across a presentation
// change (docked/expanded/narrow) as long as the parent doesn't unmount.
export function ChatComposer({ value, onChange, onSend, onStop, streaming, disabled, disabledReason }) {
  const textareaRef = useRef(null)
  // Enter must never submit while an IME composition (CJK/etc candidate
  // selection) is in progress — isComposing is the standard signal, but a
  // few browsers still fire one stray keydown with keyCode 229 right as
  // composition ends, before isComposing has flipped back to false, so
  // both are checked.
  const composingRef = useRef(false)

  const handleKeyDown = (e) => {
    if (e.key !== 'Enter' || e.shiftKey) return
    if (composingRef.current || e.nativeEvent?.isComposing || e.keyCode === 229) return
    e.preventDefault()
    onSend()
  }

  return (
    <div style={{
      padding: '8px 12px 10px',
      borderTop: '1px solid rgba(0,180,216,0.1)',
      display: 'flex', flexDirection: 'column', gap: 6,
      flexShrink: 0,
    }}>
      {disabled && disabledReason && (
        <div style={{ padding: '8px 12px', background: 'rgba(251,191,36,.08)', border: '1px solid rgba(251,191,36,.2)', borderRadius: 'var(--radius)', fontFamily: 'var(--font-mono)', fontSize: 10, color: '#fbbf24' }}>
          {disabledReason}
        </div>
      )}
      <div style={{ display: 'flex', gap: 6 }}>
        <textarea
          ref={textareaRef}
          value={value}
          onChange={e => onChange(e.target.value)}
          onKeyDown={handleKeyDown}
          onCompositionStart={() => { composingRef.current = true }}
          onCompositionEnd={() => { composingRef.current = false }}
          // Typing is blocked too, not just Send: with no usable backend
          // there is nothing a draft can be sent to, and a composer that
          // accepts text while refusing to send it reads as a broken app
          // rather than an unavailable one.
          disabled={disabled}
          placeholder="Type a message..."
          rows={1}
          style={{
            flex: 1,
            background: '#020509',
            border: '1px solid rgba(0,180,216,0.15)',
            borderRadius: 8,
            padding: '8px 10px',
            color: '#e2e8f0',
            fontFamily: 'var(--font-mono)', fontSize: 11,
            outline: 'none',
            resize: 'none',
            minHeight: 36,
            maxHeight: 120,
            lineHeight: 1.4,
          }}
          onInput={e => {
            e.target.style.height = 'auto'
            e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px'
          }}
        />
        {streaming ? (
          <button
            onClick={onStop}
            title="Stop generating"
            aria-label="Stop generating"
            style={{
              background: 'rgba(239,68,68,0.15)',
              border: '1px solid rgba(239,68,68,0.35)',
              borderRadius: 8,
              padding: '0 12px',
              cursor: 'pointer',
              color: '#ef4444',
              display: 'flex', alignItems: 'center',
              transition: 'all 100ms',
              flexShrink: 0,
            }}
            onMouseEnter={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.25)' }}
            onMouseLeave={e => { e.currentTarget.style.background = 'rgba(239,68,68,0.15)' }}
          >
            <Square size={12} fill="#ef4444" />
          </button>
        ) : (
          <button
            onClick={onSend}
            disabled={!value.trim() || disabled}
            title={disabled ? 'No backend selected — see the notice above' : 'Send message'}
            aria-label="Send message"
            style={{
              background: !value.trim() || disabled ? 'rgba(0,180,216,0.05)' : 'rgba(0,180,216,0.15)',
              border: `1px solid ${!value.trim() || disabled ? 'rgba(0,180,216,0.08)' : 'rgba(0,180,216,0.3)'}`,
              borderRadius: 8,
              padding: '0 12px',
              cursor: !value.trim() || disabled ? 'default' : 'pointer',
              color: !value.trim() || disabled ? 'var(--text-muted)' : '#00b4d8',
              display: 'flex', alignItems: 'center',
              transition: 'all 100ms',
              flexShrink: 0,
            }}
            onMouseEnter={e => { if (value.trim() && !disabled) e.currentTarget.style.background = 'rgba(0,180,216,0.25)' }}
            onMouseLeave={e => { if (value.trim() && !disabled) e.currentTarget.style.background = 'rgba(0,180,216,0.15)' }}
          >
            <Send size={13} />
          </button>
        )}
      </div>
    </div>
  )
}
