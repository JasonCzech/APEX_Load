import { describe, expect, it } from 'vitest'

import { buildExecutionStatusModel, describeGoalController } from './testExecutionStatusModel'

describe('testExecutionStatusModel', () => {
  it('builds the derived execution status fields from test data', () => {
    const model = buildExecutionStatusModel({
      id: 'test-1',
      name: 'Checkout',
      status: 'FAILED',
      scenario_mode: 'goal_oriented',
      script_shakeout: { passed: false },
      error: 'Shakeout failed on login action',
      total_vusers: 20,
      duration: '5m',
      created_at: '2026-05-12T20:00:00Z',
      groups: [
        { name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', vusers: 10 },
        { name: 'Search', script_id: 'script-2', script_name: 'Search Script', protocol: 'http', vusers: 10 },
      ],
      live_metrics: {
        tps: 42.5,
        active_vusers: 18,
        groups: {
          Checkout: { name: 'Checkout', vusers: 10, tps: 20.1, avg_response_ms: 120, p90_ms: 210, errors: 1 },
          Search: { name: 'Search', vusers: 8, tps: 22.4, avg_response_ms: 90, p90_ms: 140, errors: 0 },
        },
        transactions: {
          Login: { name: 'Login', count: 10, errors: 1, avg_ms: 100, p50_ms: 95, p90_ms: 120, p95_ms: 140, p99_ms: 180 },
        },
        goal_status: {
          current_tps: 42.5,
          target_average_tps: 50,
          breach_active: false,
          stop_triggered: false,
        },
      },
    } as never)

    expect(model.isMultiGroup).toBe(true)
    expect(model.groupNames).toEqual(['Checkout', 'Search'])
    expect(model.isGoalOriented).toBe(true)
    expect(model.shakeoutFailed).toBe(true)
    expect(model.groupMetrics).toHaveLength(2)
    expect(model.transactions).toHaveLength(1)
    expect(model.goalStatus?.target_average_tps).toBe(50)
  })

  it('describes the goal controller state for the latest scale direction', () => {
    expect(describeGoalController({
      last_scale_direction: 'up',
      last_scale_reason: 'sla_response_time',
      breach_active: false,
      stop_triggered: false,
    } as never)).toBe('Last scale up due to sla response time')
  })
})