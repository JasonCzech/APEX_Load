import type { RuntimeSettingsOverride } from '../types/runtimeSettings'
import { parseSecondsInputAsMilliseconds } from './runtimeTiming'

const DEFAULT_RANDOM_PACING_BOUND_MS = 1000

function finiteNonNegative(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback
}

function randomPacingFromEdit(
  type: 'random_delay' | 'random_interval',
  baseline?: RuntimeSettingsOverride['pacing'],
  amount = '',
): NonNullable<RuntimeSettingsOverride['pacing']> {
  const fixedSeed = type === 'random_delay'
    ? baseline?.delay_ms
    : baseline?.interval_ms
  const parsedTypedSeed = parseSecondsInputAsMilliseconds(amount)
  const typedSeed = typeof parsedTypedSeed === 'number' ? parsedTypedSeed : undefined
  const seed = finiteNonNegative(fixedSeed ?? typedSeed, DEFAULT_RANDOM_PACING_BOUND_MS)
  const lowerRaw = type === 'random_delay' ? baseline?.min_delay_ms : baseline?.min_interval_ms
  const upperRaw = type === 'random_delay' ? baseline?.max_delay_ms : baseline?.max_interval_ms
  const lower = finiteNonNegative(lowerRaw, seed)
  const upper = finiteNonNegative(upperRaw, seed)
  const min = Math.min(lower, upper)
  const max = Math.max(lower, upper)
  return type === 'random_delay'
    ? { type, min_delay_ms: min, max_delay_ms: max }
    : { type, min_interval_ms: min, max_interval_ms: max }
}

export function randomPacingDraft(
  type: 'random_delay' | 'random_interval',
  baseline?: RuntimeSettingsOverride['pacing'],
  amount = '',
): NonNullable<RuntimeSettingsOverride['pacing']> {
  return baseline?.type === type
    ? { ...baseline, type }
    : randomPacingFromEdit(type, baseline, amount)
}

export function pacingConfigForSelection(
  type: string,
  baseline?: RuntimeSettingsOverride['pacing'],
  amount = '',
): RuntimeSettingsOverride['pacing'] | undefined {
  if (type === 'random_delay' || type === 'random_interval') {
    return randomPacingDraft(type, baseline, amount)
  }
  return type === baseline?.type ? { ...baseline } : undefined
}

export function pacingFromEdit(
  type: string,
  amount: string,
  baseline?: RuntimeSettingsOverride['pacing'],
): RuntimeSettingsOverride['pacing'] | undefined {
  if (!type) return undefined
  if (type === 'random_delay' || type === 'random_interval') {
    return randomPacingFromEdit(type, baseline, amount)
  }
  if (type === baseline?.type) {
    const next = { ...baseline }
    if (type === 'fixed_delay') next.delay_ms = parseSecondsInputAsMilliseconds(amount) ?? 0
    else if (type === 'fixed_interval') next.interval_ms = parseSecondsInputAsMilliseconds(amount) ?? 0
    else if (type === 'tps') next.tps = amount ? Number(amount) : 0
    return next
  }
  if (type === 'none') return { type: 'none' }
  if (type === 'fixed_interval') return { type, interval_ms: parseSecondsInputAsMilliseconds(amount) ?? 0 }
  if (type === 'tps') return { type, tps: amount ? Number(amount) : 0 }
  if (type === 'fixed_delay') return { type, delay_ms: parseSecondsInputAsMilliseconds(amount) ?? 0 }
  return { type }
}
