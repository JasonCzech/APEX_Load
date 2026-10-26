import RunReportProjectPanels from './RunReportProjectPanels'
import MessagingProgressDiagnostics from './MessagingProgressDiagnostics'
import MessagingDiagnostics from './MessagingDiagnostics'
import RunAnnotations from './RunAnnotations'
import BaselineComparisonWorkspace from './baselineComparison/BaselineComparisonWorkspace'
import { loadAllTestSummaries } from './testCatalogPagination'
import { useExecutionTestManifest } from './useExecutionTestManifest'
import RunExecutionDetails, { RunExecutionFacts, RetainedRunMetrics, RetainedRunDiagnostics } from './runReport/RunExecutionDetails'
import { executionFacts, recordedNumber } from './runReport/executionPresentation'
import StatusBadge from './StatusBadge'
import TestNotesDialog from './TestNotesDialog'
import { reportComparisonQualification } from './reportComparisonQualification'
import { OrchestrationReturn } from './OrchestrationReturn'
import { parseAppViewFromHash, scopeAppViewToWorkspace, viewToLocationHash, workspaceScopeForAppView, type ReportFocus, type ReportSection, type RootCauseRouteState } from '../appRouting'
import TabList from './ux/TabList'
import TestExecutionArchive from './TestExecutionArchive'
import LiveTablesPanel from './LiveTablesPanel'
import RunOutcomeSummary, { RunMetricPopulations } from './RunOutcomeSummary'
import ReportComposer from './ReportComposer'
import { defaultReportPopulation, reportPopulationActions, reportPopulationOverview, populationLabels, type MetricPopulation } from './runReport/reportPresentation'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { isReportLens, type ReportLens } from '../appRouting'
import { useApi } from '../hooks/useApi'
import type { ReportAnnotations, RunArchiveErrorPattern, RunArchiveReport, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { formatFixed } from '../utils/numberFormat'
import { isCompletedTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'
import { useExecutionArchiveResourceHistory } from './useExecutionArchiveResourceHistory'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'
import { useExecutionTestPolling } from './useExecutionTestPolling'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import RunReportCompareBar from './runReport/RunReportCompareBar'
import RunReportDiagnostics from './runReport/RunReportDiagnostics'
import RootCausePanel, { type RcaHypothesisActions } from './rca/RootCausePanel'
import BusinessKeyLookup from './runReport/BusinessKeyLookup'
import { formatRcaWindow, rcaHypothesisWindow, rcaPatternHypotheses, rcaSignatureLabel, rcaTimelineOverlays, type RcaSummaryAction } from './rca/rcaModel'
import { useRcaPhases } from './rca/useRcaPhases'
import type { TimelineOverlay } from './runReport/timelineOverlays'
import { useRootCauseAnalysis } from './rca/useRootCauseAnalysis'
import { rcaSummaryWithLatest, useRootCauseSummary } from './rca/useRootCauseSummary'
import { appendReportFinding, createRunAnnotation, rcaHypothesisAnnotation, rcaHypothesisFinding, rcaHypothesisNoteDraft } from './rca/rcaActions'
import { settingsHref } from './settingsNavigation'
import RunReportExportActions from './runReport/RunReportExportActions'
import RunReportPowerBIPublish from './runReport/RunReportPowerBIPublish'
import RunReportConfluencePublish from './runReport/RunReportConfluencePublish'
import RunReportKpiScorecard from './runReport/RunReportKpiScorecard'
import RunReportLensSwitcher from './runReport/RunReportLensSwitcher'
import { REPORT_LENS_LABELS } from './runReport/reportLensLabels'
import RunArchiveCoverageNotice from './runReport/RunArchiveCoverageNotice'
import RunArchiveAvailabilitySummary from './runReport/RunArchiveAvailabilitySummary'
import RunReportGroupBreakdown from './runReport/RunReportGroupBreakdown'
import RunReportVerdictBanner from './runReport/RunReportVerdictBanner'
import SynchronizedTimeline, { type TimelineAnnotation } from './runReport/SynchronizedTimeline'
import ReleaseReadinessSummary from './runReport/ReleaseReadinessSummary'
import ImportedDashboardPanels from './runReport/ImportedDashboardPanels'
import TransactionSlaScoreboard from './runReport/TransactionSlaScoreboard'
import JourneyRiskPanel from './runReport/JourneyRiskPanel'
import BrowserIntelligenceView from './runReport/BrowserIntelligenceView'
import TransactionResponseTimeSLATable from './TransactionResponseTimeSLATable'
import {
  alignBaselineTimeline,
  buildBaselineOverviewDiff,
  buildReportKpis,
  mergeTimelineHistory,
  slaRowsForReport,
} from './runReport/runReportModel'
import './RunReportPage.css'

type ReportArea = 'Summary' | 'Performance' | 'Load Impact' | 'Comparison' | 'Diagnostics' | 'Browser' | 'Evidence' | 'Execution'

// The route names a section by its tab's label in kebab case.
function reportSectionForArea(area: ReportArea): ReportSection {
  return area.toLowerCase().replace(/ /g, '-') as ReportSection
}

interface Props {
  testId: string
  canManageTests?: boolean
  canPublishConfluence?: boolean
  // canManageProject lets a project admin approve business keys.
  canManageProject?: boolean
  initialLens?: ReportLens
  initialCompare?: string
  initialComparisonId?: string
  initialTransaction?: string
  initialPopulation?: MetricPopulation
  initialSection?: ReportSection
  /** Content an old archive-tab link named: opened and scrolled to once the report loads. */
  initialFocus?: ReportFocus
  initialRootCause?: RootCauseRouteState
  statusOverride?: string
  onOpenCommandCenter: (id?: string) => void
  onNavigateReport: (next: { lens?: ReportLens; compare?: string; comparisonId?: string; section?: ReportSection; population?: MetricPopulation; focus?: ReportFocus } & RootCauseRouteState) => void
}

// runHasBrowserArea: the Browser area appears for runs with browser groups or
// document evidence, and for runs that pinned a browser policy (journey risk,
// which any protocol may pin, is on the Performance area).
function runHasBrowserArea(test: TestSummary, report: RunArchiveReport | null): boolean {
  return Boolean(test.groups?.some(group => group.protocol === 'browser') || report?.browser_documents || report?.browser_document_evidence !== undefined || report?.overview?.browser
    || report?.manifest?.provenance?.policy_pins?.some(pin => pin.kind === 'browser_policy'))
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
  canManageTests = false,
  canPublishConfluence = false,
  canManageProject = false,
  initialLens,
  initialCompare,
  initialComparisonId,
  initialTransaction,
  initialPopulation,
  initialSection,
  initialFocus,
  initialRootCause,
  statusOverride,
  onOpenCommandCenter,
  onNavigateReport,
}: Props) {
  const api = useApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [activeLens, setActiveLens] = useState<ReportLens>(initialLens || 'executive')
  const [area, setAreaState] = useState<ReportArea>('Summary')
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
  // The report's release trend feeds Release readiness (and the exports read it too), so it is requested here.
  const archiveModel = useExecutionArchiveModel(archivedTest, true, compareId, populationChoice)
  const { archiveSummaryHistory, archiveSummaryError, archiveSummariesLoading } = useExecutionArchiveSummaryHistory(archivedTest)
  const { archiveResourceHistory, archiveResourceError, archiveResourcesLoading } = useExecutionArchiveResourceHistory(archivedTest)
  const rootCause = useRootCauseAnalysis(testId, initialRootCause?.analysis, Boolean(archivedTest?.archive))
  // The run's root cause in brief, kept current while its newest analysis
  // (an automatic one, say) is queued or running; the panel reloads once it
  // ends.
  // An analysis started or finished in Diagnostics since the report loaded
  // reaches the Summary too.
  const reportRootCauseSummary = archiveModel.archiveReport?.root_cause_summary
  const rootCauseSummaryInput = useMemo(() => rcaSummaryWithLatest(reportRootCauseSummary, rootCause.response), [reportRootCauseSummary, rootCause.response])
  const liveRootCause = useRootCauseSummary(testId, rootCauseSummaryInput, rootCause.reload)
  const [annotationReload, setAnnotationReload] = useState(0)
  // The Performance timeline marks the analysis's anomalies, the spans the
  // run was not valid and its load steps.
  const rcaAssessment = rootCause.response?.presented ? rootCause.response.assessment : undefined
  const rcaPhases = useRcaPhases(testId, archiveModel.archiveReport, rcaAssessment?.detection, Boolean(rcaAssessment) && area === 'Performance')
  const timelineOverlays = useMemo(() => (rcaAssessment ? rcaTimelineOverlays(rcaAssessment, rcaPhases.phases) : undefined), [rcaAssessment, rcaPhases.phases])
  const [noteDraft, setNoteDraft] = useState('')

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
  useEffect(() => { setAreaState('Summary'); setShowComposer(false) }, [testId])
  // The population lives in the URL, so a shared link reopens the same view.
  useEffect(() => { setPopulationChoice(initialPopulation || null) }, [testId, initialPopulation])
  const appliedAnalysisSelection = useRef('')
  useEffect(() => {
    const stamp = JSON.stringify([testId, initialTransaction, initialPopulation])
    if (!archiveModel.archiveReport || !initialTransaction || appliedAnalysisSelection.current === stamp) return
    if (archiveModel.archiveReport.manifest?.test_id && archiveModel.archiveReport.manifest.test_id !== testId) return
    const stats = reportPopulationActions(archiveModel.archiveReport, initialPopulation || defaultReportPopulation(archiveModel.archiveReport))
    if (stats?.[initialTransaction]) archiveModel.onSelectedArchiveActionChange(initialTransaction)
    appliedAnalysisSelection.current = stamp
  }, [testId, initialTransaction, initialPopulation, archiveModel])
  // Load impact had been part of Performance: a saved link to it there opens its own section.
  const legacyLoadImpact = initialFocus === 'load-impact' && initialSection === 'performance'
  useEffect(() => { setAreaState(legacyLoadImpact ? 'Load Impact' : ({ summary: 'Summary', performance: 'Performance', 'load-impact': 'Load Impact', comparison: 'Comparison', diagnostics: 'Diagnostics', browser: 'Browser', evidence: 'Evidence', execution: 'Execution' } as const)[initialSection ?? 'summary']) }, [testId, initialSection, legacyLoadImpact])
  const focusHandledRef = useRef('')
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
  const slaPopulation: MetricPopulation = archiveModel.archiveReport?.metric_populations ? 'transaction' : 'unknown'
  const slaRows = useMemo(() => slaRowsForReport(archiveModel.archiveReport), [archiveModel.archiveReport])
  const goalConfig = archiveModel.archiveReport?.manifest?.provenance?.goal_config

  const choosePopulation = (next: MetricPopulation) => {
    setPopulationChoice(next)
    onNavigateReport({ section: reportSectionForArea(area), lens: activeLens, compare: compareId || undefined, comparisonId: initialComparisonId, population: next })
  }
  const [linkCopied, setLinkCopied] = useState('')
  const [timelineAnnotations, setTimelineAnnotations] = useState<TimelineAnnotation[]>([])
  const copyReportLink = async () => {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setLinkCopied('Link copied. It opens this report, section and audience for anyone with access to the project.')
    } catch {
      setLinkCopied(`Copy this link: ${window.location.href}`)
    }
  }

  const setLens = (lens: ReportLens) => {
    if (lens === activeLens) return
    runGuarded(() => {
      setActiveLens(lens)
      onNavigateReport({ section: reportSectionForArea(area), lens, compare: compareId || undefined, comparisonId: initialComparisonId })
    })
  }

  const setCompare = (id: string) => {
    const normalized = id.trim()
    if (normalized === compareId) return
    if (normalized === testId) { setBaselineError('Choose a different run as the baseline.'); return }
    runGuarded(() => {
      setCompareId(normalized)
      onNavigateReport({ section: reportSectionForArea(area), lens: activeLens, compare: normalized || undefined })
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
              // The verdict is shared by every audience; a change made from
              // another audience since this one loaded must not be reverted.
              expected_verdict_updated_at: current.verdict_updated_at,
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

  // Archive-tab deep links (load impact, action summary, transaction
  // drill-down) and root-cause links open and scroll to their content once
  // the report, and for root cause its analysis, renders.
  const focusReport = archiveModel.archiveReport
  const focusAnalysis = rootCause.response?.analysis_id || rootCause.response?.state || ''
  useEffect(() => {
    const key = `${testId}:${initialFocus || ''}:${initialRootCause?.analysis || ''}`
    if (!initialFocus || !focusReport || focusHandledRef.current === key) return
    const target = document.querySelector<HTMLElement>(`[data-report-focus="${initialFocus}"]`)
    if (!target) return
    focusHandledRef.current = key
    if (target instanceof HTMLDetailsElement) target.open = true
    target.scrollIntoView?.({ block: 'start' })
    // A section without a heading of its own takes the focus itself.
    const heading = target.querySelector<HTMLElement>('summary, h2, h3') || target
    if (heading && heading.tagName !== 'SUMMARY' && !heading.hasAttribute('tabindex')) heading.tabIndex = -1
    heading?.focus()
  }, [area, initialFocus, initialRootCause?.analysis, focusAnalysis, focusReport, testId])
  // A Browser link to a run without a Browser area falls back to Summary once
  // the report that decides it has loaded (or cannot).
  const browserDecided = Boolean(archiveModel.archiveReport || archiveModel.archiveError || (test && !test.archive))
  const browserAvailable = test ? runHasBrowserArea(test, archiveModel.archiveReport) : false
  useEffect(() => {
    if (area === 'Browser' && browserDecided && !browserAvailable) setAreaState('Summary')
  }, [area, browserDecided, browserAvailable])

  if (testError) {
    return <div className="page run-report-page"><p className="run-report-error" role="alert">{testError}</p></div>
  }

  if (!test) {
    return <div className="page run-report-page"><p role="status">Loading run snapshot…</p></div>
  }

  if (!completed) {
    return <div className="page run-report-page"><p role="status">Opening command center…</p></div>
  }

  const report = archiveModel.archiveReport
  const comparison = report && baselineReport ? reportComparisonQualification(report, baselineReport, test, baselineTest, population) : null
  // Error patterns name the root-cause hypotheses they overlap.
  const errorPatternHypotheses = rcaAssessment ? (pattern: RunArchiveErrorPattern) => rcaPatternHypotheses(rcaAssessment, pattern).map(h => ({ id: h.id, rank: h.rank, title: rcaSignatureLabel(h.signature) })) : undefined
  const openHypothesis = (id: string) => selectTimelineOverlay({ id, kind: 'anomaly', start: 0, label: '', tone: 'danger', ref: `hypothesis:${id}` })
  const renderEvidence = (view: React.ComponentProps<typeof TestExecutionArchive>['view']) => <TestExecutionArchive
    {...archiveModel} test={test} view={view} hideReportExports errorPatternHypotheses={errorPatternHypotheses} onOpenHypothesis={openHypothesis}
    loadImpactData={timelineHistory} loadImpactLoading={archiveSummariesLoading || archiveResourcesLoading}
    loadImpactError={archiveSummaryError || archiveResourceError}
  />
  const setArea = (next: ReportArea) => {
    // The route changes only after the navigation guard accepts. Keep the
    // current section visible if the user cancels an unsaved-assessment prompt.
    onNavigateReport({ lens: activeLens, compare: compareId || undefined, comparisonId: initialComparisonId, section: reportSectionForArea(next) })
  }
  // Root-cause state lives in the route, so a shared link reopens the same
  // analysis, hypothesis and window.
  const navigateRootCause = (next: RootCauseRouteState & { focus?: ReportFocus }) => {
    onNavigateReport({ lens: activeLens, compare: compareId || undefined, comparisonId: initialComparisonId, section: 'diagnostics', ...next })
  }
  // The Summary's root-cause cell opens the leading hypothesis, the run's
  // largest changes when no cause is supported, or the analysis.
  const openRootCause = (action: RcaSummaryAction) => {
    const summary = liveRootCause
    if (!summary?.analysis_id) {
      navigateRootCause({ analysis: undefined, rev: undefined, hypothesis: undefined, window: undefined, focus: 'root-cause' })
      return
    }
    const pinned = { analysis: summary.analysis_id, rev: summary.revision ? String(summary.revision) : undefined }
    const topID = summary.top_hypothesis?.id
    if (action === 'investigate' && topID) {
      // The interactive copy of the same analysis gives the hypothesis's window.
      const assessment = rootCause.response?.analysis_id === summary.analysis_id ? rootCause.response?.assessment : undefined
      const hypothesis = assessment?.hypotheses.find(h => h.id === topID)
      const span = hypothesis && assessment ? rcaHypothesisWindow(hypothesis, assessment.anomalies) : null
      navigateRootCause({ ...pinned, hypothesis: topID, window: span ? formatRcaWindow(span) : undefined })
      return
    }
    navigateRootCause({ ...pinned, hypothesis: undefined, window: undefined, focus: action === 'review' ? 'rca-changes' : 'root-cause' })
  }
  // An editor analyzes the root cause against a comparison baseline; the
  // analysis opens in Diagnostics, where a refusal is explained too.
  const rootCauseOpen = Boolean(rootCause.response?.presented && rootCause.response.analysis_enabled !== false)
  const analyzeAgainstBaseline = canPublishConfluence && rootCauseOpen ? async (baselineId: string): Promise<string> => {
    const next = await rootCause.request({ baseline_test_id: baselineId })
    if (!next?.analysis_id) {
      navigateRootCause({ focus: 'root-cause' })
      throw new Error('The analysis could not be started; the Root cause panel says why.')
    }
    navigateRootCause({ analysis: next.analysis_id, rev: next.revision ? String(next.revision) : undefined, hypothesis: undefined, window: undefined, focus: 'root-cause' })
    return `Analysis revision ${next.revision} is ${next.state}.`
  } : undefined
  // A root-cause mark on the Performance timeline opens its hypothesis, or
  // its window among the run's largest changes.
  const selectTimelineOverlay = (overlay: TimelineOverlay) => {
    const pinned = rootCause.response?.analysis_id ? { analysis: rootCause.response.analysis_id, rev: rootCause.response.revision ? String(rootCause.response.revision) : undefined } : {}
    if (overlay.ref?.startsWith('hypothesis:')) {
      const id = overlay.ref.slice('hypothesis:'.length)
      const hypothesis = rcaAssessment?.hypotheses.find(h => h.id === id)
      const span = hypothesis && rcaAssessment ? rcaHypothesisWindow(hypothesis, rcaAssessment.anomalies) : null
      navigateRootCause({ ...pinned, hypothesis: id, window: span ? formatRcaWindow(span) : undefined })
    } else if (overlay.ref?.startsWith('window:')) {
      navigateRootCause({ ...pinned, hypothesis: undefined, window: overlay.ref.slice('window:'.length), focus: 'rca-changes' })
    }
  }
  // Report links that keep the project, application and release in scope.
  const scope = workspaceScopeForAppView(parseAppViewFromHash(window.location.hash))
  const monitorEvidenceHref = viewToLocationHash(scopeAppViewToWorkspace({ page: 'report', testId, section: 'evidence', focus: 'monitor-evidence' }, scope))
  const monitorSourcesHref = canManageProject ? viewToLocationHash(scopeAppViewToWorkspace({ page: 'monitoring', category: 'telemetry' }, scope)) : undefined
  const hypothesisLink = (hypothesisID: string) => {
    const relative = viewToLocationHash(scopeAppViewToWorkspace({ page: 'report', testId, section: 'diagnostics', analysis: rootCause.response?.analysis_id, hypothesis: hypothesisID }, scope))
    return /^https?:$/.test(window.location.protocol) ? `${window.location.origin}${window.location.pathname}${relative}` : relative
  }
  // An editor hands a hypothesis to the report's findings, the run's
  // timeline notes or a test note.
  const hypothesisActions: RcaHypothesisActions | undefined = canPublishConfluence ? {
    recordFinding: async (hypothesis, assessment) => {
      const analysisID = rootCause.response?.analysis_id || assessment.analysis_id
      const saved = await appendReportFinding(testId, activeLens, rcaHypothesisFinding(hypothesis, assessment, analysisID, rootCause.response?.revision))
      if (activeLensRef.current === activeLens && testIDRef.current === testId) setAnnotations(saved)
      return `Added to the ${REPORT_LENS_LABELS[activeLens]} report's findings.`
    },
    annotate: async (hypothesis, assessment) => {
      const note = rcaHypothesisAnnotation(hypothesis, assessment)
      if (!note) throw new Error('This hypothesis has no onset to place on the timeline.')
      await createRunAnnotation(testId, note)
      setAnnotationReload(value => value + 1)
      return 'Added to the run timeline annotations.'
    },
    addNote: (hypothesis, assessment) => {
      setNoteDraft(rcaHypothesisNoteDraft(hypothesis, assessment, hypothesisLink(hypothesis.id)))
      setShowNotes(true)
    },
  } : undefined
  const hasBrowserEvidence = runHasBrowserArea(test, report)
  const hasArchive = Boolean(test.archive)
  // A Browser link keeps its tab (and so a focusable, selected tab) while
  // the report that decides it loads.
  const areas = (['Summary', 'Performance', 'Load Impact', 'Comparison', 'Diagnostics', 'Browser', 'Evidence', 'Execution'] as const).filter(name => (name !== 'Browser' || hasBrowserEvidence || (area === 'Browser' && !browserDecided)) && (name !== 'Load Impact' || hasArchive || area === 'Load Impact'))
  const facts = executionFacts(test, report, manifestModel.manifest)
  const failed = resolvedStatus === 'FAILED'
  return (
    <div className="page run-report-page">
      <div className="run-report-toolbar">
        <div><p className="run-report-kicker">Run results · {test.ref_key || test.id}</p><h2 className="run-report-title">{test.name || test.id}</h2><p><StatusBadge status={resolvedStatus} stoppedByOperator={report?.manifest?.termination_reason === 'operator_stopped'} /> · Configured {recordedNumber(facts.vusers)} VUsers for {facts.duration || 'Not recorded'} · Lifecycle elapsed {facts.lifecycleElapsed}{/* Stated once the archive has loaded; until then it is unknown, not unrecorded. */}{hasArchive && report && <> · Archived evidence elapsed {facts.elapsed}</>}</p></div>
        <div className="run-report-toolbar-actions">
          <button className="btn btn-secondary" type="button" disabled={!hasArchive} aria-expanded={area === 'Comparison'} onClick={() => { setShowCompare(false); setArea('Comparison') }}>Compare</button>
          {hasArchive && <RunReportLensSwitcher activeLens={activeLens} onLensChange={setLens} />}
          <button className="btn btn-primary" type="button" disabled={!hasArchive} aria-expanded={showExport} onClick={() => setShowExport(v => !v)}>Export report</button>
          {/* The desktop window's own address is not a link anyone else can open. */}
          {/^https?:$/.test(window.location.protocol) && <button className="btn btn-ghost" type="button" onClick={() => void copyReportLink()}>Copy link</button>}
          <button className="btn btn-ghost" type="button" onClick={() => setShowNotes(true)}>Run notes</button>
        </div>
      </div>
      {linkCopied && <p className="run-report-muted" role="status">{linkCopied}</p>}
      {(failed || resolvedStatus === 'ABORTED' || facts.reason) && <section className={`run-report-panel run-execution-outcome${failed ? ' run-execution-outcome-failed' : ''}`} aria-label="Execution outcome">
        <h2>{failed ? 'Execution failed' : resolvedStatus === 'ABORTED' ? 'Execution aborted' : 'Recorded execution outcome'}</h2>
        <p>{facts.reason || 'No termination detail was recorded.'}</p>
        <p className="run-report-muted">Execution status is separate from sampled transaction success and performance acceptance.</p>
      </section>}
      {!hasArchive && <section className="run-report-panel run-evidence-notice" aria-label="Evidence availability">
        <div><RunArchiveAvailabilitySummary failure={test.archive_failure} recovery={test.archive_recovery} /></div>
        <button type="button" className="btn btn-secondary" disabled={archiveCheckPending} onClick={() => void checkArchiveAgain()}>{archiveCheckPending ? 'Checking…' : 'Check again'}</button>
        {archiveCheckError && <p role="alert" className="run-report-error">{archiveCheckError}</p>}
      </section>}
      {hasArchive && <RunArchiveCoverageNotice report={report} />}
      {showExport && hasArchive && <section className="run-report-panel" aria-label="Report export options">
        <h2>Export report</h2><p>Exports use the {REPORT_LENS_LABELS[activeLens]} report audience selected above. Outputs cover the whole run, independent of the population selector below. Any release trend in an output uses the prior release run and names that reference.</p>
        <button className="btn btn-secondary" type="button" onClick={() => setShowComposer(true)}>Preview / edit commentary</button>
        <RunReportExportActions testId={testId} lens={activeLens} testName={test.name} downloadHTML={(id, lens) => api.downloadTestReport(id, lens, { comparisonId: initialComparisonId })} downloadFormat={(id, lens, format) => api.downloadTestReportFormat(id, lens, format, { comparisonId: initialComparisonId })} />
        <details><summary>Publish to Power BI</summary><RunReportPowerBIPublish testId={testId} publish={api.publishTestToPowerBI} getStatus={api.getPowerBIPublishStatus} /></details>
        <details><summary>Publish to Confluence</summary><RunReportConfluencePublish testId={testId} canPublish={canPublishConfluence} /></details>
      </section>}
      {/* The population drives the analysis sections only; Evidence lists sources, not metrics. */}
      {hasArchive && (area === 'Summary' || area === 'Performance' || area === 'Diagnostics') && <section className="run-report-context" aria-label="Analysis context">
        <label>Metric population <select value={population} onChange={e => choosePopulation(e.target.value as MetricPopulation)}>{Object.entries(populationLabels).map(([key,label]) => {
          // Empty populations would turn every KPI into "n/a": offer them with their count, disabled.
          const records = report?.metric_populations?.[key as MetricPopulation]?.transactions
          return <option key={key} value={key} disabled={records === 0 && key !== population}>{label}{records !== undefined ? ` (${records.toLocaleString()} records)` : ''}</option>
        })}</select></label>
        {report?.metric_populations?.[population]?.transactions === 0 && <p role="status"><strong>No records in this population.</strong> Choose another population to see results.</p>}
        {report && <p>{populationLabels[population]} · {report.metric_populations?.[population]?.transactions?.toLocaleString() ?? 'Unknown'} samples · Whole captured run. {(report?.metric_populations?.transaction?.transactions || 0) > 0 ? 'Interval graphs show named transaction metrics.' : 'Legacy interval graphs have no identified named transaction population; their values are separate from these KPIs.'} Resource curves show the load generators.</p>}
      </section>}
      {showCompare && hasArchive && <RunReportCompareBar compareId={effectiveCompareId} suggestedCompareId={suggestedCompareId} baselineName={baselineTest?.name} options={baselineOptions} optionsLoading={baselineOptionsLoading}
        deltas={baselineDiff} loading={baselineLoading} error={baselineError || baselineOptionsError || (effectiveCompareId && report && baselineReport && (!reportPopulationOverview(report, population) || !reportPopulationOverview(baselineReport, population)) ? 'No comparable samples for the selected population.' : '')}
        onCompareChange={setCompare} onPinBaseline={pinBaseline} onAnalyzeRootCause={analyzeAgainstBaseline && effectiveCompareId ? () => analyzeAgainstBaseline(effectiveCompareId) : undefined} />}
      {effectiveCompareId && !showCompare && <p className="run-report-muted">Comparing with {baselineTest?.name || effectiveCompareId}. Open Compare to change or clear the baseline.{baselineError ? ` ${baselineError}` : ''}</p>}
      {area !== 'Comparison' && effectiveCompareId && comparison && <section className="run-report-panel" role="note" aria-label="Comparison qualification">
        <h3>Comparison qualification</h3><p>{comparison.summary}</p>
        {comparison.missing.length > 0 && <p>Unverified metadata: {comparison.missing.join(', ')}.</p>}
        <p>{populationLabels[population]} records: {comparison.currentRecords ?? 'Unavailable'} versus {comparison.baselineRecords ?? 'Unavailable'}. Observed peak VUsers: {report?.overview?.peak_active_vusers ?? 'Unavailable'} versus {baselineReport?.overview?.peak_active_vusers ?? 'Unavailable'}.</p>
        <p>{comparison.revisions} These are descriptive deltas, not proof of a performance regression.</p>
        {(comparison.limitedSamples || comparison.differentObservedLoad || comparison.observedLoadUnknown) && <strong>Limited comparability: {[comparison.limitedSamples && 'insufficient sample evidence', comparison.differentObservedLoad && 'different observed load', comparison.observedLoadUnknown && 'observed load unavailable'].filter(Boolean).join('; ')}. Do not use the deltas alone as acceptance evidence.</strong>}
      </section>}
      <OrchestrationReturn testId={testId} />
      <MessagingProgressDiagnostics snapshot={report?.messaging_progress} />
      <MessagingDiagnostics series={Object.entries(report?.by_action || {}).map(([name, stats]) => ({ name, messaging: stats.messaging }))} />
      <RunAnnotations key={`annotations-${testId}`} testId={testId} startedAt={test.started_at} canEdit={canPublishConfluence} onAnnotationsChange={setTimelineAnnotations} reloadKey={annotationReload} />
      <TabList className="run-report-areas" role="tablist" aria-label="Result sections">{areas.map((name,index) => <button key={name} type="button" role="tab" id={`result-tab-${reportSectionForArea(name)}`} aria-controls={area === name ? `result-panel-${reportSectionForArea(name)}` : undefined} aria-selected={area === name} tabIndex={area === name ? 0 : -1}
        onClick={() => setArea(name)} onKeyDown={e => { const next = e.key === 'ArrowRight' ? (index+1)%areas.length : e.key === 'ArrowLeft' ? (index+areas.length-1)%areas.length : e.key === 'Home' ? 0 : e.key === 'End' ? areas.length-1 : -1; if(next >= 0){e.preventDefault();setArea(areas[next]);document.getElementById(`result-tab-${reportSectionForArea(areas[next])}`)?.focus()} }}>{name === 'Comparison' ? 'Baseline comparison' : name}</button>)}</TabList>
      <section role="tabpanel" id={`result-panel-${reportSectionForArea(area)}`} aria-labelledby={`result-tab-${reportSectionForArea(area)}`} className="run-report-area-content">
        {archiveModel.archiveLoading && <p role="status">Loading archived results…</p>}
        {archiveModel.archiveError && <section className="run-report-panel" aria-label="Report unavailable">
          <h2>Report could not be loaded</h2><p role="alert" className="run-report-error">{archiveModel.archiveError}</p>
          <p>Retry to load the archived findings. Execution details remain available.</p>
          <a className="btn btn-ghost" href={viewToLocationHash(scopeAppViewToWorkspace({ page: 'results' }, workspaceScopeForAppView(parseAppViewFromHash(window.location.hash))))}>Back to results</a>
          <button type="button" className="btn btn-secondary" onClick={archiveModel.retryArchiveReport} disabled={archiveModel.archiveLoading}>Retry report</button>
          <button type="button" className="btn btn-ghost" onClick={() => setArea('Execution')}>View execution details</button>
        </section>}
        {hasArchive && <div hidden={area !== 'Comparison'}>{(area === 'Comparison' || initialComparisonId) && <BaselineComparisonWorkspace key={`${test.id}/${initialCompare || ''}`} test={test} initialBaseline={initialCompare} comparisonId={initialComparisonId} canEditPolicy={canPublishConfluence} lens={activeLens} onSnapshot={id => onNavigateReport({ section: 'comparison', comparisonId: id, lens: activeLens })} onAnalyzeRootCause={analyzeAgainstBaseline} />}</div>}
        {area === 'Summary' && hasArchive && !archiveModel.archiveError && <>
          {report && <RunOutcomeSummary report={report} showPopulations={false} rootCauseSummary={liveRootCause} onRootCause={openRootCause} />}
          <ReleaseReadinessSummary report={report} findings={annotations?.findings} />
          {/* Values wait for the report: "—" would read as a measured gap. */}
          {report && <RunReportGroupBreakdown key={population} report={report} population={population} kpis={kpis} />}
          <section className="run-report-panel run-report-next-steps"><h2>Findings and next steps</h2>
            {/* Each finding is a line with its one action, styled alike. */}
            {report?.error_patterns?.length ? <p>{report.error_patterns.length === 1 ? 'One error pattern was' : `${report.error_patterns.length.toLocaleString()} error patterns were`} captured across all populations. <button type="button" className="btn btn-secondary btn-sm" onClick={() => setArea('Diagnostics')}>Investigate errors</button></p> : <p>{report ? 'No recurring error patterns were captured across all populations.' : 'Findings will appear when the report loads.'}</p>}
            {report?.completeness && report.completeness.score_pct < 100 && <p>Evidence coverage is {formatFixed(report.completeness.score_pct, 0)}%. <button type="button" className="btn btn-secondary btn-sm" onClick={() => setArea('Evidence')}>Review evidence gaps</button></p>}
          </section>
          {annotationsError && <p className="run-report-error" role="alert">{annotationsError}</p>}
          <RunReportVerdictBanner report={report} annotations={annotations} status={verdictDraft.status} explanation={verdictDraft.explanation} saving={savingAnnotations || annotationsLoading} disabled={!annotations} onStatusChange={updateVerdictStatus} onExplanationChange={updateVerdictExplanation} onSaveOverride={saveOverride} />
        </>}
        {area === 'Performance' && hasArchive && <>
          {/* KPI values wait for the report: "—" would read as a measured gap. */}
          {report && <RunReportKpiScorecard kpis={kpis} />}
          <SynchronizedTimeline startedAt={test.started_at} key={testId} runId={testId} baselineId={effectiveCompareId} data={timelineHistory} annotations={timelineAnnotations} overlays={timelineOverlays} onOverlaySelect={selectTimelineOverlay} goalConfig={goalConfig} loading={archiveSummariesLoading || archiveResourcesLoading || baselineSummaryLoading} error={archiveSummaryError || archiveResourceError} showBaseline={Boolean(effectiveCompareId && timelineHistory.some(point => point.baselineTps !== undefined))} />
          <ImportedDashboardPanels test={test} baselineTest={baselineReadyTest} />
          {(report?.transaction_response_time_sla_results?.length ?? 0) > 0 ? (
            <TransactionResponseTimeSLATable
              rows={report?.transaction_response_time_sla_results ?? []}
              title="Transaction SLA scoreboard"
              selectedGroup={population === 'transaction' ? archiveModel.selectedArchiveGroup : ''}
              selectedTransaction={population === 'transaction' ? archiveModel.selectedArchiveAction : ''}
              drilldownTransactions={population === 'transaction' ? archiveModel.archiveTransactions : []}
              loadingTransactions={archiveModel.archiveTransactionsLoading}
              onSelect={row => {
                setPopulationChoice('transaction')
                archiveModel.onSelectedArchiveGroupChange(row.group)
                archiveModel.onSelectedArchiveActionChange(row.transaction)
              }}
            />
          ) : (
            <TransactionSlaScoreboard rows={slaRows} selectedAction={population === slaPopulation && slaRows.some(row => row.name === archiveModel.selectedArchiveAction) ? archiveModel.selectedArchiveAction : ''} drilldownTransactions={population === slaPopulation ? archiveModel.archiveTransactions : []} loadingTransactions={archiveModel.archiveTransactionsLoading} onSelectAction={name => { setPopulationChoice(slaPopulation); archiveModel.onSelectedArchiveGroupChange(''); archiveModel.onSelectedArchiveActionChange(name) }} />
          )}
          <div data-report-focus="action-summary">{renderEvidence('action-summary')}</div>
          <JourneyRiskPanel testId={testId} />
          {test.project_id && <RunReportProjectPanels key={`${testId}-${test.project_id}`} testId={testId} projectId={test.project_id} history={timelineHistory} properties={Object.keys(report?.by_property || {}).sort()} canManage={canManageTests} />}
        </>}
        {area === 'Load Impact' && hasArchive && <div data-report-focus="load-impact">{renderEvidence('load-impact')}</div>}
        {area === 'Diagnostics' && hasArchive && <>
          <RootCausePanel testId={testId} model={rootCause} report={report} history={timelineHistory} historyLoading={archiveSummariesLoading || archiveResourcesLoading}
            canAnalyze={canPublishConfluence} baselineTestId={effectiveCompareId || undefined} baselineName={baselineTest?.name} route={initialRootCause || {}} onRouteChange={navigateRootCause}
            liveSummary={liveRootCause} onCopyLink={/^https?:$/.test(window.location.protocol) ? () => void copyReportLink() : undefined}
            settingsHref={canManageProject ? settingsHref('analysis') : undefined} monitorEvidenceHref={monitorEvidenceHref} monitorSourcesHref={monitorSourcesHref} hypothesisActions={hypothesisActions} />
          <BusinessKeyLookup testId={testId} projectId={test.project_id || 'default'} canLookup={canPublishConfluence} canApprove={canManageProject} settingsHref={canManageProject ? settingsHref('analysis') : undefined} />
          <RunReportDiagnostics report={report} loading={archiveModel.archiveAnalysisLoading} error={archiveModel.archiveAnalysisError} />
          {renderEvidence('report-error-patterns')}
          <div data-report-focus="transaction-drilldown">{renderEvidence('transactions')}</div>
          <details><summary>Browser evidence</summary>{hasBrowserEvidence && <p>Browser findings, the baseline comparison, interaction patterns and the resource drill-down are under <button type="button" className="btn btn-ghost btn-sm" onClick={() => setArea('Browser')}>Browser</button>.</p>}{renderEvidence('report-browser-summary')}{renderEvidence('report-browser-resources')}</details>
          <details><summary>Execution path analysis</summary>{renderEvidence('report-analysis')}</details>
        </>}
        {area === 'Browser' && hasArchive && !archiveModel.archiveError && <BrowserIntelligenceView testId={testId} projectId={test.project_id || 'default'} canEvaluate={canPublishConfluence}
          rootCause={rootCause} onOpenDiagnostics={() => setArea('Diagnostics')} hasArchive={hasArchive} test={test} report={report} />}
        {!hasArchive && area === 'Browser' && <p>Browser intelligence reads the run's archive. Check archive availability above.</p>}
        {area === 'Evidence' && hasArchive && <>
          {renderEvidence('report-completeness')}
          {report && <section className="run-outcome-summary"><h2>Metric populations</h2><RunMetricPopulations report={report} /></section>}
          {renderEvidence('report-metadata')}
          <details><summary>Raw evidence downloads</summary><button className="btn btn-secondary" onClick={() => void archiveModel.onDownloadArchive()}>Download evidence bundle</button>{renderEvidence('report-assets')}</details>
          <details data-report-focus="monitor-evidence"><summary>Archived intervals and monitor evidence</summary>{renderEvidence('report-summary-timeline')}</details>
        </>}
        {area === 'Execution' && <>
          <RunExecutionDetails manifestModel={manifestModel} test={test} report={report} history={timelineHistory} historyLoading={archiveSummariesLoading} historyError={archiveSummaryError} />
          {test.data_pools?.some(binding => binding.access_mode === 'live') && <LiveTablesPanel key={`live-${testId}`} testId={testId} projectId={test.project_id} />}
        </>}
        {!hasArchive && area === 'Summary' && <>
          <RunExecutionFacts test={test} report={null} manifest={manifestModel.manifest} />
          <section className="run-report-panel"><h2>Investigate this run</h2>
            <p>Performance acceptance is not assessed here. Review the recorded reason, lifecycle events, and available evidence before deciding whether to rerun.</p>
            <div className="run-report-toolbar-actions"><button type="button" className="btn btn-primary" onClick={() => setArea('Execution')}>Review execution events</button><button type="button" className="btn btn-secondary" onClick={() => setArea('Evidence')}>View retained evidence</button></div>
          </section>
        </>}
        {!hasArchive && area === 'Comparison' && <p>Baseline comparison requires saved transaction and resource archives. Check archive availability above.</p>}
        {!hasArchive && area === 'Performance' && <RetainedRunMetrics test={test} />}
        {!hasArchive && area === 'Load Impact' && <p>Load impact reads the run's archived intervals. Check archive availability above.</p>}
        {!hasArchive && area === 'Diagnostics' && <><RetainedRunDiagnostics test={test} /><RunExecutionDetails manifestModel={manifestModel} test={test} report={null} evidenceOnly /></>}
        {!hasArchive && area === 'Evidence' && <RunExecutionDetails manifestModel={manifestModel} test={test} report={null} evidenceOnly />}
      </section>
      {showNotes && <TestNotesDialog testId={testId} testName={test.name} initialDraft={noteDraft || undefined} onClose={() => { setShowNotes(false); setNoteDraft('') }} />}
      {showComposer && <ReportComposer comparisonId={initialComparisonId} testId={testId} testName={test.name} profile={activeLens} onClose={() => setShowComposer(false)} />}
    </div>
  )
}
