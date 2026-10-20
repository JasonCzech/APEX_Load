import type { ApiServices } from '../hooks/ApiProvider'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptApprovalQueued, ScriptAuthoringBaseline, StoredScript } from '../types/storedContent'
import { baselineFromSavedScript } from '../utils/scriptAuthoringRevision'
import { preserveMappingBindings } from './sourceMappingReviewState'
import { authoringSessionIdempotencyKey, scriptRequiresJavaVuserAuthoringApproval } from './javaVuserApprovalGate'
import { collectAuthoringIssues } from './scriptAuthoringAssist'

type ScriptPersistence = Pick<ApiServices, 'createAuthoringSession' | 'updateScript' | 'createScript'>

type ScriptSaveResult =
  | { kind: 'superseded'; saved: boolean }
  | { kind: 'invalid' }
  | { kind: 'approval'; script: ScriptDefinition; name: string; approval: ScriptApprovalQueued }
  | { kind: 'saved'; script: ScriptDefinition; stored: StoredScript; baseline: ScriptAuthoringBaseline | null; definition: ScriptDefinition }

/** Owns persistence routing and admission; the editor owns draft publication. */
export async function persistScriptAuthoringDraft(
  input: {
    scriptId?: string | null
    projectId?: string
    folderId?: string | null
    baseline: ScriptAuthoringBaseline | null
    draft: ScriptDefinition
    importBaseline: ScriptDefinition | null
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
  const stored = input.scriptId
    ? await ports.api.updateScript(input.scriptId, script, input.baseline!)
    : await ports.api.createScript(script, input.projectId, input.folderId)
  if (!ports.isCurrent()) return { kind: 'superseded', saved: true }
  const baseline = input.scriptId || stored.authoring_projection ? baselineFromSavedScript(stored, input.projectId) : null
  return { kind: 'saved', script, stored, baseline, definition: baseline?.projection.script || stored.script || script }
}
