import type { MetricsHistoryPoint } from '../types'

export function aggregateDashboardTpsHistory(
  testIds: readonly string[],
  histories: Record<string, MetricsHistoryPoint[]>,
  maxPoints = 60,
): MetricsHistoryPoint[] {
  const buckets = new Map<string, { order: number; point: MetricsHistoryPoint }>()
  for (const testId of testIds) {
    const points = histories[testId]?.slice(-maxPoints) || []
    for (const [index, point] of points.entries()) {
      const parsedTimestamp = point.timestampMs ?? Date.parse(point.time)
      const absolute = Number.isFinite(parsedTimestamp)
      // Legacy live points expose only a localized clock label. Right-align
      // them so each test's newest sample represents the same live interval.
      const order = absolute ? Math.floor(parsedTimestamp / 1000) * 1000 : index - points.length
      const key = `${absolute ? 'absolute' : 'relative'}:${order}`
      const current = buckets.get(key)
      if (current) {
        current.point.tps += Number.isFinite(point.tps) ? point.tps : 0
        current.point.vusers += Number.isFinite(point.vusers) ? point.vusers : 0
      } else {
        buckets.set(key, {
          order,
          point: {
            ...point,
            timestampMs: absolute ? order : undefined,
            time: absolute ? new Date(order).toISOString() : point.time,
            tps: Number.isFinite(point.tps) ? point.tps : 0,
            vusers: Number.isFinite(point.vusers) ? point.vusers : 0,
          },
        })
      }
    }
  }
  return Array.from(buckets.values())
    .sort((left, right) => left.order - right.order)
    .slice(-maxPoints)
    .map(bucket => bucket.point)
}
