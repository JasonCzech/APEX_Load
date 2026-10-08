// Journey accounting for a finalized run (E5): GET
// /tests/{id}/archive/journeys. Weighted test-journey risk, never revenue.

export interface JourneyBoundary {
  transaction?: string
  action?: string
}

export interface JourneyAttemptRef {
  group: string
  vuser_id: number
  iteration: number
  started_at: string
  outcome: 'failed' | 'censored' | string
  failed_step?: string
}

export interface JourneyAccounting {
  id: string
  name: string
  weight: number
  start: JourneyBoundary
  success: JourneyBoundary
  completed: number
  failed: number
  aborted: number
  skipped: number
  censored: number
  eligible: number
  steps: number
  step_failures: number
  failure_rate?: number
  examples?: JourneyAttemptRef[]
}

export interface JourneyRiskReport {
  schema_version: string
  run_id: string
  policy: { id: string; name: string; revision: number; sha256: string; source: 'pinned' | 'requested' | string }
  availability: 'available' | 'unavailable' | string
  reason?: string
  journeys: JourneyAccounting[]
  weighted_failure_index?: number
  index_unscored?: string
  step_risk_enabled: boolean
  step_risk_index?: number
  step_risk_unscored?: string
  incomplete?: boolean
  rows_outside_iterations: number
  notes: string[]
}

const unavailableReasons: Record<string, string> = {
  no_journey_policy: 'This run pinned no journey policy. Name one to account its journeys.',
  archive_lacks_vuser_iteration_identity: 'This archive was written before runs recorded VUser iterations, so its journeys cannot be accounted.',
}

const unscoredReasons: Record<string, string> = {
  no_weighted_eligible_attempts: 'Unscored: no weighted journey reached an outcome.',
  no_weighted_steps: 'Unscored: no weighted journey recorded a step.',
  accounting_incomplete: 'Unscored: the accounting is incomplete (tracking bounds were reached).',
}

export function journeyUnavailableText(reason?: string): string {
  return unavailableReasons[reason || ''] || `Journey accounting is unavailable${reason ? ` (${reason})` : ''}.`
}

export function journeyUnscoredText(reason?: string): string {
  return unscoredReasons[reason || ''] || 'Unscored.'
}

export function formatJourneyBoundary(boundary: JourneyBoundary): string {
  if (boundary.transaction) return `transaction ${boundary.transaction}`
  if (boundary.action) return `action ${boundary.action}`
  return 'not set'
}

export function formatJourneyFraction(value?: number): string {
  return value === undefined || value === null || !Number.isFinite(value) ? '—' : `${(value * 100).toFixed(1)}%`
}
