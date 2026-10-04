import { describe, expect, it } from 'vitest'

import { formatHTTPVersion, formatScriptHTTPVersion, HTTP_VERSION_OPTIONS, scriptHTTPVersionSettings } from './httpVersionModel'

describe('httpVersionModel', () => {
  it('formats supported, automatic, and unexpected versions', () => {
    expect(formatHTTPVersion()).toBe(HTTP_VERSION_OPTIONS[0].label)
    expect(formatHTTPVersion('')).toBe(HTTP_VERSION_OPTIONS[0].label)
    expect(formatHTTPVersion(' 1.1 ')).toBe('HTTP/1.1 required')
    expect(formatHTTPVersion('2')).toBe('HTTP/2 required (no fallback)')
    expect(formatHTTPVersion('1.0')).toBe('Unsupported HTTP version 1.0')
  })
})

describe('effective script HTTP version labels', () => {
  it.each([
    ['http', undefined, 'Auto-negotiate (HTTP/2 or HTTP/1.1)'],
    ['http/1.1', '', 'HTTP/1.1 required'],
    ['http/2', undefined, 'HTTP/2 required (no fallback)'],
    ['http/2', '1.1', 'HTTP/1.1 required'],
    ['http/1.1', 'HTTP/2.0', 'HTTP/2 required (no fallback)'],
    ['http/2', '  ', 'Auto-negotiate (HTTP/2 or HTTP/1.1)'],
    ['http/2', 'auto', 'Unsupported script HTTP version'],
    ['http/2', null, 'Unsupported script HTTP version'],
  ])('labels %s global %j as %s', (protocol, value, expected) => {
    expect(formatScriptHTTPVersion(protocol as string, value)).toBe(expected)
  })

  it('preserves malformed settings and legacy automatic values as distinct display states', () => {
    expect(scriptHTTPVersionSettings('http/2')).toMatchObject({ selection: '', valid: true, defaultLabel: 'Protocol default (HTTP/2 required (no fallback))' })
    expect(scriptHTTPVersionSettings('http/2', 'HTTP/1.1')).toMatchObject({ selection: '1.1', valid: true })
    expect(scriptHTTPVersionSettings('http/2', '  ')).toMatchObject({ selection: 'existing-auto', valid: true, effectiveLabel: 'Auto-negotiate (HTTP/2 or HTTP/1.1)' })
    for (const value of [null, 'auto', '1.0', 2, {}, []]) expect(scriptHTTPVersionSettings('http/2', value)).toMatchObject({ selection: 'invalid', valid: false })
  })
})
