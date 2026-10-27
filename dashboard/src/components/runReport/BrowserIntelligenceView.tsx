import { useEffect, useRef, useState } from 'react'

import type { RcaAnalysisResponse } from '../../types/rca'
import { browserBaselineAbsence } from './browserBaselineAbsence'
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
  /** The run's root-cause analysis, whose assessment holds the browser baseline. */
  rootCause: { response: RcaAnalysisResponse | null; loading: boolean; error: string }
  onOpenDiagnostics: () => void
}

// BrowserIntelligenceView is the run report's Browser area (BI-4): documents
// compared by engine, device and group; policy-scored findings with their
// drill-down (finding → document → resources → HAR entry); the root-cause
// baseline comparison and its exclusions; interaction patterns and heatmaps;
// and journey accounting.
export default function BrowserIntelligenceView({ testId, projectId, canEvaluate, rootCause, onOpenDiagnostics, ...scope }: Props) {
  // Each selection is new, so choosing the same document again reopens and
  // refilters the inspector.
  const [inspect, setInspect] = useState<{ id: string; seq: number }>({ id: '', seq: 0 })
  const inspectorRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (inspect.id) inspectorRef.current?.scrollIntoView?.({ block: 'start' })
  }, [inspect])
  // Whether the run pinned a browser policy; unknown until the report loads.
  const browserPolicyPinned = scope.report ? Boolean(scope.report.manifest?.provenance?.policy_pins?.some(pin => pin.kind === 'browser_policy')) : undefined
  const baseline = rootCause.response?.assessment?.browser_baseline
  const selection = rootCause.response?.assessment?.browser_selection
  const receiptDigest = rootCause.response?.assessment?.inputs?.browser_selection
  const absence = browserBaselineAbsence(rootCause)
  return <>
    {scope.report
      ? <BrowserComparisonTable summary={scope.report.browser_documents} mode="archive-latest" />
      : <section className="run-report-panel browser-comparison" aria-label="Device, engine and group comparison"><h2>Device, engine and group comparison</h2><p role="status">Loading the run's document evidence…</p></section>}
    <BrowserFindingsPanel testId={testId} projectId={projectId} canEvaluate={canEvaluate} browserPolicyPinned={browserPolicyPinned} onInspectDocument={id => setInspect(current => ({ id, seq: current.seq + 1 }))} />
    <section className="run-report-panel browser-baseline-summary" aria-label="Browser baseline">
      {(baseline || selection) && <RcaBrowserBaselineSection baseline={baseline} selection={selection} receiptDigest={receiptDigest} />}
      {!baseline && <>
        <h2>Browser baseline</h2>
        <p role={rootCause.loading && !rootCause.response ? 'status' : undefined}>{absence}</p>
        {!rootCause.loading && <button type="button" className="btn btn-secondary" onClick={onOpenDiagnostics}>Open root-cause analysis</button>}
      </>}
    </section>
    <BrowserInteractionsPanel testId={testId} />
    <JourneyRiskPanel testId={testId} />
    <div ref={inspectorRef}>
      {inspect.id && <p className="run-report-muted" role="status">Opened from a finding: the retained resources of document …{inspect.id.slice(-8)}.</p>}
      <ArchiveResourceInspectorDisclosure key={inspect.seq} {...scope} documentId={inspect.id || undefined} />
    </div>
  </>
}
