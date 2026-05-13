import { describe, expect, it } from 'vitest'

import { buildQueuedScenarioPreview, buildQueuedScenarioRows } from './testExecutionScenarioModel'

describe('testExecutionScenarioModel', () => {
  it('builds the queued scenario preview timeline with resource baselines and a final zero point', () => {
    const preview = buildQueuedScenarioPreview({
      id: 'test-1',
      name: 'Queued Checkout',
      status: 'QUEUED',
      total_vusers: 15,
      duration: '2m',
      created_at: '2026-05-12T20:00:00Z',
      resource_baseline: true,
      groups: [{
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 15,
        ramp_up: '30s',
        ramp_down: '15s',
        initialization: 'at_runtime',
      }],
    })

    expect(preview.totalVUsers).toBe(15)
    expect(preview.steadySeconds).toBe(120)
    expect(preview.preBaselineSeconds).toBe(300)
    expect(preview.postBaselineSeconds).toBe(300)
    expect(preview.totalSeconds).toBe(735)
    expect(preview.points[0]).toEqual({ second: 0, total: 0, group0: 0 })
    expect(preview.points[preview.points.length - 1]).toEqual({ second: 735, total: 0, group0: 0 })
  })

  it('builds queued scenario rows from script details without page-level mapping', () => {
    const rows = buildQueuedScenarioRows({
      id: 'test-1',
      name: 'Queued Checkout',
      status: 'QUEUED',
      total_vusers: 15,
      duration: '2m',
      created_at: '2026-05-12T20:00:00Z',
      groups: [{
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 15,
        ramp_up: '30s',
        ramp_down: '15s',
        initialization: 'at_runtime',
      }],
    }, {
      'script-1': {
        id: 'script-1',
        name: 'Checkout Script',
        protocol: 'http',
        script: {
          init: [{}],
          actions: [{}, {}],
          teardown: [{}],
          data_sources: [{ name: 'users' }],
        },
      },
    })

    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      actionCount: 2,
      initCount: 1,
      teardownCount: 1,
      dataSourceCount: 1,
    })
    expect(rows[0].group.name).toBe('Checkout')
    expect(rows[0].detail?.name).toBe('Checkout Script')
  })
})