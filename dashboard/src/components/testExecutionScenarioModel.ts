import { TestSummary } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { buildRunLoadProfilePreview, type RunLoadProfilePreview, type ScenarioScriptGroup } from './newTestRunModel'

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

/** A queued run's load preview: the shared run projection, plus its configured totals. */
export interface QueuedScenarioPreview extends RunLoadProfilePreview {
  totalVUsers: number
  startDelaySeconds: number
}

export interface QueuedScenarioRow {
  group: ScenarioGroup
  detail?: ScenarioScriptDetail
  actionCount: number | null
  initCount: number | null
  teardownCount: number | null
  dataSourceCount: number | null
}

/**
 * The load preview of a queued run's configuration, from the projection the
 * scenario editors and the launch page use (AUTH-3): group start offsets,
 * stages, baselines, traffic profiles and goal TPS are drawn as the run will
 * execute them.
 */
export function buildQueuedScenarioPreview(test: TestSummary | null | undefined): QueuedScenarioPreview {
  const groups: ScenarioScriptGroup[] = (test?.groups || []).map((group, order) => ({
    order,
    name: group.name,
    script_id: group.script_id,
    vusers: group.vusers,
    ramp_up: group.ramp_up,
    ramp_down: group.ramp_down,
    start_offset: group.start_offset,
    stages: group.stages,
    initialization: group.initialization,
    ...(group.role ? { role: group.role } : {}),
  }))
  const preview = buildRunLoadProfilePreview(
    groups,
    test?.duration || '',
    test?.start_delay,
    Boolean(test?.resource_baseline),
    // A run summary carries no traffic profile; its groups are drawn from
    // their configured ramps and stages.
    undefined,
    test?.scenario_mode,
    test?.goal_config?.target_average_tps,
  )
  return {
    ...preview,
    totalVUsers: test?.total_vusers ?? 0,
    startDelaySeconds: parseDurationToSeconds(test?.start_delay),
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
      actionCount: detail ? detail.script?.actions?.length ?? 0 : null,
      initCount: detail ? detail.script?.init?.length ?? 0 : null,
      teardownCount: detail ? detail.script?.teardown?.length ?? 0 : null,
      dataSourceCount: detail ? detail.script?.data_sources?.length ?? 0 : null,
    }
  })
}
