// CI-style doctrine check (plan §10 Phase 5 gate): Wails bindings reach
// monomind only through `monoagentcli` subprocesses, never by importing
// internal/monomind. Scans every wails-app/*.go file.
//
// KNOWN_EXCEPTIONS are the files that imported it before the doctrine was
// written down (chat supervision, runtime scan) — none is an
// org binding. The list may only shrink: a new importer fails, and an entry
// that no longer imports fails too, so the list stays honest.
import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

const WAILS_APP_DIR = fileURLToPath(new URL('../..', import.meta.url))
const IMPORT = '"github.com/monoes/mono-agent/internal/' + 'monomind"'
const KNOWN_EXCEPTIONS = ['app.go', 'app_ai.go', 'app_chat.go']

function importers() {
  return readdirSync(WAILS_APP_DIR)
    .filter(f => f.endsWith('.go'))
    .filter(f => readFileSync(join(WAILS_APP_DIR, f), 'utf8').includes(IMPORT))
    .sort()
}

describe('wails-app doctrine: no internal/monomind imports', () => {
  it('finds the Go sources', () => {
    expect(readdirSync(WAILS_APP_DIR).filter(f => f.endsWith('.go')).length).toBeGreaterThan(10)
  })

  it('has no importer outside the known pre-doctrine exceptions', () => {
    const unexpected = importers().filter(f => !KNOWN_EXCEPTIONS.includes(f))
    expect(unexpected).toEqual([])
  })

  it('never lets an org binding import it', () => {
    expect(importers().filter(f => f.startsWith('app_org'))).toEqual([])
  })

  it('keeps the exception list free of stale entries', () => {
    const current = importers()
    expect(KNOWN_EXCEPTIONS.filter(f => !current.includes(f))).toEqual([])
  })
})
