import { useTranslation } from 'react-i18next'
import { ShieldCheck, ShieldOff } from 'lucide-react'

// The CLI's sandbox verdict (monomind.SandboxStatus*) -> the i18n key under
// agentSandbox. The CLI decides; this only renders.
const KEYS = {
  sandboxed: 'sandboxed',
  scoped: 'scoped',
  unsupported: 'unsupported',
  'awaiting-monomind': 'awaitingMonomind',
  'needs-monomind': 'needsMonomind',
  off: 'off',
}

// SandboxBadge shows whether an agent turn ran in its runtime's sandbox.
// Nothing for a turn that asked for none (coder mode) or an unknown value.
export function SandboxBadge({ status }) {
  const { t } = useTranslation()
  const key = KEYS[status]
  if (!key) return null
  const safe = status === 'sandboxed' || status === 'scoped'
  const Icon = safe ? ShieldCheck : ShieldOff
  const color = safe ? '#22c55e' : 'var(--text-muted)'
  return (
    <span
      data-testid="sandbox-badge"
      data-status={status}
      title={t(`agentSandbox.${key}Title`)}
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 4, marginTop: 4,
        padding: '1px 6px', borderRadius: 4, border: `1px solid ${safe ? 'rgba(34,197,94,.35)' : 'var(--border, rgba(127,127,127,.3))'}`,
        fontFamily: 'var(--font-mono)', fontSize: 9.5, color,
      }}
    >
      <Icon size={10} />
      {t(`agentSandbox.${key}`)}
    </span>
  )
}
