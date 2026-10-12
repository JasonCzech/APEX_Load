import { describe, expect, it } from 'vitest'
import type { TestSummary, LiveMetrics } from '../types'
import { commandRunHealth, commandRunThroughput, commandRunTiming, commandRunWaitReason, filterCommandRuns } from './commandCenterOverviewModel'

const now = Date.parse('2026-09-12T12:00:00Z')
const run = (id: string, status: TestSummary['status'] = 'RUNNING'): TestSummary => ({ id, name: 'Same name', ref_key: `REF-${id}`, status, groups: [], total_vusers: 10, duration: '10m', created_at: '2026-09-12T11:55:00Z', started_at: '2026-09-12T11:55:00Z' })
const metrics = (errors = 0): LiveMetrics => ({ timestamp: new Date(now).toISOString(), active_vusers: 10, tps: 10, total_transactions: 90, total_errors: errors, error_pct: 99, avg_response_ms: 100, p50_ms: 80, p90_ms: 150, p95_ms: 160, p99_ms: 200 })

describe('Command Center overview evidence', () => {
  it('uses attempts for overall error rate and separates missing/stale evidence from health', () => {
    expect(commandRunHealth({ ...run('errors'), live_metrics: metrics(10) }, now)).toMatchObject({ errorPct: 10, label: 'Errors reported', stale: false })
    expect(commandRunHealth(run('missing'), now).label).toBe('No samples')
    expect(commandRunHealth({ ...run('stale'), live_metrics: { ...metrics(), timestamp: '0001-01-01T00:00:00Z' } }, now).label).toBe('Metrics stale')
    expect(commandRunHealth({ ...run('aged'), live_metrics: metrics() }, now + 30_001).stale).toBe(true)
    expect(commandRunHealth({ ...run('empty'), live_metrics: { ...metrics(), total_transactions: 0 } }, now)).toMatchObject({ hasSamples: false, errorPct: undefined })
  })

  it('counts a run that sends requests by its requests, run to date, as its own page does', () => {
    // A script without transactions: no transactions pass, its failures are requests.
    const requestsOnly: LiveMetrics = { ...metrics(), tps: 0, total_transactions: 0, total_errors: 3, request_rate: 30, run_total_requests: 200, run_request_errors: 50 }
    expect(commandRunHealth({ ...run('requests'), live_metrics: requestsOnly }, now)).toMatchObject({ hasSamples: true, errorPct: 25, label: 'Errors reported' })
    expect(commandRunHealth({ ...run('clean'), live_metrics: { ...requestsOnly, total_errors: 0, run_request_errors: 0 } }, now)).toMatchObject({ hasSamples: true, errorPct: 0, label: 'No errors reported' })
    expect(commandRunThroughput({ ...run('requests'), live_metrics: requestsOnly })).toEqual({ rate: 30, unit: 'req/s' })
    expect(commandRunThroughput({ ...run('goal'), scenario_mode: 'goal_oriented', live_metrics: { ...requestsOnly, tps: 12 } })).toEqual({ rate: 12, unit: 'TPS' })
    expect(commandRunThroughput({ ...run('transactions'), live_metrics: metrics() })).toEqual({ rate: 10, unit: 'TPS' })
  })

  it('puts actionable evidence first and finds duplicate names by reference', () => {
    const good = { ...run('good'), live_metrics: metrics() }
    const bad = { ...run('bad'), live_metrics: metrics(10) }
    const waiting = run('queue', 'QUEUED')
    expect(filterCommandRuns([good, waiting, bad], '', 'all', 'attention', now).map(item => item.id)).toEqual(['bad', 'good', 'queue'])
    expect(filterCommandRuns([good, bad], 'ref-bad', 'RUNNING', 'attention', now)).toEqual([bad])
    expect(filterCommandRuns([good, bad], 'Same', 'PAUSED', 'recent', now)).toEqual([])
    expect(filterCommandRuns([good, bad], '', 'all', 'name', now).map(item => item.id)).toEqual(['bad', 'good'])
  })

  it('uses real wait reasons and does not invent capacity waits for manual starts', () => {
    expect(commandRunWaitReason(run('queue', 'QUEUED'))).toBe('Ready for manual start')
    expect(commandRunWaitReason({ ...run('pending', 'PENDING'), pending_reason: 'Project quota exhausted', queue_position: 2 })).toBe('Queue #2 · Project quota exhausted')
    expect(commandRunWaitReason(run('scheduled', 'SCHEDULED'))).toBe('Awaiting scheduled start')
    expect(commandRunHealth({ ...run('queue', 'QUEUED'), live_metrics: metrics(10) }, now).label).toBe('Not started')
  })

  it('labels estimates and avoids a false ticking countdown while paused', () => {
    expect(commandRunTiming(run('active'), now)).toEqual({ elapsed: '5m', remaining: '~5m' })
    expect(commandRunTiming(run('paused', 'PAUSED'), now).remaining).toBe('Paused')
    // Paused time is left out of the clock: the run makes it up.
    expect(commandRunTiming({ ...run('resumed'), cumulative_pause_ns: 60e9 }, now)).toEqual({ elapsed: '4m', remaining: '~6m' })
    expect(commandRunTiming({ ...run('paused-now', 'PAUSED'), paused_at: new Date(now - 30_000).toISOString() }, now).elapsed).toBe('4m 30s')
    expect(commandRunTiming({ ...run('unknown'), started_at: 'invalid' }, now).remaining).toBe('Unavailable')
    expect(commandRunTiming(run('queue', 'QUEUED'), now)).toEqual({ elapsed: 'Not started', remaining: '10m' })
  })
})

it('reports iteration progress instead of a zero-duration countdown', () => {
  const source = run('finite')
  const timing = commandRunTiming({ ...source, termination_mode: 'iterations', duration: '', groups: [{ name: 'users', vusers: 50, iterations: 200, script_id: 'script', script_name: 'Script', protocol: 'http', ramp_up: '1s', initialization: 'at_runtime' }], live_metrics: { completed_iterations: 500 } as TestSummary['live_metrics'] }, Date.parse('2026-09-12T12:00:00Z'))
  expect(timing).toEqual({ elapsed: '5m', remaining: '500 / 10000 iterations' })
})
