import type { MetricsHistoryPoint } from '../../types'

// TimelineOverlay is a span or instant from another analysis (root cause:
// an anomaly, a span the run was not valid, a load step) drawn over the
// run timeline. Times are epoch milliseconds; an overlay without end is an
// instant.
export interface TimelineOverlay {
  id: string
  kind: 'anomaly' | 'validity' | 'phase'
  start: number
  end?: number
  label: string
  tone: 'danger' | 'warning' | 'accent' | 'muted'
  // ref is what selecting the overlay opens, in the producer's own terms.
  ref?: string
}

// TimelineOverlayBand is an overlay placed on the timeline's categorical
// axis: x1 (and x2 for a span) are the time labels of the samples it covers.
export interface TimelineOverlayBand {
  overlay: TimelineOverlay
  x1: string
  x2?: string
}

// timelineOverlayBands places overlays on the visible samples. A sample
// carries its interval's end; overlay times carry interval starts, so each
// sample is read as starting one typical interval before its timestamp, the
// way the root-cause timeline lines them up. An overlay outside the visible
// samples is left out rather than pinned to an edge; a span that reaches
// past either edge is cut to it.
export function timelineOverlayBands(overlays: TimelineOverlay[] | undefined, data: MetricsHistoryPoint[]): TimelineOverlayBand[] {
  const sampled = data.filter(point => typeof point.timestampMs === 'number' && Number.isFinite(point.timestampMs))
  if (!overlays?.length || !sampled.length) return []
  const ends = sampled.map(point => point.timestampMs as number)
  const gaps = ends.slice(1).map((end, i) => end - ends[i]).filter(gap => gap > 0).sort((a, b) => a - b)
  const interval = gaps.length ? gaps[Math.floor(gaps.length / 2)] : 0
  const starts = sampled.map(point => ({ time: point.time, at: (point.timestampMs as number) - interval }))
  const first = starts[0].at
  const last = starts[starts.length - 1].at + interval
  const nearest = (at: number) => starts.reduce((best, point) => (Math.abs(point.at - at) < Math.abs(best.at - at) ? point : best)).time
  return overlays.flatMap(overlay => {
    const end = overlay.end ?? overlay.start
    if (!Number.isFinite(overlay.start) || !Number.isFinite(end) || end < first || overlay.start > last) return []
    const x1 = nearest(Math.max(overlay.start, first))
    if (overlay.end === undefined) return [{ overlay, x1 }]
    const x2 = nearest(Math.min(end, last))
    return [{ overlay, x1, x2 }]
  })
}
