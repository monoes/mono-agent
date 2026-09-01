// Generic add/remove string-list editor. Extracted from
// orgdesigner/RoleInspector.jsx's original inline Responsibilities editor so
// the same interaction (uncontrolled inputs committing on blur, a "×"
// remove button per row, an "+ Add" button) can be reused for any
// string-array field — currently: responsibilities, and the Org Designer's
// tool-policy arrays (allowTools/denyTools/fileWrite/fileRead/webAllow/
// autoApproveTools).
export default function StringListField({ label, values, onChange, placeholder, addLabel = '+ Add' }) {
  const items = values || []

  const commit = (next) => onChange(next)

  return (
    <section>
      {label && <div className="form-label">{label}</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {items.map((v, i) => (
          <div key={i} style={{ display: 'flex', gap: 4 }}>
            <input
              className="form-input"
              defaultValue={v}
              onBlur={e => {
                const next = [...items]
                next[i] = e.target.value
                commit(next)
              }}
              style={{ fontSize: 11, padding: '5px 8px', flex: 1 }}
            />
            <button
              className="btn btn-sm btn-ghost"
              onClick={() => commit(items.filter((_, idx) => idx !== i))}
              title="Remove"
            >
              &times;
            </button>
          </div>
        ))}
        {items.length === 0 && placeholder && (
          <div style={{ fontSize: 10.5, color: 'var(--text-dim, #6c7b90)', fontStyle: 'italic', marginBottom: 4 }}>
            {placeholder}
          </div>
        )}
        <button
          className="btn btn-sm btn-secondary"
          onClick={() => commit([...items, ''])}
          style={{ alignSelf: 'flex-start' }}
        >
          {addLabel}
        </button>
      </div>
    </section>
  )
}
