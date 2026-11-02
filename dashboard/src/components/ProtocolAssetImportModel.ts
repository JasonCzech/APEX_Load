import type { SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
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

// An exclusion re-discovers the draft's credential requirements without the
// source-secret session, so none comes back marked as having a recorded value.
// A requirement it kept (same ID) still has its value in the session.
export function carrySourceValueAvailability(
  previous: readonly SecretBindingRequirement[] | undefined,
  next: readonly SecretBindingRequirement[],
  sourceSecretSessionId: string | undefined,
): SecretBindingRequirement[] {
  if (!sourceSecretSessionId) return [...next]
  const available = new Set((previous || []).filter(requirement => requirement.source_value_available === true).map(requirement => requirement.id))
  return next.map(requirement => available.has(requirement.id) && requirement.source_value_available !== true
    ? { ...requirement, source_value_available: true }
    : requirement)
}
