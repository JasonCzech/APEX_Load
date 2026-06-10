import { describe, expect, it } from 'vitest'

import { buildScenarioModelPreview, buildScenarioShapeCatalog, parseDurationSeconds, validateStageTimeline } from './scenarioWizardUtils'
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
