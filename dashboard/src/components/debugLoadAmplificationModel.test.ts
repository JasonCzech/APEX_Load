import { describe, expect, it } from 'vitest'
import { vi } from 'vitest'
import { amplificationFactor, buildAmplificationModel, debugRequestSample, fetchAmplificationLoadReport, formatFactor } from './debugLoadAmplificationModel'
import { amplificationReport, amplificationSnapshot } from './__fixtures__/debugLoadAmplification'

describe('debug vs load amplification model', () => {
  it('re-splits debug timings with the archive rule and marks rows that cannot compare', () => {
    const [login, , , home, redirect, asset] = amplificationSnapshot().report!.events
    expect(debugRequestSample(login, 0).phases).toEqual({ dns: 5, connect: 10, tls: 15, server: 20, download: 50 })
    expect(debugRequestSample(home, 3).phases).toEqual({ dns: 2, connect: 4, tls: 0, server: 14, download: 20 })
    expect(debugRequestSample(redirect, 4)).toMatchObject({ phases: null, notComparable: 'redirect' })
    expect(debugRequestSample(asset, 5)).toMatchObject({ phases: null, notComparable: 'har_only' })
    expect(debugRequestSample({ ...login, protocol_evidence: undefined, network_entries: undefined }, 0)).toMatchObject({ phases: null, notComparable: 'total_only' })
    // No TTFB: setup is split, server and download are not measured.
    expect(debugRequestSample({ ...login, protocol_evidence: { timings: { dns_ms: 5, duration_ms: 100 } } }, 0).phases).toEqual({ dns: 5, connect: 0, tls: 0, server: null, download: null })
  })

  it('joins in-transaction requests by step, action and occurrence, falling back to name and occurrence', () => {
    const model = buildAmplificationModel(amplificationSnapshot(), amplificationReport(), 'p95')
    expect(model.rows.map(row => [row.debug.actionName, row.basis])).toEqual([
      ['Login', 'anatomy'], ['Search', 'anatomy'], ['Search', 'anatomy'], ['Home', 'mean_only'], ['Redirected', 'not_comparable'], ['Asset', 'not_comparable'],
    ])
    const [login, search1, search2, home] = model.rows
    expect(login).toMatchObject({ transactionName: 'Checkout flow', fallbackKey: false, occurrence: 1 })
    expect(login.load).toEqual({ dns: 10, connect: 20, tls: 15, server: 124, download: 55 })
    expect(formatFactor(login.factors.dns)).toBe('×2.0')
    expect(formatFactor(login.factors.server)).toBe('×6.2')
    expect(search1).toMatchObject({ fallbackKey: true, occurrence: 1, load: { server: 80 } })
    expect(search2).toMatchObject({ fallbackKey: true, occurrence: 2, load: { server: 66 } })
    // Debug Search made no DNS lookup and neither did load.
    expect(search1.factors.dns).toEqual({ kind: 'na' })
    // Outside a transaction: run-wide means, server = mean TTFB minus mean setup, no download.
    expect(home.load).toEqual({ dns: 2, connect: 8, tls: 0, server: 40, download: null })
    expect(formatFactor(home.server)).toBe('×2.9')
    expect(model.counts).toEqual({ requests: 6, anatomy: 3, meanOnly: 1, unmatched: 0, notComparable: 2 })
  })

  it('summarizes server and network amplification from means over transaction rows only', () => {
    const model = buildAmplificationModel(amplificationSnapshot(), amplificationReport(), 'p95')
    expect(model.summary.basis).toBe('anatomy')
    // Means add up; P95s do not. Server: (62 + 40 + 33) / (20 + 40 + 30).
    expect(formatFactor(model.summary.server)).toBe('×1.5')
    // Network: (5 + 10 + 7.5 + 27.5 + 35 + 17) / (80 + 60 + 20).
    expect(formatFactor(model.summary.network)).toBe('×0.6')
    expect(model.summary.headline).toBe('Server ×1.5, network ×0.6 under load (mean load vs debug, summed over 3 requests matched by transaction). Phase percentiles do not add up, so these totals use means; the table shows P95 per phase. 1 request compared only with run-wide means is not in these totals.')
    // The row's network sum is from means too; its per-phase factors keep the statistic.
    expect(model.rows[0].loadMean).toEqual({ dns: 5, connect: 10, tls: 7.5, server: 62, download: 27.5 })
    expect(formatFactor(model.rows[0].network)).toBe('×0.6')
    const avg = buildAmplificationModel(amplificationSnapshot(), amplificationReport(), 'avg')
    expect(avg.rows[0].load?.server).toBe(62)
    expect(avg.summary.headline).toBe('Server ×1.5, network ×0.6 under load (mean load vs debug, summed over 3 requests matched by transaction). 1 request compared only with run-wide means is not in these totals.')
  })

  it('says new once, and names a shakeout run as unloaded', () => {
    const report = amplificationReport()
    for (const row of report.time_anatomy!.transactions[0].requests) row.phases.server = { ...row.phases.server, avg_ms: 40 }
    const snapshot = amplificationSnapshot()
    for (const event of snapshot.report!.events.slice(0, 3)) {
      const timings = event.protocol_evidence!.timings as Record<string, number>
      // TTFB equal to setup: about zero server wait in debug.
      timings.ttfb_ms = (timings.dns_ms ?? 0) + (timings.connect_ms ?? 0) + (timings.tls_ms ?? 0) + 0.1
    }
    const model = buildAmplificationModel(snapshot, report, 'avg')
    expect(model.summary.server).toEqual({ kind: 'new' })
    expect(model.summary.headline).toMatch(/^Server new, network .* under load \(/)
    expect(model.summary.headline.match(/under load/g)).toHaveLength(1)
    const shakeout = buildAmplificationModel(amplificationSnapshot(), amplificationReport(), 'avg', { shakeout: true })
    expect(shakeout.summary.headline).toContain('in the shakeout run (unloaded)')
    expect(shakeout.summary.headline).not.toContain('under load')
  })

  it('falls back by name only on a per-step occurrence unique on both sides', () => {
    const snapshot = amplificationSnapshot()
    // Load: GET /cart once at step 3, twice at step 7. A step was inserted
    // since, so debug runs them at steps 4, 8, 8: no exact match.
    snapshot.report!.events.slice(0, 3).forEach((event, index) => Object.assign(event, {
      action_name: 'GET /cart', source_step_index: [4, 8, 8][index], protocol_evidence: { timings: { ttfb_ms: 40, duration_ms: 50 } },
    }))
    const report = amplificationReport()
    const cart = (step: number, occurrence: number, server: number) => ({
      ...report.time_anatomy!.transactions[0].requests[0], key: `${step}|GET /cart#${occurrence}`, action_name: 'GET /cart', source_step_index: step, occurrence,
      phases: { ...report.time_anatomy!.transactions[0].requests[0].phases, server: { ...report.time_anatomy!.transactions[0].requests[0].phases.server, p95_ms: server } },
    })
    report.time_anatomy!.transactions[0].requests = [cart(3, 1, 11), cart(7, 1, 22), cart(7, 2, 33)]
    const [step4, first8, second8] = buildAmplificationModel(snapshot, report, 'p95').rows
    // GET /cart#1 is ambiguous on both sides (steps 4 and 8 in debug, 3 and 7 in load).
    expect(step4).toMatchObject({ basis: 'unmatched', anatomyMiss: 'no_row' })
    expect(first8).toMatchObject({ basis: 'unmatched', anatomyMiss: 'no_row' })
    // GET /cart#2 is unique on both sides: the second call at step 8 is 7|GET /cart#2.
    expect(second8).toMatchObject({ basis: 'anatomy', fallbackKey: true, occurrence: 2, load: { server: 33 } })
  })

  it('numbers occurrences over every claimed request, comparable or not', () => {
    const snapshot = amplificationSnapshot()
    // The first Search fails with no timings: the archive still numbers it #1.
    Object.assign(snapshot.report!.events[1], { protocol_evidence: undefined, status_code: 500, success: false })
    const model = buildAmplificationModel(snapshot, amplificationReport(), 'p95')
    expect(model.rows[1]).toMatchObject({ basis: 'not_comparable', claimedBy: 'Checkout flow' })
    expect(model.rows[2]).toMatchObject({ basis: 'anatomy', occurrence: 2, load: { server: 66 } })
  })

  it('leaves out server and network where load often got no first byte', () => {
    const report = amplificationReport()
    const login = report.time_anatomy!.transactions[0].requests[0]
    login.phases.server = { ...login.phases.server, observed_pct: 70 }
    const model = buildAmplificationModel(amplificationSnapshot(), report, 'p95')
    expect(model.rows[0]).toMatchObject({ basis: 'anatomy', loadNoFirstBytePct: 30, server: { kind: 'na' }, network: { kind: 'na' } })
    expect(model.rows[0].factors.server).toEqual({ kind: 'na' })
    expect(model.rows[0].factors.download).toEqual({ kind: 'na' })
    expect(formatFactor(model.rows[0].factors.dns)).toBe('×2.0')
    expect(model.summary).toMatchObject({ requests: 2, excluded: 1 })
    expect(model.summary.headline).toContain('1 matched request is left out: under load it often had no TTFB split (timeouts, or a protocol without TTFB)')
    login.phases.server = { ...login.phases.server, observed_pct: 98 }
    expect(buildAmplificationModel(amplificationSnapshot(), report, 'p95').rows[0].loadNoFirstBytePct).toBeUndefined()
  })

  it('does not blame load for a protocol that records no TTFB on either side', () => {
    const report = amplificationReport()
    const login = report.time_anatomy!.transactions[0].requests[0]
    // gRPC / WebSocket: the load anatomy has no TTFB share at all.
    login.phases.server = { ...login.phases.server, observed_pct: 0 }
    const snapshot = amplificationSnapshot()
    const timings = snapshot.report!.events[0].protocol_evidence!.timings as Record<string, unknown>
    delete timings.ttfb_ms
    const model = buildAmplificationModel(snapshot, report, 'p95')
    expect(model.rows[0]).toMatchObject({ basis: 'anatomy', noTtfbSplit: true, server: { kind: 'na' } })
    expect(model.rows[0].loadNoFirstBytePct).toBeUndefined()
    expect(formatFactor(model.rows[0].factors.dns)).toBe('×2.0')
    expect(model.rows[0].network.kind).toBe('ratio')
    expect(model.summary.excluded).toBe(0)
    expect(model.summary.headline).not.toContain('no TTFB split')
    expect(model.summary.headline).not.toContain('first byte')
  })

  it('tells requests outside a transaction from claimed requests the load anatomy left out', () => {
    const report = amplificationReport()
    report.network_phases!.by_action!.Login = { count: 4, avg_dns_ms: 5, avg_connect_ms: 10, avg_tls_ms: 15, avg_ttfb_ms: 70 }
    const capped = structuredClone(report)
    capped.time_anatomy!.transactions = []
    capped.time_anatomy!.truncation = { truncated: true, reasons: ['transaction_cap'], transactions_tracked: 1, transactions_not_emitted: 1 } as never
    let model = buildAmplificationModel(amplificationSnapshot(), capped, 'p95')
    expect(model.rows[0]).toMatchObject({ basis: 'mean_only', claimedBy: 'Checkout flow', anatomyMiss: 'transaction_capped' })
    expect(model.rows[3]).toMatchObject({ basis: 'mean_only' })
    expect(model.rows[3].claimedBy).toBeUndefined()
    const folded = structuredClone(report)
    folded.time_anatomy!.transactions[0].requests = folded.time_anatomy!.transactions[0].requests.slice(1)
    folded.time_anatomy!.transactions[0].requests_truncated = true
    model = buildAmplificationModel(amplificationSnapshot(), folded, 'p95')
    expect(model.rows[0]).toMatchObject({ basis: 'mean_only', anatomyMiss: 'row_capped' })
  })

  it('falls back to means when no transaction row matches, and says so', () => {
    const report = amplificationReport()
    report.time_anatomy = undefined
    report.network_phases!.by_action!.Login = { count: 4, avg_dns_ms: 5, avg_connect_ms: 10, avg_tls_ms: 15, avg_ttfb_ms: 70 }
    const model = buildAmplificationModel(amplificationSnapshot(), report, 'p95')
    expect(model.rows[0].basis).toBe('mean_only')
    expect(model.rows[1].basis).toBe('unmatched')
    expect(model.summary.basis).toBe('mean_only')
    expect(model.summary.headline).toContain('run-wide means only')
    // Every compared row is in the mean-only totals: nothing left out to name.
    expect(model.summary.headline).not.toContain('not in these totals')
    // Run-wide means have no download split: network is connection setup only, and says so.
    expect(model.rows[0].networkSetupOnly).toBe(true)
    expect(model.summary.networkSetupOnly).toBe(true)
    expect(model.summary.headline).toMatch(/connection setup \(DNS, connect, TLS\) /)
    expect(model.summary.headline).not.toMatch(/, network /)
    expect(model.summary.headline).toContain('Run-wide means have no download split, so download is not compared.')
    const empty = buildAmplificationModel(amplificationSnapshot(), {}, 'p95')
    expect(empty.hasLoadPhaseData).toBe(false)
    expect(empty.summary).toMatchObject({ basis: 'none', headline: 'No debug request could be matched to this load run.' })
  })

  it('does not let many sub-millisecond rows add up to a headline network factor', () => {
    const snapshot = amplificationSnapshot()
    // Every in-transaction request spends ~0.2 ms on the network in debug: each row is "n/a".
    for (const event of snapshot.report!.events.slice(0, 3)) {
      const ttfb = Number(event.protocol_evidence!.timings!.ttfb_ms)
      event.protocol_evidence = { timings: { ttfb_ms: ttfb, duration_ms: ttfb + 0.2 } }
      event.duration_ms = ttfb + 0.2
    }
    const model = buildAmplificationModel(snapshot, amplificationReport(), 'avg')
    expect(model.rows.slice(0, 3).every(row => row.network.kind !== 'ratio')).toBe(true)
    expect(model.summary.network.kind).not.toBe('ratio')
    expect(model.summary.headline).toMatch(/^Server ×[\d.]+, network (new|n\/a) under load/)
  })

  it('guards factors against tiny and zero debug values', () => {
    expect(amplificationFactor(0, 5)).toEqual({ kind: 'new' })
    expect(amplificationFactor(0.2, 0.3)).toEqual({ kind: 'na' })
    expect(amplificationFactor(null, 5)).toEqual({ kind: 'na' })
    expect(amplificationFactor(5, null)).toEqual({ kind: 'na' })
    expect(formatFactor(amplificationFactor(10, 62))).toBe('×6.2')
    expect(formatFactor(amplificationFactor(10, 120))).toBe('×12')
    expect(formatFactor(amplificationFactor(10, 0))).toBe('×0.0')
    expect(formatFactor({ kind: 'new' })).toBe('new')
    expect(formatFactor({ kind: 'na' })).toBe('n/a')
  })
})

describe('fetchAmplificationLoadReport', () => {
  it('asks for the report without the release trend and keeps only the compared fields', async () => {
    const full = { ...amplificationReport(), by_transaction: [{ name: 'big' }], release_trend: { runs: [] } }
    const getReport = vi.fn().mockResolvedValue(full)
    const value = await fetchAmplificationLoadReport(getReport, 'peak')
    expect(getReport).toHaveBeenCalledWith('peak', { includeReleaseTrend: false })
    expect(Object.keys(value).sort()).toEqual(['network_phases', 'time_anatomy'])
    expect(value.time_anatomy).toBe(full.time_anatomy)
  })
})
