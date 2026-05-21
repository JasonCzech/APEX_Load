export type DurationUnit = 's' | 'm' | 'h'

export interface DurationFieldValue {
  value: number | ''
  unit: DurationUnit
}

const durationTokenPattern = /(\d+(?:\.\d+)?)\s*(ms|h|m|s)/g

function parseDurationTokens(input?: string): { seconds: number, valid: boolean } {
  const text = String(input || '').trim().toLowerCase()
  if (!text) return { seconds: 0, valid: false }

  let totalSeconds = 0
  let hasMatch = false
  let lastIndex = 0
  durationTokenPattern.lastIndex = 0

  let match: RegExpExecArray | null
  while ((match = durationTokenPattern.exec(text)) !== null) {
    if (text.slice(lastIndex, match.index).trim() !== '') {
      return { seconds: 0, valid: false }
    }
    hasMatch = true
    const value = Number(match[1])
    const unit = match[2]
    if (!Number.isFinite(value) || value < 0) {
      return { seconds: 0, valid: false }
    }

    if (unit === 'h') totalSeconds += value * 3600
    else if (unit === 'm') totalSeconds += value * 60
    else if (unit === 's') totalSeconds += value
    else if (unit === 'ms') totalSeconds += value / 1000
    lastIndex = match.index + match[0].length
  }

  if (!hasMatch || text.slice(lastIndex).trim() !== '') {
    return { seconds: 0, valid: false }
  }

  return { seconds: Math.ceil(totalSeconds), valid: totalSeconds > 0 }
}

export function parseDurationToSeconds(input?: string): number {
  const parsed = parseDurationTokens(input)
  return parsed.valid ? parsed.seconds : 0
}

export function isValidDurationInput(input?: string, allowEmpty = false): boolean {
  if (!String(input || '').trim()) return allowEmpty
  return parseDurationTokens(input).valid
}

export function serializeDurationFieldValue(value: number | '', unit: DurationUnit): string {
  if (value === '' || !Number.isFinite(Number(value)) || Number(value) <= 0) return ''
  return `${Number(value)}${unit}`
}

export function splitDurationInput(input?: string, fallbackUnit: DurationUnit = 'm'): DurationFieldValue {
  const seconds = parseDurationToSeconds(input)
  if (seconds <= 0) {
    return { value: '', unit: fallbackUnit }
  }
  if (seconds % 3600 === 0) {
    return { value: seconds / 3600, unit: 'h' }
  }
  if (seconds % 60 === 0) {
    return { value: seconds / 60, unit: 'm' }
  }
  return { value: seconds, unit: 's' }
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
