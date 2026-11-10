import { describe, expect, it } from 'vitest'
import { childGaps, criticalPathIds, dominantPhase, filterTree, flattenAll, flattenVisible, phaseTotals, selfChildTime, sortSiblings, topTimeSinks } from './analysis'
import { nodeFromAggregatePhaseVector, phasesFromTraceTimings } from './aggregate'
import type { WaterfallNode, WaterfallPhaseSpan } from './types'

const leaf = (id: string, startMs: number, durationMs: number, phases: WaterfallPhaseSpan[] = [{ key: 'wait', startMs: 0, endMs: durationMs }], extra: Partial<WaterfallNode> = {}): WaterfallNode => ({
  id, kind: 'request', label: id, startMs, durationMs, status: 'passed', phases, children: [], ...extra,
})
const parent = (id: string, children: WaterfallNode[], extra: Partial<WaterfallNode> = {}): WaterfallNode => {
  const start = Math.min(...children.map(c => c.startMs!))
  const end = Math.max(...children.map(c => c.startMs! + c.durationMs!))
  return { id, kind: 'transaction', label: id, startMs: start, durationMs: end - start, status: 'passed', phases: [], children, ...extra }
}

describe('waterfall critical path', () => {
  it('follows the chain that sets the end time through concurrent requests', () => {
    // a (0-100) → b (100-300) and c (100-180) in parallel → d (300-350). c is not critical.
    const a = leaf('a', 0, 100), b = leaf('b', 100, 200), c = leaf('c', 100, 80), d = leaf('d', 300, 50)
    const ids = criticalPathIds([parent('tx', [a, b, c, d])])
    expect([...ids].sort()).toEqual(['a', 'b', 'd', 'tx'])
  })

  it('marks every request when execution is sequential and recurses into children', () => {
    const tx1 = parent('tx1', [leaf('r1', 0, 50), leaf('r2', 60, 40)])
    const tx2 = parent('tx2', [leaf('r3', 120, 30), leaf('r4', 125, 10)])
    const ids = criticalPathIds([tx1, tx2])
    expect(ids.has('r1') && ids.has('r2') && ids.has('r3')).toBe(true)
    expect(ids.has('r4')).toBe(false)
  })

  it('ignores untimed nodes', () => {
    const ids = criticalPathIds([leaf('timed', 0, 10), { ...leaf('untimed', 0, 0), startMs: null, durationMs: null }])
    expect([...ids]).toEqual(['timed'])
  })
})

describe('waterfall self vs child time', () => {
  it('splits parent elapsed into covered child time, self time and overlap', () => {
    const tx: WaterfallNode = { ...parent('tx', [leaf('a', 10, 40), leaf('b', 30, 40), leaf('c', 120, 20)]), startMs: 0, durationMs: 200 }
    expect(selfChildTime(tx)).toEqual({ childMs: 80, selfMs: 120, overlapMs: 20, childSumMs: 100 })
    expect(childGaps(tx)).toEqual([{ startMs: 0, durationMs: 10 }, { startMs: 70, durationMs: 50 }, { startMs: 140, durationMs: 60 }])
    expect(selfChildTime(leaf('x', 0, 5))).toBeNull()
  })
})

describe('waterfall tree views', () => {
  const slowTls = leaf('slow', 0, 300, [{ key: 'tls', startMs: 0, endMs: 200 }, { key: 'wait', startMs: 200, endMs: 300 }])
  const fast = leaf('fast', 300, 20)
  const mid = leaf('mid', 320, 100, [{ key: 'dns', startMs: 0, endMs: 30 }, { key: 'wait', startMs: 30, endMs: 100 }], { status: 'failed', errorMessage: 'boom' })
  const iteration: WaterfallNode = { ...parent('it', [slowTls, fast, mid]), kind: 'iteration' }

  it('sorts siblings chronologically, slowest first or by dominant phase', () => {
    expect(sortSiblings([mid, fast, slowTls], 'chronological').map(n => n.id)).toEqual(['slow', 'fast', 'mid'])
    expect(sortSiblings([fast, mid, slowTls], 'slowest').map(n => n.id)).toEqual(['slow', 'mid', 'fast'])
    // TLS handshake sorts before Waiting (TTFB); heavier phase time first within a group.
    expect(sortSiblings([fast, mid, slowTls], 'dominant').map(n => n.id)).toEqual(['slow', 'mid', 'fast'])
    expect(dominantPhase(slowTls)).toMatchObject({ key: 'tls', ms: 200 })
    expect(dominantPhase(iteration)?.key).toBe('tls')
  })

  it('flattens only expanded nodes with tree positions and keeps iterations in order', () => {
    const second: WaterfallNode = { ...parent('it2', [leaf('z', 500, 900)]), kind: 'iteration' }
    const collapsed = flattenVisible([iteration, second], new Set(), 'slowest')
    expect(collapsed.map(r => r.node.id)).toEqual(['it', 'it2'])
    const open = flattenVisible([iteration, second], new Set(['it']), 'slowest')
    expect(open.map(r => [r.node.id, r.depth, r.posInSet, r.setSize, r.parentId])).toEqual([
      ['it', 0, 1, 2, null], ['slow', 1, 1, 3, 'it'], ['mid', 1, 2, 3, 'it'], ['fast', 1, 3, 3, 'it'], ['it2', 0, 2, 2, null],
    ])
    expect(flattenAll([iteration, second]).map(n => n.id)).toEqual(['it', 'slow', 'fast', 'mid', 'it2', 'z'])
  })

  it('filters to matches while keeping their ancestors', () => {
    const filtered = filterTree([iteration], n => n.status === 'failed' && n.kind === 'request')
    expect(filtered).toHaveLength(1)
    expect(filtered[0].children.map(c => c.id)).toEqual(['mid'])
  })

  it('summarises the top time sinks', () => {
    const sinks = topTimeSinks([iteration], 420)
    expect(sinks.map(s => s.id)).toEqual(['slowest', 'dominant-phase', 'tls', 'failures'])
    expect(sinks[0].label).toBe('3 requests = 100% of elapsed')
    expect(sinks[0].targetId).toBe('slow')
    expect(sinks.find(s => s.id === 'tls')?.label).toBe('TLS on 1 new connection = 200.0 ms')
    expect(sinks.find(s => s.id === 'failures')).toMatchObject({ label: '1 failure', targetId: 'mid', tone: 'danger' })
    expect(sinks.find(s => s.id === 'dominant-phase')?.label).toBe('TLS = 48% of measured phase time')
  })
})

describe('waterfall adapters', () => {
  it('derives server and download from TTFB without double counting setup', () => {
    expect(phasesFromTraceTimings({ dns_ms: 10, connect_ms: 20, tls_ms: 30, ttfb_ms: 160, duration_ms: 200 })).toEqual([
      { key: 'dns', startMs: 0, endMs: 10 },
      { key: 'connect', startMs: 10, endMs: 30 },
      { key: 'tls', startMs: 30, endMs: 60 },
      { key: 'wait', startMs: 60, endMs: 160 },
      { key: 'receive', startMs: 160, endMs: 200 },
    ])
    // Without TTFB the time cannot be split into server and download.
    expect(phasesFromTraceTimings({ duration_ms: 40 })).toEqual([{ key: 'total', startMs: 0, endMs: 40 }])
    expect(phasesFromTraceTimings({ dns_ms: 5, duration_ms: 40 })).toEqual([{ key: 'dns', startMs: 0, endMs: 5 }, { key: 'total', startMs: 5, endMs: 40 }])
  })

  it('builds aggregate rows from a phase vector for the chosen statistic', () => {
    const node = nodeFromAggregatePhaseVector({ id: 'agg', label: 'GET /cart', offsetMs: 25, statistic: 'p95', vector: { tls: { p95: 40, observedPct: 12 }, server: { p95: 300 }, download: { p95: 10 } } })
    expect(node).toMatchObject({ startMs: 25, durationMs: 350, timingSource: 'aggregate', detail: 'P95 of each phase' })
    expect(node.phases.map(p => p.key)).toEqual(['tls', 'wait', 'receive'])
  })
})

describe('waterfall analysis after review', () => {
  it('finds the critical path through thousands of sequential siblings quickly', () => {
    const kids = Array.from({ length: 3000 }, (_, i) => leaf(`r${i}`, i * 10, 10))
    const t0 = performance.now()
    const ids = criticalPathIds([parent('tx', kids)])
    expect(performance.now() - t0).toBeLessThan(500)
    expect(ids.size).toBe(3001)
  })

  it('counts think time as self time and clips overlap to the parent', () => {
    const think = { ...leaf('think', 500, 2000), kind: 'think' as const }
    const tx: WaterfallNode = { ...parent('tx', [leaf('a', 0, 500), think, leaf('b', 2500, 500)]), startMs: 0, durationMs: 3000 }
    expect(selfChildTime(tx)).toMatchObject({ childMs: 1000, selfMs: 2000, overlapMs: 0 })
    const runsPast: WaterfallNode = { ...parent('tx2', [leaf('late', 900, 400)]), startMs: 0, durationMs: 1000 }
    expect(selfChildTime(runsPast)).toMatchObject({ childMs: 100, overlapMs: 0 })
  })

  it('says how many requests contributed phase timings', () => {
    const totalOnly = Array.from({ length: 4 }, (_, i) => leaf(`t${i}`, i * 100, 50, [{ key: 'total', startMs: 0, endMs: 50 }]))
    const sinks = topTimeSinks([parent('tx', [...totalOnly, leaf('har', 500, 120)])], 620)
    expect(sinks.find(s => s.id === 'dominant-phase')?.detail).toBe('120.0 ms across 1 request with phase timings')
  })
})

describe('phase totals exclude non-request leaves', () => {
  it('does not count think time or markers as execution phase time', () => {
    const think = { ...leaf('think', 300, 5000, [{ key: 'total', startMs: 0, endMs: 5000 }]), kind: 'think' as const }
    const marker = { ...leaf('m', 0, 1000, [{ key: 'total', startMs: 0, endMs: 1000 }]), kind: 'marker' as const }
    const tx = parent('tx', [leaf('a', 0, 300), think, marker])
    expect([...phaseTotals(tx)]).toEqual([['wait', 300]])
  })
})

describe('page/step groups are not leaves', () => {
  it('counts the child requests of a page row once', () => {
    const page: WaterfallNode = { ...parent('page', [leaf('r1', 0, 100, undefined, { status: 'failed' }), leaf('r2', 100, 100)]), kind: 'step', status: 'failed' }
    const sinks = topTimeSinks([page], 200)
    expect(sinks.find(s => s.id === 'failures')?.label).toBe('1 failure')
    expect(sinks.find(s => s.id === 'slowest')?.label).toBe('2 requests = 100% of elapsed')
  })
})

describe('filtered copies keep their timing', () => {
  it('computes self time and gaps from every child, not just the matches', () => {
    const tx: WaterfallNode = { ...parent('tx', [leaf('ok1', 0, 400), leaf('bad', 400, 100, undefined, { status: 'failed' }), leaf('ok2', 500, 500)]), startMs: 0, durationMs: 1000 }
    const [copy] = filterTree([tx], n => n.status === 'failed')
    expect(copy.children.map(c => c.id)).toEqual(['bad'])
    expect(selfChildTime(copy)).toEqual(selfChildTime(tx))
    expect(childGaps(copy)).toEqual([])
    expect([...phaseTotals(copy)]).toEqual([...phaseTotals(tx)])
  })
})

describe('round-7 analysis fixes', () => {
  it('keeps closing markers off the critical path and sees filtered-out siblings', () => {
    const marker = { ...leaf('close', 0, 1000, [{ key: 'total', startMs: 0, endMs: 1000 }]), kind: 'marker' as const }
    const a = leaf('a', 0, 100), b = leaf('b', 0, 500)
    const tx: WaterfallNode = { ...parent('tx', [a, b, marker]), startMs: 0, durationMs: 1000 }
    expect(criticalPathIds([tx]).has('close')).toBe(false)
    expect(criticalPathIds([tx]).has('b')).toBe(true)
    const [onlyA] = filterTree([tx], n => n.id === 'a')
    const ids = criticalPathIds([onlyA])
    expect(ids.has('a')).toBe(false)
    expect(ids.has('b')).toBe(true)
  })
})
