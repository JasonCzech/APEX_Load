import { type ThinkTimeConfig } from '../types/scripts'

// The engine accepts every finite multiplier greater than zero. Keep the HTML
// minimum at zero so number inputs can represent arbitrarily small positive
// values; normalizeThinkTimeMultiplier still treats zero as the legacy
// "inherit the default multiplier" sentinel.
export const MIN_THINK_TIME_MULTIPLIER = 0
export const MAX_THINK_TIME_MULTIPLIER = 100
export const THINK_TIME_MULTIPLIER_STEP = 'any'

function normalizedThinkTimeType(type?: string): string {
	return type?.trim().toLowerCase() || ''
}

/** `normal` is the legacy wire alias for the canonical `gaussian` mode. */
export function canonicalThinkTimeType(type?: string): string {
	const normalized = normalizedThinkTimeType(type)
	return normalized === 'normal' ? 'gaussian' : normalized
}

export function normalizeThinkTimeMultiplier(value: unknown): number | undefined {
	const parsed = typeof value === 'number' ? value : Number(value)
	return Number.isFinite(parsed) &&
		parsed > MIN_THINK_TIME_MULTIPLIER &&
		parsed <= MAX_THINK_TIME_MULTIPLIER
		? parsed
		: undefined
}

export function thinkTimeUsesMean(type?: string): boolean {
  const normalized = normalizedThinkTimeType(type)
  return normalized !== 'none' && normalized !== 'recorded' && normalized !== 'uniform' && normalized !== 'random_percentage'
}

export function thinkTimeUsesStdDeviation(type?: string): boolean {
  const normalized = normalizedThinkTimeType(type)
  return normalized === 'gaussian' || normalized === 'normal'
}

export function thinkTimeUsesBounds(type?: string): boolean {
  const normalized = normalizedThinkTimeType(type)
  return normalized === 'gaussian' || normalized === 'normal' || normalized === 'uniform'
}

/**
 * Changes think-time mode while keeping only fields that have meaning for the
 * selected mode. Recorded replay inherits timing from action-local source
 * waits, so carrying distribution values into that mode creates a stale hidden
 * override.
 */
export function thinkTimeConfigForType(
  current: Partial<ThinkTimeConfig> | undefined,
  type: string,
): ThinkTimeConfig {
  const normalizedType = canonicalThinkTimeType(type)
  const next: ThinkTimeConfig = {
    ...(current ?? {}),
    type: normalizedType,
  }
  if (normalizedType === 'random_percentage') {
    next.min_percent ??= canonicalThinkTimeType(current?.type) === 'random_percentage' ? 0 : 50
    next.max_percent ??= canonicalThinkTimeType(current?.type) === 'random_percentage' ? 0 : 150
  } else {
    delete next.min_percent
    delete next.max_percent
  }
  if (normalizedType === 'none' || normalizedType === 'recorded' || normalizedType === 'random_percentage') {
    delete next.mean_ms
    delete next.std_ms
    delete next.min_ms
    delete next.max_ms
  } else if (normalizedType === 'uniform') {
    const fallback = next.mean_ms ?? 0
    next.min_ms ??= fallback
    next.max_ms ??= fallback
    delete next.mean_ms
    delete next.std_ms
	} else {
		next.mean_ms ??= next.min_ms !== undefined && next.max_ms !== undefined
			? (next.min_ms + next.max_ms) / 2
			: 0
    if (!thinkTimeUsesStdDeviation(normalizedType)) {
      delete next.std_ms
      delete next.min_ms
			delete next.max_ms
		}
	}
	const multiplier = normalizeThinkTimeMultiplier(next.multiplier)
	if (multiplier === undefined) delete next.multiplier
	else next.multiplier = multiplier
	return next
}

export function normalizedThinkTimeConfig(
  current: Partial<ThinkTimeConfig>,
): ThinkTimeConfig {
  return thinkTimeConfigForType(current, current.type || 'fixed')
}
