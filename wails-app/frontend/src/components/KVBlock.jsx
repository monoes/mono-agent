// Generic read-only key/value table. Extracted from OrgsPanel.jsx into its
// own module because orgdesigner/RoleInspector.jsx also needs it — importing
// it from OrgsPanel.jsx directly created a circular import (OrgsPanel ->
// OrgDesigner -> RoleInspector -> OrgsPanel), which broke bundle evaluation
// order in the production (Rollup) build and blanked the whole app.
export function KVBlock({ obj }) {
  if (!obj || typeof obj !== 'object') return null
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {Object.entries(obj).map(([k, v]) => (
        <div key={k} style={{ display: 'flex', justifyContent: 'space-between', gap: 10 }}>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: 1, flexShrink: 0 }}>{k}</span>
          <span style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--text-secondary)', textAlign: 'right', wordBreak: 'break-word' }}>
            {typeof v === 'object' ? JSON.stringify(v) : String(v)}
          </span>
        </div>
      ))}
    </div>
  )
}
