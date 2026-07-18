import { useEffect, useMemo, useState } from 'react'

import { isReportLens, type ReportLens } from '../appRouting'
import { useApi } from '../hooks/useApi'
import type { ReportAnnotations, RunArchiveAnalysisResponse, RunArchiveReport, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { isCompletedTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'
import { useExecutionArchiveResourceHistory } from './useExecutionArchiveResourceHistory'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'
import { useExecutionTestPolling } from './useExecutionTestPolling'
import RunReportCompareBar from './runReport/RunReportCompareBar'
import RunReportDiagnostics from './runReport/RunReportDiagnostics'
import RunReportExportActions from './runReport/RunReportExportActions'
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
  const [baselineTest, setBaselineTest] = useState<TestSummary | null>(null)
  const [baselineReport, setBaselineReport] = useState<RunArchiveReport | null>(null)
  const [baselineError, setBaselineError] = useState('')
  const [baselineLoading, setBaselineLoading] = useState(false)
  const [compareAnalysis, setCompareAnalysis] = useState<RunArchiveAnalysisResponse | null>(null)
  const [compareAnalysisError, setCompareAnalysisError] = useState('')
  const [compareAnalysisLoading, setCompareAnalysisLoading] = useState(false)

  const suggestedCompareId = test?.comparison_baseline?.source_test_id || ''
  const effectiveCompareId = compareId || suggestedCompareId
  const baselineReadyTest = useMemo(() => baselineTest && isCompletedTestStatus(baselineTest.status) ? baselineTest : null, [baselineTest])
  const { archiveSummaryHistory: baselineSummaryHistory, archiveSummariesLoading: baselineSummaryLoading } = useExecutionArchiveSummaryHistory(baselineReadyTest)

  useEffect(() => {
    if (initialLens && isReportLens(initialLens)) setActiveLens(initialLens)
  }, [initialLens])

  useEffect(() => {
    setCompareId(initialCompare || '')
  }, [initialCompare])

  useEffect(() => {
    if (!test) return
    if (!completed) {
      onOpenCommandCenter(testId)
    }
  }, [completed, onOpenCommandCenter, test, testId])

  useEffect(() => {
    let cancelled = false
    setAnnotationsLoading(true)
    setAnnotationsError('')
    api.getTestReportAnnotations(testId, activeLens)
      .then(response => {
        if (!cancelled) setAnnotations(response.annotations)
      })
      .catch(error => {
        if (!cancelled) {
          setAnnotations(null)
          setAnnotationsError(getErrorMessage(error, 'Unable to load report annotations'))
        }
      })
      .finally(() => {
        if (!cancelled) setAnnotationsLoading(false)
      })
    return () => {
      cancelled = true
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
    setActiveLens(lens)
    onNavigateReport({ lens, compare: compareId || undefined })
  }

  const setCompare = (id: string) => {
    const normalized = id.trim()
    setCompareId(normalized)
    onNavigateReport({ lens: activeLens, compare: normalized || undefined })
  }

  const saveOverride = async (status: string, explanation: string): Promise<boolean> => {
    const current = annotations
    if (!current) return false
    setSavingAnnotations(true)
    setAnnotationsError('')
    try {
      const response = await api.saveTestReportAnnotations(testId, activeLens, {
        title: current.title,
        prepared_by: current.prepared_by,
        executive_narrative: current.executive_narrative,
        section_comments: current.section_comments || {},
        verdict_override: status,
        verdict_explanation: explanation,
        findings: current.findings || [],
      })
      setAnnotations(response.annotations)
      return true
    } catch (error: unknown) {
      // Never let a rejected save go unhandled and silently succeed: an
      // audit-relevant verdict override must surface its failure so the analyst
      // does not navigate away believing it was recorded.
      setAnnotationsError(getErrorMessage(error, 'Unable to save the verdict override'))
      return false
    } finally {
      setSavingAnnotations(false)
    }
  }

  const pinBaseline = async () => {
    if (!effectiveCompareId) return
    try {
      const updated = await api.pinComparisonBaseline(effectiveCompareId)
      if (updated.id === testId) setTest(updated)
      setBaselineError('')
    } catch (error: unknown) {
      setBaselineError(getErrorMessage(error, 'Unable to pin the comparison baseline'))
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
          saving={savingAnnotations || annotationsLoading}
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
    </div>
  )
}
