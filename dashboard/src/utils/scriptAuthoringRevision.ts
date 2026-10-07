import type { JsonSuccessResponseBody } from '../types/generated/openapi'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline, ScriptAuthoringProjection, StoredScript } from '../types/storedContent'
import { AuthoringSaveOutcomeUnknown, authoringChangedPaths as changedPaths, authoringSaveNeedsReconciliation, authoringTagFor, isAuthoringRecord as record, isAuthoringRevision, reapplyAuthoringDraft } from './authoringRevision'

type AuthoringTransport = Extract<JsonSuccessResponseBody<'handleGetScript', '200'>, { projection_version: 'apexload.script_authoring.v1' }>
const version: AuthoringTransport['projection_version'] = 'apexload.script_authoring.v1'
const unsupported = 'This controller did not return a supported paired authoring revision. Reload using a compatible controller; this draft cannot be saved without revision protection.'

export function authoringTag(revision: string): string {
  return authoringTagFor('script', revision, unsupported)
}

export function admitScriptAuthoringBaseline(value: unknown, etag: string | null, id: string, projectId?: string): ScriptAuthoringBaseline {
  if (!record(value) || value.projection_version !== version || value.id !== id
    || typeof value.project_id !== 'string' || !value.project_id || (projectId !== undefined && value.project_id !== projectId)
    || !isAuthoringRevision('script', value.content_revision)
    || etag !== authoringTag(value.content_revision)
    || !(value.folder_id === null || typeof value.folder_id === 'string')
    || !record(value.library_placement)
    || !(value.library_placement.application_id === null || typeof value.library_placement.application_id === 'string')
    || !Array.isArray(value.library_placement.release_ids) || value.library_placement.release_ids.some(id => typeof id !== 'string')
    || !record(value.script) || value.script.id !== id || value.script.project_id !== value.project_id
    || typeof value.script.name !== 'string' || typeof value.script.version !== 'string' || typeof value.script.protocol !== 'string'
    || !record(value.script.config) || typeof value.script.config.base_url !== 'string' || !Array.isArray(value.script.actions)
    || (value.script.folder_id ?? null) !== value.folder_id) {
    throw new Error(unsupported)
  }
  // Clone both halves together so parent/draft mutation cannot alter authority.
  return { projection: JSON.parse(JSON.stringify(value)) as ScriptAuthoringProjection, etag }
}

export function baselineFromSavedScript(saved: StoredScript, projectId?: string): ScriptAuthoringBaseline {
  const projection = saved.authoring_projection
  if (!projection || saved.content_revision !== projection.content_revision) throw new ScriptSaveOutcomeUnknown()
  try {
    return admitScriptAuthoringBaseline(projection, authoringTag(projection.content_revision), saved.id, projectId)
  } catch {
    throw new ScriptSaveOutcomeUnknown()
  }
}

export class ScriptSaveOutcomeUnknown extends AuthoringSaveOutcomeUnknown {
  constructor() {
    super('script', 'ScriptSaveOutcomeUnknown')
  }
}

export function scriptSaveNeedsReconciliation(error: unknown): boolean {
  return authoringSaveNeedsReconciliation(error, ['script_save_outcome_unknown', 'script_save_outcome_audit_unavailable'])
}

// Read-only server clocks are not authored content. Some older pure helpers
// serialize zero time values even when the submitted paired draft omits them.
export function withoutScriptAuthoringReadContext(script: ScriptDefinition): ScriptDefinition {
  if (!record(script) || typeof script.id !== 'string' || typeof script.project_id !== 'string'
    || typeof script.name !== 'string' || typeof script.version !== 'string' || typeof script.protocol !== 'string'
    || !record(script.config) || !Array.isArray(script.actions)) {
    throw new Error('The helper did not return a supported authored script draft. Your original draft is retained.')
  }
  const authored = { ...script }
  delete authored.created_at
  delete authored.updated_at
  delete authored.archived_at
  return authored
}

// Ownership/placement belongs to the newly reviewed baseline, not the old draft.
export function reapplyScriptDraft(draft: ScriptDefinition, baseline: ScriptAuthoringBaseline, original?: ScriptDefinition): ScriptDefinition {
  const reapplied = original ? reapplyAuthoringDraft(original, draft, baseline.projection.script) : draft
  return { ...reapplied, id: baseline.projection.id, project_id: baseline.projection.project_id, folder_id: baseline.projection.folder_id ?? undefined }
}

// Diff only bounded field paths, never credential or draft values.
export function authoringChangedPaths(before: unknown, after: unknown): string[] {
  return changedPaths(before, after, 'Script')
}
