import { describe, expect, it } from 'vitest'
import type { MetricsHistoryPoint } from '../../types'
import { nearestTimelineTick, parseTimelineTime, sampleTimeline, timelineTimeBounds, timelineTimeWindow } from './timelineSampling'

const origin = Date.parse('2026-10-26T23:59:00Z')
const point = (i: number, extra = {}) => ({ time: String(i), timestampMs: origin + i * 1000, tps: i % 100, ...extra }) as MetricsHistoryPoint

describe('timeline sampling', () => {
  it('keeps small windows unchanged, including empty windows', () => {
    const data = [point(0), point(1)]
    expect(sampleTimeline(data, ['tps'], 100)).toBe(data)
    expect(sampleTimeline([], ['tps'], 100)).toEqual([])
  })
  it('bounds rendered rows by width, keeps extrema of every selected series and endpoints without mutating input', () => {
    const data = Array.from({ length: 10000 }, (_, i) => point(i, { cpuProcess: i === 4321 ? 1000 : 50, tps: i === 1234 ? -1000 : i % 100 }))
    const small = sampleTimeline(data, ['tps', 'cpuProcess'], 100)
    const large = sampleTimeline(data, ['tps', 'cpuProcess'], 500)
    expect(small.length).toBeLessThanOrEqual(200)
    expect(large.length).toBeLessThanOrEqual(1000)
    expect(large.length).toBeGreaterThan(small.length)
    expect(small).toContain(data[1234])
    expect(small).toContain(data[4321])
    expect(small[0]).toBe(data[0])
    expect(small[small.length - 1]).toBe(data[data.length - 1])
    expect(small.every((p, i) => i === 0 || p.timestampMs! > small[i - 1].timestampMs!)).toBe(true)
    expect(data).toHaveLength(10000)
  })
  it('keeps each series first and last finite samples even when neither is a bucket extremum', () => {
    const data = Array.from({ length: 10000 }, (_, i) => point(i, {
      tps: i >= 10 && i <= 90 ? (i === 11 ? 0 : i === 12 ? 10 : 5) : NaN,
      cpuProcess: i >= 2010 && i <= 2090 ? (i === 2011 ? 0 : i === 2012 ? 100 : 50) : undefined,
    }))
    const rows = sampleTimeline(data, ['tps', 'cpuProcess'], 100)
    for (const index of [10, 11, 12, 90, 2010, 2011, 2012, 2090]) expect(rows).toContain(data[index])
    expect(rows.length).toBeLessThanOrEqual(200)
  })
  it('retains sparse zeroes and ignores nonfinite values with uneven timestamps', () => {
    const data = Array.from({ length: 500 }, (_, i) => point(i, { timestampMs: origin + i * i * 1000, tps: NaN, cpuProcess: i === 249 ? 0 : undefined }))
    const rows = sampleTimeline(data, ['tps', 'cpuProcess'], 50)
    expect(rows).toContain(data[249])
    expect(rows.some(row => row.cpuProcess === 0)).toBe(true)
    expect(rows.length).toBeLessThanOrEqual(100)
  })
  it('synchronizes differently sampled numeric lanes by nearest time and categorical lanes by exact label', () => {
    expect(nearestTimelineTick([{ value: 10 }, { value: 30 }], { activeLabel: '26' })).toBe(1)
    expect(nearestTimelineTick([{ value: '00:01' }], { activeLabel: '00:01' })).toBe(0)
    expect(nearestTimelineTick([], { activeLabel: 20 })).toBe(-1)
  })
})

describe('manual timeline ranges', () => {
  it.each([['90', 90000], ['90s', 90000], ['1500ms', 1500], ['1.5m', 90000], ['1h', 3600000], ['01:30', 90000], ['01:02:03.5', 3723500]])('parses relative %s', (text, delta) => {
    expect(parseTimelineTime(text, 'relative', origin)).toBe(origin + delta)
  })
  it.each(['', '-1', '1:60', '1:00:60', 'hello', 'Infinity', '1d'])('rejects invalid relative input %s', text => {
    expect(parseTimelineTime(text, 'relative', origin)).toBeNull()
  })
  it('uses UTC run date for clock input and explicit dates for midnight crossings', () => {
    expect(parseTimelineTime('23:59:30.5', 'clock', origin)).toBe(origin + 30500)
    expect(parseTimelineTime('2026-10-27T00:01:00Z', 'clock', origin)).toBe(origin + 120000)
    expect(parseTimelineTime('2026-10-26T20:01:00-04:00', 'clock', origin)).toBe(origin + 120000)
    expect(parseTimelineTime('2026-02-30T12:00:00Z', 'clock', origin)).toBeNull()
    expect(parseTimelineTime('24:00', 'clock', origin)).toBeNull()
    expect(parseTimelineTime('12:60', 'clock', origin)).toBeNull()
    expect(parseTimelineTime('2026-10-27T00:01', 'clock', origin)).toBeNull()
  })
  it('brackets sparse observations, includes exact boundaries, and rejects reversed or out-of-range windows', () => {
    const data = [0, 10, 20].map(i => point(i))
    expect(timelineTimeWindow(data, origin + 1000, origin + 9000)).toEqual([0, 1])
    expect(timelineTimeWindow(data, origin + 10000, origin + 20000)).toEqual([1, 2])
    expect(timelineTimeWindow(data, origin + 20000, origin)).toBeNull()
    expect(timelineTimeWindow(data, origin - 1, origin + 20000)).toBeNull()
    expect(timelineTimeWindow([], origin, origin)).toBeNull()
  })
  it('includes every summary and resource row sharing the selected boundary timestamps', () => {
    const data = [0, 0, 10, 10, 20, 20].map(i => point(i))
    expect(timelineTimeWindow(data, origin, origin + 10000)).toEqual([0, 3])
    expect(timelineTimeWindow(data, origin + 10000, origin + 10000)).toEqual([2, 3])
    expect(timelineTimeWindow(data, origin + 11000, origin + 19000)).toEqual([2, 5])
  })
  it('accepts a run-start bound before the first interval sample without accepting times outside the run', () => {
    const data = [10, 20, 30].map(i => point(i))
    expect(timelineTimeWindow(data, origin, origin + 20000, origin)).toEqual([0, 1])
    expect(timelineTimeWindow(data, origin - 1, origin + 20000, origin)).toBeNull()
    expect(timelineTimeWindow(data, origin, origin + 5000, origin)).toBeNull()
  })
  it('rejects missing, nonfinite, or out-of-order timestamps instead of guessing clock bounds', () => {
    expect(timelineTimeBounds([point(0), point(1, { timestampMs: undefined })])).toBeNull()
    expect(timelineTimeBounds([point(0), point(1, { timestampMs: NaN })])).toBeNull()
    expect(timelineTimeBounds([point(1), point(0)])).toBeNull()
    expect(timelineTimeBounds([point(0), point(0), point(1)])).toEqual([origin, origin + 1000])
    expect(timelineTimeWindow([point(0), point(1, { timestampMs: undefined }), point(2)], origin, origin + 2000)).toBeNull()
    expect(parseTimelineTime(`${'9'.repeat(400)}:00`, 'relative', origin)).toBeNull()
  })

})
