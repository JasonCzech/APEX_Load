import type { ApiServices } from '../hooks/ApiProvider'
import { ApiError } from '../hooks/apiClient'
import type { ScenarioAuthoringBaseline } from '../types/storedContent'
import type { ScenarioDraftSave } from '../types/scenarioDrafts'
import { scenarioBaselineFromRead } from '../utils/scenarioAuthoringRevision'
import type { ScenarioAuthoringDraft } from './scenarioAuthoringDraft'

type ScenarioPersistence = Pick<ApiServices, 'createScenario' | 'updateScenario' | 'getScenario' | 'createScenarioDraft' | 'updateScenarioDraft'>

/** Persists one reviewed scenario snapshot; publication remains scope-owned. */
export async function persistScenarioAuthoringDraft(
  input: {
    editingId: string | null
    projectId?: string
    payload: Parameters<ScenarioPersistence['createScenario']>[0]
    baseline: ScenarioAuthoringBaseline | null
  },
  ports: { api: Pick<ScenarioPersistence, 'createScenario' | 'updateScenario'>; isCurrent: () => boolean },
) {
  if (input.editingId && !input.baseline) throw new Error('A current scenario baseline is required before saving this draft.')
  const saved = input.editingId
    ? await ports.api.updateScenario(input.editingId, input.payload, input.baseline!)
    : await ports.api.createScenario(input.payload)
  if (!ports.isCurrent()) return { kind: 'superseded' } as const
  if (!saved?.id) return { kind: 'incomplete' } as const
  let baseline: ScenarioAuthoringBaseline | null = null
  try { baseline = scenarioBaselineFromRead(saved, input.projectId) } catch { /* Legacy create responses may lack a paired baseline. */ }
  return { kind: 'saved', saved, baseline } as const
}

/** A deleted project draft may be recreated; a conflicting revision never is. */
export async function persistScenarioProjectDraft(
  projectId: string,
  current: { id: string; revision: number } | null,
  document: ScenarioDraftSave,
  api: Pick<ScenarioPersistence, 'updateScenarioDraft' | 'createScenarioDraft'>,
) {
  if (current) {
    try { return await api.updateScenarioDraft(projectId, current.id, { ...document, revision: current.revision }) }
    catch (cause) { if (!(cause instanceof ApiError && cause.status === 404)) throw cause }
  }
  return api.createScenarioDraft(projectId, document)
}

/** Restoring a checkpoint never silently adopts a newer durable revision. */
export async function restoreScenarioDraftBaseline(
  id: string,
  projectId: string | undefined,
  draft: Pick<ScenarioAuthoringDraft, 'baselineTag' | 'editingUpdatedAt'>,
  ports: { api: Pick<ScenarioPersistence, 'getScenario'>; isCurrent: () => boolean },
) {
  const fresh = await ports.api.getScenario(id)
  if (!ports.isCurrent()) return { kind: 'superseded' } as const
  const baseline = fresh?.id === id ? scenarioBaselineFromRead(fresh, projectId) : null
  const unchanged = baseline && (draft.baselineTag ? baseline.etag === draft.baselineTag : fresh.updated_at === draft.editingUpdatedAt)
  return unchanged ? { kind: 'baseline', baseline } as const : { kind: 'changed' } as const
}
