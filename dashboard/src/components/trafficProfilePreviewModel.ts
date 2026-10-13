import type { TrafficProfile, TrafficProfileMapping } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export const trafficProfilePreviewPointLimit = 1000

export interface TrafficProfileTpsStatistics {
  minimum: number
  maximum: number
  average: number
}

/**
 * Axis ticks in the same notation as the phases table beneath the chart
 * ("1m3s", "26s", "1h30m"), never fractional minutes (0.43m).
 */
export function formatTrafficProfileAxisTime(elapsedSeconds: number): string {
  const parsedSeconds = Number(elapsedSeconds)
  const whole = Number.isFinite(parsedSeconds) ? Math.max(0, Math.round(parsedSeconds)) : 0
  const hours = Math.floor(whole / 3600)
  const minutes = Math.floor((whole % 3600) / 60)
  const seconds = whole % 60
  if (hours > 0) return `${hours}h${minutes ? `${minutes}m` : ''}`
  if (minutes > 0) return `${minutes}m${seconds ? `${seconds}s` : ''}`
  return `${seconds}s`
}

function normalizedGroupName(value?: string): string {
  return (value || '').trim().toLowerCase()
}

export function trafficProfileMappingForGroup(profile: TrafficProfile | undefined, groupName?: string): TrafficProfileMapping | undefined {
  if (!profile || !groupName) return undefined
  const key = normalizedGroupName(groupName)
  return profile.mappings.find(mapping => normalizedGroupName(mapping.group_name) === key)
}

export function trafficProfileTargetAt(mapping: TrafficProfileMapping | undefined, elapsedSeconds: number): number {
  const points = mapping?.points || []
  if (!points.length || elapsedSeconds * 1000 < points[0].offset_ms) return 0
  const elapsedMilliseconds = elapsedSeconds * 1000
  let low = 0
  let high = points.length - 1
  while (low < high) {
    const middle = Math.ceil((low + high) / 2)
    if (points[middle].offset_ms <= elapsedMilliseconds) low = middle
    else high = middle - 1
  }
  return Math.max(0, Number(points[low].target) || 0)
}

export function trafficProfileTpsStatistics(
  mappings: Array<TrafficProfileMapping | undefined>,
  durationSeconds: number,
): TrafficProfileTpsStatistics {
  const activeMappings = mappings.filter((mapping): mapping is TrafficProfileMapping => Boolean(mapping?.points.length))
  if (!activeMappings.length || !Number.isFinite(durationSeconds) || durationSeconds <= 0) return { minimum: 0, maximum: 0, average: 0 }

  const targetDeltas = new Map<number, number>()
  activeMappings.forEach(mapping => {
    let previousTarget = 0
    mapping.points.forEach(point => {
      const second = Math.max(0, point.offset_ms / 1000)
      const target = Math.max(0, Number(point.target) || 0)
      if (second < durationSeconds) {
        targetDeltas.set(second, (targetDeltas.get(second) || 0) + target - previousTarget)
      }
      previousTarget = target
    })
  })

  const boundaries = Array.from(new Set([0, durationSeconds, ...targetDeltas.keys()])).sort((left, right) => left - right)
  let minimum = Number.POSITIVE_INFINITY
  let maximum = 0
  let weightedTotal = 0
  let currentTarget = 0
  for (let index = 0; index < boundaries.length - 1; index += 1) {
    const start = boundaries[index]
    currentTarget += targetDeltas.get(start) || 0
    const interval = boundaries[index + 1] - start
    const target = Math.max(0, currentTarget)
    minimum = Math.min(minimum, target)
    maximum = Math.max(maximum, target)
    weightedTotal += target * interval
  }

  return {
    minimum: Number.isFinite(minimum) ? minimum : 0,
    maximum,
    average: weightedTotal / durationSeconds,
  }
}

// A profile is replaced, never edited in place, so its peak is computed once:
// lists show it for every scenario on every render and sort by it.
const profilePeaks = new WeakMap<TrafficProfile, number>()

/** The highest summed target of all mapped groups, in the profile's runtime unit (TPS or VUsers). */
export function trafficProfilePeak(profile: TrafficProfile | undefined): number {
  if (!profile?.mappings.length) return 0
  const cached = profilePeaks.get(profile)
  if (cached !== undefined) return cached
  const lastOffsetSeconds = profile.mappings.reduce((latest, mapping) => mapping.points.reduce((value, point) => Math.max(value, point.offset_ms / 1000), latest), 0)
  const peak = trafficProfileTpsStatistics(profile.mappings, Math.max(parseDurationToSeconds(profile.target_duration), lastOffsetSeconds + 1)).maximum
  profilePeaks.set(profile, peak)
  return peak
}

export function trafficProfileTimelineSeconds(profile: TrafficProfile | undefined, limit = trafficProfilePreviewPointLimit): number[] {
  if (!profile || limit < 2) return []
  const durationSeconds = Math.max(0, parseDurationToSeconds(profile.target_duration))
  const offsets = new Set<number>([0, durationSeconds])
  const required = new Set<number>([0, durationSeconds])

  profile.mappings.forEach(mapping => {
    let minimum = mapping.points[0]
    let maximum = mapping.points[0]
    mapping.points.forEach(point => {
      const second = Math.max(0, Math.min(durationSeconds, point.offset_ms / 1000))
      offsets.add(second)
      if (!minimum || point.target < minimum.target) minimum = point
      if (!maximum || point.target > maximum.target) maximum = point
    })
    if (minimum) required.add(Math.max(0, Math.min(durationSeconds, minimum.offset_ms / 1000)))
    if (maximum) required.add(Math.max(0, Math.min(durationSeconds, maximum.offset_ms / 1000)))
  })

  const sorted = Array.from(offsets).sort((left, right) => left - right)
  if (sorted.length <= limit) return sorted

  const requiredSorted = Array.from(required).sort((left, right) => left - right)
  if (requiredSorted.length >= limit) {
    const result = new Set<number>()
    for (let index = 0; index < limit; index += 1) {
      const requiredIndex = Math.round((index * (requiredSorted.length - 1)) / (limit - 1))
      result.add(requiredSorted[requiredIndex])
    }
    return Array.from(result).sort((left, right) => left - right)
  }

  const result = new Set(requiredSorted)
  const candidates = sorted.filter(second => !result.has(second))
  const budget = Math.max(0, limit - result.size)
  for (let index = 0; index < budget; index += 1) {
    const candidateIndex = budget === 1 ? 0 : Math.round((index * (candidates.length - 1)) / (budget - 1))
    result.add(candidates[candidateIndex])
  }
  return Array.from(result).sort((left, right) => left - right)
}

// Mirrors trafficprofile.ParseRelativeTimestamp: a Go duration ("60s",
// "1m30s") or elapsed H:MM[:SS].
const RELATIVE_TIME = /^(?:(?:\d+(?:\.\d+)?(?:ns|us|µs|ms|s|m|h))+|\d+:[0-5]\d(?::[0-5]\d(?:\.\d+)?)?)$/

/** The time mode the sampled values imply, or undefined when they do not decide it. */
export function inferTimestampMode(samples: string[][] | undefined, columns: string[], column: string): 'absolute' | 'relative' | undefined {
  const index = columns.indexOf(column)
  if (index < 0) return undefined
  const values = (samples || []).map(row => (row[index] || '').trim()).filter(Boolean)
  if (values.length === 0) return undefined
  if (values.every(value => RELATIVE_TIME.test(value))) return 'relative'
  if (values.every(value => !Number.isNaN(Date.parse(value)) && /\d{4}-\d{2}-\d{2}/.test(value))) return 'absolute'
  return undefined
}

// Column-name words that mean a response time or a duration, never a clock
// time or a workload count: "response_time", "avg_rt_ms", "Latency".
const DURATION_COLUMN_WORDS = new Set(['art', 'rt', 'latency', 'response', 'resp', 'duration', 'ms', 'millis', 'milliseconds', 'seconds', 'secs'])
const TIME_COLUMN_WORDS = new Set(['timestamp', 'time', 'date', 'datetime', 'ts', 'elapsed', 'period'])

function columnWords(column: string): string[] {
  return column.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean)
}

function namesDuration(column: string): boolean {
  return columnWords(column).some(word => DURATION_COLUMN_WORDS.has(word))
}

/**
 * The column that most likely holds the timestamps: a time- or date-named
 * column whose samples read as times, else the first such name, else the
 * first column whose samples read as dates or elapsed times. Empty when
 * nothing qualifies.
 */
export function suggestTimestampColumn(columns: string[], samples: string[][] | undefined): string {
  const named = columns.filter(column => !namesDuration(column) && columnWords(column).some(word => TIME_COLUMN_WORDS.has(word)))
  return named.find(column => inferTimestampMode(samples, columns, column))
    || named[0]
    || columns.find(column => inferTimestampMode(samples, columns, column))
    || ''
}

/** The numeric columns to replay as workload: not the excluded ones, and not response times or durations. */
export function suggestValueColumns(columns: string[], samples: string[][] | undefined, exclude: string[]): string[] {
  return columns.filter((column, index) => {
    if (exclude.includes(column) || namesDuration(column)) return false
    const values = (samples || []).map(row => (row[index] || '').trim()).filter(Boolean)
    return values.length > 0 && values.every(value => Number.isFinite(Number(value)))
  })
}

/**
 * Each group's series: a choice that still exists stays (an empty choice is
 * a deliberate "none"), a group named like a series takes it, and a single
 * series goes to every group. Other groups are left for the author to
 * choose: a guess by position would replay another group's traffic without
 * saying so.
 */
export function suggestGroupSeries(groupNames: string[], series: Array<{ key: string; name: string }>, current: Record<string, string>): Record<string, string> {
  const keys = new Set(series.map(item => item.key))
  const next: Record<string, string> = {}
  groupNames.forEach(name => {
    if (Object.prototype.hasOwnProperty.call(current, name) && (current[name] === '' || keys.has(current[name]))) next[name] = current[name]
    else if (series.length === 1) next[name] = series[0].key
    else {
      const match = series.find(item => [item.name, item.key].some(value => normalizedGroupName(value) === normalizedGroupName(name)))
      if (match) next[name] = match.key
    }
  })
  return next
}
