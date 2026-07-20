import { describe, expect, it } from 'vitest'

import { TestSummary } from '../types'
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
      vuserTransitionSeconds: 0,
      isGradualVuserChange: false,
      hasActiveVuserRamp: false,
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
    expect(preview.vuserTransitionSeconds).toBe(30)
    expect(preview.isGradualVuserChange).toBe(false)
    expect(preview.hasActiveVuserRamp).toBe(false)
    expect(preview.hasPendingChanges).toBe(true)
    expect(preview.points).toEqual([
      { second: 0, total: 30, group0: 12, group1: 18 },
      { second: 30, total: 36, group0: 16, group1: 20 },
      { second: 600, total: 36, group0: 16, group1: 20 },
      { second: 601, total: 0, group0: 0, group1: 0 },
    ])
  })

  it('uses the requested gradual VUser ramp duration in the preview', () => {
    const preview = buildLiveControlPreview({
      test: buildRunningTest(),
      durationInput: '10m',
      vuserTargets: {
        Checkout: 20,
      },
      vuserRampEnabled: true,
      vuserRampDurationInput: '2m',
      nowMs: Date.parse('2026-05-12T20:02:00Z'),
    })

    expect(preview.vuserTransitionSeconds).toBe(120)
    expect(preview.isGradualVuserChange).toBe(true)
    expect(preview.hasActiveVuserRamp).toBe(false)
    expect(preview.points).toEqual([
      { second: 0, total: 12, group0: 12 },
      { second: 120, total: 20, group0: 20 },
      { second: 480, total: 20, group0: 20 },
      { second: 481, total: 0, group0: 0 },
    ])
  })

  it('projects a resumable group pause to zero VUsers', () => {
    const preview = buildLiveControlPreview({
      test: buildRunningTest(),
      durationInput: '10m',
      vuserTargets: {
        Checkout: 20,
      },
      vuserGroupActions: {
        Checkout: 'pause',
      },
      nowMs: Date.parse('2026-05-12T20:02:00Z'),
    })

    expect(preview.currentTotal).toBe(12)
    expect(preview.projectedTotal).toBe(0)
    expect(preview.hasPendingChanges).toBe(true)
    expect(preview.points).toEqual([
      { second: 0, total: 12, group0: 12 },
      { second: 29, total: 0, group0: 0 },
      { second: 480, total: 0, group0: 0 },
      { second: 481, total: 0, group0: 0 },
    ])
  })

  it('projects an already-running gradual VUser ramp to its scheduled target', () => {
    const preview = buildLiveControlPreview({
      test: {
        ...buildRunningTest(),
        groups: [
          {
            name: 'Checkout',
            script_id: 'script-1',
            script_name: 'Checkout Script',
            protocol: 'http',
            vusers: 14,
          },
        ],
        live_metrics: {
          timestamp: '2026-05-12T20:02:30Z',
          active_vusers: 15,
          tps: 0,
          avg_response_ms: 0,
          error_pct: 0,
          total_transactions: 0,
          total_errors: 0,
          p50_ms: 0,
          p90_ms: 0,
          p95_ms: 0,
          p99_ms: 0,
          groups: {
            Checkout: {
              name: 'Checkout',
              vusers: 15,
              transactions: 0,
              errors: 0,
              tps: 0,
              avg_response_ms: 0,
              p50_ms: 0,
              p90_ms: 0,
              p99_ms: 0,
            },
          },
        },
        vuser_ramps: {
          Checkout: {
            group: 'Checkout',
            start_vusers: 12,
            target_vusers: 24,
            started_at: '2026-05-12T20:02:00Z',
            ramp_duration: '2m',
            ramp_duration_seconds: 120,
          },
        },
      },
      durationInput: '10m',
      vuserTargets: {},
      nowMs: Date.parse('2026-05-12T20:02:30Z'),
    })

    expect(preview.currentTotal).toBe(15)
    expect(preview.projectedTotal).toBe(24)
    expect(preview.vuserTransitionSeconds).toBe(90)
    expect(preview.isGradualVuserChange).toBe(true)
    expect(preview.hasActiveVuserRamp).toBe(true)
    expect(preview.hasPendingChanges).toBe(true)
    expect(preview.points).toEqual([
      { second: 0, total: 15, group0: 15 },
      { second: 90, total: 24, group0: 24 },
      { second: 450, total: 24, group0: 24 },
      { second: 451, total: 0, group0: 0 },
    ])
  })
})

function buildRunningTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Running Checkout',
    status: 'RUNNING',
    total_vusers: 12,
    duration: '10m',
    created_at: '2026-05-12T20:00:00Z',
    started_at: '2026-05-12T20:00:00Z',
    groups: [
      {
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 12,
      },
    ],
  }
}
