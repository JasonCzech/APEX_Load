import type { ScenarioAuthoringBaseline, ScenarioAuthoringDocument, StoredScenario } from '../types/storedContent'

// scenarioAuthoringFixture is a paired baseline (projection + strong tag) for
// the scenario as given; revisionChar distinguishes revisions.
export function scenarioAuthoringFixture(scenario: Partial<StoredScenario> = {}, id = scenario.id || 'scenario-1', projectId = scenario.project_id || 'project-1', revisionChar = 'a'): ScenarioAuthoringBaseline {
  const revision = `scn-v1-${revisionChar.repeat(64)}`
  const document: ScenarioAuthoringDocument = {
    id, project_id: projectId, name: scenario.name ?? 'Fixture scenario', duration: scenario.duration ?? '1m',
    ...(scenario.workload_shape_id ? { workload_shape_id: scenario.workload_shape_id } : {}),
    ...(scenario.telemetry_acceptance_policy_id ? { telemetry_acceptance_policy_id: scenario.telemetry_acceptance_policy_id } : {}),
    ...(scenario.telemetry_budget_policy_id ? { telemetry_budget_policy_id: scenario.telemetry_budget_policy_id } : {}),
    groups: (scenario.scripts || []).map(group => ({ name: group.name || '', script_id: group.script_id, vusers: group.vusers, ...(group.ramp_up ? { ramp_up: group.ramp_up } : {}) })),
    data_pools: (scenario.data_pools || []).map(binding => ({ id: binding.id || '', pool_id: binding.pool_id, alias: binding.alias, access_mode: binding.access_mode, selection_strategy: binding.selection_strategy, ...(binding.live_options ? { live_options: structuredClone(binding.live_options) } : {}) })),
  }
  return { etag: `"scenario-authoring-v1:${revision}"`, projection: {
    projection_version: 'apexload.scenario_authoring.v1', id, project_id: projectId, content_revision: revision,
    scenario: document, library_placement: { application_id: null, release_ids: [] }, monitor_selection: { sources: [] },
  } }
}

// withScenarioAuthoring is the scenario as a durable read or save returns it:
// the body plus its content revision and authoring projection.
export function withScenarioAuthoring<T extends Partial<StoredScenario> & { id: string }>(scenario: T, revisionChar = 'a', projectId = scenario.project_id || 'project-1'): T & Pick<StoredScenario, 'content_revision' | 'authoring_projection'> {
  const baseline = scenarioAuthoringFixture(scenario, scenario.id, projectId, revisionChar)
  return { ...scenario, content_revision: baseline.projection.content_revision, authoring_projection: baseline.projection }
}
