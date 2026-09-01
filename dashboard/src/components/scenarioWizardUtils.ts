import {
  GoalConfig,
  LoadModelSupportEntry,
  LoadModelTemplate,
  LoadStageDraft,
  RuntimeSettingsOverride,
  ScenarioMode,
  StageTimelineValidationResult,
  TrafficProfile,
} from '../types'
import { type PacingConfig, type ThinkTimeConfig } from '../types/scripts'
import { formatTimeCompact, parseDurationToNanoseconds, parseDurationToSeconds } from '../utils/timeUtils'
import { trafficProfileMappingForGroup, trafficProfileTargetAt, trafficProfileTimelineSeconds, trafficProfileTpsStatistics } from './trafficProfilePreviewModel'

export type ScenarioWizardStep = 'start' | 'source' | 'scripts' | 'run' | 'data' | 'review'

export type ScenarioSourceMode = 'shape' | 'existing' | 'traffic_profile'

export interface ScenarioShapeCatalogOption {
  id: string
  support: LoadModelSupportEntry
  templates: LoadModelTemplate[]
}

export interface ScenarioModelGroupInput {
  name?: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  stages?: LoadStageDraft[]
}

export interface ScenarioModelPoint {
  elapsedSeconds: number
  label: string
  totalVusers: number
  totalTps?: number
  [key: string]: number | string | undefined
}

export interface ScenarioModelSeries {
  key: string
  label: string
  color: string
  kind: 'group' | 'total' | 'tps'
  interpolation?: 'linear' | 'stepAfter'
}

export interface ScenarioModelPreview {
  points: ScenarioModelPoint[]
  series: ScenarioModelSeries[]
  showVUsers: boolean
  peakVusers: number
  peakTps?: number
  minTps?: number
  averageTps?: number
  estimatedPeakVusers?: number
  recommendedGenerators?: number
  durationSeconds: number
}

const GROUP_COLORS = ['#2563eb', '#16a34a', '#f59e0b', '#dc2626', '#7c3aed', '#0891b2']

export function buildScenarioShapeCatalog(
  supportMatrix: LoadModelSupportEntry[],
  templates: LoadModelTemplate[],
): ScenarioShapeCatalogOption[] {
  return supportMatrix.map((support) => ({
    id: support.id,
    support,
    templates: templates.filter(template => template.shape_id === support.id),
  }))
}

export type ScenarioShapePacingType =
  | 'none'
  | 'fixed_delay'
  | 'random_delay'
  | 'fixed_interval'
  | 'random_interval'
  | 'tps'

export interface ScenarioShapePresetGoal {
  target_average_tps?: number
  min_vusers?: number
  max_vusers?: number
  p95_latency_ms?: number
  max_error_pct?: number
}

export interface ScenarioShapePresetPacing {
  type: ScenarioShapePacingType
  delay_ms?: number
  min_delay_ms?: number
  max_delay_ms?: number
  interval_ms?: number
  min_interval_ms?: number
  max_interval_ms?: number
  tps?: number
}

export interface ScenarioSeedGroup {
  name?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  stages?: LoadStageDraft[]
  iterations?: number
}

export interface ScenarioSeed {
  duration?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  groups?: ScenarioSeedGroup[]
  goal?: GoalConfig
  pacing?: Omit<Partial<PacingConfig>, 'type'> & { type?: ScenarioShapePacingType | '' }
  think_time?: Partial<ThinkTimeConfig>
  runtime_settings?: RuntimeSettingsOverride
  iterations?: number
  log_level?: string
  auto_transactions?: string
  continue_on_error?: boolean
  snapshot_on_error?: boolean
}

export interface ScenarioShapeAdaptContext {
  duration?: string
  groups?: ScenarioSeedGroup[]
  goalTps?: number
}

export type ShapeStageStrategy = 'stepped' | 'stress' | 'soak' | 'spike' | 'chaos' | 'none'

export interface ShapeStageBuildOptions {
  peakVusers: number
  durationSeconds: number
  baselineVusers?: number
  turbulenceTps?: number
}

export interface SteppedRampPlanInput {
  durationSeconds: number
  startVusers: number
  endVusers: number
  incrementBy: 'step_count' | 'vusers_per_step'
  stepCount?: number
  vusersPerStep?: number
}

export interface SteppedRampPlan {
  stages: LoadStageDraft[]
  increment: number
  intervalSeconds: number
  partialFinalStep: boolean
  error?: string
}

export const MAX_STEPPED_RAMP_STEPS = 100

export function buildSteppedRampPlan(input: SteppedRampPlanInput): SteppedRampPlan {
  const durationSeconds = Math.max(0, Math.round(input.durationSeconds))
  const start = Math.max(0, Math.round(input.startVusers))
  const end = Math.max(0, Math.round(input.endVusers))
  if (![durationSeconds, start, end].every(Number.isFinite)) {
    return { stages: [], increment: 0, intervalSeconds: 0, partialFinalStep: false, error: 'Duration and load levels must be finite numbers.' }
  }
  if (end <= start) return { stages: [], increment: 0, intervalSeconds: 0, partialFinalStep: false, error: 'Ending load level must be greater than starting load level.' }
  const range = end - start
  const requested = input.incrementBy === 'step_count'
    ? Math.max(1, Math.round(input.stepCount || 0))
    : Math.max(1, Math.round(input.vusersPerStep || 0))
  const intervalCount = input.incrementBy === 'step_count' ? requested : Math.ceil(range / requested)
  const increment = input.incrementBy === 'step_count' ? Math.ceil(range / intervalCount) : requested
  if (!Number.isFinite(requested) || !Number.isFinite(intervalCount)) {
    return { stages: [], increment: 0, intervalSeconds: 0, partialFinalStep: false, error: 'Step values must be finite numbers.' }
  }
  if (input.incrementBy === 'step_count' && intervalCount > range) {
    return { stages: [], increment, intervalSeconds: 0, partialFinalStep: false, error: `Number of steps cannot exceed the ${range}-VUser load range.` }
  }
  if (intervalCount > MAX_STEPPED_RAMP_STEPS) {
    return { stages: [], increment, intervalSeconds: 0, partialFinalStep: false, error: `Stepped Ramp supports at most ${MAX_STEPPED_RAMP_STEPS} measurement intervals.` }
  }
  if (durationSeconds <= 0 || durationSeconds % intervalCount !== 0) {
    const lower = Math.max(intervalCount, Math.floor(durationSeconds / intervalCount) * intervalCount)
    const upper = Math.max(intervalCount, Math.ceil(durationSeconds / intervalCount) * intervalCount)
    return {
      stages: [], increment, intervalSeconds: 0, partialFinalStep: false,
      error: `Duration must divide evenly across ${intervalCount} steps. Try ${formatTimeCompact(lower)} or ${formatTimeCompact(upper)}.`,
    }
  }
  const intervalSeconds = durationSeconds / intervalCount
  const targets = Array.from({ length: intervalCount }, (_, index) => Math.min(end, start + increment * (index + 1)))
  const finalIncrement = targets[targets.length - 1] - (targets[targets.length - 2] ?? start)
  const partialFinalStep = finalIncrement !== increment
  return {
    stages: targets.map((target, index) => ({
      name: `Level ${index + 1}${partialFinalStep && index === targets.length - 1 ? ' (partial)' : ''}`,
      type: 'hold',
      duration: formatTimeCompact(intervalSeconds),
      target_vusers: target,
    })),
    increment,
    intervalSeconds,
    partialFinalStep,
  }
}

/**
 * A ScenarioShapePreset is the deterministic starting point applied when the operator
 * selects a workload shape via "Use Manual Shape". It seeds the scenario form so the
 * later wizard steps reflect the chosen shape (mode, groups, stages, goal envelope).
 *
 * Numbers mirror pkg/api/load_model_support.go so the manual-shape path and the matching
 * starter-template button produce the same draft. Invariants (enforced in tests):
 *   - staged groups: Σ(stage durations) <= scenario `duration`, and group.vusers == staged peak.
 *   - closed-vuser keeps exactly one group with no stages.
 */
export interface ScenarioShapePreset {
  duration: string
  scenarioMode?: ScenarioMode
  scriptShakeout?: boolean
  resourceBaseline?: boolean
  groups: ScenarioModelGroupInput[]
  goal?: ScenarioShapePresetGoal
  pacing?: ScenarioShapePresetPacing
  continueOnError?: boolean
  snapshotOnError?: boolean
  guidance: string
  recommendedRunTab?: 'scenario' | 'runtime' | 'infrastructure'
  suggestMultipleGroups?: boolean
}

export function shapeStageStrategyFor(shapeId?: string): ShapeStageStrategy {
  switch (shapeId) {
    case 'stepped-ramp':
      return 'stepped'
    case 'stress':
      return 'stress'
    case 'soak':
      return 'soak'
    case 'spike-rendezvous':
      return 'spike'
    case 'chaos-resilience':
      return 'chaos'
    default:
      return 'none'
  }
}

export function steppedStepPlan(peak: number, maxSteps = 20): { inc: number, steps: number } {
  const p = Math.max(1, Math.round(peak))
  if (p <= maxSteps) return { inc: 1, steps: p }
  const inc = Math.ceil(p / maxSteps)
  return { inc, steps: Math.ceil(p / inc) }
}

function clampPositiveSeconds(value: number, fallback = 1): number {
  return Math.max(1, Math.round(Number.isFinite(value) && value > 0 ? value : fallback))
}

function normalizePeak(value: number): number {
  return Math.max(1, Math.round(Number(value) || 0))
}

function durationText(seconds: number): string {
  return formatTimeCompact(clampPositiveSeconds(seconds))
}

function distributeSeconds(totalSeconds: number, bucketCount: number): number[] {
  const count = Math.max(1, Math.floor(bucketCount))
  const total = Math.max(count, Math.round(totalSeconds))
  const base = Math.floor(total / count)
  let remainder = total - base * count
  return Array.from({ length: count }, () => {
    const value = base + (remainder > 0 ? 1 : 0)
    remainder -= 1
    return Math.max(1, value)
  })
}

function splitPhaseDuration(totalSeconds: number, rampFraction: number, maxRampSeconds?: number): { ramp: number, hold: number } {
  const total = Math.max(2, Math.round(totalSeconds))
  const candidate = Math.max(1, Math.round(total * rampFraction))
  const capped = maxRampSeconds ? Math.min(candidate, maxRampSeconds) : candidate
  const ramp = Math.max(1, Math.min(capped, total - 1))
  return { ramp, hold: Math.max(1, total - ramp) }
}

function buildSteppedStages(options: ShapeStageBuildOptions): LoadStageDraft[] {
  const peak = normalizePeak(options.peakVusers)
  const durationSeconds = clampPositiveSeconds(options.durationSeconds, 60)
  const planned = steppedStepPlan(peak)
  const maxPositiveStepWindows = Math.max(1, Math.floor(durationSeconds / 2))
  const steps = Math.min(planned.steps, maxPositiveStepWindows)
  const inc = Math.ceil(peak / steps)
  const windows = distributeSeconds(durationSeconds, steps)

  return windows.flatMap((windowSeconds, index) => {
    const target = Math.min(peak, inc * (index + 1))
    const { ramp, hold } = splitPhaseDuration(windowSeconds, 0.25, 15)
    return [
      { name: `Step ${index + 1}`, type: 'ramp_up', duration: durationText(ramp), target_vusers: target },
      { name: `Hold ${index + 1}`, type: 'hold', duration: durationText(hold), target_vusers: target },
    ]
  })
}

function buildStressStages(options: ShapeStageBuildOptions): LoadStageDraft[] {
  const peak = normalizePeak(options.peakVusers)
  const durationSeconds = clampPositiveSeconds(options.durationSeconds, 60)
  const targets = [0.33, 0.66, 1].map(fraction => Math.max(1, Math.round(peak * fraction)))
  targets[targets.length - 1] = peak
  const windows = distributeSeconds(durationSeconds, targets.length)

  return windows.flatMap((windowSeconds, index) => {
    const target = targets[index]
    const { ramp, hold } = splitPhaseDuration(windowSeconds, index === targets.length - 1 ? 0.5 : 0.45)
    return [
      { name: `Ramp ${index + 1}`, type: 'ramp_up', duration: durationText(ramp), target_vusers: target },
      { name: `Hold ${index + 1}`, type: 'hold', duration: durationText(hold), target_vusers: target },
    ]
  })
}

function buildSoakStages(options: ShapeStageBuildOptions): LoadStageDraft[] {
  const peak = normalizePeak(options.peakVusers)
  const durationSeconds = Math.max(3, clampPositiveSeconds(options.durationSeconds, 60))
  const ramp = Math.max(1, Math.round(durationSeconds * 0.06))
  const drain = Math.max(1, Math.round(durationSeconds * 0.05))
  const hold = Math.max(1, durationSeconds - ramp - drain)
  const adjustedHold = ramp + drain + hold === durationSeconds ? hold : Math.max(1, durationSeconds - ramp - drain)
  return [
    { name: 'Warmup', type: 'ramp_up', duration: durationText(ramp), target_vusers: peak },
    { name: 'Endurance', type: 'hold', duration: durationText(adjustedHold), target_vusers: peak },
    { name: 'Cooldown', type: 'ramp_down', duration: durationText(Math.max(1, durationSeconds - ramp - adjustedHold)), target_vusers: 0 },
  ]
}

function buildSpikeStages(options: ShapeStageBuildOptions): LoadStageDraft[] {
  const peak = normalizePeak(options.peakVusers)
  const durationSeconds = Math.max(3, clampPositiveSeconds(options.durationSeconds, 60))
  const ramp = Math.max(1, Math.round(durationSeconds * 0.15))
  const drain = Math.max(1, Math.round(durationSeconds * 0.1))
  const hold = Math.max(1, durationSeconds - ramp - drain)
  return [
    { name: 'Spike Ramp', type: 'ramp_up', duration: durationText(ramp), target_vusers: peak },
    { name: 'Rendezvous Window', type: 'hold', duration: durationText(hold), target_vusers: peak },
    { name: 'Spike Drain', type: 'ramp_down', duration: durationText(Math.max(1, durationSeconds - ramp - hold)), target_vusers: 0 },
  ]
}

function buildChaosStages(options: ShapeStageBuildOptions): LoadStageDraft[] {
  const peak = normalizePeak(options.peakVusers)
  const durationSeconds = Math.max(3, clampPositiveSeconds(options.durationSeconds, 60))
  const baseline = Math.max(1, Math.round(durationSeconds / 6))
  const recovery = Math.max(1, Math.round(durationSeconds * 5 / 18))
  const turbulence = Math.max(1, durationSeconds - baseline - recovery)
  const targetTps = Number(options.turbulenceTps) > 0 ? Number(options.turbulenceTps) : undefined
  const recoveryTarget = Math.max(1, Math.round(peak * 0.2))
  return [
    { name: 'Baseline', type: 'ramp_up', duration: durationText(baseline), target_vusers: peak },
    { name: 'Turbulence', type: 'hold', duration: durationText(turbulence), target_vusers: peak, ...(targetTps ? { target_tps: targetTps } : {}) },
    { name: 'Recovery', type: 'ramp_down', duration: durationText(Math.max(1, durationSeconds - baseline - turbulence)), target_vusers: recoveryTarget },
  ]
}

export function buildShapeStages(strategy: ShapeStageStrategy, options: ShapeStageBuildOptions): LoadStageDraft[] {
  switch (strategy) {
    case 'stepped':
      return buildSteppedStages(options)
    case 'stress':
      return buildStressStages(options)
    case 'soak':
      return buildSoakStages(options)
    case 'spike':
      return buildSpikeStages(options)
    case 'chaos':
      return buildChaosStages(options)
    default:
      return []
  }
}

function maxStageVUsers(stages?: LoadStageDraft[]): number {
  return (stages || []).reduce((max, stage) => Math.max(max, Number(stage.target_vusers) || 0), 0)
}

function stageDurationSum(stages: LoadStageDraft[], type: string): number {
  return stages
    .filter(stage => stage.type === type)
    .reduce((sum, stage) => sum + parseDurationToSeconds(stage.duration), 0)
}

function simpleRampForDuration(durationSeconds: number, capFraction = 0.2, desiredSeconds = 30): string {
  const cap = Math.max(1, Math.round(durationSeconds * capFraction))
  return durationText(Math.min(Math.max(1, desiredSeconds), cap))
}

function scenarioDurationSeconds(seed: ScenarioSeed, ctx?: ScenarioShapeAdaptContext): number {
  return parseDurationToSeconds(seed.duration || ctx?.duration) || 60
}

function seedGroups(seed: ScenarioSeed, ctx?: ScenarioShapeAdaptContext): ScenarioSeedGroup[] {
  const groups = seed.groups?.length ? seed.groups : ctx?.groups
  return groups?.length ? groups : [{ vusers: 10 }]
}

function targetTpsFor(seed: ScenarioSeed, ctx?: ScenarioShapeAdaptContext, fallback = 25): number {
  return Number(ctx?.goalTps || seed.goal?.target_average_tps || seed.pacing?.tps || fallback)
}

function vuserGuardrails(peak: number, multiplier: number) {
  const p = normalizePeak(peak)
  return {
    min_vusers: Math.max(1, Math.floor(p / 2)),
    max_vusers: Math.max(p, Math.round(p * multiplier)),
  }
}

export function adaptScenarioShapeDefaults(shapeId: string, seed: ScenarioSeed, ctx?: ScenarioShapeAdaptContext): ScenarioSeed {
  const strategy = shapeStageStrategyFor(shapeId)
  const durationSeconds = scenarioDurationSeconds(seed, ctx)
  const duration = seed.duration || ctx?.duration || durationText(durationSeconds)
  const baseGroups = seedGroups(seed, ctx)
  const next: ScenarioSeed = {
    ...seed,
    duration,
    groups: baseGroups.map(group => ({ ...group })),
    goal: seed.goal ? { ...seed.goal } : undefined,
    pacing: seed.pacing ? { ...seed.pacing } : undefined,
    think_time: seed.think_time ? { ...seed.think_time } : undefined,
    runtime_settings: seed.runtime_settings ? structuredClone(seed.runtime_settings) : undefined,
  }

  if (shapeId === 'closed-vuser') {
    next.scenario_mode = 'vuser_driven'
    next.groups = next.groups?.slice(0, 1).map(group => ({
      ...group,
      vusers: normalizePeak(group.vusers || 5),
      ramp_up: simpleRampForDuration(durationSeconds, 0.2, parseDurationToSeconds(group.ramp_up) || 30),
      ramp_down: group.ramp_down || '',
      stages: [],
    }))
    return next
  }

  if (shapeId === 'open-arrival-rate' || shapeId === 'goal-based') {
    const defaultTps = shapeId === 'goal-based' ? 30 : 25
    const targetTps = targetTpsFor(seed, ctx, defaultTps)
    const peak = normalizePeak(next.groups?.[0]?.vusers || 10)
    const guardrails = vuserGuardrails(peak, shapeId === 'goal-based' ? 10 : 5)
    next.scenario_mode = 'goal_oriented'
    next.goal = {
      ...(next.goal || {}),
      target_average_tps: targetTps,
      min_vusers: next.goal?.min_vusers || guardrails.min_vusers,
      max_vusers: next.goal?.max_vusers || guardrails.max_vusers,
    }
    next.pacing = { ...(next.pacing || {}), type: 'tps', tps: targetTps }
    next.groups = next.groups?.map(group => ({
      ...group,
      vusers: normalizePeak(group.vusers || peak),
      ramp_up: group.ramp_up || simpleRampForDuration(durationSeconds, 0.2, Math.max(10, Math.round(durationSeconds * 0.2))),
      stages: [],
    }))
    return next
  }

  if (shapeId === 'mixed-protocol') {
    next.scenario_mode = 'vuser_driven'
    const groups = next.groups?.length ? next.groups : [{ name: 'api-bulk', vusers: 500 }, { name: 'browser-canary', vusers: 5 }]
    next.groups = groups.map((group, index) => {
      const isObserver = (group.name || '').toLowerCase().includes('canary') || index > 0
      const vusers = isObserver ? Math.min(5, normalizePeak(group.vusers || 5)) : normalizePeak(group.vusers || 100)
      return {
        ...group,
        vusers,
        ramp_up: group.ramp_up || simpleRampForDuration(durationSeconds, isObserver ? 0.15 : 0.33, isObserver ? 120 : 300),
        ramp_down: group.ramp_down || simpleRampForDuration(durationSeconds, isObserver ? 0.08 : 0.15, isObserver ? 60 : 120),
        stages: [],
      }
    })
    return next
  }

  if (strategy === 'none') {
    return next
  }

  const targetTps = targetTpsFor(seed, ctx, shapeId === 'chaos-resilience' ? 15 : 25)
  next.groups = next.groups?.map((group) => {
    const peak = normalizePeak(group.vusers || maxStageVUsers(group.stages) || 10)
    const stages = buildShapeStages(strategy, {
      peakVusers: peak,
      durationSeconds,
      baselineVusers: Math.max(1, Math.ceil(peak / 3)),
      turbulenceTps: shapeId === 'chaos-resilience' ? targetTps : undefined,
    })
    const rampUpSeconds = shapeId === 'stepped-ramp'
      ? durationSeconds
      : stageDurationSum(stages, 'ramp_up')
    const rampDownSeconds = stageDurationSum(stages, 'ramp_down')
    return {
      ...group,
      vusers: maxStageVUsers(stages) || peak,
      ramp_up: shapeId === 'chaos-resilience' && group.ramp_up ? group.ramp_up : (rampUpSeconds > 0 ? durationText(rampUpSeconds) : ''),
      ramp_down: shapeId === 'chaos-resilience' && group.ramp_down ? group.ramp_down : (rampDownSeconds > 0 ? durationText(rampDownSeconds) : ''),
      stages,
    }
  })

  if (shapeId === 'stress') {
    const peak = normalizePeak(next.groups?.[0]?.vusers || 300)
    next.scenario_mode = 'goal_oriented'
    next.goal = { ...(next.goal || {}), max_vusers: peak }
  } else if (shapeId === 'chaos-resilience') {
    const peak = normalizePeak(next.groups?.[0]?.vusers || 25)
    next.scenario_mode = 'goal_oriented'
    next.script_shakeout = true
    next.resource_baseline = true
    next.continue_on_error = true
    next.snapshot_on_error = true
    next.goal = {
      ...(next.goal || {}),
      target_average_tps: targetTps,
      min_vusers: next.goal?.min_vusers || Math.max(1, Math.floor(peak / 5)),
      max_vusers: next.goal?.max_vusers || peak * 3,
    }
  } else if (shapeId === 'soak') {
    next.scenario_mode = 'vuser_driven'
    next.pacing = { ...(next.pacing || {}), type: 'fixed_delay', delay_ms: next.pacing?.delay_ms || 1000 }
  } else {
    next.scenario_mode = 'vuser_driven'
  }

  return next
}

export const SCENARIO_SHAPE_PRESETS: Record<string, ScenarioShapePreset> = {
  'closed-vuser': {
    duration: '3m',
    scenarioMode: 'vuser_driven',
    groups: [{ vusers: 5, ramp_up: '30s' }],
    guidance: 'Fixed VUsers loop for the full duration. Set per-group VUsers and ramp on the Run Settings step.',
  },
  'open-arrival-rate': {
    duration: '10m',
    scenarioMode: 'goal_oriented',
    groups: [{ vusers: 10, ramp_up: '2m' }],
    goal: { target_average_tps: 25, min_vusers: 5, max_vusers: 50, p95_latency_ms: 800, max_error_pct: 1 },
    pacing: { type: 'tps', tps: 25 },
    guidance: 'Throughput-driven: the goal controller holds a target TPS while pacing keeps arrival rate steady. Tune Target TPS and the goal envelope on Run Settings.',
    recommendedRunTab: 'scenario',
  },
  'stepped-ramp': {
		duration: '60m',
    scenarioMode: 'vuser_driven',
    groups: [{
			vusers: 60,
			ramp_up: '',
			stages: buildSteppedRampPlan({ durationSeconds: 60 * 60, startVusers: 0, endVusers: 60, incrementBy: 'step_count', stepCount: 6 }).stages,
    }],
		guidance: 'Measure performance at equal-duration load levels. Configure the range, increment, scope, and protection settings on Run Settings.',
  },
  'spike-rendezvous': {
    duration: '8m',
    scenarioMode: 'vuser_driven',
    groups: [{
      vusers: 100,
      ramp_up: '1m12s',
      ramp_down: '48s',
      stages: buildShapeStages('spike', { peakVusers: 100, durationSeconds: 8 * 60 }),
    }],
    guidance: 'Add an actions[].type=rendezvous step in the script before the spike transaction so VUsers synchronize before the surge.',
  },
  soak: {
    duration: '2h',
    scenarioMode: 'vuser_driven',
    groups: [{
      vusers: 20,
      ramp_up: '7m12s',
      ramp_down: '6m',
      stages: buildShapeStages('soak', { peakVusers: 20, durationSeconds: 2 * 60 * 60 }),
    }],
    pacing: { type: 'fixed_delay', delay_ms: 1000 },
    guidance: 'Long, steady-state endurance run. Keep VUsers modest and pacing consistent to surface leaks and drift over time.',
  },
  stress: {
    duration: '20m',
    scenarioMode: 'goal_oriented',
    groups: [{
      vusers: 300,
      ramp_up: '9m',
      stages: buildShapeStages('stress', { peakVusers: 300, durationSeconds: 20 * 60 }),
    }],
    goal: { max_vusers: 300, p95_latency_ms: 1000, max_error_pct: 2 },
    guidance: 'Escalating load to find the breaking point. Staged groups push VUsers higher while the goal guardrails (max VUsers, SLA) and capacity advisory keep the run bounded.',
  },
  'goal-based': {
    duration: '10m',
    scenarioMode: 'goal_oriented',
    groups: [{ vusers: 10, ramp_up: '2m' }],
    goal: { target_average_tps: 30, min_vusers: 5, max_vusers: 100, p95_latency_ms: 800, max_error_pct: 1 },
    guidance: 'The goal controller scales VUsers within the min/max guardrails to hold the target TPS and SLA. Set the goal envelope on Run Settings.',
  },
  'chaos-resilience': {
    duration: '18m',
    scenarioMode: 'goal_oriented',
    scriptShakeout: true,
    resourceBaseline: true,
    groups: [{ name: 'chaos-steady-state', vusers: 25, ramp_up: '3m', ramp_down: '2m', stages: buildShapeStages('chaos', { peakVusers: 25, durationSeconds: 18 * 60, turbulenceTps: 15 }) }],
    goal: { target_average_tps: 15, min_vusers: 5, max_vusers: 75, p95_latency_ms: 1200, max_error_pct: 5 },
    continueOnError: true,
    snapshotOnError: true,
    guidance: 'Guarded resilience run: the shakeout gate validates scripts first, then baseline/turbulence/recovery stages run with snapshot-on-error evidence. Pair with controlled fault injection.',
  },
  'mixed-protocol': {
    duration: '15m',
    scenarioMode: 'vuser_driven',
    groups: [
      { name: 'api-bulk', vusers: 500, ramp_up: '5m', ramp_down: '2m' },
      { name: 'browser-canary', vusers: 5, ramp_up: '2m', ramp_down: '1m' },
    ],
    suggestMultipleGroups: true,
    guidance: 'Assign a different protocol script to each group; the capacity advisory accounts for per-protocol density. Pick scripts for every group on the Scripts step.',
  },
}

export function getScenarioShapePreset(shapeId: string): ScenarioShapePreset | undefined {
  return SCENARIO_SHAPE_PRESETS[shapeId]
}

export function parseDurationSeconds(value?: string, fallbackSeconds = 60): number {
  const text = String(value || '').trim().toLowerCase()
  if (!text) return fallbackSeconds

  const unitPattern = /(\d+(?:\.\d+)?)\s*(ms|s|m|h)/g
  let match: RegExpExecArray | null
  let seconds = 0
  let matched = false

  while ((match = unitPattern.exec(text)) !== null) {
    matched = true
    const amount = Number(match[1])
    const unit = match[2]
    if (unit === 'ms') seconds += amount / 1000
    else if (unit === 's') seconds += amount
    else if (unit === 'm') seconds += amount * 60
    else if (unit === 'h') seconds += amount * 3600
  }

  if (matched) return Math.max(1, Math.round(seconds))
  const numeric = Number(text)
  return Number.isFinite(numeric) && numeric > 0 ? Math.round(numeric) : fallbackSeconds
}

export function formatModelTimeLabel(seconds: number): string {
  const whole = Math.max(0, Math.round(seconds))
  const hours = Math.floor(whole / 3600)
  const minutes = Math.floor((whole % 3600) / 60)
  const secs = whole % 60
  if (hours > 0) return `${hours}h${minutes}m`
  if (minutes > 0) return `${minutes}m${secs ? `${secs}s` : ''}`
  return `${secs}s`
}

export function validateStageTimeline(duration: string, stages: LoadStageDraft[]): StageTimelineValidationResult {
  const scenarioDurationSeconds = parseDurationToSeconds(duration)
  const scenarioDurationNanoseconds = parseDurationToNanoseconds(duration)
  if (stages.length === 0) {
    return { status: 'ok', totalSeconds: 0, scenarioDurationSeconds }
  }
  if (scenarioDurationNanoseconds === null) {
    return {
      status: 'error',
      totalSeconds: 0,
      scenarioDurationSeconds,
      message: 'Set a positive scenario duration before using stair-step stages.',
    }
  }

  let hasInvalidStage = false
  let totalNanoseconds = 0n
  const totalSeconds = stages.reduce((sum, stage) => {
    const seconds = parseDurationToSeconds(stage.duration)
    const nanoseconds = parseDurationToNanoseconds(stage.duration)
    if (nanoseconds === null) {
      hasInvalidStage = true
    } else {
      totalNanoseconds += nanoseconds
    }
    return sum + seconds
  }, 0)

  if (hasInvalidStage) {
    return {
      status: 'error',
      totalSeconds,
      scenarioDurationSeconds,
      message: 'Every stair-step stage needs a positive duration.',
    }
  }

  if (totalNanoseconds > scenarioDurationNanoseconds) {
    return {
      status: 'error',
      totalSeconds,
      scenarioDurationSeconds,
      message: `Stage schedule is ${formatModelTimeLabel(totalSeconds)}, longer than the ${formatModelTimeLabel(scenarioDurationSeconds)} scenario duration.`,
    }
  }

  if (totalNanoseconds < scenarioDurationNanoseconds) {
    return {
      status: 'warning',
      totalSeconds,
      scenarioDurationSeconds,
      message: `Stages cover ${formatModelTimeLabel(totalSeconds)}; the last target holds for the remaining ${formatModelTimeLabel(scenarioDurationSeconds - totalSeconds)}.`,
    }
  }

  return { status: 'ok', totalSeconds, scenarioDurationSeconds }
}

function safeSeriesKey(index: number) {
  return `group_${index}_vusers`
}

function tpsSeriesKey(index: number) {
  return `group_${index}_tps`
}

function simpleGroupValue(group: ScenarioModelGroupInput, elapsedSeconds: number, durationSeconds: number): number {
  const target = Math.max(0, Number(group.vusers) || 0)
  const rampUpSeconds = Math.min(parseDurationSeconds(group.ramp_up, 0), durationSeconds)
  const rampDownSeconds = parseDurationSeconds(group.ramp_down, 0)

  if (rampUpSeconds > 0 && elapsedSeconds < rampUpSeconds) {
    return target * (elapsedSeconds / rampUpSeconds)
  }
  if (elapsedSeconds < durationSeconds) return target
  if (rampDownSeconds > 0 && elapsedSeconds < durationSeconds + rampDownSeconds) {
    return Math.max(0, target * (1 - ((elapsedSeconds - durationSeconds) / rampDownSeconds)))
  }
  return 0
}

function stagedValue(stages: LoadStageDraft[], elapsedSeconds: number, valueKey: 'target_vusers' | 'target_tps'): number {
  let cursor = 0
  let previousValue = 0
  for (const stage of stages) {
    const stageDuration = parseDurationSeconds(stage.duration, 60)
    const rawTarget = stage[valueKey]
    const target = rawTarget === undefined ? previousValue : Math.max(0, Number(rawTarget) || 0)
    const stageEnd = cursor + stageDuration
    // A stage owns [start, end). At the exact boundary the runtime has already
    // applied the next stage target, so the preview must advance as well.
    if (elapsedSeconds < stageEnd) {
      if (stage.type === 'ramp_up' || stage.type === 'ramp_down') {
        const progress = stageDuration > 0 ? (elapsedSeconds - cursor) / stageDuration : 1
        return previousValue + ((target - previousValue) * Math.max(0, Math.min(1, progress)))
      }
      return target
    }
    cursor = stageEnd
    previousValue = target
  }
  return previousValue
}

function stageTimelineSeconds(stages: LoadStageDraft[]): number[] {
  const timeline = [0]
  let cursor = 0
  stages.forEach((stage) => {
    cursor += parseDurationSeconds(stage.duration, 60)
    timeline.push(cursor)
  })
  return timeline
}

function collectTimeline(steadySeconds: number, totalSeconds: number, groups: ScenarioModelGroupInput[], trafficProfile?: TrafficProfile): number[] {
  const step = Math.max(1, Math.ceil(totalSeconds / 24))
  const values = new Set<number>([0, steadySeconds, totalSeconds])
  for (let second = step; second < totalSeconds; second += step) {
    values.add(second)
  }
  groups.forEach((group) => {
    const rampUpSeconds = parseDurationSeconds(group.ramp_up, 0)
    if (rampUpSeconds > 0) {
      values.add(Math.min(steadySeconds, rampUpSeconds))
    }
    const rampDownSeconds = parseDurationSeconds(group.ramp_down, 0)
    if (rampDownSeconds > 0) {
      values.add(steadySeconds + rampDownSeconds)
    }
    if (group.stages?.length) {
      stageTimelineSeconds(group.stages).forEach(second => values.add(Math.min(steadySeconds, second)))
    }
  })
  trafficProfileTimelineSeconds(trafficProfile).forEach(second => values.add(Math.min(steadySeconds, second)))
  return Array.from(values).filter(second => second >= 0 && second <= totalSeconds).sort((left, right) => left - right)
}

export function buildScenarioModelPreview(params: {
  duration: string
  groups: ScenarioModelGroupInput[]
  scenarioMode?: ScenarioMode
  goalTargetTps?: number
  trafficProfile?: TrafficProfile
}): ScenarioModelPreview {
  const steadySeconds = parseDurationSeconds(params.duration, 60)
  const showVUsers = params.scenarioMode !== 'goal_oriented'
  const groups = params.groups.length > 0 ? params.groups : [{ vusers: 0 }]
  const profileMappings = groups.map(group => trafficProfileMappingForGroup(params.trafficProfile, group.name))
  const profileVUsers = params.trafficProfile?.runtime_target === 'vusers'
  const profileTps = params.trafficProfile?.runtime_target === 'tps'
  const groupUsesSteppedStages = (group: ScenarioModelGroupInput) => (
    Boolean(group.stages?.length) && group.stages!.every(stage => stage.type === 'hold')
  )
  const allVuserSeriesAreStepped = groups.every((group, index) => (
    Boolean(profileVUsers && profileMappings[index]) || groupUsesSteppedStages(group)
  ))
  const maxRampDownSeconds = groups.reduce(
    (max, group, index) => (profileMappings[index] || group.stages?.length) ? max : Math.max(max, parseDurationSeconds(group.ramp_down, 0)),
    0,
  )
  const durationSeconds = steadySeconds + maxRampDownSeconds
  const timeline = collectTimeline(steadySeconds, durationSeconds, groups, params.trafficProfile)
  const groupSeries: ScenarioModelSeries[] = groups.map((group, index) => ({
    key: safeSeriesKey(index),
    label: group.name || `Group ${index + 1}`,
    color: GROUP_COLORS[index % GROUP_COLORS.length],
    kind: 'group',
    interpolation: (profileVUsers && profileMappings[index]) || groupUsesSteppedStages(group) ? 'stepAfter' : 'linear',
  }))
  const tpsGroupIndexes = groups.flatMap((group, index) => (
    (profileTps && profileMappings[index]) || group.stages?.some(stage => Number(stage.target_tps) > 0) ? [index] : []
  ))
  const hasGroupTps = tpsGroupIndexes.length > 0
  const hasGoalTps = !profileTps && Number(params.goalTargetTps) > 0
  const includeTps = hasGroupTps || hasGoalTps
  const profileTpsStatistics = profileTps
    ? trafficProfileTpsStatistics(profileMappings, steadySeconds)
    : undefined

  let peakVusers = 0
  let peakTps = 0
  const points = timeline.map((elapsedSeconds) => {
    const point: ScenarioModelPoint = {
      elapsedSeconds,
      label: formatModelTimeLabel(elapsedSeconds),
      totalVusers: 0,
    }

    groups.forEach((group, index) => {
      const groupKey = safeSeriesKey(index)
      const tpsKey = tpsSeriesKey(index)
      const profileMapping = profileMappings[index]
      const value = profileVUsers && profileMapping
        ? (elapsedSeconds < steadySeconds ? trafficProfileTargetAt(profileMapping, elapsedSeconds) : 0)
        : group.stages?.length
          ? (elapsedSeconds < steadySeconds ? stagedValue(group.stages, elapsedSeconds, 'target_vusers') : 0)
          : simpleGroupValue(group, elapsedSeconds, steadySeconds)
      point[groupKey] = Math.round(value * 100) / 100
      point.totalVusers += value

      if ((profileTps && profileMapping) || group.stages?.some(stage => Number(stage.target_tps) > 0)) {
        const tps = elapsedSeconds < steadySeconds
          ? profileTps && profileMapping
            ? trafficProfileTargetAt(profileMapping, elapsedSeconds)
            : stagedValue(group.stages || [], elapsedSeconds, 'target_tps')
          : 0
        point[tpsKey] = Math.round(tps * 100) / 100
        point.totalTps = (point.totalTps || 0) + tps
      }
    })

    if (hasGoalTps) {
      point.totalTps = Math.max(point.totalTps || 0, Number(params.goalTargetTps) || 0)
    }

    point.totalVusers = Math.round(point.totalVusers * 100) / 100
    if (point.totalTps !== undefined) {
      point.totalTps = Math.round(point.totalTps * 100) / 100
      peakTps = Math.max(peakTps, point.totalTps)
    }
    peakVusers = Math.max(peakVusers, point.totalVusers)
    return point
  })

  return {
    points,
    showVUsers,
    peakVusers,
    peakTps: profileTpsStatistics?.maximum ?? (includeTps ? peakTps : undefined),
    minTps: profileTpsStatistics?.minimum,
    averageTps: profileTpsStatistics?.average,
    estimatedPeakVusers: profileTps && params.trafficProfile?.sizing?.complete ? params.trafficProfile.sizing.estimated_peak_vusers : undefined,
    recommendedGenerators: profileTps && params.trafficProfile?.sizing?.complete ? params.trafficProfile.sizing.recommended_generators : undefined,
    durationSeconds,
    series: [
      ...groupSeries,
      { key: 'totalVusers', label: 'Total VUsers', color: 'var(--text-primary)', kind: 'total', interpolation: allVuserSeriesAreStepped ? 'stepAfter' : 'linear' },
      ...(tpsGroupIndexes.length > 1 ? tpsGroupIndexes.map(index => ({
        key: tpsSeriesKey(index),
        label: `${groups[index].name || `Group ${index + 1}`} TPS`,
        color: GROUP_COLORS[index % GROUP_COLORS.length],
        kind: 'tps' as const,
        interpolation: (profileTps && profileMappings[index]) || groupUsesSteppedStages(groups[index]) ? 'stepAfter' as const : 'linear' as const,
      })) : []),
      ...(includeTps ? [{ key: 'totalTps', label: profileTps ? 'Production target TPS' : 'Target TPS', color: 'var(--warning)', kind: 'tps', interpolation: profileTps || tpsGroupIndexes.every(index => groupUsesSteppedStages(groups[index])) ? 'stepAfter' : 'linear' } as ScenarioModelSeries] : []),
    ],
  }
}
