import { describe, expect, it } from 'vitest'

import { buildComparisonActionCsv, comparisonCsvFilename, type ComparisonCsvCard } from './comparisonCsv'

const card = (id: string, ref: string, p95: number, errorPct: number): ComparisonCsvCard => ({
  test: { id, ref_key: ref, name: `Run ${ref}` } as ComparisonCsvCard['test'],
  report: { metric_populations: {}, by_request: { 'GET /cart': { transactions: 100, errors: 2, error_pct: errorPct, avg_response_ms: 120, min_response_ms: 1, max_response_ms: 900, p95_ms: p95 } } } as unknown as ComparisonCsvCard['report'],
})

describe('comparison CSV', () => {
  it('exports the displayed per-action comparison with deltas against the baseline', () => {
    const csv = buildComparisonActionCsv([card('a', 'LOC-RUN-1', 400, 1), card('b', 'LOC-RUN-2', 450, 3)])
    const lines = csv.trim().split('\n')
    expect(lines).toHaveLength(3)
    expect(lines[0]).toContain('p95_ms_delta_vs_baseline')
    expect(lines[1]).toContain('GET /cart,LOC-RUN-1,Run LOC-RUN-1,true')
    expect(lines[2]).toMatch(/GET \/cart,LOC-RUN-2,Run LOC-RUN-2,false,100,2,3,120,,,450,,,0,50,2$/)
  })

  it('keeps operator-controlled names from running as spreadsheet formulas', () => {
    const base = card('a', 'LOC-RUN-1', 400, 1)
    const named = { ...base, test: { ...base.test, name: '=HYPERLINK("x")' }, report: { metric_populations: {}, by_request: { '+SUM(A1)': { transactions: 1, errors: 0, error_pct: 0, avg_response_ms: 1, p95_ms: 1 } } } as unknown as ComparisonCsvCard['report'] }
    const [, row] = buildComparisonActionCsv([named, card('b', 'LOC-RUN-2', 350, 0)]).trim().split('\n')
    expect(row.startsWith(`'+SUM(A1),LOC-RUN-1,"'=HYPERLINK(""x"")",true`)).toBe(true)
    // A negative delta is a number, not text, and is left alone.
    expect(buildComparisonActionCsv([card('a', 'LOC-RUN-1', 400, 1), card('b', 'LOC-RUN-2', 350, 0)])).toContain(',0,-50,-1\n')
  })

  it('names the file after the runs’ reference keys', () => {
    expect(comparisonCsvFilename([card('a', 'LOC-RUN-1', 1, 0), card('b', 'LOC-RUN-2', 1, 0)])).toBe('run-comparison-LOC-RUN-1-vs-LOC-RUN-2.csv')
  })
})
