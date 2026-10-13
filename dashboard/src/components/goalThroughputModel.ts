// A goal run is judged against its TPS target in both directions, as the
// engine judges it (pkg/api goal_controller.go): over the last 10 s, and off
// target once the gap reaches 5% either way. Gaps use the engine's sign —
// (target − actual) / target, positive under the target, negative over it.

const GOAL_GAP_ALERT_PCT = 5

/** The gap of actual to target: positive under, negative over; undefined without a target. */
export function throughputGapToTarget(actual: number | undefined, target: number | undefined): number | undefined {
  if (target === undefined || !(target > 0) || actual === undefined || !Number.isFinite(actual)) return undefined
  return (target - actual) / target * 100
}

/** Whether a gap is far enough from the target, either way, to warn about. */
export function isOffTarget(gap: number | undefined): boolean {
  return gap !== undefined && Math.abs(gap) >= GOAL_GAP_ALERT_PCT
}

/** "9.0% under", "12.5% over" or "on target"; undefined without a gap. */
export function formatGapToTarget(gap: number | undefined): string | undefined {
  if (gap === undefined || !Number.isFinite(gap)) return undefined
  const size = Math.abs(gap)
  if (size < 0.05) return 'on target'
  return `${size.toFixed(1)}% ${gap > 0 ? 'under' : 'over'}`
}

/** The run's standing against its target: on, under or over. */
export function goalStanding(gap: number | undefined): 'on' | 'under' | 'over' | undefined {
  if (gap === undefined) return undefined
  if (!isOffTarget(gap)) return 'on'
  return gap > 0 ? 'under' : 'over'
}

export interface ProfilePosition {
  /** 1-based point the run is on, and the profile's point count. */
  point: number
  points: number
  target: number
  next?: { target: number; inSeconds: number }
}

/**
 * Where a run stands in a group's traffic profile, from the run's active
 * seconds: the point in force, and the next change if any. Points are step
 * targets at offsets from the start of load.
 */
export function profilePosition(points: ReadonlyArray<{ offset_ms: number; target: number }> | undefined, elapsedSeconds: number | undefined): ProfilePosition | undefined {
  if (!points || points.length === 0 || elapsedSeconds === undefined || !Number.isFinite(elapsedSeconds)) return undefined
  const elapsedMs = Math.max(0, elapsedSeconds) * 1000
  let index = 0
  for (let i = 0; i < points.length; i++) {
    if (points[i].offset_ms <= elapsedMs) index = i
  }
  const nextPoint = points[index + 1]
  return {
    point: index + 1,
    points: points.length,
    target: points[index].target,
    next: nextPoint ? { target: nextPoint.target, inSeconds: Math.max(0, Math.ceil((nextPoint.offset_ms - elapsedMs) / 1000)) } : undefined,
  }
}
