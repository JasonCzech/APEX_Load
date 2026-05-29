import { MetricsHistoryPoint } from '../types'

export type LoadImpactMetricCategory = 'performance' | 'resource'

export interface LoadImpactMetric {
  id: string
  key: string
  label: string
  unit?: string
  category: LoadImpactMetricCategory
}

export interface LoadImpactBucket {
  id: string
  label: string
  minVusers: number
  maxVusers: number
  exact: boolean
  sampleCount: number
}

export interface LoadImpactPoint {
  time: string
  timestampMs?: number
  vusers: number
  value: number
  source: MetricsHistoryPoint
}

export interface LoadImpactCell {
  metric: LoadImpactMetric
  bucket: LoadImpactBucket
  average?: number
  min?: number
  max?: number
  sampleCount: number
  intensity: number
  points: LoadImpactPoint[]
}

export interface LoadImpactRow {
  metric: LoadImpactMetric
  cells: LoadImpactCell[]
  minAverage?: number
  maxAverage?: number
}

export interface LoadImpactModel {
  buckets: LoadImpactBucket[]
  rows: LoadImpactRow[]
  pointCount: number
}

export const LOAD_IMPACT_METRICS: LoadImpactMetric[] = [
  { id: 'avgMs', key: 'avgMs', label: 'ART', unit: 'ms', category: 'performance' },
  { id: 'p50', key: 'p50', label: 'P50', unit: 'ms', category: 'performance' },
  { id: 'p90', key: 'p90', label: 'P90', unit: 'ms', category: 'performance' },
  { id: 'p95', key: 'p95', label: 'P95', unit: 'ms', category: 'performance' },
  { id: 'p99', key: 'p99', label: 'P99', unit: 'ms', category: 'performance' },
  { id: 'tps', key: 'tps', label: 'TPS', category: 'performance' },
  { id: 'errorPct', key: 'errorPct', label: 'Error %', unit: '%', category: 'performance' },
  { id: 'cpuProcess', key: 'cpuProcess', label: 'CPU VUsers', unit: '%', category: 'resource' },
  { id: 'cpuFree', key: 'cpuFree', label: 'CPU Free', unit: '%', category: 'resource' },
  { id: 'memProcess', key: 'memProcess', label: 'Memory VUsers', unit: 'MB', category: 'resource' },
  { id: 'memFree', key: 'memFree', label: 'Memory Free', unit: 'MB', category: 'resource' },
  { id: 'netSent', key: 'netSent', label: 'Network Egress', unit: 'KB/s', category: 'resource' },
  { id: 'netRecv', key: 'netRecv', label: 'Network Ingress', unit: 'KB/s', category: 'resource' },
  { id: 'diskRead', key: 'diskRead', label: 'Disk Read', unit: 'KB/s', category: 'resource' },
  { id: 'diskWrite', key: 'diskWrite', label: 'Disk Write', unit: 'KB/s', category: 'resource' },
]

export function loadImpactCellKey(cell: LoadImpactCell) {
  return `${cell.metric.id}:${cell.bucket.id}`
}

export function formatLoadImpactCellTitle(cell: LoadImpactCell) {
  if (cell.sampleCount === 0) {
    return `${cell.bucket.label}: no ${cell.metric.label} samples`
  }
  return [
    `${cell.bucket.label}`,
    `${cell.metric.label} avg ${formatLoadImpactValue(cell.average, cell.metric.unit)}`,
    `min ${formatLoadImpactValue(cell.min, cell.metric.unit)}`,
    `max ${formatLoadImpactValue(cell.max, cell.metric.unit)}`,
    `${cell.sampleCount} sample${cell.sampleCount === 1 ? '' : 's'}`,
  ].join(' | ')
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function formatVuserLabel(minVusers: number, maxVusers = minVusers) {
  const suffix = maxVusers === 1 ? 'VUser' : 'VUsers'
  if (minVusers === maxVusers) {
    return `${minVusers} ${suffix}`
  }
  return `${minVusers}-${maxVusers} VUsers`
}

function bucketId(minVusers: number, maxVusers: number) {
  return minVusers === maxVusers ? `vusers-${minVusers}` : `vusers-${minVusers}-${maxVusers}`
}

function createBuckets(points: MetricsHistoryPoint[], maxBuckets: number): LoadImpactBucket[] {
  const loads = points
    .map(point => Math.round(point.vusers))
    .filter(load => Number.isFinite(load) && load > 0)
    .sort((left, right) => left - right)
  const distinctLoads = Array.from(new Set(loads))

  if (distinctLoads.length === 0) {
    return []
  }

  if (distinctLoads.length <= 10) {
    return distinctLoads.map(load => ({
      id: bucketId(load, load),
      label: formatVuserLabel(load),
      minVusers: load,
      maxVusers: load,
      exact: true,
      sampleCount: loads.filter(candidate => candidate === load).length,
    }))
  }

  const bucketCount = Math.min(Math.max(1, maxBuckets), distinctLoads.length)
  const minLoad = distinctLoads[0]
  const maxLoad = distinctLoads[distinctLoads.length - 1]
  const range = maxLoad - minLoad + 1

  return Array.from({ length: bucketCount }, (_, index) => {
    const minVusers = minLoad + Math.floor((index * range) / bucketCount)
    const maxVusers = index === bucketCount - 1
      ? maxLoad
      : minLoad + Math.floor(((index + 1) * range) / bucketCount) - 1
    return {
      id: bucketId(minVusers, maxVusers),
      label: formatVuserLabel(minVusers, maxVusers),
      minVusers,
      maxVusers,
      exact: false,
      sampleCount: loads.filter(load => load >= minVusers && load <= maxVusers).length,
    }
  })
}

function bucketForPoint(point: MetricsHistoryPoint, buckets: LoadImpactBucket[]) {
  const load = Math.round(point.vusers)
  return buckets.find(bucket => (
    bucket.exact ? load === bucket.minVusers : load >= bucket.minVusers && load <= bucket.maxVusers
  ))
}

function metricValue(metric: LoadImpactMetric, point: MetricsHistoryPoint): number | undefined {
  if (metric.category === 'performance' && point.resourceSampleOnly === 1) {
    return undefined
  }

  const value = point[metric.key]
  return isFiniteNumber(value) ? value : undefined
}

export function buildLoadImpactModel(
  points: MetricsHistoryPoint[] | undefined,
  maxBuckets = 8,
  metrics = LOAD_IMPACT_METRICS,
): LoadImpactModel {
  const validPoints = (points || []).filter(point => isFiniteNumber(point.vusers) && point.vusers > 0)
  const buckets = createBuckets(validPoints, maxBuckets)

  if (validPoints.length === 0 || buckets.length === 0) {
    return { buckets: [], rows: [], pointCount: 0 }
  }

  const bucketedPoints = new Map<string, MetricsHistoryPoint[]>()
  buckets.forEach(bucket => bucketedPoints.set(bucket.id, []))
  validPoints.forEach(point => {
    const bucket = bucketForPoint(point, buckets)
    if (bucket) {
      bucketedPoints.get(bucket.id)?.push(point)
    }
  })

  const rows = metrics
    .map((metric): LoadImpactRow | null => {
      const baseCells = buckets.map((bucket): LoadImpactCell => {
        const loadImpactPoints = (bucketedPoints.get(bucket.id) || [])
          .map((point): LoadImpactPoint | null => {
            const value = metricValue(metric, point)
            if (value === undefined) return null
            return {
              time: point.time,
              timestampMs: point.timestampMs,
              vusers: point.vusers,
              value,
              source: point,
            }
          })
          .filter((point): point is LoadImpactPoint => point !== null)
        const values = loadImpactPoints.map(point => point.value)
        const sampleCount = values.length
        const average = sampleCount > 0 ? values.reduce((sum, value) => sum + value, 0) / sampleCount : undefined

        return {
          metric,
          bucket,
          average,
          min: sampleCount > 0 ? Math.min(...values) : undefined,
          max: sampleCount > 0 ? Math.max(...values) : undefined,
          sampleCount,
          intensity: 0,
          points: loadImpactPoints,
        }
      })

      if (!baseCells.some(cell => cell.sampleCount > 0)) {
        return null
      }

      const averages = baseCells
        .map(cell => cell.average)
        .filter((value): value is number => value !== undefined)
      const minAverage = Math.min(...averages)
      const maxAverage = Math.max(...averages)
      const averageRange = maxAverage - minAverage
      const cells = baseCells.map(cell => ({
        ...cell,
        intensity: cell.average === undefined
          ? 0
          : averageRange === 0
            ? 0.65
            : (cell.average - minAverage) / averageRange,
      }))

      return { metric, cells, minAverage, maxAverage }
    })
    .filter((row): row is LoadImpactRow => row !== null)

  return { buckets, rows, pointCount: validPoints.length }
}

export function filterLoadImpactModel(
  model: LoadImpactModel,
  metricIds: string[],
  bucketIds: string[],
): LoadImpactModel {
  const selectedMetrics = new Set(metricIds)
  const selectedBuckets = new Set(bucketIds)
  const buckets = model.buckets.filter(bucket => selectedBuckets.has(bucket.id))

  const rows = model.rows
    .filter(row => selectedMetrics.has(row.metric.id))
    .map((row): LoadImpactRow => {
      const baseCells = row.cells
        .filter(cell => selectedBuckets.has(cell.bucket.id))
        .map(cell => ({ ...cell, intensity: 0 }))
      const averages = baseCells
        .map(cell => cell.average)
        .filter((value): value is number => value !== undefined)
      const minAverage = averages.length > 0 ? Math.min(...averages) : undefined
      const maxAverage = averages.length > 0 ? Math.max(...averages) : undefined
      const averageRange = minAverage !== undefined && maxAverage !== undefined ? maxAverage - minAverage : 0
      const cells = baseCells.map(cell => ({
        ...cell,
        intensity: cell.average === undefined || minAverage === undefined
          ? 0
          : averageRange === 0
            ? 0.65
            : (cell.average - minAverage) / averageRange,
      }))

      return { metric: row.metric, cells, minAverage, maxAverage }
    })
    .filter(row => row.cells.length > 0)

  return { buckets, rows, pointCount: model.pointCount }
}

export function formatLoadImpactValue(value: number | undefined, unit?: string): string {
  if (value === undefined || !Number.isFinite(value)) {
    return 'n/a'
  }

  const absolute = Math.abs(value)
  const maximumFractionDigits = unit === '%'
    ? 2
    : absolute >= 100
      ? 0
      : absolute >= 10
        ? 1
        : 2
  const formatted = value.toLocaleString(undefined, { maximumFractionDigits })

  if (!unit) return formatted
  if (unit === '%' || unit === 'ms') return `${formatted}${unit}`
  return `${formatted} ${unit}`
}
