import { lazy, Suspense, useId, useState } from 'react'
import { archiveResourceInspectorOwner, type ArchiveResourceInspectorReadScope } from './archiveResourceInspectorOwner'
import type { BrowserResourcePageLoader } from './useBrowserResourcePage'
import LazyLoadBoundary from './LazyLoadBoundary'

const BrowserResourceInspector = lazy(() => import('./BrowserResourceInspector'))

interface Props extends ArchiveResourceInspectorReadScope { loadPage?: BrowserResourcePageLoader }
export default function ArchiveResourceInspectorDisclosure(props: Props) {
  const owner = archiveResourceInspectorOwner(props)
  return <OwnedDisclosure key={owner?.sourceIdentity || 'unavailable'} owner={owner} loadPage={props.loadPage} />
}
function OwnedDisclosure({ owner, loadPage }: { owner: ReturnType<typeof archiveResourceInspectorOwner>; loadPage?: BrowserResourcePageLoader }) {
  const [open, setOpen] = useState(false)
  const id = useId()
  return <section className="tx-section archive-summary-section" aria-label="Archive resource inspection">
    <h3 className="chart-title">Retained archive resource inspection</h3>
    <p>Open bounded archive pages to filter retained observations and inspect available native request details.</p>
    <button type="button" className="btn btn-secondary btn-sm" aria-expanded={open} aria-controls={id} disabled={!owner} onClick={() => setOpen(value => !value)}>
      {open ? 'Close retained resource inspector' : 'Inspect retained archive resources'}
    </button>
    {!owner && <p role="status">A matching archived run, project and report are required for resource inspection.</p>}
    <div id={id}>{open && owner && <LazyLoadBoundary title="Retained resource inspector could not be loaded."><Suspense fallback={<p role="status">Loading retained resource inspector…</p>}><BrowserResourceInspector {...owner} loadPage={loadPage} /></Suspense></LazyLoadBoundary>}</div>
  </section>
}
