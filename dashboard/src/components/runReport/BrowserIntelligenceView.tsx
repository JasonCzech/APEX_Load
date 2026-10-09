import { useEffect, useRef, useState } from 'react'

import type { RcaBrowserBaseline } from '../../types/rca'
import ArchiveResourceInspectorDisclosure from '../ArchiveResourceInspectorDisclosure'
import type { ArchiveResourceInspectorReadScope } from '../archiveResourceInspectorOwner'
import RcaBrowserBaselineSection from '../rca/RcaBrowserBaseline'
import BrowserComparisonTable from './BrowserComparisonTable'
import BrowserFindingsPanel from './BrowserFindingsPanel'
import BrowserInteractionsPanel from './BrowserInteractionsPanel'
import JourneyRiskPanel from './JourneyRiskPanel'

interface Props extends ArchiveResourceInspectorReadScope {
  testId: string
  projectId: string
  canEvaluate: boolean
  /** The browser baseline of the run's root-cause analysis, when one has been run. */
  baseline?: RcaBrowserBaseline
  onOpenDiagnostics: () => void
}

// BrowserIntelligenceView is the run report's Browser area (BI-4): documents
// compared by engine, device and group; policy-scored findings with their
// drill-down (finding → document → resources → HAR entry); the root-cause
// baseline comparison and its exclusions; interaction patterns and heatmaps;
// and journey accounting.
export default function BrowserIntelligenceView({ testId, projectId, canEvaluate, baseline, onOpenDiagnostics, ...scope }: Props) {
  const [inspectDocumentId, setInspectDocumentId] = useState('')
  const inspectorRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (inspectDocumentId) inspectorRef.current?.scrollIntoView?.({ block: 'start' })
  }, [inspectDocumentId])
  return <>
    <BrowserComparisonTable summary={scope.report?.browser_documents} mode="archive-latest" />
    <BrowserFindingsPanel testId={testId} projectId={projectId} canEvaluate={canEvaluate} onInspectDocument={setInspectDocumentId} />
    <section className="run-report-panel browser-baseline-summary" aria-label="Browser baseline">
      {baseline
        ? <RcaBrowserBaselineSection baseline={baseline} />
        : <>
          <h2>Browser baseline</h2>
          <p>The baseline comparison of browser documents (anomalies, qualification and exclusions) is part of root-cause analysis, which has not been run for this run.</p>
          <button type="button" className="btn btn-secondary" onClick={onOpenDiagnostics}>Open root-cause analysis</button>
        </>}
    </section>
    <BrowserInteractionsPanel testId={testId} />
    <JourneyRiskPanel testId={testId} />
    <div ref={inspectorRef}>
      {inspectDocumentId && <p className="run-report-muted" role="status">Showing the retained resources of document …{inspectDocumentId.slice(-8)}.</p>}
      <ArchiveResourceInspectorDisclosure {...scope} documentId={inspectDocumentId || undefined} />
    </div>
  </>
}
