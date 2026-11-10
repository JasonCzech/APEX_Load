import type { RunArchiveTimeAnatomy } from '../../types/timeAnatomy'
import { headlineTransaction } from './timeAnatomyModel'
import './TimeAnatomy.css'

interface Props {
  anatomy?: RunArchiveTimeAnatomy | null
  onOpen: (name: string) => void
}

/** One Summary line: why the slowest instances of a transaction were slow. */
export default function TimeAnatomyHeadline({ anatomy, onOpen }: Props) {
  const entry = headlineTransaction(anatomy)
  if (!entry?.tail?.headline) return null
  return (
    <p className="rr-anatomy-summary-line">
      <strong>{entry.name}:</strong> {entry.tail.headline}{' '}
      <button type="button" className="btn btn-secondary btn-sm" onClick={() => onOpen(entry.name)} aria-label={`Open time anatomy for ${entry.name}`}>Open time anatomy</button>
    </p>
  )
}
