import type { TestRunManifest } from '../../types'
import type { TestGroup } from '../../types/testConfiguration'
import type { EffectiveRuntimeGroupSource, StoredRuntimeRow } from './effectiveRuntimeSummaryModel'

// Where the read-only runtime views take their override rows from. A run's
// group rows and its run-level row are read from ONE configuration snapshot,
// so a group row is never laid over a run-level row from a different moment.

export interface EffectiveRuntimeSources {
  groups: EffectiveRuntimeGroupSource[]
  scenarioRow?: StoredRuntimeRow
  /** True when the rows come from the run's immutable initial configuration (what started). */
  immutable: boolean
}

/** The group sources of stored test groups (a run snapshot, manifest configuration or archive provenance). */
export function effectiveRuntimeGroupSources(groups: readonly TestGroup[] | undefined): EffectiveRuntimeGroupSource[] {
  return (groups ?? []).map(group => ({
    name: group.name,
    protocol: group.protocol,
    row: group.runtime_settings,
    iterations: group.iterations,
    iterationOverride: group.iteration_override,
    iterationOverrideKnown: group.iteration_override_known,
    continueOnError: group.continue_on_error,
  }))
}

/**
 * The manifest's rows: its immutable initial configuration when preserved (what the run started
 * with), otherwise its current configuration, with the manifest's script list standing in for an
 * older configuration that carries no groups.
 */
export function effectiveRuntimeSourcesFromManifest(manifest: TestRunManifest): EffectiveRuntimeSources {
  const immutable = manifest.initial_configuration_provenance?.immutable === true
  const config = immutable ? manifest.initial_configuration : manifest.current_configuration
  const groups = config?.groups && config.groups.length > 0
    ? effectiveRuntimeGroupSources(config.groups)
    : (manifest.scripts ?? []).map(script => ({
      name: script.group_name,
      protocol: script.protocol,
      row: script.runtime_settings,
      iterations: script.iterations,
      continueOnError: script.continue_on_error,
    }))
  return { groups, scenarioRow: config?.runtime_settings, immutable }
}
