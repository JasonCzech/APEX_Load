import { describe, expect, it } from 'vitest'

import type { ArchiveTransactionWaterfall, ArchiveWaterfallRow, RunArchiveTimeAnatomy, RunArchiveTransactionAnatomy } from '../../types/timeAnatomy'
import fixture from './__fixtures__/timeAnatomyReport.json'
import {
  EMPTY_TRANSACTIONS_COPY,
  PHASE_SPLIT_PARITY_CASES,
  aggregateRoots,
  aggregateSpanMs,
  bandEstimateCopy,
  clampedNotice,
  compositionSegments,
  compositionSentence,
  coverageNotices,
  defaultStatistic,
  anatomyAnnouncement,
  DRILLDOWN_BUSY_COPY,
  drilldownErrorCopy,
  drilldownErrorIsBusy,
  drilldownHints,
  drilldownIsPartial,
  drilldownJoinMayDiffer,
  headlineTransaction,
  honestyNote,
  othersRoot,
  pickAnatomyTransaction,
  reportPhaseRowId,
  representativeChipLabel,
  requestPhaseSplit,
  rowsHaveOnlyUnsplit,
  selectionNote,
  tailBars,
  tailStatusCopy,
  toAggregateVector,
  truncationNotices,
  truncationReasonCopy,
  notJoinedCopy,
  joinMayDifferCopy,
  partialDrilldownCopy,
  partialOnlyCopy,
  unjoinedCopy,
  waterfallRootsFromDrilldown,
  drilldownOutsideSpans,
  phaseDefinitionsText,
  presentPhaseKeys,
  traceLinkLabel,
  traceLinkNote,
} from './timeAnatomyModel'
import { childGaps, phaseTotals, selfChildTime } from '../waterfall/analysis'

const anatomy = fixture.time_anatomy as unknown as RunArchiveTimeAnatomy
const checkout = anatomy.transactions[0]
const login = anatomy.transactions[1]

function row(partial: Partial<ArchiveWaterfallRow>): ArchiveWaterfallRow {
  return {
    event_id: 'tx_00000000000000000000000000000001', ordinal: 1, population: 'action', timestamp: '2026-11-01T10:00:00.000Z',
    offset_ms: 0, duration_ms: 0, action_name: 'GET /', success: true,
    phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 },
    ...partial,
  }
}

describe('time anatomy fixture', () => {
  it('carries every optional key of the frozen contract', () => {
    const paths = new Set<string>()
    const walk = (value: unknown, path: string) => {
      if (Array.isArray(value)) { value.forEach(item => walk(item, `${path}[]`)); return }
      if (value && typeof value === 'object') {
        for (const [key, child] of Object.entries(value)) {
          paths.add(`${path}.${key}`)
          walk(child, `${path}.${key}`)
        }
      }
    }
    walk(fixture, '')
    const t = '.time_anatomy.transactions[]'
    for (const path of [
      '.time_anatomy.truncation.reasons', '.time_anatomy.truncation.transactions_omitted', '.time_anatomy.truncation.transactions_not_emitted',
      '.time_anatomy.truncation.request_rows_omitted', '.time_anatomy.truncation.pending_requests_dropped',
      `${t}.sla_status`, `${t}.other_requests`, `${t}.requests_truncated`, `${t}.tail`, `${t}.representatives`,
      `${t}.duration.percentile_quality`, `${t}.requests[].source_step_index`, `${t}.requests[].occurrence_folded`, `${t}.requests[].dependency_type`,
      `${t}.requests[].connections.reuse_pct`, `${t}.requests[].phases.dns.observed_pct`, `${t}.requests[].phases.dns.avg_when_observed_ms`,
      `${t}.tail.threshold_quality`, `${t}.tail.requests`, `${t}.tail.headline`, `${t}.tail.delta.components[].share_pct`,
      `${t}.representatives[].target_ms`, `${t}.representatives[].elapsed_ms`, `${t}.representatives[].status_code`, `${t}.representatives[].error_class`,
      `${t}.representatives[].group`, `${t}.representatives[].vuser_id`, `${t}.representatives[].vuser_lifetime`, `${t}.representatives[].iteration`,
      `${t}.representatives[].trace_id`, `${t}.representatives[].sampled`,
    ]) expect(paths.has(path), path).toBe(true)
    // The lifetime nonce crosses JSON as a string.
    for (const sample of anatomy.transactions.flatMap(entry => entry.representatives || [])) expect(typeof sample.vuser_lifetime).toBe('string')
  })

  it('keeps the decomposition identity exact', () => {
    expect(compositionSegments(checkout.decomposition).identityResidual).toBe(0)
    const delta = checkout.tail!.delta
    expect(delta.components.reduce((sum, component) => sum + component.delta_ms, 0)).toBeCloseTo(delta.total_ms, 6)
  })
})

describe('phase split parity', () => {
  it.each(PHASE_SPLIT_PARITY_CASES.map(c => [c.name, c] as const))('%s', (_, c) => {
    expect(requestPhaseSplit(c.input)).toEqual(c.expected)
  })

  it('always sums to the duration with no negative part', () => {
    let seed = 42
    const rand = () => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed / 2147483648 }
    for (let i = 0; i < 2000; i += 1) {
      const input = { duration_ms: rand() * 500, dns_ms: rand() * 100 - 20, connect_ms: rand() * 100, tls_ms: rand() * 100, ttfb_ms: rand() < 0.3 ? 0 : rand() * 600 }
      const split = requestPhaseSplit(input)
      const parts = [split.dns, split.connect, split.tls, split.server, split.download, split.unsplit]
      expect(parts.every(part => part >= 0)).toBe(true)
      expect(parts.reduce((a, b) => a + b, 0)).toBeCloseTo(input.duration_ms, 6)
    }
  })
})

describe('aggregate waterfall', () => {
  it('maps backend phase stats to the aggregate vector', () => {
    const vector = toAggregateVector(checkout.requests[1].phases)
    expect(Object.keys(vector)).toEqual(['dns', 'connect', 'tls', 'server', 'download'])
    expect(vector.dns).toEqual({ avg: 0.5, p50: 0, p90: 0, p95: 5, p99: 5, observedPct: 9 })
    expect(vector.server?.p99).toBe(460)
  })

  it('keeps backend row order and changes spans, not offsets, with the statistic', () => {
    const p50 = aggregateRoots(checkout, 'p50')
    const p95 = aggregateRoots(checkout, 'p95')
    expect(p50.map(node => node.id)).toEqual(['1|GET /cart#1', '2|POST /cart#1', '3|GET /poll#9'])
    expect(p50.map(node => node.startMs)).toEqual(p95.map(node => node.startMs))
    expect(p50.map(node => node.startMs)).toEqual([0, 12, 90])
    expect(p50[1].durationMs).not.toBe(p95[1].durationMs)
    expect(p95[1].phases.map(span => span.key)).toEqual(['dns', 'connect', 'tls', 'wait', 'receive'])
    expect(p95[1].status).toBe('incomplete')
    expect(p95[1].statusLabel).toBe(`${checkout.requests[1].failed.toLocaleString()} failed`)
    expect(p95[0].status).toBe('passed')
    expect(p95[0].statusLabel).toBe('Passed')
    // Aggregate rows say they are statistics, not one request.
    expect(p95.every(node => node.timingSource === 'aggregate')).toBe(true)
    expect(p95[2].label).toBe('GET /poll #9+')
    expect(p95[2].detail).toBe('P95 of each phase · in 60% of instances · reuse 100%')
    expect(aggregateRoots(checkout, 'avg')[0].detail).toBe('Average of each phase · reuse 90%')
  })

  it('adds the unsplit remainder as a total span', () => {
    const [node] = aggregateRoots(login, 'p95')
    expect(node.phases).toEqual([{ key: 'total', startMs: 0, endMs: 55 }])
    expect(rowsHaveOnlyUnsplit(login)).toBe(true)
    expect(rowsHaveOnlyUnsplit(checkout)).toBe(false)
  })

  it('draws Other requests muted at the origin and measures the span', () => {
    const others = othersRoot(checkout)
    expect(others).toBeTruthy()
    expect(others?.startMs).toBe(0)
    expect(others?.derived).toBe(true)
    expect(othersRoot(login)).toBeNull()
    const roots = aggregateRoots(checkout, 'p95')
    expect(aggregateSpanMs(roots)).toBe(Math.max(...roots.map(node => (node.startMs ?? 0) + (node.durationMs ?? 0))))
  })
})

describe('composition and tail', () => {
  it('describes the average instance', () => {
    const { segments } = compositionSegments(checkout.decomposition)
    expect(segments.map(segment => segment.key)).toEqual(['server', 'setup', 'download', 'non_request'])
    expect(compositionSentence('Checkout', checkout.decomposition)).toBe('Average Checkout: server 61% · setup 5% · download 15% · outside requests 18%')
    expect(compositionSentence('Checkout', { ...checkout.decomposition, overlap_ms: 4, non_request_ms: 28 })).toContain('overlap −3%')
  })

  it('keeps backend bar order and signs', () => {
    expect(tailBars(checkout.tail)).toEqual([
      { component: 'server', label: 'server', deltaMs: 400, sharePct: 90.9, sign: 'positive' },
      { component: 'connect', label: 'connect', deltaMs: 40, sharePct: 9.1, sign: 'positive' },
    ])
    expect(tailBars(login.tail).map(bar => bar.sign)).toEqual(['positive', 'positive', 'zero'])
    expect(tailBars({ ...checkout.tail!, delta: { total_ms: -5, components: [{ component: 'overlap', delta_ms: -5 }] } })[0]).toMatchObject({ sign: 'negative', sharePct: null, label: 'overlap' })
  })

  it('maps every tail status to its copy', () => {
    const tail = checkout.tail!
    expect(tailStatusCopy(tail)).toBeNull()
    expect(tailStatusCopy(login.tail)).toBe('Tail attribution needs at least 40 completed instances with 3 in the tail; this run has 29.')
    expect(tailStatusCopy({ ...tail, status: 'no_tail', threshold_p95_ms: 110 })).toBe('No tail to explain: P95 equals the median.')
    expect(tailStatusCopy({ ...tail, status: 'low_join_coverage', tail: { ...tail.tail, count: 50, joined: 35 } })).toBe('Only 70% of tail samples could be matched to their requests; attribution shown without a headline.')
    expect(bandEstimateCopy(tail)).toBe('Exact: every completed instance')
    expect(bandEstimateCopy(login.tail!)).toBe('Estimated from a uniform sample of 20 of 29')
  })

  it('blames join coverage, not the instance count, when a large run cannot fill its bands', () => {
    // 5,000 instances, but too few tail members joined: the controller still says insufficient_samples.
    const tail = { ...checkout.tail!, status: 'insufficient_samples' as const, instances: 5000, tail: { ...checkout.tail!.tail, count: 250, joined: 2 }, median: { ...checkout.tail!.median, count: 2500, joined: 2400 } }
    const copy = tailStatusCopy(tail)
    expect(copy).toBe('Too few instances could be matched to their requests to explain the tail: 2 of 250 above P95 and 2,400 of 2,500 at or below P50 (needs 3 and 10).')
    expect(copy).not.toContain('needs at least 40')
  })

  it('names the band whose join coverage is short', () => {
    const tail = { ...checkout.tail!, status: 'low_join_coverage' as const, tail: { ...checkout.tail!.tail, count: 10, joined: 10 }, median: { ...checkout.tail!.median, count: 100, joined: 70 } }
    expect(tailStatusCopy(tail)).toBe('Only 70% of median samples could be matched to their requests; attribution shown without a headline.')
    expect(tailStatusCopy({ ...tail, tail: { ...tail.tail, count: 10, joined: 6 } })).toBe('Only 60% of tail samples could be matched to their requests; attribution shown without a headline.')
  })

  it('does not claim P95 equals the median when the tail band is just empty', () => {
    const tail = { ...checkout.tail!, status: 'no_tail' as const, threshold_p50_ms: 400, threshold_p95_ms: 1000, tail: { ...checkout.tail!.tail, count: 0, joined: 0 } }
    expect(tailStatusCopy(tail)).toBe('No tail to explain: no instance ran slower than P95 (1,000 ms).')
    expect(tailStatusCopy({ ...tail, band_estimate: 'sampled' })).toBe('No tail to explain: no sampled instance ran slower than P95 (1,000 ms).')
  })

  it('announces the transaction with its headline or status copy', () => {
    expect(anatomyAnnouncement(checkout)).toBe(`Checkout: ${checkout.tail!.headline}`)
    expect(anatomyAnnouncement(login)).toBe('Login: Tail attribution needs at least 40 completed instances with 3 in the tail; this run has 29.')
    expect(anatomyAnnouncement({ ...checkout, joined_instances: 0 })).toBe(unjoinedCopy('Checkout'))
    expect(anatomyAnnouncement(undefined)).toBe('')
    expect(bandEstimateCopy(login.tail!)).toBe('Estimated from a uniform sample of 20 of 29')
  })
})

describe('copy', () => {
  it('states the honesty rule for percentiles and averages', () => {
    expect(honestyNote(checkout, 'p95')).toBe("Each bar shows the P95 of each phase on its own. Phase P95s don't add up to the transaction's P95 (130 ms). The tail card above shows what slow transactions actually spend extra time on. Rows start at their median offset; concurrent requests overlap.")
    expect(honestyNote(checkout, 'avg')).toBe('Each bar shows the average of each phase. Averages add up; rows start at their median offset.')
  })

  it('maps coverage, truncation and empty states to their copy', () => {
    expect(truncationNotices(anatomy, checkout)).toEqual([
      'Showing 2 of 4 transactions (SLA-failed first).',
      'Showing 3 request rows; 12 samples grouped as Other requests.',
    ])
    expect(truncationNotices(anatomy, login)).toEqual(['Showing 2 of 4 transactions (SLA-failed first).'])
    expect(coverageNotices(anatomy, 'engineer')).toEqual([
      '2 requests were written after their transaction and are excluded.',
      '1 transaction rows had no VUser identity.',
      "At least 8 requests ran outside any transaction; 4 more were dropped by the report's processing limits before they could be checked.",
    ])
    const clean = { ...anatomy, coverage: { ...anatomy.coverage, late_request_rows: 0, unjoinable_transaction_rows: 0, requests_outside_transactions: 0, clamped_request_samples: 0 } }
    const unplaced = "4 requests could not be placed inside or outside a transaction; they were dropped by the report's processing limits before they could be checked."
    expect(coverageNotices(clean, 'engineer')).toEqual([unplaced])
    expect(coverageNotices(clean, 'audit')).toHaveLength(3)
    const settled = { ...clean, truncation: { ...clean.truncation, pending_requests_dropped: 0, pending_requests_evicted: 0 } }
    expect(coverageNotices(settled, 'engineer')).toEqual([])
    expect(coverageNotices(settled, 'audit')).toHaveLength(3)
    expect(clampedNotice(clean)).toBeNull()
    expect(clampedNotice(anatomy)).toContain('5 request samples')
    expect(unjoinedCopy('Checkout')).toBe('Child requests could not be matched to Checkout (no VUser identity or no requests inside its time window).')
    expect(selectionNote('Checkout')).toBe('Showing Checkout; time anatomy covers business transactions')
    expect(EMPTY_TRANSACTIONS_COPY).toContain('This run recorded no business transactions.')
    for (const reason of anatomy.truncation.reasons || []) expect(truncationReasonCopy(reason)).not.toBe(reason)
    // Every reason the backend emits (pkg/api/archive_time_anatomy.go) has readable copy.
    for (const reason of ['transaction_cap', 'report_transaction_cap', 'request_row_cap', 'occurrence_fold', 'pending_key_cap', 'pending_global_cap', 'instance_sample', 'sample_budget', 'identity_cap', 'work_budget']) {
      expect(truncationReasonCopy(reason)).not.toBe(reason)
      expect(truncationReasonCopy(reason)).not.toContain('_')
    }
  })
})

describe('selection', () => {
  it('picks the selected transaction and falls back to the first', () => {
    expect(pickAnatomyTransaction(anatomy, 'Login')).toEqual({ entry: login, matchedSelection: true })
    expect(pickAnatomyTransaction(anatomy, 'POST /cart')).toEqual({ entry: checkout, matchedSelection: false })
    expect(pickAnatomyTransaction(anatomy, '')).toEqual({ entry: checkout, matchedSelection: false })
    expect(pickAnatomyTransaction({ ...anatomy, transactions: [] }, 'Login').entry).toBeUndefined()
    expect(defaultStatistic('executive')).toBe('p50')
    expect(defaultStatistic('engineer')).toBe('p95')
    expect(defaultStatistic('audit')).toBe('p95')
  })
})

describe('summary headline', () => {
  it('prefers an SLA-failed transaction with an ok tail', () => {
    expect(headlineTransaction(anatomy)?.name).toBe('Checkout')
    const passedFirst = { ...anatomy, transactions: [{ ...login, tail: { ...checkout.tail! } }, checkout] }
    expect(headlineTransaction(passedFirst)?.name).toBe('Checkout')
    const noFailed = { ...anatomy, transactions: [{ ...login, tail: { ...checkout.tail! } }, { ...checkout, sla_status: 'passed' as const }] }
    expect(headlineTransaction(noFailed)?.name).toBe('Login')
    expect(headlineTransaction({ ...anatomy, transactions: [login] })).toBeUndefined()
    expect(headlineTransaction(undefined)).toBeUndefined()
  })

  it('maps drill-down errors to their copy', () => {
    expect(drilldownErrorCopy({ status: 404 })).toBe('Event no longer in archive.')
    expect(drilldownErrorCopy(Object.assign(new Error('too many transaction drill-downs in flight; wait for one to finish'), { status: 429 }))).toBe(DRILLDOWN_BUSY_COPY)
    expect(drilldownErrorCopy(Object.assign(new Error('archive busy, try again'), { status: 503 }))).toBe(DRILLDOWN_BUSY_COPY)
    expect(drilldownErrorIsBusy({ status: 429 })).toBe(true)
    expect(drilldownErrorIsBusy({ status: 503 })).toBe(true)
    expect(drilldownErrorIsBusy({ status: 404 })).toBe(false)
    expect(drilldownErrorCopy({ status: 413 })).toBe('Archive too large to rebuild this transaction.')
    expect(drilldownErrorCopy({ status: 422 })).toBe('Archive too large to rebuild this transaction.')
    // A rejected request reads as plain words, never the backend's validation sentence.
    expect(drilldownErrorCopy(Object.assign(new Error('first_ordinal must be at most ordinal'), { status: 400 }))).toBe('This transaction could not be located in the archive.')
  })

  it('sends the ordinal hint whenever it is valid, whatever the span', () => {
    expect(drilldownHints({ ordinal: 4021, first_ordinal: 4018 })).toEqual({ ordinal: 4021, first_ordinal: 4018 })
    // The endpoint accepts any span: a long-open transaction keeps both hints.
    expect(drilldownHints({ ordinal: 2_500_000, first_ordinal: 100 })).toEqual({ ordinal: 2_500_000, first_ordinal: 100 })
    expect(drilldownHints({ ordinal: 50_000_000, first_ordinal: 0 })).toEqual({ ordinal: 50_000_000, first_ordinal: 0 })
    // An inverted or bad first_ordinal would be a 400: send the ordinal alone.
    expect(drilldownHints({ ordinal: 10, first_ordinal: 11 })).toEqual({ ordinal: 10 })
    expect(drilldownHints({ ordinal: 10, first_ordinal: Number.NaN })).toEqual({ ordinal: 10 })
    expect(drilldownHints({ ordinal: 10, first_ordinal: -1 })).toEqual({ ordinal: 10 })
    // first_ordinal without ordinal is also a 400: send nothing, so the backend finds the event by id.
    expect(drilldownHints({ ordinal: -1, first_ordinal: -1 })).toEqual({})
    expect(drilldownHints({ ordinal: Number.NaN, first_ordinal: 3 })).toEqual({})
  })

  it('blames the report limits when every matched instance was partial', () => {
    expect(notJoinedCopy({ name: 'Checkout', partial_instances: 0 })).toBe(unjoinedCopy('Checkout'))
    expect(notJoinedCopy({ name: 'Checkout' })).toBe(unjoinedCopy('Checkout'))
    expect(notJoinedCopy({ name: 'Checkout', partial_instances: 4 })).toBe(partialOnlyCopy('Checkout'))
    // Pending caps and the CPU work budget both cause it, so the copy names neither memory nor an action the report lacks.
    expect(partialOnlyCopy('Checkout')).toContain("may have lost requests to the report's processing limits")
    expect(partialOnlyCopy('Checkout')).not.toMatch(/memory|time range/)
    expect(coverageNotices({ ...anatomy, coverage: { ...anatomy.coverage, partial_transaction_rows: 3 } }, 'engineer').join(' ')).toContain("3 transaction rows may have lost requests to the report's processing limits")
    expect(anatomyAnnouncement({ ...checkout, joined_instances: 0, partial_instances: 4 })).toBe(partialOnlyCopy('Checkout'))
  })

  it('labels a failed representative by its status code, or by an informative error class', () => {
    const failed = { reason: 'failed' as const, duration_ms: 95 }
    expect(representativeChipLabel({ ...failed, status_code: 503, error_class: 'http_5xx' })).toBe('Failed · HTTP 503')
    expect(representativeChipLabel({ ...failed, error_class: 'other' })).toBe('Failed')
    expect(representativeChipLabel({ ...failed })).toBe('Failed')
    expect(representativeChipLabel({ ...failed, status_code: 0, error_class: 'timeout' })).toBe('Failed · timeout')
    expect(representativeChipLabel({ ...failed, error_class: 'session_lost' })).toBe('Failed · session lost')
    expect(representativeChipLabel({ reason: 'p95', duration_ms: 130 })).toBe('P95 130 ms')
  })

  it('gives distinct row keys distinct DOM ids', () => {
    const keys = ['3|GET /a b#1', '3|GET /a_b#1', '3|GET /a.b#1', '0|GET /api/v1.0#1', '0|GET /api/v1_0#1']
    const ids = keys.map(key => reportPhaseRowId('rr-anatomy-row', key))
    expect(new Set(ids).size).toBe(keys.length)
    for (const id of ids) expect(id).toMatch(/^[A-Za-z0-9_-]+$/)
  })
})

describe('drill-down', () => {
  const drilldown: ArchiveTransactionWaterfall = {
    schema: 'apexload.transaction_waterfall.v1', join_rule: 'vuser_lifetime_containment_v1', phase_model: 'sequential_clipped_v1', identity_quality: 'lifetime',
    root: row({ event_id: 'tx_0123456789abcdef0123456789abcdef', ordinal: 4021, population: 'transaction', action_name: 'Checkout', duration_ms: 130 }),
    requests: [
      // Out of order on purpose: the model sorts by start, then ordinal.
      row({ event_id: 'b', ordinal: 4020, offset_ms: 40, duration_ms: 50, action_name: 'POST /cart', phases: { dns_ms: 0, connect_ms: 10, tls_ms: 0, server_ms: 30, download_ms: 10, unsplit_ms: 0 } }),
      // A request that has no TTFB split: the backend sends it all as unsplit.
      row({ event_id: 'a', ordinal: 4018, offset_ms: 0, duration_ms: 20, action_name: 'GET /cart', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 20 } }),
      row({ event_id: 'c', ordinal: 4019, offset_ms: 100, duration_ms: 10, action_name: 'GET /done', success: false, status_code: 500, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 8, download_ms: 2, unsplit_ms: 0 } }),
    ],
    requests_total: 3,
  }

  it('draws the backend phases without re-splitting them', () => {
    const [root] = waterfallRootsFromDrilldown(drilldown)
    expect(root.kind).toBe('transaction')
    expect(root.durationMs).toBe(130)
    const requests = root.children.filter(node => node.kind !== 'think')
    expect(requests.map(node => node.id)).toEqual(['a', 'b', 'c'])
    // A trace-timing split would have put an unsplit request into server time.
    expect(requests[0].phases).toEqual([{ key: 'total', startMs: 0, endMs: 20 }])
    expect(requests[0].timingSource).toBe('duration')
    expect(requests[1].phases).toEqual([
      { key: 'connect', startMs: 0, endMs: 10 },
      { key: 'wait', startMs: 10, endMs: 40 },
      { key: 'receive', startMs: 40, endMs: 50 },
    ])
    expect(requests[2].status).toBe('failed')
  })

  it('turns the gaps between covered intervals into derived think rows', () => {
    const [root] = waterfallRootsFromDrilldown(drilldown)
    const gaps = root.children.filter(node => node.kind === 'think')
    expect(gaps.map(gap => [gap.startMs, gap.durationMs])).toEqual([[20, 20], [90, 10], [110, 20]])
    expect(gaps.every(gap => gap.derived)).toBe(true)
    expect(root.children.map(node => node.kind)).toEqual(['request', 'think', 'request', 'think', 'request', 'think'])
    expect(gaps.every(gap => gap.tooltipNote?.startsWith('Time inside the transaction when none of its requests was running'))).toBe(true)
    // Every gap is drawn hatched on the root's track (here each as its own row).
    expect(drilldownOutsideSpans([root])).toEqual([{ startMs: 20, endMs: 40 }, { startMs: 90, endMs: 100 }, { startMs: 110, endMs: 130 }])
    expect(root.gapsKnownUntilMs).toBeUndefined()
  })

  it('leaves the root track to the shared waterfall: its children and the gaps between them', () => {
    const [root] = waterfallRootsFromDrilldown(drilldown)
    // A parent draws its children, so the root carries no phases of its own.
    expect(root.phases).toEqual([])
    // Phase totals and self time come from the requests.
    expect(Object.fromEntries(phaseTotals(root))).toEqual({ total: 20, connect: 10, wait: 38, receive: 12 })
    expect(selfChildTime(root)).toMatchObject({ childMs: 80, selfMs: 50, overlapMs: 0 })
    // Overlapping requests: covered time is counted once, and only the uncovered end is outside.
    const [overlap] = waterfallRootsFromDrilldown({ ...drilldown, requests: [
      row({ event_id: 'x', ordinal: 1, offset_ms: 0, duration_ms: 100, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 100, download_ms: 0, unsplit_ms: 0 } }),
      row({ event_id: 'y', ordinal: 2, offset_ms: 50, duration_ms: 70, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 20, download_ms: 50, unsplit_ms: 0 } }),
    ] })
    expect(selfChildTime(overlap)).toMatchObject({ childMs: 120, selfMs: 10, overlapMs: 50 })
    expect(drilldownOutsideSpans([overlap])).toEqual([{ startMs: 120, endMs: 130 }])
  })

  it('lists only material gaps as rows and keeps the small ones on the track', () => {
    const phases = { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 }
    const [root] = waterfallRootsFromDrilldown({ ...drilldown, root: { ...drilldown.root, duration_ms: 1000 }, requests: [
      row({ event_id: 'a', ordinal: 1, offset_ms: 0, duration_ms: 100, phases: { ...phases, server_ms: 100 } }),
      row({ event_id: 'b', ordinal: 2, offset_ms: 100.1, duration_ms: 399.9, phases: { ...phases, server_ms: 399.9 } }),
      row({ event_id: 'c', ordinal: 3, offset_ms: 500.1, duration_ms: 399.9, phases: { ...phases, server_ms: 399.9 } }),
    ] })
    // Two 0.1 ms gaps stay on the root's track; the 100 ms tail (10%) gets its own row.
    expect(root.children.filter(node => node.kind === 'think').map(gap => [gap.startMs, gap.durationMs])).toEqual([[900, 100]])
    expect(childGaps(root).map(gap => +gap.durationMs.toFixed(3))).toEqual([0.1, 0.1])
    expect(drilldownOutsideSpans([root]).map(gap => +(gap.endMs - gap.startMs).toFixed(3))).toEqual([0.1, 0.1, 100])
    // 40 ms of 1,000 is under both thresholds; 50 ms is material.
    const [small] = waterfallRootsFromDrilldown({ ...drilldown, root: { ...drilldown.root, duration_ms: 1000 }, requests: [row({ event_id: 'a', ordinal: 1, offset_ms: 0, duration_ms: 960, phases: { ...phases, server_ms: 960 } })] })
    expect(small.children.map(node => node.kind)).toEqual(['request'])
    expect(drilldownOutsideSpans([small])).toEqual([{ startMs: 960, endMs: 1000 }])
  })

  it('leaves the time after the last request blank when the requests were truncated', () => {
    const [root] = waterfallRootsFromDrilldown({ ...drilldown, truncated: true, requests_total: 812 })
    expect(root.gapsKnownUntilMs).toBe(110)
    expect(drilldownOutsideSpans([root])).toEqual([{ startMs: 20, endMs: 40 }, { startMs: 90, endMs: 100 }])
    expect(root.children[root.children.length - 1]?.kind).toBe('request')
    // With the end unknown, no self time is claimed either.
    expect(selfChildTime(root)).toBeNull()
  })

  it.each([
    ['partial', { partial: true, partial_reasons: ['pending_key_cap'] }],
    ['partial and join-may-differ', { partial: true, partial_reasons: ['work_budget'], join_may_differ: true, join_may_differ_reasons: ['work_budget'] }],
  ])('draws no outside-request time when the drill-down is %s', (_name, flags) => {
    const partial = { ...drilldown, ...flags }
    expect(drilldownIsPartial(partial)).toBe(true)
    const [root] = waterfallRootsFromDrilldown(partial)
    // Requests may be missing anywhere in the window: no gap is called outside, leading, inner or trailing.
    expect(root.gapsKnownUntilMs).toBe(0)
    expect(childGaps(root)).toEqual([])
    expect(drilldownOutsideSpans([root])).toEqual([])
    expect(selfChildTime(root)).toBeNull()
    expect(root.children.map(node => node.kind)).toEqual(['request', 'request', 'request'])
    // The request phases still draw where they ran.
    expect(root.children[0].phases[0]).toEqual({ key: 'total', startMs: 0, endMs: 20 })
    expect(drilldownIsPartial(drilldown)).toBe(false)
    expect(drilldownIsPartial({ partial: false })).toBe(false)
  })

  it('keeps outside-request time when only the report join may differ', () => {
    const differs = { ...drilldown, join_may_differ: true, join_may_differ_reasons: ['pending_global_cap'] }
    expect(drilldownIsPartial(differs)).toBe(false)
    expect(drilldownJoinMayDiffer(differs)).toBe(true)
    expect(drilldownJoinMayDiffer({ ...differs, partial: true })).toBe(false)
    expect(drilldownJoinMayDiffer(drilldown)).toBe(false)
    expect(drilldownOutsideSpans(waterfallRootsFromDrilldown(differs))).toEqual(drilldownOutsideSpans(waterfallRootsFromDrilldown(drilldown)))
  })

  it('words partial and join-may-differ drill-downs from their reasons, never the raw codes', () => {
    const codes = /pending_key_cap|pending_global_cap|identity_cap|work_budget/
    expect(partialDrilldownCopy(undefined)).toBe('Some requests of this transaction were not retained by the report; outside-request time is unknown.')
    expect(partialDrilldownCopy(['pending_key_cap'])).toContain('because this VUser had too many requests waiting for their transaction')
    expect(partialDrilldownCopy(['pending_key_cap', 'identity_cap'])).toContain('this VUser had too many requests waiting for their transaction and too many VUsers were being tracked at once')
    expect(partialDrilldownCopy(['pending_key_cap', 'work_budget'])).toContain('reached its processing budget before this transaction, so it matched no requests')
    expect(partialDrilldownCopy(['work_budget'])).not.toContain('not retained')
    expect(partialDrilldownCopy(['some_future_code'])).toBe(partialDrilldownCopy(undefined))
    expect(joinMayDifferCopy(['work_budget'])).toContain('During this run the report reached its processing budget, so the report may have matched fewer requests')
    expect(joinMayDifferCopy([])).toContain('could not be checked')
    for (const reasons of [['pending_key_cap'], ['pending_global_cap', 'identity_cap', 'work_budget'], ['work_budget'], ['some_future_code']]) {
      expect(partialDrilldownCopy(reasons)).not.toMatch(codes)
      expect(joinMayDifferCopy(reasons)).not.toMatch(codes)
      expect(joinMayDifferCopy(reasons)).not.toContain('some_future_code')
    }
  })

  it('points a traced row at its APM links and lists only the phases drawn', () => {
    const links = [{ provider: 'dynatrace', connection_id: 'dt', url: 'https://abc.live.dynatrace.com/trace?traceId=t1' }, { provider: 'elasticsearch', connection_id: 'es', url: 'https://kibana.test/app/discover' }]
    const [root] = waterfallRootsFromDrilldown({ ...drilldown, requests: [{ ...drilldown.requests[0], trace_id: 't1', trace_links: links }, ...drilldown.requests.slice(1)] })
    const traced = root.children.find(node => node.id === 'b')
    expect(traced?.tooltipNote).toBe('Server time is traced in Dynatrace and Kibana (ELK): select the row to open the trace.')
    expect(root.children.find(node => node.id === 'a')?.tooltipNote).toBeUndefined()
    expect(traceLinkNote({ trace_links: [] })).toBeUndefined()
    expect(links.map(traceLinkLabel)).toEqual(['Open trace in Dynatrace', 'Open trace in Kibana (ELK)'])
    expect(presentPhaseKeys([root])).toEqual(['connect', 'wait', 'receive', 'total'])
    expect(phaseDefinitionsText(presentPhaseKeys([root]), true)).toMatch(/^Server wait: Time to first byte minus connection setup.* No TTFB split: The request recorded no time to first byte.* Outside requests: Time inside the transaction/)
    expect(phaseDefinitionsText(['dns'], false)).toBe('')
  })

  it('keeps a request root as a request', () => {
    const [root] = waterfallRootsFromDrilldown({ ...drilldown, root: row({ event_id: 'r', duration_ms: 50, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 40, download_ms: 10, unsplit_ms: 0 } }), requests: [], requests_total: 0 })
    expect(root.kind).toBe('request')
    expect(root.children).toEqual([])
    expect(root.phases.map(span => span.key)).toEqual(['wait', 'receive'])
  })
})

// The fixture's transactions are typed against the contract.
const typed: RunArchiveTransactionAnatomy[] = anatomy.transactions
void typed

describe('partial transaction coverage', () => {
  it('tells every lens when instances were left out because requests may be missing', async () => {
    const { coverageNotices } = await import('./timeAnatomyModel')
    const fixture = (await import('./__fixtures__/timeAnatomyReport.json')).default as unknown as { time_anatomy: import('../../types/timeAnatomy').RunArchiveTimeAnatomy }
    const anatomy = { ...fixture.time_anatomy, coverage: { ...fixture.time_anatomy.coverage, partial_transaction_rows: 12 } }
    expect(coverageNotices(anatomy, 'executive').some(note => note.startsWith('12 transaction rows may have lost requests'))).toBe(true)
    const none = { ...fixture.time_anatomy, coverage: { ...fixture.time_anatomy.coverage, partial_transaction_rows: 0 } }
    expect(coverageNotices(none, 'engineer').some(note => note.includes('may have lost requests'))).toBe(false)
  })
})

describe('outside-request coverage under eviction', () => {
  it('words the outside count as a lower bound when requests were evicted', async () => {
    const { coverageNotices } = await import('./timeAnatomyModel')
    const fixture = (await import('./__fixtures__/timeAnatomyReport.json')).default as unknown as { time_anatomy: import('../../types/timeAnatomy').RunArchiveTimeAnatomy }
    const base = fixture.time_anatomy
    const evicted = { ...base, coverage: { ...base.coverage, requests_outside_transactions: 5 }, truncation: { ...base.truncation, pending_requests_evicted: 40 } }
    const evictedOnly = { ...evicted, truncation: { ...evicted.truncation, pending_requests_dropped: 0 } }
    expect(coverageNotices(evictedOnly, 'engineer')).toContain('At least 5 requests ran outside any transaction; 40 more were not kept long enough to tell whether they ran inside one.')
    const exact = { ...base, coverage: { ...base.coverage, requests_outside_transactions: 5 }, truncation: { ...base.truncation, pending_requests_evicted: 0, pending_requests_dropped: 0 } }
    expect(coverageNotices(exact, 'engineer')).toContain('5 requests ran outside any transaction.')
  })

  it('words the outside count as a lower bound when requests were dropped by processing limits, even with no eviction', async () => {
    const { coverageNotices } = await import('./timeAnatomyModel')
    const fixture = (await import('./__fixtures__/timeAnatomyReport.json')).default as unknown as { time_anatomy: import('../../types/timeAnatomy').RunArchiveTimeAnatomy }
    const base = fixture.time_anatomy
    const dropped = { ...base, coverage: { ...base.coverage, requests_outside_transactions: 5 }, truncation: { ...base.truncation, pending_requests_evicted: 0, pending_requests_dropped: 20000 } }
    const notices = coverageNotices(dropped, 'engineer')
    expect(notices).toContain("At least 5 requests ran outside any transaction; 20,000 more were dropped by the report's processing limits before they could be checked.")
    expect(notices).not.toContain('5 requests ran outside any transaction.')
    const both = { ...dropped, truncation: { ...dropped.truncation, pending_requests_evicted: 40 } }
    expect(coverageNotices(both, 'engineer')).toContain("At least 5 requests ran outside any transaction; 20,040 more were dropped by the report's processing limits or not kept long enough to tell whether they ran inside one.")
  })
})

describe('unplaced requests with none outside', () => {
  it('never leads with a lower bound of 0 and tells every lens about unplaced requests', async () => {
    const { coverageNotices } = await import('./timeAnatomyModel')
    const fixture = (await import('./__fixtures__/timeAnatomyReport.json')).default as unknown as { time_anatomy: import('../../types/timeAnatomy').RunArchiveTimeAnatomy }
    const base = fixture.time_anatomy
    const dropped = { ...base, coverage: { ...base.coverage, requests_outside_transactions: 0 }, truncation: { ...base.truncation, pending_requests_evicted: 0, pending_requests_dropped: 20000 } }
    const expected = "20,000 requests could not be placed inside or outside a transaction; they were dropped by the report's processing limits before they could be checked."
    for (const lens of ['audit', 'engineer', 'executive'] as const) {
      const notices = coverageNotices(dropped, lens)
      expect(notices).toContain(expected)
      expect(notices.some(note => note.startsWith('At least 0'))).toBe(false)
    }
  })
})
