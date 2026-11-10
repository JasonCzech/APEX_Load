import { describe, expect, it } from 'vitest'

import type { LiveMetrics, TxMetrics } from '../types'
import { phaseObservationStats, txObservationStats, txPercentilesReported, txPhaseVector, txRestoredWithoutPhases } from './livePhaseModel'
import {
  LIVE_WATERFALL_HOLD_WINDOWS,
  LIVE_WATERFALL_MAX_ROWS,
  advanceLiveWaterfallHold,
  liveWaterfallNodes,
  liveWaterfallSinks,
  liveWaterfallSpanMs,
  liveWaterfallSpans,
  liveWaterfallTransactionCount,
  stableMinuteNote,
  type LiveWaterfallHold,
} from './liveWaterfallModel'
import {
  EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW,
  EXEMPLAR_MAX_WINDOWS,
  EXEMPLAR_RETENTION_MS,
  STABLE_MAX_TRANSACTIONS,
  advanceLiveWindowRetention,
  advanceStableMinute,
  appendExemplarWindow,
  emptyLiveWindowRetention,
  emptyStableMinute,
  exemplarBucketRange,
  exemplarSelectionForSeries,
  exemplarWindowsInRange,
  exemplarsForTransactions,
  liveExemplarsReceived,
  liveExemplarsRetained,
  nearestExemplarWindow,
  slowestExemplarWindowInRange,
  stableMinuteBaseline,
  type StableMinuteState,
} from './liveWindowRetention'
import { isExecutionChartClick } from './testExecutionChartZoomModel'

function tx(partial: Partial<TxMetrics> = {}): TxMetrics {
  return { name: 'Checkout', count: 100, errors: 0, avg_ms: 100, p50_ms: 90, p90_ms: 140, p99_ms: 200, tps: 10, ...partial }
}

function live(partial: Partial<LiveMetrics> = {}): LiveMetrics {
  return {
    timestamp: '2026-11-10T10:00:00Z', active_vusers: 10, tps: 10, avg_response_ms: 100, error_pct: 0, total_transactions: 10, total_errors: 0,
    p50_ms: 0, p90_ms: 0, p95_ms: 0, p99_ms: 0, ...partial,
  }
}

const sum = (v: ReturnType<typeof txPhaseVector>) => v.dns + v.connect + v.tls + v.server + v.download + v.noResponse

describe('txPhaseVector', () => {
  it('uses the controller split and derives download as the remainder', () => {
    const v = txPhaseVector(tx({ avg_ms: 100, avg_dns_ms: 2, avg_connect_ms: 8, avg_tls_ms: 10, avg_ttfb_ms: 70, avg_server_ms: 50, avg_no_response_ms: 5 }))
    expect(v).toMatchObject({ dns: 2, connect: 8, tls: 10, server: 50, noResponse: 5, download: 25, serverSource: 'split' })
    expect(sum(v)).toBeCloseTo(100)
  })

  it('falls back to TTFB minus setup for an older generator', () => {
    const v = txPhaseVector(tx({ avg_ms: 100, avg_dns_ms: 5, avg_connect_ms: 5, avg_tls_ms: 0, avg_ttfb_ms: 60 }))
    expect(v).toMatchObject({ server: 50, download: 40, noResponse: 0, serverSource: 'ttfb' })
  })

  it('puts the time after setup in the no-TTFB bucket when nothing got a first byte', () => {
    const v = txPhaseVector(tx({ avg_ms: 100, avg_connect_ms: 10 }))
    expect(v).toMatchObject({ server: 0, download: 0, noResponse: 90, serverSource: 'none' })
  })

  it('treats a record with no phase figures and no percentiles (restored from history) as phases unknown, not no first byte', () => {
    // intervalTransactionRecordsToLiveMetrics keeps name, tps, count, errors and avg_ms only.
    const restored = { name: 'Checkout', tps: 10, count: 100, errors: 0, avg_ms: 120, p50_ms: 0, p90_ms: 0, p99_ms: 0 }
    expect(txRestoredWithoutPhases(restored)).toBe(true)
    expect(txPercentilesReported(restored)).toBe(false)
    expect(txPhaseVector(restored)).toEqual({ dns: 0, connect: 0, tls: 0, server: 0, download: 0, noResponse: 0, avgMs: 120, serverSource: 'unknown' })
    // An older generator's protocol with no TTFB still sends percentiles: unchanged.
    expect(txPhaseVector(tx({ avg_ms: 100 })).serverSource).toBe('none')
    expect(txRestoredWithoutPhases(tx({ avg_ms: 100 }))).toBe(false)
    const [node] = liveWaterfallNodes({ Checkout: restored })
    expect(node).toMatchObject({ phases: [], durationMs: 120, derived: true, timingSource: 'duration' })
    expect(node.detail).toContain('restored from history')
    // Not counted toward the dominant phase.
    expect(liveWaterfallSinks([node], { Checkout: restored }).some(s => s.id === 'dominant-phase')).toBe(false)
  })

  it('leaves restored records out of the first stable minute', () => {
    let state = emptyStableMinute()
    const restored = { name: 'R', tps: 10, count: 100, errors: 0, avg_ms: 120, p50_ms: 0, p90_ms: 0, p99_ms: 0 }
    const transactions = { R: restored, L: tx({ name: 'L', avg_ms: 40, avg_server_ms: 30, avg_no_response_ms: 0 }) }
    ;[10, 10, 10, 10].forEach((v, i) => { state = advanceStableMinute(state, live({ active_vusers: v, transactions }), i * 1000) })
    state = advanceStableMinute(state, live({ active_vusers: 10, transactions }), 64_000)
    const baseline = stableMinuteBaseline(state)!
    expect(Object.keys(baseline.rows)).toEqual(['L'])
  })

  it('clamps download at zero when phases overshoot the average', () => {
    const v = txPhaseVector(tx({ avg_ms: 50, avg_connect_ms: 30, avg_server_ms: 40, avg_no_response_ms: 0 }))
    expect(v.download).toBe(0)
  })
})

describe('phase observation stats', () => {
  it('turns per-sample averages into when-it-happened figures', () => {
    const stats = txObservationStats(tx({ count: 100, avg_tls_ms: 3, avg_dns_ms: 1, avg_connect_ms: 2, phase_observed: { dns: 10, connect: 20, tls: 20, no_ttfb: 4 } }))
    // The 4 samples with no first byte and no setup are left out of reuse.
    expect(stats.reuseScope).toBe('first_byte')
    expect(stats.reusePct).toBeCloseTo((1 - 20 / 96) * 100)
    expect(stats.avgHandshakeMs).toBeCloseTo(15)
    expect(stats.avgDnsLookupMs).toBeCloseTo(10)
    expect(stats.avgConnectMs).toBeCloseTo(10)
    expect(stats.noTtfbSamples).toBe(4)
  })

  it('leaves reuse undefined when no sample timed setup or a first byte (gRPC, JDBC, messaging)', () => {
    const stats = phaseObservationStats({ dns: 0, connect: 0, tls: 0, no_ttfb: 50 }, 50, {})
    expect(stats).toEqual({ samples: 50, noTtfbSamples: 50, setupUnmeasured: true })
    // Some first bytes: keep-alive reuse is measured.
    expect(phaseObservationStats({ dns: 0, connect: 0, tls: 0, no_ttfb: 10 }, 50, {}).reusePct).toBe(100)
    // Timeouts that still opened connections: measured.
    expect(phaseObservationStats({ dns: 0, connect: 5, tls: 0, no_ttfb: 50 }, 50, {}).setupUnmeasured).toBeUndefined()
    // A mixed window: unmeasured samples do not count as reused connections.
    expect(phaseObservationStats({ dns: 0, connect: 10, tls: 0, no_ttfb: 90 }, 100, {})).toMatchObject({ reuseScope: 'first_byte', reuseSamples: 10, reusePct: 0 })
    expect(phaseObservationStats({ dns: 0, connect: 10, tls: 0, no_ttfb: 90, no_ttfb_setup_ms: 4 }, 100, {})).toMatchObject({ reuseScope: 'bounded', reuseSamples: 10, reuseMinPct: 0, reusePct: 90 })
  })

  it('reports nothing when observations are unknown or a phase never happened', () => {
    expect(phaseObservationStats(undefined, 100, {})).toEqual({ samples: 100 })
    expect(phaseObservationStats({ dns: 0, connect: 0, tls: 0 }, 10, { tls: 0 }).avgHandshakeMs).toBeUndefined()
  })
})

describe('live waterfall card layout', () => {
  it('spans the whole chart grid from the route\'s static stylesheet, not the lazy chunk', async () => {
    // One grid cell folds the waterfall's Status column (the error share) away.
    // The lazy panel's loading and error cards render before (or without) its
    // chunk, so the span must not live in the chunk's stylesheet.
    // @ts-expect-error node:fs is available in the Vitest runtime.
    const { readFileSync } = await import('node:fs')
    const routeCss = readFileSync(new URL('./TestExecution.css', import.meta.url), 'utf8') as string
    expect(routeCss).toMatch(/\.chart-card\.live-waterfall-card\s*\{[^}]*grid-column:\s*1 \/ -1/)
    const chunkCss = readFileSync(new URL('./LiveWaterfallPanel.css', import.meta.url), 'utf8') as string
    expect(chunkCss).not.toMatch(/\.live-waterfall-card\s*\{[^}]*grid-column/)
    // Both execution surfaces load it statically, and the error card carries the class.
    for (const surface of ['./TestExecution.tsx', './CommandCenterPrototype.tsx']) {
      expect(readFileSync(new URL(surface, import.meta.url), 'utf8') as string).toMatch(/^import '\.\/TestExecution\.css'/m)
    }
    const charts = readFileSync(new URL('./TestExecutionCharts.tsx', import.meta.url), 'utf8') as string
    expect(charts).toContain('className="chart-card lazy-load-error live-waterfall-card"')
  })
})

describe('liveWaterfallNodes', () => {
  const transactions = {
    Fast: tx({ name: 'Fast', avg_ms: 20, avg_server_ms: 15, avg_no_response_ms: 0, phase_observed: { dns: 0, connect: 0, tls: 0 } }),
    Slow: tx({ name: 'Slow', avg_ms: 300, avg_connect_ms: 10, avg_server_ms: 200, avg_no_response_ms: 40, phase_observed: { dns: 0, connect: 50, tls: 0 } }),
    Idle: tx({ name: 'Idle', count: 0 }),
  }

  it('builds one aggregate row per transaction, slowest first, starting at 0', () => {
    const nodes = liveWaterfallNodes(transactions)
    expect(nodes.map(n => n.label)).toEqual(['Slow', 'Fast'])
    expect(nodes.every(n => n.startMs === 0 && n.timingSource === 'aggregate' && !n.ghost && !n.delta)).toBe(true)
    const slow = nodes[0]
    expect(slow.phases.map(p => p.key)).toEqual(['connect', 'wait', 'receive', 'total'])
    // No-response time is the unsplit 'total' bucket, drawn last.
    expect(slow.phases[3]).toEqual({ key: 'total', startMs: 260, endMs: 300 })
    expect(slow.durationMs).toBe(300)
    expect(slow.detail).toContain('50% of transactions opened no connection')
    expect(liveWaterfallSpanMs(nodes)).toBe(300)
  })

  it('names the error share in the status and fails a mostly failing row', () => {
    const nodes = liveWaterfallNodes({
      A: tx({ name: 'A', errors: 10 }),
      B: tx({ name: 'B', count: 5, errors: 5 }),
      C: tx({ name: 'C', count: 1000, errors: 990 }),
      D: tx({ name: 'D' }),
    })
    const byName = (name: string) => nodes.find(n => n.label === name)!
    expect(byName('A')).toMatchObject({ status: 'passed', statusLabel: '10% errors' })
    expect(byName('B')).toMatchObject({ status: 'failed', statusLabel: 'Failing · 100% errors' })
    // 99% failing must not read as Passed.
    expect(byName('C')).toMatchObject({ status: 'failed', statusLabel: 'Failing · 99% errors' })
    expect(byName('D')).toMatchObject({ status: 'passed', statusLabel: 'Passed' })
  })

  it('keeps transactions named like Object.prototype members from corrupting the baseline', () => {
    let state = emptyStableMinute()
    const names = ['constructor', 'toString', 'valueOf', 'hasOwnProperty', '__proto__']
    const transactions = Object.fromEntries(names.map(n => [n, tx({ name: n, avg_ms: 40, avg_server_ms: 30, avg_no_response_ms: 0, count: 10 })]))
    ;[10, 10, 10, 10].forEach((v, i) => { state = advanceStableMinute(state, live({ active_vusers: v, transactions }), i * 1000) })
    state = advanceStableMinute(state, live({ active_vusers: 10, transactions }), 64_000)
    const baseline = stableMinuteBaseline(state)!
    for (const name of ['constructor', 'toString', 'valueOf', 'hasOwnProperty']) {
      expect(baseline.rows[name].avgMs).toBeCloseTo(40)
      expect(Number.isFinite(baseline.rows[name].server)).toBe(true)
    }
    // A baseline without the name never resolves an inherited member.
    const empty = { startMs: 0, endMs: 60_000, vusers: 1, windows: 1, rows: {} }
    const nodes = liveWaterfallNodes(transactions, { baseline: empty, showBaseline: true })
    expect(nodes.every(n => !n.ghost && n.delta?.outcome === 'added')).toBe(true)
    expect(Number.isFinite(liveWaterfallSpanMs(nodes))).toBe(true)
    const withBase = liveWaterfallNodes(transactions, { baseline, showBaseline: true })
    expect(liveWaterfallSpanMs(withBase)).toBeCloseTo(40)
  })

  it('caps the rows but takes the dominant phase over every transaction', () => {
    const transactions: Record<string, TxMetrics> = {}
    for (let i = 0; i < LIVE_WATERFALL_MAX_ROWS; i++) transactions[`slow-${i}`] = tx({ name: `slow-${i}`, count: 1, avg_ms: 1000 + i, avg_server_ms: 1000 + i, avg_no_response_ms: 0 })
    // 150 fast, busy transactions dominated by DNS fall past the cap.
    for (let i = 0; i < 150; i++) transactions[`fast-${i}`] = tx({ name: `fast-${i}`, count: 10_000, avg_ms: 10, avg_dns_ms: 9, avg_server_ms: 1, avg_no_response_ms: 0 })
    const nodes = liveWaterfallNodes(transactions)
    expect(nodes).toHaveLength(LIVE_WATERFALL_MAX_ROWS)
    expect(liveWaterfallTransactionCount(transactions)).toBe(LIVE_WATERFALL_MAX_ROWS + 150)
    expect(liveWaterfallSinks(nodes).find(s => s.id === 'dominant-phase')!.label).toMatch(/^server/)
    expect(liveWaterfallSinks(nodes, transactions).find(s => s.id === 'dominant-phase')!.label).toMatch(/^DNS/)
  })

  it('notes that download holds no-first-byte time for an older generator', () => {
    const [node] = liveWaterfallNodes({ A: tx({ name: 'A', avg_ms: 100, avg_ttfb_ms: 40 }) })
    expect(node.payload!.vector.serverSource).toBe('ttfb')
    expect(node.detail).toContain('download includes samples with no first byte')
    expect(liveWaterfallNodes({ A: tx({ name: 'A', avg_ms: 100, avg_server_ms: 40, avg_no_response_ms: 0 }) })[0].detail).not.toContain('no first byte')
  })

  it('says reuse is not measured for a protocol that times no setup', () => {
    const [node] = liveWaterfallNodes({ Q: tx({ name: 'Q', avg_ms: 20, phase_observed: { dns: 0, connect: 0, tls: 0, no_ttfb: 100 } }) })
    expect(node.detail).toContain('reuse not measured')
    expect(node.detail).not.toContain('100% of transactions opened no connection')
  })

  it('words a first-byte-only or bounded reuse share by its basis, not as a share of every transaction', () => {
    // 50 HTTP samples with a first byte (25 connected) and 50 gRPC samples with
    // no TTFB and no setup: 50% of the first-byte samples, but 75 of 100 in all.
    const [mixed] = liveWaterfallNodes({ M: tx({ name: 'M', avg_ms: 50, avg_server_ms: 20, avg_no_response_ms: 10, phase_observed: { dns: 0, connect: 25, tls: 0, no_ttfb: 50 } }) })
    expect(txObservationStats(mixed.payload!.tx).reuseScope).toBe('first_byte')
    expect(mixed.detail).toContain('50% of transactions with a first byte opened no connection')
    expect(mixed.detail).not.toMatch(/of transactions opened no connection/)
    // Some no-first-byte samples did setup: the share is over those that could time it.
    const [bounded] = liveWaterfallNodes({ B: tx({ name: 'B', avg_ms: 50, avg_server_ms: 20, avg_no_response_ms: 10, phase_observed: { dns: 0, connect: 25, tls: 0, no_ttfb: 50, no_ttfb_setup_ms: 4 } }) })
    expect(txObservationStats(bounded.payload!.tx).reuseScope).toBe('bounded')
    expect(bounded.detail).toContain('of transactions that could time setup opened no connection')
  })

  it('holds a transaction missing from a window for a few windows, then drops it', () => {
    const window = (ts: string, transactions: Record<string, TxMetrics>) => live({ timestamp: ts, transactions })
    const both = { Login: tx({ name: 'Login', count: 1, avg_ms: 400 }), Browse: tx({ name: 'Browse' }) }
    let hold: LiveWaterfallHold = advanceLiveWaterfallHold({}, window('2026-11-10T10:00:00Z', both))
    const browseOnly = { Browse: tx({ name: 'Browse' }) }
    hold = advanceLiveWaterfallHold(hold, window('2026-11-10T10:00:01Z', browseOnly))
    let nodes = liveWaterfallNodes(browseOnly, { held: hold })
    const login = nodes.find(n => n.label === 'Login')!
    expect(login).toMatchObject({ statusLabel: 'No samples' })
    expect(login.detail).toContain('No samples this window')
    expect(login.payload?.heldFrom).toBe('2026-11-10T10:00:00Z')
    // Held rows are listed, not this window's facts.
    expect(liveWaterfallSinks(nodes)[0].label).toBe('Slowest: Browse')
    for (let i = 0; i < LIVE_WATERFALL_HOLD_WINDOWS; i++) hold = advanceLiveWaterfallHold(hold, window(`2026-11-10T10:01:${String(i).padStart(2, '0')}Z`, browseOnly))
    nodes = liveWaterfallNodes(browseOnly, { held: hold })
    expect(nodes.map(n => n.label)).toEqual(['Browse'])
    // A fresh sample replaces the held one.
    hold = advanceLiveWaterfallHold(hold, window('2026-11-10T10:02:00Z', both))
    expect(hold.Login.windowsAgo).toBe(0)
  })

  it('keeps held rows through a window with no completions (no transactions key)', () => {
    const first = live({ timestamp: '2026-11-10T10:00:00Z', transactions: { Login: tx({ name: 'Login', count: 1, avg_ms: 400 }) } })
    let hold = advanceLiveWaterfallHold({}, first)
    // Go omits an empty map (omitempty): the next window has no transactions key.
    const quiet = live({ timestamp: '2026-11-10T10:00:01Z' })
    delete (quiet as Partial<LiveMetrics>).transactions
    hold = advanceLiveWaterfallHold(hold, quiet)
    const nodes = liveWaterfallNodes(quiet.transactions, { held: hold })
    expect(nodes.map(n => n.id)).toEqual(['tx:Login'])
    expect(nodes[0]).toMatchObject({ statusLabel: 'No samples' })
    expect(liveWaterfallSinks(nodes, quiet.transactions)).toEqual([])
    expect(liveWaterfallNodes(undefined)).toEqual([])
  })

  it('caps this window’s rows first, so held rows never push a current one out', () => {
    const current: Record<string, TxMetrics> = {}
    for (let i = 0; i < LIVE_WATERFALL_MAX_ROWS - 10; i++) current[`Cur${i}`] = tx({ name: `Cur${i}`, avg_ms: 10 })
    const held: LiveWaterfallHold = {}
    for (let i = 0; i < 20; i++) held[`Old${i}`] = { tx: tx({ name: `Old${i}`, avg_ms: 1000 + i }), lastSeen: '2026-11-10T10:00:00Z', windowsAgo: 2 }
    const nodes = liveWaterfallNodes(current, { held })
    expect(nodes).toHaveLength(LIVE_WATERFALL_MAX_ROWS)
    expect(nodes.filter(n => !n.payload?.heldFrom)).toHaveLength(LIVE_WATERFALL_MAX_ROWS - 10)
    // The slowest held rows fill the space left.
    expect(nodes.filter(n => n.payload?.heldFrom).map(n => n.label).sort()).toEqual(Array.from({ length: 10 }, (_, i) => `Old${19 - i}`).sort())
    // Slowest first overall.
    expect(nodes[0].label).toBe('Old19')
  })

  it('claims no current change or now-failing outcome for a held row', () => {
    const baseline = {
      startMs: 0, endMs: 60_000, vusers: 10, windows: 60,
      rows: { Rare: { dns: 0, connect: 0, tls: 0, server: 100, download: 50, noResponse: 0, avgMs: 150, serverSource: 'split' as const, count: 60, errorPct: 0 } },
    }
    const held: LiveWaterfallHold = { Rare: { tx: tx({ name: 'Rare', count: 1, errors: 1, avg_ms: 2100 }), lastSeen: '2026-11-10T10:00:00Z', windowsAgo: 12 } }
    const [row] = liveWaterfallNodes({}, { baseline, showBaseline: true, held })
    expect(row.payload?.heldFrom).toBe('2026-11-10T10:00:00Z')
    expect(row.delta).toBeUndefined()
    expect(row.ghost?.durationMs).toBe(150)
    // A current row with the same figures does get the comparison.
    const [now] = liveWaterfallNodes({ Rare: held.Rare.tx }, { baseline, showBaseline: true })
    expect(now.delta?.outcome).toBe('now_failing')
    expect(now.status).toBe('failed')
  })

  it('draws a held row neutral, not failing, though its last window failed', () => {
    const held: LiveWaterfallHold = { Checkout: { tx: tx({ name: 'Checkout', count: 10, errors: 8, avg_ms: 900 }), lastSeen: '2026-11-10T10:00:00Z', windowsAgo: 3 } }
    const [row] = liveWaterfallNodes({}, { held })
    expect(row).toMatchObject({ status: 'not_run', statusLabel: 'No samples' })
    // The earlier error share stays in the detail, beside "last seen".
    expect(row.detail).toContain('last seen')
    expect(row.detail).toContain('80% errors')
  })

  it('ghosts the first stable minute and states the change with its outcome', () => {
    const baseline = {
      startMs: 0, endMs: 60_000, vusers: 10, windows: 60,
      rows: {
        Slow: { dns: 0, connect: 0, tls: 0, server: 100, download: 50, noResponse: 0, avgMs: 150, serverSource: 'split' as const, count: 600, errorPct: 0 },
        Fast: { dns: 0, connect: 0, tls: 0, server: 15, download: 5, noResponse: 0, avgMs: 20, serverSource: 'split' as const, count: 600, errorPct: 0 },
      },
    }
    const nodes = liveWaterfallNodes({ ...transactions, New: tx({ name: 'New', avg_ms: 5 }), Broken: tx({ name: 'Broken', count: 3, errors: 3, avg_ms: 1 }) }, { baseline, showBaseline: true })
    const slow = nodes.find(n => n.label === 'Slow')!
    expect(slow.ghost?.durationMs).toBe(150)
    expect(slow.delta).toMatchObject({ ms: 150, outcome: 'same' })
    expect(slow.delta!.label).toContain('mostly server +100.0 ms')
    expect(nodes.find(n => n.label === 'New')!.delta?.outcome).toBe('added')
    expect(nodes.find(n => n.label === 'Broken')!.delta?.outcome).toBe('added')
    // Hidden overlay: no ghost.
    expect(liveWaterfallNodes(transactions, { baseline, showBaseline: false })[0].ghost).toBeUndefined()
    const sinks = liveWaterfallSinks(nodes)
    expect(sinks.map(s => s.id)).toEqual(['slowest', 'dominant-phase', 'growth'])
    expect(sinks[2].label).toContain('Slow')
  })

  it('reports now failing from a rise in the error share, not only a fully failing window', () => {
    const row = (errorPct: number) => ({ dns: 0, connect: 0, tls: 0, server: 1, download: 0, noResponse: 0, avgMs: 1, serverSource: 'split' as const, count: 10, errorPct })
    const baseline = { startMs: 0, endMs: 60_000, vusers: 1, windows: 60, rows: { B: row(0), C: row(0), D: row(1), E: row(40) } }
    const nodes = liveWaterfallNodes({
      B: tx({ name: 'B', count: 5, errors: 5 }),
      C: tx({ name: 'C', count: 1000, errors: 950 }),
      D: tx({ name: 'D', count: 1000, errors: 20 }),
      E: tx({ name: 'E', count: 1000, errors: 0 }),
    }, { baseline, showBaseline: true })
    const byName = (name: string) => nodes.find(n => n.label === name)!
    expect(byName('B').delta?.outcome).toBe('now_failing')
    expect(byName('C').delta?.outcome).toBe('now_failing')
    expect(byName('C').delta?.label).toContain('errors 0% → 95%')
    expect(byName('D').delta?.outcome).toBe('same')
    expect(byName('E').delta?.outcome).toBe('now_passing')
  })

  it('spans add up in order', () => {
    expect(liveWaterfallSpans({ dns: 1, connect: 0, tls: 2, server: 3, download: 0, noResponse: 4 })).toEqual([
      { key: 'dns', startMs: 0, endMs: 1 }, { key: 'tls', startMs: 1, endMs: 3 }, { key: 'wait', startMs: 3, endMs: 6 }, { key: 'total', startMs: 6, endMs: 10 },
    ])
  })
})

describe('stable minute selection', () => {
  const step = (state: StableMinuteState, vusers: number, tsMs: number, transactions?: Record<string, TxMetrics>) =>
    advanceStableMinute(state, live({ active_vusers: vusers, transactions }), tsMs)

  it('starts after VUsers stop rising for 3 windows and closes after 60 s', () => {
    let state = emptyStableMinute()
    const ramp = [5, 10, 15, 20, 20, 20]
    ramp.forEach((v, i) => { state = step(state, v, i * 1000) })
    expect(state.phase).toBe('waiting')
    state = step(state, 20, 6000, { Checkout: tx({ avg_ms: 100, avg_server_ms: 80, avg_no_response_ms: 0, count: 10 }) })
    expect(state).toMatchObject({ phase: 'collecting', startMs: 6000, vusers: 20 })
    state = step(state, 19, 30_000, { Checkout: tx({ avg_ms: 200, avg_server_ms: 180, avg_no_response_ms: 0, count: 30 }) })
    expect(stableMinuteBaseline(state)).toBeNull()
    state = step(state, 20, 66_000, { Checkout: tx({ avg_ms: 999, count: 1000 }) })
    expect(state.phase).toBe('ready')
    const baseline = stableMinuteBaseline(state)!
    expect(baseline).toMatchObject({ startMs: 6000, endMs: 66_000, vusers: 20, windows: 2 })
    // Count-weighted: (100*10 + 200*30) / 40; the window at 66 s is outside the minute.
    expect(baseline.rows.Checkout.avgMs).toBeCloseTo(175)
    expect(baseline.rows.Checkout.server).toBeCloseTo(155)
    // Fixed once ready.
    expect(step(state, 500, 70_000)).toBe(state)
  })

  it('starts over when the ramp resumes while collecting, and resets at zero VUsers', () => {
    let state = emptyStableMinute()
    ;[10, 10, 10, 10].forEach((v, i) => { state = step(state, v, i * 1000) })
    expect(state.phase).toBe('collecting')
    // Within the tolerance: still the same minute.
    state = step(state, 11, 4500)
    expect(state.phase).toBe('collecting')
    state = step(state, 20, 5000)
    expect(state.phase).toBe('waiting')
    expect(state.lastVusers).toBe(20)
    expect(step(state, 0, 6000)).toEqual(emptyStableMinute())
  })

  it('marks a baseline capped at STABLE_MAX_TRANSACTIONS and does not call a dropped transaction unseen', () => {
    const many = Object.fromEntries(Array.from({ length: STABLE_MAX_TRANSACTIONS + 5 }, (_, i) => [`T${i}`, tx({ name: `T${i}`, count: 5 })]))
    let state = emptyStableMinute()
    ;[10, 10, 10].forEach((v, i) => { state = step(state, v, i * 1000) })
    state = step(state, 10, 3000, many)
    expect(state.truncated).toBe(true)
    state = step(state, 10, 64_000)
    const baseline = stableMinuteBaseline(state)!
    expect(baseline.truncated).toBe(true)
    expect(Object.keys(baseline.rows)).toHaveLength(STABLE_MAX_TRANSACTIONS)
    const dropped = `T${STABLE_MAX_TRANSACTIONS + 2}`
    const node = liveWaterfallNodes({ [dropped]: tx({ name: dropped }) }, { baseline, showBaseline: true })[0]
    expect(node.delta?.label).not.toContain('not seen')
    expect(node.delta?.label).toContain(`first ${STABLE_MAX_TRANSACTIONS} transactions`)
    expect(stableMinuteNote(state)).toContain(`only the first ${STABLE_MAX_TRANSACTIONS} transactions`)
    // An uncapped baseline still says a missing transaction was not seen.
    const small = liveWaterfallNodes({ X: tx({ name: 'X' }) }, { baseline: { ...baseline, truncated: undefined }, showBaseline: true })[0]
    expect(small.delta?.label).toBe('not seen in the first stable minute')
  })

  it('says no first stable minute was captured once the run has ended', () => {
    const waiting = emptyStableMinute()
    expect(stableMinuteNote(waiting)).toContain('starts once active VUsers stop rising')
    const ended = stableMinuteNote(waiting, { finished: true })
    expect(ended).toContain('the run ended before one was received')
    expect(ended).not.toContain('starts once')
    let collecting = emptyStableMinute()
    ;[10, 10, 10, 10].forEach((v, i) => { collecting = step(collecting, v, i * 1000) })
    expect(collecting.phase).toBe('collecting')
    expect(stableMinuteNote(collecting, { finished: true })).toContain('No first stable minute was captured')
    // A minute that did complete is still named after the run ends.
    let ready = emptyStableMinute()
    ;[10, 10, 10].forEach((v, i) => { ready = step(ready, v, i * 1000) })
    ready = step(ready, 10, 3000, { Checkout: tx() })
    ready = step(ready, 10, 64_000)
    expect(ready.phase).toBe('ready')
    expect(stableMinuteNote(ready, { finished: true })).toContain('First stable minute this page received')
  })
})

describe('exemplar retention', () => {
  const exemplar = (traceId: string, reason = 'sample') => ({ trace_id: traceId, duration_ms: 12, success: true, reason, timestamp: '2026-11-10T10:00:00Z' })
  const withExemplars = (names: string[]) => live({ transactions: Object.fromEntries(names.map(n => [n, tx({ name: n, exemplars: [exemplar(`${n}-trace`)] })])) })

  it('keeps windows with exemplars, trims by age and caps the window count', () => {
    let ring = appendExemplarWindow([], withExemplars(['A']), 0, 't0')
    ring = appendExemplarWindow(ring, live({ transactions: { B: tx({ name: 'B' }) } }), 1000, 't1')
    expect(ring.map(w => w.tsMs)).toEqual([0])
    ring = appendExemplarWindow(ring, withExemplars(['A', 'B']), EXEMPLAR_RETENTION_MS + 1, 'late')
    expect(ring.map(w => w.tsMs)).toEqual([EXEMPLAR_RETENTION_MS + 1])
    let big = ring
    for (let i = 0; i < EXEMPLAR_MAX_WINDOWS + 5; i++) big = appendExemplarWindow(big, withExemplars(['A']), EXEMPLAR_RETENTION_MS + 2 + i, `w${i}`)
    expect(big.length).toBe(EXEMPLAR_MAX_WINDOWS)
  })

  it('records when a window carried exemplars for more transactions than it keeps', () => {
    const names = Array.from({ length: EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW + 7 }, (_, i) => `T${String(i).padStart(3, '0')}`)
    const [window] = appendExemplarWindow([], withExemplars(names), 0, 't')
    expect(window.transactions.length).toBe(EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW)
    expect(window.truncated).toEqual({ seen: EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW + 7, kept: EXEMPLAR_MAX_TRANSACTIONS_PER_WINDOW })
    const [small] = appendExemplarWindow([], withExemplars(['A']), 0, 't')
    expect(small.truncated).toBeUndefined()
  })

  it('replaces a replayed window and keeps time order', () => {
    let ring = appendExemplarWindow([], withExemplars(['A']), 2000, 'a')
    ring = appendExemplarWindow(ring, withExemplars(['B']), 1000, 'b')
    ring = appendExemplarWindow(ring, withExemplars(['C']), 2000, 'c')
    expect(ring.map(w => [w.tsMs, w.transactions[0].name])).toEqual([[1000, 'B'], [2000, 'C']])
  })

  it('resolves the nearest window within the tolerance and filters transactions', () => {
    const ring = [0, 10_000].reduce((r, ts) => appendExemplarWindow(r, withExemplars(['A', 'B']), ts, String(ts)), [] as ReturnType<typeof appendExemplarWindow>)
    expect(nearestExemplarWindow(ring, 9000)?.tsMs).toBe(10_000)
    expect(nearestExemplarWindow(ring, 5100, 4000)).toBeUndefined()
    expect(exemplarsForTransactions(ring[0], ['B']).map(t => t.name)).toEqual(['B'])
    expect(exemplarsForTransactions(ring[0], []).length).toBe(2)
  })

  it('resolves a bucketed chart point to its span and the span\'s slowest window', () => {
    // aggregateByGranularity keys buckets by floor(ts / bucket).
    expect(exemplarBucketRange(12_345, 10)).toEqual({ startMs: 10_000, endMs: 20_000 })
    expect(exemplarBucketRange(12_345, 0)).toBeUndefined()
    expect(exemplarBucketRange(Number.NaN, 10)).toBeUndefined()
    const at = (ts: number, name: string, ms: number) => ({ tsMs: ts, timestamp: String(ts), transactions: [{ name, exemplars: [{ ...exemplar(`${name}-${ts}`), duration_ms: ms }] }] })
    const ring = [at(10_000, 'A', 50), at(13_000, 'B', 900), at(17_000, 'A', 400), at(20_000, 'A', 5000)]
    const range = { startMs: 10_000, endMs: 20_000 }
    expect(exemplarWindowsInRange(ring, range).map(w => w.tsMs)).toEqual([10_000, 13_000, 17_000])
    expect(slowestExemplarWindowInRange(ring, range, [])?.tsMs).toBe(13_000)
    // Only the chart's transactions count.
    expect(slowestExemplarWindowInRange(ring, range, ['A'])?.tsMs).toBe(17_000)
    // None of the chart's transactions: the span's first window.
    expect(slowestExemplarWindowInRange(ring, range, ['C'])?.tsMs).toBe(10_000)
    expect(slowestExemplarWindowInRange(ring, { startMs: 30_000, endMs: 40_000 }, [])).toBeUndefined()
  })

  it('keeps each transaction’s groups and filters by them', () => {
    const metrics = live({ transactions: {
      A: tx({ name: 'A', groups: ['GroupA'], exemplars: [exemplar('a-trace')] }),
      B: tx({ name: 'B', groups: ['GroupB'], exemplars: [exemplar('b-trace')] }),
      C: tx({ name: 'C', exemplars: [exemplar('c-trace')] }),
    } })
    const [window] = appendExemplarWindow([], metrics, 0, 't')
    expect(window.transactions.find(t => t.name === 'A')!.groups).toEqual(['GroupA'])
    expect(exemplarsForTransactions(window, [], ['GroupA']).map(t => t.name)).toEqual(['A'])
    expect(exemplarsForTransactions(window, ['C'], ['GroupB']).map(t => t.name)).toEqual(['B', 'C'])
  })

  it('selects every transaction when the chart shows its total, else the visible transactions and groups', () => {
    const transactionName = (key: string) => (key.startsWith('transaction:') ? key.split(':')[1] : undefined)
    const options = { totalKey: 'avgMs', metric: 'avgMs', groupNames: ['GroupA', 'GroupB'], transactionName }
    expect(exemplarSelectionForSeries(['avgMs', 'transaction:Login:avgMs'], options)).toEqual({ transactionNames: [], groups: [] })
    expect(exemplarSelectionForSeries(['transaction:Login:avgMs', 'GroupA_avgMs'], options)).toEqual({ transactionNames: ['Login'], groups: ['GroupA'] })
    expect(exemplarSelectionForSeries(['GroupB_avgMs'], options)).toEqual({ transactionNames: [], groups: ['GroupB'] })
    expect(exemplarSelectionForSeries(['p95', 'GroupA_p95'], { ...options, totalKey: 'p95', metric: 'p95' })).toEqual({ transactionNames: [], groups: [] })
  })

  it('tells a page that received live windows from one that did not', () => {
    const none = emptyLiveWindowRetention()
    expect(liveExemplarsReceived(none)).toBe(false)
    expect(liveExemplarsReceived(undefined)).toBe(false)
    const plain = advanceLiveWindowRetention(none, live(), '2026-11-10T10:00:00Z')
    expect(liveExemplarsReceived(plain)).toBe(true)
    expect(liveExemplarsRetained(plain)).toBe(false)
    expect(liveExemplarsRetained(advanceLiveWindowRetention(plain, withExemplars(['A']), '2026-11-10T10:00:01Z'))).toBe(true)
  })

  it('advances both retentions per window and ignores a bad timestamp', () => {
    const start = emptyLiveWindowRetention()
    const next = advanceLiveWindowRetention(start, withExemplars(['A']), '2026-11-10T10:00:00Z')
    expect(next.exemplars).toHaveLength(1)
    expect(next.firstWindowMs).toBe(Date.parse('2026-11-10T10:00:00Z'))
    expect(advanceLiveWindowRetention(next, withExemplars(['A']), 'not a time')).toBe(next)
  })
})

describe('chart click versus zoom drag', () => {
  it('is a click only when the pointer barely moved', () => {
    expect(isExecutionChartClick({ x: 10, y: 10 }, { x: 12, y: 13 })).toBe(true)
    expect(isExecutionChartClick({ x: 10, y: 10 }, { x: 30, y: 10 })).toBe(false)
    expect(isExecutionChartClick(null, { x: 1, y: 1 })).toBe(false)
    expect(isExecutionChartClick({ x: Number.NaN, y: 0 }, { x: 0, y: 0 })).toBe(false)
  })
})
