import type { JsonSuccessResponseBody } from '../types/generated/openapi'
import { ApiError } from '../hooks/apiClient'
import type { ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline, ScriptAuthoringProjection, StoredScript } from '../types/storedContent'

const revisionPattern = /^scr-v1-[a-f0-9]{64}$/
type AuthoringTransport = Extract<JsonSuccessResponseBody<'handleGetScript', '200'>, { projection_version: 'apexload.script_authoring.v1' }>
const version: AuthoringTransport['projection_version'] = 'apexload.script_authoring.v1'
const unsupported = 'This controller did not return a supported paired authoring revision. Reload using a compatible controller; this draft cannot be saved without revision protection.'

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export function authoringTag(revision: string): string {
  if (!revisionPattern.test(revision)) throw new Error(unsupported)
  return `"script-authoring-v1:${revision}"`
}

export function admitScriptAuthoringBaseline(value: unknown, etag: string | null, id: string, projectId?: string): ScriptAuthoringBaseline {
  if (!record(value) || value.projection_version !== version || value.id !== id
    || typeof value.project_id !== 'string' || !value.project_id || (projectId !== undefined && value.project_id !== projectId)
    || typeof value.content_revision !== 'string' || !revisionPattern.test(value.content_revision)
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

export class ScriptSaveOutcomeUnknown extends Error {
  constructor() {
    super('The save outcome could not be verified. Your draft is retained. Reload the current baseline before reviewing and reapplying it; do not retry this save automatically.')
    this.name = 'ScriptSaveOutcomeUnknown'
  }
}

export function scriptSaveNeedsReconciliation(error: unknown): boolean {
  return !(error instanceof ApiError) || error.status === 412 || error.status === 428 || error.status >= 500
    || error.code === 'script_save_outcome_unknown' || error.code === 'script_save_outcome_audit_unavailable'
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
  function equal(a: unknown, b: unknown): boolean { return JSON.stringify(a) === JSON.stringify(b) }
  let visited = 0
  function merge(old: unknown, local: unknown, current: unknown, depth = 0): unknown {
    if (++visited > 50_000 || depth > 64) throw new Error('Draft reapply exceeds the bounded review limit. Simplify the draft before reapplying.')
    if (equal(old, local)) return current
    if (record(old) && record(local) && record(current)) {
      const result: Record<string, unknown> = { ...current }
      const hasOwn = (value: Record<string, unknown>, key: string) => Object.prototype.hasOwnProperty.call(value, key)
      const ownValue = (value: Record<string, unknown>, key: string) => hasOwn(value, key) ? value[key] : undefined
      for (const key of new Set([...Object.keys(old), ...Object.keys(local)])) {
        if (equal(ownValue(old, key), ownValue(local, key))) continue
        if (!hasOwn(local, key)) delete result[key]
        // JSON keys are data, including __proto__ and inherited property names.
        else Object.defineProperty(result, key, {
          value: merge(ownValue(old, key), ownValue(local, key), ownValue(current, key), depth + 1),
          enumerable: true, configurable: true, writable: true,
        })
      }
      return result
    }
    return local
  }
  const reapplied = original ? merge(original, draft, baseline.projection.script) as ScriptDefinition : draft
  return { ...reapplied, id: baseline.projection.id, project_id: baseline.projection.project_id, folder_id: baseline.projection.folder_id ?? undefined }
}

// Diff only bounded field paths, never credential or draft values.
export function authoringChangedPaths(before: unknown, after: unknown): string[] {
  const paths: string[] = []
  let visited = 0
  function visit(a: unknown, b: unknown, path: string, depth: number) {
    if (Object.is(a, b)) return
    if (++visited > 10_000 || paths.length >= 50 || depth > 20) {
      if (!paths.includes('Additional changes (bounded preview)')) paths.push('Additional changes (bounded preview)')
      return
    }
    if (Array.isArray(a) && Array.isArray(b)) {
      for (let i = 0; i < Math.max(a.length, b.length); i++) visit(a[i], b[i], `${path}[${i}]`, depth + 1)
    } else if (record(a) && record(b)) {
      for (const key of [...new Set([...Object.keys(a), ...Object.keys(b)])].sort()) visit(a[key], b[key], path ? `${path}.${key.slice(0, 240)}` : key.slice(0, 240), depth + 1)
    } else {
      paths.push((path || 'Script').slice(0, 240))
    }
  }
  visit(before, after, '', 0)
  return paths
}
