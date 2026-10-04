import { useTranslation } from 'react-i18next'

// Shared look of the API settings section: the same tokens, chips and text
// styles as the Jev and Coder mode sections.

export const mono = 'var(--font-mono)'
export const label = { fontFamily: mono, fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1 }
export const hint = { fontFamily: 'var(--font-body)', fontSize: 10.5, color: 'var(--text-muted)', lineHeight: 1.5 }
export const errText = { fontFamily: mono, fontSize: 10.5, color: 'var(--red)', lineHeight: 1.5, wordBreak: 'break-word' }
export const okText = { fontFamily: mono, fontSize: 10.5, color: 'var(--green-neon)', lineHeight: 1.5, wordBreak: 'break-word' }
export const block = { display: 'flex', flexDirection: 'column', gap: 10, borderTop: '1px solid var(--border-dim)', paddingTop: 14 }

const TONES = {
  ok: { color: 'var(--green-neon)', bg: 'rgba(16,185,129,.1)', bd: 'rgba(74,222,128,.25)' },
  info: { color: 'var(--cyan)', bg: 'rgba(0,180,216,.1)', bd: 'rgba(0,180,216,.25)' },
  warn: { color: 'var(--yellow)', bg: 'rgba(234,179,8,.08)', bd: 'rgba(234,179,8,.22)' },
  hot: { color: 'var(--orange)', bg: 'rgba(249,115,22,.08)', bd: 'rgba(249,115,22,.25)' },
  bad: { color: 'var(--red)', bg: 'rgba(239,68,68,.08)', bd: 'rgba(239,68,68,.25)' },
  muted: { color: 'var(--text-muted)', bg: 'rgba(255,255,255,.05)', bd: 'var(--border)' },
}

// Badge is the rounded chip of the Jev section. The text carries the meaning;
// the tone only backs it up.
export function Badge({ tone = 'muted', title, style, children, ...rest }) {
  const c = TONES[tone] || TONES.muted
  return (
    <span title={title} {...rest} style={{
      display: 'inline-flex', alignItems: 'center', gap: 5, fontFamily: mono, fontSize: 10.5, whiteSpace: 'nowrap',
      color: c.color, background: c.bg, border: `1px solid ${c.bd}`, borderRadius: 99, padding: '2px 9px', ...style,
    }}>{children}</span>
  )
}

// A runtime's class is chat-only, sandboxed or unconfined; a policy's strongest
// class is chat-only, sandboxed or any. The words are the ones of the flags and
// of `api models`, so they are not translated; the title says what they mean.
const CLASS_TONE = { 'chat-only': 'ok', sandboxed: 'warn', unconfined: 'hot', any: 'hot' }
const CLASS_HINT = {
  'chat-only': 'settings.api.classHint.chatOnly',
  sandboxed: 'settings.api.classHint.sandboxed',
  unconfined: 'settings.api.classHint.unconfined',
  any: 'settings.api.classHint.any',
}

export function ClassBadge({ value, children, ...rest }) {
  const { t } = useTranslation()
  if (!value) return <span style={{ ...hint, color: 'var(--text-dim)' }}>–</span>
  return <Badge tone={CLASS_TONE[value] || 'muted'} title={CLASS_HINT[value] ? t(CLASS_HINT[value]) : undefined} {...rest}>{children ?? value}</Badge>
}

// What a model can do, from `api models`: text, image (generation) and tools (function calling). The text of a badge
// says which, in words, and the tone only backs it up; its title says what it means. A capability this page does not
// know is shown as the CLI named it, with no meaning to give it. No list (missing in a CLI that predates the field,
// null, empty, or not a list) is a dash: nothing is claimed.
const CAPS = {
  text: { label: 'settings.api.models.capText', hint: 'settings.api.models.capTextHint', tone: 'muted' },
  image: { label: 'settings.api.models.capImage', hint: 'settings.api.models.capImageHint', tone: 'info' },
  tools: { label: 'settings.api.models.capTools', hint: 'settings.api.models.capToolsHint', tone: 'info' },
}

// Up to three of them share a cell of the models table, so they are a little smaller than the other badges.
const small = { fontSize: 10, padding: '1px 6px' }

export function CapBadges({ caps, model }) {
  const { t } = useTranslation()
  const list = Array.isArray(caps) ? caps.filter(c => typeof c === 'string' && c !== '') : []
  if (!list.length) return <span style={{ ...hint, color: 'var(--text-dim)' }}>–</span>
  return (
    <ul aria-label={t('settings.api.models.capsOf', { model })} style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexWrap: 'wrap', gap: 3 }}>
      {list.map((c, i) => {
        const known = Object.prototype.hasOwnProperty.call(CAPS, c) ? CAPS[c] : undefined
        return (
          <li key={`${c}-${i}`}>
            <Badge tone={known?.tone || 'muted'} title={known ? t(known.hint) : undefined} style={small}>{known ? t(known.label) : c}</Badge>
          </li>
        )
      })}
    </ul>
  )
}
