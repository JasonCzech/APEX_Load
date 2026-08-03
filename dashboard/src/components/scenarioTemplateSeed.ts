import type { LoadModelTemplate } from '../types'
import type { ScenarioSeed } from './scenarioWizardUtils'

/** Convert a stored workload template into the canonical builder seed while
 * retaining its complete runtime profile for the advanced editors. */
export function scenarioSeedFromTemplate(template: LoadModelTemplate): ScenarioSeed {
  const runtime = template.scenario.runtime_settings
  const goal = template.scenario.goal_config
  return {
    duration: template.scenario.duration,
    scenario_mode: template.scenario.scenario_mode || 'vuser_driven',
    script_shakeout: template.scenario.script_shakeout,
    resource_baseline: template.scenario.resource_baseline,
    groups: template.scenario.groups,
    goal: goal ? { ...goal } : undefined,
    pacing: runtime?.pacing ? { ...runtime.pacing } as ScenarioSeed['pacing'] : undefined,
    think_time: runtime?.think_time ? { ...runtime.think_time } : undefined,
    runtime_settings: runtime ? structuredClone(runtime) : undefined,
    iterations: runtime?.run_logic?.iterations ?? runtime?.iterations,
    log_level: runtime?.log?.level ?? runtime?.log_level,
    auto_transactions: runtime?.run_logic?.auto_transactions ?? runtime?.auto_transactions,
    continue_on_error: runtime?.error_handling?.continue_on_error,
    snapshot_on_error: runtime?.error_handling?.snapshot_on_error,
  }
}
