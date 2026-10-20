import type { EnterpriseArchiveImportResult } from '../types/scriptImports'
import type { ReplayPreflight } from '../types/scripts'
import { requirementsPreserveDestinationValues } from './secretBindingUtils'

// The archive review's unsaved-work guard. An editor opened from the review
// keeps it when it returns there, since the review stays open behind it.
export const ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID = 'enterprise-archive-import'

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

export function archiveReviewOutcome(item: ReviewState, optionsIdentity: string, projectId: string): string {
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
  if (fidelity?.promotion_status === 'blocked' || result.summary?.migration_readiness?.blocked) return 'Blocker'
  if (scripts.some(script => {
    const preflight = (script.metadata?.import?.preserved?.replay_preflight ?? result.preflight) as ReplayPreflight | undefined
    // Missing variables block only through valid/checks: replay sends most as placeholder text.
    return preflight?.valid === false || Boolean(preflight?.missing_secrets?.length) || preflight?.checks?.some(check => check.status === 'fail')
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
  if ((result.parameterization?.suggestions || []).some(suggestion => !suggestion.pre_applied)) return 'Needs parameter review'
  return 'Ready to save'
}
