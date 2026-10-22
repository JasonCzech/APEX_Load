import type { ApiServices } from '../hooks/ApiProvider'
import { ApiError } from '../hooks/apiClient'
import type { ScenarioAuthoringBaseline, StoredScenario } from '../types/storedContent'
import type { ScenarioDraftSave } from '../types/scenarioDrafts'
import { scenarioBaselineFromRead } from '../utils/scenarioAuthoringRevision'
import type { ScenarioAuthoringDraft } from './scenarioAuthoringDraft'
import { authoringPayloadDigest, nextAuthoringCreateIntent, sendUnderAuthoringCreateIntent, startingAuthoringCreateIntent, type AuthoringCreateIntent } from './authoringCreateIntent'

type ScenarioPersistence = Pick<ApiServices, 'createScenarioKeyed' | 'updateScenario' | 'getScenario' | 'createScenarioDraft' | 'updateScenarioDraft'>

/** The builder's draft-owned create intent: read before a create, replaced after it. */
export interface ScenarioCreateIntentOwner {
  current: AuthoringCreateIntent | null
  update: (next: AuthoringCreateIntent | null) => void
  // fresh: an explicit new intent (Save as new) starts a new key.
  fresh?: boolean
}

/**
 * Persists one reviewed scenario snapshot; publication remains scope-owned.
 * (Not named "…ScenarioAuthoring…": see readScenarioDraftCheckpoint.)
 */
export async function persistBuilderScenario(
  input: {
    editingId: string | null
    projectId?: string
    payload: Parameters<ScenarioPersistence['createScenarioKeyed']>[0]
    baseline: ScenarioAuthoringBaseline | null
    createIntent: ScenarioCreateIntentOwner
  },
  ports: { api: Pick<ScenarioPersistence, 'createScenarioKeyed' | 'updateScenario' | 'getScenario'>; isCurrent: () => boolean },
) {
  if (input.editingId && !input.baseline) throw new Error('A current scenario baseline is required before saving this draft.')
  if (input.editingId) {
    const saved = await ports.api.updateScenario(input.editingId, input.payload, input.baseline!)
    if (!ports.isCurrent()) return { kind: 'superseded' } as const
    return pairedScenarioSave(saved, input.projectId, false)
  }
  const projectId = input.projectId || ''
  const digest = authoringPayloadDigest(input.payload)
  const intent = nextAuthoringCreateIntent(startingAuthoringCreateIntent(input.createIntent.current, 'scenario_create', projectId, digest, Date.now(), input.createIntent.fresh), 'scenario_create', projectId, digest)
  return sendUnderAuthoringCreateIntent(intent, input.createIntent, 'scenario',
    key => ports.api.createScenarioKeyed(input.payload, key),
    async outcome => {
      // The original outcome names the scenario; edit from its current state.
      const saved = outcome.kind === 'replayed-original' ? await ports.api.getScenario(outcome.outcome.resource_id) : outcome.resource
      const value = !saved?.id ? { kind: 'incomplete' } as const
        : !ports.isCurrent() ? { kind: 'superseded' } as const
          : pairedScenarioSave(saved, input.projectId, outcome.kind !== 'created')
      return { done: value.kind === 'saved', value }
    })
}

function pairedScenarioSave(saved: StoredScenario, projectId: string | undefined, replayed: boolean) {
  if (!saved?.id) return { kind: 'incomplete' } as const
  let baseline: ScenarioAuthoringBaseline | null = null
  try { baseline = scenarioBaselineFromRead(saved, projectId) } catch { /* Legacy create responses may lack a paired baseline. */ }
  return { kind: 'saved', saved, baseline, replayed } as const
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
