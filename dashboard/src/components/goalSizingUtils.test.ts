import { describe, expect, it } from 'vitest'

import { baselineGroupOptions, littlesLawVUsers } from './goalSizingUtils'
import { RunArchiveProvenance, RunArchiveReport } from '../types'

function archiveReport(partial: Partial<RunArchiveReport>): RunArchiveReport {
  return {
    manifest: { schema_version: '1', test_id: 'b', test_name: 'Baseline', created_at: '', transaction_count: 0, resource_sample_count: 0, interval_summary_count: 0, sla_breached: false, transactions_file: '', resource_samples_file: '', interval_summaries_file: '' },
    overview: { transactions: 0, errors: 0, error_pct: 0, avg_response_ms: 0, min_response_ms: 0, max_response_ms: 0, resource_samples: 0, interval_summaries: 0, peak_tps: 0, peak_active_vusers: 0 },
    by_action: {},
    resource_peaks: { cpu_process_pct: 0, mem_process_mb: 0, net_sent_kbps: 0, net_recv_kbps: 0 },
    ...partial,
  } as RunArchiveReport
}

describe('littlesLawVUsers', () => {
  it('reproduces the 100 TPS / 3s -> 300 VUsers example', () => {
    expect(littlesLawVUsers(100, 3000)).toBe(300)
  })

  it('rounds up and clamps to a minimum of 1', () => {
    expect(littlesLawVUsers(10, 334)).toBe(4) // 10 * 0.334 = 3.34 -> 4
    expect(littlesLawVUsers(1, 50)).toBe(1) // 0.05 -> 1
  })

  it('returns 0 for non-positive inputs', () => {
    expect(littlesLawVUsers(0, 3000)).toBe(0)
    expect(littlesLawVUsers(100, 0)).toBe(0)
  })
})

describe('baselineGroupOptions', () => {
  it('uses per-group rollup when present', () => {
    const report = archiveReport({
      manifest: { ...archiveReport({}).manifest, provenance: { groups: [
        { name: 'Checkout', script_id: 'script-a', script_name: 'Checkout', protocol: 'http', vusers: 1 },
        { name: 'Search', script_id: 'script-b', script_name: 'Search', protocol: 'http', vusers: 1 },
      ] } as RunArchiveProvenance },
      by_group: {
        Checkout: { transactions: 2, errors: 0, error_pct: 0, avg_response_ms: 3000, min_response_ms: 2000, max_response_ms: 4000 },
        Search: { transactions: 1, errors: 0, error_pct: 0, avg_response_ms: 500, min_response_ms: 500, max_response_ms: 500 },
      },
    })
    const options = baselineGroupOptions(report)
    expect(options).toHaveLength(2)
    expect(options[0]).toMatchObject({ group: 'Checkout', script_id: 'script-a', baseline_art_ms: 3000, basis: 'group' })
    expect(options[1]).toMatchObject({ group: 'Search', script_id: 'script-b', baseline_art_ms: 500 })
  })

  it('falls back to overall ART when no per-group rollup exists', () => {
    const report = archiveReport({
      manifest: { ...archiveReport({}).manifest, provenance: { groups: [
        { name: 'Main', script_id: 'script-a', script_name: 'Main', protocol: 'http', vusers: 1 },
      ] } as RunArchiveProvenance },
      overview: { ...archiveReport({}).overview, avg_response_ms: 2000 },
    })
    const options = baselineGroupOptions(report)
    expect(options).toHaveLength(1)
    expect(options[0]).toMatchObject({ group: 'Main', script_id: 'script-a', baseline_art_ms: 2000, basis: 'overview' })
  })

  it('returns nothing when there is no response-time data', () => {
    expect(baselineGroupOptions(archiveReport({}))).toEqual([])
    expect(baselineGroupOptions(null)).toEqual([])
  })
})
