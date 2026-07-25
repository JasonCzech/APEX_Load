import { type DropIngestPhase } from './dropIngestFeedback'
import './DropIngestOverlay.css'

interface Props {
  phase: DropIngestPhase
  label: string
  // inline lays the spinner and label out in a row, for short single-row hosts.
  inline?: boolean
}

// DropIngestOverlay renders the working/confirmed layer over a dropzone so a
// drag-drop is visibly acknowledged. The host element must carry the
// `hostClassName` from useDropIngestFeedback.
//
// The live region is rendered unconditionally and only its text changes: a live
// region inserted together with its text is announced inconsistently across
// screen readers. The visual layer is aria-hidden so the label is not announced
// twice.
//
// It carries aria-live/aria-atomic rather than role="status" — the semantics are
// identical, but a permanent role="status" inside every dropzone would shadow the
// page's own status regions for anything selecting on that role.
export default function DropIngestOverlay({ phase, label, inline }: Props) {
  return (
    <>
      <span className="drop-ingest-live" aria-live="polite" aria-atomic="true">{phase === 'idle' ? '' : label}</span>
      {phase !== 'idle' && (
        <div className={`drop-ingest-overlay drop-ingest-${phase} ${inline ? 'is-inline' : ''}`} data-testid="drop-ingest-overlay" aria-hidden="true">
          {phase === 'ingesting' ? (
            <span className="drop-ingest-spinner" />
          ) : (
            // Small by design: the confirmation is a corner pill, not a cover.
            <svg className="drop-ingest-check" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2.5">
              <circle cx="12" cy="12" r="10" opacity="0.35" />
              <polyline points="7 12.5 10.5 16 17 9" />
            </svg>
          )}
          <span className="drop-ingest-label">{label}</span>
        </div>
      )}
    </>
  )
}
