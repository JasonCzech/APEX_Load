import { ScenarioDraft, ScenarioDraftGroup, StoredScript, TestSummary } from '../types'

export type ScenarioRuntimeSettings = ScenarioDraft['runtimeSettings']
export type ScenarioDraftState = ScenarioDraft

export function buildDefaultScenarioGroup(scripts: StoredScript[]): ScenarioDraftGroup {
  return {
    name: '',
    scriptId: scripts[0]?.id || '',
    vusers: 10,
    rampUp: '',
    rampDown: '',
    initialization: 'at_runtime',
  }
}

export function buildInitialScenarioDraft(initialScenario: ScenarioDraft | undefined, scripts: StoredScript[]): ScenarioDraftState {
  return {
    name: initialScenario?.name || '',
    duration: initialScenario?.duration || '1m',
    scenarioMode: initialScenario?.scenarioMode || 'vuser_driven',
    scriptShakeout: Boolean(initialScenario?.scriptShakeout),
    resourceBaseline: Boolean(initialScenario?.resourceBaseline),
    goalConfig: {
      target_average_tps: initialScenario?.goalConfig?.target_average_tps,
      p95_latency_ms: initialScenario?.goalConfig?.p95_latency_ms,
      max_error_pct: initialScenario?.goalConfig?.max_error_pct,
      min_vusers: initialScenario?.goalConfig?.min_vusers,
      max_vusers: initialScenario?.goalConfig?.max_vusers,
      stop_on_sla_breach: initialScenario?.goalConfig?.stop_on_sla_breach || false,
      sla_breach_window: initialScenario?.goalConfig?.sla_breach_window || '30s',
    },
    runtimeSettings: {
      iterations: initialScenario?.runtimeSettings?.iterations,
      log_level: initialScenario?.runtimeSettings?.log_level || '',
      auto_transactions: initialScenario?.runtimeSettings?.auto_transactions || '',
      snapshot_on_error: initialScenario?.runtimeSettings?.snapshot_on_error || false,
    },
    groups: initialScenario?.groups.length ? initialScenario.groups : [buildDefaultScenarioGroup(scripts)],
  }
}

export function buildScenarioDraftFromTest(test: TestSummary): ScenarioDraft {
  const groups = test.groups?.length
    ? test.groups.map((group) => ({
      name: group.name,
      scriptId: group.script_id,
      vusers: group.vusers,
      rampUp: group.ramp_up || '',
      rampDown: group.ramp_down || '',
      initialization: group.initialization || 'at_runtime',
    }))
    : [{
      name: '',
      scriptId: test.script_id || '',
      vusers: test.total_vusers || 10,
      rampUp: '',
      rampDown: '',
      initialization: 'at_runtime' as const,
    }]

  return {
    name: test.name,
    duration: test.duration,
    scenarioMode: test.scenario_mode || 'vuser_driven',
    scriptShakeout: Boolean(test.script_shakeout),
    resourceBaseline: Boolean(test.resource_baseline),
    goalConfig: {
      target_average_tps: test.goal_config?.target_average_tps,
      p95_latency_ms: test.goal_config?.p95_latency_ms,
      max_error_pct: test.goal_config?.max_error_pct,
      min_vusers: test.goal_config?.min_vusers,
      max_vusers: test.goal_config?.max_vusers,
      stop_on_sla_breach: test.goal_config?.stop_on_sla_breach,
      sla_breach_window: test.goal_config?.sla_breach_window || '',
    },
    runtimeSettings: {
      iterations: test.runtime_settings?.iterations,
      log_level: test.runtime_settings?.log_level || '',
      auto_transactions: test.runtime_settings?.auto_transactions || '',
      snapshot_on_error: Boolean(test.runtime_settings?.error_handling?.snapshot_on_error),
    },
    groups,
  }
}