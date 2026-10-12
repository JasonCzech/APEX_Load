import type { MetricsHistoryPoint, TestSummary } from '../types'
import type { CommandGroup } from './commandCenterScenarioRows'
import type { RunLoadProfilePreview } from './newTestRunModel'
import { buildQueuedScenarioPreview, type QueuedScenarioPreview } from './testExecutionScenarioModel'
import { plannedVUsersAt } from './loadPlanSteps'
import { formatTrafficProfileAxisTime } from './trafficProfilePreviewModel'

export { plannedVUsersAt }

/**
 * A run's load plan as the engine runs it: the projection the scenario
 * builder and New Test draw, with each ramped group at the VUser target it
 * has now (an operator's live change, or a goal run's sizing, replaces the
 * configured count). Staged groups keep their authored stages. A run to its
 * iterations is planned through horizonSeconds (see buildQueuedScenarioPreview).
 */
export function buildRunLoadPlan(test: TestSummary, groups: CommandGroup[], horizonSeconds?: number): QueuedScenarioPreview {
  return buildQueuedScenarioPreview({
    ...test,
    groups: (test.groups || []).map(group => {
      if (group.stages?.length) return group
      const live = groups.find(candidate => candidate.name === group.name)
      const target = live?.targetMode === 'vusers' ? live.target : live?.suggestedVusers
      return target !== undefined && Number.isFinite(target) ? { ...group, vusers: Math.max(0, Math.round(target)) } : group
    }),
  }, horizonSeconds)
}

/**
 * Where a run's plan is at an elapsed second, in the words an operator uses.
 * An open-ended plan (a run to its iterations) has no deadline to pass.
 */
export function loadPlanPhaseAt(plan: RunLoadProfilePreview, second: number, staged: boolean, openEnded = false): string {
  if (plan.points.length === 0) return 'No load plan'
  const deadline = openEnded ? Number.POSITIVE_INFINITY : plan.preBaselineSeconds + plan.steadySeconds
  if (second < plan.preBaselineSeconds) return 'Before load'
  const planned = plannedVUsersAt(plan, second)
  if (second >= deadline) return planned > 0 ? 'Ramp-down' : 'Load complete'
  if (staged) return 'Staged load'
  const steadyPeak = plan.points.reduce((peak, point) => point.second >= plan.preBaselineSeconds && point.second < deadline ? Math.max(peak, point.total || 0) : peak, 0)
  return planned < steadyPeak ? 'Ramp-up' : openEnded ? 'Running iterations' : 'Steady state'
}

/** A run's VUser samples on elapsed seconds from its start; samples from before it are dropped. */
export function observedVUsersSince(history: MetricsHistoryPoint[], startedMs: number): Array<{ second: number; vusers: number }> {
  if (!Number.isFinite(startedMs)) return []
  return history.flatMap(point => {
    const second = point.timestampMs === undefined ? Number.NaN : (point.timestampMs - startedMs) / 1000
    return Number.isFinite(second) && second >= 0 ? [{ second, vusers: point.vusers }] : []
  })
}

/**
 * Where a run stands against its plan at an elapsed second: the planned VUsers
 * then, its progress through the plan, the phase, and the elapsed label. A run
 * that has ended is past its plan, whatever the plan had left.
 */
export function runLoadStatus(plan: RunLoadProfilePreview, elapsedSeconds: number, options: { state: 'not_started' | 'paused' | 'running' | 'ended'; openEnded: boolean; staged: boolean }) {
  const { state, openEnded, staged } = options
  const at = openEnded ? Math.min(elapsedSeconds, Math.max(0, plan.totalSeconds - 1e-6)) : elapsedSeconds
  const elapsed = formatTrafficProfileAxisTime(elapsedSeconds)
  return {
    plannedNow: plannedVUsersAt(plan, at),
    progressPct: state === 'ended' ? 100 : !openEnded && plan.totalSeconds > 0 ? Math.max(0, Math.min(100, elapsedSeconds / plan.totalSeconds * 100)) : 0,
    phase: state === 'ended' ? 'Run ended' : state === 'paused' ? 'Paused' : state === 'not_started' ? 'Not started' : loadPlanPhaseAt(plan, at, staged, openEnded),
    // "1m of 5m elapsed", or just "4m elapsed" for a run to its iterations.
    elapsedLabel: openEnded ? `${elapsed} elapsed` : `${elapsed} of ${formatTrafficProfileAxisTime(plan.totalSeconds)} elapsed`,
  }
}

/**
 * What a live load change does, said in its confirmation: the group's applied
 * target to the new one, and, with more than one group, the run's total
 * ("Store_Browse 2 → 4 VUsers · run total 6 → 8 VUsers"). A goal run's
 * targets are TPS; a gradual VUser change names its ramp.
 */
export function loadChangeImpact(groups: CommandGroup[], groupName: string, target: number, rampDuration = ''): string {
  const group = groups.find(item => item.name === groupName)
  const mode = group?.targetMode ?? 'vusers'
  const applied = (item: CommandGroup) => mode === 'tps' ? item.committedTargetTps ?? 0 : item.targetVusers
  const amount = (value: number) => value.toLocaleString(undefined, { maximumFractionDigits: mode === 'tps' ? 1 : 0 })
  const unit = mode === 'tps' ? 'TPS' : 'VUsers'
  const from = group ? applied(group) : 0
  const change = `${groupName} ${amount(from)} → ${amount(target)} ${unit}${rampDuration ? ` over ${rampDuration}` : ''}`
  if (groups.length < 2) return change
  const totalFrom = groups.reduce((sum, item) => sum + applied(item), 0)
  return `${change} · ${mode === 'tps' ? 'run goal' : 'run total'} ${amount(totalFrom)} → ${amount(totalFrom - from + target)} ${unit}`
}
