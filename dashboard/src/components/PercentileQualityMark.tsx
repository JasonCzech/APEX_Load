import { percentileQualityDescription } from '../utils/percentileQuality'

interface Props {
  quality?: string
  /** The percentile it marks, where one can be missing: no mark shows without it. */
  value?: number | null
}

/** A "≈" before a percentile that is not exact, explained on hover and to screen readers. */
export default function PercentileQualityMark(props: Props) {
  const description = percentileQualityDescription(props.quality)
  if (!description || ('value' in props && (props.value === undefined || props.value === null))) return null
  return (
    <span className="percentile-quality-mark" title={description}>
      <span aria-hidden="true">≈ </span>
      <span className="sr-only">{description} </span>
    </span>
  )
}
