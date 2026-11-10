import { describe, expect, it } from 'vitest'

import type { ArchiveIterationWaterfall } from '../../types/iterationReplay'
import type { ArchiveWaterfallRow, RunArchiveRepresentativeSample } from '../../types/timeAnatomy'
import {
  THINK_GAP_LABEL,
  buildIterationTree,
  formTarget,
  iterationPartialCopy,
  iterationRowPhases,
  sameTarget,
  targetFromBusinessKeyMatch,
  targetFromIterationRef,
  targetFromRepresentative,
  type IterationWaterfallNode,
} from './iterationReplayModel'

const START = Date.parse('2026-11-01T10:00:00.000Z')

function row(partial: Partial<ArchiveWaterfallRow> & { ordinal: number; offset_ms: number }): ArchiveWaterfallRow {
  return {
    event_id: `tx_${String(partial.ordinal).padStart(32, '0')}`, population: 'action',
    timestamp: new Date(START + partial.offset_ms).toISOString(), duration_ms: 0, action_name: 'GET /', success: true,
    phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 },
    ...partial,
  }
}

function iteration(rows: ArchiveWaterfallRow[], extra: Partial<ArchiveIterationWaterfall> = {}): ArchiveIterationWaterfall {
  return {
    schema: 'apexload.iteration_waterfall.v1', identity_quality: 'lifetime', group: 'g1', vuser_id: 3, vuser_lifetime: '9007199254740993',
    iteration: 2, first_ordinal: 0, last_ordinal: rows.length - 1, started_at: new Date(START).toISOString(), span_ms: 0,
    rows, rows_total: rows.length, think_ms_inside_transactions: 0, ...extra,
  }
}

const labels = (nodes: IterationWaterfallNode[]) => nodes.map(node => node.label)

// Login (0–150 ms wall, 100 ms duration) with A and B; a 150 ms pause; then
// Browse (300–400) with C. Rows are given in completion order.
const loginRows = [
  row({ ordinal: 0, offset_ms: 50, duration_ms: 40, action_name: 'B', source_transaction: 'Login', phases: { dns_ms: 5, connect_ms: 10, tls_ms: 0, server_ms: 20, download_ms: 5, unsplit_ms: 0 } }),
  row({ ordinal: 1, offset_ms: 0, duration_ms: 40, action_name: 'A', source_transaction: 'Login', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 40 } }),
  row({ ordinal: 3, offset_ms: 0, duration_ms: 100, elapsed_ms: 150, population: 'transaction', action_name: 'Login', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 100 } }),
  row({ ordinal: 4, offset_ms: 300, duration_ms: 80, action_name: 'C', success: false, status_code: 500, error_message: 'boom', source_transaction: 'Browse' }),
  row({ ordinal: 5, offset_ms: 300, duration_ms: 100, population: 'transaction', action_name: 'Browse', phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 100 } }),
]

describe('iterationRowPhases', () => {
  it('maps the backend phases onto the shared keys, laid one after another', () => {
    expect(iterationRowPhases({ dns_ms: 5, connect_ms: 10, tls_ms: 3, server_ms: 20, download_ms: 5, unsplit_ms: 2 })).toEqual([
      { key: 'dns', startMs: 0, endMs: 5 },
      { key: 'connect', startMs: 5, endMs: 15 },
      { key: 'tls', startMs: 15, endMs: 18 },
      { key: 'wait', startMs: 18, endMs: 38 },
      { key: 'receive', startMs: 38, endMs: 43 },
      { key: 'total', startMs: 43, endMs: 45 },
    ])
    expect(iterationRowPhases(undefined)).toEqual([])
  })
})

describe('buildIterationTree', () => {
  it('sorts rows by start, nests requests in their transaction and adds the pause as think time', () => {
    const tree = buildIterationTree(iteration(loginRows, { span_ms: 400, gaps_ms: 150 }))
    expect(tree.roots).toHaveLength(1)
    const root = tree.roots[0]
    expect(root.kind).toBe('iteration')
    expect(root.label).toBe('Iteration 2')
    expect(root.status).toBe('failed')
    expect(tree.spanMs).toBe(400)
    expect(labels(root.children)).toEqual(['Login', THINK_GAP_LABEL, 'Browse'])
    const [login, gap, browse] = root.children
    expect(labels(login.children)).toEqual(['A', 'B'])
    expect(login.durationMs).toBe(150)
    expect(login.payload?.thinkInsideMs).toBe(50)
    expect(login.detail).toContain('50 ms think time')
    expect(gap).toMatchObject({ kind: 'think', startMs: 150, durationMs: 150, derived: true })
    expect(tree.thinkGapMs).toBe(150)
    expect(labels(browse.children)).toEqual(['C'])
    expect(browse.children[0]).toMatchObject({ status: 'failed', statusCode: 500, errorMessage: 'boom', timingSource: 'duration' })
    const b = login.children[1]
    expect(b).toMatchObject({ kind: 'request', startMs: 50, durationMs: 40, timingSource: 'protocol_evidence' })
    expect(b.phases.map(span => span.key)).toEqual(['dns', 'connect', 'wait', 'receive'])
    expect(login.children[0].timingSource).toBe('duration')
  })

  it('claims a request by its start, as the report join does: source_transaction only settles the slack before a window', () => {
    const rows = [
      // Inside Login's window: claimed whatever its source_transaction says.
      row({ ordinal: 0, offset_ms: 110, duration_ms: 5, action_name: 'other-source', source_transaction: 'Other' }),
      row({ ordinal: 1, offset_ms: 100, duration_ms: 50, population: 'transaction', action_name: 'Login' }),
      row({ ordinal: 2, offset_ms: 120, duration_ms: 5, action_name: 'untagged' }),
      row({ ordinal: 3, offset_ms: 130, duration_ms: 5, action_name: 'scrubbed', source_transaction: '[REDACTED]' }),
      // In the 2 ms slack before Login: the names decide, trimmed and in any case.
      row({ ordinal: 4, offset_ms: 99, duration_ms: 0.5, action_name: 'slack-match', source_transaction: '  login ' }),
      row({ ordinal: 5, offset_ms: 98.5, duration_ms: 5, action_name: 'slack-mismatch', source_transaction: 'Other' }),
      // Scrubbed name in the slack: claimed only when it still runs at Login's start.
      row({ ordinal: 6, offset_ms: 99, duration_ms: 0.5, action_name: 'slack-ended', source_transaction: '[REDACTED]' }),
      row({ ordinal: 7, offset_ms: 99.2, duration_ms: 5, action_name: 'slack-running', source_transaction: '[REDACTED]' }),
    ]
    const tree = buildIterationTree(iteration(rows, { span_ms: 150 }))
    const top = tree.roots[0].children
    const login = top.find(node => node.label === 'Login')!
    expect(labels(login.children).sort()).toEqual(['other-source', 'scrubbed', 'slack-match', 'slack-running', 'untagged'])
    expect(labels(top).sort()).toEqual(['Login', 'slack-ended', 'slack-mismatch'])
  })

  it('nests a request under the innermost matching transaction and transactions by containment', () => {
    const rows = [
      row({ ordinal: 0, offset_ms: 35, duration_ms: 10, action_name: 'inner-request' }),
      row({ ordinal: 1, offset_ms: 30, duration_ms: 40, population: 'transaction', action_name: 'Inner' }),
      row({ ordinal: 2, offset_ms: 0, duration_ms: 100, population: 'transaction', action_name: 'script_iteration' }),
      row({ ordinal: 3, offset_ms: 5, duration_ms: 10, action_name: 'outer-request' }),
    ]
    const tree = buildIterationTree(iteration(rows, { span_ms: 100 }))
    const [outer] = tree.roots[0].children
    expect(outer.label).toBe('script_iteration')
    expect(labels(outer.children)).toEqual(['outer-request', 'Inner'])
    expect(labels(outer.children[1].children)).toEqual(['inner-request'])
    expect(tree.roots[0].children).toHaveLength(1)
  })

  it('draws no think rows when rows were cut or may be missing', () => {
    for (const extra of [{ truncated: true }, { partial: true, partial_reasons: ['scan_budget' as const] }]) {
      const tree = buildIterationTree(iteration(loginRows, { span_ms: 400, ...extra }))
      expect(tree.roots[0].children.some(node => node.kind === 'think')).toBe(false)
      expect(tree.thinkGapMs).toBe(0)
    }
  })

  it('ignores immaterial gaps and gives a lone transaction its own bar', () => {
    const rows = [
      row({ ordinal: 0, offset_ms: 0, duration_ms: 1000, population: 'transaction', action_name: 'One' }),
      row({ ordinal: 1, offset_ms: 1010, duration_ms: 1000, population: 'transaction', action_name: 'Two' }),
    ]
    const tree = buildIterationTree(iteration(rows, { span_ms: 2010 }))
    expect(labels(tree.roots[0].children)).toEqual(['One', 'Two'])
    expect(tree.roots[0].children[0].phases).toEqual([{ key: 'total', startMs: 0, endMs: 1000 }])
    expect(tree.roots[0].status).toBe('passed')
  })
})

describe('iterationPartialCopy', () => {
  it('says an ambiguous VUser ID shows the linked row\'s iteration, else the first', () => {
    expect(iterationPartialCopy(['ambiguous_vuser_id'])).toContain('holding the linked row, or else the first matching one')
    expect(iterationPartialCopy(['ambiguous_vuser_id'])).not.toMatch(/this is the first matching iteration/)
  })
})

describe('targets', () => {
  it('builds replay targets from the index, a representative and a business-key match', () => {
    const fromRef = targetFromIterationRef({ reason: 'slowest', identity_quality: 'lifetime', group: 'g1', vuser_id: 3, vuser_lifetime: '77', iteration: 4, first_ordinal: 10, last_ordinal: 20, span_ms: 5, transaction_rows: 1, request_rows: 2, failed_rows: 0 })
    expect(fromRef).toEqual({ group: 'g1', vuser_id: 3, vuser_lifetime: '77', iteration: 4, first_ordinal: 10, last_ordinal: 20, source: 'Slowest' })
    const sample = { reason: 'p95', event_id: 'tx_1', ordinal: 12, first_ordinal: 10, duration_ms: 5, success: true, children: 1, group: 'g1', vuser_id: 3, vuser_lifetime: '77', iteration: 4 } as RunArchiveRepresentativeSample
    expect(targetFromRepresentative(sample, 'Checkout')).toEqual({ group: 'g1', vuser_id: 3, vuser_lifetime: '77', iteration: 4, ordinal: 12, source: 'Checkout' })
    expect(targetFromRepresentative({ ...sample, iteration: undefined }, 'Checkout')).toBeNull()
    expect(targetFromRepresentative({ ...sample, vuser_id: undefined, vuser_lifetime: undefined }, 'Checkout')).toBeNull()
    const match = { timestamp: '', name: 'pay', duration_ms: 1, success: true, matched_by: 'digest' as const, group: 'g1', vuser_id: 3, iteration: 4, ordinal: 7 }
    expect(targetFromBusinessKeyMatch(match)).toEqual({ group: 'g1', vuser_id: 3, vuser_lifetime: undefined, iteration: 4, ordinal: 7, source: 'Business key' })
    expect(targetFromBusinessKeyMatch({ ...match, iteration: undefined })).toBeNull()
    expect(sameTarget(fromRef, { ...fromRef, source: 'Other' })).toBe(true)
    expect(sameTarget(fromRef, { ...fromRef, iteration: 5 })).toBe(false)
  })

  it('drops the hints of an iteration split by identity eviction', () => {
    const ref = { reason: 'slowest' as const, identity_quality: 'lifetime' as const, group: 'g1', vuser_id: 3, vuser_lifetime: '77', iteration: 4, first_ordinal: 10, last_ordinal: 20, span_ms: 5, transaction_rows: 1, request_rows: 2, failed_rows: 0, split: true }
    const target = targetFromIterationRef(ref)
    expect(target.first_ordinal).toBeUndefined()
    expect(target.last_ordinal).toBeUndefined()
    expect(target.vuser_lifetime).toBe('77')
  })

  it('keeps the shown target for unchanged form fields and allows VUser 0 with a lifetime', () => {
    const shown = { group: 'g1', vuser_id: 0, vuser_lifetime: '77', iteration: 4, first_ordinal: 10, last_ordinal: 20, ordinal: 12, source: 'Slowest' }
    expect(formTarget({ group: 'g1', vuser: '0', iteration: '4' }, shown)).toBe(shown)
    // An edited field is a VUser-ID-only lookup: no lifetime, no anchors.
    expect(formTarget({ group: 'g1', vuser: '0', iteration: '5' }, shown)).toBeNull()
    expect(formTarget({ group: 'g1', vuser: '2', iteration: '5' }, shown)).toEqual({ group: 'g1', vuser_id: 2, iteration: 5, source: 'Manual' })
    expect(formTarget({ group: '', vuser: '0', iteration: '4' }, null)).toBeNull()
    expect(formTarget({ group: '', vuser: '', iteration: '4' }, null)).toBeNull()
    expect(formTarget({ group: '', vuser: '-1', iteration: '4' }, null)).toBeNull()
  })
})
