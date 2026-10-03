import type { ScriptActionDefinition } from '../types/scripts'

export type HTTPSequenceExecutionMode = 'sequential' | 'concurrent'
export const HTTP_SEQUENCE_MAX_CONCURRENCY = 16
export const HTTP_SEQUENCE_MAX_CONCURRENT_CHILDREN = 256
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)

export function httpSequenceExecutionSettings(action: ScriptActionDefinition, protocol: string) {
  if (protocol !== 'http' || action.type !== 'http_sequence') return null
  const paramsInvalid = action.params !== undefined && !record(action.params)
  const params = record(action.params) ? action.params : {}
  const defaultMode = !own(params, 'execution_mode')
  const rawMode: unknown = params.execution_mode
  const mode: HTTPSequenceExecutionMode | null = defaultMode ? 'sequential' : rawMode === 'sequential' || rawMode === 'concurrent' ? rawMode : null
  const limitPresent = own(params, 'max_concurrency')
  const rawLimit: unknown = params.max_concurrency
  const limitValid = !limitPresent || typeof rawLimit === 'number' && Number.isInteger(rawLimit) && rawLimit >= 1 && rawLimit <= HTTP_SEQUENCE_MAX_CONCURRENCY
  const limit = limitPresent && limitValid ? rawLimit as number : undefined
  const requests: unknown = params.requests
  const requestCount = Array.isArray(requests) ? requests.length : 0
  const concurrentChildrenValid = Array.isArray(requests) && requestCount >= 1 && requestCount <= HTTP_SEQUENCE_MAX_CONCURRENT_CHILDREN && requests.every(record)
  const valid = !paramsInvalid && mode !== null && limitValid && (mode !== 'concurrent' || concurrentChildrenValid)
  const effectiveConcurrency = valid && mode === 'concurrent' ? Math.min(limit ?? 8, requestCount) : undefined
  // Invalid scalar/object claims never become select labels or inferred facts.
  const limitInput = typeof rawLimit === 'number' && Number.isFinite(rawLimit) ? String(rawLimit) : ''
  return { mode, defaultMode, paramsInvalid, limitPresent, limitValid, limitInput, requestCount, concurrentChildrenValid, valid, effectiveConcurrency }
}

/** Explicit edits change only the chosen policy field, preserving the raw request list and unknown params. */
export function setHTTPSequenceExecutionMode(action: ScriptActionDefinition, protocol: string, mode: HTTPSequenceExecutionMode | undefined): ScriptActionDefinition {
  const settings = httpSequenceExecutionSettings(action, protocol)
  if (!settings || settings.paramsInvalid || (mode !== undefined && mode !== 'sequential' && mode !== 'concurrent')) return action
  const params = { ...action.params }
  if (mode === undefined) delete params.execution_mode
  else params.execution_mode = mode
  return { ...action, params }
}

export function setHTTPSequenceMaxConcurrency(action: ScriptActionDefinition, protocol: string, limit: number | undefined): ScriptActionDefinition {
  const settings = httpSequenceExecutionSettings(action, protocol)
  if (!settings || settings.paramsInvalid || (limit !== undefined && !Number.isFinite(limit))) return action
  const params = { ...action.params }
  if (limit === undefined) delete params.max_concurrency
  else params.max_concurrency = limit
  return { ...action, params }
}
