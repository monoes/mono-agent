import React from 'react'
import ReactDOM from 'react-dom/client'
import AccountApp from './AccountApp.jsx'
import ErrorBoundary from './components/ErrorBoundary.jsx'
import './i18n.js'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <ErrorBoundary>
      <AccountApp />
    </ErrorBoundary>
  </React.StrictMode>,
)
