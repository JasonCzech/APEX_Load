import { describe, expect, it } from 'vitest'

import type { MetricsHistoryPoint, RunArchiveReport } from '../../types'
import { alignBaselineTimeline, buildBreachWindows, buildReportKpis, mergeTimelineHistory, slaRowsForReport } from './runReportModel'

const point = (time: string, timestampMs: number, values: Partial<MetricsHistoryPoint> = {}): MetricsHistoryPoint => ({
  time,
  timestampMs,
  vusers: 1,
  tps: 1,
  avgMs: 100,
  errorPct: 0,
  p50: 80,
  p90: 100,
  p95: 120,
  p99: 150,
  ...values,
})

describe('runReportModel', () => {
  it('merges summary and resource history by timestamp', () => {
    const merged = mergeTimelineHistory(
      [point('10:00:00', 1_000, { tps: 12 })],
      [point('10:00:00', 1_000, { cpuProcess: 44 }), point('10:00:01', 2_000, { resourceSampleOnly: 1, cpuProcess: 30 })],
    )

    expect(merged).toHaveLength(2)
    expect(merged[0]).toMatchObject({ tps: 12, cpuProcess: 44 })
    expect(merged[1]).toMatchObject({ timestampMs: 2_000, resourceSampleOnly: 1 })
  })

  it('aligns baseline curves by nearest timestamp instead of sample index', () => {
    const aligned = alignBaselineTimeline(
      [point('10:00:00', 1_000), point('10:00:10', 10_000)],
      [point('09:59:59', 900, { tps: 8 }), point('10:00:11', 11_000, { tps: 20, p95: 300 })],
    )

    expect(aligned[0].baselineTps).toBe(8)
    expect(aligned[1].baselineTps).toBe(20)
    expect(aligned[1].baselineP95).toBe(300)
  })

  it('detects contiguous SLA breach windows', () => {
    const windows = buildBreachWindows([
      point('a', 1, { p95: 100, errorPct: 0 }),
      point('b', 2, { p95: 700, errorPct: 0 }),
      point('c', 3, { p95: 750, errorPct: 0 }),
      point('d', 4, { p95: 100, errorPct: 10 }),
    ], 500, 5)

    expect(windows).toEqual([{ start: 'b', end: 'd' }])
  })

  it('builds KPIs and SLA rows from the report contract', () => {
    const report: RunArchiveReport = {
      manifest: {} as RunArchiveReport['manifest'],
      overview: {
        transactions: 10,
        errors: 1,
        error_pct: 10,
        avg_response_ms: 120,
        min_response_ms: 10,
        max_response_ms: 300,
        p95_ms: 250,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 30,
        peak_active_vusers: 5,
      },
      completeness: { score_pct: 98, status: 'complete' },
      by_action: {},
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 100,
        net_sent_kbps: 1,
        net_recv_kbps: 1,
        disk_read_kbps: 1,
        disk_write_kbps: 1,
      },
      sla_rows: {
        Checkout: { name: 'Checkout', volume: 10, p95_ms: 250, sla_ms: 300, has_sla: true, error_pct: 0, result: 'Pass', result_kind: 'pass' },
      },
    }

    expect(buildReportKpis(report, [point('a', 1)])).toHaveLength(6)
    expect(slaRowsForReport(report)[0].name).toBe('Checkout')
  })
})
