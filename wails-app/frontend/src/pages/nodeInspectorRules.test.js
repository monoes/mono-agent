import { describe, it, expect } from 'vitest'
import { derivePlatformId, isSessionPicker, isMediaField, automationIdsFrom, browserAutomationOf } from './nodeInspectorRules.js'

const ids = automationIdsFrom({ automations: [{ id: 'hackernews' }, { id: 'instagram' }, { id: 'my-shelf' }, { id: 'gemini', removed: true }] })

describe('session / credential picker', () => {
  it('takes the schema credential_platform first', () => {
    expect(derivePlatformId({ subtype: 'hackernews.reply_to_comment' }, { credential_platform: 'hackernews' }, new Set())).toBe('hackernews')
  })
  it('falls back to the installed automation — no hardcoded platform list', () => {
    expect(derivePlatformId({ subtype: 'hackernews.reply_to_comment' }, {}, ids)).toBe('hackernews')
    expect(derivePlatformId({ subtype: 'my-shelf.list_items' }, null, ids)).toBe('my-shelf')
    expect(derivePlatformId({ subtype: 'gemini.ask' }, null, ids)).toBe('gemini')
    expect(isSessionPicker({ subtype: 'my-shelf.list_items' }, ids)).toBe(true)
  })
  it('keeps API credentials for API nodes and nothing for plain nodes', () => {
    expect(derivePlatformId({ subtype: 'comm.slack' }, null, ids)).toBe('slack')
    expect(isSessionPicker({ subtype: 'comm.slack' }, ids)).toBe(false)
    expect(derivePlatformId({ subtype: 'core.set' }, null, ids)).toBe(null)
    expect(browserAutomationOf('unknown.thing', ids)).toBe(null)
  })
})

describe('isMediaField', () => {
  const k = (key, extra = {}) => isMediaField({ key, type: 'text', ...extra })
  it('accepts explicit hints and media keys', () => {
    for (const key of ['image_url', 'imageUrl', 'media', 'photo_url', 'media_url', 'file_path', 'local_path', 'video_file_path', 'attachments', 'cover']) expect(k(key)).toBe(true)
    expect(k('anything', { type: 'file' })).toBe(true)
    expect(k('anything', { ui: { widget: 'image' } })).toBe(true)
    expect(k('anything', { format: 'path' })).toBe(true)
  })
  it('rejects ordinary text, ids and names', () => {
    for (const key of ['session_username', 'itemID', 'profile', 'text', 'comment_text', 'video_id', 'media_ids', 'file_name', 'remote_path', 'url']) expect(k(key)).toBe(false)
  })
})
