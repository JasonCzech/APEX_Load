import { describe, expect, it } from 'vitest'

import { buildGeneratorImpactView, classifyGeneratorImpact, resourceImpactHistoryValues } from './generatorImpact'

describe('generatorImpact', () => {
  it('maps enhanced resource samples into chart history values', () => {
    expect(resourceImpactHistoryValues({
      cpu_iowait_pct: 12,
      cpu_steal_pct: 2,
      gc_pause_ms: 35,
      container_cpu_usage_pct: 78,
      container_cpu_throttled_seconds_rate: 0.2,
      container_memory_usage_pct: 81,
      container_oom_kill_count_delta: 1,
      net_drop_in_per_sec: 2,
      net_drop_out_per_sec: 3,
      net_error_in_per_sec: 4,
      net_error_out_per_sec: 5,
      tcp_retransmits_per_sec: 6,
      disk_io_time_pct: 44,
    })).toEqual({
      cpuIOWait: 12,
      cpuSteal: 2,
      gcPauseMs: 35,
      containerCPUUsagePct: 78,
      containerCPUThrottledSecondsRate: 0.2,
      containerMemoryUsagePct: 81,
      containerOOMKillCountDelta: 1,
      netDropsPerSec: 5,
      netErrorsPerSec: 9,
      tcpRetransmitsPerSec: 6,
      diskIOTimePct: 44,
    })
  })

  it('keeps missing resource samples unknown', () => {
    expect(classifyGeneratorImpact(undefined, 120, 0)).toEqual({
      verdict: 'unknown',
      reasons: [],
      metrics: {},
    })
  })

  it('warns for saturation without run degradation', () => {
    const result = classifyGeneratorImpact({
      cpu_process_pct: 20,
      cpu_free_pct: 60,
      mem_process_mb: 128,
      mem_free_mb: 2048,
      net_sent_kbps: 1,
      net_recv_kbps: 1,
      disk_read_kbps: 0,
      disk_write_kbps: 0,
      container_cpu_throttled_periods_delta: 3,
    }, 200, 0)

    expect(result.verdict).toBe('warning')
    expect(result.reasons).toEqual(['CPU throttling'])
  })

  it('marks likely impact when saturation overlaps latency or errors', () => {
    const result = classifyGeneratorImpact({
      cpu_process_pct: 96,
      cpu_free_pct: 5,
      mem_process_mb: 512,
      mem_free_mb: 1024,
      net_sent_kbps: 1,
      net_recv_kbps: 1,
      disk_read_kbps: 0,
      disk_write_kbps: 0,
      container_memory_usage_pct: 93,
      tcp_retransmits_per_sec: 2,
    }, 1400, 0)

    expect(result.verdict).toBe('danger')
    expect(result.reasons).toEqual(['CPU saturation', 'Memory near limit', 'Network loss/retransmits'])
  })

  it('builds per-generator rows from live generator resource snapshots', () => {
    const view = buildGeneratorImpactView({
      timestamp: '2026-05-12T20:00:00Z',
      active_vusers: 20,
      tps: 10,
      avg_response_ms: 100,
      error_pct: 0,
      total_transactions: 10,
      total_errors: 0,
      p50_ms: 80,
      p90_ms: 120,
      p95_ms: 150,
      p99_ms: 180,
      generator_resources: {
        'gen-b': {
          generator_id: 'gen-b',
          target: '10.0.0.2',
          active_vusers: 4,
          tps: 1,
          p95_ms: 80,
          error_pct: 0,
          resources: {
            cpu_process_pct: 10,
            cpu_free_pct: 60,
            mem_process_mb: 128,
            mem_free_mb: 2048,
            net_sent_kbps: 1,
            net_recv_kbps: 1,
            disk_read_kbps: 0,
            disk_write_kbps: 0,
          },
          timestamp: '2026-05-12T20:00:00Z',
        },
        'gen-a': {
          generator_id: 'gen-a',
          target: '10.0.0.1',
          active_vusers: 16,
          tps: 9,
          p95_ms: 1300,
          error_pct: 0,
          resources: {
            cpu_process_pct: 97,
            cpu_free_pct: 2,
            mem_process_mb: 512,
            mem_free_mb: 1024,
            net_sent_kbps: 2,
            net_recv_kbps: 2,
            disk_read_kbps: 0,
            disk_write_kbps: 0,
          },
          timestamp: '2026-05-12T20:00:00Z',
        },
      },
    })

    expect(view.verdict).toBe('danger')
    expect(view.rows.map(row => row.id)).toEqual(['gen-a', 'gen-b'])
    expect(view.rows[0].reasons).toContain('CPU saturation')
    expect(view.rows[0].reasons).toContain('Generator imbalance')
    expect(view.rows[1].reasons).toContain('Generator imbalance')
  })
})
