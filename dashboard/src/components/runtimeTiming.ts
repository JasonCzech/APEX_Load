export const SECONDS_INPUT_STEP = '0.001'

export interface RuntimePacingTiming {
  type?: string
  delay_ms?: number
  min_delay_ms?: number
  max_delay_ms?: number
  interval_ms?: number
  min_interval_ms?: number
  max_interval_ms?: number
  tps?: number
}

/**
 * Runtime timing is stored and transported as integer milliseconds. Editors
 * expose seconds, retaining up to millisecond precision for decimal values.
 */
export function formatMillisecondsAsSecondsValue(
  valueMs: number | null | undefined,
  fallbackMs = 0,
  emptyWhenUndefined = false,
): string {
  if ((valueMs === undefined || valueMs === null) && emptyWhenUndefined) return ''
  const safeMs = valueMs ?? fallbackMs
  if (!Number.isFinite(safeMs)) return '0'
  const roundedSeconds = Math.round((safeMs / 1000) * 1000) / 1000
  return roundedSeconds.toFixed(3).replace(/\.?0+$/, '')
}

export function formatMillisecondsAsSecondsLabel(valueMs: number | null | undefined, fallbackMs = 0): string {
  return `${formatMillisecondsAsSecondsValue(valueMs, fallbackMs)}s`
}

export function parseSecondsInputAsMilliseconds(value: string, minSeconds = 0): number | null | undefined {
  if (value.trim() === '') return undefined
  const seconds = Number(value)
  if (!Number.isFinite(seconds)) return null
  const milliseconds = Math.round(Math.max(minSeconds, seconds) * 1000)
  // JSON stringifies Infinity/NaN as null, and integers above the safe range
  // silently lose millisecond precision. Keep an invalid draft local instead
  // of corrupting the persisted runtime setting through the change callback.
  if (!Number.isFinite(milliseconds) || !Number.isSafeInteger(milliseconds)) return null
  return milliseconds
}

export function formatMillisecondsAsSecondsDuration(valueMs: number | null | undefined, fallbackMs = 0): string {
  const seconds = formatMillisecondsAsSecondsValue(valueMs, fallbackMs)
  return `${seconds} ${seconds === '1' ? 'second' : 'seconds'}`
}

export function formatMillisecondsRangeAsSecondsDuration(
  minMs: number | null | undefined,
  maxMs: number | null | undefined,
): string {
  return `${formatMillisecondsAsSecondsValue(minMs)}–${formatMillisecondsAsSecondsValue(maxMs)} seconds`
}

export function formatPacingTiming(pacing?: RuntimePacingTiming | null): string | null {
  if (!pacing?.type) return null
  if (pacing.type === 'none') return 'None'
  if (pacing.type === 'fixed_delay') {
    return `Fixed delay · ${formatMillisecondsAsSecondsDuration(pacing.delay_ms)}`
  }
  if (pacing.type === 'fixed_interval') {
    return `Fixed interval · ${formatMillisecondsAsSecondsDuration(pacing.interval_ms)}`
  }
  if (pacing.type === 'random_delay') {
    return `Random delay · ${formatMillisecondsRangeAsSecondsDuration(pacing.min_delay_ms, pacing.max_delay_ms)}`
  }
  if (pacing.type === 'random_interval') {
    return `Random interval · ${formatMillisecondsRangeAsSecondsDuration(pacing.min_interval_ms, pacing.max_interval_ms)}`
  }
  if (pacing.type === 'tps') return `Target TPS · ${pacing.tps || 0}`
  return pacing.type.replace(/_/g, ' ')
}
