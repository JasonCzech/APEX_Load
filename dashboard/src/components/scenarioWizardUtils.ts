import { LoadModelSupportEntry, LoadModelTemplate, LoadStageDraft, StageTimelineValidationResult } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export type ScenarioWizardStep = 'start' | 'source' | 'scripts' | 'run' | 'data' | 'review'

export type ScenarioSourceMode = 'shape' | 'existing'

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
}

export interface ScenarioModelPreview {
  points: ScenarioModelPoint[]
  series: ScenarioModelSeries[]
  peakVusers: number
  peakTps?: number
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
  if (hours > 0) return `T+${hours}h${minutes}m`
  if (minutes > 0) return `T+${minutes}m${secs ? `${secs}s` : ''}`
  return `T+${secs}s`
}

export function validateStageTimeline(duration: string, stages: LoadStageDraft[]): StageTimelineValidationResult {
  const scenarioDurationSeconds = parseDurationToSeconds(duration)
  if (stages.length === 0) {
    return { status: 'ok', totalSeconds: 0, scenarioDurationSeconds }
  }
  if (scenarioDurationSeconds <= 0) {
    return {
      status: 'error',
      totalSeconds: 0,
      scenarioDurationSeconds,
      message: 'Set a positive scenario duration before using stair-step stages.',
    }
  }

  let hasInvalidStage = false
  const totalSeconds = stages.reduce((sum, stage) => {
    const seconds = parseDurationToSeconds(stage.duration)
    if (seconds <= 0) hasInvalidStage = true
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

  if (totalSeconds > scenarioDurationSeconds) {
    return {
      status: 'error',
      totalSeconds,
      scenarioDurationSeconds,
      message: `Stage schedule is ${formatModelTimeLabel(totalSeconds).replace('T+', '')}, longer than the ${formatModelTimeLabel(scenarioDurationSeconds).replace('T+', '')} scenario duration.`,
    }
  }

  if (totalSeconds < scenarioDurationSeconds) {
    return {
      status: 'warning',
      totalSeconds,
      scenarioDurationSeconds,
      message: `Stages cover ${formatModelTimeLabel(totalSeconds).replace('T+', '')}; the last target holds for the remaining ${formatModelTimeLabel(scenarioDurationSeconds - totalSeconds).replace('T+', '')}.`,
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
  const rampDownSeconds = Math.min(parseDurationSeconds(group.ramp_down, 0), durationSeconds)
  const rampDownStart = Math.max(rampUpSeconds, durationSeconds - rampDownSeconds)

  if (rampUpSeconds > 0 && elapsedSeconds < rampUpSeconds) {
    return target * (elapsedSeconds / rampUpSeconds)
  }
  if (rampDownSeconds > 0 && elapsedSeconds > rampDownStart) {
    return Math.max(0, target * (1 - ((elapsedSeconds - rampDownStart) / rampDownSeconds)))
  }
  return target
}

function stagedValue(stages: LoadStageDraft[], elapsedSeconds: number, valueKey: 'target_vusers' | 'target_tps'): number {
  let cursor = 0
  let previousValue = 0
  for (const stage of stages) {
    const stageDuration = parseDurationSeconds(stage.duration, 60)
    const rawTarget = stage[valueKey]
    const target = rawTarget === undefined ? previousValue : Math.max(0, Number(rawTarget) || 0)
    const stageEnd = cursor + stageDuration
    if (elapsedSeconds <= stageEnd) {
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

function collectTimeline(durationSeconds: number, groups: ScenarioModelGroupInput[]): number[] {
  const step = Math.max(1, Math.ceil(durationSeconds / 24))
  const values = new Set<number>([0, durationSeconds])
  for (let second = step; second < durationSeconds; second += step) {
    values.add(second)
  }
  groups.forEach((group) => {
    const rampUpSeconds = parseDurationSeconds(group.ramp_up, 0)
    if (rampUpSeconds > 0) {
      values.add(Math.min(durationSeconds, rampUpSeconds))
    }
    const rampDownSeconds = parseDurationSeconds(group.ramp_down, 0)
    if (rampDownSeconds > 0) {
      values.add(Math.max(0, durationSeconds - rampDownSeconds))
    }
    if (group.stages?.length) {
      stageTimelineSeconds(group.stages).forEach(second => values.add(Math.min(durationSeconds, second)))
    }
  })
  return Array.from(values).filter(second => second >= 0 && second <= durationSeconds).sort((left, right) => left - right)
}

export function buildScenarioModelPreview(params: {
  duration: string
  groups: ScenarioModelGroupInput[]
  goalTargetTps?: number
}): ScenarioModelPreview {
  const durationSeconds = parseDurationSeconds(params.duration, 60)
  const groups = params.groups.length > 0 ? params.groups : [{ vusers: 0 }]
  const timeline = collectTimeline(durationSeconds, groups)
  const groupSeries: ScenarioModelSeries[] = groups.map((group, index) => ({
    key: safeSeriesKey(index),
    label: group.name || `Group ${index + 1}`,
    color: GROUP_COLORS[index % GROUP_COLORS.length],
    kind: 'group',
  }))
  const hasGroupTps = groups.some(group => group.stages?.some(stage => Number(stage.target_tps) > 0))
  const hasGoalTps = Number(params.goalTargetTps) > 0
  const includeTps = hasGroupTps || hasGoalTps

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
      const value = group.stages?.length
        ? stagedValue(group.stages, elapsedSeconds, 'target_vusers')
        : simpleGroupValue(group, elapsedSeconds, durationSeconds)
      point[groupKey] = Math.round(value * 100) / 100
      point.totalVusers += value

      if (group.stages?.some(stage => Number(stage.target_tps) > 0)) {
        const tps = stagedValue(group.stages, elapsedSeconds, 'target_tps')
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
    peakVusers,
    peakTps: includeTps ? peakTps : undefined,
    durationSeconds,
    series: [
      ...groupSeries,
      { key: 'totalVusers', label: 'Total VUsers', color: 'var(--text-primary)', kind: 'total' },
      ...(includeTps ? [{ key: 'totalTps', label: 'Target TPS', color: 'var(--warning)', kind: 'tps' } as ScenarioModelSeries] : []),
    ],
  }
}
