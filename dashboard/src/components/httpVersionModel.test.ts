import { describe, expect, it } from 'vitest'

import { formatHTTPVersion, formatScriptHTTPVersion, HTTP3_EXECUTION_NOTE, HTTP_VERSION_OPTIONS, inheritedHTTPActionVersion, isHTTP3Policy, scriptHTTPVersionSettings } from './httpVersionModel'

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

describe('HTTP/3 policies', () => {
  it('labels and parses auto+h3 and 3 like the engine', () => {
    expect(formatHTTPVersion('auto+h3')).toBe('Upgrade to HTTP/3 when advertised')
    expect(formatHTTPVersion('3')).toBe('HTTP/3 required (no fallback)')
    for (const raw of ['3', 'HTTP/3', 'http/3.0', 'h3', ' H3 ']) expect(inheritedHTTPActionVersion('http', raw)).toBe('3')
    expect(inheritedHTTPActionVersion('http', 'auto+h3')).toBe('auto+h3')
    // auto+h3 is an exact token, and draft tokens never become a policy.
    for (const raw of ['AUTO+H3', 'auto+h2', 'h3-29']) expect(inheritedHTTPActionVersion('http', raw)).toBeNull()
    expect(scriptHTTPVersionSettings('http', '3')).toMatchObject({ selection: '3', valid: true, effectiveLabel: 'HTTP/3 required (no fallback)' })
    expect(scriptHTTPVersionSettings('http/2', 'auto+h3')).toMatchObject({ selection: 'auto+h3', valid: true })
    // The http/3 protocol alias supplies exact HTTP/3 like http/2 supplies 2.
    expect(inheritedHTTPActionVersion('http/3')).toBe('3')
    expect(scriptHTTPVersionSettings('http/3')).toMatchObject({ selection: '', valid: true, defaultLabel: 'Protocol default (HTTP/3 required (no fallback))' })
    expect(inheritedHTTPActionVersion('http/3', '2')).toBe('2')
  })

  it('names the execution requirement only for the HTTP/3 policies', () => {
    expect(isHTTP3Policy('3') && isHTTP3Policy('auto+h3')).toBe(true)
    for (const value of ['', 'auto', '1.1', '2', 'h3', undefined]) expect(isHTTP3Policy(value)).toBe(false)
    expect(HTTP3_EXECUTION_NOTE).toContain('HTTP/3 execution enabled')
  })
})
