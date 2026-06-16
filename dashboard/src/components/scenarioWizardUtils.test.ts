import { describe, expect, it } from 'vitest'

import { buildScenarioModelPreview, buildScenarioShapeCatalog, getScenarioShapePreset, parseDurationSeconds, SCENARIO_SHAPE_PRESETS, validateStageTimeline } from './scenarioWizardUtils'
import { LoadModelSupportEntry, LoadModelTemplate } from '../types'

describe('scenarioWizardUtils', () => {
  it('merges supported workload shapes with matching templates', () => {
    const support: LoadModelSupportEntry[] = [
      { id: 'closed-vuser', name: 'Closed VUser', category: 'vuser', status: 'executed', apexload_shape: 'Closed model', fields: ['groups[].vusers'], notes: 'Ready' },
      { id: 'stress', name: 'Stress', category: 'capacity', status: 'executed', apexload_shape: 'Stress model', fields: ['groups[].stages'], notes: 'Ready' },
    ]
    const templates: LoadModelTemplate[] = [
      { id: 'closed-smoke', name: 'Closed Smoke', shape_id: 'closed-vuser', description: 'Smoke', scenario: { duration: '3m', groups: [] } },
    ]

    const catalog = buildScenarioShapeCatalog(support, templates)

    expect(catalog).toHaveLength(2)
    expect(catalog[0].templates.map(template => template.id)).toEqual(['closed-smoke'])
    expect(catalog[1].templates).toEqual([])
  })

  it('parses source-style duration strings into seconds', () => {
    expect(parseDurationSeconds('1h 2m 3s')).toBe(3723)
    expect(parseDurationSeconds('90s')).toBe(90)
    expect(parseDurationSeconds('2m')).toBe(120)
    expect(parseDurationSeconds('', 45)).toBe(45)
  })

  it('builds a simple VUser ramp model with ramp down', () => {
    const preview = buildScenarioModelPreview({
      duration: '10m',
      groups: [{ name: 'Checkout', vusers: 10, ramp_up: '2m', ramp_down: '1m' }],
    })

    const start = preview.points.find(point => point.elapsedSeconds === 0)
    const rampComplete = preview.points.find(point => point.elapsedSeconds === 120)
    const rampDownStart = preview.points.find(point => point.elapsedSeconds === 540)
    const end = preview.points.find(point => point.elapsedSeconds === 600)

    expect(start?.totalVusers).toBe(0)
    expect(rampComplete?.group_0_vusers).toBe(10)
    expect(rampDownStart?.totalVusers).toBe(10)
    expect(end?.totalVusers).toBe(0)
    expect(preview.peakVusers).toBe(10)
  })

  it('builds staged VUser and TPS model points', () => {
    const preview = buildScenarioModelPreview({
      duration: '4m',
      groups: [{
        name: 'API',
        vusers: 5,
        stages: [
          { type: 'ramp_up', duration: '1m', target_vusers: 20, target_tps: 15 },
          { type: 'hold', duration: '2m', target_vusers: 20, target_tps: 25 },
          { type: 'ramp_down', duration: '1m', target_vusers: 0, target_tps: 0 },
        ],
      }],
    })

    const hold = preview.points.find(point => point.elapsedSeconds === 180)
    const end = preview.points.find(point => point.elapsedSeconds === 240)

    expect(hold?.totalVusers).toBe(20)
    expect(hold?.totalTps).toBe(25)
    expect(end?.totalVusers).toBe(0)
    expect(preview.peakTps).toBe(25)
    expect(preview.series.some(series => series.kind === 'tps')).toBe(true)
  })

  it('validates stage timelines against scenario duration', () => {
    const shortTimeline = validateStageTimeline('10m', [
      { type: 'ramp_up', duration: '2m', target_vusers: 10 },
      { type: 'hold', duration: '5m', target_vusers: 10 },
    ])
    expect(shortTimeline.status).toBe('warning')
    expect(shortTimeline.message).toContain('last target holds')

    const longTimeline = validateStageTimeline('5m', [
      { type: 'ramp_up', duration: '3m', target_vusers: 10 },
      { type: 'hold', duration: '3m', target_vusers: 10 },
    ])
    expect(longTimeline.status).toBe('error')
    expect(longTimeline.message).toContain('longer than')
  })
})

describe('SCENARIO_SHAPE_PRESETS', () => {
  const SHAPE_IDS = [
    'closed-vuser',
    'open-arrival-rate',
    'stepped-ramp',
    'spike-rendezvous',
    'soak',
    'stress',
    'goal-based',
    'chaos-resilience',
    'mixed-protocol',
  ]

  it('defines a preset for every workload shape with non-empty guidance', () => {
    expect(Object.keys(SCENARIO_SHAPE_PRESETS).sort()).toEqual([...SHAPE_IDS].sort())
    SHAPE_IDS.forEach((id) => {
      const preset = getScenarioShapePreset(id)
      expect(preset).toBeDefined()
      expect(preset?.guidance.length).toBeGreaterThan(0)
      expect(preset?.groups.length).toBeGreaterThan(0)
    })
  })

  it('returns undefined for an unknown shape id', () => {
    expect(getScenarioShapePreset('nope')).toBeUndefined()
  })

  it('keeps staged stage totals within the scenario duration so Next is never blocked', () => {
    SHAPE_IDS.forEach((id) => {
      const preset = getScenarioShapePreset(id)!
      const scenarioSeconds = parseDurationSeconds(preset.duration)
      preset.groups.forEach((group) => {
        if (!group.stages?.length) return
        const validation = validateStageTimeline(preset.duration, group.stages)
        expect(validation.status).not.toBe('error')
        const stageSeconds = group.stages.reduce((sum, stage) => sum + parseDurationSeconds(stage.duration), 0)
        expect(stageSeconds).toBeLessThanOrEqual(scenarioSeconds)
      })
    })
  })

  it('sets staged group vusers to the staged peak', () => {
    ;['stepped-ramp', 'stress', 'chaos-resilience'].forEach((id) => {
      const group = getScenarioShapePreset(id)!.groups[0]
      const peak = Math.max(...(group.stages || []).map(stage => Number(stage.target_vusers) || 0))
      expect(group.vusers).toBe(peak)
    })
  })

  it('uses goal_oriented mode for adaptive/throughput shapes', () => {
    ;['open-arrival-rate', 'stress', 'goal-based', 'chaos-resilience'].forEach((id) => {
      expect(getScenarioShapePreset(id)?.scenarioMode).toBe('goal_oriented')
    })
  })

  it('keeps closed-vuser a single group with no stages (protects the manual-shape smoke flow)', () => {
    const preset = getScenarioShapePreset('closed-vuser')!
    expect(preset.groups).toHaveLength(1)
    expect(preset.groups[0].stages ?? []).toHaveLength(0)
    expect(preset.scenarioMode).toBe('vuser_driven')
  })

  it('seeds two groups for mixed-protocol', () => {
    const preset = getScenarioShapePreset('mixed-protocol')!
    expect(preset.groups).toHaveLength(2)
    expect(preset.suggestMultipleGroups).toBe(true)
    expect(preset.groups.map(group => group.name)).toEqual(['api-bulk', 'browser-canary'])
  })

  it('mirrors the chaos-resilience starter template numbers', () => {
    const preset = getScenarioShapePreset('chaos-resilience')!
    expect(preset.duration).toBe('18m')
    expect(preset.scriptShakeout).toBe(true)
    expect(preset.snapshotOnError).toBe(true)
    expect(preset.goal).toMatchObject({ target_average_tps: 15, min_vusers: 5, max_vusers: 75, p95_latency_ms: 1200, max_error_pct: 5 })
    const stages = preset.groups[0].stages!
    expect(stages.map(stage => stage.name)).toEqual(['Baseline', 'Turbulence', 'Recovery'])
    expect(stages[1]).toMatchObject({ type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 })
  })
})
