import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../../types/replay'
import { buildDebugTimelineModel } from './debugEventModel'
import { debugRequestView, toCurl, toHarLog } from './debugRequestExport'

const event: BrowserReplayEvent = {
  phase: 'action', action_index: 0, action_name: 'Pay', action_type: 'request', method: 'POST', target: 'https://shop.test/pay?x=1',
  success: false, status_code: 500, started_at: '2026-11-09T12:00:00.000Z', completed_at: '2026-11-09T12:00:00.100Z', duration_ms: 100,
  snapshot: {
    request_method: 'POST', request_url: 'https://shop.test/pay?x=1', request_headers: { 'Content-Type': 'application/json', Host: 'shop.test', 'X-Note': "it's" },
    request_body: '{"pay":true}', response_status: 500, response_headers: { 'Content-Type': 'application/json' }, response_body: '{"error":"down"}',
  },
  protocol_evidence: { timings: { dns_ms: 5, connect_ms: 10, tls_ms: 15, ttfb_ms: 80, duration_ms: 100 } },
}

describe('debug request export', () => {
  it('copies a runnable cURL command with one flag per line', () => {
    const view = debugRequestView(event)!
    expect(toCurl(view)).toBe([
      'curl',
      '-X POST',
      "'https://shop.test/pay?x=1'",
      "-H 'Content-Type: application/json'",
      `-H 'X-Note: it'\\''s'`,
      `--data-raw '{"pay":true}'`,
    ].join(' \\\n  '))
    expect(debugRequestView({ ...event, snapshot: undefined, method: undefined, action_type: 'think_time', target: '' })).toBeNull()
  })

  it('exports a single-entry HAR log with phase timings', () => {
    const row = buildDebugTimelineModel([event]).rows[0]
    const har = toHarLog(debugRequestView(event)!, row)
    const entry = har.log.entries[0]
    expect(entry.request).toMatchObject({ method: 'POST', url: 'https://shop.test/pay?x=1', queryString: [{ name: 'x', value: '1' }], postData: { text: '{"pay":true}' } })
    expect(entry.response).toMatchObject({ status: 500, content: { text: '{"error":"down"}', mimeType: 'application/json' } })
    // HAR connect includes the TLS handshake.
    expect(entry.timings).toMatchObject({ dns: 5, connect: 25, ssl: 15, wait: 50, receive: 20 })
  })
})

describe('cURL for HEAD', () => {
  it('uses --head instead of -X HEAD', () => {
    expect(toCurl({ method: 'HEAD', url: 'https://shop.test/', headers: {}, responseHeaders: {} })).toBe("curl \\\n  --head \\\n  'https://shop.test/'")
  })
})

describe('export details after review', () => {
  it('drops Accept-Encoding in favour of --compressed', async () => {
    const curl = toCurl({ method: 'GET', url: 'https://shop.test/', headers: { 'Accept-Encoding': 'gzip, br, zstd' }, responseHeaders: {} })
    expect(curl).not.toContain('Accept-Encoding')
    expect(curl).toContain('--compressed')
  })
  it('writes HAR version tokens, not display labels', async () => {
    const { harHttpVersion } = await import('./debugRequestExport')
    expect(harHttpVersion('HTTP/3 (QUIC)')).toBe('HTTP/3')
    expect(harHttpVersion('HTTP/2')).toBe('HTTP/2')
    expect(harHttpVersion('HTTP')).toBe('HTTP/1.1')
    expect(harHttpVersion('HTTPS')).toBe('HTTP/1.1')
  })
})

describe('HAR timings add up', () => {
  it('puts time the phases do not explain into blocked', () => {
    const e = { ...event, duration_ms: 300, protocol_evidence: { timings: { dns_ms: 5, connect_ms: 10, tls_ms: 15, ttfb_ms: 80, duration_ms: 100 } } }
    const row = buildDebugTimelineModel([e]).rows[0]
    const t = toHarLog(debugRequestView(e)!, row).log.entries[0].timings
    expect(Math.max(0, t.blocked) + t.dns + t.connect + t.send + t.wait + t.receive).toBeCloseTo(row.durationMs)
  })
})

describe('empty header values', () => {
  it('sends them with curl\'s Name; syntax', () => {
    expect(toCurl({ method: 'GET', url: 'https://shop.test/', headers: { 'X-Empty': '' }, responseHeaders: {} })).toContain("-H 'X-Empty;'")
  })
})

describe('HAR for a redirected request', () => {
  it('times only the final response, not the whole chain', () => {
    const e = { ...event, duration_ms: 1000, protocol_evidence: undefined, network_entries: [
      { url: 'https://shop.test/pay', method: 'POST', status_code: 302, duration_ms: 400, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 400 }] } },
      { url: 'https://shop.test/pay?x=1', method: 'GET', status_code: 200, duration_ms: 600, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 600 }] } },
    ] }
    const row = buildDebugTimelineModel([e]).rows[0]
    const entry = toHarLog(debugRequestView(e)!, row).log.entries[0]
    expect(entry.time).toBe(600)
    expect(entry.timings.wait).toBe(600)
  })
})

describe('HAR picks the document chain', () => {
  it('uses the last document hop, not a sub-resource, and its start time', () => {
    const e = { ...event, duration_ms: 1000, protocol_evidence: undefined, started_at: '2026-11-09T12:00:00.000Z', network_entries: [
      { url: 'https://shop.test/', method: 'GET', status_code: 302, resource_type: 'document', duration_ms: 100, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 100 }] } },
      { url: 'https://shop.test/home', method: 'GET', status_code: 200, resource_type: 'document', duration_ms: 300, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 300 }] } },
      { url: 'https://cdn.test/app.js', method: 'GET', status_code: 200, resource_type: 'script', duration_ms: 50, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 50 }] } },
    ] }
    const row = buildDebugTimelineModel([e]).rows[0]
    const entry = toHarLog(debugRequestView(e)!, row).log.entries[0]
    expect(entry.time).toBe(300)
    expect(entry.startedDateTime).toBe('2026-11-09T12:00:00.100Z')
  })
})
