import { describe, expect, it } from 'vitest'

import { mergeHeaderInits, withCallOptions, type ApiCallOptions } from './requestOptions'

function headerEntries(headers?: HeadersInit): Record<string, string> {
  const out: Record<string, string> = {}
  new Headers(headers).forEach((value, key) => {
    out[key] = value
  })
  return out
}

describe('withCallOptions', () => {
  it('returns the hook-built init untouched when no options are supplied', () => {
    const init = { method: 'POST', body: '{}' }
    expect(withCallOptions(init, undefined)).toBe(init)
    expect(withCallOptions(undefined, undefined)).toBeUndefined()
  })

  it('returns the init untouched when the options object carries nothing', () => {
    const init = { method: 'DELETE' }
    expect(withCallOptions(init, {})).toBe(init)
  })

  it('layers transport options onto the init', () => {
    const controller = new AbortController()
    const merged = withCallOptions(
      { method: 'PUT', body: '{"a":1}' },
      { signal: controller.signal, timeoutMs: 250, credentials: 'omit', cache: 'no-store', keepalive: true },
    )
    expect(merged).toEqual({
      method: 'PUT',
      body: '{"a":1}',
      signal: controller.signal,
      timeoutMs: 250,
      credentials: 'omit',
      cache: 'no-store',
      keepalive: true,
    })
  })

  it('merges caller headers over the hook headers instead of replacing them', () => {
    const merged = withCallOptions(
      { method: 'PUT', headers: { 'If-Match': '"rev-1"', Accept: 'application/json' } },
      { headers: { Accept: 'text/csv', 'X-Trace': 'abc' } },
    )
    expect(headerEntries(merged?.headers)).toEqual({
      'if-match': '"rev-1"',
      accept: 'text/csv',
      'x-trace': 'abc',
    })
    expect(merged?.method).toBe('PUT')
  })

  it('does not mutate the init it was given', () => {
    const init = { method: 'POST', headers: { Accept: 'application/json' } }
    withCallOptions(init, { headers: { Accept: 'text/csv' }, timeoutMs: 10 })
    expect(init).toEqual({ method: 'POST', headers: { Accept: 'application/json' } })
  })

  it('ignores non-object trailing arguments', () => {
    // `ids.map(api.getTest)` hands the array index to the options parameter.
    const init = { method: 'GET' }
    expect(withCallOptions(init, 2 as unknown as ApiCallOptions)).toBe(init)
    expect(withCallOptions(init, 'x' as unknown as ApiCallOptions)).toBe(init)
    expect(withCallOptions(init, [] as unknown as ApiCallOptions)).toBe(init)
  })

  it('ignores objects that merely lack transport fields, such as a React event', () => {
    const init = { method: 'POST' }
    expect(withCallOptions(init, { type: 'click', target: null } as unknown as ApiCallOptions)).toBe(init)
  })
})

describe('mergeHeaderInits', () => {
  it('returns undefined when there is nothing to merge', () => {
    expect(mergeHeaderInits(undefined, undefined)).toBeUndefined()
  })

  it('keeps base headers the extra set does not override', () => {
    expect(headerEntries(mergeHeaderInits({ a: '1', b: '2' }, { b: '3' }))).toEqual({ a: '1', b: '3' })
  })
})
