import BaselineComparisonWorkspace from './baselineComparison/BaselineComparisonWorkspace'
import { loadAllTestSummaries } from './testCatalogPagination'
import { useExecutionTestManifest } from './useExecutionTestManifest'
import RunExecutionDetails, { RunExecutionFacts, RetainedRunMetrics, RetainedRunDiagnostics } from './runReport/RunExecutionDetails'
import { executionFacts, recordedNumber, executionDate } from './runReport/executionPresentation'
import StatusBadge from './StatusBadge'
import TestNotesDialog from './TestNotesDialog'
import { reportComparisonQualification } from './reportComparisonQualification'
import { OrchestrationReturn } from './OrchestrationReturn'
import type { ReportSection } from '../appRouting'
import TabList from './ux/TabList'
import TestExecutionArchive from './TestExecutionArchive'
import RunOutcomeSummary, { RunMetricPopulations } from './RunOutcomeSummary'
import ReportComposer from './ReportComposer'
import { defaultReportPopulation, reportPopulationOverview, populationLabels, type MetricPopulation } from './runReport/reportPresentation'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { isReportLens, type ReportLens } from '../appRouting'
import { useApi } from '../hooks/useApi'
import type { ReportAnnotations, RunArchiveReport, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { isCompletedTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'
import { useExecutionArchiveResourceHistory } from './useExecutionArchiveResourceHistory'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'
import { useExecutionTestPolling } from './useExecutionTestPolling'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import RunReportCompareBar from './runReport/RunReportCompareBar'
import RunReportDiagnostics from './runReport/RunReportDiagnostics'
import RunReportExportActions from './runReport/RunReportExportActions'
import RunReportPowerBIPublish from './runReport/RunReportPowerBIPublish'
import RunReportConfluencePublish from './runReport/RunReportConfluencePublish'
import RunReportKpiScorecard from './runReport/RunReportKpiScorecard'
import RunReportLensSwitcher from './runReport/RunReportLensSwitcher'
import RunArchiveCoverageNotice from './runReport/RunArchiveCoverageNotice'
import RunReportVerdictBanner from './runReport/RunReportVerdictBanner'
import SynchronizedTimeline from './runReport/SynchronizedTimeline'
import ImportedDashboardPanels from './runReport/ImportedDashboardPanels'
import TransactionSlaScoreboard from './runReport/TransactionSlaScoreboard'
import TransactionResponseTimeSLATable from './TransactionResponseTimeSLATable'
import {
  alignBaselineTimeline,
  buildBaselineOverviewDiff,
  buildReportKpis,
  mergeTimelineHistory,
  slaRowsForReport,
} from './runReport/runReportModel'
import './RunReportPage.css'

interface Props {
  testId: string
  canPublishConfluence?: boolean
  initialLens?: ReportLens
  initialCompare?: string
  initialComparisonId?: string
  initialTransaction?: string
  initialPopulation?: MetricPopulation
  initialSection?: ReportSection
  statusOverride?: string
  onOpenCommandCenter: (id?: string) => void
  onNavigateReport: (next: { lens?: ReportLens; compare?: string; comparisonId?: string; section?: ReportSection }) => void
}

function archiveReadyTest(test: TestSummary | null, status: string): TestSummary | null {
  if (!test || !isCompletedTestStatus(status)) return null
  return { ...test, status: status as TestSummary['status'] }
}

type VerdictDraft = { status: string; explanation: string }

function verdictDraftFrom(report: RunArchiveReport | null, annotations: ReportAnnotations | null): VerdictDraft {
  void report
  return { status: annotations?.verdict_override || '', explanation: annotations?.verdict_explanation || '' }
}

function httpStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

export default function RunReportPage({
  testId,
  canPublishConfluence = false,
  initialLens,
  initialCompare,
  initialComparisonId,
  initialTransaction,
  initialPopulation,
  initialSection,
  statusOverride,
  onOpenCommandCenter,
  onNavigateReport,
}: Props) {
  const api = useApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [activeLens, setActiveLens] = useState<ReportLens>(initialLens || 'executive')
  const [area, setAreaState] = useState<'Summary' | 'Performance' | 'Comparison' | 'Diagnostics' | 'Evidence' | 'Execution'>('Summary')
  const [showExport, setShowExport] = useState(false)
  const [showNotes, setShowNotes] = useState(false)
  const [showCompare, setShowCompare] = useState(Boolean(initialCompare))
  const [showComposer, setShowComposer] = useState(false)
  const [populationChoice, setPopulationChoice] = useState<MetricPopulation | null>(null)
  const [baselineOptions, setBaselineOptions] = useState<TestSummary[]>([])
  const [baselineOptionsError, setBaselineOptionsError] = useState('')
  const [baselineOptionsLoading, setBaselineOptionsLoading] = useState(false)
  const [compareId, setCompareId] = useState(initialCompare || '')
  const { test, setTest, error: testError, archiveCheckPending, archiveCheckError, checkArchiveAgain } = useExecutionTestPolling(testId, { stopOnTerminal: true })
  const resolvedStatus = test ? resolveTestStatus(test, statusOverride ? { [test.id]: statusOverride } : {}) : ''
  const completed = isCompletedTestStatus(resolvedStatus)
  const archivedTest = useMemo(() => archiveReadyTest(test, resolvedStatus), [resolvedStatus, test])
  const manifestModel = useExecutionTestManifest(archivedTest)
  const archiveModel = useExecutionArchiveModel(archivedTest, false, compareId)
  const { archiveSummaryHistory, archiveSummaryError, archiveSummariesLoading } = useExecutionArchiveSummaryHistory(archivedTest)
  const { archiveResourceHistory, archiveResourceError, archiveResourcesLoading } = useExecutionArchiveResourceHistory(archivedTest)

  const [annotations, setAnnotations] = useState<ReportAnnotations | null>(null)
  const [annotationsLoading, setAnnotationsLoading] = useState(false)
  const [annotationsError, setAnnotationsError] = useState('')
  const [savingAnnotations, setSavingAnnotations] = useState(false)
  const [verdictDraft, setVerdictDraft] = useState<VerdictDraft>({ status: 'Conditional Pass', explanation: '' })
  const [verdictBaseline, setVerdictBaseline] = useState<VerdictDraft>({ status: 'Conditional Pass', explanation: '' })
  const verdictDraftRef = useRef(verdictDraft)
  const verdictBaselineRef = useRef(verdictBaseline)
  const verdictDirtyRef = useRef(false)
  const verdictRevisionRef = useRef(0)
  const annotationRequestGenerationRef = useRef(0)
  const annotationSaveGenerationRef = useRef(0)
  const verdictSaveTailRef = useRef<Promise<void>>(Promise.resolve())
  const activeVerdictSaveRef = useRef<{ key: string; promise: Promise<boolean> } | null>(null)
  const activeLensRef = useRef(activeLens)
  const testIDRef = useRef(testId)
  const effectiveCompareIDRef = useRef('')
  const pinCompareStamp = useRef(0)
  const archiveReportRef = useRef(archiveModel.archiveReport)
  activeLensRef.current = activeLens
  testIDRef.current = testId
  archiveReportRef.current = archiveModel.archiveReport
  verdictDraftRef.current = verdictDraft
  verdictBaselineRef.current = verdictBaseline
  const verdictDirty = verdictDraft.status !== verdictBaseline.status || verdictDraft.explanation !== verdictBaseline.explanation
  verdictDirtyRef.current = verdictDirty
  const [baselineTest, setBaselineTest] = useState<TestSummary | null>(null)
  const [baselineReport, setBaselineReport] = useState<RunArchiveReport | null>(null)
  const [baselineError, setBaselineError] = useState('')
  const [baselineLoading, setBaselineLoading] = useState(false)

  const suggestedCompareId = test?.comparison_baseline?.source_test_id || ''
  const population = populationChoice || defaultReportPopulation(archiveModel.archiveReport)
  useEffect(() => {
    if (!showCompare || !test) return
    let cancelled = false
    setBaselineOptionsError('')
    setBaselineOptions([])
    setBaselineOptionsLoading(true)
    loadAllTestSummaries(api.listTests, { project_id: test.project_id, application_id: test.application_id, summary: true }, 100).then(result => {
      if (!cancelled) setBaselineOptions((result.tests || []).filter(run => run.id !== testId && isCompletedTestStatus(run.status) && Boolean(run.archive)))
    }).catch(error => { if (!cancelled) setBaselineOptionsError(getErrorMessage(error, 'Unable to load baseline runs')) }).finally(() => { if (!cancelled) setBaselineOptionsLoading(false) })
    return () => { cancelled = true }
  }, [api, showCompare, test, testId])
  const effectiveCompareId = compareId
  effectiveCompareIDRef.current = effectiveCompareId
  const baselineReadyTest = useMemo(() => baselineTest && isCompletedTestStatus(baselineTest.status) ? baselineTest : null, [baselineTest])
  const { archiveSummaryHistory: baselineSummaryHistory, archiveSummariesLoading: baselineSummaryLoading } = useExecutionArchiveSummaryHistory(baselineReadyTest)

  useEffect(() => {
    verdictRevisionRef.current += 1
    const next = verdictDraftFrom(archiveReportRef.current, null)
    setAnnotations(null)
    setVerdictBaseline(next)
    setVerdictDraft(next)
  }, [activeLens, testId])

  useEffect(() => {
    const next = verdictDraftFrom(archiveModel.archiveReport, annotations)
    setVerdictBaseline(current => current.status === next.status && current.explanation === next.explanation ? current : next)
    if (!verdictDirtyRef.current) {
      setVerdictDraft(current => current.status === next.status && current.explanation === next.explanation ? current : next)
    }
  }, [annotations, archiveModel.archiveReport])

  useEffect(() => {
    if (initialLens && isReportLens(initialLens)) setActiveLens(initialLens)
  }, [initialLens])

  useEffect(() => {
    setCompareId(initialCompare || '')
    if (initialCompare) setShowCompare(true)
  }, [initialCompare, testId])
  useEffect(() => { setAreaState('Summary'); setPopulationChoice(initialPopulation || null); setShowComposer(false) }, [testId, initialPopulation])
  const appliedAnalysisSelection = useRef('')
  useEffect(() => {
    const stamp = JSON.stringify([testId, initialTransaction, initialPopulation])
    if (!archiveModel.archiveReport || !initialTransaction || appliedAnalysisSelection.current === stamp) return
    if (archiveModel.archiveReport.manifest?.test_id && archiveModel.archiveReport.manifest.test_id !== testId) return
    const stats = initialPopulation === 'transaction' ? archiveModel.archiveReport.by_transaction : initialPopulation === 'action' ? archiveModel.archiveReport.by_request : archiveModel.archiveReport.by_action
    if (stats?.[initialTransaction]) archiveModel.onSelectedArchiveActionChange(initialTransaction)
    appliedAnalysisSelection.current = stamp
  }, [testId, initialTransaction, initialPopulation, archiveModel])
  useEffect(() => { setAreaState(({ summary: 'Summary', performance: 'Performance', comparison: 'Comparison', diagnostics: 'Diagnostics', evidence: 'Evidence', execution: 'Execution' } as const)[initialSection ?? 'summary']) }, [testId, initialSection])
  useEffect(() => {
    pinCompareStamp.current += 1
  }, [effectiveCompareId])

  useEffect(() => {
    if (!test) return
    if (!completed) {
      onOpenCommandCenter(testId)
    }
  }, [completed, onOpenCommandCenter, test, testId])

  useEffect(() => {
    const requestGeneration = ++annotationRequestGenerationRef.current
    let cancelled = false
    setAnnotationsLoading(true)
    setAnnotationsError('')
    setAnnotations(null)
    api.getTestReportAnnotations(testId, activeLens)
      .then(response => {
        if (!cancelled && requestGeneration === annotationRequestGenerationRef.current) {
          setAnnotations(response.annotations)
        }
      })
      .catch(error => {
        if (!cancelled && requestGeneration === annotationRequestGenerationRef.current) {
          setAnnotations(null)
          setAnnotationsError(getErrorMessage(error, 'Unable to load report annotations'))
        }
      })
      .finally(() => {
        if (!cancelled && requestGeneration === annotationRequestGenerationRef.current) setAnnotationsLoading(false)
      })
    return () => {
      cancelled = true
      if (annotationRequestGenerationRef.current === requestGeneration) {
        annotationRequestGenerationRef.current += 1
      }
    }
  }, [activeLens, api, testId])

  useEffect(() => {
    if (!effectiveCompareId || effectiveCompareId === testId) {
      setBaselineTest(null)
      setBaselineReport(null)
      setBaselineError(effectiveCompareId === testId ? 'Choose a different run as the baseline.' : '')
      setBaselineLoading(false)
      return
    }
    let cancelled = false
    setBaselineLoading(true)
    setBaselineReport(null)
    setBaselineTest(null)
    setBaselineError('')
    Promise.all([
      api.getTest(effectiveCompareId),
      api.getTestArchiveReport(effectiveCompareId, { includeReleaseTrend: false }),
    ]).then(([baseline, report]) => {
      if (cancelled) return
      setBaselineTest(baseline)
      setBaselineReport(report)
    }).catch(error => {
      if (cancelled) return
      setBaselineTest(null)
      setBaselineReport(null)
      setBaselineError(getErrorMessage(error, 'Unable to load comparison baseline'))
    }).finally(() => {
      if (!cancelled) setBaselineLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [api, effectiveCompareId, testId])

  const timelineHistory = useMemo(() => {
    const merged = mergeTimelineHistory(archiveSummaryHistory, archiveResourceHistory)
    return (archiveModel.archiveReport?.metric_populations?.transaction?.transactions || 0) > 0 && (baselineReport?.metric_populations?.transaction?.transactions || 0) > 0 ? alignBaselineTimeline(merged, baselineSummaryHistory) : merged
  }, [archiveResourceHistory, archiveSummaryHistory, baselineSummaryHistory, archiveModel.archiveReport, baselineReport])

  const baselineDiff = useMemo(
    () => buildBaselineOverviewDiff(reportPopulationOverview(archiveModel.archiveReport, population), reportPopulationOverview(baselineReport, population)),
    [archiveModel.archiveReport, baselineReport, population],
  )
  const kpis = useMemo(
    () => buildReportKpis(archiveModel.archiveReport, population === 'transaction' && reportPopulationOverview(archiveModel.archiveReport, population) ? timelineHistory : [], baselineDiff, population),
    [archiveModel.archiveReport, baselineDiff, timelineHistory, population],
  )
  const slaRows = useMemo(() => slaRowsForReport(archiveModel.archiveReport), [archiveModel.archiveReport])
  const goalConfig = archiveModel.archiveReport?.manifest?.provenance?.goal_config

  const setLens = (lens: ReportLens) => {
    if (lens === activeLens) return
    runGuarded(() => {
      setActiveLens(lens)
      onNavigateReport({ section: area.toLowerCase() as ReportSection, lens, compare: compareId || undefined, comparisonId: initialComparisonId })
    })
  }

  const setCompare = (id: string) => {
    const normalized = id.trim()
    if (normalized === compareId) return
    if (normalized === testId) { setBaselineError('Choose a different run as the baseline.'); return }
    runGuarded(() => {
      setCompareId(normalized)
      onNavigateReport({ section: area.toLowerCase() as ReportSection, lens: activeLens, compare: normalized || undefined })
    })
  }

  const saveOverride = useCallback((status: string, explanation: string): Promise<boolean> => {
    const current = annotations
    if (!current) {
      setAnnotationsError('Report annotations are unavailable. Reload the report before saving a verdict override.')
      return Promise.resolve(false)
    }
    const requestLens = activeLens
    const requestTestID = testId
    const requestRevision = verdictRevisionRef.current
    const saveKey = JSON.stringify([requestTestID, requestLens, status, explanation, current.updated_at || ''])
    if (activeVerdictSaveRef.current?.key === saveKey) {
      return activeVerdictSaveRef.current.promise
    }
    const requestGeneration = ++annotationSaveGenerationRef.current
    const requestIsCurrent = () => requestGeneration === annotationSaveGenerationRef.current
      && activeLensRef.current === requestLens
      && testIDRef.current === requestTestID
    setSavingAnnotations(true)
    setAnnotationsError('')
    const performSave = async (): Promise<boolean> => {
      try {
        let latest = current
        let response: Awaited<ReturnType<typeof api.saveTestReportAnnotations>> | null = null
        for (let attempt = 0; attempt < 3; attempt += 1) {
          try {
            response = await api.saveTestReportAnnotations(requestTestID, requestLens, {
              expected_updated_at: latest.updated_at,
              title: latest.title,
              prepared_by: latest.prepared_by,
              executive_narrative: latest.executive_narrative,
              section_comments: latest.section_comments || {},
              verdict_override: status,
              verdict_explanation: explanation,
              findings: latest.findings || [],
            })
            break
          } catch (error: unknown) {
            if (httpStatus(error) !== 409 || attempt === 2) throw error
            latest = (await api.getTestReportAnnotations(requestTestID, requestLens)).annotations
          }
        }
        if (!response) return false
        if (requestIsCurrent()) {
          const savedDraft = verdictDraftFrom(archiveModel.archiveReport, response.annotations)
          setAnnotations(response.annotations)
          setVerdictBaseline(savedDraft)
          if (verdictRevisionRef.current === requestRevision) setVerdictDraft(savedDraft)
        }
        return true
      } catch (error: unknown) {
        // Never let a rejected save go unhandled and silently succeed: an
        // audit-relevant verdict override must surface its failure so the analyst
        // does not navigate away believing it was recorded.
        if (requestIsCurrent()) setAnnotationsError(getErrorMessage(error, 'Unable to save the verdict override'))
        return false
      } finally {
        if (requestGeneration === annotationSaveGenerationRef.current) setSavingAnnotations(false)
      }
    }

    const savePromise = verdictSaveTailRef.current.then(performSave, performSave)
    verdictSaveTailRef.current = savePromise.then(() => undefined, () => undefined)
    activeVerdictSaveRef.current = { key: saveKey, promise: savePromise }
    void savePromise.finally(() => {
      if (activeVerdictSaveRef.current?.promise === savePromise) activeVerdictSaveRef.current = null
    })
    return savePromise
  }, [activeLens, annotations, api, archiveModel.archiveReport, testId])

  const updateVerdictStatus = useCallback((status: string) => {
    verdictRevisionRef.current += 1
    setVerdictDraft(current => ({ ...current, status }))
  }, [])

  const updateVerdictExplanation = useCallback((explanation: string) => {
    verdictRevisionRef.current += 1
    setVerdictDraft(current => ({ ...current, explanation }))
  }, [])

  const discardVerdictDraft = useCallback(() => {
    verdictRevisionRef.current += 1
    setVerdictDraft(verdictBaselineRef.current)
  }, [])

  const saveVerdictDraftRef = useRef<() => Promise<boolean>>(async () => false)
  saveVerdictDraftRef.current = () => saveOverride(verdictDraftRef.current.status, verdictDraftRef.current.explanation)
  const verdictNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: `run-report-verdict-${testId}`,
    label: 'Run report verdict override',
    isDirty: () => verdictDirtyRef.current,
    save: () => saveVerdictDraftRef.current(),
    discard: discardVerdictDraft,
  }), [discardVerdictDraft, testId])
  useNavigationGuard(verdictNavigationGuard, verdictDirty)

  const pinBaseline = async (): Promise<boolean> => {
    if (!effectiveCompareId) return false
    const requestTestID = testId
    const requestCompareID = effectiveCompareId
    const requestPinStamp = pinCompareStamp.current
    try {
      await api.pinComparisonBaseline(requestCompareID)
      setBaselineError('')
      if (testIDRef.current !== requestTestID || effectiveCompareIDRef.current !== requestCompareID) {
        return true
      }
      if (pinCompareStamp.current !== requestPinStamp) return true
      const refreshed = await api.getTest(requestTestID)
      setTest(refreshed)
      return true
    } catch (error: unknown) {
      if (testIDRef.current !== requestTestID || effectiveCompareIDRef.current !== requestCompareID) return false
      setBaselineError(getErrorMessage(error, 'Unable to pin the comparison baseline'))
      return false
    }
  }

  if (testError) {
    return <div className="page run-report-page"><p className="run-report-error" role="alert">{testError}</p></div>
  }

  if (!test) {
    return <div className="page run-report-page"><p role="status">Loading run snapshot...</p></div>
  }

  if (!completed) {
    return <div className="page run-report-page"><p role="status">Opening command center...</p></div>
  }

  const report = archiveModel.archiveReport
  const comparison = report && baselineReport ? reportComparisonQualification(report, baselineReport, test, baselineTest, population) : null
  const renderEvidence = (view: React.ComponentProps<typeof TestExecutionArchive>['view']) => <TestExecutionArchive
    {...archiveModel} test={test} view={view} hideReportExports
    loadImpactData={timelineHistory} loadImpactLoading={archiveSummariesLoading || archiveResourcesLoading}
    loadImpactError={archiveSummaryError || archiveResourceError}
  />
  const setArea = (next: 'Summary' | 'Performance' | 'Comparison' | 'Diagnostics' | 'Evidence' | 'Execution') => {
    // The route changes only after the navigation guard accepts. Keep the
    // current section visible if the user cancels an unsaved-assessment prompt.
    onNavigateReport({ lens: activeLens, compare: compareId || undefined, comparisonId: initialComparisonId, section: next.toLowerCase() as ReportSection })
  }
  const areas = ['Summary', 'Performance', 'Comparison', 'Diagnostics', 'Evidence', 'Execution'] as const
  const facts = executionFacts(test, report, manifestModel.manifest)
  const failed = resolvedStatus === 'FAILED'
  const hasArchive = Boolean(test.archive)
  return (
    <div className="page run-report-page">
      <div className="run-report-toolbar">
        <div><p className="run-report-kicker">Run results · {test.ref_key || test.id}</p><h1>{test.name || test.id}</h1><p><StatusBadge status={resolvedStatus} stoppedByOperator={report?.manifest?.termination_reason === 'operator_stopped'} /> · Configured {recordedNumber(facts.vusers)} VUsers for {facts.duration || 'Not recorded'} · Lifecycle elapsed {facts.lifecycleElapsed}{hasArchive && <> · Archived evidence elapsed {report ? facts.elapsed : 'Not recorded'}</>}</p></div>
        <div className="run-report-toolbar-actions">
          <button className="btn btn-secondary" type="button" disabled={!hasArchive} aria-expanded={area === 'Comparison'} onClick={() => { setShowCompare(false); setArea('Comparison') }}>Compare</button>
          <button className="btn btn-primary" type="button" disabled={!hasArchive} aria-expanded={showExport} onClick={() => setShowExport(v => !v)}>Export report</button>
          <button className="btn btn-ghost" type="button" onClick={() => setShowNotes(true)}>Test notes</button>
        </div>
      </div>
      {(failed || resolvedStatus === 'ABORTED' || facts.reason) && <section className={`run-report-panel run-execution-outcome${failed ? ' run-execution-outcome-failed' : ''}`} aria-label="Execution outcome">
        <h2>{failed ? 'Execution failed' : resolvedStatus === 'ABORTED' ? 'Execution aborted' : 'Recorded execution outcome'}</h2>
        <p>{facts.reason || 'No termination detail was recorded.'}</p>
        <p className="run-report-muted">Execution status is separate from sampled transaction success and performance acceptance.</p>
      </section>}
      {!hasArchive && <section className="run-report-panel run-evidence-notice" aria-label="Evidence availability">
        <div>{test.archive_failure ? <>
          <h2>{test.archive_failure.reason === 'archive_collection_unconfirmed' ? 'Archive collection unconfirmed' : 'Archive collection failed'}</h2>
          <p>{test.archive_failure.reason === 'archive_collection_unconfirmed'
            ? 'The run ended before archive collection was confirmed. Collection may still be in progress or may have been interrupted.'
            : test.archive_failure.reason === 'remote_collection_unavailable'
            ? 'No generator archive was available when collection finished.'
            : 'The controller could not collect the generator archive.'}</p>
          <p>Recorded: {executionDate(test.archive_failure.recorded_at)}</p>
          <p>Retained controller records are available below and may be incomplete. Checking again refreshes archive availability; it does not restart collection.</p>
        </> : <><h2>Saved archive unavailable</h2><p>Retained controller records are available below. Archive publication progress is unknown; these records do not establish complete workload or performance evidence.</p></>}</div>
        <button type="button" className="btn btn-secondary" disabled={archiveCheckPending} onClick={() => void checkArchiveAgain()}>{archiveCheckPending ? 'Checking…' : 'Check again'}</button>
        {archiveCheckError && <p role="alert" className="run-report-error">{archiveCheckError}</p>}
      </section>}
      {hasArchive && <RunArchiveCoverageNotice report={report} />}
      {showExport && hasArchive && <section className="run-report-panel" aria-label="Report export options">
        <h2>Export report</h2><p>Choose an audience. Outputs cover the whole run, independent of the population selector below. Any release trend in an output uses the prior release run and names that reference.</p>
        <RunReportLensSwitcher activeLens={activeLens} onLensChange={setLens} />
        <button className="btn btn-secondary" type="button" onClick={() => setShowComposer(true)}>Preview / edit commentary</button>
        <RunReportExportActions testId={testId} lens={activeLens} testName={test.name} downloadHTML={(id, lens) => api.downloadTestReport(id, lens, { comparisonId: initialComparisonId })} downloadFormat={(id, lens, format) => api.downloadTestReportFormat(id, lens, format, { comparisonId: initialComparisonId })} />
        <details><summary>Publish to Power BI</summary><RunReportPowerBIPublish testId={testId} publish={api.publishTestToPowerBI} getStatus={api.getPowerBIPublishStatus} /></details>
        <details><summary>Publish to Confluence</summary><RunReportConfluencePublish testId={testId} canPublish={canPublishConfluence} /></details>
      </section>}
      {hasArchive && area !== 'Execution' && area !== 'Comparison' && <section className="run-report-context" aria-label="Analysis context">
        <label>Metric population <select value={population} onChange={e => setPopulationChoice(e.target.value as MetricPopulation)}>{Object.entries(populationLabels).map(([key,label]) => <option key={key} value={key}>{label}</option>)}</select></label>
        <p>{populationLabels[population]} · {report?.metric_populations?.[population]?.transactions ?? 'Unknown'} samples · Whole captured run. {(report?.metric_populations?.transaction?.transactions || 0) > 0 ? 'Interval graphs show named transaction metrics.' : 'Legacy interval graphs have no identified named transaction population; their values are separate from these KPIs.'} Resource curves show the load generators.</p>
      </section>}
      {showCompare && hasArchive && <RunReportCompareBar compareId={effectiveCompareId} suggestedCompareId={suggestedCompareId} baselineName={baselineTest?.name} options={baselineOptions} optionsLoading={baselineOptionsLoading}
        deltas={baselineDiff} loading={baselineLoading} error={baselineError || baselineOptionsError || (effectiveCompareId && report && baselineReport && (!reportPopulationOverview(report, population) || !reportPopulationOverview(baselineReport, population)) ? 'No comparable samples for the selected population.' : '')}
        onCompareChange={setCompare} onPinBaseline={pinBaseline} />}
      {effectiveCompareId && !showCompare && <p className="run-report-muted">Comparing with {baselineTest?.name || effectiveCompareId}. Open Compare to change or clear the baseline.{baselineError ? ` ${baselineError}` : ''}</p>}
      {area !== 'Comparison' && effectiveCompareId && comparison && <section className="run-report-panel" role="note" aria-label="Comparison qualification">
        <h3>Comparison qualification</h3><p>{comparison.summary}</p>
        {comparison.missing.length > 0 && <p>Unverified metadata: {comparison.missing.join(', ')}.</p>}
        <p>{populationLabels[population]} records: {comparison.currentRecords ?? 'Unavailable'} versus {comparison.baselineRecords ?? 'Unavailable'}. Observed peak VUsers: {report?.overview?.peak_active_vusers ?? 'Unavailable'} versus {baselineReport?.overview?.peak_active_vusers ?? 'Unavailable'}.</p>
        <p>{comparison.revisions} These are descriptive deltas, not proof of a performance regression.</p>
        {(comparison.limitedSamples || comparison.differentObservedLoad || comparison.observedLoadUnknown) && <strong>Limited comparability: {[comparison.limitedSamples && 'insufficient sample evidence', comparison.differentObservedLoad && 'different observed load', comparison.observedLoadUnknown && 'observed load unavailable'].filter(Boolean).join('; ')}. Do not use the deltas alone as acceptance evidence.</strong>}
      </section>}
      <OrchestrationReturn testId={testId} />
      <TabList className="run-report-areas" role="tablist" aria-label="Result sections">{areas.map((name,index) => <button key={name} type="button" role="tab" id={`result-tab-${name}`} aria-controls={`result-panel-${name}`} aria-selected={area === name} tabIndex={area === name ? 0 : -1}
        onClick={() => setArea(name)} onKeyDown={e => { const next = e.key === 'ArrowRight' ? (index+1)%areas.length : e.key === 'ArrowLeft' ? (index+areas.length-1)%areas.length : e.key === 'Home' ? 0 : e.key === 'End' ? areas.length-1 : -1; if(next >= 0){e.preventDefault();setArea(areas[next]);document.getElementById(`result-tab-${areas[next]}`)?.focus()} }}>{name === 'Comparison' ? 'Baseline Comparison' : name}</button>)}</TabList>
      <section role="tabpanel" id={`result-panel-${area}`} aria-labelledby={`result-tab-${area}`} className="run-report-area-content">
        {archiveModel.archiveLoading && <p role="status">Loading archived results...</p>}
        {archiveModel.archiveError && <p role="alert" className="run-report-error">{archiveModel.archiveError}</p>}
        {hasArchive && <div hidden={area !== 'Comparison'}>{(area === 'Comparison' || initialComparisonId) && <BaselineComparisonWorkspace key={`${test.id}/${initialCompare || ''}`} test={test} initialBaseline={initialCompare} comparisonId={initialComparisonId} canEditPolicy={canPublishConfluence} onSnapshot={id => onNavigateReport({ section: 'comparison', comparisonId: id, lens: activeLens })} />}</div>}
        {area === 'Summary' && hasArchive && <>
          {report && <RunOutcomeSummary report={report} showPopulations={false} />}
          <RunReportKpiScorecard kpis={kpis} />
          <section className="run-report-panel"><h2>Findings and next steps</h2>
            {report?.error_patterns?.length ? <><p>{report.error_patterns.length} error patterns were captured.</p><button className="btn btn-secondary" onClick={() => setArea('Diagnostics')}>Investigate errors</button></> : <p>{report ? 'No recurring error patterns were captured.' : 'Findings will appear when the report loads.'}</p>}
            {report?.completeness && report.completeness.score_pct < 100 && <p>Evidence coverage is {report.completeness.score_pct}%. <button className="btn btn-ghost" onClick={() => setArea('Evidence')}>Review evidence gaps</button></p>}
          </section>
          {annotationsError && <p className="run-report-error" role="alert">{annotationsError}</p>}
          <details><summary>Analyst assessment ({activeLens} output)</summary><RunReportVerdictBanner report={report} annotations={annotations} status={verdictDraft.status} explanation={verdictDraft.explanation} saving={savingAnnotations || annotationsLoading} disabled={!annotations} onStatusChange={updateVerdictStatus} onExplanationChange={updateVerdictExplanation} onSaveOverride={saveOverride} /></details>
        </>}
        {area === 'Performance' && hasArchive && <>
          <RunReportKpiScorecard kpis={kpis} />
          <SynchronizedTimeline key={testId} data={timelineHistory} goalConfig={goalConfig} loading={archiveSummariesLoading || archiveResourcesLoading || baselineSummaryLoading} error={archiveSummaryError || archiveResourceError} showBaseline={Boolean(effectiveCompareId && timelineHistory.some(point => point.baselineTps !== undefined))} />
          <ImportedDashboardPanels test={test} baselineTest={baselineReadyTest} />
          {(report?.transaction_response_time_sla_results?.length ?? 0) > 0 ? (
            <TransactionResponseTimeSLATable
              rows={report?.transaction_response_time_sla_results ?? []}
              title="Transaction SLA Scoreboard"
              selectedGroup={archiveModel.selectedArchiveGroup}
              selectedTransaction={archiveModel.selectedArchiveAction}
              drilldownTransactions={archiveModel.archiveTransactions}
              loadingTransactions={archiveModel.archiveTransactionsLoading}
              onSelect={row => {
                archiveModel.onSelectedArchiveGroupChange(row.group)
                archiveModel.onSelectedArchiveActionChange(row.transaction)
              }}
            />
          ) : (
            <TransactionSlaScoreboard rows={slaRows} selectedAction={slaRows.some(row => row.name === archiveModel.selectedArchiveAction) ? archiveModel.selectedArchiveAction : ''} drilldownTransactions={archiveModel.archiveTransactions} loadingTransactions={archiveModel.archiveTransactionsLoading} onSelectAction={archiveModel.onSelectedArchiveActionChange} />
          )}
          {renderEvidence('action-summary')}
          <details><summary>Load impact</summary>{renderEvidence('load-impact')}</details>
        </>}
        {area === 'Diagnostics' && hasArchive && <>
          <RunReportDiagnostics report={report} loading={archiveModel.archiveAnalysisLoading} error={archiveModel.archiveAnalysisError} />
          {renderEvidence('report-error-patterns')}
          {renderEvidence('transactions')}
          <details><summary>Browser evidence</summary>{renderEvidence('report-browser-summary')}{renderEvidence('report-browser-resources')}</details>
          <details><summary>Execution path analysis</summary>{renderEvidence('report-analysis')}</details>
        </>}
        {area === 'Evidence' && hasArchive && <>
          {renderEvidence('report-completeness')}
          {report && <section className="run-outcome-summary"><h2>Metric populations</h2><RunMetricPopulations report={report} /></section>}
          {renderEvidence('report-metadata')}
          <details><summary>Raw evidence downloads</summary><button className="btn btn-secondary" onClick={() => void archiveModel.onDownloadArchive()}>Download evidence bundle</button>{renderEvidence('report-assets')}</details>
          <details><summary>Archived intervals</summary>{renderEvidence('report-summary-timeline')}</details>
        </>}
        {area === 'Execution' && <RunExecutionDetails manifestModel={manifestModel} test={test} report={report} history={timelineHistory} historyLoading={archiveSummariesLoading} historyError={archiveSummaryError} />}
        {!hasArchive && area === 'Summary' && <>
          <RunExecutionFacts test={test} report={null} manifest={manifestModel.manifest} />
          <section className="run-report-panel"><h2>Investigate this run</h2>
            <p>Performance acceptance is not assessed here. Review the recorded reason, lifecycle events, and available evidence before deciding whether to rerun.</p>
            <div className="run-report-toolbar-actions"><button type="button" className="btn btn-primary" onClick={() => setArea('Execution')}>Review execution events</button><button type="button" className="btn btn-secondary" onClick={() => setArea('Evidence')}>View retained evidence</button></div>
          </section>
        </>}
        {!hasArchive && area === 'Comparison' && <p>Baseline comparison requires saved transaction and resource archives. Check archive availability above.</p>}
        {!hasArchive && area === 'Performance' && <RetainedRunMetrics test={test} />}
        {!hasArchive && area === 'Diagnostics' && <><RetainedRunDiagnostics test={test} /><RunExecutionDetails manifestModel={manifestModel} test={test} report={null} evidenceOnly /></>}
        {!hasArchive && area === 'Evidence' && <RunExecutionDetails manifestModel={manifestModel} test={test} report={null} evidenceOnly />}
      </section>
      {showNotes && <TestNotesDialog testId={testId} testName={test.name} onClose={() => setShowNotes(false)} />}
      {showComposer && <ReportComposer comparisonId={initialComparisonId} testId={testId} testName={test.name} profile={activeLens} onClose={() => setShowComposer(false)} />}
    </div>
  )
}
