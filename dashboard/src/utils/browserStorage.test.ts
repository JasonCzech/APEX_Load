// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  readStoredJSON,
  readStoredString,
  readStoredStringArray,
  readStoredStringArrayRecord,
  removeStoredItem,
  writeStoredJSON,
  writeStoredString,
} from './browserStorage'

describe('browserStorage', () => {
  afterEach(() => {
    window.localStorage.clear()
    vi.restoreAllMocks()
  })

  it('reads JSON with a fallback for missing or malformed values', () => {
    expect(readStoredJSON('missing', { enabled: false })).toEqual({ enabled: false })

    window.localStorage.setItem('valid', JSON.stringify({ enabled: true }))
    expect(readStoredJSON('valid', { enabled: false })).toEqual({ enabled: true })

    window.localStorage.setItem('invalid', '{')
    expect(readStoredJSON('invalid', { enabled: false })).toEqual({ enabled: false })
  })

  it('writes JSON and strings without leaking storage failures', () => {
    writeStoredJSON('json', { ids: ['a'] })
    writeStoredString('string', 'relative')

    expect(window.localStorage.getItem('json')).toBe('{"ids":["a"]}')
    expect(readStoredString('string')).toBe('relative')

    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('quota')
    })
    expect(() => writeStoredJSON('blocked-json', { ok: true })).not.toThrow()
    expect(() => writeStoredString('blocked-string', 'value')).not.toThrow()
  })

  it('removes items without leaking storage failures', () => {
    window.localStorage.setItem('temporary', 'value')
    removeStoredItem('temporary')
    expect(window.localStorage.getItem('temporary')).toBeNull()

    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(() => removeStoredItem('temporary')).not.toThrow()
  })

  it('filters stored string arrays and string-array records', () => {
    window.localStorage.setItem('array', JSON.stringify(['a', 1, 'b', null]))
    window.localStorage.setItem('record', JSON.stringify({
      visible: ['vusers', 2, 'tps'],
      ignored: 'not-array',
      hidden: [false, 'errors'],
    }))

    expect(readStoredStringArray('array')).toEqual(['a', 'b'])
    expect(readStoredStringArray('missing')).toEqual([])
    expect(readStoredStringArrayRecord('record')).toEqual({
      visible: ['vusers', 'tps'],
      hidden: ['errors'],
    })
  })
})
