import { describe, expect, it } from 'vitest'
import { clampViewport, fitViewport, formatTickMs, fullViewport, isFullViewport, niceTickStep, niceTicks, panViewport, projectSpan, zoomViewport } from './axis'

describe('waterfall axis', () => {
  it('picks 1/2/5 × 10ⁿ steps', () => {
    expect(niceTickStep(1000, 8)).toBe(200)
    expect(niceTickStep(1000, 10)).toBe(100)
    expect(niceTickStep(73, 8)).toBe(10)
    expect(niceTickStep(0.9, 8)).toBe(0.2)
    expect(niceTickStep(12_000, 6)).toBe(2000)
    expect(niceTickStep(0)).toBe(1)
    expect(niceTickStep(Number.NaN)).toBe(1)
  })

  it('places ticks on step boundaries inside the viewport', () => {
    const ticks = niceTicks({ startMs: 130, endMs: 870 }, 8)
    expect(ticks.map(t => t.ms)).toEqual([200, 300, 400, 500, 600, 700, 800])
    expect(ticks[0].pct).toBeCloseTo(((200 - 130) / 740) * 100)
    expect(ticks.every(t => t.pct >= 0 && t.pct <= 100)).toBe(true)
    // Floating drift never leaks into labels.
    expect(niceTicks({ startMs: 0, endMs: 0.7 }, 7).map(t => t.label)).toEqual(['0', '0.1 ms', '0.2 ms', '0.3 ms', '0.4 ms', '0.5 ms', '0.6 ms', '0.7 ms'])
    expect(niceTicks({ startMs: 0, endMs: 10_000 }, 5).map(t => t.label)).toEqual(['0', '2 s', '4 s', '6 s', '8 s', '10 s'])
    // One unit per axis: an axis that reaches 1 s labels every tick in seconds.
    expect(niceTicks({ startMs: 0, endMs: 1000 }, 5).map(t => t.label)).toEqual(['0', '0.2 s', '0.4 s', '0.6 s', '0.8 s', '1.0 s'])
  })

  it('formats ticks by step size', () => {
    expect(formatTickMs(0, 100)).toBe('0')
    expect(formatTickMs(250, 50)).toBe('250 ms')
    expect(formatTickMs(1500, 500)).toBe('1.5 s')
    expect(formatTickMs(3000, 1000)).toBe('3 s')
  })

  it('zooms around the anchor and clamps to the session', () => {
    const vp = fullViewport(1000)
    const zoomed = zoomViewport(vp, 0.5, 0.25, 1000)
    expect(zoomed.startMs).toBeCloseTo(125)
    expect(zoomed.endMs).toBeCloseTo(625)
    // The time under the anchor stays put.
    expect(zoomed.startMs + (zoomed.endMs - zoomed.startMs) * 0.25).toBeCloseTo(250)
    expect(zoomViewport(zoomed, 10, 0.5, 1000)).toEqual({ startMs: 0, endMs: 1000 })
    expect(isFullViewport(zoomViewport(zoomed, 10, 0.5, 1000), 1000)).toBe(true)
    const tiny = zoomViewport(vp, 1e-9, 0.5, 1000)
    expect(tiny.endMs - tiny.startMs).toBeCloseTo(0.5)
  })

  it('pans without leaving the session and fits ranges with padding', () => {
    const vp = { startMs: 100, endMs: 300 }
    expect(panViewport(vp, 50, 1000)).toEqual({ startMs: 150, endMs: 350 })
    expect(panViewport(vp, -500, 1000)).toEqual({ startMs: 0, endMs: 200 })
    expect(panViewport(vp, 5000, 1000)).toEqual({ startMs: 800, endMs: 1000 })
    const fit = fitViewport(400, 500, 1000, 0.1)
    expect(fit.startMs).toBeCloseTo(390)
    expect(fit.endMs).toBeCloseTo(510)
    expect(clampViewport({ startMs: Number.NaN, endMs: 50 }, 1000).startMs).toBe(0)
  })

  it('projects spans into viewport percentages', () => {
    expect(projectSpan(200, 100, { startMs: 100, endMs: 500 })).toEqual({ leftPct: 25, widthPct: 25, visible: true })
    expect(projectSpan(600, 100, { startMs: 100, endMs: 500 }).visible).toBe(false)
    expect(projectSpan(0, 150, { startMs: 100, endMs: 500 })).toMatchObject({ leftPct: -25, visible: true })
  })
})

describe('waterfall axis limits', () => {
  it('keeps the viewport still when zooming in past the minimum width', () => {
    const vp = { startMs: 100, endMs: 100.5 }
    expect(zoomViewport(vp, 0.8, 0.9, 1000)).toEqual(vp)
  })
})
