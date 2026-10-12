import { describe, expect, it } from 'vitest'

import { SCENARIO_PARITY_FIXTURES } from '../test/scenarioAssemblyParityFixtures'
import type { TestSummary } from '../types'
import { scenarioDraftFromStoredScenario, scenarioLoadPreview } from './scenarioAssembly'
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

  it('draws a queued run exactly as the scenario editors draw its scenario (AUTH-3)', () => {
    // The execution preview and the editors share one projection, so start
    // offsets, stages, baselines and goal TPS agree for every corpus scenario.
    for (const scenario of SCENARIO_PARITY_FIXTURES.filter(fixture => !fixture.traffic_profile)) {
      const groups = [...scenario.scripts].sort((left, right) => left.order - right.order)
      const queued = buildQueuedScenarioPreview({
        id: scenario.id,
        name: scenario.name,
        status: 'QUEUED',
        created_at: '',
        total_vusers: groups.reduce((sum, group) => sum + group.vusers, 0),
        duration: scenario.duration,
        start_delay: scenario.start_delay,
        resource_baseline: scenario.resource_baseline,
        scenario_mode: scenario.scenario_mode,
        goal_config: scenario.goal_config,
        groups: groups.map(group => ({
          name: group.name || '',
          script_id: group.script_id,
          script_name: group.script_name || '',
          protocol: group.protocol || 'http',
          vusers: group.vusers,
          ramp_up: group.ramp_up,
          ramp_down: group.ramp_down,
          start_offset: group.start_offset,
          stages: group.stages,
          role: group.role,
          initialization: group.initialization,
        })),
      } as TestSummary)
      const edited = scenarioLoadPreview(scenarioDraftFromStoredScenario(scenario))
      expect(queued.points, scenario.id).toEqual(edited.points)
      expect(queued.totalSeconds, scenario.id).toBe(edited.totalSeconds)
      expect(queued.preBaselineSeconds, scenario.id).toBe(edited.preBaselineSeconds)
    }
  })

  it('starts a delayed group at its offset and holds it to the shared deadline', () => {
    const preview = buildQueuedScenarioPreview({
      id: 'offset', name: 'Offset', status: 'QUEUED', created_at: '', total_vusers: 30, duration: '2m',
      groups: [
        { name: 'Now', script_id: 'a', script_name: 'A', protocol: 'http', vusers: 10 },
        { name: 'Later', script_id: 'b', script_name: 'B', protocol: 'http', vusers: 20, start_offset: '30s', ramp_up: '20s' },
      ],
    } as TestSummary)
    const at = (second: number) => preview.points.find(point => point.second === second)
    // The engine starts the delayed group's first VUser as it activates and
    // one more every second (20 s / 20), so all 20 run from 49 s.
    expect(at(29)?.group1 ?? 0).toBe(0)
    expect(at(30)).toMatchObject({ group0: 10, group1: 1 })
    expect(at(49)).toMatchObject({ group0: 10, group1: 20 })
    expect(preview.points.filter(point => point.second > 30 && point.second < 49).every(point => point.group1 > 1 && point.group1 < 20)).toBe(true)
    expect(at(120)).toMatchObject({ group0: 0, group1: 0 })
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