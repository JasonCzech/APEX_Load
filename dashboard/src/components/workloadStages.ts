import type { LoadStageDraft } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

// Load stage semantics shared by every load preview (AUTH-3): the scenario
// editors' model chart, the launch page and the execution preview. They follow
// the controller's stage schedule (pkg/api/testexecution.go runStageSchedule):
// stages run in order from the group's activation; a stage owns [start, end),
// so at its end the next stage's target already applies; a ramp moves linearly
// from the level before it to its target; a hold applies its target at once;
// after the last stage the last level holds.

/** A stage type as the controller reads it. */
export function stageType(stage: Pick<LoadStageDraft, 'type'>): string {
  return String(stage.type || '').trim().toLowerCase()
}

/** The highest VUser target across stages (zero without one). */
export function maxStageVUsers(stages?: LoadStageDraft[]): number {
  return (stages || []).reduce((max, stage) => Math.max(max, Number(stage.target_vusers) || 0), 0)
}

/**
 * The level a group is at when its first stage starts. A ramp moves from the
 * group's current level, so a schedule that opens by ramping down starts from
 * the group's VUsers and one that opens with a hold starts at its target; any
 * other starts from zero. Only VUser levels have a group level to start from.
 */
export function initialStageLevel(stages: LoadStageDraft[], groupVUsers: number, valueKey: 'target_vusers' | 'target_tps' = 'target_vusers'): number {
  if (valueKey !== 'target_vusers' || stages.length === 0) return 0
  const first = stageType(stages[0])
  if (first === 'ramp_down') return Math.max(0, Number(groupVUsers) || 0)
  if (first === 'hold') return Math.max(0, Number(stages[0].target_vusers) || 0)
  return 0
}

export interface StageLevelOptions {
  /** The level before the first stage (initialStageLevel). */
  initialLevel: number
  /**
   * Seconds an empty or unparseable duration counts for. The controller
   * refuses one (0), so a saved schedule never has one; an editor drawing an
   * unfinished draft may give it a placeholder length.
   */
  emptyDurationSeconds: number
  /**
   * A stage without the target: 'zero' as the controller reads it, or
   * 'previous' to keep the level (an editor drawing a TPS-only stage's VUsers).
   */
  missingTarget: 'zero' | 'previous'
}

// Durations parse as the controller parses them (Go's time.ParseDuration); an
// empty or unparseable one counts for the caller's placeholder.
function stageSeconds(stage: LoadStageDraft, emptyDurationSeconds: number): number {
  const seconds = parseDurationToSeconds(stage.duration)
  return seconds > 0 ? seconds : emptyDurationSeconds
}

/** The total length of a stage schedule. */
export function stageScheduleSeconds(stages: LoadStageDraft[], emptyDurationSeconds = 0): number {
  return stages.reduce((sum, stage) => sum + stageSeconds(stage, emptyDurationSeconds), 0)
}

/** The second each stage ends, from the group's activation. */
export function stageBoundarySeconds(stages: LoadStageDraft[], emptyDurationSeconds = 0): number[] {
  let cursor = 0
  return stages.map(stage => (cursor += stageSeconds(stage, emptyDurationSeconds)))
}

/** The level a stage schedule reaches elapsedSeconds after the group's activation. */
export function stageLevelAt(stages: LoadStageDraft[], elapsedSeconds: number, valueKey: 'target_vusers' | 'target_tps', options: StageLevelOptions): number {
  let cursor = 0
  let previous = options.initialLevel
  for (const stage of stages) {
    const duration = stageSeconds(stage, options.emptyDurationSeconds)
    const raw = stage[valueKey]
    const target = raw === undefined || raw === null
      ? (options.missingTarget === 'previous' ? previous : 0)
      : Math.max(0, Number(raw) || 0)
    const end = cursor + duration
    if (elapsedSeconds < end) {
      const type = stageType(stage)
      if (type === 'ramp_up' || type === 'ramp_down') {
        const progress = duration > 0 ? Math.max(0, Math.min(1, (elapsedSeconds - cursor) / duration)) : 1
        return previous + ((target - previous) * progress)
      }
      return target
    }
    cursor = end
    previous = target
  }
  return previous
}
