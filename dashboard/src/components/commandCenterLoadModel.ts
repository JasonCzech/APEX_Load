import type { TestGroup } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import type { CommandGroup } from './commandCenterScenarioRows'

export interface LoadModelPoint {
  pct: number
  planned: number
  phase: string
}

function authoredGroupStageDuration(group: TestGroup) {
  return (group.stages || []).reduce(
    (total, stage) => total + Math.max(0, parseDurationToSeconds(stage.duration)),
    0,
  )
}

export function plannedGroupLoadAt(group: TestGroup, target: number, elapsedSeconds: number, modelDurationSeconds: number) {
  const stages = group.stages || []
  if (stages.length > 0) {
    let cursor = 0
    // Runtime compilation seeds a stage-driven group at zero for a ramp-up, at
    // the hold target for a hold, and at the authored peak for a ramp-down.
    // Starting every profile at zero makes a first ramp-down appear to ramp up.
    const firstType = stages[0]?.type?.trim().toLowerCase()
    const stagePeak = stages.reduce(
      (maximum, stage) => Math.max(maximum, Math.max(0, stage.target_vusers || 0)),
      Math.max(0, target),
    )
    let previous = firstType === 'ramp_down'
      ? stagePeak
      : firstType === 'hold'
        ? Math.max(0, stages[0]?.target_vusers || 0)
        : 0
    for (const stage of stages) {
      const duration = Math.max(0, parseDurationToSeconds(stage.duration))
      const next = Math.max(0, stage.target_vusers ?? previous)
      const stageType = stage.type?.trim().toLowerCase()
      if (elapsedSeconds <= cursor + duration) {
        if (duration === 0 || stageType === 'hold') return next
        const ratio = Math.max(0, Math.min(1, (elapsedSeconds - cursor) / duration))
        return previous + (next - previous) * ratio
      }
      cursor += duration
      previous = next
    }
    return previous
  }

  const rampUp = Math.max(0, parseDurationToSeconds(group.ramp_up || '0s'))
  const rampDown = Math.max(0, parseDurationToSeconds(group.ramp_down || '0s'))
  if (rampUp > 0 && elapsedSeconds < rampUp) return target * elapsedSeconds / rampUp
  const rampDownStart = Math.max(rampUp, modelDurationSeconds - rampDown)
  if (rampDown > 0 && elapsedSeconds > rampDownStart) {
    return target * Math.max(0, modelDurationSeconds - elapsedSeconds) / rampDown
  }
  return target
}

export function buildLoadModelProfile(testGroups: TestGroup[], groups: CommandGroup[], totalDurationSeconds: number): LoadModelPoint[] {
  const stagedDuration = testGroups.reduce(
    (maximum, group) => Math.max(maximum, authoredGroupStageDuration(group)),
    0,
  )
  const modelDuration = Math.max(1, stagedDuration, totalDurationSeconds)
  const boundaries = new Set<number>([0, modelDuration])
  testGroups.forEach(group => {
    let cursor = 0
    if ((group.stages || []).length > 0) {
      group.stages?.forEach(stage => {
        cursor += Math.max(0, parseDurationToSeconds(stage.duration))
        boundaries.add(Math.min(modelDuration, cursor))
      })
      return
    }
    const rampUp = Math.max(0, parseDurationToSeconds(group.ramp_up || '0s'))
    const rampDown = Math.max(0, parseDurationToSeconds(group.ramp_down || '0s'))
    boundaries.add(Math.min(modelDuration, rampUp))
    boundaries.add(Math.max(0, modelDuration - rampDown))
  })
  return [...boundaries]
    .sort((left, right) => left - right)
    .map(elapsed => {
      const planned = testGroups.reduce((sum, group) => {
        const commandGroup = groups.find(candidate => candidate.name === group.name)
        const target = commandGroup?.targetMode === 'vusers'
          ? commandGroup.target
          : commandGroup?.suggestedVusers ?? group.vusers
        return sum + plannedGroupLoadAt(group, Math.max(0, target || 0), elapsed, modelDuration)
      }, 0)
      return {
        pct: elapsed / modelDuration * 100,
        planned: Math.round(planned),
        phase: elapsed === 0 ? 'Start' : elapsed >= modelDuration ? 'End' : 'Authored profile',
      }
    })
}
