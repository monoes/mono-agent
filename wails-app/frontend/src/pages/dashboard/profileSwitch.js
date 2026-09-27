import { SwitchProfile } from '../../wailsjs/go/main/App'

// Another profile's rows are read-only on the dashboard: run, stop and open
// act on the app's active profile. Switching works like the sidebar's:
// persist the choice, then reload so every page re-reads its data.
export async function switchToProfile(id, reload = () => window.location.reload()) {
  await SwitchProfile(id)
  reload()
}
