import { describe, expect, it } from 'vitest'

import { formatHTTPVersion, HTTP_VERSION_OPTIONS } from './httpVersionModel'

describe('httpVersionModel', () => {
  it('formats supported, automatic, and unexpected versions', () => {
    expect(formatHTTPVersion()).toBe(HTTP_VERSION_OPTIONS[0].label)
    expect(formatHTTPVersion('')).toBe(HTTP_VERSION_OPTIONS[0].label)
    expect(formatHTTPVersion(' 1.1 ')).toBe('HTTP/1.1 required')
    expect(formatHTTPVersion('2')).toBe('HTTP/2 required (no fallback)')
    expect(formatHTTPVersion('1.0')).toBe('Unsupported HTTP version 1.0')
  })
})
