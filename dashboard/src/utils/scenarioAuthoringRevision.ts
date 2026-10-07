import type { JsonSuccessResponseBody } from '../types/generated/openapi'
import type { ScenarioAuthoringBaseline, ScenarioAuthoringProjection, StoredScenario } from '../types/storedContent'
import { AuthoringSaveOutcomeUnknown, authoringSaveNeedsReconciliation, authoringTagFor, isAuthoringRecord as record, isAuthoringRevision } from './authoringRevision'

type AuthoringTransport = Extract<JsonSuccessResponseBody<'handleGetScenario', '200'>, { projection_version: 'apexload.scenario_authoring.v1' }>
const version: AuthoringTransport['projection_version'] = 'apexload.scenario_authoring.v1'
export const scenarioRevisionUnsupported = 'This controller did not return a supported scenario revision. Load the current baseline before saving; this draft cannot be saved without revision protection.'

export function scenarioAuthoringTag(revision: string): string {
  return authoringTagFor('scenario', revision, scenarioRevisionUnsupported)
}

const strings = (value: unknown) => Array.isArray(value) && value.every(item => typeof item === 'string')

// admitScenarioAuthoringBaseline accepts only a complete projection whose
// identity matches the scenario being edited and whose tag is the one the
// controller derives from its revision.
export function admitScenarioAuthoringBaseline(value: unknown, etag: string | null, id: string, projectId?: string): ScenarioAuthoringBaseline {
  if (!record(value) || value.projection_version !== version || value.id !== id
    || typeof value.project_id !== 'string' || !value.project_id || (projectId !== undefined && value.project_id !== projectId)
    || !isAuthoringRevision('scenario', value.content_revision)
    || etag !== scenarioAuthoringTag(value.content_revision)
    || !record(value.library_placement)
    || !(value.library_placement.application_id === null || typeof value.library_placement.application_id === 'string')
    || !strings(value.library_placement.release_ids)
    || !record(value.monitor_selection) || !Array.isArray(value.monitor_selection.sources)
    || value.monitor_selection.sources.some(source => !record(source) || typeof source.source_id !== 'string' || typeof source.required !== 'boolean')
    || !record(value.scenario) || value.scenario.id !== id || value.scenario.project_id !== value.project_id
    || typeof value.scenario.name !== 'string' || typeof value.scenario.duration !== 'string'
    || !Array.isArray(value.scenario.groups)
    || value.scenario.groups.some(group => !record(group) || typeof group.script_id !== 'string' || typeof group.vusers !== 'number')
    || !Array.isArray(value.scenario.data_pools)
    || value.scenario.data_pools.some(binding => !record(binding) || typeof binding.pool_id !== 'string' || typeof binding.alias !== 'string')) {
    throw new Error(scenarioRevisionUnsupported)
  }
  // Clone both halves together so parent/draft mutation cannot alter authority.
  return { projection: JSON.parse(JSON.stringify(value)) as ScenarioAuthoringProjection, etag }
}

// scenarioBaselineFromRead is the baseline a scenario read carries: the
// controller attaches content_revision and authoring_projection only when the
// body is the durable state they describe.
export function scenarioBaselineFromRead(scenario: StoredScenario, projectId?: string): ScenarioAuthoringBaseline {
  const projection = scenario?.authoring_projection
  if (!projection || scenario.content_revision !== projection.content_revision) throw new Error(scenarioRevisionUnsupported)
  return admitScenarioAuthoringBaseline(projection, scenarioAuthoringTag(projection.content_revision), scenario.id, projectId)
}

// baselineFromSavedScenario is the committed baseline a save returned; a save
// response without one is an unverified outcome.
export function baselineFromSavedScenario(saved: StoredScenario, projectId?: string): ScenarioAuthoringBaseline {
  try {
    return scenarioBaselineFromRead(saved, projectId)
  } catch {
    throw new ScenarioSaveOutcomeUnknown()
  }
}

export class ScenarioSaveOutcomeUnknown extends AuthoringSaveOutcomeUnknown {
  constructor() {
    super('scenario', 'ScenarioSaveOutcomeUnknown')
  }
}

export function scenarioSaveNeedsReconciliation(error: unknown): boolean {
  return authoringSaveNeedsReconciliation(error, ['scenario_save_outcome_unknown', 'scenario_save_outcome_audit_unavailable'])
}

// ScenarioChangedSinceObserved refuses a write whose confirmation was made
// against a scenario the controller has since changed.
export class ScenarioChangedSinceObserved extends Error {
  constructor() {
    super('This scenario changed after the list was loaded. Refresh and review it before trying again.')
    this.name = 'ScenarioChangedSinceObserved'
  }
}

// adoptableAfterBindingsSave is the baseline an editor may adopt after its own
// monitor-binding save advanced the scenario revision: only when the authored
// scenario and its library placement are unchanged (the binding save is the
// only change), otherwise the next save reconciles.
export function adoptableAfterBindingsSave(previous: ScenarioAuthoringBaseline, next: ScenarioAuthoringBaseline): ScenarioAuthoringBaseline | null {
  const authored = (baseline: ScenarioAuthoringBaseline) => JSON.stringify([baseline.projection.scenario, baseline.projection.library_placement])
  return authored(next) === authored(previous) ? next : null
}
