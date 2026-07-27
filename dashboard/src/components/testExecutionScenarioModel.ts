import { TestSummary } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

const RESOURCE_BASELINE_SECONDS = 5 * 60

export interface ScenarioScriptDetail {
  id: string
  name: string
  protocol: string
  version?: string
  script?: {
    config?: {
      base_url?: string
      think_time?: {
        type?: string
        mean_ms?: number
        ignore?: boolean
      }
      pacing?: {
        type?: string
        delay_ms?: number
        min_delay_ms?: number
        max_delay_ms?: number
        interval_ms?: number
        min_interval_ms?: number
        max_interval_ms?: number
        tps?: number
      }
      run_logic?: {
        iterations?: number
        auto_transactions?: string
      }
      log?: {
        level?: string
      }
      error_handling?: {
        continue_on_error?: boolean
        snapshot_on_error?: boolean
        max_retries?: number
      }
      http?: {
        version?: string
        max_conns_per_host?: number
      }
    }
    data_sources?: Array<{ name?: string; type?: string; strategy?: string }>
    init?: unknown[]
    actions?: unknown[]
    teardown?: unknown[]
  }
}

type ScenarioGroup = NonNullable<TestSummary['groups']>[number]

export interface QueuedScenarioPreview {
  points: Array<Record<string, number>>
  totalVUsers: number
  totalSeconds: number
  steadySeconds: number
  startDelaySeconds: number
  preBaselineSeconds: number
  postBaselineSeconds: number
}

export interface QueuedScenarioRow {
  group: ScenarioGroup
  detail?: ScenarioScriptDetail
  actionCount: number
  initCount: number
  teardownCount: number
  dataSourceCount: number
}

export function buildQueuedScenarioPreview(test: TestSummary | null | undefined): QueuedScenarioPreview {
  if (!test?.groups?.length) {
    return {
      points: [],
      totalVUsers: 0,
      totalSeconds: 0,
      steadySeconds: 0,
      startDelaySeconds: 0,
      preBaselineSeconds: 0,
      postBaselineSeconds: 0,
    }
  }

  const steadySeconds = parseDurationToSeconds(test.duration)
  const startDelaySeconds = parseDurationToSeconds(test.start_delay)
  const preBaselineSeconds = startDelaySeconds + (test.resource_baseline ? RESOURCE_BASELINE_SECONDS : 0)
  const postBaselineSeconds = test.resource_baseline ? RESOURCE_BASELINE_SECONDS : 0
  let maxRampDown = 0
  test.groups.forEach((group) => {
    const rampDown = parseDurationToSeconds(group.ramp_down)
    if (rampDown > maxRampDown) maxRampDown = rampDown
  })

  const totalSeconds = preBaselineSeconds + steadySeconds + maxRampDown + postBaselineSeconds
  if (totalSeconds <= 0) {
    return {
      points: [],
      totalVUsers: test.total_vusers,
      totalSeconds: 0,
      steadySeconds,
      startDelaySeconds,
      preBaselineSeconds,
      postBaselineSeconds,
    }
  }

  const step = Math.max(1, Math.floor(totalSeconds / 30))
  const points: Array<Record<string, number>> = []

  for (let second = 0; second <= totalSeconds; second += step) {
    const point: Record<string, number> = { second, total: 0 }
    test.groups.forEach((group, index) => {
      const rampUp = parseDurationToSeconds(group.ramp_up)
      const rampDown = parseDurationToSeconds(group.ramp_down)
      let value = 0

      const loadSecond = Math.max(0, second - preBaselineSeconds)

      if (second < preBaselineSeconds) {
        value = 0
      } else if (loadSecond < rampUp) {
        value = rampUp > 0 ? (loadSecond / rampUp) * group.vusers : group.vusers
      } else if (loadSecond < steadySeconds) {
        value = group.vusers
      } else if (loadSecond < steadySeconds + rampDown) {
        value = rampDown > 0 ? (1 - (loadSecond - steadySeconds) / rampDown) * group.vusers : 0
      }

      const roundedValue = Math.round(Math.max(0, value))
      point[`group${index}`] = roundedValue
      point.total += roundedValue
    })
    points.push(point)
  }

  const lastPoint = points[points.length - 1]
  if (!lastPoint || lastPoint.second < totalSeconds) {
    const finalPoint: Record<string, number> = { second: totalSeconds, total: 0 }
    test.groups.forEach((_, index) => {
      finalPoint[`group${index}`] = 0
    })
    points.push(finalPoint)
  }

  return {
    points,
    totalVUsers: test.total_vusers,
    totalSeconds,
    steadySeconds,
    startDelaySeconds,
    preBaselineSeconds,
    postBaselineSeconds,
  }
}

export function buildQueuedScenarioRows(
  test: TestSummary | null | undefined,
  scriptDetails: Record<string, ScenarioScriptDetail>,
): QueuedScenarioRow[] {
  if (!test?.groups?.length) {
    return []
  }

  return test.groups.map((group) => {
    const detail = scriptDetails[group.script_id]
    return {
      group,
      detail,
      actionCount: detail?.script?.actions?.length || 0,
      initCount: detail?.script?.init?.length || 0,
      teardownCount: detail?.script?.teardown?.length || 0,
      dataSourceCount: detail?.script?.data_sources?.length || 0,
    }
  })
}
