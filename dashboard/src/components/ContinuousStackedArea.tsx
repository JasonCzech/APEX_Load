import { AreaRevealShape, type AreaRevealShapeProps } from 'recharts'

type ContinuousStackedAreaProps = AreaRevealShapeProps & { stackKeys?: readonly string[] }

// Keep connectNulls=false on the parent Area so Recharts preserves a missing
// stack value as a gap instead of a zero contribution. Join only the SVG paths.
export default function ContinuousStackedArea({ stackKeys, points, baseLine, ...props }: ContinuousStackedAreaProps) {
  // A series' cumulative position also needs the observed series below it.
  // Mask its upper and lower coordinates together when that offset is unknown.
  const missingStackPoint = points?.map(point => {
    const payload = (point as { payload?: Record<string, unknown> }).payload
    return stackKeys?.some(key => {
      const value = payload?.[key]
      return typeof value !== 'number' || !Number.isFinite(value)
    }) ?? false
  })
  const paintedPoints = points?.map((point, index) => missingStackPoint?.[index] ? { ...point, x: null, y: null } : point)
  const paintedBaseLine = Array.isArray(baseLine)
    ? baseLine.map((point, index) => missingStackPoint?.[index] ? { ...point, x: null, y: null } : point)
    : baseLine

  return <AreaRevealShape {...props} points={paintedPoints} baseLine={paintedBaseLine} connectNulls />
}
