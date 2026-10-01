export interface NativeEvidencePoint { from: string; to: string; offset_seconds: number; count: number; coverage?: number; value: number | null }

// Keep uncovered time visible when publications stop entirely (for example,
// during a collector interruption). Small publication jitter is not a gap.
export function nativeEvidencePlotPoints(points: NativeEvidencePoint[], start: string, live: boolean): NativeEvidencePoint[] {
  if (!live) return points
  const result: NativeEvidencePoint[] = []
  for (const point of points) {
    const previous = result[result.length - 1]
    if (previous && Date.parse(point.from) - Date.parse(previous.to) > 250) {
      const middle = (Date.parse(previous.to) + Date.parse(point.from)) / 2
      result.push({ from: previous.to, to: point.from, offset_seconds: (middle - Date.parse(start)) / 1000, count: 0, value: null })
    }
    result.push(point)
  }
  return result
}
