import OrgsPanel from '../components/OrgsPanel.jsx'

// isActive tells the panel whether the Orgs page is the app's active tab —
// App.jsx keeps pages mounted (display:none) after first visit, so the
// panel needs it to stop its SSE stream while another page is showing.
export default function Orgs({ isActive = true }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      <div className="page-header">
        <div className="page-header-left">
          <div className="page-title">Orgs</div>
          <div className="page-subtitle">Local agent organizations (monomind Org Runtime v2)</div>
        </div>
      </div>
      <div className="page-body" style={{ flex: 1, overflow: 'hidden', display: 'flex' }}>
        <OrgsPanel pageActive={isActive} />
      </div>
    </div>
  )
}
