import { describe, expect, it } from 'vitest'

import type { LoadImpactPoint } from './loadImpactModel'
import { sortLoadImpactDetailPoints } from './loadImpactHeatmapUtils'

function point(time: string, value: number, vusers: number, extra: Record<string, number | undefined> = {}): LoadImpactPoint {
  return {
    time,
    value,
    vusers,
    source: { time, vusers, ...extra } as LoadImpactPoint['source'],
  }
}

describe('sortLoadImpactDetailPoints', () => {
  const points = [
    point('2026-05-12T20:00:02Z', 140, 10, { p95: 180, netSent: 4, netRecv: 6 }),
    point('2026-05-12T20:00:01Z', 100, 5, { p95: 130, netSent: 2, netRecv: 3 }),
    point('2026-05-12T20:00:03Z', 120, 8),
  ]

  it('sorts timestamps chronologically and leaves the source array unchanged', () => {
    const sorted = sortLoadImpactDetailPoints(points, { key: 'timestamp', direction: 'asc' })

    expect(sorted.map(item => item.time)).toEqual([
      '2026-05-12T20:00:01Z',
      '2026-05-12T20:00:02Z',
      '2026-05-12T20:00:03Z',
    ])
    expect(points[0].time).toBe('2026-05-12T20:00:02Z')
  })

  it('sorts raw numeric values and keeps missing values at the bottom in either direction', () => {
    expect(sortLoadImpactDetailPoints(points, { key: 'p95', direction: 'desc' }).map(item => item.time)).toEqual([
      '2026-05-12T20:00:02Z',
      '2026-05-12T20:00:01Z',
      '2026-05-12T20:00:03Z',
    ])
    expect(sortLoadImpactDetailPoints(points, { key: 'net', direction: 'asc' }).map(item => item.time)).toEqual([
      '2026-05-12T20:00:01Z',
      '2026-05-12T20:00:02Z',
      '2026-05-12T20:00:03Z',
    ])
  })
})
