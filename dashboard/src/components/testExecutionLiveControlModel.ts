import { TestSummary } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export interface LiveControlPreview {
  points: Array<Record<string, number>>
  currentTotal: number
  projectedTotal: number
  remainingSeconds: number
  projectedDurationSeconds: number
  vuserTransitionSeconds: number
  isGradualVuserChange: boolean
  hasActiveVuserRamp: boolean
  hasPendingChanges: boolean
}

export type VUserGroupAction = 'run' | 'pause'

interface BuildLiveControlPreviewOptions {
  test: TestSummary | null | undefined
  durationInput: string
  vuserTargets: Record<string, number>
  vuserGroupActions?: Record<string, VUserGroupAction>
  vuserRampEnabled?: boolean
  vuserRampDurationInput?: string
  nowMs?: number
}

export function buildLiveControlPreview({
  test,
  durationInput,
  vuserTargets,
  vuserGroupActions = {},
  vuserRampEnabled = false,
  vuserRampDurationInput = '',
  nowMs = Date.now(),
}: BuildLiveControlPreviewOptions): LiveControlPreview {
  if (!test?.groups?.length) {
    return {
      points: [],
      currentTotal: 0,
      projectedTotal: 0,
      remainingSeconds: 0,
      projectedDurationSeconds: 0,
      vuserTransitionSeconds: 0,
      isGradualVuserChange: false,
      hasActiveVuserRamp: false,
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
  const rampSeconds = vuserRampEnabled ? parseDurationToSeconds(vuserRampDurationInput) : 0
  const groupTargets = test.groups.map((group, index) => {
    const liveGroup = groupMetricsMap[group.name]
    const currentValue = liveGroup?.vusers ?? group.vusers
    const activeRamp = test.vuser_ramps?.[group.name]
    const selectedAction = vuserGroupActions[group.name] ?? 'run'
    const runTarget = vuserTargets[group.name] ?? activeRamp?.target_vusers ?? group.vusers
    const projectedValue = selectedAction === 'run' ? runTarget : 0
    return {
      dataKey: `group${index}`,
      group,
      currentValue,
      projectedValue,
      activeRamp,
    }
  })
  const hasVuserTargetChanges = groupTargets.some(({ currentValue, projectedValue }) => projectedValue !== currentValue)
  const activeRampRemainingSeconds = Math.max(0, ...groupTargets.map(({ activeRamp }) => (
    activeRamp ? getActiveRampRemainingSeconds(activeRamp, nowMs) : 0
  )))
  const hasActiveVuserRamp = activeRampRemainingSeconds > 0 && groupTargets.some(({ activeRamp, currentValue }) => (
    activeRamp !== undefined && activeRamp.target_vusers !== currentValue
  ))
  const useGradualVuserChange = (rampSeconds > 0 && hasVuserTargetChanges) || hasActiveVuserRamp
  const requestedTransitionSeconds = rampSeconds > 0 && hasVuserTargetChanges ? rampSeconds : 0
  const activeTransitionSeconds = hasActiveVuserRamp ? Math.max(activeRampRemainingSeconds, 1) : 0
  const transitionSeconds = Math.max(requestedTransitionSeconds, activeTransitionSeconds) > 0
    ? Math.max(requestedTransitionSeconds, activeTransitionSeconds)
    : remainingSeconds > 0 ? Math.min(Math.max(Math.round(remainingSeconds * 0.06), 1), 30) : 1

  const currentPoint: Record<string, number> = { second: 0, total: 0 }
  const projectedPoint: Record<string, number> = { second: transitionSeconds, total: 0 }
  const steadyPoint: Record<string, number> = { second: Math.max(transitionSeconds, remainingSeconds), total: 0 }
  const endPoint: Record<string, number> = { second: Math.max(transitionSeconds + 1, remainingSeconds + 1), total: 0 }

  let hasPendingChanges = durationInput !== '' && durationInput !== test.duration

  groupTargets.forEach(({ dataKey, currentValue, projectedValue }) => {
    currentPoint[dataKey] = currentValue
    projectedPoint[dataKey] = projectedValue
    steadyPoint[dataKey] = projectedValue
    endPoint[dataKey] = 0

    currentPoint.total += currentValue
    projectedPoint.total += projectedValue
    steadyPoint.total += projectedValue

    if (projectedValue !== currentValue) {
      hasPendingChanges = true
    }
  })

  return {
    points: [currentPoint, projectedPoint, steadyPoint, endPoint],
    currentTotal: currentPoint.total,
    projectedTotal: projectedPoint.total,
    remainingSeconds,
    projectedDurationSeconds,
    vuserTransitionSeconds: transitionSeconds,
    isGradualVuserChange: useGradualVuserChange,
    hasActiveVuserRamp,
    hasPendingChanges,
  }
}

function getActiveRampRemainingSeconds(
  ramp: NonNullable<TestSummary['vuser_ramps']>[string],
  nowMs: number,
): number {
  const configuredSeconds = parseDurationToSeconds(ramp.ramp_duration) || ramp.ramp_duration_seconds || 0
  if (configuredSeconds <= 0) return 0

  const startedAtMs = Date.parse(ramp.started_at)
  if (!Number.isFinite(startedAtMs)) {
    return configuredSeconds
  }

  const elapsedSeconds = Math.max(0, (nowMs - startedAtMs) / 1000)
  return Math.max(0, Math.ceil(configuredSeconds - elapsedSeconds))
}
