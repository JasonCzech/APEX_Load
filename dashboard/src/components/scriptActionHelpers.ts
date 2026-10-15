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

export function asHeaderList(value: unknown): Action['header_list'] {
  if (!Array.isArray(value)) return undefined
  return value
    .filter(isRecord)
    .map(header => ({
      name: asString(header.name),
      value: asString(header.value),
      enabled: typeof header.enabled === 'boolean' ? header.enabled : undefined,
    }))
}

// Match dsl.HeaderList.ToMap for names represented in the ordered list while
// retaining compatibility-map names that have no ordered occurrence. The
// final ordered occurrence (including its spelling) is authoritative.
export function headerListToCompatibilityMap(
  headerList: NonNullable<Action['header_list']>,
  existingHeaders?: Record<string, string>,
): Record<string, string> {
  const identities = new Set(
    headerList.map(header => header.name.trim().toLowerCase()).filter(Boolean),
  )
  const headers: Record<string, string> = {}
  for (const [name, value] of Object.entries(existingHeaders || {})) {
    if (!identities.has(name.trim().toLowerCase())) headers[name] = value
  }
  const spelling = new Map<string, string>()
  for (const header of headerList) {
    const name = header.name.trim()
    if (!name) continue
    const identity = name.toLowerCase()
    const previous = spelling.get(identity)
    if (previous && previous !== name) delete headers[previous]
    spelling.set(identity, name)
    if (header.enabled === false) {
      delete headers[name]
      continue
    }
    headers[name] = header.value
  }
  return headers
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
      ...(Object.prototype.hasOwnProperty.call(request, 'http_version') ? { http_version: request.http_version as Action['http_version'] } : {}),
      target: asString(request.target),
      headers: asHeaders(request.headers),
      header_list: asHeaderList(request.header_list),
      body: asString(request.body),
      body_encoding: asString(request.body_encoding) || undefined,
      params: isRecord(request.params) ? request.params : undefined,
      metadata,
      extractors: asExtractors(request.extractors),
      assertions: asAssertions(request.assertions),
    }
  })
}

// rawHttpSequenceRequest returns the UNMODIFIED request object at index from an
// http_sequence's params.requests, preserving every field. Unlike
// httpSequenceRequests (which rebuilds a whitelisted view for display/counting),
// this is used when a sub-request is selected for the scoped-JSON view or saved as
// a reusable block, so vendor/advanced keys the importer kept are not dropped.
export function rawHttpSequenceRequest(action: Action, index: number): Action | null {
  const rawRequests = action.params?.requests
  if (!Array.isArray(rawRequests)) return null
  const raw = rawRequests[index]
  return isRecord(raw) ? (raw as unknown as Action) : null
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

export function actionExecutionBlockers(action: Action): string[] {
  const blockers = new Set<string>()
  const add = (value: unknown) => {
    if (typeof value !== 'string') return
    const normalized = value.trim()
    if (normalized) blockers.add(normalized)
  }
  add(action.metadata?.execution_blocker)
  const values = action.metadata?.execution_blockers
  if (Array.isArray(values)) values.forEach(add)
  return [...blockers].sort()
}

// The headers an import left out of the request, from its header fidelity
// marker. Markers written before headers were named hold a placeholder.
export function actionOmittedImportHeaderNames(action: Action): string[] {
  const marker = action.metadata?.import_header_fidelity
  const occurrences = marker && typeof marker === 'object' && Array.isArray((marker as { omitted_occurrences?: unknown }).omitted_occurrences)
    ? (marker as { omitted_occurrences: unknown[] }).omitted_occurrences
    : []
  const names: string[] = []
  for (const occurrence of occurrences) {
    const name = occurrence && typeof occurrence === 'object' ? (occurrence as { name?: unknown }).name : undefined
    if (typeof name !== 'string') continue
    const trimmed = name.trim()
    if (!trimmed || trimmed === '<header>' || trimmed === '<invalid-header>' || names.includes(trimmed)) continue
    names.push(trimmed)
  }
  return names
}

// Whether an import holds the action only for headers it left out.
function importReviewOnlyForHeaders(action: Action): boolean {
  const metadata = action.metadata || {}
  if (metadata.import_multipart_fidelity || metadata.import_http_version_fidelity) return false
  const review = metadata.import_review
  const reasons = review && typeof review === 'object' && Array.isArray((review as { reason_codes?: unknown }).reason_codes)
    ? (review as { reason_codes: unknown[] }).reason_codes
    : []
  return reasons.every(reason => typeof reason === 'string' && reason.startsWith('http_header_'))
}

// Each execution blocker with what it is about, as the server words it
// (pkg/api/replay_review_gates.go executionBlockerDetail): the variables a
// Bruno request leaves unset, or a count of the headers an import left out,
// noting when the same hold covers something else too.
export function actionExecutionBlockerDetails(action: Action): string[] {
  return actionExecutionBlockers(action).map(code => {
    if (code === 'bruno_missing_variables') {
      const missing = action.metadata?.missing_variables
      const names = Array.isArray(missing) ? missing.filter((name): name is string => typeof name === 'string' && Boolean(name.trim())) : []
      return names.length ? `${code} (${names.join(', ')})` : code
    }
    if (code === 'import_review_required') {
      const names = actionOmittedImportHeaderNames(action)
      if (names.length === 0) return code
      const label = `${names.length} ${names.length === 1 ? 'header' : 'headers'} left out`
      return `${code} (${importReviewOnlyForHeaders(action) ? '' : 'other review and '}${label}: ${names.join(', ')})`
    }
    return code
  })
}

/**
 * Imported LoadRunner calls (transaction markers, rendezvous, timers and other
 * lr_* utilities) replayed at their source position. They carry the source
 * function and arguments, never a request target, headers or body.
 */
export function isSourceFunctionAction(action: Action) {
  return action.type.startsWith('source_') || action.type.startsWith('loadrunner_') || action.type === 'rendezvous'
}

export function isControlAction(action: Action) {
	return action.type === 'control_if' || action.type === 'control_switch' || action.type === 'control_loop' || action.type === 'control_parallel'
}

export function isReviewGatedJavaScriptAction(action: Action) {
  if (action.type !== 'browser_evaluate_js') return false
  const reviewGate = asString(action.metadata?.review_gate).trim().toLowerCase()
  return reviewGate === 'javascript' || actionMetadataBool(action, 'review_required') || actionMetadataBool(action, 'promotion_blocker')
}

export function actionRequiresReview(action: Action) {
  return isReviewGatedJavaScriptAction(action)
    || actionMetadataBool(action, 'review_required')
    || actionMetadataBool(action, 'promotion_blocker')
    || actionExecutionBlockers(action).includes('import_review_required')
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
