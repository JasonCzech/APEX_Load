import type { ScriptActionDefinition } from '../types/scripts'
import { dslSpecForActionType, type DslActionCatalog } from './dslActionCatalog'

// Action is the editor's working alias for a script action definition.
type Action = ScriptActionDefinition

// Pure action-introspection helpers shared by ScriptEditor.tsx and its extracted
// sub-components. Kept in their own module so the Action editor UI can be split out
// without a circular import back into ScriptEditor.tsx.

export function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

export function asString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function asHeaders(value: unknown): Record<string, string> | undefined {
  if (!isRecord(value)) return undefined
  const headers: Record<string, string> = {}
  Object.entries(value).forEach(([key, headerValue]) => {
    if (typeof headerValue === 'string') headers[key] = headerValue
  })
  return Object.keys(headers).length > 0 ? headers : undefined
}

export function asExtractors(value: unknown): Action['extractors'] {
  if (!Array.isArray(value)) return undefined
  return value.filter(isRecord).map(item => ({
    name: asString(item.name),
    source: asString(item.source),
    type: asString(item.type),
    expression: asString(item.expression),
  })).filter(item => item.name || item.expression)
}

export function asAssertions(value: unknown): Action['assertions'] {
  if (!Array.isArray(value)) return undefined
  return value.filter(isRecord).map(item => ({
    type: asString(item.type),
    expected: item.expected,
    value: asString(item.value),
    max: typeof item.max === 'number' ? item.max : undefined,
  })).filter(item => item.type)
}

export function httpSequenceRequests(action: Action): Action[] {
  if (action.type !== 'http_sequence') return []
  const rawRequests = action.params?.requests
  if (!Array.isArray(rawRequests)) return []
  return rawRequests.filter(isRecord).map((request, index) => {
    const method = asString(request.method) || asString(request.type) || 'GET'
    const metadata = isRecord(request.metadata) ? request.metadata : undefined
    return {
      name: asString(request.name) || `${action.name || 'Request'} ${index + 1}`,
      type: asString(request.type) || method,
      method,
      target: asString(request.target),
      headers: asHeaders(request.headers),
      body: asString(request.body),
      body_encoding: asString(request.body_encoding) || undefined,
      params: isRecord(request.params) ? request.params : undefined,
      metadata,
      extractors: asExtractors(request.extractors),
      assertions: asAssertions(request.assertions),
    }
  })
}

export function actionRequestCount(action: Action) {
  return httpSequenceRequests(action).length
}

export function actionExtractorCount(action: Action) {
  return (action.extractors?.length || 0) + httpSequenceRequests(action).reduce((count, request) => count + (request.extractors?.length || 0), 0)
}

export function actionAssertionCount(action: Action) {
  return (action.assertions?.length || 0) + httpSequenceRequests(action).reduce((count, request) => count + (request.assertions?.length || 0), 0)
}

export function actionMetadataBool(action: Action, key: string) {
  const value = action.metadata?.[key]
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') return value.trim().toLowerCase() === 'true'
  return false
}

export function isControlAction(action: Action) {
  return action.type === 'control_if' || action.type === 'control_loop'
}

export function isReviewGatedJavaScriptAction(action: Action) {
  if (action.type !== 'browser_evaluate_js') return false
  const reviewGate = asString(action.metadata?.review_gate).trim().toLowerCase()
  return reviewGate === 'javascript' || actionMetadataBool(action, 'review_required') || actionMetadataBool(action, 'promotion_blocker')
}

export function actionRequiresReview(action: Action) {
  return isReviewGatedJavaScriptAction(action) || actionMetadataBool(action, 'review_required') || actionMetadataBool(action, 'promotion_blocker')
}

export function actionSelectorFallbackCount(action: Action) {
  const fallbacks = action.metadata?.source_selector_fallbacks
  if (Array.isArray(fallbacks)) return fallbacks.length
  const candidates = action.metadata?.source_selector_candidates
  if (Array.isArray(candidates)) return Math.max(0, candidates.length - 1)
  return 0
}

export function actionDisplayType(action: Action) {
  return action.type === 'http_sequence' ? 'HTTP sequence' : (action.type || action.method || 'action')
}

export function actionDisplayTarget(action: Action) {
  const requests = httpSequenceRequests(action)
  if (requests.length === 0) return action.selector || action.target || action.method || ''
  const firstTarget = requests[0]?.target || action.target
  return `${requests.length} requests${firstTarget ? ` • ${firstTarget}` : ''}`
}

// Humanize a raw DSL action type into a short, readable label, e.g.
// "source_transaction" -> "Source transaction", "ws_send" -> "Ws send".
// Used as a fallback so an outline of cryptic types stays legible to
// less-technical authors before (or without) the action catalog.
export function humanizeActionType(type: string): string {
  const cleaned = (type || '').replace(/_/g, ' ').trim()
  if (!cleaned) return 'Action'
  return cleaned.charAt(0).toUpperCase() + cleaned.slice(1)
}

// Plain-English description of an action's type, sourced from the DSL action
// catalog (GET /api/v1/dsl/actions) when it has loaded, falling back to a
// humanized form of the type. Intended for hover/title text on action rows so
// authors can read what a step does without decoding the DSL type string.
export function actionTypeSummary(
  action: Action,
  catalog?: DslActionCatalog | null,
): string {
  const spec = dslSpecForActionType(action.type, catalog ?? undefined)
  if (spec?.summary) return spec.summary
  return humanizeActionType(action.type)
}

// Readable label for an action row: the author-given name when present,
// otherwise a humanized label for its type (instead of a bare "Unnamed").
export function actionRowLabel(action: Action): string {
  const name = (action.name || '').trim()
  if (name) return name
  return humanizeActionType(action.type)
}
