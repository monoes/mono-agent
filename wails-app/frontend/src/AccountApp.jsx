// The root of the app: the monoes.me account in front of the shell (spec §6.5).
// Its own file, so it can be tested without mounting every page the shell has.
import { useState } from 'react'
import App from './App.jsx'
import AccountShell from './components/account/AccountShell.jsx'
import { createAccountGate } from './lib/accountGate.js'
import { account } from './services/account.js'

export default function AccountApp() {
  const [gate] = useState(() => createAccountGate({ status: account.status }))
  return <AccountShell gate={gate} renderShell={(banners) => <App banners={banners} />} />
}
