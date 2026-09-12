import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ImageOff } from 'lucide-react'
import { api } from '../../services/api.js'

// Schemes this panel will actually navigate to. Anything else (javascript:,
// data:, file:, vbscript:, a bare custom scheme, or no scheme at all) is
// rendered inert rather than passed to openURL — "Web links require normal
// user activation and scheme validation" (plan global constraint). App.OpenURL
// itself does no validation (a thin pass-through to runtime.BrowserOpenURL),
// so this is the app's own gate — Wails' runtime does its own further
// validation underneath (rejects javascript:/data:/file:/schemeless), but
// that's an internal, unversioned-by-contract detail of a third-party
// dependency, not something this app can rely on staying true.
const ALLOWED_URL_SCHEMES = ['http:', 'https:', 'mailto:']

// Exported for direct unit testing. Deliberately parsed with NO base: a
// relative/protocol-relative/bare-path href (which has no meaningful target
// in a chat bubble anyway) then fails to parse at all and is correctly
// rejected here, rather than silently resolving to an allowed scheme it
// never actually had. (An earlier version resolved against a placeholder
// base to avoid throwing on such hrefs — but that made a schemeless string
// like "../../secret.txt" or "Applications/Terminal.app" resolve to
// "https:" and pass this check, while the ORIGINAL unresolved string was
// still what got handed to openApproved/rendered as the real href below —
// a validate-one-value/use-another-value mismatch. Parsing without a base
// means only a href that already carries its own real, absolute scheme can
// ever pass, so the value that passes this check and the value used
// afterward are always identical.)
export function isAllowedURL(href) {
  if (typeof href !== 'string' || href === '') return false
  try {
    const u = new URL(href)
    return ALLOWED_URL_SCHEMES.includes(u.protocol)
  } catch {
    return false
  }
}

function openApproved(url) {
  if (isAllowedURL(url)) api.openURL(url)
}

// Renders an assistant/error message's Markdown. No rehype-raw plugin —
// react-markdown's default behavior already keeps embedded raw HTML as
// literal text, exactly what the plan requires ("raw HTML stays text").
const components = {
  p: (p) => <p style={{ margin: '0.35em 0' }} {...p} />,
  a: ({ href, children, ...rest }) => {
    if (!isAllowedURL(href)) {
      return (
        <span title="Link blocked: unsupported scheme" style={{ color: '#94a3b8', textDecoration: 'underline dotted', cursor: 'default' }}>
          {children}
        </span>
      )
    }
    return (
      <a
        href={href}
        style={{ color: '#00b4d8', textDecoration: 'underline' }}
        onClick={(e) => { e.preventDefault(); openApproved(href) }}
        {...rest}
      >
        {children}
      </a>
    )
  },
  // Never auto-fetch remote Markdown images (plan: "Do not auto-fetch
  // remote Markdown images") — no <img> is ever rendered, so the browser
  // never issues the request itself. A same-scheme-gated link lets the
  // user open it explicitly instead.
  img: ({ src, alt }) => (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, padding: '1px 6px', border: '1px dashed rgba(148,163,184,0.4)', borderRadius: 4, color: '#94a3b8', fontSize: 10, verticalAlign: 'middle' }}>
      <ImageOff size={11} />
      {alt || 'image'}
      {isAllowedURL(src) && (
        <a href={src} onClick={(e) => { e.preventDefault(); openApproved(src) }} style={{ color: '#00b4d8' }}>open</a>
      )}
    </span>
  ),
  ul: (p) => <ul style={{ margin: '0.3em 0', paddingLeft: 18 }} {...p} />,
  ol: (p) => <ol style={{ margin: '0.3em 0', paddingLeft: 18 }} {...p} />,
  li: (p) => <li style={{ margin: '0.1em 0' }} {...p} />,
  strong: (p) => <strong style={{ color: '#f1f5f9' }} {...p} />,
  blockquote: (p) => <blockquote style={{ margin: '0.35em 0', paddingLeft: 10, borderLeft: '2px solid #1e3a4f', color: '#94a3b8' }} {...p} />,
  hr: (p) => <hr style={{ border: 'none', borderTop: '1px solid #1e3a4f', margin: '0.5em 0' }} {...p} />,
  code: (p) => <code style={{ background: '#020509', padding: '1px 4px', borderRadius: 3 }} {...p} />,
  // overflow:auto bounds a huge single code line to this block's own
  // scrollbar rather than blowing out the panel's layout.
  pre: (p) => <pre style={{ background: '#020509', border: '1px solid #1e3a4f', borderRadius: 6, padding: 8, overflow: 'auto', margin: '0.35em 0', maxWidth: '100%' }} {...p} />,
}

export function ChatMarkdown({ content }) {
  if (!content) return null
  return (
    <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.55, wordBreak: 'break-word' }}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>{content}</ReactMarkdown>
    </div>
  )
}
