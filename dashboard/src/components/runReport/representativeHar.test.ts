import { describe, expect, it } from 'vitest'

import type { ArchiveTransactionWaterfall, ArchiveWaterfallRow } from '../../types/timeAnatomy'
import { REPRESENTATIVE_HAR_COMMENT, harMethod, harUrl, representativeHarFileName, representativeHarLog } from './representativeHar'

function row(partial: Partial<ArchiveWaterfallRow>): ArchiveWaterfallRow {
  return {
    event_id: 'tx_00000000000000000000000000000001', ordinal: 1, population: 'action', timestamp: '2026-11-01T10:00:00.000Z',
    offset_ms: 0, duration_ms: 0, action_name: 'GET /', success: true,
    phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 },
    ...partial,
  }
}

const data: ArchiveTransactionWaterfall = {
  schema: 'apexload.transaction_waterfall.v1', join_rule: 'vuser_lifetime_containment_v1', phase_model: 'sequential_clipped_v1', identity_quality: 'lifetime',
  root: row({ event_id: 'tx_0123456789abcdef0123456789abcdef', population: 'transaction', action_name: 'Checkout', duration_ms: 130, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 130 } }),
  requests: [
    // Out of start order on purpose: entries follow the waterfall's order.
    row({ event_id: 'r2', ordinal: 3, offset_ms: 20, timestamp: '2026-11-01T10:00:00.020Z', duration_ms: 90, action_name: 'POST /cart', dependency_target: 'shop.example.test', http_version: '2', connection: 'new', status_code: 201, trace_id: 'abc', phases: { dns_ms: 5, connect_ms: 10, tls_ms: 15, server_ms: 50, download_ms: 10, unsplit_ms: 0 } }),
    row({ event_id: 'r1', ordinal: 2, offset_ms: 0, duration_ms: 20, action_name: 'Load basket', dependency_target: 'shop.example.test', success: false, status_code: 503, error_message: 'upstream unavailable', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 20 } }),
  ],
  requests_total: 2,
}

describe('representative HAR export', () => {
  it('builds a HAR 1.2 log with one page and the requests in start order', () => {
    const har = representativeHarLog([{ title: 'P95 Checkout', data }])
    expect(har.log.version).toBe('1.2')
    expect(har.log.creator.name).toBe('APEX Load run report')
    expect(har.log.comment).toBe(REPRESENTATIVE_HAR_COMMENT)
    expect(har.log.pages).toEqual([{ startedDateTime: '2026-11-01T10:00:00.000Z', id: 'page_1', title: 'P95 Checkout', pageTimings: { onContentLoad: -1, onLoad: 130 } }])
    expect(har.log.entries.map(entry => entry._action_name)).toEqual(['Load basket', 'POST /cart'])
    expect(har.log.entries.every(entry => entry.pageref === 'page_1')).toBe(true)
  })

  it('maps the backend phases to HAR timings that add up to the request time', () => {
    const [failed, post] = representativeHarLog([{ title: 'P95 Checkout', data }]).log.entries
    expect(post.time).toBe(90)
    expect(post.timings).toEqual({ blocked: 0, dns: 5, connect: 25, ssl: 15, send: 0, wait: 50, receive: 10 })
    expect(post.startedDateTime).toBe('2026-11-01T10:00:00.020Z')
    expect(post.request).toMatchObject({ method: 'POST', url: 'apexload://shop.example.test/POST%20%2Fcart', httpVersion: 'HTTP/2', headers: [], cookies: [], queryString: [], headersSize: -1, bodySize: -1 })
    expect(post.response).toMatchObject({ status: 201, httpVersion: 'HTTP/2', headers: [], cookies: [], content: { size: 0, mimeType: '' } })
    expect(post._trace_id).toBe('abc')
    expect(post._connection).toBe('new')
    // A request with no TTFB is all wait, and says so; no method is invented for an unnamed one.
    expect(failed.timings).toEqual({ blocked: -1, dns: -1, connect: -1, ssl: -1, send: 0, wait: 20, receive: 0 })
    expect(failed.comment).toBe('Only the total time was recorded: it is all in wait.')
    expect(failed.request.method).toBe('')
    expect(failed.request.httpVersion).toBe('')
    expect(failed.response).toMatchObject({ status: 503, _error: 'upstream unavailable' })
  })

  it('never exports what the drill-down did not carry', () => {
    const text = JSON.stringify(representativeHarLog([{ title: 'P95 Checkout', data }]))
    // No request or response content, no cookies or headers, no invented http(s) address.
    expect(text).not.toMatch(/"postData"|"text":|"value":/)
    expect(text).not.toMatch(/https?:\/\/shop/)
  })

  it('keeps an archived URL target, scrubbed by the archive, and otherwise names the target and action', () => {
    expect(harUrl({ event_id: 'e', action_name: 'GET /a', dependency_target: 'https://api.example.test/v1/items?page=2' })).toBe('https://api.example.test/v1/items?page=2')
    expect(harUrl({ event_id: 'e', action_name: 'GET /a', dependency_target: 'API.example.test:8443' })).toBe('apexload://api.example.test:8443/GET%20%2Fa')
    expect(harUrl({ event_id: 'e', action_name: '', dependency_target: 'queue://orders' })).toBe('apexload://unknown-target/e')
    expect(harUrl({ event_id: 'e', action_name: 'Login', dependency_target: undefined })).toBe('apexload://unknown-target/Login')
    const [entry] = representativeHarLog([{ title: 't', data: { ...data, requests: [row({ dependency_target: 'https://api.example.test/v1/items?page=2' })] } }]).log.entries
    expect(entry.request.queryString).toEqual([{ name: 'page', value: '2' }])
  })

  it('takes the method only from a request line, never from an ordinary word', () => {
    expect(harMethod({ action_name: 'GET /cart' })).toBe('GET')
    expect(harMethod({ action_name: ' DELETE https://api.example.test/items/1' })).toBe('DELETE')
    for (const name of ['get /cart', 'DELETE', 'Getting started', 'POSTAL code lookup', 'Get account', 'Options page', 'Head office', 'Connect wallet', 'Trace order', 'GET account']) {
      expect(harMethod({ action_name: name }), name).toBe('')
    }
  })

  it('exports a request root itself, and notes truncation, partial and legacy caveats on the page', () => {
    const requestRoot = representativeHarLog([{ title: 'Slowest GET /a', data: { ...data, root: row({ action_name: 'GET /a', duration_ms: 40, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 30, download_ms: 10, unsplit_ms: 0 } }), requests: [], requests_total: 0 } }])
    expect(requestRoot.log.entries).toHaveLength(1)
    expect(requestRoot.log.entries[0].timings).toMatchObject({ wait: 30, receive: 10 })
    const page = representativeHarLog([{ title: 't', data: { ...data, truncated: true, requests_total: 812, partial: true, identity_quality: 'legacy' } }]).log.pages[0]
    expect(page.comment).toBe('Only the first 2 of 812 requests are included. The report did not retain every request of this transaction, so some may be missing. Legacy archive: requests from another VUser may appear.')
    // A transaction with no requests exports an empty page, not its own rollup.
    expect(representativeHarLog([{ title: 't', data: { ...data, requests: [], requests_total: 0 } }]).log.entries).toEqual([])
  })

  it('names the file after the instance', () => {
    expect(representativeHarFileName('P95 Checkout', 'tx_0123456789abcdef0123456789abcdef')).toBe('p95-checkout-89abcdef.har')
    expect(representativeHarFileName('Failed · Pay / now', 'tx_ff')).toBe('failed-pay-now-tx_ff.har')
  })
})
