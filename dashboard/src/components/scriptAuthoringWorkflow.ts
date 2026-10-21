import type { ApiServices } from '../hooks/ApiProvider'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptApprovalQueued, ScriptAuthoringBaseline, StoredScript } from '../types/storedContent'
import { baselineFromSavedScript } from '../utils/scriptAuthoringRevision'
import { preserveMappingBindings } from './sourceMappingReviewState'
import { authoringSessionIdempotencyKey, scriptRequiresJavaVuserAuthoringApproval } from './javaVuserApprovalGate'
import { collectAuthoringIssues } from './scriptAuthoringAssist'
import { authoringPayloadDigest, nextAuthoringCreateIntent, sendUnderAuthoringCreateIntent, startingAuthoringCreateIntent, type AuthoringCreateIntent } from './authoringCreateIntent'

type ScriptPersistence = Pick<ApiServices, 'createAuthoringSession' | 'updateScript' | 'createScriptKeyed' | 'getScript' | 'getScriptAuthoring'>

/** The editor's draft-owned create intent: read before a create, replaced after it. */
export interface ScriptCreateIntentOwner {
  current: AuthoringCreateIntent | null
  update: (next: AuthoringCreateIntent | null) => void
  // fresh: an explicit new intent (Save as new) starts a new key.
  fresh?: boolean
}

type ScriptSaveResult =
  | { kind: 'superseded'; saved: boolean }
  | { kind: 'invalid' }
  | { kind: 'approval'; script: ScriptDefinition; name: string; approval: ScriptApprovalQueued }
  // replayed: an earlier attempt with this draft's intent created the script;
  // stored and baseline are its current state, read after the replay.
  | { kind: 'saved'; script: ScriptDefinition; stored: StoredScript; baseline: ScriptAuthoringBaseline | null; definition: ScriptDefinition; replayed?: boolean }

/** Owns persistence routing and admission; the editor owns draft publication. */
export async function persistScriptAuthoringDraft(
  input: {
    scriptId?: string | null
    projectId?: string
    folderId?: string | null
    baseline: ScriptAuthoringBaseline | null
    draft: ScriptDefinition
    importBaseline: ScriptDefinition | null
    createIntent: ScriptCreateIntentOwner
  },
  ports: { api: ScriptPersistence; isCurrent: () => boolean },
): Promise<ScriptSaveResult> {
  const script = await preserveMappingBindings(input.draft, input.importBaseline)
  if (!ports.isCurrent()) return { kind: 'superseded', saved: false }
  if (!input.scriptId && scriptRequiresJavaVuserAuthoringApproval(script)) {
    const session = await ports.api.createAuthoringSession({
      project_id: input.projectId,
      folder_id: input.folderId,
      source: 'manual',
      name: script.name || 'Java Vuser Draft',
      status: 'reviewing',
      draft: script,
      idempotency_key: authoringSessionIdempotencyKey('script-editor-java-vuser', script),
    })
    if (!ports.isCurrent()) return { kind: 'superseded', saved: true }
    if (!session.id?.trim()) throw new Error('The approval queue did not return a session identity. Your draft is retained; reconcile the authoring sessions before retrying.')
    return { kind: 'approval', script, name: session.name, approval: {
      sessionId: session.id, name: session.name || script.name,
      projectId: input.projectId || '', folderId: input.folderId || null,
    } }
  }
  if (collectAuthoringIssues(script).some(issue => issue.severity === 'error')) return { kind: 'invalid' }
  // An edit cannot become a create or use an unpaired baseline.
  if (input.scriptId && !input.baseline) throw new Error('A current authoring baseline is required before saving this script.')
  if (input.scriptId) {
    const stored = await ports.api.updateScript(input.scriptId, script, input.baseline!)
    if (!ports.isCurrent()) return { kind: 'superseded', saved: true }
    const baseline = baselineFromSavedScript(stored, input.projectId)
    return { kind: 'saved', script, stored, baseline, definition: baseline.projection.script || stored.script || script }
  }
  return createScriptUnderIntent(script, input, ports)
}

async function createScriptUnderIntent(
  script: ScriptDefinition,
  input: { projectId?: string; folderId?: string | null; createIntent: ScriptCreateIntentOwner },
  ports: { api: ScriptPersistence; isCurrent: () => boolean },
): Promise<ScriptSaveResult> {
  const projectId = input.projectId || ''
  const digest = authoringPayloadDigest({ script, project_id: input.projectId, folder_id: input.folderId })
  const intent = nextAuthoringCreateIntent(startingAuthoringCreateIntent(input.createIntent.current, 'script_create', projectId, digest, Date.now(), input.createIntent.fresh), 'script_create', projectId, digest)
  return sendUnderAuthoringCreateIntent(intent, input.createIntent, 'script',
    key => ports.api.createScriptKeyed(script, input.projectId, input.folderId, key),
    async (outcome): Promise<{ done: boolean; value: ScriptSaveResult }> => {
      let saved: ScriptSaveResult
      if (outcome.kind === 'replayed-original') {
        // The original outcome names the script; edit from its current state.
        const id = outcome.outcome.resource_id
        const [stored, baseline] = await Promise.all([ports.api.getScript(id), ports.api.getScriptAuthoring(id, input.projectId)])
        saved = { kind: 'saved', script, stored, baseline, definition: baseline.projection.script || stored.script || script, replayed: true }
      } else {
        const stored = outcome.resource
        const baseline = stored.authoring_projection ? baselineFromSavedScript(stored, input.projectId) : null
        saved = { kind: 'saved', script, stored, baseline, definition: baseline?.projection.script || stored.script || script, ...(outcome.kind === 'replayed' ? { replayed: true } : {}) }
      }
      if (!ports.isCurrent()) return { done: false, value: { kind: 'superseded', saved: true } }
      return { done: true, value: saved }
    })
}
