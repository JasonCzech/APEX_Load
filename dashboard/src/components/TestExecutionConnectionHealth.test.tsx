// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { LiveMetrics } from '../types'
import { connectionHealth } from './connectionHealthModel'
import TestExecutionConnectionHealth from './TestExecutionConnectionHealth'

function live(partial: Partial<LiveMetrics> = {}): LiveMetrics {
  return {
    timestamp: '2026-11-10T10:00:00Z', active_vusers: 10, tps: 40, avg_response_ms: 100, error_pct: 0, total_transactions: 80, total_errors: 20,
    p50_ms: 0, p90_ms: 0, p95_ms: 0, p99_ms: 0, avg_dns_ms: 0.5, avg_connect_ms: 2, avg_tls_ms: 6,
    phase_observed: { dns: 5, connect: 25, tls: 20 },
    ...partial,
  }
}

describe('connectionHealth', () => {
  it('derives reuse, new connections per second and per-event averages', () => {
    const h = connectionHealth(live())!
    expect(h.samples).toBe(100)
    expect(h.stats.reusePct).toBeCloseTo(75)
    // 80 successes at 40 tps: a 2 s window.
    expect(h.windowSeconds).toBeCloseTo(2)
    expect(h.newConnectionsPerSec).toBeCloseTo(12.5)
    expect(h.stats.avgHandshakeMs).toBeCloseTo(30)
    expect(h.stats.avgDnsLookupMs).toBeCloseTo(10)
  })

  it('assumes a 1 s window without throughput and takes the worst generator for ports', () => {
    const h = connectionHealth(live({
      tps: 0,
      resources: { tcp_time_wait: 5 } as LiveMetrics['resources'],
      generator_resources: {
        a: { generator_id: 'gen-a', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_time_wait: 900, ephemeral_port_utilization_pct: 12 } as LiveMetrics['resources'] },
        b: { generator_id: 'gen-b', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_time_wait: 4000, ephemeral_port_utilization_pct: 91 } as LiveMetrics['resources'] },
      },
    }))!
    expect(h.windowSeconds).toBeUndefined()
    expect(h.newConnectionsPerSec).toBe(25)
    expect(h.timeWait).toEqual({ value: 4000, generatorId: 'gen-b' })
    expect(h.ephemeralPortPct).toEqual({ value: 91, generatorId: 'gen-b' })
  })

  it('reads an omitted TIME_WAIT as 0 when the snapshot carried TCP diagnostics, but never the port share', () => {
    // Go drops zero counts (omitempty); tcp_established shows the socket table was read.
    const h = connectionHealth(live({
      generator_resources: { a: { generator_id: 'gen-a', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_established: 40 } as LiveMetrics['resources'] } },
    }))!
    expect(h.timeWait).toEqual({ value: 0, inferredZero: true })
    // The port share needs the port range, read separately: a missing share is unknown, not 0.
    expect(h.ephemeralPortPct).toBeUndefined()
    expect(connectionHealth(live({ resources: { tcp_established: 3 } as LiveMetrics['resources'] }))!.ephemeralPortPct).toBeUndefined()
    // A measured value on another generator still wins.
    const mixed = connectionHealth(live({
      generator_resources: {
        a: { generator_id: 'gen-a', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_established: 40 } as LiveMetrics['resources'] },
        b: { generator_id: 'gen-b', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_established: 4, tcp_time_wait: 7 } as LiveMetrics['resources'] },
      },
    }))!
    expect(mixed.timeWait).toEqual({ value: 7, generatorId: 'gen-b' })
    expect(connectionHealth(live({ resources: { tcp_established: 3 } as LiveMetrics['resources'] }))!.timeWait).toEqual({ value: 0, inferredZero: true })
  })

  it('does not read protocols that never time connection setup as perfect reuse', () => {
    // A JDBC- or gRPC-only window: nothing timed setup and no sample had a first byte.
    const h = connectionHealth(live({ avg_dns_ms: 0, avg_connect_ms: 0, avg_tls_ms: 0, phase_observed: { dns: 0, connect: 0, tls: 0, no_ttfb: 100 } }))!
    expect(h.stats.setupUnmeasured).toBe(true)
    expect(h.stats.reusePct).toBeUndefined()
    expect(h.newConnectionsPerSec).toBeUndefined()
    // HTTP keep-alive (first bytes, no new connections) is still measured reuse.
    const reused = connectionHealth(live({ phase_observed: { dns: 0, connect: 0, tls: 0, no_ttfb: 0 } }))!
    expect(reused.stats.setupUnmeasured).toBeUndefined()
    expect(reused.stats.reusePct).toBe(100)
  })

  it('leaves samples that time no setup out of reuse in a mixed-protocol window', () => {
    // 100 HTTP samples, each opening a new connection, plus 900 Kafka/JDBC samples with no first byte and no setup.
    const mixed = connectionHealth(live({ total_transactions: 1000, total_errors: 0, tps: 1000, phase_observed: { dns: 0, connect: 100, tls: 100, no_ttfb: 900 } }))!
    expect(mixed.stats.reuseScope).toBe('first_byte')
    expect(mixed.stats.reuseSamples).toBe(100)
    expect(mixed.stats.reusePct).toBe(0)
    // Some no-first-byte samples did setup (HTTP timeouts): how many could time setup is unknown, so a range.
    const bounded = connectionHealth(live({ total_transactions: 1000, total_errors: 0, tps: 1000, phase_observed: { dns: 0, connect: 100, tls: 0, no_ttfb: 900, no_ttfb_setup_ms: 50 } }))!
    expect(bounded.stats.reuseScope).toBe('bounded')
    expect(bounded.stats.reuseMinPct).toBe(0)
    expect(bounded.stats.reusePct).toBeCloseTo(90)
    // No sample without a first byte: every sample counts.
    expect(connectionHealth(live())!.stats.reuseScope).toBe('all')
  })

  it('reports missing observations and resources as unknown', () => {
    const h = connectionHealth(live({ phase_observed: undefined }))!
    expect(h.observed).toBe(false)
    expect(h.stats.reusePct).toBeUndefined()
    expect(h.timeWait).toBeUndefined()
    expect(connectionHealth(undefined)).toBeNull()
  })
})

describe('TestExecutionConnectionHealth', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  const tile = (label: string) => Array.from(container.querySelectorAll('.connection-health-tile')).find(el => el.querySelector('dt')?.textContent === label)

  it('renders the tiles and flags port exhaustion', async () => {
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={live({ generator_resources: { b: { generator_id: 'gen-b', target: 't', active_vusers: 1, timestamp: '', resources: { ephemeral_port_utilization_pct: 91 } as LiveMetrics['resources'] } } })} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('75.0%')
    // The unit is a whole transaction: one new connection anywhere counts the transaction.
    expect(tile('Transactions with no new connection')?.textContent).toContain('transaction samples opened no new connection')
    expect(tile('Transactions with no new connection')?.textContent).toContain('even one new connection')
    expect(container.textContent).not.toContain('Connection reuse')
    // It counts transaction samples, not connections: the label says so.
    expect(tile('Transactions opening connections / s')?.textContent).toContain('12.5')
    expect(tile('Transactions opening connections / s')?.textContent).toContain('counts once')
    expect(tile('TLS time per transaction with a handshake')?.textContent).toContain('30.0 ms')
    // A transaction with several handshakes counts their total: the label must not read as one handshake.
    expect(tile('TLS time per transaction with a handshake')?.textContent).toContain('counts their total')
    expect(tile('DNS time per transaction with a lookup')?.textContent).toContain('10.0 ms')
    expect(container.textContent).not.toContain('Avg TLS handshake')
    expect(tile('Ephemeral ports used')?.classList.contains('warning')).toBe(true)
    expect(tile('Ephemeral ports used')?.textContent).toContain('port exhaustion risk')
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('Not reported')
    expect(container.textContent).toContain('this window has none, so only the averages are shown')
  })

  it('shows a measured zero TIME_WAIT and an unmeasured protocol honestly', async () => {
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={live({
      avg_dns_ms: 0, avg_connect_ms: 0, avg_tls_ms: 0, phase_observed: { dns: 0, connect: 0, tls: 0, no_ttfb: 100 },
      generator_resources: { a: { generator_id: 'gen-a', target: 't', active_vusers: 1, timestamp: '', resources: { tcp_established: 40 } as LiveMetrics['resources'] } },
    })} />))
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('0')
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('Measured 0')
    expect(tile('TCP TIME_WAIT')?.textContent).not.toContain('Not reported')
    // An unreadable port range leaves the share out: not reported, never a measured 0%.
    expect(tile('Ephemeral ports used')?.textContent).toContain('Not reported')
    expect(tile('Ephemeral ports used')?.textContent).toContain('local port range could not be read')
    expect(tile('Ephemeral ports used')?.textContent).not.toContain('0.0%')
    expect(tile('Ephemeral ports used')?.textContent).not.toContain('Measured 0')
    for (const label of ['Transactions with no new connection', 'Transactions opening connections / s', 'TLS time per transaction with a handshake', 'DNS time per transaction with a lookup']) {
      expect(tile(label)?.textContent).toContain('Not measured for this protocol')
      expect(tile(label)?.textContent).not.toMatch(/100\.0%|None/)
    }
  })

  it('shows reuse over the samples that could time setup in a mixed-protocol window', async () => {
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={live({ total_transactions: 1000, total_errors: 0, tps: 1000, phase_observed: { dns: 0, connect: 100, tls: 100, no_ttfb: 900 } })} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('0.0%')
    expect(tile('Transactions with no new connection')?.textContent).not.toContain('90.0%')
    expect(tile('Transactions with no new connection')?.textContent).toContain('of 100 transaction samples with a first byte')
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={live({ total_transactions: 1000, total_errors: 0, tps: 1000, phase_observed: { dns: 0, connect: 100, tls: 0, no_ttfb: 900, no_ttfb_setup_ms: 50 } })} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('0.0%–90.0%')
  })

  it('shows not reported for an older generator and waits without metrics', async () => {
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={live({ phase_observed: undefined })} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('Not reported')
    expect(tile('Transactions with no new connection')?.textContent).toContain('predates phase observations')
    await act(async () => root.render(<TestExecutionConnectionHealth />))
    expect(container.textContent).toContain('Waiting for live metrics')
  })

  // intervalSummaryRecordToLiveMetrics: name, rate, counts and the average per
  // transaction; no resources; phase_observed only when the row stored phases.
  const restored = (partial: Partial<LiveMetrics> = {}) => live({
    phase_observed: undefined,
    transactions: { Checkout: { name: 'Checkout', tps: 40, count: 100, errors: 0, avg_ms: 100, p50_ms: 0, p90_ms: 0, p99_ms: 0 } },
    ...partial,
  })

  it('blames a restored window\'s missing phases and sockets on stored history, not on the generators', async () => {
    expect(connectionHealth(restored())!.fromHistory).toBe(true)
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={restored()} />))
    for (const label of ['Transactions with no new connection', 'TLS time per transaction with a handshake']) {
      expect(tile(label)?.textContent).toContain('Not reported')
      expect(tile(label)?.textContent).toContain('Restored from stored history')
      expect(tile(label)?.textContent).not.toContain('predates phase observations')
    }
    for (const label of ['TCP TIME_WAIT', 'Ephemeral ports used']) {
      expect(tile(label)?.textContent).toContain('Not kept in run history')
      expect(tile(label)?.textContent).not.toContain('not collected on this OS')
    }
    expect(container.textContent).toContain('This window was restored from stored history')
    expect(container.textContent).toContain('The next live window replaces it.')
  })

  it('keeps a restored row\'s stored phases and says only the sockets were not kept', async () => {
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={restored({ phase_observed: { dns: 5, connect: 25, tls: 20 } })} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('75.0%')
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('Not kept in run history')
    expect(container.textContent).not.toContain('no phases')
  })

  it('frames an ended run as its last window and reads a resource-less compacted tail as history', async () => {
    // A compacted ended-run tail: no transactions; with no resources either, it came from history.
    const tail = live({ phase_observed: undefined, transactions: undefined })
    expect(connectionHealth(tail, { runFinished: true })!.fromHistory).toBe(true)
    expect(connectionHealth(tail)!.fromHistory).toBe(false)
    // A tail compacted in the same controller session keeps its resources: still live data.
    expect(connectionHealth(live({ transactions: undefined, resources: { tcp_established: 3, tcp_time_wait: 9 } as LiveMetrics['resources'] }), { runFinished: true })!.fromHistory).toBe(false)
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={tail} runFinished />))
    expect(container.textContent).toContain('The run has ended: these are averages over the transaction samples of its last window.')
    expect(container.textContent).not.toContain('Averages over the latest window')
    expect(container.textContent).not.toContain('The next live window replaces it.')
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('Not kept in run history')
  })

  it('still names an older generator for a live window whose records carry percentiles', async () => {
    const liveOld = live({ phase_observed: undefined, transactions: { Checkout: { name: 'Checkout', tps: 40, count: 100, errors: 0, avg_ms: 100, p50_ms: 90, p90_ms: 140, p99_ms: 200 } } })
    expect(connectionHealth(liveOld)!.fromHistory).toBe(false)
    await act(async () => root.render(<TestExecutionConnectionHealth metrics={liveOld} />))
    expect(tile('Transactions with no new connection')?.textContent).toContain('predates phase observations')
    expect(tile('TCP TIME_WAIT')?.textContent).toContain('not collected on this OS')
  })
})
