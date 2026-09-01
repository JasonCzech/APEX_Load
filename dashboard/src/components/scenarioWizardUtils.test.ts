import { describe, expect, it } from 'vitest'

import { adaptScenarioShapeDefaults, buildScenarioModelPreview, buildScenarioShapeCatalog, buildShapeStages, buildSteppedRampPlan, getScenarioShapePreset, MAX_STEPPED_RAMP_STEPS, parseDurationSeconds, SCENARIO_SHAPE_PRESETS, ShapeStageStrategy, steppedStepPlan, validateStageTimeline } from './scenarioWizardUtils'
import { LoadModelSupportEntry, LoadModelTemplate, TrafficProfile } from '../types'

const stageSeconds = (stages: { duration: string }[]) => stages.reduce((sum, stage) => sum + parseDurationSeconds(stage.duration), 0)
const stagePeak = (stages: { target_vusers?: number }[]) => Math.max(...stages.map(stage => Number(stage.target_vusers) || 0))

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
    expect(parseDurationSeconds('1h2m3s')).toBe(3723)
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
    const configuredEnd = preview.points.find(point => point.elapsedSeconds === 600)
    const end = preview.points.find(point => point.elapsedSeconds === 660)

    expect(start?.totalVusers).toBe(0)
    expect(rampComplete?.group_0_vusers).toBe(10)
    expect(configuredEnd?.totalVusers).toBe(10)
    expect(end?.totalVusers).toBe(0)
    expect(preview.peakVusers).toBe(10)
    expect(preview.durationSeconds).toBe(660)
  })

  it('stops zero-ramp-down groups at the steady boundary while other groups drain', () => {
    const preview = buildScenarioModelPreview({
      duration: '2m',
      groups: [
        { name: 'Immediate stop', vusers: 8 },
        { name: 'Draining', vusers: 12, ramp_down: '1m' },
      ],
    })

    const configuredEnd = preview.points.find(point => point.elapsedSeconds === 120)
    const drainEnd = preview.points.find(point => point.elapsedSeconds === 180)

    expect(configuredEnd?.group_0_vusers).toBe(0)
    expect(configuredEnd?.group_1_vusers).toBe(12)
    expect(configuredEnd?.totalVusers).toBe(12)
    expect(drainEnd?.totalVusers).toBe(0)
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

  it('models hold stages as instantaneous stair-step transitions at runtime boundaries', () => {
    const preview = buildScenarioModelPreview({
      duration: '30m',
      groups: [{
        name: 'API',
        vusers: 30,
        stages: [
          { type: 'hold', duration: '10m', target_vusers: 10 },
          { type: 'hold', duration: '10m', target_vusers: 20 },
          { type: 'hold', duration: '10m', target_vusers: 30 },
        ],
      }],
    })

    expect(preview.points.find(point => point.elapsedSeconds === 0)?.totalVusers).toBe(10)
    expect(preview.points.find(point => point.elapsedSeconds === 600)?.totalVusers).toBe(20)
    expect(preview.points.find(point => point.elapsedSeconds === 1200)?.totalVusers).toBe(30)
    expect(preview.series.find(series => series.key === 'group_0_vusers')?.interpolation).toBe('stepAfter')
    expect(preview.series.find(series => series.key === 'totalVusers')?.interpolation).toBe('stepAfter')
  })

  it('keeps true ramp stages linearly interpolated in the preview', () => {
    const preview = buildScenarioModelPreview({
      duration: '10m',
      groups: [{
        name: 'API',
        vusers: 30,
        stages: [{ type: 'ramp_up', duration: '10m', target_vusers: 30 }],
      }],
    })

    expect(preview.series.find(series => series.key === 'group_0_vusers')?.interpolation).toBe('linear')
    expect(preview.series.find(series => series.key === 'totalVusers')?.interpolation).toBe('linear')
  })

  it('plots a production TPS profile instead of flattening it to configured VUsers', () => {
    const trafficProfile: TrafficProfile = {
      version: 1,
      source: { kind: 'file', file: { filename: 'traffic.csv', timestamp_column: 'time', value_columns: ['transactions'] } },
      value_semantics: 'tps', runtime_target: 'tps', interpolation: 'step', source_duration: '3m', target_duration: '3m', refreshed_at: '2026-08-31T00:00:00Z',
      mappings: [{ series_key: 'requests', series_name: 'Requests', group_name: 'Checkout', peak_target: 10, points: [{ offset_ms: 0, target: 2 }, { offset_ms: 60_000, target: 10 }, { offset_ms: 120_000, target: 4 }] }],
      sizing: { complete: true, profile_peak_vusers: 12, static_peak_vusers: 0, estimated_peak_vusers: 12, suggested_min_vusers: 1, suggested_max_vusers: 24, recommended_generators: 2 },
    }

    const preview = buildScenarioModelPreview({ duration: '3m', scenarioMode: 'goal_oriented', groups: [{ name: 'Checkout', vusers: 9 }], goalTargetTps: 99, trafficProfile })

    expect(preview.points.find(point => point.elapsedSeconds === 0)).toMatchObject({ totalVusers: 9, totalTps: 2 })
    expect(preview.points.find(point => point.elapsedSeconds === 60)?.totalTps).toBe(10)
    expect(preview.points.find(point => point.elapsedSeconds === 120)?.totalTps).toBe(4)
    expect(preview.peakTps).toBe(10)
    expect(preview.minTps).toBe(2)
    expect(preview.averageTps).toBeCloseTo(16 / 3)
    expect(preview.showVUsers).toBe(false)
    expect(preview.estimatedPeakVusers).toBe(12)
    expect(preview.recommendedGenerators).toBe(2)
    expect(preview.series.find(series => series.key === 'totalTps')).toMatchObject({ label: 'Production target TPS', interpolation: 'stepAfter' })

    const multiGroupProfile: TrafficProfile = {
      ...trafficProfile,
      mappings: [
        ...trafficProfile.mappings,
        { series_key: 'search', series_name: 'Search', group_name: 'Search', peak_target: 5, points: [{ offset_ms: 0, target: 1 }, { offset_ms: 60_000, target: 5 }, { offset_ms: 120_000, target: 2 }] },
      ],
    }
    const multiGroupPreview = buildScenarioModelPreview({
      duration: '3m',
      groups: [{ name: 'Checkout', vusers: 9 }, { name: 'Search', vusers: 4 }],
      trafficProfile: multiGroupProfile,
    })
    expect(multiGroupPreview.points.find(point => point.elapsedSeconds === 60)).toMatchObject({ group_0_tps: 10, group_1_tps: 5, totalTps: 15 })
    expect(multiGroupPreview.minTps).toBe(3)
    expect(multiGroupPreview.averageTps).toBeCloseTo(8)
    expect(multiGroupPreview.series.filter(series => series.kind === 'tps').map(series => series.key)).toEqual(['group_0_tps', 'group_1_tps', 'totalTps'])
  })

  it('uses production VUser targets for profile-managed groups', () => {
    const trafficProfile: TrafficProfile = {
      version: 1,
      source: { kind: 'file', file: { filename: 'vusers.csv', timestamp_column: 'time', value_columns: ['vusers'] } },
      value_semantics: 'vusers', runtime_target: 'vusers', interpolation: 'step', source_duration: '2m', target_duration: '2m', refreshed_at: '2026-08-31T00:00:00Z',
      mappings: [{ series_key: 'users', series_name: 'Users', group_name: 'Checkout', peak_target: 24, points: [{ offset_ms: 0, target: 4 }, { offset_ms: 60_000, target: 24 }] }],
    }

    const preview = buildScenarioModelPreview({ duration: '2m', groups: [{ name: 'Checkout', vusers: 24 }], trafficProfile })

    expect(preview.points.find(point => point.elapsedSeconds === 0)?.totalVusers).toBe(4)
    expect(preview.points.find(point => point.elapsedSeconds === 60)?.totalVusers).toBe(24)
    expect(preview.series.find(series => series.key === 'group_0_vusers')?.interpolation).toBe('stepAfter')
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

  it('compares large stage timelines at exact nanosecond precision', () => {
    const oneNanosecondOver = validateStageTimeline('17280000000000001ns', [
      { type: 'hold', duration: '17280000000000000ns', target_vusers: 10 },
      { type: 'hold', duration: '2ns', target_vusers: 10 },
    ])
    expect(oneNanosecondOver.status).toBe('error')

    const oneNanosecondShort = validateStageTimeline('17280000000000001ns', [
      { type: 'hold', duration: '17280000000000000ns', target_vusers: 10 },
    ])
    expect(oneNanosecondShort.status).toBe('warning')
  })
})

describe('shape-adaptive stage generators', () => {
	it.each([
		{
			name: 'defaults',
			input: { durationSeconds: 3600, startVusers: 0, endVusers: 60, incrementBy: 'step_count' as const, stepCount: 6 },
			targets: [10, 20, 30, 40, 50, 60],
			duration: '10m',
			partial: false,
		},
		{
			name: 'non-zero start',
			input: { durationSeconds: 600, startVusers: 20, endVusers: 60, incrementBy: 'step_count' as const, stepCount: 4 },
			targets: [30, 40, 50, 60],
			duration: '2m30s',
			partial: false,
		},
		{
			name: 'VUsers per step with partial final increment',
			input: { durationSeconds: 600, startVusers: 0, endVusers: 55, incrementBy: 'vusers_per_step' as const, vusersPerStep: 10 },
			targets: [10, 20, 30, 40, 50, 55],
			duration: '1m40s',
			partial: true,
		},
	])('builds equal measurement intervals for $name', ({ input, targets, duration, partial }) => {
		const plan = buildSteppedRampPlan(input)
		expect(plan.error).toBeUndefined()
		expect(plan.stages.map(stage => stage.target_vusers)).toEqual(targets)
		expect(plan.stages.every(stage => stage.type === 'hold' && stage.duration === duration)).toBe(true)
		expect(plan.partialFinalStep).toBe(partial)
	})

	it('blocks a duration that cannot divide evenly and suggests nearby valid values', () => {
		const plan = buildSteppedRampPlan({ durationSeconds: 601, startVusers: 0, endVusers: 60, incrementBy: 'step_count', stepCount: 6 })
		expect(plan.stages).toEqual([])
		expect(plan.error).toContain('Try 10m or 10m6s')
	})

	it('rejects an ending load at or below the starting point', () => {
		expect(buildSteppedRampPlan({ durationSeconds: 60, startVusers: 10, endVusers: 10, incrementBy: 'step_count', stepCount: 2 }).error)
			.toContain('greater than starting')
	})

	it('rejects duplicate-producing and excessive interval counts', () => {
		expect(buildSteppedRampPlan({ durationSeconds: 360, startVusers: 0, endVusers: 2, incrementBy: 'step_count', stepCount: 6 }).error)
			.toContain('cannot exceed')
		expect(buildSteppedRampPlan({ durationSeconds: 10_000, startVusers: 0, endVusers: 1_000, incrementBy: 'step_count', stepCount: MAX_STEPPED_RAMP_STEPS + 1 }).error)
			.toContain(`at most ${MAX_STEPPED_RAMP_STEPS}`)
	})

	it('rejects non-finite duration and load inputs', () => {
		expect(buildSteppedRampPlan({ durationSeconds: Number.NaN, startVusers: 0, endVusers: 60, incrementBy: 'step_count', stepCount: 6 }).error)
			.toContain('finite')
		expect(buildSteppedRampPlan({ durationSeconds: 3600, startVusers: Number.POSITIVE_INFINITY, endVusers: 60, incrementBy: 'step_count', stepCount: 6 }).error)
			.toContain('finite')
	})

  it('plans small stepped ramps as one VUser per step', () => {
    expect(steppedStepPlan(20)).toEqual({ inc: 1, steps: 20 })
  })

  it('builds the stepped-ramp hero case as 20 three-minute windows', () => {
    const stages = buildShapeStages('stepped', { peakVusers: 20, durationSeconds: 3600 })
    const ramps = stages.filter(stage => stage.type === 'ramp_up')
    const holds = stages.filter(stage => stage.type === 'hold')

    expect(ramps).toHaveLength(20)
    expect(holds).toHaveLength(20)
    expect(stageSeconds(stages)).toBe(3600)
    expect(stagePeak(stages)).toBe(20)
    ramps.forEach((stage, index) => {
      expect(stage.target_vusers).toBe(index + 1)
      expect(parseDurationSeconds(stage.duration) + parseDurationSeconds(holds[index].duration)).toBe(180)
    })
  })

  it('caps large stepped ramps near twenty windows with larger increments', () => {
    const stages = buildShapeStages('stepped', { peakVusers: 500, durationSeconds: 3600 })
    const ramps = stages.filter(stage => stage.type === 'ramp_up')

    expect(ramps.length).toBeLessThanOrEqual(20)
    expect(ramps[0].target_vusers).toBe(25)
    expect(stagePeak(stages)).toBe(500)
    expect(stageSeconds(stages)).toBe(3600)
  })

  it('keeps generated strategy timelines exact and positive', () => {
    const strategies: ShapeStageStrategy[] = ['stepped', 'stress', 'soak', 'spike', 'chaos']
    strategies.forEach(strategy => {
      const stages = buildShapeStages(strategy, { peakVusers: 25, durationSeconds: 1080, turbulenceTps: 15 })
      expect(stages.length).toBeGreaterThan(0)
      expect(stageSeconds(stages)).toBe(1080)
      expect(stagePeak(stages)).toBe(25)
      expect(stages.every(stage => parseDurationSeconds(stage.duration) > 0)).toBe(true)
      expect(validateStageTimeline('18m', stages).status).not.toBe('error')
    })
  })

  it('preserves the chaos-resilience mirror numbers at default inputs', () => {
    const stages = buildShapeStages('chaos', { peakVusers: 25, durationSeconds: 18 * 60, turbulenceTps: 15 })
    expect(stages).toEqual([
      { name: 'Baseline', type: 'ramp_up', duration: '3m', target_vusers: 25 },
      { name: 'Turbulence', type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 },
      { name: 'Recovery', type: 'ramp_down', duration: '5m', target_vusers: 5 },
    ])
  })
})

describe('adaptScenarioShapeDefaults', () => {
  it('adapts stepped-ramp seeds from the selected peak and duration', () => {
    const seed = adaptScenarioShapeDefaults('stepped-ramp', {
      duration: '1h',
      scenario_mode: 'vuser_driven',
      groups: [{ name: 'checkout', vusers: 20, ramp_up: '1m' }],
    })

    expect(seed.groups?.[0].ramp_up).toBe('1h')
    expect(seed.groups?.[0].vusers).toBe(20)
    expect(seed.groups?.[0].stages?.filter(stage => stage.type === 'ramp_up')).toHaveLength(20)
    expect(stageSeconds(seed.groups?.[0].stages || [])).toBe(3600)
  })

  it('aligns throughput goal and pacing defaults for goal shapes', () => {
    const seed = adaptScenarioShapeDefaults('open-arrival-rate', {
      duration: '10m',
      groups: [{ vusers: 12 }],
      goal: { target_average_tps: 42 },
    })

    expect(seed.scenario_mode).toBe('goal_oriented')
    expect(seed.goal?.target_average_tps).toBe(42)
    expect(seed.pacing).toMatchObject({ type: 'tps', tps: 42 })
    expect(seed.groups?.[0].stages).toEqual([])
  })

  it('caps closed and mixed-protocol non-stage defaults', () => {
    const closed = adaptScenarioShapeDefaults('closed-vuser', {
      duration: '10m',
      groups: [{ vusers: 10, ramp_up: '5m' }],
    })
    expect(parseDurationSeconds(closed.groups?.[0].ramp_up)).toBeLessThanOrEqual(120)
    expect(closed.groups?.[0].stages).toEqual([])

    const mixed = adaptScenarioShapeDefaults('mixed-protocol', {
      duration: '15m',
      groups: [{ name: 'api-bulk', vusers: 200 }, { name: 'browser-canary', vusers: 30 }],
    })
    expect(mixed.groups?.[1].vusers).toBe(5)
  })

  it('sets stress goal max to the generated peak', () => {
    const seed = adaptScenarioShapeDefaults('stress', {
      duration: '20m',
      scenario_mode: 'vuser_driven',
      groups: [{ vusers: 120 }],
    })
    expect(seed.scenario_mode).toBe('goal_oriented')
    expect(seed.goal?.max_vusers).toBe(120)
    expect(stagePeak(seed.groups?.[0].stages || [])).toBe(120)
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
    ;['stepped-ramp', 'spike-rendezvous', 'soak', 'stress', 'chaos-resilience'].forEach((id) => {
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
