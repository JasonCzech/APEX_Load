import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import {
  buildDebugTimelineModel,
  formatTimelineDurationMs,
} from './debugTimelineWaterfallModel'

const baseEvent = (overrides: Partial<BrowserReplayEvent> = {}): BrowserReplayEvent => ({
  phase: 'action',
  action_index: 0,
  action_name: 'Fetch profile',
  action_type: 'request',
  method: 'GET',
  target: 'https://api.example.test/v1/profile',
  status_code: 200,
  success: true,
  started_at: '2026-11-07T12:00:00.000Z',
  completed_at: '2026-11-07T12:00:00.100Z',
  duration_ms: 100,
  ...overrides,
})

describe('debugTimelineWaterfallModel', () => {
  it('handles empty event lists in both waterfall and stacked modes', () => {
    const waterfall = buildDebugTimelineModel([], 'waterfall')
    expect(waterfall.rows).toEqual([])
    expect(waterfall.sessionSpanMs).toBe(0)
    expect(waterfall.rulerTicks).toEqual(['0 ms', '25 ms', '50 ms', '75 ms', '100 ms'])

    const stacked = buildDebugTimelineModel([], 'stacked')
    expect(stacked.rows).toEqual([])
    expect(stacked.rulerTicks).toEqual(['0%', '25%', '50%', '75%', '100%'])
  })

  it('de-overlaps HAR connect and tls phases and synthesizes queueing when start_ms > 0', () => {
    const ev = baseEvent({
      duration_ms: 95,
      network_entries: [
        {
          url: 'https://api.example.test/v1/profile',
          method: 'GET',
          status_code: 200,
          duration_ms: 95,
          har: {
            id: '1',
            started_at: '2026-11-07T12:00:00.000Z',
            phases: [
              { name: 'dns', start_ms: 2, end_ms: 12 },
              // harConnectIncludesTLS extends connect.end_ms (25) to tls.end_ms (45)
              { name: 'connect', start_ms: 12, end_ms: 45 },
              { name: 'tls', start_ms: 25, end_ms: 45 },
              { name: 'send', start_ms: 45, end_ms: 48 },
              { name: 'request_wait', start_ms: 48, end_ms: 83 },
              { name: 'response', start_ms: 83, end_ms: 95 },
            ],
          },
        },
      ],
    })

    const model = buildDebugTimelineModel([ev], 'waterfall')
    expect(model.rows).toHaveLength(1)
    const row = model.rows[0]
    expect(row.timingSource).toBe('har')
    expect(row.segments.map(s => [s.key, Number(s.durationMs.toFixed(1))])).toEqual([
      ['queueing', 2],
      ['dns', 10],
      ['connect', 13],
      ['tls', 20],
      ['send', 3],
      ['wait', 35],
      ['receive', 12],
    ])
    const widthSum = row.segments.reduce((acc, s) => acc + s.widthPct, 0)
    expect(widthSum).toBeCloseTo(100, 4)
  })

  it('builds per-hop sub-rows and combined segments for multi-hop redirect chains', () => {
    const ev = baseEvent({
      action_name: 'Submit order',
      duration_ms: 140,
      network_entries: [
        {
          url: 'https://api.example.test/orders',
          method: 'POST',
          status_code: 307,
          duration_ms: 40,
          har: {
            id: 'hop-1',
            started_at: '2026-11-07T12:00:00.000Z',
            phases: [
              { name: 'dns', start_ms: 0, end_ms: 5 },
              { name: 'connect', start_ms: 5, end_ms: 20 },
              { name: 'tls', start_ms: 12, end_ms: 20 },
              { name: 'send', start_ms: 20, end_ms: 22 },
              { name: 'request_wait', start_ms: 22, end_ms: 38 },
              { name: 'response', start_ms: 38, end_ms: 40 },
            ],
          },
        },
        {
          url: 'https://api.example.test/orders/submit',
          method: 'POST',
          status_code: 200,
          duration_ms: 100,
          trace: { connection_reused: true },
          har: {
            id: 'hop-2',
            started_at: '2026-11-07T12:00:00.040Z',
            phases: [
              { name: 'send', start_ms: 0, end_ms: 3 },
              { name: 'request_wait', start_ms: 3, end_ms: 85 },
              { name: 'response', start_ms: 85, end_ms: 100 },
            ],
          },
        },
      ],
    })

    const model = buildDebugTimelineModel([ev], 'waterfall')
    const row = model.rows[0]
    expect(row.hops).toHaveLength(2)
    expect(row.hops[0].label).toContain('Hop 1 · 307 POST')
    expect(row.hops[1].label).toContain('Hop 2 · 200 POST')
    expect(row.connectionReused).toBe(false)
    expect(row.hops[0].connectionReused).toBe(false)
    expect(row.hops[1].connectionReused).toBe(true)

    const waitBreakdown = row.breakdown.find(b => b.key === 'wait')
    expect(waitBreakdown?.durationMs).toBeCloseTo(16 + 82, 4)
  })

  it('extracts browser resource evidence phases and availability reasons', () => {
    const ev = baseEvent({
      action_type: 'browser_navigate',
      duration_ms: 80,
      network_entries: [
        {
          url: 'https://app.example.test/dashboard',
          method: 'GET',
          status_code: 200,
          duration_ms: 80,
          browser_evidence: {
            version: 4,
            source_generation: '0123456789abcdef0123456789abcdef',
            request_id: '0123456789abcdef0123456789abcdef-1',
            redirect_hop: 0,
            observed_at: '2026-11-07T12:00:00Z',
            timing_source: 'playwright',
            protocol: 'h2',
            state: 'finished',
            bytes: {},
            phases: [
              { name: 'dns', unavailable_reason: 'not_exposed' },
              { name: 'connect', start_ms: 0, end_ms: 30 },
              { name: 'tls', start_ms: 10, end_ms: 30 },
              { name: 'request_wait', start_ms: 30, end_ms: 65 },
              { name: 'response', start_ms: 65, end_ms: 80 },
            ],
          },
        },
      ],
    })

    const row = buildDebugTimelineModel([ev]).rows[0]
    expect(row.timingSource).toBe('browser')
    expect(row.protocolLabel).toBe('HTTP/2')
    expect(row.segments.map(s => [s.key, s.durationMs])).toEqual([
      ['connect', 10],
      ['tls', 20],
      ['wait', 35],
      ['receive', 15],
    ])
    expect(row.breakdown.find(b => b.key === 'dns')?.unavailableReason).toBe('Not exposed by runtime')
  })

  it('falls back to protocol_evidence.timings for HTTP/3 QUIC and non-HAR runs', () => {
    const ev = baseEvent({
      duration_ms: 120,
      protocol_evidence: {
        http_version_negotiated: '3',
        http_version_source: 'wire',
        tls_in_transport_handshake: true,
        timing_availability: {
          dns: 'ip_literal',
          connect: 'available',
          tls: 'in_transport_handshake',
          ttfb: 'available',
        },
        timings: {
          connect_ms: 25,
          ttfb_ms: 95,
          duration_ms: 120,
        },
      },
    })

    const row = buildDebugTimelineModel([ev]).rows[0]
    expect(row.timingSource).toBe('protocol_evidence')
    expect(row.protocolLabel).toBe('HTTP/3 (QUIC)')
    // waitMs = ttfb_ms (95) - connect_ms (25) = 70; receiveMs = 120 - 95 = 25
    expect(row.segments.map(s => [s.key, s.durationMs])).toEqual([
      ['connect', 25],
      ['wait', 70],
      ['receive', 25],
    ])
    expect(row.breakdown.find(b => b.key === 'dns')?.unavailableReason).toBe('IP literal (no DNS)')
    expect(row.breakdown.find(b => b.key === 'tls')?.unavailableReason).toBe('In QUIC transport handshake')
  })

  it('falls back to a single Action execution segment for non-network actions', () => {
    const ev = baseEvent({
      action_name: 'Verify customer tier',
      action_type: 'assert',
      method: undefined,
      target: '$.tier == "gold"',
      duration_ms: 4.5,
    })

    const row = buildDebugTimelineModel([ev]).rows[0]
    expect(row.timingSource).toBe('duration')
    expect(row.segments).toHaveLength(1)
    expect(row.segments[0]).toMatchObject({
      key: 'total',
      label: 'Action execution',
      durationMs: 4.5,
      widthPct: 100,
    })
    expect(row.breakdown).toHaveLength(1)
    expect(row.breakdown[0].key).toBe('total')
  })

  it('positions bars by session offset in waterfall mode and 100% width in stacked mode', () => {
    const events = [
      baseEvent({
        event_id: 'e1',
        action_name: 'First',
        started_at: '2026-11-07T12:00:00.000Z',
        duration_ms: 50,
      }),
      baseEvent({
        event_id: 'e2',
        action_name: 'Second',
        started_at: '2026-11-07T12:00:00.050Z',
        duration_ms: 150,
      }),
    ]

    const waterfall = buildDebugTimelineModel(events, 'waterfall')
    expect(waterfall.sessionSpanMs).toBe(200)
    expect(waterfall.rows[0].trackOffsetPct).toBeCloseTo(0, 4)
    expect(waterfall.rows[0].trackWidthPct).toBeCloseTo(25, 4)
    expect(waterfall.rows[1].trackOffsetPct).toBeCloseTo(25, 4)
    expect(waterfall.rows[1].trackWidthPct).toBeCloseTo(75, 4)

    const stacked = buildDebugTimelineModel(events, 'stacked')
    expect(stacked.rows[0].trackOffsetPct).toBe(0)
    expect(stacked.rows[0].trackWidthPct).toBe(100)
    expect(stacked.rows[1].trackOffsetPct).toBe(0)
    expect(stacked.rows[1].trackWidthPct).toBe(100)
  })

  it('uses cumulative durations when events share identical synthetic timestamps', () => {
    const events = [
      baseEvent({ event_id: 's1', started_at: '2026-09-13T00:00:00Z', duration_ms: 40 }),
      baseEvent({ event_id: 's2', started_at: '2026-09-13T00:00:00Z', duration_ms: 60 }),
    ]
    const model = buildDebugTimelineModel(events, 'waterfall')
    expect(model.sessionSpanMs).toBe(100)
    expect(model.rows[0].startOffsetMs).toBe(0)
    expect(model.rows[1].startOffsetMs).toBe(40)
  })

  it('formats millisecond and second durations cleanly', () => {
    expect(formatTimelineDurationMs(0)).toBe('0.0 ms')
    expect(formatTimelineDurationMs(14.24)).toBe('14.2 ms')
    expect(formatTimelineDurationMs(1250)).toBe('1.25 s')
  })

  it('does not inflate parent duration or corrupt primaryEntry when concurrent sub-resources accompany a document request', () => {
    const ev = baseEvent({
      action_name: 'Open dashboard',
      action_type: 'browser_navigate',
      target: 'https://app.example.test/dashboard',
      status_code: undefined,
      duration_ms: 80,
      network_entries: [
        {
          url: 'https://app.example.test/dashboard',
          method: 'GET',
          status_code: 200,
          resource_type: 'document',
          duration_ms: 50,
          browser_evidence: {
            version: 4,
            source_generation: '0123456789abcdef0123456789abcdef',
            request_id: '0123456789abcdef0123456789abcdef-1',
            redirect_hop: 0,
            observed_at: '2026-11-07T12:00:00Z',
            timing_source: 'playwright',
            protocol: 'h2',
            document_offset_ms: 0,
            state: 'finished',
            bytes: {},
            phases: [
              { name: 'connect', start_ms: 0, end_ms: 10 },
              { name: 'request_wait', start_ms: 10, end_ms: 40 },
              { name: 'response', start_ms: 40, end_ms: 50 },
            ],
          },
        },
        {
          url: 'https://app.example.test/static/app.js',
          method: 'GET',
          status_code: 200,
          resource_type: 'script',
          duration_ms: 45,
          browser_evidence: {
            version: 4,
            source_generation: '0123456789abcdef0123456789abcdef',
            request_id: '0123456789abcdef0123456789abcdef-2',
            redirect_hop: 0,
            observed_at: '2026-11-07T12:00:00Z',
            timing_source: 'playwright',
            protocol: 'h2',
            document_offset_ms: 15,
            service_worker: true,
            state: 'finished',
            bytes: {},
            phases: [
              { name: 'request_wait', start_ms: 0, end_ms: 25 },
              { name: 'response', start_ms: 25, end_ms: 45 },
            ],
          },
        },
        {
          url: 'https://app.example.test/static/missing.png',
          method: 'GET',
          status_code: 404,
          resource_type: 'image',
          duration_ms: 40,
          browser_evidence: {
            version: 4,
            source_generation: '0123456789abcdef0123456789abcdef',
            request_id: '0123456789abcdef0123456789abcdef-3',
            redirect_hop: 0,
            observed_at: '2026-11-07T12:00:00Z',
            timing_source: 'playwright',
            protocol: 'h1',
            document_offset_ms: 20,
            state: 'finished',
            bytes: {},
            phases: [
              { name: 'request_wait', start_ms: 0, end_ms: 35 },
              { name: 'response', start_ms: 35, end_ms: 40 },
            ],
          },
        },
      ],
    })

    const row = buildDebugTimelineModel([ev], 'waterfall').rows[0]
    expect(row.statusCode).toBe(200)
    expect(row.protocolLabel).toBe('HTTP/2')
    expect(row.durationMs).toBe(80)
    expect(row.segments.map(s => [s.key, s.durationMs])).toEqual([
      ['connect', 10],
      ['wait', 30],
      ['receive', 10],
    ])
    expect(row.hops).toHaveLength(3)
    expect(row.hops[1].startOffsetMs).toBe(15)
    expect(row.hops[2].startOffsetMs).toBe(20)
  })

  it('does not attribute post-dial timeout duration to Content download when ttfb_ms is 0', () => {
    const headerTimeoutEvent = baseEvent({
      success: false,
      status_code: undefined,
      duration_ms: 100,
      protocol_evidence: {
        http_version_negotiated: '1.1',
        timing_availability: {
          dns: 'available',
          connect: 'available',
          tls: 'not_attempted',
          ttfb: 'no_response',
        },
        timings: {
          dns_ms: 5,
          connect_ms: 20,
          ttfb_ms: 0,
          duration_ms: 100,
        },
      },
    })

    const row = buildDebugTimelineModel([headerTimeoutEvent]).rows[0]
    expect(row.timingSource).toBe('protocol_evidence')
    expect(row.segments.map(s => [s.key, s.durationMs])).toEqual([
      ['dns', 5],
      ['connect', 20],
      ['wait', 75],
    ])
    expect(row.breakdown.find(b => b.key === 'receive')?.durationMs).toBe(0)
    expect(row.breakdown.find(b => b.key === 'tls')?.unavailableReason).toBe('Not attempted')
  })

  it('reports service worker availability reason and avoids false connection reuse on 0ms HAR connect', () => {
    const swEvent = baseEvent({
      duration_ms: 12,
      network_entries: [
        {
          url: 'https://app.example.test/offline',
          method: 'GET',
          status_code: 200,
          duration_ms: 12,
          browser_evidence: {
            version: 4,
            source_generation: '0123456789abcdef0123456789abcdef',
            request_id: '0123456789abcdef0123456789abcdef-9',
            redirect_hop: 0,
            observed_at: '2026-11-07T12:00:00Z',
            timing_source: 'playwright',
            service_worker: true,
            state: 'finished',
            bytes: {},
            phases: [{ name: 'response', start_ms: 0, end_ms: 12 }],
          },
        },
      ],
    })
    const swRow = buildDebugTimelineModel([swEvent]).rows[0]
    expect(swRow.breakdown.find(b => b.key === 'dns')?.unavailableReason).toBe('Served by service worker')

    const fastLoopbackEvent = baseEvent({
      duration_ms: 6,
      network_entries: [
        {
          url: 'http://127.0.0.1:8080/health',
          method: 'GET',
          status_code: 200,
          duration_ms: 6,
          har: {
            id: 'loopback-1',
            started_at: '2026-11-07T12:00:00.000Z',
            phases: [
              { name: 'connect', start_ms: 1, end_ms: 1 },
              { name: 'request_wait', start_ms: 1, end_ms: 5 },
              { name: 'response', start_ms: 5, end_ms: 6 },
            ],
          },
        },
      ],
    })
    const loopbackRow = buildDebugTimelineModel([fastLoopbackEvent]).rows[0]
    expect(loopbackRow.connectionReused).toBe(false)
    expect(loopbackRow.startedOffsetMs).toBe(1)
  })

  it('clamps zero-duration and end-of-session waterfall bars within 0..100% bounds', () => {
    const events = [
      baseEvent({
        event_id: 'long',
        started_at: '2026-11-07T12:00:00.000Z',
        duration_ms: 1000,
      }),
      baseEvent({
        event_id: 'zero-at-end',
        started_at: '2026-11-07T12:00:00.995Z',
        duration_ms: 0,
      }),
    ]
    const model = buildDebugTimelineModel(events, 'waterfall')
    const tailRow = model.rows[1]
    expect(tailRow.trackOffsetPct).toBeLessThanOrEqual(98)
    expect(tailRow.trackOffsetPct + tailRow.trackWidthPct).toBeLessThanOrEqual(100)
    expect(tailRow.trackWidthPct).toBe(1.5)
  })

  it('preserves protocol_evidence breakdown for cache hits, reused-connection timeouts, and pre-dial failures', () => {
    const cachedEvent = baseEvent({
      duration_ms: 2,
      protocol_evidence: {
        http_version_source: 'cache',
        timings: { duration_ms: 2 },
      },
    })
    const cachedRow = buildDebugTimelineModel([cachedEvent]).rows[0]
    expect(cachedRow.timingSource).toBe('protocol_evidence')
    expect(cachedRow.breakdown.find(b => b.key === 'dns')?.unavailableReason).toBe('Served from cache')
    expect(cachedRow.breakdown.find(b => b.key === 'wait')?.unavailableReason).toBe('Served from cache')

    const reusedTimeoutEvent = baseEvent({
      success: false,
      duration_ms: 80,
      protocol_evidence: {
        http_version_negotiated: '3',
        quic_connection: 'reused',
        timing_availability: {
          dns: 'reused_connection',
          connect: 'reused_connection',
          tls: 'reused_connection',
          ttfb: 'no_response',
        },
        timings: {
          ttfb_ms: 0,
          duration_ms: 80,
        },
      },
    })
    const reusedRow = buildDebugTimelineModel([reusedTimeoutEvent]).rows[0]
    expect(reusedRow.timingSource).toBe('protocol_evidence')
    expect(reusedRow.connectionReused).toBe(true)
    expect(reusedRow.segments.map(s => [s.key, s.durationMs])).toEqual([['wait', 80]])
    expect(reusedRow.breakdown.find(b => b.key === 'connect')?.unavailableReason).toBe('Reused connection')

    const notAttemptedEvent = baseEvent({
      success: false,
      duration_ms: 5,
      protocol_evidence: {
        quic_attempted: false,
        timing_availability: {
          dns: 'not_attempted',
          connect: 'not_attempted',
          tls: 'not_attempted',
          ttfb: 'no_response',
        },
        timings: { duration_ms: 5 },
      },
    })
    const notAttemptedRow = buildDebugTimelineModel([notAttemptedEvent]).rows[0]
    expect(notAttemptedRow.timingSource).toBe('protocol_evidence')
    expect(notAttemptedRow.breakdown.find(b => b.key === 'connect')?.unavailableReason).toBe('Not attempted')
    expect(notAttemptedRow.breakdown.find(b => b.key === 'wait')?.unavailableReason).toBe('No response')
  })

  it('follows typed redirect chains and excludes non-redirect document sub-resources', () => {
    const xhrRedirectEvent = baseEvent({
      target: 'https://api.example.test/orders',
      status_code: undefined,
      duration_ms: 90,
      network_entries: [
        {
          url: 'https://api.example.test/orders',
          method: 'POST',
          status_code: 307,
          resource_type: 'xhr',
          duration_ms: 30,
          har: {
            id: 'xhr-1',
            started_at: '2026-11-07T12:00:00.000Z',
            redirect_url: 'https://api.example.test/orders/submit',
            phases: [{ name: 'request_wait', start_ms: 0, end_ms: 30 }],
          },
        },
        {
          url: 'https://api.example.test/orders/submit',
          method: 'POST',
          status_code: 200,
          resource_type: 'xhr',
          duration_ms: 60,
          har: {
            id: 'xhr-2',
            started_at: '2026-11-07T12:00:00.000Z',
            phases: [
              { name: 'request_wait', start_ms: 5, end_ms: 45 },
              { name: 'response', start_ms: 45, end_ms: 60 },
            ],
          },
        },
      ],
    })
    const xhrRow = buildDebugTimelineModel([xhrRedirectEvent]).rows[0]
    expect(xhrRow.statusCode).toBe(200)
    expect(xhrRow.startedOffsetMs).toBe(0)
    expect(xhrRow.hops[0].startOffsetMs).toBe(0)
    expect(xhrRow.hops[1].startOffsetMs).toBe(30)
    expect(xhrRow.breakdown.find(b => b.key === 'wait')?.durationMs).toBe(70)

    const iframeSubResourceEvent = baseEvent({
      target: 'https://app.example.test/home',
      status_code: undefined,
      duration_ms: 70,
      network_entries: [
        {
          url: 'https://app.example.test/home',
          method: 'GET',
          status_code: 200,
          resource_type: 'document',
          duration_ms: 40,
          har: {
            id: 'doc-1',
            started_at: '2026-11-07T12:00:00.000Z',
            phases: [{ name: 'request_wait', start_ms: 0, end_ms: 40 }],
          },
        },
        {
          url: 'https://app.example.test/app.js',
          method: 'GET',
          status_code: 200,
          resource_type: 'script',
          duration_ms: 20,
          har: {
            id: 'js-1',
            started_at: '2026-11-07T12:00:00.010Z',
            phases: [{ name: 'response', start_ms: 0, end_ms: 20 }],
          },
        },
        {
          url: 'https://app.example.test/embed-frame',
          method: 'GET',
          status_code: 404,
          resource_type: 'document',
          duration_ms: 25,
          har: {
            id: 'iframe-1',
            started_at: '2026-11-07T12:00:00.020Z',
            phases: [{ name: 'request_wait', start_ms: 0, end_ms: 25 }],
          },
        },
      ],
    })
    const docRow = buildDebugTimelineModel([iframeSubResourceEvent]).rows[0]
    expect(docRow.statusCode).toBe(200)
    expect(docRow.breakdown.find(b => b.key === 'wait')?.durationMs).toBe(40)
  })

  it('preserves sub-millisecond RFC3339Nano offsets across fast loopback steps', () => {
    const events = [
      baseEvent({
        event_id: 'nano-1',
        started_at: '2026-11-07T12:00:00.000100Z',
        duration_ms: 0.3,
      }),
      baseEvent({
        event_id: 'nano-2',
        started_at: '2026-11-07T12:00:00.000500Z',
        duration_ms: 0.4,
      }),
    ]
    const model = buildDebugTimelineModel(events, 'waterfall')
    expect(model.rows[0].startOffsetMs).toBe(0)
    expect(model.rows[1].startOffsetMs).toBeCloseTo(0.4, 3)
  })
})
