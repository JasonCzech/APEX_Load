// cronBuilder.ts — a small bridge between a structured schedule model and a
// 5-field cron string. The raw cron string remains the source of truth in the
// schedule form; this module powers a visual builder layered on top. When an
// expression is too complex to represent (ranges in odd places, L/W/#, month
// restrictions, aliases), exprToCronModel returns null and the UI falls back to
// the raw text input.
//
// Invariant: cronModelToExpr always emits Sunday as 0 (never 7), matching the
// backend's canonical day-of-week numbering.

export type CronFrequency = 'minutes' | 'hourly' | 'daily' | 'weekly' | 'monthly'

export type CronModel = {
  frequency: CronFrequency
  everyN: number // step for 'minutes' (1-59) / 'hourly' (1-23)
  minute: number // 0-59
  hour: number // 0-23
  dayOfMonth: number // 1-31 for 'monthly'
  daysOfWeek: number[] // 0-6 (0 = Sunday) for 'weekly'
}

export const DEFAULT_CRON_MODEL: CronModel = {
  frequency: 'daily',
  everyN: 5,
  minute: 0,
  hour: 9,
  dayOfMonth: 1,
  daysOfWeek: [1, 2, 3, 4, 5],
}

const clampN = (value: number, lo: number, hi: number) => {
  if (!Number.isFinite(value)) return lo
  return Math.min(hi, Math.max(lo, Math.trunc(value)))
}

const canonicalDow = (d: number) => (d === 7 ? 0 : d)

export function cronModelToExpr(model: CronModel): string {
  const minute = clampN(model.minute, 0, 59)
  const hour = clampN(model.hour, 0, 23)
  switch (model.frequency) {
    case 'minutes':
      return `*/${clampN(model.everyN, 1, 59)} * * * *`
    case 'hourly':
      return `${minute} */${clampN(model.everyN, 1, 23)} * * *`
    case 'daily':
      return `${minute} ${hour} * * *`
    case 'weekly': {
      const days = model.daysOfWeek.length
        ? [...new Set(model.daysOfWeek.map(canonicalDow))].sort((a, b) => a - b).join(',')
        : '*'
      return `${minute} ${hour} * * ${days}`
    }
    case 'monthly':
      return `${minute} ${hour} ${clampN(model.dayOfMonth, 1, 31)} * *`
    default:
      return ''
  }
}

const MACRO_TO_MODEL: Record<string, CronModel> = {
  '@hourly': { ...DEFAULT_CRON_MODEL, frequency: 'hourly', minute: 0, everyN: 1 },
  '@daily': { ...DEFAULT_CRON_MODEL, frequency: 'daily', minute: 0, hour: 0 },
  '@midnight': { ...DEFAULT_CRON_MODEL, frequency: 'daily', minute: 0, hour: 0 },
  '@weekly': { ...DEFAULT_CRON_MODEL, frequency: 'weekly', minute: 0, hour: 0, daysOfWeek: [0] },
  '@monthly': { ...DEFAULT_CRON_MODEL, frequency: 'monthly', minute: 0, hour: 0, dayOfMonth: 1 },
}

const isInt = (s: string) => /^\d+$/.test(s)

function parseDayList(field: string): number[] | null {
  const out = new Set<number>()
  for (const token of field.split(',')) {
    const range = /^(\d+)-(\d+)$/.exec(token)
    if (range) {
      const a = parseInt(range[1], 10)
      const b = parseInt(range[2], 10)
      if (a > 7 || b > 7 || a > b) return null
      for (let d = a; d <= b; d++) out.add(canonicalDow(d))
    } else if (isInt(token)) {
      const d = parseInt(token, 10)
      if (d > 7) return null
      out.add(canonicalDow(d))
    } else {
      return null
    }
  }
  return [...out].sort((a, b) => a - b)
}

export function exprToCronModel(expr: string): CronModel | null {
  const trimmed = expr.trim()
  const macro = MACRO_TO_MODEL[trimmed.toLowerCase()]
  if (macro) return { ...macro, daysOfWeek: [...macro.daysOfWeek] }

  const parts = trimmed.split(/\s+/)
  if (parts.length !== 5) return null
  const [minF, hourF, domF, monF, dowF] = parts
  // The builder does not model month restrictions.
  if (monF !== '*') return null

  let m: RegExpExecArray | null
  if ((m = /^\*\/(\d+)$/.exec(minF)) && hourF === '*' && domF === '*' && dowF === '*') {
    return { ...DEFAULT_CRON_MODEL, frequency: 'minutes', everyN: parseInt(m[1], 10) }
  }
  if (isInt(minF) && (m = /^\*\/(\d+)$/.exec(hourF)) && domF === '*' && dowF === '*') {
    return { ...DEFAULT_CRON_MODEL, frequency: 'hourly', minute: parseInt(minF, 10), everyN: parseInt(m[1], 10) }
  }
  if (!isInt(minF) || !isInt(hourF)) return null
  const minute = parseInt(minF, 10)
  const hour = parseInt(hourF, 10)
  if (domF === '*' && dowF === '*') {
    return { ...DEFAULT_CRON_MODEL, frequency: 'daily', minute, hour }
  }
  if (domF === '*' && dowF !== '*') {
    const days = parseDayList(dowF)
    if (!days || days.length === 0) return null
    return { ...DEFAULT_CRON_MODEL, frequency: 'weekly', minute, hour, daysOfWeek: days }
  }
  if (dowF === '*' && isInt(domF)) {
    return { ...DEFAULT_CRON_MODEL, frequency: 'monthly', minute, hour, dayOfMonth: parseInt(domF, 10) }
  }
  return null
}
