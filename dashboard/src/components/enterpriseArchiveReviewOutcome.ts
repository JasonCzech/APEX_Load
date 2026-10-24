import type { EnterpriseArchiveImportResult } from '../types/scriptImports'
import type { ReplayPreflight, ScriptDefinition } from '../types/scripts'
import type { ImportReadiness } from './importReviewIssues'
import { requirementsPreserveDestinationValues } from './secretBindingUtils'

// The archive review's unsaved-work guard. An editor opened from the review
// keeps it when it returns there, since the review stays open behind it.
export const ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID = 'enterprise-archive-import'

// A fidelity blocker that restates a step gate: the converter's
// "<code> at <path>" line, or the bare code a guided repair writes
// (pkg/api import_repairs.go recomputedFidelityBlocker). Every other line is
// a converter sentence.
const GATE_BACKED_FIDELITY_BLOCKER = /^\S+( at \S+)?$/

interface ReviewState {
  result: EnterpriseArchiveImportResult | null
  savedResultScripts: Record<number, unknown>
  queuedResultScripts?: Record<number, unknown>
  reviewOptionsIdentity: string
  reviewGeneration: number
  replayReviewGeneration: number
  replayScriptIndex: number
  replayProjectId: string
  replayValidationLoading: boolean
  replayValidationSession: { status: string } | null
}

/**
 * @param inspectedReadiness the readiness a script's protected candidate
 *   reported when inspected, once one has answered.
 */
export function archiveReviewOutcome(item: ReviewState, optionsIdentity: string, projectId: string, inspectedReadiness?: (script: ScriptDefinition) => ImportReadiness | undefined): string {
  const result = item.result
  const scripts = result?.scripts?.length ? result.scripts : result?.script ? [result.script] : []
  const savedCount = scripts.filter((_, index) => Object.prototype.hasOwnProperty.call(item.savedResultScripts, index)).length
  const queuedCount = scripts.filter((_, index) => !Object.prototype.hasOwnProperty.call(item.savedResultScripts, index) && Boolean(item.queuedResultScripts?.[index])).length
  if (queuedCount > 0) return `Queued ${queuedCount} of ${scripts.length} for approval${savedCount ? `; saved ${savedCount}` : ''}`
  if (savedCount > 0) return savedCount === scripts.length ? 'Saved' : `Saved ${savedCount} of ${scripts.length}`
  if (item.reviewOptionsIdentity !== optionsIdentity) return 'Needs validation'
  if (!result) return 'Review required'
  const protocol = result.detected_protocol ?? result.summary?.detected_protocol
  if (protocol?.import_mode === 'external_runner_required') return protocol.apexload_protocol === 'java_vuser' ? 'Needs runner / approval' : 'Review only'
  if (protocol && protocol.import_mode !== 'executable') return 'Review only'
  if (scripts.length === 0) return 'Review required'
  const fidelity = result.fidelity_report ?? result.summary?.fidelity_report
  // Promotion is also held by review codes that never refuse replay (a value
  // sent as placeholder text, a review decision) and by the converter's
  // archive-level sentences, which hold promotion and load-test launch only.
  // Only a blocker that names a step gate, or the runtime migration policy,
  // makes the held status a blocker.
  const promotionHeld = fidelity?.promotion_status === 'blocked'
  if ((promotionHeld && (fidelity?.blockers || []).some(blocker => GATE_BACKED_FIDELITY_BLOCKER.test(blocker))) || result.summary?.migration_readiness?.blocked) return 'Blocker'
  if (scripts.some(script => {
    const preflight = (script.metadata?.import?.preserved?.replay_preflight ?? result.preflight) as ReplayPreflight | undefined
    // As the review counts credentials (importReviewFindings): an inspection's
    // readiness resolves secrets against the project, while a preflight
    // stamped without that resolver lists every project secret as missing, so
    // it counts only until an inspection answers.
    const readiness = inspectedReadiness?.(script)
    const unresolvedCredentials = readiness ? (readiness.unresolved_bindings ?? 0) > 0 : Boolean(preflight?.missing_secrets?.length)
    // Missing variables block only through valid/checks: replay sends most as placeholder text.
    return preflight?.valid === false || unresolvedCredentials || preflight?.checks?.some(check => check.status === 'fail')
  })) return 'Blocker'
  const browserIndex = scripts.findIndex(script => script.protocol === 'browser')
  if (browserIndex >= 0) {
    const matches = item.replayReviewGeneration === item.reviewGeneration
      && item.replayScriptIndex === browserIndex && item.replayProjectId === projectId
    if (!matches) return 'Needs replay'
    const status = item.replayValidationSession?.status
    if (item.replayValidationLoading || status === 'queued' || status === 'running' || status === 'paused') return 'Replay in progress'
    if (status === 'failed' || status === 'stopped') return 'Needs replay retry'
    if (status === 'completed') return 'Review replay results'
    return 'Needs replay'
  }
  if ((result.secret_binding_requirements || []).some(requirement => !requirementsPreserveDestinationValues([requirement]))) return 'Needs credential review'
  if (promotionHeld) return 'Needs review'
  // Parameterization suggestions are optional and never hold back a save.
  return 'Ready to save'
}
