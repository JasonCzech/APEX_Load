import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../../types/replay'
import { buildDebugRunDiffTree, deltaVerdict, diffDebugRuns, formatDeltaMs } from './debugRunDiff'
import type { DebugWaterfallNode } from './debugTree'

const at = (ms: number) => new Date(Date.UTC(2026, 10, 9, 12) + ms).toISOString()
type Timing = { dns?: number; connect?: number; tls?: number; ttfb?: number }
const ev = (o: Partial<BrowserReplayEvent> & { start: number; dur: number; t?: Timing }): BrowserReplayEvent => {
  const { start, dur, t, ...rest } = o
  return {
    phase: 'action', action_index: 0, action_name: 'Step', action_type: 'request', method: 'GET', target: 'https://shop.test/', status_code: 200, success: true,
    started_at: at(start), completed_at: at(start + dur), duration_ms: dur,
    ...(t ? { protocol_evidence: { timings: { dns_ms: t.dns ?? 0, connect_ms: t.connect ?? 0, tls_ms: t.tls ?? 0, ttfb_ms: t.ttfb ?? 0, duration_ms: dur } } } : {}),
    ...rest,
  }
}

describe('debug-run diff model', () => {
  it('explains a change by the phase that moved', () => {
    expect(deltaVerdict(310, { wait: 300, dns: 10 }, true)).toBe('+310.0 ms, all in server')
    expect(deltaVerdict(100, { wait: 60, tls: 40 }, true)).toBe('+100.0 ms, mostly server (60%)')
    expect(deltaVerdict(100, { wait: 30, tls: 30, dns: 40 }, true)).toBe('+100.0 ms, spread across phases (largest: DNS)')
    expect(deltaVerdict(-42, { tls: -40, wait: -2 }, true)).toBe('−42.0 ms, all in TLS')
    expect(deltaVerdict(80, {}, false)).toBe('+80.0 ms (total only)')
    expect(deltaVerdict(0.2, { wait: 0.2 }, true)).toBe('No change')
    expect(formatDeltaMs(1500)).toBe('+1.50 s')
  })

  it('aligns executions by action and occurrence, so iteration 2 compares with iteration 2', () => {
    const base = [
      ev({ action_index: 0, action_name: 'Login', start: 0, dur: 100, t: { ttfb: 90 } }),
      ev({ action_index: 1, action_name: 'Browse', start: 100, dur: 50, t: { ttfb: 40 } }),
      ev({ action_index: 0, action_name: 'Login', start: 150, dur: 100, t: { ttfb: 90 } }),
    ]
    const cur = [
      ev({ action_index: 0, action_name: 'Login', start: 0, dur: 100, t: { ttfb: 90 } }),
      ev({ action_index: 1, action_name: 'Browse', start: 100, dur: 50, t: { ttfb: 40 } }),
      ev({ action_index: 0, action_name: 'Login', start: 150, dur: 410, t: { ttfb: 400 } }),
    ]
    const diff = diffDebugRuns(base, cur)
    expect(diff.entries.map(e => [e.current?.actionName, e.deltaMs])).toEqual([['Login', 0], ['Browse', 0], ['Login', 310]])
    expect(diff.byCurrentPosition.get(2)?.summary).toBe('+310.0 ms, all in server')
  })

  it('flags outcome changes, new executions and ones only the baseline had', () => {
    const base = [ev({ action_index: 0, action_name: 'Pay', start: 0, dur: 100 }), ev({ action_index: 1, action_name: 'Receipt', start: 100, dur: 30 })]
    const cur = [ev({ action_index: 0, action_name: 'Pay', start: 0, dur: 120, success: false, status_code: 500 }), ev({ action_index: 2, action_name: 'Retry', start: 120, dur: 40 })]
    const diff = diffDebugRuns(base, cur)
    expect(diff.entries.map(e => [e.base?.actionName ?? e.current?.actionName, e.outcome, e.summary])).toEqual([
      ['Pay', 'now_failing', '+20.0 ms (total only) · now failing'],
      ['Retry', 'added', 'New in this run (40.0 ms)'],
      ['Receipt', 'removed', 'Only in baseline (30.0 ms)'],
    ])
  })

  it('ignores transaction markers and container events when aligning', () => {
    const marker = (start: number) => ev({ action_index: 9, action_type: 'source_transaction', method: undefined, target: '', start, dur: 0 })
    const seq = (start: number, dur: number) => ev({ action_index: 8, action_type: 'http_sequence', method: undefined, target: '', start, dur })
    const diff = diffDebugRuns([marker(0), seq(0, 100), ev({ start: 0, dur: 100 })], [marker(0), seq(0, 300), ev({ start: 0, dur: 300 })])
    expect(diff.entries).toHaveLength(1)
    expect(diff.entries[0].deltaMs).toBe(200)
  })
})

describe('debug-run diff tree', () => {
  const shape = (n: DebugWaterfallNode, d = 0): string[] => [`${'  '.repeat(d)}${n.label}${n.delta ? ` [${n.delta.label}]` : ''}${n.ghost ? ` ghost=${n.ghost.durationMs}` : ''}`, ...n.children.flatMap(c => shape(c, d + 1))]

  it('annotates the current run with baseline ghosts, rolls changes up and lists baseline-only executions', () => {
    const base = [
      ev({ action_index: 0, action_name: 'Login', source_transaction: 'Auth', start: 0, dur: 100, t: { tls: 20, ttfb: 90 } }),
      ev({ action_index: 1, action_name: 'Token', source_transaction: 'Auth', start: 100, dur: 50, t: { ttfb: 40 } }),
      ev({ action_index: 2, action_name: 'Old step', start: 150, dur: 25 }),
    ]
    const cur = [
      ev({ action_index: 0, action_name: 'Login', source_transaction: 'Auth', start: 0, dur: 400, t: { tls: 20, ttfb: 390 } }),
      ev({ action_index: 1, action_name: 'Token', source_transaction: 'Auth', start: 400, dur: 40, t: { ttfb: 30 } }),
    ]
    const tree = buildDebugRunDiffTree({ events: base }, { events: cur })
    expect(tree.roots.flatMap(r => shape(r))).toEqual([
      // Parents report their own measured change and carry a baseline ghost.
      'Actions [+265.0 ms, all in server] ghost=175',
      '  Auth [+290.0 ms, all in server] ghost=150',
      '    Login [+300.0 ms, all in server] ghost=100',
      '    Token [−10.0 ms, all in server] ghost=50',
      'Only in baseline [1 not executed in this run]',
      '  Old step [Only in baseline (25.0 ms)]',
    ])
    expect(tree.sinks.map(s => s.label)).toEqual([
      // Different steps ran, so total elapsed time is not a like-for-like comparison.
      'Elapsed 175.0 ms → 440.0 ms (not like for like)',
      'Login: +300.0 ms, all in server',
      'Token: −10.0 ms, all in server',
      '0 new · 1 only in baseline',
    ])
    expect(tree.sinks[1].targetId).toBe(tree.nodeIdByCurrentPosition.get(0))
  })
})

describe('changes outside measured phases', () => {
  it('does not credit a small phase change with a large total change', () => {
    const har = (dur: number, wait: number) => ev({ start: 0, dur, network_entries: [{ url: 'https://sf.test/login', method: 'GET', status_code: 200, duration_ms: wait, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: wait }] } }] })
    const diff = diffDebugRuns([har(706, 179)], [har(440, 151)])
    const [entry] = diff.entries
    expect(entry.deltaMs).toBeCloseTo(-266)
    expect(entry.phaseDeltas.wait).toBeCloseTo(-28)
    expect(entry.phaseDeltas.total).toBeCloseTo(-238)
    expect(entry.summary).toBe('−266.0 ms, mostly time outside measured phases (89%)')
  })
})

describe('honest comparisons', () => {
  it('keeps iterations aligned when an earlier iteration ran differently', () => {
    // Baseline iteration 1 fails at Login and skips Browse; current runs both iterations fully.
    const base = [
      ev({ action_index: 0, action_name: 'Login', start: 0, dur: 50, success: false }),
      ev({ action_index: 0, action_name: 'Login', start: 100, dur: 50 }),
      ev({ action_index: 1, action_name: 'Browse', start: 150, dur: 70 }),
    ]
    const cur = [
      ev({ action_index: 0, action_name: 'Login', start: 0, dur: 50 }),
      ev({ action_index: 1, action_name: 'Browse', start: 50, dur: 60 }),
      ev({ action_index: 0, action_name: 'Login', start: 110, dur: 50 }),
      ev({ action_index: 1, action_name: 'Browse', start: 160, dur: 75 }),
    ]
    const diff = diffDebugRuns(base, cur)
    const browse = diff.entries.filter(e => e.current?.actionName === 'Browse')
    expect(browse.map(e => e.outcome)).toEqual(['added', 'same'])
    expect(browse[1].deltaMs).toBe(5)
  })

  it('does not rank a request that now fails fast as an improvement', () => {
    const tree = buildDebugRunDiffTree({ events: [ev({ action_name: 'Login', start: 0, dur: 300 })] }, { events: [ev({ action_name: 'Login', start: 0, dur: 20, success: false, status_code: 500 })] })
    expect(tree.sinks.map(s => s.id)).toEqual(['elapsed', 'now-failing'])
    expect(tree.roots[0].children[0].delta?.outcome).toBe('now_failing')
  })

  it('keeps new and missing executions out of parent changes and elapsed verdicts', () => {
    const base = [ev({ action_index: 0, action_name: 'A', source_transaction: 'T', start: 0, dur: 100 }), ev({ action_index: 1, action_name: 'B', source_transaction: 'T', start: 100, dur: 400 })]
    const cur = [ev({ action_index: 0, action_name: 'A', source_transaction: 'T', start: 0, dur: 100 }), ev({ action_index: 2, action_name: 'C', source_transaction: 'T', start: 100, dur: 50 })]
    const tree = buildDebugRunDiffTree({ events: base }, { events: cur })
    const c = tree.roots[0].children[0].children.find(n => n.label === 'C')!
    expect(c.delta?.outcome).toBe('added')
    expect(tree.sinks[0].label).toBe('Elapsed 500.0 ms → 150.0 ms (not like for like)')
  })

  it('does not split a change into phases when the runs measured it differently', () => {
    const har = ev({ start: 0, dur: 300, network_entries: [{ url: 'https://a.test/', method: 'GET', status_code: 200, duration_ms: 300, har: { phases: [{ name: 'request_wait', start_ms: 0, end_ms: 300 }] } }] })
    const trace = ev({ start: 0, dur: 360, t: { dns: 10, ttfb: 340 } })
    const [entry] = diffDebugRuns([har], [trace]).entries
    expect(entry.phasesComparable).toBe(false)
    expect(entry.phasesReason).toBe('timing sources differ')
    expect(entry.summary).toBe('+60.0 ms (timing sources differ)')
  })
})
