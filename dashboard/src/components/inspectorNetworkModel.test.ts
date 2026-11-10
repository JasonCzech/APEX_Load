import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent, BrowserReplayNetworkEntry } from '../types/replay'
import type { ScriptResourceSnapshot } from '../types/scripts'
import {
  filterInspectorNetworkRows,
  hasInspectorNetworkSubresources,
  inspectorNetworkBodySummary,
  inspectorNetworkCounts,
  inspectorNetworkDisplayURL,
  inspectorNetworkFilters,
  inspectorNetworkKeptBody,
  inspectorNetworkPhases,
  inspectorNetworkRows,
  inspectorNetworkType,
  matchResourcesToEntries,
} from './inspectorNetworkModel'

function event(overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent {
  return {
    phase: 'action', action_name: 'Home', action_type: 'request', method: 'GET', target: 'https://shop.example.test/',
    success: true, status_code: 200, started_at: '', completed_at: '', duration_ms: 400,
    ...overrides,
  } as BrowserReplayEvent
}

const resource = (url: string, overrides: Partial<ScriptResourceSnapshot> = {}): ScriptResourceSnapshot => ({
  request_url: url, response_status: 200, response_body_state: 'not_captured', ...overrides,
})

describe('inspectorNetworkType', () => {
  it('prefers the resource type, then content type, then extension', () => {
    expect(inspectorNetworkType({ resourceType: 'stylesheet', contentType: 'image/png', url: '/a.js' })).toBe('style')
    expect(inspectorNetworkType({ resourceType: 'fetch' })).toBe('xhr')
    expect(inspectorNetworkType({ resourceType: 'websocket' })).toBe('other')
    expect(inspectorNetworkType({ contentType: 'text/javascript; charset=utf-8', url: '/a.css' })).toBe('script')
    expect(inspectorNetworkType({ contentType: 'font/woff2' })).toBe('font')
    expect(inspectorNetworkType({ contentType: 'application/json', url: '/img/a.PNG?v=1' })).toBe('image')
    expect(inspectorNetworkType({ url: 'https://x.test/fonts/a.woff2' })).toBe('font')
    expect(inspectorNetworkType({ contentType: 'application/json', url: '/api/cart' })).toBe('other')
  })
})

describe('inspectorNetworkDisplayURL', () => {
  it('drops the host when it is the action’s and keeps another host', () => {
    expect(inspectorNetworkDisplayURL('https://shop.example.test/img/a.png?x=1', 'https://shop.example.test/')).toBe('/img/a.png?x=1')
    expect(inspectorNetworkDisplayURL('https://cdn.example.test/a.js', 'https://shop.example.test/')).toBe('cdn.example.test/a.js')
    expect(inspectorNetworkDisplayURL('/relative', 'https://shop.example.test/')).toBe('/relative')
    expect(inspectorNetworkDisplayURL('[not retained: URL limit]', 'https://shop.example.test/')).toBe('[not retained: URL limit]')
  })
})

describe('matchResourcesToEntries', () => {
  const entry = (url: string, status = 200, method = 'GET'): BrowserReplayNetworkEntry => ({ url, status_code: status, method })

  it('pairs a resource with the GET entry of the same URL and status', () => {
    expect(matchResourcesToEntries([entry('/a.css'), entry('/b.js')], [resource('/b.js'), resource('/a.css')])).toEqual([1, 0])
  })

  it('keeps them separate when the status differs or the method is not GET', () => {
    expect(matchResourcesToEntries([entry('/a.css', 404)], [resource('/a.css')])).toEqual([-1])
    expect(matchResourcesToEntries([entry('/a.css', 200, 'POST')], [resource('/a.css')])).toEqual([-1])
  })

  it('pairs same-URL fetches in order only when both sides have as many', () => {
    expect(matchResourcesToEntries([entry('/a.png'), entry('/a.png')], [resource('/a.png'), resource('/a.png')])).toEqual([0, 1])
    expect(matchResourcesToEntries([entry('/a.png'), entry('/a.png')], [resource('/a.png')])).toEqual([-1])
    expect(matchResourcesToEntries([entry('/a.png')], [resource('/a.png'), resource('/a.png')])).toEqual([-1, -1])
  })

  it('does not merge a scrubbed resource URL or an excluded entry', () => {
    expect(matchResourcesToEntries([entry('/a?token=abc')], [resource('/a?token=[REDACTED]')])).toEqual([-1])
    expect(matchResourcesToEntries([entry('/a.css')], [resource('/a.css')], new Set([0]))).toEqual([-1])
  })

  it('matches failures without a status by failure kind', () => {
    const failed: BrowserReplayNetworkEntry = { url: '/x.js', failure_kind: 'timeout' }
    expect(matchResourcesToEntries([failed], [resource('/x.js', { response_status: 0, failure_kind: 'timeout' })])).toEqual([0])
    expect(matchResourcesToEntries([failed], [resource('/x.js', { response_status: 0, failure_kind: 'dns' })])).toEqual([-1])
  })
})

describe('inspectorNetworkRows', () => {
  const httpEvent = event({
    network_entries: [
      { method: 'GET', url: 'https://shop.example.test/', status_code: 302, duration_ms: 20, response_headers: { Location: '/home' } },
      { method: 'GET', url: 'https://shop.example.test/home', status_code: 200, duration_ms: 80, har: { phases: [{ name: 'dns', start_ms: 0, end_ms: 2 }, { name: 'tls', unavailable_reason: 'reused_connection' }] } },
      { method: 'GET', url: 'https://shop.example.test/app.js', status_code: 200, duration_ms: 30 },
      { method: 'GET', url: 'https://shop.example.test/img/keys.png', status_code: 404, duration_ms: 54.5 },
    ],
    snapshot: {
      response_body_state: 'captured',
      resources: [
        resource('https://shop.example.test/app.js', { response_body_state: 'captured', response_body: 'console.log(1)', response_headers: { 'Content-Type': 'text/javascript' } }),
        resource('https://shop.example.test/img/keys.png', { response_status: 404 }),
        resource('https://shop.example.test/site.css', { response_body_state: 'redacted', response_body: 'a{} /* [REDACTED] */', cache_hit: true }),
      ],
      omitted_resources: 2,
    },
  })

  it('is one row per request: entries in order, then unmatched resources', () => {
    const rows = inspectorNetworkRows(httpEvent)
    expect(rows.map(row => [row.hop, row.display, row.role, row.status])).toEqual([
      [1, '/', 'self', 302],
      [2, '/home', 'redirect', 200],
      [3, '/app.js', null, 200],
      [4, '/img/keys.png', null, 404],
      [null, '/site.css', null, 200],
    ])
    expect(rows[2].resource?.response_body).toBe('console.log(1)')
    expect(rows[2].type).toBe('script')
    expect(rows[2].body.label).toBe('Kept')
    expect(rows[3].failed).toBe(true)
    expect(rows[3].body.label).toBe('Not kept')
    expect(rows[4]).toMatchObject({ type: 'style', durationMs: null, cacheHit: true, body: { kind: 'redacted' } })
  })

  it('takes the own request’s body state from the snapshot', () => {
    expect(inspectorNetworkRows(httpEvent)[0].body.label).toBe('Kept')
  })

  it('stops the redirect chain at a hop the Location does not name', () => {
    const rows = inspectorNetworkRows(event({
      network_entries: [
        { method: 'GET', url: 'https://shop.example.test/', status_code: 302, response_headers: { Location: '/elsewhere' } },
        { method: 'GET', url: 'https://shop.example.test/home', status_code: 200 },
      ],
    }))
    expect(rows.map(row => row.role)).toEqual(['self', null])
  })

  it('marks a browser action’s document as this page and follows browser redirect links', () => {
    const rows = inspectorNetworkRows(event({
      method: undefined, action_type: 'navigate', target: 'https://shop.example.test/start',
      network_entries: [
        { url: 'https://shop.example.test/start', resource_type: 'document', status_code: 301, browser_evidence: { request_id: 'r1' } as never },
        { url: 'https://shop.example.test/landing', resource_type: 'document', status_code: 200, browser_evidence: { request_id: 'r2', redirect_from_request_id: 'r1', state: 'finished' } as never },
        { url: 'https://shop.example.test/x.woff2', resource_type: 'font', browser_evidence: { request_id: 'r3', state: 'failed' } as never },
      ],
    }))
    expect(rows.map(row => [row.role, row.type, row.failed])).toEqual([['page', 'document', false], ['redirect', 'document', false], [null, 'font', true]])
  })

  it('reads the HAR body state when no resource matched', () => {
    const rows = inspectorNetworkRows(event({
      target: '/other',
      network_entries: [
        { url: '/a', har: { response_body: { state: 'size_limit' } } },
        { url: '/b', har: { response_body: { state: 'retained', text: '{"a":1}', mime_type: 'application/json' } } },
        { url: '/c' },
      ],
    }))
    expect(rows.map(row => row.body.label)).toEqual(['Not kept (size limit)', 'Kept', 'Unknown'])
    expect(inspectorNetworkKeptBody(rows[1])).toEqual({ text: '{"a":1}', headers: { 'Content-Type': 'application/json' } })
    expect(inspectorNetworkKeptBody(rows[0])).toBeNull()
  })

  it('reads snapshot network entries when the event carries none', () => {
    expect(inspectorNetworkRows(event({ snapshot: { network_entries: [{ url: '/z.png' }] } })).map(row => row.type)).toEqual(['image'])
  })

  it('counts rows and failures for the tab badge', () => {
    expect(inspectorNetworkCounts(httpEvent)).toEqual({ total: 5, failed: 1 })
    expect(inspectorNetworkCounts(event())).toEqual({ total: 0, failed: 0 })
  })

  it('has sub-resources only beyond the own request and its redirects', () => {
    expect(hasInspectorNetworkSubresources(httpEvent)).toBe(true)
    expect(hasInspectorNetworkSubresources(event({ network_entries: [{ method: 'GET', url: 'https://shop.example.test/' }] }))).toBe(false)
    expect(hasInspectorNetworkSubresources(event({ snapshot: { resources: [resource('/a.css')] } }))).toBe(true)
  })

  it('offers All, Failed and each present type, and filters by them', () => {
    const rows = inspectorNetworkRows(httpEvent)
    const filters = inspectorNetworkFilters(rows)
    expect(filters.map(option => `${option.label} ${option.count}`)).toEqual(['All 5', 'Failed 1', 'Scripts 1', 'Styles 1', 'Images 1', 'Other 2'])
    expect(filterInspectorNetworkRows(rows, 'failed').map(row => row.display)).toEqual(['/img/keys.png'])
    expect(filterInspectorNetworkRows(rows, 'image').map(row => row.display)).toEqual(['/img/keys.png'])
    expect(filterInspectorNetworkRows(rows, 'all')).toHaveLength(5)
  })

  it('leaves out Failed with no failures and types when only one is present', () => {
    const rows = inspectorNetworkRows(event({ target: '/x', network_entries: [{ url: '/a.png' }, { url: '/b.png' }] }))
    expect(inspectorNetworkFilters(rows).map(option => option.id)).toEqual(['all'])
  })

  it('summarizes kept bodies and resources capture limits left out', () => {
    const rows = inspectorNetworkRows(httpEvent)
    expect(inspectorNetworkBodySummary(rows, 2)).toBe('3 bodies kept · 1 not kept · 2 more requests not recorded (capture limits)')
    expect(inspectorNetworkBodySummary(rows.slice(2, 3))).toBe('1 body kept · 0 not kept')
    // With no rows listed there is nothing for "more" to be more than.
    expect(inspectorNetworkBodySummary([], 1)).toBe('1 request not recorded (capture limits)')
    expect(inspectorNetworkBodySummary([], 3)).toBe('3 requests not recorded (capture limits)')
    expect(inspectorNetworkBodySummary(inspectorNetworkRows(event({ target: '/x', network_entries: [{ url: '/a' }] })))).toBeNull()
  })
})

describe('inspectorNetworkPhases', () => {
  it('lists each phase with its duration or why it is unknown', () => {
    const entry: BrowserReplayNetworkEntry = { har: { phases: [{ name: 'dns', start_ms: 1, end_ms: 3.5 }, { name: 'tls', unavailable_reason: 'reused' }, { name: 'request_wait', start_ms: 5, end_ms: 4 }] } }
    expect(inspectorNetworkPhases({ entry })).toEqual([
      { name: 'dns', label: 'DNS', durationMs: 2.5, unavailableReason: '' },
      { name: 'tls', label: 'TLS', durationMs: null, unavailableReason: 'reused' },
      { name: 'request_wait', label: 'Waiting', durationMs: null, unavailableReason: '' },
    ])
    expect(inspectorNetworkPhases({})).toEqual([])
    expect(inspectorNetworkPhases({ entry: { browser_evidence: { phases: [{ name: 'response', start_ms: 0, end_ms: 9 }] } as never } })[0].label).toBe('Receive')
  })
})
