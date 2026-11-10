/** Time-axis math for waterfalls: nice ticks and a zoomable/pannable viewport. */

export interface WaterfallViewport {
  startMs: number
  endMs: number
}

export interface WaterfallTick {
  ms: number
  /** Position within the viewport, 0..100. */
  pct: number
  label: string
}

const NICE_STEPS = [1, 2, 5] as const

/** Smallest 1/2/5 × 10ⁿ step that yields at most `maxTicks` intervals over `spanMs`. */
export function niceTickStep(spanMs: number, maxTicks = 8): number {
  if (!(spanMs > 0) || !Number.isFinite(spanMs)) return 1
  const target = spanMs / Math.max(1, maxTicks)
  const magnitude = 10 ** Math.floor(Math.log10(target))
  for (const m of NICE_STEPS) {
    if (m * magnitude >= target) return m * magnitude
  }
  return 10 * magnitude
}

/**
 * Label a tick. One unit per axis: seconds once the axis reaches 1 s with a
 * step of at least 10 ms, otherwise milliseconds. Decimals follow the step.
 */
export function formatTickMs(ms: number, stepMs: number, seconds = stepMs >= 1000 || (Math.abs(ms) >= 1000 && stepMs >= 10)): string {
  if (ms === 0) return '0'
  if (seconds) {
    const decimals = Math.min(3, Math.max(0, Math.ceil(-Math.log10(stepMs / 1000) - 1e-9)))
    return `${(ms / 1000).toFixed(decimals)} s`
  }
  const decimals = stepMs >= 1 ? 0 : Math.min(3, Math.ceil(-Math.log10(stepMs) - 1e-9))
  return `${ms.toFixed(decimals)} ms`
}

/** Ticks on nice boundaries covering the viewport (inclusive of both ends when aligned). */
export function niceTicks(viewport: WaterfallViewport, maxTicks = 8): WaterfallTick[] {
  const span = viewport.endMs - viewport.startMs
  if (!(span > 0)) return [{ ms: viewport.startMs, pct: 0, label: formatTickMs(viewport.startMs, 1) }]
  const step = niceTickStep(span, maxTicks)
  const first = Math.ceil(viewport.startMs / step - 1e-9) * step
  const seconds = step >= 1000 || (viewport.endMs >= 1000 && step >= 10)
  const ticks: WaterfallTick[] = []
  for (let i = 0; i < 1000; i++) {
    const ms = first + i * step
    if (ms > viewport.endMs + step * 1e-6) break
    // Round away floating drift (0.1 + 0.2) so labels stay exact.
    const rounded = Number(ms.toPrecision(12))
    ticks.push({ ms: rounded, pct: ((rounded - viewport.startMs) / span) * 100, label: formatTickMs(rounded, step, seconds) })
  }
  return ticks
}

/** Minimum visible span so zoom cannot collapse the axis. */
export const MIN_VIEWPORT_MS = 0.5

export function fullViewport(spanMs: number): WaterfallViewport {
  return { startMs: 0, endMs: Math.max(spanMs, 1) }
}

export function clampViewport(viewport: WaterfallViewport, spanMs: number): WaterfallViewport {
  const total = Math.max(spanMs, 1)
  let width = Math.min(total, Math.max(MIN_VIEWPORT_MS, viewport.endMs - viewport.startMs))
  if (!Number.isFinite(width)) width = total
  let start = Number.isFinite(viewport.startMs) ? viewport.startMs : 0
  start = Math.min(Math.max(0, start), total - width)
  return { startMs: start, endMs: start + width }
}

/**
 * Zoom around an anchor (0..1 within the viewport). factor < 1 zooms in.
 * The anchor's time stays under the pointer.
 */
export function zoomViewport(viewport: WaterfallViewport, factor: number, anchor: number, spanMs: number): WaterfallViewport {
  const a = Math.min(1, Math.max(0, Number.isFinite(anchor) ? anchor : 0.5))
  const width = viewport.endMs - viewport.startMs
  const anchorMs = viewport.startMs + width * a
  // Clamp the width first so the anchored time stays put at the zoom limits.
  const nextWidth = Math.min(Math.max(spanMs, 1), Math.max(MIN_VIEWPORT_MS, width * factor))
  return clampViewport({ startMs: anchorMs - nextWidth * a, endMs: anchorMs + nextWidth * (1 - a) }, spanMs)
}

export function panViewport(viewport: WaterfallViewport, deltaMs: number, spanMs: number): WaterfallViewport {
  return clampViewport({ startMs: viewport.startMs + deltaMs, endMs: viewport.endMs + deltaMs }, spanMs)
}

/** Fit a [start,end] range with a little padding on both sides. */
export function fitViewport(startMs: number, endMs: number, spanMs: number, paddingRatio = 0.04): WaterfallViewport {
  const width = Math.max(MIN_VIEWPORT_MS, endMs - startMs)
  const pad = width * paddingRatio
  return clampViewport({ startMs: startMs - pad, endMs: startMs + width + pad }, spanMs)
}

export function isFullViewport(viewport: WaterfallViewport, spanMs: number): boolean {
  const full = fullViewport(spanMs)
  return Math.abs(viewport.startMs - full.startMs) < 1e-6 && Math.abs(viewport.endMs - full.endMs) < 1e-6
}

/** Project a [start, start+duration] span into viewport percentages, clipped. */
export function projectSpan(startMs: number, durationMs: number, viewport: WaterfallViewport): { leftPct: number; widthPct: number; visible: boolean } {
  const span = viewport.endMs - viewport.startMs
  if (!(span > 0)) return { leftPct: 0, widthPct: 0, visible: false }
  const endMs = startMs + Math.max(0, durationMs)
  const visible = endMs >= viewport.startMs && startMs <= viewport.endMs
  const left = ((startMs - viewport.startMs) / span) * 100
  const width = (Math.max(0, durationMs) / span) * 100
  return { leftPct: left, widthPct: width, visible }
}
