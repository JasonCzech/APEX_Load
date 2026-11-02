import type { Parameterization, ParameterizationSuggestion, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import { importActionEntries } from './importReviewIssues'

// What the server's Salesforce shaping did to the reviewed draft. Step
// transactions tag each request with its step; recorded concurrency wraps
// parallel requests in a group. A recording that marks its own transactions
// keeps them, and the server says it added no step transactions.
export function harShapingApplied(script: ScriptDefinition | null | undefined, warnings: readonly string[] | undefined) {
  if (!script) return { stepTransactions: false, recordedConcurrency: false }
  const entries = importActionEntries(script)
  const recordedTransactions = (warnings || []).some(warning => warning.startsWith('Step transactions were not added'))
  return {
    stepTransactions: !recordedTransactions && entries.some(entry => typeof entry.action.metadata?.source_transaction === 'string'),
    recordedConcurrency: entries.some(entry => entry.action.metadata?.recorded_concurrent_group !== undefined),
  }
}

// Compares two captures without holding either whole file in memory, and
// stops at the first differing chunk.
const HAR_COMPARE_CHUNK_BYTES = 4 * 1024 * 1024
export async function filesHaveIdenticalBytes(left: File, right: File, cancelled: () => boolean): Promise<boolean | null> {
  if (left.size !== right.size) return false
  for (let offset = 0; offset < left.size; offset += HAR_COMPARE_CHUNK_BYTES) {
    const end = Math.min(left.size, offset + HAR_COMPARE_CHUNK_BYTES)
    const [a, b] = await Promise.all([left.slice(offset, end).arrayBuffer(), right.slice(offset, end).arrayBuffer()])
    if (cancelled()) return null
    const viewA = new Uint8Array(a)
    const viewB = new Uint8Array(b)
    if (viewA.length !== viewB.length) return false
    for (let index = 0; index < viewA.length; index++) {
      if (viewA[index] !== viewB[index]) return false
    }
  }
  return true
}

// The source-secret session after an exclusion. The request sends the
// import's session; the server consumes it and re-keys its recorded values to
// the requirements' IDs after the exclusion (IDs hash each location's action
// path, so a moved credential's ID changes), marking the requirements whose
// value followed them. The client never carries the flag by ID itself: an
// old ID can name a different location now. When the session did not survive
// (or the response does not name its replacement), no requirement claims a
// recorded value.
export interface SourceSecretSessionAfterExclusion {
  sessionId?: string
  expiresAt?: string
  requirements: SecretBindingRequirement[]
}
export function sourceSecretSessionAfterExclusion(
  sentSessionId: string | undefined,
  response: { source_secret_session_id?: string; source_secret_session_expires_at?: string },
  requirements: readonly SecretBindingRequirement[],
): SourceSecretSessionAfterExclusion {
  if (!sentSessionId) return { requirements: withoutSourceValues(requirements) }
  const sessionId = typeof response.source_secret_session_id === 'string' ? response.source_secret_session_id.trim() : ''
  if (!sessionId) return { requirements: withoutSourceValues(requirements) }
  const expiresAt = typeof response.source_secret_session_expires_at === 'string' && response.source_secret_session_expires_at.trim()
    ? response.source_secret_session_expires_at
    : undefined
  return { sessionId, expiresAt, requirements: [...requirements] }
}

export function withoutSourceValues(requirements: readonly SecretBindingRequirement[] | undefined): SecretBindingRequirement[] {
  return (requirements || []).map(requirement => {
    if (requirement.source_value_available === undefined) return requirement
    const rest = { ...requirement }
    delete rest.source_value_available
    return rest
  })
}

// The exclusion endpoint re-derives parameterization from the edited draft,
// which cannot say which values the converter already correlated: that record
// exists only at conversion. The import's pre-applied rows are carried over
// while the draft still extracts their variable. Their step numbers and
// locations named actions by position and went stale with the exclusion (some
// name a step that moved, some a step that is gone), so the carried rows say
// what was applied without either.
export const PRE_APPLIED_AFTER_EXCLUSION_RATIONALE = 'Already applied during conversion: the draft still extracts this value and substitutes it where it was recorded.'
export function carryPreAppliedCorrelations(
  previous: Parameterization | null | undefined,
  next: Parameterization | null | undefined,
  script: ScriptDefinition,
): Parameterization | undefined {
  const nextParameterization = next || undefined
  const previousRows = (previous?.suggestions || []).filter(suggestion => suggestion.pre_applied === true)
  if (previousRows.length === 0) return nextParameterization
  const extracted = new Set<string>()
  for (const { action } of importActionEntries(script)) {
    for (const extractor of action.extractors || []) {
      if (extractor && typeof extractor.name === 'string' && extractor.name) extracted.add(extractor.name)
    }
  }
  const nextRows = nextParameterization?.suggestions || []
  const takenIds = new Set(nextRows.map(row => row.id))
  const appliedNames = new Set(nextRows.filter(row => row.pre_applied).map(row => row.var_name))
  const carried: ParameterizationSuggestion[] = []
  for (const row of previousRows) {
    if (!extracted.has(row.var_name) || appliedNames.has(row.var_name) || takenIds.has(row.id)) continue
    appliedNames.add(row.var_name)
    takenIds.add(row.id)
    const carriedRow: ParameterizationSuggestion = { ...row, rationale: PRE_APPLIED_AFTER_EXCLUSION_RATIONALE, auto_apply: false }
    delete carriedRow.locations
    carried.push(carriedRow)
  }
  if (carried.length === 0) return nextParameterization
  const summary = nextParameterization?.summary
  const byKind = { ...(summary?.by_kind || {}) }
  for (const row of carried) byKind[row.kind] = (byKind[row.kind] || 0) + 1
  return {
    ...(nextParameterization || {}),
    // Correlations sort first, as the server orders them.
    suggestions: [...carried, ...nextRows],
    summary: {
      total: (summary?.total || 0) + carried.length,
      by_kind: byKind,
      auto_apply_count: summary?.auto_apply_count || 0,
      review_count: summary?.review_count || 0,
      high_risk_count: (summary?.high_risk_count || 0) + carried.filter(row => row.high_risk).length,
      pre_applied_count: (summary?.pre_applied_count || 0) + carried.length,
    },
  }
}

// The exclusion endpoint consumes the source-secret session it is sent and
// rotates a credential-preserving draft's attestation before it answers, and
// every refusal it writes comes before either. A 4xx says the draft and
// session are untouched, and so do the coded 5xx answers written before the
// rotation: the failed rotation itself and the domain feature policy check
// that runs before any change. Any other 5xx may follow a finished exclusion:
// the access logger stamps X-Request-ID before the handler runs and answers a
// handler panic with a 500, and a proxy may add its own. A dropped
// connection, a timeout or an unreadable 200 may also follow one.
const EXCLUSION_REFUSED_SERVER_CODES = new Set([
  'import_draft_attestation_unavailable',
  'domain_feature_policy_unavailable',
])

export function exclusionRefusedBeforeCommit(error: unknown): boolean {
  if (!(error instanceof ApiError) || error.status < 400) return false
  return error.status < 500 || EXCLUSION_REFUSED_SERVER_CODES.has(error.code || '')
}

// The conversion's converted_actions counts the draft's request actions, and
// an exclusion removes requests from it.
export function statsAfterExclusion<T>(stats: T, removedCount: number): T {
  if (!stats || typeof stats !== 'object' || removedCount <= 0) return stats
  const converted = (stats as Record<string, unknown>).converted_actions
  if (typeof converted !== 'number' || !Number.isFinite(converted)) return stats
  return { ...stats, converted_actions: Math.max(0, converted - removedCount) }
}
