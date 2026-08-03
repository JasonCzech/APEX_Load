import { useMemo } from 'react'

import { TestRunManifest, TestSummary } from '../types'
import { buildQueuedScenarioPreview, buildQueuedScenarioRows, QueuedScenarioPreview, QueuedScenarioRow } from './testExecutionScenarioModel'

export type ScenarioSnapshotSource = 'immutable_manifest' | 'stored_manifest' | 'retained_manifest' | 'summary'

interface ExecutionScenarioPreviewModel {
  scenarioSnapshot: TestSummary | null
  snapshotSource: ScenarioSnapshotSource
  queuedScenarioPreview: QueuedScenarioPreview
  queuedScenarioRows: QueuedScenarioRow[]
  snapshotLoading: boolean
  snapshotError: string
}

export function buildScenarioSnapshot(test: TestSummary | null, manifest: TestRunManifest | null): TestSummary | null {
  if (!test || !manifest) return test

  const initial = manifest.initial_configuration
  return {
    ...test,
    name: manifest.test_name || test.name,
    application_id: manifest.identity.application_id ?? test.application_id,
    release_id: manifest.identity.release_id ?? test.release_id,
    scenario_id: manifest.identity.scenario_id ?? test.scenario_id,
    project_id: manifest.identity.project_id ?? test.project_id,
    folder_id: manifest.identity.folder_id ?? test.folder_id,
    created_at: manifest.identity.created_at || test.created_at,
    duration: initial.duration ?? test.duration,
    total_vusers: initial.total_vusers ?? test.total_vusers,
    start_delay: initial.start_delay ?? test.start_delay,
    scenario_mode: initial.scenario_mode ?? test.scenario_mode,
    script_shakeout: initial.script_shakeout ?? test.script_shakeout,
    resource_baseline: initial.resource_baseline ?? test.resource_baseline,
    assignment: initial.assignment ?? test.assignment,
    goal_config: initial.goal_config ?? test.goal_config,
    runtime_settings: initial.runtime_settings ?? test.runtime_settings,
    groups: initial.groups ?? test.groups,
    // Operational state remains live so start blockers and queue status are
    // accurate. Configuration comes from the manifest when available, while
    // initial_configuration_provenance determines whether it is immutable.
    status: test.status,
  }
}

export function useExecutionScenarioPreviewModel(
  test: TestSummary | null,
  manifest: TestRunManifest | null,
  manifestLoading: boolean,
  manifestError: string,
): ExecutionScenarioPreviewModel {
  const scenarioSnapshot = useMemo(() => buildScenarioSnapshot(test, manifest), [manifest, test])
  const queuedScenarioRows = useMemo(() => buildQueuedScenarioRows(scenarioSnapshot, {}), [scenarioSnapshot])
  const queuedScenarioPreview = useMemo(() => buildQueuedScenarioPreview(scenarioSnapshot), [scenarioSnapshot])

  const snapshotSource: ScenarioSnapshotSource = !manifest
    ? 'summary'
    : manifest.initial_configuration_provenance?.immutable === true
      ? 'immutable_manifest'
      : manifest.initial_configuration_provenance?.source === 'stored_test_record'
        ? 'stored_manifest'
        : 'retained_manifest'

  return {
    scenarioSnapshot,
    snapshotSource,
    queuedScenarioPreview,
    queuedScenarioRows,
    snapshotLoading: manifestLoading,
    snapshotError: manifestError,
  }
}
