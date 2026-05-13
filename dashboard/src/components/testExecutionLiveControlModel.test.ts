import { describe, expect, it } from 'vitest'

import { buildLiveControlPreview } from './testExecutionLiveControlModel'

describe('testExecutionLiveControlModel', () => {
  it('builds an empty preview when no groups are available', () => {
    const preview = buildLiveControlPreview({
      test: null,
      durationInput: '',
      vuserTargets: {},
      nowMs: Date.parse('2026-05-12T20:10:00Z'),
    })

    expect(preview).toEqual({
      points: [],
      currentTotal: 0,
      projectedTotal: 0,
      remainingSeconds: 0,
      projectedDurationSeconds: 0,
      hasPendingChanges: false,
    })
  })

  it('builds a projected live-control preview with pending vuser and duration changes', () => {
    const preview = buildLiveControlPreview({
      test: {
        id: 'test-1',
        name: 'Running Checkout',
        status: 'RUNNING',
        total_vusers: 30,
        duration: '10m',
        created_at: '2026-05-12T20:00:00Z',
        started_at: '2026-05-12T20:00:00Z',
        live_metrics: {
          groups: {
            Checkout: { vusers: 12 },
            Search: { vusers: 18 },
          },
        },
        groups: [
          {
            name: 'Checkout',
            script_id: 'script-1',
            script_name: 'Checkout Script',
            protocol: 'http',
            vusers: 10,
          },
          {
            name: 'Search',
            script_id: 'script-2',
            script_name: 'Search Script',
            protocol: 'http',
            vusers: 20,
          },
        ],
      } as never,
      durationInput: '15m',
      vuserTargets: {
        Checkout: 16,
      },
      nowMs: Date.parse('2026-05-12T20:05:00Z'),
    })

    expect(preview.currentTotal).toBe(30)
    expect(preview.projectedTotal).toBe(36)
    expect(preview.projectedDurationSeconds).toBe(900)
    expect(preview.remainingSeconds).toBe(600)
    expect(preview.hasPendingChanges).toBe(true)
    expect(preview.points).toEqual([
      { second: 0, total: 30, group0: 12, group1: 18 },
      { second: 30, total: 36, group0: 16, group1: 20 },
      { second: 600, total: 36, group0: 16, group1: 20 },
      { second: 601, total: 0, group0: 0, group1: 0 },
    ])
  })
})