export type DurationUnit = 's' | 'm' | 'h'

export interface DurationFieldValue {
  value: string
  unit: DurationUnit
}

interface ParsedDuration {
  seconds: number
  nanoseconds: bigint
  valid: boolean
}

const durationTokenPattern = /([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h)/g
const maxDurationMagnitudeNanoseconds = 1n << 63n
const maxPositiveDurationNanoseconds = maxDurationMagnitudeNanoseconds - 1n
const durationUnitNanoseconds: Record<string, bigint> = {
  ns: 1n,
  us: 1_000n,
  'µs': 1_000n,
  'μs': 1_000n,
  ms: 1_000_000n,
  s: 1_000_000_000n,
  m: 60_000_000_000n,
  h: 3_600_000_000_000n,
}
const durationFieldDecimalPattern = /^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$/

function invalidDuration(): ParsedDuration {
  return { seconds: 0, nanoseconds: 0n, valid: false }
}

function formatPlainNumber(value: number): string {
  const text = String(value)
  const match = text.match(/^(-?)(\d+)(?:\.(\d+))?e([+-]?\d+)$/i)
  if (!match) return text

  const [, sign, integer, fraction = '', rawExponent] = match
  const digits = `${integer}${fraction}`
  const decimalIndex = integer.length + Number(rawExponent)
  if (decimalIndex <= 0) {
    return `${sign}0.${'0'.repeat(-decimalIndex)}${digits}`
  }
  if (decimalIndex >= digits.length) {
    return `${sign}${digits}${'0'.repeat(decimalIndex - digits.length)}`
  }
  return `${sign}${digits.slice(0, decimalIndex)}.${digits.slice(decimalIndex)}`
}

function normalizeDecimalText(value: string): string {
  const [rawInteger, rawFraction] = value.split('.', 2)
  const integer = rawInteger.replace(/^0+(?=\d)/, '') || '0'
  if (rawFraction === undefined) return integer
  const fraction = rawFraction.replace(/0+$/, '')
  return fraction ? `${integer}.${fraction}` : integer
}

function parseUnsignedInteger(digits: string): bigint | null {
  let value = 0n
  for (const digit of digits) {
    if (value > maxDurationMagnitudeNanoseconds / 10n) return null
    value = value * 10n + BigInt(digit)
    if (value > maxDurationMagnitudeNanoseconds) return null
  }
  return value
}

function parseFraction(digits: string): { value: bigint, scale: bigint } {
  let value = 0n
  let scale = 1n
  for (const digit of digits) {
    value = value * 10n + BigInt(digit)
    scale *= 10n
  }
  return { value, scale }
}

function parseDurationTokens(input?: string): ParsedDuration {
  const text = input === undefined || input === null ? '' : String(input)
  if (!text) return invalidDuration()

  let position = text.startsWith('+') ? 1 : 0
  if (position === text.length || text.startsWith('-')) return invalidDuration()

  let totalNanoseconds = 0n
  let hasMatch = false
  durationTokenPattern.lastIndex = 0
  durationTokenPattern.lastIndex = position

  let match: RegExpExecArray | null
  while ((match = durationTokenPattern.exec(text)) !== null) {
    if (match.index !== position) {
      return invalidDuration()
    }
    hasMatch = true
    const numeric = match[1]
    const unit = match[2]
    const decimalIndex = numeric.indexOf('.')
    const integerDigits = decimalIndex >= 0 ? numeric.slice(0, decimalIndex) : numeric
    const fractionDigits = decimalIndex >= 0 ? numeric.slice(decimalIndex + 1) : ''
    const integer = parseUnsignedInteger(integerDigits)
    const unitNanoseconds = durationUnitNanoseconds[unit]
    if (integer === null || integer > maxDurationMagnitudeNanoseconds / unitNanoseconds) {
      return invalidDuration()
    }

    let componentNanoseconds = integer * unitNanoseconds
    if (fractionDigits) {
      const fraction = parseFraction(fractionDigits)
      // Go's time.ParseDuration truncates positive fractional components toward
      // zero at nanosecond precision. Keep the full decimal ratio in bigint so a
      // long but legal fraction never crosses Number's Infinity/NaN boundary and
      // late digits can still decide the final nanosecond exactly.
      componentNanoseconds += fraction.value * unitNanoseconds / fraction.scale
      if (componentNanoseconds > maxDurationMagnitudeNanoseconds) {
        return invalidDuration()
      }
    }
    totalNanoseconds += componentNanoseconds
    if (totalNanoseconds > maxDurationMagnitudeNanoseconds) {
      return invalidDuration()
    }
    position = match.index + match[0].length
  }

  if (!hasMatch || position !== text.length || totalNanoseconds > maxPositiveDurationNanoseconds) {
    return invalidDuration()
  }

  return {
    seconds: Number(totalNanoseconds) / Number(durationUnitNanoseconds.s),
    nanoseconds: totalNanoseconds,
    valid: totalNanoseconds > 0n,
  }
}

export function parseDurationToSeconds(input?: string): number {
  const parsed = parseDurationTokens(input)
  return parsed.valid ? parsed.seconds : 0
}

// Returns the exact positive duration used by Go's time.Duration domain. UI
// validation that compares two durations should use this representation rather
// than floating-point seconds, whose precision falls below one nanosecond for
// sufficiently large (but still valid) durations.
export function parseDurationToNanoseconds(input?: string): bigint | null {
  const parsed = parseDurationTokens(input)
  return parsed.valid ? parsed.nanoseconds : null
}

export function isValidDurationInput(input?: string, allowEmpty = false): boolean {
  if (input === undefined || input === null || input === '') return allowEmpty
  return parseDurationTokens(input).valid
}

export function serializeDurationFieldValue(value: number | string, unit: DurationUnit): string {
  if (value === '') return ''

  let numericText: string
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return ''
    numericText = formatPlainNumber(value)
  } else if (durationFieldDecimalPattern.test(value)) {
    numericText = normalizeDecimalText(value)
  } else {
    if (value !== value.trim()) return ''
    const numericValue = Number(value)
    if (!Number.isFinite(numericValue) || numericValue <= 0) return ''
    numericText = formatPlainNumber(numericValue)
  }

  const serialized = `${numericText}${unit}`
  return parseDurationTokens(serialized).valid ? serialized : ''
}

export function splitDurationInput(input?: string, fallbackUnit: DurationUnit = 'm'): DurationFieldValue {
  const parsed = parseDurationTokens(input)
  if (!parsed.valid) {
    return { value: '', unit: fallbackUnit }
  }
  if (parsed.nanoseconds % durationUnitNanoseconds.h === 0n) {
    return { value: String(parsed.nanoseconds / durationUnitNanoseconds.h), unit: 'h' }
  }
  if (parsed.nanoseconds % durationUnitNanoseconds.m === 0n) {
    return { value: String(parsed.nanoseconds / durationUnitNanoseconds.m), unit: 'm' }
  }
  if (parsed.nanoseconds % durationUnitNanoseconds.s === 0n) {
    return { value: String(parsed.nanoseconds / durationUnitNanoseconds.s), unit: 's' }
  }

  const wholeSeconds = parsed.nanoseconds / durationUnitNanoseconds.s
  const fractionalNanoseconds = String(parsed.nanoseconds % durationUnitNanoseconds.s)
    .padStart(9, '0')
    .replace(/0+$/, '')
  return { value: `${wholeSeconds}.${fractionalNanoseconds}`, unit: 's' }
}

export function formatTimeCompact(seconds: number): string {
  if (!seconds || seconds <= 0) return '0s';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;

  if (h > 0) return `${h}h${m > 0 ? `${m}m` : ''}${s > 0 ? `${s}s` : ''}`;
  if (m > 0) return `${m}m${s > 0 ? `${s}s` : ''}`;
  return `${s}s`;
}

export function formatTimeLong(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) {
    const s = seconds % 60;
    return s > 0 ? `${m}m ${s}s` : `${m}m`;
  }
  const h = Math.floor(m / 60);
  const remM = m % 60;
  return remM > 0 ? `${h}h ${remM}m` : `${h}h`;
}
