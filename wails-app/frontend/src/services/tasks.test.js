// The bindings the task service calls exist in the generated module. They
// are placed there by hand (phase 3 plan, Task 3): a misspelt name would
// only fail when a person clicks.
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import * as App from '../wailsjs/go/main/App'

const service = readFileSync(join(__dirname, 'tasks.js'), 'utf8')
const used = [...new Set([...service.matchAll(/GoApp\.(\w+)/g)].map(m => m[1]))].sort()
const dts = readFileSync(join(__dirname, '..', 'wailsjs', 'go', 'main', 'App.d.ts'), 'utf8')

describe('task bindings', () => {
  it('finds the eleven bindings the service uses', () => {
    expect(used).toEqual(['TaskAdd', 'TaskAgentShell', 'TaskApprove', 'TaskArchive', 'TaskBoard', 'TaskComment',
      'TaskEdit', 'TaskMove', 'TaskPulse', 'TaskShow', 'TaskUnarchive'])
  })
  it('has each in App.js and App.d.ts', () => {
    expect(used.filter(n => typeof App[n] !== 'function')).toEqual([])
    expect(used.filter(n => !dts.includes(`export function ${n}(`))).toEqual([])
  })
})
