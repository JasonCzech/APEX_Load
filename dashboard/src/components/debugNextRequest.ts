import type { ScriptDebugAction } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { phaseActionsForScript, resolveNestedAction } from './scriptEditorJsonModel'

/**
 * One piece of a template as the paused debugger previews it: literal text, a
 * value this session already holds, a reference it does not hold yet, a secret
 * kept hidden, or a value generated only when the step runs.
 */
export type PreviewSegment = { text: string; kind: 'text' | 'value' | 'unresolved' | 'secret' | 'generated' }

const TEMPLATE_REFERENCE = /\{\{\s*([^{}]+?)\s*\}\}/g

/** A template with this session's current values filled in where it has them. */
export function previewTemplate(text: string, values: Readonly<Record<string, string>> | null, runId?: string): PreviewSegment[] {
  const segments: PreviewSegment[] = []
  let last = 0
  for (const match of text.matchAll(TEMPLATE_REFERENCE)) {
    if (match.index > last) segments.push({ text: text.slice(last, match.index), kind: 'text' })
    last = match.index + match[0].length
    const expr = match[1]
    if (expr === 'run.id') {
      segments.push(runId ? { text: runId, kind: 'value' } : { text: match[0], kind: 'unresolved' })
    } else if (expr.startsWith('secret.')) {
      segments.push({ text: match[0], kind: 'secret' })
    } else if (expr.startsWith('fn.') || expr.startsWith('fake.')) {
      segments.push({ text: match[0], kind: 'generated' })
    } else {
      const value = values?.[expr]
      segments.push(value !== undefined ? { text: value, kind: 'value' } : { text: match[0], kind: 'unresolved' })
    }
  }
  if (last < text.length) segments.push({ text: text.slice(last), kind: 'text' })
  return segments
}

/** The step a paused session is about to run, as the script defines it. */
export function debugNextAction(script: ScriptDefinition, action: ScriptDebugAction): ScriptActionDefinition | null {
  const phase = action.phase === 'init' || action.phase === 'teardown' ? action.phase : 'action'
  const root = phaseActionsForScript(script, phase)[action.action_index]
  if (!root) return null
  if (action.step_path?.length) return resolveNestedAction(root, action.step_path)
  const requests = root.params?.requests
  if (typeof action.request_index === 'number' && Array.isArray(requests)) return (requests[action.request_index] as ScriptActionDefinition | undefined) || null
  return root
}

/** The request parts the preview shows, in order; headers as name/value pairs. */
export function debugNextRequestParts(action: ScriptActionDefinition): { target?: string; headers: Array<[string, string]>; body?: string } {
  const headers = Array.isArray(action.headers)
    ? action.headers.map(header => [String(header.name ?? ''), String(header.value ?? '')] as [string, string])
    : Object.entries(action.headers || {}).map(([name, value]) => [name, String(value)] as [string, string])
  const body = typeof action.body === 'string' && action.body ? action.body : undefined
  return { target: action.target || undefined, headers, body }
}
