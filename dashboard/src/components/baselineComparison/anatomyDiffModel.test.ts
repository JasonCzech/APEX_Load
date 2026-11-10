import { describe, expect, it } from 'vitest'
import { anatomyOf, requestRow, stat, transaction } from './__fixtures__/anatomyDiff'
import {
  alignAnatomyRows,
  anatomyDiffAvailability,
  anatomyDiffNotes,
  anatomyDiffSinks,
  buildAnatomyDiffTree,
  meanAttribution,
  meanHeadline,
  reuseSentence,
  transactionConnectionReuse,
  transactionPercentileChange,
} from './anatomyDiffModel'

const login = (server: number, tls = 10) => requestRow(1, 'POST /login', { dns: 2, connect: 5, tls, server, download: 3 }, { offset: 0 })

describe('alignAnatomyRows', () => {
  it('matches by key, falls back to action#occurrence when steps were renumbered, and keeps one-sided rows', () => {
    const current = transaction('Login', [login(100), requestRow(4, 'GET /home', { server: 20 }, { offset: 120 }), requestRow(5, 'GET /new', { server: 5 })], {})
    const baseline = transaction('Login', [login(80), requestRow(3, 'GET /home', { server: 15 }, { offset: 100 }), requestRow(6, 'GET /gone', { server: 7 })], {})
    const { pairs, fallbackMatches } = alignAnatomyRows(current, baseline)
    expect(fallbackMatches).toBe(1)
    expect(pairs.map(p => [p.id, p.match ?? null, Boolean(p.current), Boolean(p.baseline)])).toEqual([
      ['1|POST /login#1', 'key', true, true],
      ['4|GET /home#1', 'action_occurrence', true, true],
      ['5|GET /new#1', null, true, false],
      ['baseline:6|GET /gone#1', null, false, true],
    ])
  })

  it('does not fall back when an action#occurrence is ambiguous', () => {
    const current = transaction('T', [requestRow(7, 'GET /a', { server: 1 })], {})
    const baseline = transaction('T', [requestRow(2, 'GET /a', { server: 1 }), requestRow(3, 'GET /a', { server: 1 })], {})
    const { pairs, fallbackMatches } = alignAnatomyRows(current, baseline)
    expect(fallbackMatches).toBe(0)
    expect(pairs.filter(p => p.match)).toHaveLength(0)
  })

  it('pairs the Other requests rows', () => {
    const current = { ...transaction('T', [], {}), other_requests: requestRow(0, 'Other requests', { server: 3 }) }
    const { pairs } = alignAnatomyRows(current, transaction('T', [], {}))
    expect(pairs).toEqual([expect.objectContaining({ id: 'other-requests', other: true })])
    expect(pairs[0].baseline).toBeUndefined()
  })
})

describe('buildAnatomyDiffTree', () => {
  const current = transaction('Login', [login(180), requestRow(5, 'GET /new', { server: 5 }, { offset: 200 })], {})
  const baseline = transaction('Login', [login(100), requestRow(6, 'GET /gone', { server: 7 })], {})

  it('draws current bars with the baseline as ghost and the change as delta', () => {
    const tree = buildAnatomyDiffTree(current, baseline, 'avg')
    const [matched, added, removed] = tree.roots
    expect(matched.durationMs).toBe(200)
    expect(matched.ghost?.durationMs).toBe(120)
    expect(matched.delta).toMatchObject({ ms: 80, outcome: 'same' })
    expect(matched.delta?.label).toMatch(/^\+80\.0 ms, all in server/)
    expect(added.delta).toMatchObject({ ms: 5, outcome: 'added' })
    expect(removed).toMatchObject({ status: 'not_run', startMs: null, durationMs: null })
    expect(removed.delta).toMatchObject({ ms: -7, outcome: 'removed' })
    expect(tree.hasDeltas).toBe(true)
    expect(tree.spanMs).toBe(205)
  })

  it('uses the chosen statistic per phase and names the request percentile apart', () => {
    const tree = buildAnatomyDiffTree(current, baseline, 'p95')
    const matched = tree.roots[0]
    // Bar = sum of each phase's P95 (2× avg in the fixture).
    expect(matched.durationMs).toBe(400)
    expect(matched.ghost?.durationMs).toBe(240)
    expect(matched.delta?.label).toContain('request P95 +120.0 ms')
    expect(tree.rows[0].phaseDeltas.server).toBe(160)
  })

  it('names an unsplit change as the drawer does, not as unmeasured time', () => {
    const cur = transaction('T', [requestRow(1, 'GET /slow', { server: 20, unsplit: 820 })], {})
    const base = transaction('T', [requestRow(1, 'GET /slow', { server: 20, unsplit: 20 })], {})
    const label = buildAnatomyDiffTree(cur, base, 'avg').roots[0].delta?.label
    expect(label).toMatch(/^\+800\.0 ms, all in no TTFB split/)
    expect(label).not.toContain('outside measured phases')
  })

  it('flags a fallback match in the row label', () => {
    const tree = buildAnatomyDiffTree(transaction('T', [requestRow(9, 'GET /x', { server: 20 })], {}), transaction('T', [requestRow(2, 'GET /x', { server: 10 })], {}), 'avg')
    expect(tree.fallbackMatches).toBe(1)
    expect(tree.roots[0].delta?.label).toContain('matched by action')
  })

  it('reports no deltas when nothing changed', () => {
    const t = transaction('T', [login(50)], {})
    expect(buildAnatomyDiffTree(t, t, 'avg').hasDeltas).toBe(false)
  })
})

describe('mean attribution', () => {
  it('splits the mean change by decomposition phase; shares sum to 100%', () => {
    const current = transaction('Login', [], { server: 253, tls: 37, download: 10 })
    const baseline = transaction('Login', [], { server: 100, tls: 10, download: 10 })
    const mean = meanAttribution(current, baseline)!
    expect(mean.deltaMs).toBe(180)
    expect(mean.headline).toBe('Mean +180.0 ms: 85% server, 15% TLS')
    expect(mean.components.reduce((s, c) => s + c.deltaMs, 0)).toBeCloseTo(180)
  })

  it('states amounts when another component offsets the change', () => {
    const current = transaction('T', [], { server: 300, download: 0 })
    const baseline = transaction('T', [], { server: 100, download: 20 })
    expect(meanAttribution(current, baseline)!.headline).toBe('Mean +180.0 ms: server +200.0 ms, download −20.0 ms')
  })

  it('counts overlap growth as time saved', () => {
    const mean = meanAttribution(transaction('T', [], { server: 100, overlap: 30 }), transaction('T', [], { server: 100, overlap: 10 }))!
    expect(mean.deltaMs).toBe(-20)
    expect(mean.components.find(c => c.component === 'overlap')?.deltaMs).toBe(-20)
    expect(mean.headline).toBe('Mean −20.0 ms: 100% overlap')
  })

  it('withholds shares and qualifies the headline when a run matched too few of its instances', () => {
    // Baseline joins 98 of 100; the current run only 25 (the rest lost requests to drops).
    const current = { ...transaction('Login', [], { server: 60, tls: 10, instances: 25 }), partial_instances: 75 }
    const baseline = transaction('Login', [], { server: 100, tls: 10, instances: 98 })
    const mean = meanAttribution(current, baseline)!
    expect(mean.deltaMs).toBe(-40)
    expect(mean.lowCoverage).toBe(true)
    expect(mean.coverage).toEqual({ current: { matched: 25, eligible: 100, ratio: 0.25 }, baseline: { matched: 98, eligible: 100, ratio: 0.98 } })
    expect(mean.headline).toBe('Mean −40.0 ms (low match coverage): server −40.0 ms')
    expect(mean.components.every(c => c.share === undefined)).toBe(true)
    const notes = anatomyDiffNotes(current, baseline, 'avg', 0).join(' ')
    expect(notes).toContain('Low match coverage: the means cover 98 of 100 instances (98%) in the baseline and 25 of 100 instances (25%) in this run')
    const sinks = anatomyDiffSinks(mean, null, null, buildAnatomyDiffTree(current, baseline, 'avg'))
    expect(sinks[0]).toMatchObject({ id: 'mean', label: mean.headline, detail: expect.stringContaining('matched 98 of 100 instances (98%) → 25 of 100 instances (25%)') })
  })

  it('leaves interrupted instances out of the coverage denominator', () => {
    const current = { ...transaction('T', [], { server: 200, instances: 80 }), interrupted_instances: 15 }
    const mean = meanAttribution(current, transaction('T', [], { server: 100 }))!
    expect(mean.coverage.current).toEqual({ matched: 80, eligible: 85, ratio: 80 / 85 })
    expect(mean.lowCoverage).toBe(false)
    expect(mean.headline).toBe('Mean +100.0 ms: 100% server')
    expect(anatomyDiffNotes(current, transaction('T', [], {}), 'avg', 0).join(' ')).not.toContain('Low match coverage')
  })

  it('is unavailable without matched instances', () => {
    expect(meanAttribution(transaction('T', [], { instances: 0 }), transaction('T', [], {}))).toBeNull()
    expect(meanHeadline(0.1, [])).toBe('Mean: no change')
  })

  it('reports the P95 change from duration, not from phases', () => {
    const current = transaction('T', [], { server: 10, p95: 900 })
    const baseline = { ...transaction('T', [], { server: 10, p95: 700 }) }
    baseline.duration = stat(10, 700, { percentile_quality: 'approximate' })
    expect(transactionPercentileChange(current, baseline)).toEqual({ statistic: 'p95', baselineMs: 700, currentMs: 900, deltaMs: 200, approximate: true })
  })
})

describe('connection reuse', () => {
  it('sums request rows and Other requests per run', () => {
    const current = { ...transaction('T', [requestRow(1, 'a', { server: 1 }, { newConns: 50, reused: 30 })], {}), other_requests: requestRow(0, 'Other requests', { server: 1 }, { newConns: 10, reused: 10 }) }
    const baseline = transaction('T', [requestRow(1, 'a', { server: 1 }, { newConns: 8, reused: 92 })], {})
    const c = transactionConnectionReuse(current)
    expect(c).toEqual({ newConnections: 60, reused: 40, reusePct: 40 })
    expect(reuseSentence(c, transactionConnectionReuse(baseline))).toBe('Connection reuse 92% → 40%')
  })

  it('says nothing when either run classified no connection', () => {
    const none = transactionConnectionReuse(transaction('T', [requestRow(1, 'a', { server: 1 })], {}))
    expect(none.reusePct).toBeUndefined()
    expect(reuseSentence(none, { newConnections: 1, reused: 1, reusePct: 50 })).toBeNull()
  })
})

describe('availability and notes', () => {
  const t = transaction('Login', [login(10)], {})
  it('explains missing anatomy and the 40-transaction emission cap', () => {
    expect(anatomyDiffAvailability('Login', undefined, anatomyOf([t]))).toMatchObject({ status: 'unavailable', message: expect.stringContaining('current run') })
    expect(anatomyDiffAvailability('Login', anatomyOf([t]), undefined)).toMatchObject({ status: 'unavailable', message: expect.stringContaining('baseline') })
    const capped = anatomyDiffAvailability('Login', anatomyOf([t]), anatomyOf([transaction('Other', [], {})], 3))
    expect(capped).toMatchObject({ status: 'unavailable', message: expect.stringContaining('left 3 out') })
    expect(anatomyDiffAvailability('Login', anatomyOf([t]), anatomyOf([t]))).toMatchObject({ status: 'ready' })
  })

  it('states scope, merging, percentile and fallback caveats', () => {
    const notes = anatomyDiffNotes(t, t, 'p95', 2)
    expect(notes.join(' ')).toMatch(/Whole run/)
    expect(notes.join(' ')).toMatch(/Groups are merged/)
    expect(notes.join(' ')).toMatch(/do not add up/)
    expect(notes.join(' ')).toMatch(/2 rows were matched by action/)
    expect(anatomyDiffNotes(t, t, 'avg', 0).join(' ')).not.toMatch(/do not add up/)
  })

  it('builds headline chips', () => {
    const tree = buildAnatomyDiffTree(t, transaction('Login', [], {}), 'avg')
    const sinks = anatomyDiffSinks(null, { statistic: 'p95', baselineMs: 1, currentMs: 2, deltaMs: 1, approximate: false }, 'Connection reuse 92% → 40%', tree)
    expect(sinks.map(s => s.id)).toEqual(['p95', 'reuse', 'rows'])
  })
})
