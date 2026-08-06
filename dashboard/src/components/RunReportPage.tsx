import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { isReportLens, type ReportLens } from '../appRouting'
import { useApi } from '../hooks/useApi'
import type { ReportAnnotations, RunArchiveAnalysisResponse, RunArchiveReport, TestSummary } from '../types'
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
import RunReportKpiScorecard from './runReport/RunReportKpiScorecard'
import RunReportLensSwitcher from './runReport/RunReportLensSwitcher'
import RunReportVerdictBanner from './runReport/RunReportVerdictBanner'
import SynchronizedTimeline from './runReport/SynchronizedTimeline'
import ImportedDashboardPanels from './runReport/ImportedDashboardPanels'
import TransactionSlaScoreboard from './runReport/TransactionSlaScoreboard'
import { reportSectionLens } from './runReport/reportSectionLens'
import {
  alignBaselineTimeline,
  buildBaselineOverviewDiff,
  buildReportKpis,
  effectiveReportVerdict,
  mergeTimelineHistory,
  slaRowsForReport,
} from './runReport/runReportModel'
import './RunReportPage.css'

interface Props {
  testId: string
  initialLens?: ReportLens
  initialCompare?: string
  statusOverride?: string
  onOpenCommandCenter: (id?: string) => void
  onOpenLegacyArchive: (id: string) => void
  onNavigateReport: (next: { lens?: ReportLens; compare?: string }) => void
}

function archiveReadyTest(test: TestSummary | null, status: string): TestSummary | null {
  if (!test || !isCompletedTestStatus(status)) return null
  return { ...test, status: status as TestSummary['status'] }
}

type VerdictDraft = { status: string; explanation: string }

function verdictDraftFrom(report: RunArchiveReport | null, annotations: ReportAnnotations | null): VerdictDraft {
  const verdict = effectiveReportVerdict(report, annotations)
  return { status: verdict.status, explanation: verdict.explanation }
}

function httpStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

export default function RunReportPage({
  testId,
  initialLens,
  initialCompare,
  statusOverride,
  onOpenCommandCenter,
  onOpenLegacyArchive,
  onNavigateReport,
}: Props) {
  const api = useApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [activeLens, setActiveLens] = useState<ReportLens>(initialLens || 'executive')
  const [compareId, setCompareId] = useState(initialCompare || '')
  const { test, setTest, error: testError } = useExecutionTestPolling(testId, { stopOnTerminal: true })
  const resolvedStatus = test ? resolveTestStatus(test, statusOverride ? { [test.id]: statusOverride } : {}) : ''
  const completed = isCompletedTestStatus(resolvedStatus)
  const archivedTest = useMemo(() => archiveReadyTest(test, resolvedStatus), [resolvedStatus, test])
  const archiveModel = useExecutionArchiveModel(archivedTest)
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
  const [compareAnalysis, setCompareAnalysis] = useState<RunArchiveAnalysisResponse | null>(null)
  const [compareAnalysisError, setCompareAnalysisError] = useState('')
  const [compareAnalysisLoading, setCompareAnalysisLoading] = useState(false)

  const suggestedCompareId = test?.comparison_baseline?.source_test_id || ''
  const effectiveCompareId = compareId || suggestedCompareId
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
  }, [initialCompare])
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
      setBaselineError('')
      setBaselineLoading(false)
      return
    }
    let cancelled = false
    setBaselineLoading(true)
    setBaselineError('')
    Promise.all([
      api.getTest(effectiveCompareId),
      api.getTestArchiveReport(effectiveCompareId),
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

  useEffect(() => {
    if (!effectiveCompareId || effectiveCompareId === testId || !completed) {
      setCompareAnalysis(null)
      setCompareAnalysisError('')
      setCompareAnalysisLoading(false)
      return
    }
    let cancelled = false
    setCompareAnalysisLoading(true)
    setCompareAnalysisError('')
    api.getTestArchiveAnalysis(testId, { baseline_id: effectiveCompareId, limit: 10 })
      .then(response => {
        if (!cancelled) setCompareAnalysis(response)
      })
      .catch(error => {
        if (!cancelled) {
          setCompareAnalysis(null)
          setCompareAnalysisError(getErrorMessage(error, 'Unable to load baseline analysis'))
        }
      })
      .finally(() => {
        if (!cancelled) setCompareAnalysisLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [api, completed, effectiveCompareId, testId])

  const timelineHistory = useMemo(() => {
    const merged = mergeTimelineHistory(archiveSummaryHistory, archiveResourceHistory)
    return alignBaselineTimeline(merged, baselineSummaryHistory)
  }, [archiveResourceHistory, archiveSummaryHistory, baselineSummaryHistory])

  const baselineDiff = useMemo(
    () => buildBaselineOverviewDiff(archiveModel.archiveReport?.overview, baselineReport?.overview),
    [archiveModel.archiveReport?.overview, baselineReport?.overview],
  )
  const kpis = useMemo(
    () => buildReportKpis(archiveModel.archiveReport, timelineHistory, baselineDiff),
    [archiveModel.archiveReport, baselineDiff, timelineHistory],
  )
  const slaRows = useMemo(() => slaRowsForReport(archiveModel.archiveReport), [archiveModel.archiveReport])
  const lensSections = useMemo(() => reportSectionLens(archiveModel.archiveReport, activeLens), [activeLens, archiveModel.archiveReport])
  const goalConfig = archiveModel.archiveReport?.manifest?.provenance?.goal_config

  const setLens = (lens: ReportLens) => {
    if (lens === activeLens) return
    runGuarded(() => {
      setActiveLens(lens)
      onNavigateReport({ lens, compare: compareId || undefined })
    })
  }

  const setCompare = (id: string) => {
    const normalized = id.trim()
    if (normalized === compareId) return
    runGuarded(() => {
      setCompareId(normalized)
      onNavigateReport({ lens: activeLens, compare: normalized || undefined })
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

  if (!test.archive) {
    return (
      <div className="page run-report-page">
        <section className="run-report-panel">
          <h1>No archive available</h1>
          <p>This run is complete, but no archive was recorded for report rendering.</p>
          <button className="btn btn-secondary" type="button" onClick={() => onOpenLegacyArchive(testId)}>Open legacy execution view</button>
        </section>
      </div>
    )
  }

  return (
    <div className="page run-report-page">
      <div className="run-report-toolbar">
        <div>
          <p className="run-report-kicker">Completed run report</p>
          <h1>{test.name || test.id}</h1>
        </div>
        <div className="run-report-toolbar-actions">
          <RunReportLensSwitcher activeLens={activeLens} onLensChange={setLens} />
          <button className="btn btn-sm btn-secondary" type="button" onClick={() => onOpenLegacyArchive(testId)}>Legacy archive</button>
        </div>
      </div>
      {annotationsError && <p className="run-report-error" role="alert">{annotationsError}</p>}
      {lensSections.sectionSet.has('verdict') && (
        <RunReportVerdictBanner
          report={archiveModel.archiveReport}
          annotations={annotations}
          status={verdictDraft.status}
          explanation={verdictDraft.explanation}
          saving={savingAnnotations || annotationsLoading}
          disabled={!annotations}
          onStatusChange={updateVerdictStatus}
          onExplanationChange={updateVerdictExplanation}
          onSaveOverride={saveOverride}
        />
      )}
      {lensSections.sectionSet.has('kpis') && <RunReportKpiScorecard kpis={kpis} />}
      {lensSections.sectionSet.has('timeline') && (
        <SynchronizedTimeline
          data={timelineHistory}
          goalConfig={goalConfig}
          loading={archiveSummariesLoading || archiveResourcesLoading || baselineSummaryLoading}
          error={archiveSummaryError || archiveResourceError}
          showBaseline={Boolean(effectiveCompareId && baselineSummaryHistory.length)}
        />
      )}
      {lensSections.sectionSet.has('imported_dashboards') && <ImportedDashboardPanels test={test} baselineTest={baselineReadyTest} />}
      {lensSections.sectionSet.has('sla') && (
        <TransactionSlaScoreboard
          rows={slaRows}
          selectedAction={archiveModel.selectedArchiveAction}
          drilldownTransactions={archiveModel.archiveTransactions}
          loadingTransactions={archiveModel.archiveTransactionsLoading}
          onSelectAction={archiveModel.onSelectedArchiveActionChange}
        />
      )}
      {lensSections.sectionSet.has('diagnostics') && (
        <RunReportDiagnostics
          report={archiveModel.archiveReport}
          analysis={compareAnalysis || archiveModel.archiveAnalysis}
          loading={archiveModel.archiveLoading || archiveModel.archiveAnalysisLoading || compareAnalysisLoading}
          error={archiveModel.archiveError || archiveModel.archiveAnalysisError || compareAnalysisError}
        />
      )}
      {lensSections.sectionSet.has('compare') && (
        <RunReportCompareBar
          compareId={compareId}
          suggestedCompareId={suggestedCompareId}
          baselineName={baselineTest?.name || test.comparison_baseline?.source_test_name}
          deltas={baselineDiff}
          loading={baselineLoading}
          error={baselineError}
          onCompareChange={setCompare}
          onPinBaseline={pinBaseline}
        />
      )}
      {lensSections.sectionSet.has('exports') && (
        <RunReportExportActions
          testId={testId}
          lens={activeLens}
          testName={test.name}
          downloadHTML={api.downloadTestReport}
          downloadFormat={api.downloadTestReportFormat}
        />
      )}
      {lensSections.sectionSet.has('exports') && (
        <RunReportPowerBIPublish
          testId={testId}
          publish={api.publishTestToPowerBI}
          getStatus={api.getPowerBIPublishStatus}
        />
      )}
    </div>
  )
}
