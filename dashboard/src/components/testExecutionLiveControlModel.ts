import { TestSummary } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export interface LiveControlPreview {
  points: Array<Record<string, number>>
  currentTotal: number
  projectedTotal: number
  remainingSeconds: number
  projectedDurationSeconds: number
  hasPendingChanges: boolean
}

interface BuildLiveControlPreviewOptions {
  test: TestSummary | null | undefined
  durationInput: string
  vuserTargets: Record<string, number>
  nowMs?: number
}

export function buildLiveControlPreview({
  test,
  durationInput,
  vuserTargets,
  nowMs = Date.now(),
}: BuildLiveControlPreviewOptions): LiveControlPreview {
  if (!test?.groups?.length) {
    return {
      points: [],
      currentTotal: 0,
      projectedTotal: 0,
      remainingSeconds: 0,
      projectedDurationSeconds: 0,
      hasPendingChanges: false,
    }
  }

  const groupMetricsMap = test.live_metrics?.groups ?? {}
  const startedAt = test.started_at || test.start_time
  const elapsedSeconds = startedAt
    ? Math.max(0, Math.floor((nowMs - new Date(startedAt).getTime()) / 1000))
    : 0
  const projectedDurationSeconds = parseDurationToSeconds(durationInput || test.duration)
  const remainingSeconds = Math.max(projectedDurationSeconds - elapsedSeconds, 0)
  const transitionSeconds = remainingSeconds > 0 ? Math.min(Math.max(Math.round(remainingSeconds * 0.06), 1), 30) : 1

  const currentPoint: Record<string, number> = { second: 0, total: 0 }
  const projectedPoint: Record<string, number> = { second: transitionSeconds, total: 0 }
  const steadyPoint: Record<string, number> = { second: Math.max(transitionSeconds, remainingSeconds), total: 0 }
  const endPoint: Record<string, number> = { second: Math.max(transitionSeconds + 1, remainingSeconds + 1), total: 0 }

  let hasPendingChanges = durationInput !== '' && durationInput !== test.duration

  test.groups.forEach((group, index) => {
    const dataKey = `group${index}`
    const liveGroup = groupMetricsMap[group.name]
    const currentValue = liveGroup?.vusers ?? group.vusers
    const projectedValue = vuserTargets[group.name] ?? group.vusers

    currentPoint[dataKey] = currentValue
    projectedPoint[dataKey] = projectedValue
    steadyPoint[dataKey] = projectedValue
    endPoint[dataKey] = 0

    currentPoint.total += currentValue
    projectedPoint.total += projectedValue
    steadyPoint.total += projectedValue

    if (projectedValue !== group.vusers) {
      hasPendingChanges = true
    }
  })

  return {
    points: [currentPoint, projectedPoint, steadyPoint, endPoint],
    currentTotal: currentPoint.total,
    projectedTotal: projectedPoint.total,
    remainingSeconds,
    projectedDurationSeconds,
    hasPendingChanges,
  }
}