import { describe, it, expect } from 'vitest'
import { setupSteps } from './SetupSteps.jsx'

describe('setupSteps', () => {
  it('orders runnable fixes, then manual steps, then the runtime pointer', () => {
    const report = { results: [
      { id: 'browser.extension', title: 'Extension', status: 'warn', fix: { id: 'browser.extension.install', label: 'Load it', safety: 'manual' } },
      { id: 'core.db', title: 'Database', status: 'fail', fix: { id: 'core.db.migrate', label: 'Migrate', safety: 'auto' } },
      { id: 'services.daemon', title: 'Daemon', status: 'warn', fix: { id: 'services.daemon.start', label: 'Start', safety: 'confirm' } },
      { id: 'browser.bridge', title: 'Bridge', status: 'warn', fix: { id: 'services.daemon.start', label: 'Start', safety: 'confirm' } },
      { id: 'services.autostart', title: 'Login', status: 'info', fix: { id: 'services.autostart.install', label: 'Autostart', safety: 'confirm', optional: true } },
      { id: 'runtimes.agents', title: 'AI agent runtimes', status: 'fail' },
      { id: 'monomind.node', title: 'Node.js', status: 'ok', actions: [{ id: 'monomind.node.update', label: 'Update' }] },
    ] }
    expect(setupSteps(report).map(s => s.fix?.id || s.kind)).toEqual([
      'core.db.migrate', 'services.daemon.start', 'browser.extension.install', 'runtime',
    ])
  })
  it('is empty for a healthy report', () => {
    expect(setupSteps({ results: [{ id: 'core.db', status: 'ok' }] })).toEqual([])
  })
})
