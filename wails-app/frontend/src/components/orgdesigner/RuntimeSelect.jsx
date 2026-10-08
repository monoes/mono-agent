import { runtimeLabel } from '../../lib/runtimeLabels.js'
import { pickerRuntimes, runtimeChoice, useSectionsRuntimePolicy } from './sectionsRuntimes.js'

// The role inspector's runtime picker. In a sections org it disables the
// runtimes monomind refuses (showing its reason) and marks the unverified
// ones with a warning, which still can be chosen (#341).
export default function RuntimeSelect({ value, options, sectionsOrg, onChange }) {
  const policy = useSectionsRuntimePolicy(!!sectionsOrg)
  const current = runtimeChoice(policy, value)
  return (
    <>
      <select className="form-select" value={value} onChange={e => onChange(e.target.value)}>
        <option value="">(inherit org default)</option>
        {pickerRuntimes(options, policy).map(r => {
          const c = runtimeChoice(policy, r)
          const label = c.status === 'unverified' ? `${runtimeLabel(r)} (unverified)`
            : c.status === 'refused' ? `${runtimeLabel(r)} (not available in a sections org)`
            : runtimeLabel(r)
          return <option key={r} value={r} disabled={c.status === 'refused' && r !== value} title={c.reason}>{label}</option>
        })}
      </select>
      {current.status !== 'ok' && (
        <div
          role={current.status === 'refused' ? 'alert' : 'status'}
          data-testid="runtime-policy-note"
          style={{ fontSize: 11, marginTop: 4, color: current.status === 'refused' ? 'var(--danger, #c0392b)' : 'var(--warning, #b7791f)' }}
        >
          {current.status === 'unverified' ? 'Unverified: ' : ''}{current.reason}
        </div>
      )}
    </>
  )
}
