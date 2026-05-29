import { describe, expect, it } from 'vitest'

import { MetricsHistoryPoint } from '../types'
import {
  buildLoadImpactModel,
  filterLoadImpactModel,
  formatLoadImpactCellTitle,
  formatLoadImpactValue,
  loadImpactCellKey,
} from './loadImpactModel'

describe('loadImpactModel', () => {
  it('uses exact VUser levels when there are 10 or fewer distinct loads', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 1, { avgMs: 100, tps: 2 }),
      point('10:00:01', 3, { avgMs: 120, tps: 4 }),
      point('10:00:02', 3, { avgMs: 140, tps: 5 }),
    ])

    expect(model.buckets.map(bucket => bucket.label)).toEqual(['1 VUser', '3 VUsers'])
    expect(model.buckets.map(bucket => bucket.sampleCount)).toEqual([1, 2])
  })

  it('uses up to 8 equal-width buckets when distinct loads exceed 10', () => {
    const points = Array.from({ length: 12 }, (_, index) => point(`10:00:${index}`, index + 1, { avgMs: 100 + index }))

    const model = buildLoadImpactModel(points)

    expect(model.buckets).toHaveLength(8)
    expect(model.buckets[0]).toMatchObject({ minVusers: 1, maxVusers: 1, exact: false })
    expect(model.buckets[7]).toMatchObject({ minVusers: 11, maxVusers: 12, exact: false })
  })

  it('returns an empty model for empty data or idle-only samples', () => {
    expect(buildLoadImpactModel([])).toEqual({ buckets: [], rows: [], pointCount: 0 })
    expect(buildLoadImpactModel([point('10:00:00', 0, { avgMs: 100 })])).toEqual({ buckets: [], rows: [], pointCount: 0 })
  })

  it('omits metric rows that do not have samples', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 5, { avgMs: 100, tps: 2 }),
      point('10:00:01', 10, { avgMs: 140, tps: 3 }),
    ])

    expect(model.rows.map(row => row.metric.id)).toEqual(['avgMs', 'tps'])
  })

  it('aggregates average, min, max, sample count, and row-relative intensity', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 5, { avgMs: 100 }),
      point('10:00:01', 5, { avgMs: 140 }),
      point('10:00:02', 10, { avgMs: 220 }),
    ])
    const artRow = model.rows.find(row => row.metric.id === 'avgMs')

    expect(artRow?.cells[0]).toMatchObject({
      sampleCount: 2,
      average: 120,
      min: 100,
      max: 140,
      intensity: 0,
    })
    expect(artRow?.cells[1]).toMatchObject({
      sampleCount: 1,
      average: 220,
      min: 220,
      max: 220,
      intensity: 1,
    })
  })

  it('filters selected metrics and VUser load points with recalculated intensity', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 5, { avgMs: 100, tps: 2 }),
      point('10:00:01', 10, { avgMs: 220, tps: 3 }),
      point('10:00:02', 15, { avgMs: 340, tps: 4 }),
    ])

    const filtered = filterLoadImpactModel(
      model,
      ['avgMs'],
      [model.buckets[1].id, model.buckets[2].id],
    )

    expect(filtered.pointCount).toBe(model.pointCount)
    expect(filtered.buckets.map(bucket => bucket.label)).toEqual(['10 VUsers', '15 VUsers'])
    expect(filtered.rows.map(row => row.metric.id)).toEqual(['avgMs'])
    expect(filtered.rows[0].cells.map(cell => cell.average)).toEqual([220, 340])
    expect(filtered.rows[0].cells.map(cell => cell.intensity)).toEqual([0, 1])
  })

  it('ignores performance zeros from archive resource-only points while keeping resource metrics', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 10, { avgMs: 120, tps: 6 }),
      {
        ...point('10:00:01', 20, { avgMs: 0, tps: 0, cpuProcess: 44 }),
        resourceSampleOnly: 1,
      },
    ])

    expect(model.rows.map(row => row.metric.id)).toContain('avgMs')
    expect(model.rows.map(row => row.metric.id)).toContain('cpuProcess')
    expect(model.rows.find(row => row.metric.id === 'avgMs')?.cells.map(cell => cell.sampleCount)).toEqual([1, 0])
    expect(model.rows.find(row => row.metric.id === 'cpuProcess')?.cells.map(cell => cell.sampleCount)).toEqual([0, 1])
  })

  it('formats values with compact metric units', () => {
    expect(formatLoadImpactValue(123.45, 'ms')).toBe('123ms')
    expect(formatLoadImpactValue(1.2345, '%')).toBe('1.23%')
    expect(formatLoadImpactValue(45.67, 'KB/s')).toBe('45.7 KB/s')
  })

  it('formats shared cell metadata for 2D and 3D views', () => {
    const model = buildLoadImpactModel([
      point('10:00:00', 5, { avgMs: 100 }),
      point('10:00:01', 5, { avgMs: 140 }),
    ])
    const cell = model.rows[0].cells[0]

    expect(loadImpactCellKey(cell)).toBe('avgMs:vusers-5')
    expect(formatLoadImpactCellTitle(cell)).toBe('5 VUsers | ART avg 120ms | min 100ms | max 140ms | 2 samples')
  })
})

function point(time: string, vusers: number, values: Partial<MetricsHistoryPoint> = {}): MetricsHistoryPoint {
  return {
    time,
    vusers,
    tps: undefined as unknown as number,
    avgMs: undefined as unknown as number,
    errorPct: undefined as unknown as number,
    p50: undefined as unknown as number,
    p90: undefined as unknown as number,
    p99: undefined as unknown as number,
    ...values,
  }
}
