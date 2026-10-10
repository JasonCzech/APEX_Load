import { describe, expect, it } from 'vitest'

import type { RunArchiveReport } from '../../types'
import {
  formatRcaEffect,
  formatRcaWindow,
  normalizeRcaAssessment,
  parseRcaSeries,
  parseRcaWindow,
  rcaErrorPatterns,
  rcaHypothesisAnomalies,
  rcaHypothesisWindow,
  rcaLoadSteps,
  rcaPhaseMarkers,
  rcaSeriesLabel,
  rcaSeriesPopulation,
  rcaTimelinePhases,
  rcaTimelineRows,
  rcaTone,
  rcaValidityConcerns,
} from './rcaModel'
import { rcaTestAssessment } from './rcaTestFixture'

describe('rcaModel', () => {
  it('fills every list the controller sent as null', () => {
    const a = normalizeRcaAssessment(rcaTestAssessment())
    expect(a.validity.limitations).toEqual([])
    expect(a.detection?.spikes).toEqual([])
    expect(a.graph?.edges).toEqual([])
    expect(a.capacity).toEqual([])
    expect(a.littles_law).toEqual([])
    expect(a.hypotheses[1].chain).toEqual([])
    const bare = normalizeRcaAssessment({ ...rcaTestAssessment(), anomalies: null as never, hypotheses: null as never, coverage: null as never, limitations: null as never })
    expect([bare.anomalies, bare.hypotheses, bare.coverage, bare.limitations]).toEqual([[], [], [], []])
  })

  it('parses and labels each kind of series', () => {
    expect(parseRcaSeries('transaction/Cart/Add item/p95_ms')).toEqual({ dimension: 'transaction', name: 'Cart/Add item', metric: 'p95_ms' })
    expect(rcaSeriesLabel('action/Login/error_rate')).toBe('Login · error rate')
    expect(rcaSeriesLabel('dependency/http:api.shop.local/dns_ms')).toBe('http:api.shop.local · DNS time')
    expect(rcaSeriesLabel('imported/0f3c2a9e-1111-2222-3333-444455556666/host.cpu.usage/abcdef012345')).toBe('host.cpu.usage · imported dataset 0f3c2a9e')
    expect(rcaSeriesLabel('action_dimensions/["Login","fast-3g","","action"]/p95_ms')).toBe('Login · fast-3g · action · p95 latency')
    expect(rcaSeriesLabel('browser_document/lcp/3fa9c1e0b2d4/p75')).toBe('LCP p75 · tuple 3fa9c1e0')
    expect(rcaSeriesLabel('monitor/dynatrace/dt-src/builtin:service.cpu.time/abcdef012345')).toBe('builtin:service.cpu.time · Dynatrace')
    expect(rcaSeriesLabel('odd')).toBe('odd')
    expect(rcaSeriesPopulation('transaction/Checkout/p95_ms')).toEqual({ population: 'transaction', name: 'Checkout' })
    expect(rcaSeriesPopulation('dependency/http:api/dns_ms')).toBeNull()
  })

  it('formats effects with their units and sign', () => {
    expect(formatRcaEffect({ effect: 312.4, unit: 'ms' })).toBe('+312 ms')
    expect(formatRcaEffect({ effect: -4.25, unit: '%' })).toBe('−4.25 pts')
    expect(formatRcaEffect({ effect: 12.5, unit: '1/s' })).toBe('+12.5/s')
    expect(formatRcaEffect({ effect: Number.NaN, unit: 'ms' })).toBe('Not recorded')
    // A small change keeps two significant digits rather than reading as zero.
    expect(formatRcaEffect({ effect: 0.0012, unit: '%' })).toBe('+0.0012 pts')
    expect(formatRcaEffect({ effect: -0.5, unit: 'ms' })).toBe('−0.5 ms')
  })

  it('round-trips windows and rejects malformed ones', () => {
    const w = { start: Date.parse('2026-10-05T12:01:25Z'), end: Date.parse('2026-10-05T12:06:55Z') }
    expect(formatRcaWindow(w)).toBe('2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z')
    expect(parseRcaWindow(formatRcaWindow(w))).toEqual(w)
    expect(parseRcaWindow('2026-10-05T12:06:55Z~2026-10-05T12:01:25Z')).toBeNull()
    expect(parseRcaWindow('nonsense')).toBeNull()
    expect(parseRcaWindow(undefined)).toBeNull()
  })

  it('spans a hypothesis from its earliest possible onset with context', () => {
    const a = normalizeRcaAssessment(rcaTestAssessment())
    const h = a.hypotheses[0]
    expect(rcaHypothesisAnomalies(h, a.anomalies).map(x => [x.anomaly.id, x.role])).toEqual([['anomaly-sym', 'symptom'], ['anomaly-cpu', 'cause']])
    // 12:02:20 (onset less its uncertainty) to 12:06:00 is 220 s; a quarter
    // of it, 55 s, pads each side.
    expect(formatRcaWindow(rcaHypothesisWindow(h, a.anomalies)!)).toBe('2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z')
    expect(rcaHypothesisWindow({ ...h, symptoms: ['gone'], chain: [] }, a.anomalies)).toBeNull()
  })

  it('places load steps before their settle period', () => {
    const levels = normalizeRcaAssessment(rcaTestAssessment()).detection!.levels
    expect(rcaLoadSteps(levels, 30e9).map(step => [new Date(step.at).toISOString(), step.label])).toEqual([
      ['2026-10-05T12:00:00.000Z', 'Load step to 10 VUsers'],
      ['2026-10-05T12:02:00.000Z', 'Load step to 20 VUsers'],
    ])
    expect(rcaLoadSteps([{ start: '2026-10-05T12:00:30Z', end: '2026-10-05T12:01:00Z', rate: 50 }], 0)[0].label).toBe('Load step to 50/s')
  })

  it('reads archived phase events, skipping what it cannot read', () => {
    const jsonl = [
      '{"schema":"apexload.phase_event.v1","kind":"level_reached","level":20,"target":"checkout","at":"2026-10-05T12:02:00Z","deliveries":[{"sink":"grafana","format":"grafana","status":"delivered"}]}',
      'not json',
      '{"kind":"ramp_start","level":20,"target":"checkout","at":"2026-10-05T12:01:30Z"}',
      '{"kind":"stop","at":"2026-10-05T12:10:00Z"}',
      '{"kind":"pause"}',
      '',
    ].join('\n')
    expect(rcaPhaseMarkers(jsonl).map(m => [new Date(m.at).toISOString().slice(11, 19), m.label])).toEqual([
      ['12:01:30', 'checkout began ramping to 20 VUsers'],
      ['12:02:00', 'checkout reached 20 VUsers'],
      ['12:10:00', 'The run stopped'],
    ])
  })

  it('infers load steps when the archived events mark no level', () => {
    const inferred = rcaLoadSteps([{ start: '2026-10-05T12:00:30Z', end: '2026-10-05T12:02:00Z', vusers: 4 }], 30e9)
    // A run that held one level archives only its stop.
    const stopOnly = rcaPhaseMarkers('{"kind":"stop","at":"2026-10-05T12:02:00Z"}')
    const held = rcaTimelinePhases(stopOnly, inferred)
    expect(held.inferred).toBe(true)
    expect(held.phases.map(p => p.label)).toEqual(['Load step to 4 VUsers', 'The run stopped'])
    // Archived level events are used as they are.
    const ramped = rcaPhaseMarkers('{"kind":"level_reached","level":4,"at":"2026-10-05T12:00:05Z"}\n{"kind":"stop","at":"2026-10-05T12:02:00Z"}')
    expect(rcaTimelinePhases(ramped, inferred)).toEqual({ phases: ramped, inferred: false })
    expect(rcaTimelinePhases(null, inferred)).toEqual({ phases: inferred, inferred: true })
    expect(rcaTimelinePhases(null, [])).toEqual({ phases: [], inferred: false })
  })

  it('draws each interval at its start, as onsets are', () => {
    const point = (iso: string, p95?: number) => ({ time: iso, timestampMs: Date.parse(iso), vusers: 20, tps: 5, avgMs: 1, errorPct: 0, p50: 1, p90: 2, p95, p99: 3 })
    // Summaries carry interval ends, 10 s apart, here out of order.
    const history = [point('2026-10-05T12:00:20Z', 300), point('2026-10-05T12:00:10Z', 200), point('2026-10-05T12:00:40Z'), point('2026-10-05T12:00:30Z', 400)]
    expect(rcaTimelineRows(history, null).map(row => [new Date(row.t).toISOString().slice(11, 19), row.p95])).toEqual([
      ['12:00:00', 200], ['12:00:10', 300], ['12:00:20', 400], ['12:00:30', 2],
    ])
    const window = { start: Date.parse('2026-10-05T12:00:10Z'), end: Date.parse('2026-10-05T12:00:20Z') }
    expect(rcaTimelineRows(history, window).map(row => row.p95)).toEqual([300, 400])
    expect(rcaTimelineRows([point('2026-10-05T12:00:10Z', 1)], null).map(row => new Date(row.t).toISOString())).toEqual(['2026-10-05T12:00:10.000Z'])
  })

  it('keeps the spans that were not valid and tones the controller statuses', () => {
    const windows = normalizeRcaAssessment(rcaTestAssessment()).validity.windows
    expect(rcaValidityConcerns(windows).map(w => w.verdict)).toEqual(['suspect'])
    expect(['passed', 'breached', 'not_configured', 'unknown', 'suspect', 'invalid'].map(rcaTone)).toEqual(['success', 'danger', 'info', 'info', 'warning', 'danger'])
  })

  it('filters error patterns to the window and the symptom\'s names', () => {
    const window = { start: Date.parse('2026-10-05T12:02:00Z'), end: Date.parse('2026-10-05T12:04:00Z') }
    const pattern = (signature: string, first: string, latest: string, actions?: string[]) => ({ signature, sample: signature, count: 1, first_seen: first, latest_seen: latest, actions })
    const report = { error_patterns: [
      pattern('inside', '2026-10-05T12:03:00Z', '2026-10-05T12:03:30Z', ['Checkout']),
      pattern('spanning', '2026-10-05T12:00:00Z', '2026-10-05T12:09:00Z'),
      pattern('other action', '2026-10-05T12:03:00Z', '2026-10-05T12:03:30Z', ['Login']),
      pattern('before', '2026-10-05T12:00:00Z', '2026-10-05T12:01:00Z', ['Checkout']),
    ] } as unknown as RunArchiveReport
    expect(rcaErrorPatterns(report, ['Checkout'], window).map(p => p.signature)).toEqual(['inside', 'spanning'])
    expect(rcaErrorPatterns(report, ['Checkout'], null).map(p => p.signature)).toEqual(['inside', 'spanning', 'before'])
  })
})
