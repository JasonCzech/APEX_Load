import type { MetricsHistoryPoint } from '../../types'

// Keep original observations, including each bucket's extrema for every visible
// series. A time bucket is wider as the visible window grows. No averages or
// invented values are substituted for retained observations.
export function sampleTimeline(data: MetricsHistoryPoint[], keys: string[], width: number): MetricsHistoryPoint[] {
  const budget = Math.max(32, keys.length * 4 + 2, Math.min(2000, Math.floor(width * 2)))
  if (data.length <= budget || keys.length === 0) return data
  const buckets = Math.max(1, Math.floor((budget - 2 - 2 * keys.length) / (2 * keys.length)))
  const first = data[0].timestampMs
  const last = data[data.length - 1].timestampMs
  const timed = data.every(point => Number.isFinite(point.timestampMs)) && last! > first!
  const endpoints = new Map<string, [number, number]>()
  const groups = Array.from({ length: buckets }, () => new Map<string, [number, number]>())
  data.forEach((point, index) => {
    const fraction = timed && Number.isFinite(point.timestampMs) ? (point.timestampMs! - first!) / (last! - first!) : index / (data.length - 1)
    const group = groups[Math.max(0, Math.min(buckets - 1, Math.floor(fraction * buckets)))]
    for (const key of keys) {
      const value = point[key]
      if (typeof value !== 'number' || !Number.isFinite(value)) continue
      const endpoint = endpoints.get(key)
      if (endpoint) endpoint[1] = index
      else endpoints.set(key, [index, index])
      const range = group.get(key)
      if (!range) group.set(key, [index, index])
      else {
        if (value < (data[range[0]][key] as number)) range[0] = index
        if (value >= (data[range[1]][key] as number)) range[1] = index
      }
    }
  })
  const selected = new Set([0, data.length - 1])
  for (const [first, last] of endpoints.values()) { selected.add(first); selected.add(last) }
  for (const group of groups) for (const [min, max] of group.values()) { selected.add(min); selected.add(max) }
  return [...selected].sort((a, b) => a - b).map(index => data[index])
}

export function parseTimelineTime(value: string, mode: 'relative' | 'clock', origin: number): number | null {
  const text = value.trim()
  if (mode === 'clock') {
    // Clock-only input uses the run's UTC date; ISO input specifies its date and zone.
    const clock = /^(\d{2}):(\d{2})(?::(\d{2}(?:\.\d{1,3})?))?$/.exec(text)
    if (clock) {
      if (+clock[1] > 23 || +clock[2] > 59 || +(clock[3] ?? 0) >= 60) return null
      const date = new Date(origin)
      date.setUTCHours(+clock[1], +clock[2], 0, 0)
      return date.getTime() + +(clock[3] ?? 0) * 1000
    }
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})$/.test(text)) return null
    const date = text.slice(0, 10)
    const day = new Date(`${date}T00:00:00Z`)
    if (!Number.isFinite(day.getTime()) || day.toISOString().slice(0, 10) !== date) return null
    const timestamp = Date.parse(text)
    return Number.isFinite(timestamp) ? timestamp : null
  }
  const duration = /^(\d+(?:\.\d+)?)(ms|s|m|h)?$/.exec(text)
  if (duration) {
    const units: Record<string, number> = { ms: 1, s: 1000, m: 60000, h: 3600000 }
    const result = origin + +duration[1] * units[duration[2] ?? 's']
    return Number.isFinite(result) ? result : null
  }
  const parts = /^(\d+):(\d{2})(?::(\d{2}(?:\.\d+)?))?$/.exec(text)
  if (!parts || +parts[2] >= 60 || +(parts[3] ?? 0) >= 60) return null
  const seconds = parts[3] === undefined ? +parts[1] * 60 + +parts[2] : +parts[1] * 3600 + +parts[2] * 60 + +parts[3]
  const result = origin + seconds * 1000
  return Number.isFinite(result) ? result : null
}

// Manual time entry needs a chronological clock for every row. Legacy history
// without timestamps can still use the categorical chart and index sliders.
export function timelineTimeBounds(data: MetricsHistoryPoint[]): [number, number] | null {
  if (!data.length || data.some((point, index) => !Number.isFinite(point.timestampMs) || (index > 0 && point.timestampMs! < data[index - 1].timestampMs!))) return null
  return [data[0].timestampMs!, data[data.length - 1].timestampMs!]
}

export function timelineTimeWindow(data: MetricsHistoryPoint[], start: number, end: number, runStart?: number): [number, number] | null {
  const bounds = timelineTimeBounds(data)
  if (!bounds) return null
  const [first, last] = bounds
  const minimum = Number.isFinite(runStart) ? Math.min(runStart!, first) : first
  if (!Number.isFinite(start) || !Number.isFinite(end) || start > end || start < minimum || end < first || end > last) return null
  // Include bracketing observations and every row sharing either boundary's
  // timestamp: summary and resource rows may carry distinct metrics at one time.
  let left = 0
  while (left + 1 < data.length && data[left + 1].timestampMs! <= start) left++
  while (left > 0 && data[left - 1].timestampMs === data[left].timestampMs) left--
  let right = left
  while (right < data.length - 1 && data[right].timestampMs! < end) right++
  while (right + 1 < data.length && data[right + 1].timestampMs === data[right].timestampMs) right++
  return [left, right]
}

// Different lanes retain different extrema; synchronize to the closest actual
// observation rather than requiring an exact match in their sampled arrays.
export function nearestTimelineTick(ticks: ReadonlyArray<{ value?: unknown }>, state: { activeLabel?: string | number }): number {
  const exact = ticks.findIndex(tick => String(tick.value) === String(state.activeLabel))
  if (exact >= 0) return exact
  const target = Number(state.activeLabel)
  if (!Number.isFinite(target)) return -1
  let best = -1
  let distance = Infinity
  ticks.forEach((tick, index) => {
    const delta = Math.abs(Number(tick.value) - target)
    if (delta < distance) { best = index; distance = delta }
  })
  return best
}
