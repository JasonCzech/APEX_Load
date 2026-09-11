import type { GoalConfig } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

const maxManagedTestVUsers = 1_000_000

function hasNumericValue(value: number | undefined | null): value is number {
  return value !== undefined && value !== null
}

function isInvalidNumber(value: number | undefined | null, invalidRange: (value: number) => boolean): boolean {
  return hasNumericValue(value) && (!Number.isFinite(value) || invalidRange(value))
}

const goalNumericFields = [
  'target_average_tps',
  'p95_latency_ms',
  'transaction_response_time_sla_ms',
  'max_error_pct',
  'max_avg_server_processing_ms',
  'max_avg_transfer_ms',
  'max_error_count',
  'min_vusers',
  'max_vusers',
  'max_lcp_ms',
  'max_inp_ms',
  'max_cls',
  'max_throttled_requests',
  'max_end_to_end_latency_ms',
  'max_partition_skew_pct',
  'max_redelivery_rate_pct',
  'max_dead_letter_rate_pct',
] as const satisfies ReadonlyArray<keyof GoalConfig>

const slaThresholdFields = [
  'p95_latency_ms',
  'max_error_pct',
  'max_avg_server_processing_ms',
  'max_avg_transfer_ms',
  'max_lcp_ms',
  'max_inp_ms',
  'max_cls',
  'max_throttled_requests',
  'max_end_to_end_latency_ms',
  'max_partition_skew_pct',
  'max_redelivery_rate_pct',
  'max_dead_letter_rate_pct',
] as const satisfies ReadonlyArray<keyof GoalConfig>

export function hasGoalModeConfiguration(goalConfig: GoalConfig): boolean {
  return goalNumericFields.some(field => hasNumericValue(goalConfig[field] as number | undefined))
		|| Boolean(goalConfig.transaction_rules?.length)
    || goalConfig.stop_on_sla_breach === true
    || goalConfig.stop_on_max_errors === true
}

function hasSLAThreshold(goalConfig: GoalConfig): boolean {
  return slaThresholdFields.some(field => hasNumericValue(goalConfig[field] as number | undefined))
}

export function validateGoalConfig(goalConfig: GoalConfig): string | null {
	if ((goalConfig.transaction_rules?.length || 0) > 128) {
		return 'Transaction SLA rules cannot contain more than 128 entries'
	}
	for (const [index, rule] of (goalConfig.transaction_rules || []).entries()) {
		if (!rule.transaction || rule.transaction.trim() !== rule.transaction || new TextEncoder().encode(rule.transaction).length > 256) {
			return `Transaction SLA rule ${index + 1} requires an exact name of at most 256 bytes without surrounding whitespace`
		}
		if (!Number.isFinite(rule.threshold) || rule.threshold < 0) {
			return `Transaction SLA rule ${index + 1} requires a finite non-negative threshold`
		}
	}
  if (isInvalidNumber(goalConfig.target_average_tps, value => value <= 0)) {
    return 'Goal-oriented scenarios require target average TPS to be greater than 0'
  }
  if (isInvalidNumber(goalConfig.p95_latency_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require P95 latency ceiling to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.transaction_response_time_sla_ms, value => value <= 0)) {
    return 'Transaction response-time SLA must be a finite duration greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.max_error_pct, value => value < 0 || value > 100)) {
    return 'Goal-oriented scenarios require max error % to be between 0 and 100'
  }
  if (isInvalidNumber(goalConfig.max_avg_server_processing_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require max average server processing to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.max_avg_transfer_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require max average transfer time to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.min_vusers, value => value < 1 || !Number.isSafeInteger(value))) {
    return 'Goal-oriented scenarios require min VUsers to be a safe integer greater than 0'
  }
  if (isInvalidNumber(goalConfig.max_vusers, value => value < 1 || !Number.isSafeInteger(value))) {
    return 'Goal-oriented scenarios require max VUsers to be a safe integer greater than 0'
  }
  if (hasNumericValue(goalConfig.min_vusers) && goalConfig.min_vusers > maxManagedTestVUsers) {
    return `Goal-oriented scenarios require min VUsers to be at most ${maxManagedTestVUsers}`
  }
  if (hasNumericValue(goalConfig.max_vusers) && goalConfig.max_vusers > maxManagedTestVUsers) {
    return `Goal-oriented scenarios require max VUsers to be at most ${maxManagedTestVUsers}`
  }
  if (
    goalConfig.min_vusers !== undefined &&
    goalConfig.max_vusers !== undefined &&
    goalConfig.min_vusers > goalConfig.max_vusers
  ) {
    return 'Goal-oriented scenarios require min VUsers to be less than or equal to max VUsers'
  }
  if (isInvalidNumber(goalConfig.max_error_count, value => value < 1 || !Number.isSafeInteger(value))) {
    return 'Goal-oriented scenarios require max error count to be a safe integer greater than 0'
  }
  if (isInvalidNumber(goalConfig.max_lcp_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require max LCP to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.max_inp_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require max INP to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.max_cls, value => value <= 0)) {
    return 'Goal-oriented scenarios require max CLS to be greater than 0'
  }
  if (isInvalidNumber(goalConfig.max_throttled_requests, value => value < 0 || !Number.isSafeInteger(value))) {
    return 'Goal-oriented scenarios require max throttled requests to be a safe integer zero or greater'
  }
  if (isInvalidNumber(goalConfig.max_end_to_end_latency_ms, value => value <= 0)) {
    return 'Goal-oriented scenarios require max end-to-end latency to be greater than 0 ms'
  }
  if (isInvalidNumber(goalConfig.max_partition_skew_pct, value => value < 0 || value > 100)) {
    return 'Goal-oriented scenarios require max partition skew % to be between 0 and 100'
  }
  if (isInvalidNumber(goalConfig.max_redelivery_rate_pct, value => value < 0 || value > 100)) {
    return 'Goal-oriented scenarios require max redelivery rate % to be between 0 and 100'
  }
  if (isInvalidNumber(goalConfig.max_dead_letter_rate_pct, value => value < 0 || value > 100)) {
    return 'Goal-oriented scenarios require max dead-letter rate % to be between 0 and 100'
  }
  if (goalConfig.sla_breach_window && parseDurationToSeconds(goalConfig.sla_breach_window) <= 0) {
    return 'Goal-oriented scenarios require a valid SLA breach window duration'
  }
  if (goalConfig.stop_on_sla_breach && !hasSLAThreshold(goalConfig)) {
    return 'Goal-oriented scenarios require an SLA threshold when stop on sustained SLA breach is enabled'
  }
  if (goalConfig.stop_on_max_errors && goalConfig.max_error_count === undefined) {
    return 'Goal-oriented scenarios require max error count when stop on max errors is enabled'
  }
  return null
}

export function validateTransactionResponseTimeSLAOverrides(
  groups: Array<{ transactionResponseTimeSlaMs?: number }>,
): string | null {
  const invalidIndex = groups.findIndex(group => isInvalidNumber(group.transactionResponseTimeSlaMs, value => value <= 0))
  return invalidIndex < 0 ? null : `Group ${invalidIndex + 1} transaction response-time SLA must be a finite duration greater than 0 ms`
}
