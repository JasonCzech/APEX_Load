// The engine's own load schedule (pkg/engine/group.go). A group starts its
// VUsers one at a time, rampUp / vusers apart with the first at once, and at
// the scenario deadline stops them one at a time, rampDown / running apart
// with the first one interval later. Load is therefore a staircase, which is
// how every load chart draws it.

/** Group colors on every load chart, in group order: the theme's validated chart palette. */
export const LOAD_PLAN_COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)']

/** Boundaries drawn per ramp at most; a longer ramp is drawn at this resolution. */
export const MAX_RAMP_STEPS = 240

/**
 * Step times are computed as index * interval; a level read at such a time
 * must count that step even when the division lands a hair below the index.
 */
export const STEP_EPSILON = 1e-9

function rampInterval(count: number, rampSeconds: number) {
  return count > 1 && rampSeconds > 0 ? rampSeconds / count : 0
}

/** VUsers a group has started activeSeconds after it activated. */
export function engineRampUpLevel(vusers: number, rampUpSeconds: number, activeSeconds: number): number {
  const target = Math.max(0, Math.floor(vusers) || 0)
  if (target === 0 || activeSeconds < 0) return 0
  const interval = rampInterval(target, rampUpSeconds)
  if (interval === 0) return target
  return Math.min(target, Math.floor(activeSeconds / interval + STEP_EPSILON) + 1)
}

/** VUsers a group started before its deadline, activeSeconds after it activated. */
export function engineLevelAtDeadline(vusers: number, rampUpSeconds: number, activeSeconds: number): number {
  if (activeSeconds <= 0) return 0
  const target = Math.max(0, Math.floor(vusers) || 0)
  const interval = rampInterval(target, rampUpSeconds)
  if (interval === 0) return target
  // A VUser due at the deadline itself is never started.
  return Math.min(target, Math.ceil(activeSeconds / interval - STEP_EPSILON))
}

/** VUsers still running sinceSeconds after the deadline, of level running then. */
export function engineRampDownLevel(level: number, rampDownSeconds: number, sinceSeconds: number): number {
  const running = Math.max(0, Math.floor(level) || 0)
  if (running === 0 || sinceSeconds < 0) return running
  if (rampDownSeconds <= 0) return 0
  const interval = rampDownSeconds / running
  return Math.max(0, running - Math.floor(sinceSeconds / interval + STEP_EPSILON))
}

// Every step time of a ramp of count steps over rampSeconds, from first (0
// for a ramp-up, 1 for a ramp-down), at most MAX_RAMP_STEPS of them.
function stepSeconds(count: number, rampSeconds: number, first: 0 | 1): number[] {
  const steps = Math.max(0, Math.floor(count) || 0)
  if (steps === 0 || rampSeconds <= 0) return []
  const interval = rampSeconds / steps
  const stride = Math.max(1, Math.ceil(steps / MAX_RAMP_STEPS))
  const out: number[] = []
  for (let index = first; index < steps + first; index += stride) out.push(index * interval)
  const last = (steps - 1 + first) * interval
  if (out[out.length - 1] !== last) out.push(last)
  return out
}

/** When a group's VUsers start, in seconds after it activates. */
export function engineRampUpSeconds(vusers: number, rampUpSeconds: number): number[] {
  return stepSeconds(vusers > 1 ? vusers : 0, rampUpSeconds, 0)
}

/** When a group's running VUsers stop, in seconds after the deadline. */
export function engineRampDownSeconds(level: number, rampDownSeconds: number): number[] {
  return stepSeconds(level, rampDownSeconds, 1)
}

const TIME_TICK_STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400]

/** Round elapsed-time ticks (whole seconds, minutes or hours) from 0 to totalSeconds. */
export function loadTimeTicks(totalSeconds: number, maxTicks = 6): number[] {
  const total = Math.max(0, totalSeconds)
  if (total === 0) return [0]
  const step = TIME_TICK_STEPS.find(candidate => total / candidate <= maxTicks - 1) ?? Math.ceil(total / (maxTicks - 1) / 86400) * 86400
  const ticks: number[] = []
  for (let tick = 0; tick <= total + STEP_EPSILON; tick += step) ticks.push(tick)
  return ticks
}

/** Round value ticks from 0 up to at least maxValue: 0, 5, 10, 15 rather than 0, 4, 8, 15. */
export function loadValueTicks(maxValue: number, maxTicks = 5): number[] {
  const top = Math.max(1, Math.ceil(maxValue))
  const rough = top / (maxTicks - 1)
  const magnitude = 10 ** Math.floor(Math.log10(rough))
  const step = Math.max(1, [1, 2, 2.5, 5, 10].map(factor => factor * magnitude).find(candidate => candidate >= rough && Number.isInteger(candidate)) ?? 10 * magnitude)
  const ticks: number[] = []
  for (let tick = 0; tick < top + step; tick += step) {
    ticks.push(tick)
    if (tick >= top) break
  }
  return ticks
}

/** Planned VUsers at an elapsed second: the step of a plan in force then. */
export function plannedVUsersAt(plan: { points: ReadonlyArray<Record<string, number>> }, second: number): number {
  let planned = 0
  for (const point of plan.points) {
    if (point.second > second) break
    planned = point.total || 0
  }
  return planned
}
