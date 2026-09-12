import BulkActions from './ux/BulkActions'
import { lifecycleElapsedSeconds, formatElapsedSeconds } from './runReport/executionPresentation'
import RowActions from './ux/RowActions'
import TabList from './ux/TabList'
import { confirmAction } from './ux/confirmAction'
import { handleTabKey } from './tabKeyboard'
import ResultHeadline from './ResultHeadline'
import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { downloadBlob } from '../utils/fileUtils'
import { isCompletedTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { Application, BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserPlatformLiveMetrics, ExternalResultImport, IntervalSummaryRecord, Release, RunArchiveActionStats, RunArchiveReport, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import BrowserBreakdownChart from './BrowserBreakdownChart'
import DesktopApiResourceLink from './DesktopApiResourceLink'
import { SortHeader } from './SortHeader'
import SelectAllCheckbox from './SelectAllCheckbox'
import {
  BrowserBreakdown,
  BrowserBreakdownDatum,
  buildBrowserBreakdownData,
  buildComparisonBrowserBreakdownSeries,
  formatBrowserEngineLabel,
  formatDevicePlatformLabel,
  getBrowserBreakdownChartCopy,
  getBrowserBreakdownSegmentLabel,
  getBrowserBreakdownTitle,
} from './browserBreakdownModel'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import {
  COMPARISON_CHART_METRICS,
  buildComparisonSummarySeries,
  comparisonChartMetricValue,
  comparisonChartYAxisLabel,
  comparisonRunColor,
  comparisonSummaryDataKey,
  formatComparisonChartValue,
  getComparisonChartMetricConfig,
  type ComparisonChartMetric,
} from './resultsComparisonChartModel'
import StatusBadge from './StatusBadge'
import LibraryFederationControls from './LibraryFederationControls'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import { libraryStatusTone, shortLibraryHash } from './libraryFederationUtils'
import { useLibraryFederation, type LibraryAssetSourceKind } from './useLibraryFederation'
import { useSharedTestList } from '../hooks/useSharedTestList'
import type { NavPage } from './Sidebar'
import type { LibraryCatalogItem } from '../types/library'
import { requestSettingsFocus } from './settingsSectionLayout'
import { loadAllExternalResultImports } from './externalResultImportPagination'
import { loadResultTestTail } from './resultTestPagination'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { ARCHIVE_CHART_MAX_SAMPLES } from './archiveHistorySampling'
import { boundedAllSettled } from '../utils/boundedAllSettled'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onOpenTestArchive?: (id: string) => void
  onOpenReport?: (id: string) => void
  onNavigate?: (page: NavPage) => void
  activeApplication?: Application | null
  activeRelease?: Release | null
  activeProjectName?: string
}

type SortField = 'source' | 'name' | 'status' | 'total_vusers' | 'groups' | 'started' | 'duration' | 'archive'
type StatusFilter = 'all' | 'complete' | 'failed' | 'aborted'
type ArchiveFilter = 'all' | 'ready' | 'pending'
type ComparisonBreakdown = BrowserBreakdown
const MAX_COMPARISON_RUNS = 10
const ALL_COMPARISON_ACTIONS = 'all'
const EMPTY_EXTERNAL_IMPORTS: ExternalResultImport[] = []

type ResultSourceRow = {
  rowKey: string
  source: LibraryAssetSourceKind
  syncStatus: string
  name: string
  status: string
  startedAt?: string
  duration?: string
  durationSeconds?: number
  applicationId?: string
  releaseId?: string
  releaseName?: string
  hasArchive: boolean
  archiveCount?: number
  totalVusers?: number
  groupCount?: number
  test?: TestSummary
  externalImport?: ExternalResultImport
  item?: LibraryCatalogItem
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Never'
}

function runStartedAt(test: TestSummary) {
  return new Date(test.start_time || test.created_at || 0).getTime()
}

function sortTestsByStartAscending(tests: TestSummary[]) {
  return [...tests].sort((left, right) => {
    const startedDelta = runStartedAt(left) - runStartedAt(right)
    if (startedDelta !== 0) return startedDelta
    return left.id.localeCompare(right.id)
  })
}

function rowStartedAt(row: ResultSourceRow) {
  return new Date(row.startedAt || 0).getTime()
}

function resultSourceStatusTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'complete':
    case 'completed':
    case 'success':
    case 'succeeded':
      return 'healthy'
    case 'failed':
    case 'aborted':
      return 'danger'
    default:
      return libraryStatusTone(status)
  }
}

function catalogItemForTest(test: TestSummary): LibraryCatalogItem {
  return {
    kind: 'result',
    id: test.id,
    name: test.name,
    project_id: test.project_id,
    application_id: test.application_id,
    release_id: test.release_id,
    status: test.status,
    execution_run_date: test.start_time || test.started_at || test.created_at,
    updated_at: test.ended_at || test.created_at,
    has_archive: Boolean(test.archive),
  }
}

function resultReleaseValue(row: ResultSourceRow, localReleaseByRemoteId?: Map<string, string>) {
  if (row.source === 'Remote' && row.releaseId) {
    return localReleaseByRemoteId?.get(row.releaseId) || '__unmapped__'
  }
  if (row.releaseId) return row.releaseId
  if (row.releaseName && row.applicationId) {
    return `application:${row.applicationId}:release-name:${normalizedReleaseKey(row.releaseName)}`
  }
  return row.releaseName ? `release-name:${normalizedReleaseKey(row.releaseName)}` : '__unassigned__'
}

function resultReleaseLabel(row: ResultSourceRow) {
  return row.releaseName || row.releaseId || 'Unassigned'
}

function normalizedReleaseKey(value?: string) {
  return (value || '').trim().toLowerCase()
}

function rowMatchesActiveApplication(row: ResultSourceRow, activeRelease?: Release | null, activeApplication?: Application | null, mappedRemoteApplicationId?: string) {
  const activeApplicationId = activeRelease?.application_id || activeApplication?.id
  // Local results came from the already application-scoped list API (and are
  // release-scoped there while the active release filter is selected);
  // older local summaries may omit application_id without becoming ambiguous.
  if (row.source === 'Local') {
    if (row.externalImport) {
      return Boolean(activeApplicationId && row.applicationId === activeApplicationId)
    }
    return !row.applicationId || Boolean(activeApplicationId && row.applicationId === activeApplicationId)
  }
  // Remote UUIDs live in a separate namespace. Only an administrator-saved
  // mapping may correlate them; matching by mutable/non-unique display names
  // would admit results from the wrong application.
  return Boolean(mappedRemoteApplicationId && row.applicationId === mappedRemoteApplicationId)
}

function rowMatchesReleaseFilter(
  row: ResultSourceRow,
  releaseFilter: string,
  activeRelease?: Release | null,
  activeApplication?: Application | null,
  mappedRemoteApplicationId?: string,
  mappedRemoteReleaseId?: string,
  localReleaseByRemoteId?: Map<string, string>,
) {
  if (releaseFilter === 'all') return true
  if (releaseFilter === activeRelease?.id) {
    const releaseMatches = row.source === 'Remote'
      ? Boolean(mappedRemoteReleaseId && row.releaseId === mappedRemoteReleaseId)
      : row.releaseId === activeRelease.id
    return releaseMatches && rowMatchesActiveApplication(row, activeRelease, activeApplication, mappedRemoteApplicationId)
  }
  return resultReleaseValue(row, localReleaseByRemoteId) === releaseFilter
}

function externalResultStatus(result: ExternalResultImport) {
  const status = result.summary?.status
  return typeof status === 'string' && status.trim() ? status : 'imported'
}

function externalResultNumber(result: ExternalResultImport, key: string) {
  const value = result.summary?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function formatBrowserMetric(value?: number, suffix = '', digits = 1) {
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(digits)}${suffix}` : 'n/a'
}

function hasBrowserOverview(report?: RunArchiveReport) {
  return Boolean(report?.overview.browser)
}

function formatBrowserScore(value?: number) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(3) : 'n/a'
}

function getBrowserDeviceEntries(report?: RunArchiveReport): Array<[string, BrowserDeviceLiveMetrics]> {
  return Object.entries(report?.browser_by_device ?? {}).sort(([left], [right]) => left.localeCompare(right))
}

function getBrowserPlatformEntries(report?: RunArchiveReport): BrowserPlatformLiveMetrics[] {
  const archivedEntries = Object.values(report?.browser_by_platform ?? {})
  if (archivedEntries.length > 0) {
    return archivedEntries.sort((left, right) => formatDevicePlatformLabel(left.device_platform).localeCompare(formatDevicePlatformLabel(right.device_platform)))
  }

  const entries = Object.values(report?.browser_by_device ?? {})
  if (entries.length === 0) return []

  const grouped = new Map<string, {
    platform: string
    engine?: string
    samples: number
    lcpTotal: number
    clsTotal: number
    inpTotal: number
  }>()

  for (const metrics of entries) {
    if (!metrics) continue
    const platform = metrics.device_platform || 'unknown'
    const current = grouped.get(platform) ?? {
      platform,
      engine: metrics.browser_engine,
      samples: 0,
      lcpTotal: 0,
      clsTotal: 0,
      inpTotal: 0,
    }
    const samples = metrics.samples || 0
    current.samples += samples
    current.lcpTotal += (metrics.avg_lcp_ms ?? 0) * samples
    current.clsTotal += (metrics.avg_cls_score ?? 0) * samples
    current.inpTotal += (metrics.avg_inp_ms ?? 0) * samples
    if (current.engine && metrics.browser_engine && current.engine !== metrics.browser_engine) {
      current.engine = 'multiple'
    } else if (!current.engine) {
      current.engine = metrics.browser_engine
    }
    grouped.set(platform, current)
  }

  return Array.from(grouped.values())
    .map(entry => ({
      device_platform: entry.platform,
      browser_engine: entry.engine,
      samples: entry.samples,
      avg_lcp_ms: entry.samples > 0 ? entry.lcpTotal / entry.samples : undefined,
      avg_cls_score: entry.samples > 0 ? entry.clsTotal / entry.samples : undefined,
      avg_inp_ms: entry.samples > 0 ? entry.inpTotal / entry.samples : undefined,
    }))
    .sort((left, right) => formatDevicePlatformLabel(left.device_platform).localeCompare(formatDevicePlatformLabel(right.device_platform)))
}

function getBrowserEngineEntries(report?: RunArchiveReport): BrowserEngineLiveMetrics[] {
  const archivedEntries = Object.values(report?.browser_by_engine ?? {})
  if (archivedEntries.length > 0) {
    return archivedEntries.sort((left, right) => formatBrowserEngineLabel(left.browser_engine).localeCompare(formatBrowserEngineLabel(right.browser_engine)))
  }

  const entries = Object.values(report?.browser_by_device ?? {})
  if (entries.length === 0) return []

  const grouped = new Map<string, {
    browserEngine: string
    samples: number
    lcpTotal: number
    clsTotal: number
    inpTotal: number
  }>()

  for (const metrics of entries) {
    if (!metrics) continue
    const browserEngine = metrics.browser_engine || 'browser'
    const current = grouped.get(browserEngine) ?? {
      browserEngine,
      samples: 0,
      lcpTotal: 0,
      clsTotal: 0,
      inpTotal: 0,
    }
    const samples = metrics.samples || 0
    current.samples += samples
    current.lcpTotal += (metrics.avg_lcp_ms ?? 0) * samples
    current.clsTotal += (metrics.avg_cls_score ?? 0) * samples
    current.inpTotal += (metrics.avg_inp_ms ?? 0) * samples
    grouped.set(browserEngine, current)
  }

  return Array.from(grouped.values())
    .map(entry => ({
      browser_engine: entry.browserEngine,
      samples: entry.samples,
      avg_lcp_ms: entry.samples > 0 ? entry.lcpTotal / entry.samples : undefined,
      avg_cls_score: entry.samples > 0 ? entry.clsTotal / entry.samples : undefined,
      avg_inp_ms: entry.samples > 0 ? entry.inpTotal / entry.samples : undefined,
    }))
    .sort((left, right) => formatBrowserEngineLabel(left.browser_engine).localeCompare(formatBrowserEngineLabel(right.browser_engine)))
}

function resolveStatus(test: TestSummary, testStatuses: Record<string, string>) {
  return resolveTestStatus(test, testStatuses)
}

function isCompletedStatus(status?: string) {
  return isCompletedTestStatus(status)
}

function formatShortRunId(id: string) {
  return id.length > 12 ? id.slice(0, 8) : id
}

function formatComparisonRunLabel(test: TestSummary) {
  const name = test.name || 'Unnamed run'
  return `${name} (${test.ref_key || formatShortRunId(test.id)})`
}

function formatBaselinePin(value?: string) {
  return value ? new Date(value).toLocaleString() : 'Pinned baseline'
}

function comparisonFilenameToken(value: string) {
  return value.trim().replace(/[^A-Za-z0-9_-]+/g, '_').replace(/^_+|_+$/g, '') || 'transaction'
}


function peakComparisonSummaryMetric(summaries: IntervalSummaryRecord[], metric: ComparisonChartMetric) {
  const values = summaries
    .map(summary => comparisonChartMetricValue(summary, metric))
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
  if (values.length === 0) return undefined
  return Math.max(...values)
}

function comparisonTableMetricLabel(metric: ComparisonChartMetric) {
  const config = getComparisonChartMetricConfig(metric)
  if (metric === 'art') return 'Avg ART'
  if (metric === 'tps') return 'Peak TPS'
  if (metric === 'errors') return 'Error Rate'
  return config.label
}

function comparisonTableMetricValue(stats: RunArchiveActionStats | undefined, summaries: IntervalSummaryRecord[], metric: ComparisonChartMetric, actionFiltered: boolean) {
  if (!stats || stats.transactions === 0) return undefined
  switch (metric) {
    case 'art':
      return finiteComparisonValue(stats?.avg_response_ms)
    case 'p50':
      return finiteComparisonValue(stats?.p50_ms) ?? (actionFiltered ? peakComparisonSummaryMetric(summaries, metric) : undefined)
    case 'p90':
      return finiteComparisonValue(stats?.p90_ms) ?? (actionFiltered ? peakComparisonSummaryMetric(summaries, metric) : undefined)
    case 'p95':
      return finiteComparisonValue(stats?.p95_ms) ?? (actionFiltered ? peakComparisonSummaryMetric(summaries, metric) : undefined)
    case 'p99':
      return finiteComparisonValue(stats?.p99_ms) ?? (actionFiltered ? peakComparisonSummaryMetric(summaries, metric) : undefined)
    case 'errors':
      return finiteComparisonValue(stats?.error_pct)
    case 'tps':
      return finiteComparisonValue(stats?.peak_tps) ?? (actionFiltered ? peakComparisonSummaryMetric(summaries, metric) : undefined)
    default:
      return undefined
  }
}

function finiteComparisonValue(value: number | undefined) {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function formatComparisonTableMetricValue(value: number | undefined, metric: ComparisonChartMetric) {
  if (value === undefined) return '-'
  return formatComparisonChartValue(value, getComparisonChartMetricConfig(metric).valueFormat)
}

function formatComparisonTableDelta(delta: number | undefined, metric: ComparisonChartMetric) {
  if (delta === undefined) return '-'
  const formatted = formatComparisonChartValue(Math.abs(delta), getComparisonChartMetricConfig(metric).valueFormat)
  return `${delta >= 0 ? '+' : '-'}${formatted}`
}

function comparisonTableDeltaClass(delta: number | undefined, metric: ComparisonChartMetric) {
  if (delta === undefined || delta === 0) return 'cell-muted'
  if (metric === 'tps') return delta > 0 ? 'text-success' : 'text-danger'
  return delta > 0 ? 'text-danger' : 'text-success'
}

function ResultsPage({ testStatuses, onSelectTest, onOpenTestArchive, onOpenReport, onNavigate, activeApplication, activeRelease, activeProjectName }: Props) {
  const api = useApi()
  const [releaseFilter, setReleaseFilter] = useState(() => activeRelease?.id || 'all')
  const testListParams = useMemo(() => activeApplication?.id ? {
    project_id: activeApplication.project_id,
    application_id: activeApplication.id,
    ...(activeRelease?.id && releaseFilter === activeRelease.id ? { release_id: activeRelease.id } : {}),
    summary: true,
    limit: 1000,
    offset: 0,
  } : null, [activeApplication?.id, activeApplication?.project_id, activeRelease?.id, releaseFilter])
  const listResultPage = useCallback(
    (params: Parameters<typeof api.listTests>[0]) => api.listTests(params),
    [api],
  )
  const {
    tests: firstResultPage,
    total: resultListTotal,
    error: listError,
    loading: resultsLoading,
    refreshing: resultsRefreshing,
    updatedAt: resultListUpdatedAt,
    refresh,
  } = useSharedTestList(testListParams, { loader: listResultPage, pollIntervalMs: 10000 })
  const resultCatalogScopeKey = useMemo(() => JSON.stringify(testListParams || {}), [testListParams])
  const resultCatalogScopeKeyRef = useRef(resultCatalogScopeKey)
  resultCatalogScopeKeyRef.current = resultCatalogScopeKey
  const [resultCatalog, setResultCatalog] = useState<{
    scopeKey: string
    tests: TestSummary[]
    total: number
  }>({ scopeKey: '', tests: [], total: 0 })
  const resultCatalogRef = useRef(resultCatalog)
  const resultTailGenerationRef = useRef(0)
  const resultTailScopeRef = useRef('')
  const [resultTailLoading, setResultTailLoading] = useState(false)
  const [resultTailError, setResultTailError] = useState('')
  const commitResultCatalog = useCallback((next: typeof resultCatalog) => {
    resultCatalogRef.current = next
    setResultCatalog(next)
  }, [])
  const invalidateResultTailAfterDelete = useCallback((deletedIDs: string[]) => {
    if (deletedIDs.length === 0) return
    resultTailGenerationRef.current += 1
    resultTailScopeRef.current = ''
    setResultTailLoading(false)
    setResultTailError('')

    // The delete response is authoritative. Remove committed rows from the
    // local catalog immediately and fence every older tail page so a response
    // that was already in flight cannot merge a deleted result back in.
    const deleted = new Set(deletedIDs)
    const current = resultCatalogRef.current
    const remaining = current.tests.filter(test => !deleted.has(test.id))
    commitResultCatalog({
      ...current,
      tests: remaining,
      total: Math.max(remaining.length, current.total - (current.tests.length - remaining.length)),
    })
  }, [commitResultCatalog])

  useEffect(() => {
    resultTailGenerationRef.current += 1
    resultTailScopeRef.current = ''
    commitResultCatalog({ scopeKey: resultCatalogScopeKey, tests: [], total: 0 })
    setResultTailLoading(false)
    setResultTailError('')
  }, [commitResultCatalog, resultCatalogScopeKey])

  useEffect(() => {
    if (!testListParams || resultListUpdatedAt === null) return
    const current = resultCatalogRef.current
    const shouldReset = current.scopeKey !== resultCatalogScopeKey || resultListTotal < current.total
    const testsByID = new Map<string, TestSummary>()
    if (!shouldReset) current.tests.forEach(test => testsByID.set(test.id, test))
    firstResultPage.forEach(test => testsByID.set(test.id, test))
    if (testsByID.size > resultListTotal) {
      testsByID.clear()
      firstResultPage.forEach(test => testsByID.set(test.id, test))
    }
    const next = {
      scopeKey: resultCatalogScopeKey,
      tests: Array.from(testsByID.values()),
      total: resultListTotal,
    }
    commitResultCatalog(next)
    if (next.tests.length >= resultListTotal || resultTailScopeRef.current === resultCatalogScopeKey) {
      if (next.tests.length >= resultListTotal) {
        setResultTailLoading(false)
        setResultTailError('')
      }
      return
    }

    const requestGeneration = ++resultTailGenerationRef.current
    resultTailScopeRef.current = resultCatalogScopeKey
    setResultTailLoading(true)
    setResultTailError('')
    void loadResultTestTail(listResultPage, testListParams, {
      startOffset: firstResultPage.length,
      total: resultListTotal,
      onPage: (pageTests, nextTotal) => {
        if (
          requestGeneration !== resultTailGenerationRef.current
          || resultTailScopeRef.current !== resultCatalogScopeKey
        ) return
        const latest = resultCatalogRef.current
        const merged = new Map(latest.tests.map(test => [test.id, test]))
        pageTests.forEach(test => {
          if (!merged.has(test.id)) merged.set(test.id, test)
        })
        commitResultCatalog({
          scopeKey: resultCatalogScopeKey,
          tests: Array.from(merged.values()),
          total: nextTotal,
        })
      },
    }).catch(error => {
      if (
        requestGeneration === resultTailGenerationRef.current
        && resultTailScopeRef.current === resultCatalogScopeKey
      ) setResultTailError(getErrorMessage(error, 'Unable to load older results'))
    }).finally(() => {
      if (
        requestGeneration === resultTailGenerationRef.current
        && resultTailScopeRef.current === resultCatalogScopeKey
      ) {
        resultTailScopeRef.current = ''
        setResultTailLoading(false)
      }
    })
  }, [commitResultCatalog, firstResultPage, listResultPage, resultCatalogScopeKey, resultListTotal, resultListUpdatedAt, testListParams])

  const tests = resultCatalog.scopeKey === resultCatalogScopeKey ? resultCatalog.tests : firstResultPage
  const resultsTruncated = tests.length < resultListTotal
  const externalImportsScopeKey = `${activeApplication?.project_id || activeRelease?.project_id || ''}\u0000${activeApplication?.id || ''}\u0000${releaseFilter}`
  const externalImportsScopeKeyRef = useRef(externalImportsScopeKey)
  externalImportsScopeKeyRef.current = externalImportsScopeKey
  const externalImportsGenerationRef = useRef(0)
  const externalImportsMountedRef = useRef(true)
  const [externalImportsState, setExternalImportsState] = useState<{
    scopeKey: string
    imports: ExternalResultImport[]
    error: string
  }>({ scopeKey: '', imports: [], error: '' })
  const [externalImportsLoadingScopeKey, setExternalImportsLoadingScopeKey] = useState('')
  const externalImports = externalImportsState.scopeKey === externalImportsScopeKey ? externalImportsState.imports : EMPTY_EXTERNAL_IMPORTS
  const externalImportsError = externalImportsState.scopeKey === externalImportsScopeKey ? externalImportsState.error : ''
  const externalImportsLoading = externalImportsLoadingScopeKey === externalImportsScopeKey
  const refreshExternalImports = useCallback(async () => {
    const requestScopeKey = externalImportsScopeKey
    const requestGeneration = ++externalImportsGenerationRef.current
    if (!activeApplication?.id || !activeRelease?.id) {
      setExternalImportsState({ scopeKey: requestScopeKey, imports: [], error: '' })
      setExternalImportsLoadingScopeKey('')
      return
    }
    setExternalImportsLoadingScopeKey(requestScopeKey)
    const requestIsCurrent = () => externalImportsMountedRef.current
      && requestGeneration === externalImportsGenerationRef.current
      && externalImportsScopeKeyRef.current === requestScopeKey
    try {
      const imports = await loadAllExternalResultImports(params => api.listExternalResultImports(params), {
        project_id: activeApplication.project_id || activeRelease.project_id,
        application_id: activeApplication.id,
        ...(releaseFilter === activeRelease.id ? { release_id: activeRelease.id } : {}),
      }, {
        isCurrent: requestIsCurrent,
      })
      if (imports === null || !requestIsCurrent()) return
      setExternalImportsState({ scopeKey: requestScopeKey, imports, error: '' })
    } catch (error) {
      if (!requestIsCurrent()) return
      setExternalImportsState({ scopeKey: requestScopeKey, imports: [], error: getErrorMessage(error, 'Unable to load imported results') })
    } finally {
      if (requestIsCurrent()) {
        setExternalImportsLoadingScopeKey(current => current === requestScopeKey ? '' : current)
      }
    }
  }, [activeApplication?.id, activeApplication?.project_id, activeRelease?.id, activeRelease?.project_id, api, externalImportsScopeKey, releaseFilter])

  useEffect(() => {
    externalImportsMountedRef.current = true
    return () => {
      externalImportsMountedRef.current = false
      externalImportsGenerationRef.current += 1
    }
  }, [])

  useEffect(() => {
    void refreshExternalImports()
  }, [refreshExternalImports])

  const refreshLocalResults = useCallback(async () => {
    await Promise.all([refresh(), refreshExternalImports()])
  }, [refresh, refreshExternalImports])
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [deletedResultIds, setDeletedResultIds] = useState<Set<string>>(new Set())
  const [deletingResultIds, setDeletingResultIds] = useState<Set<string>>(new Set())
  const deletingResultIdsRef = useRef<Set<string>>(new Set())
  const [comparisonTestIds, setComparisonTestIds] = useState<string[]>([])
  const [comparisonTestSnapshots, setComparisonTestSnapshots] = useState<TestSummary[]>([])
  const [comparisonReports, setComparisonReports] = useState<Record<string, RunArchiveReport>>({})
  const [comparisonSummaries, setComparisonSummaries] = useState<Record<string, IntervalSummaryRecord[]>>({})
  const [comparisonLoading, setComparisonLoading] = useState(false)
  const [comparisonError, setComparisonError] = useState('')
  const [comparisonBreakdown, setComparisonBreakdown] = useState<ComparisonBreakdown>('device')
  const [comparisonChartMetric, setComparisonChartMetric] = useState<ComparisonChartMetric>('tps')
  const [comparisonActionFilter, setComparisonActionFilter] = useState(ALL_COMPARISON_ACTIONS)
  const [operationError, setError] = useState('')
  const [sortField, setSortField] = useState<SortField>('started')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [search, setSearch] = useState('')
  const [fromDate, setFromDate] = useState('')
  const [toDate, setToDate] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [archiveFilter, setArchiveFilter] = useState<ArchiveFilter>('all')
  const error = operationError || listError || resultTailError || externalImportsError
  // Older local pages stream in behind the first page. Keep the usable first
  // page visible while that background work continues.
  const resultsSourcesLoading = resultsLoading || externalImportsLoading
  const openRemoteSettings = useCallback(() => {
    requestSettingsFocus('integrations')
    onNavigate?.('settings')
  }, [onNavigate])

  const libraryProjectId = activeRelease?.project_id || activeApplication?.project_id
  const federation = useLibraryFederation({
    kind: 'result',
    projectId: libraryProjectId,
    applicationId: activeApplication?.id,
    releaseId: activeRelease?.id,
    onRefreshLocal: refreshLocalResults,
  })
  const {
    sourceFilter,
    localItems,
    remoteItems,
    actionBlockedReason,
    transferBlockReasonForItem,
    pullRemoteItem,
    pushLocalItem,
    compareItems,
    hostItemForLocal,
    syncStatusForLocal,
    remoteItemHref,
    selectedRemote,
  } = federation
  const mappedRemoteApplicationId = activeApplication?.id
    ? selectedRemote?.application_mappings?.[activeApplication.id]
    : undefined
  const mappedRemoteReleaseId = activeRelease?.id
    ? selectedRemote?.release_mappings?.[activeRelease.id]
    : undefined
  const localReleaseByRemoteId = useMemo(() => {
    const mapped = new Map<string, string>()
    const ambiguous = new Set<string>()
    Object.entries(selectedRemote?.release_mappings || {}).forEach(([localReleaseId, remoteReleaseId]) => {
      const localID = localReleaseId.trim()
      const remoteID = remoteReleaseId.trim()
      if (!localID || !remoteID || ambiguous.has(remoteID)) return
      const existing = mapped.get(remoteID)
      if (existing && existing !== localID) {
        mapped.delete(remoteID)
        ambiguous.add(remoteID)
        return
      }
      mapped.set(remoteID, localID)
    })
    return mapped
  }, [selectedRemote?.release_mappings])

  useEffect(() => {
    setSelectedIds(new Set())
    setDeletedResultIds(new Set())
    setDeletingResultIds(new Set())
    setComparisonTestIds([])
    setComparisonTestSnapshots([])
    setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    setReleaseFilter(activeRelease?.id || 'all')
  }, [activeApplication?.id, activeRelease?.id])

  const completed = useMemo(
    () =>
      tests.filter(test => {
        if (deletedResultIds.has(test.id)) return false
        const status = resolveStatus(test, testStatuses)
        return isCompletedStatus(status)
      }),
    [deletedResultIds, tests, testStatuses],
  )

  const localCatalogById = useMemo(() => {
    const map = new Map<string, LibraryCatalogItem>()
    localItems.forEach(item => {
      if (item.kind === 'result') map.set(item.id, item)
    })
    return map
  }, [localItems])

  const sourceRows = useMemo<ResultSourceRow[]>(() => {
    const rows: ResultSourceRow[] = []

    if (sourceFilter !== 'host') {
      completed.forEach(test => {
        const item = localCatalogById.get(test.id) ?? catalogItemForTest(test)
        rows.push({
          rowKey: `local:${test.id}`,
          source: 'Local',
          syncStatus: syncStatusForLocal(test.id),
          name: test.name,
          status: resolveStatus(test, testStatuses),
          startedAt: test.start_time || test.started_at || test.created_at,
          duration: formatElapsedSeconds(lifecycleElapsedSeconds(test)),
          durationSeconds: lifecycleElapsedSeconds(test),
          applicationId: item.application_id || test.application_id,
          releaseId: test.release_id || item.release_id,
          releaseName: item.release_name || (test.release_id === activeRelease?.id ? activeRelease?.name : undefined),
          hasArchive: Boolean(test.archive),
          archiveCount: test.archive?.transaction_count,
          totalVusers: test.total_vusers,
          groupCount: test.groups?.length || 1,
          test,
          item,
        })
      })
      externalImports.forEach(imported => {
        const item: LibraryCatalogItem = {
          kind: 'result',
          id: imported.id,
          name: imported.name,
          project_id: imported.project_id,
          application_id: imported.application_id,
          release_id: imported.release_id,
          status: externalResultStatus(imported),
          content_hash: imported.content_hash,
          execution_run_date: imported.created_at,
          updated_at: imported.created_at,
          has_archive: imported.has_archive,
        }
        rows.push({
          rowKey: `external:${imported.id}`,
          source: 'Local',
          syncStatus: syncStatusForLocal(imported.id),
          name: imported.name,
          status: externalResultStatus(imported),
          startedAt: imported.created_at,
          applicationId: imported.application_id,
          releaseId: imported.release_id,
          releaseName: imported.release_id === activeRelease?.id ? activeRelease?.name : undefined,
          hasArchive: Boolean(imported.has_archive),
          archiveCount: externalResultNumber(imported, 'transactions'),
          totalVusers: externalResultNumber(imported, 'total_vusers'),
          externalImport: imported,
          item,
        })
      })
    }

    if (sourceFilter !== 'local') {
      remoteItems
        .filter(item => item.kind === 'result')
        .forEach(item => {
          rows.push({
            rowKey: `remote:${item.remote_item_id || item.id}`,
            source: 'Remote',
            syncStatus: item.sync_status || 'host only',
            name: item.name || item.id,
            status: item.status || 'Unknown',
            startedAt: item.execution_run_date || item.updated_at,
            applicationId: item.application_id,
            releaseId: item.release_id,
            releaseName: item.release_name,
            hasArchive: Boolean(item.has_archive),
            item,
          })
        })
    }

    return rows
  }, [activeRelease?.id, activeRelease?.name, completed, externalImports, localCatalogById, remoteItems, sourceFilter, syncStatusForLocal, testStatuses])

  const applicationQualifiedRows = useMemo(
    () => sourceRows.filter(row => (
      rowMatchesActiveApplication(row, activeRelease, activeApplication, mappedRemoteApplicationId)
      && (row.source === 'Local' || Boolean(row.releaseId && localReleaseByRemoteId.has(row.releaseId)))
    )),
    [activeApplication, activeRelease, localReleaseByRemoteId, mappedRemoteApplicationId, sourceRows],
  )

  const releaseOptions = useMemo(() => {
    const options = new Map<string, string>()
    applicationQualifiedRows.forEach(row => {
      options.set(resultReleaseValue(row, localReleaseByRemoteId), resultReleaseLabel(row))
    })
    if (activeRelease?.id) {
      options.set(activeRelease.id, activeRelease.name)
    }
    return Array.from(options.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((left, right) => left.label.localeCompare(right.label))
  }, [activeRelease?.id, activeRelease?.name, applicationQualifiedRows, localReleaseByRemoteId])

  const filteredRows = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase()
    return applicationQualifiedRows.filter(row => {
      if (!rowMatchesReleaseFilter(row, releaseFilter, activeRelease, activeApplication, mappedRemoteApplicationId, mappedRemoteReleaseId, localReleaseByRemoteId)) return false
      if (statusFilter !== 'all' && row.status.toLowerCase() !== statusFilter) return false
      if (archiveFilter === 'ready' && !row.hasArchive) return false
      if (archiveFilter === 'pending' && row.hasArchive) return false
      const started = row.startedAt ? new Date(row.startedAt).getTime() : NaN
      if (fromDate && (!Number.isFinite(started) || started < new Date(`${fromDate}T00:00:00`).getTime())) return false
      if (toDate) {
        const end = new Date(`${toDate}T00:00:00`)
        end.setDate(end.getDate() + 1)
        if (!Number.isFinite(started) || started >= end.getTime()) return false
      }
      if (!normalizedSearch) return true
      const haystack = [
        row.source,
        row.name,
        row.duration,
        row.status,
        row.releaseId,
        row.releaseName,
        row.totalVusers !== undefined ? String(row.totalVusers) : '',
        row.groupCount !== undefined ? String(row.groupCount) : '',
        row.item?.content_hash,
        row.item?.id,
        row.item?.local_item_id,
        row.item?.remote_item_id,
        row.test?.id,
        row.test?.ref_key,
        row.test?.archive?.ref_key,
        ...(row.test?.groups || []).map(group => group.name || group.script_name || group.script_id),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return haystack.includes(normalizedSearch)
    })
  }, [activeApplication, activeRelease, applicationQualifiedRows, archiveFilter, localReleaseByRemoteId, mappedRemoteApplicationId, mappedRemoteReleaseId, releaseFilter, search, statusFilter, fromDate, toDate])

  const sortedRows = useMemo(() => {
    return [...filteredRows].sort((a, b) => {
      let aValue: string | number = ''
      let bValue: string | number = ''
      switch (sortField) {
        case 'source':
          aValue = a.source
          bValue = b.source
          break
        case 'status':
          aValue = a.status.toLowerCase()
          bValue = b.status.toLowerCase()
          break
        case 'total_vusers':
          aValue = a.totalVusers ?? -1
          bValue = b.totalVusers ?? -1
          break
        case 'groups':
          aValue = a.groupCount ?? -1
          bValue = b.groupCount ?? -1
          break
        case 'started':
          aValue = rowStartedAt(a)
          bValue = rowStartedAt(b)
          break
        case 'duration':
          if (a.test && a.durationSeconds === undefined) return b.test && b.durationSeconds === undefined ? 0 : 1
          if (b.test && b.durationSeconds === undefined) return -1
          aValue = a.durationSeconds ?? parseDurationToSeconds(a.duration)
          bValue = b.durationSeconds ?? parseDurationToSeconds(b.duration)
          break
        case 'archive':
          aValue = a.archiveCount ?? (a.hasArchive ? 1 : 0)
          bValue = b.archiveCount ?? (b.hasArchive ? 1 : 0)
          break
        case 'name':
        default:
          aValue = (a.name || '').toLowerCase()
          bValue = (b.name || '').toLowerCase()
      }
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [filteredRows, sortDirection, sortField])
  const resultsTableRef = useRef<HTMLDivElement | null>(null)
  const shouldVirtualizeRows = sortedRows.length > 80
  const rowVirtualizer = useVirtualizer({
    count: sortedRows.length,
    getScrollElement: () => resultsTableRef.current,
    estimateSize: () => 76,
    overscan: 8,
    getItemKey: index => sortedRows[index]?.rowKey ?? index,
  })
  const virtualRows = shouldVirtualizeRows ? rowVirtualizer.getVirtualItems() : []
  const virtualPaddingTop = shouldVirtualizeRows ? (virtualRows[0]?.start ?? 0) : 0
  const virtualPaddingBottom = shouldVirtualizeRows && virtualRows.length > 0
    ? Math.max(0, rowVirtualizer.getTotalSize() - virtualRows[virtualRows.length - 1].end)
    : 0

  const visibleSelectableIds = useMemo(
    () => sortedRows.flatMap(row => row.test ? [row.test.id] : []),
    [sortedRows],
  )
  const visibleSelectedIds = useMemo(
    () => visibleSelectableIds.filter(id => selectedIds.has(id)),
    [selectedIds, visibleSelectableIds],
  )

  useEffect(() => {
    setSelectedIds(current => {
      const visible = new Set(visibleSelectableIds)
      const next = new Set(Array.from(current).filter(id => visible.has(id)))
      return next.size === current.size ? current : next
    })
  }, [visibleSelectableIds])

  const selectedTests = useMemo(
    () => sortedRows.flatMap(row => (row.test && visibleSelectedIds.includes(row.test.id) ? [row.test] : [])),
    [sortedRows, visibleSelectedIds],
  )

  const selectedArchivedTests = useMemo(
    () => selectedTests.filter(test => Boolean(test.archive)),
    [selectedTests],
  )

  const selectionState = useMemo(() => {
    if (selectedArchivedTests.length > MAX_COMPARISON_RUNS) return `${selectedArchivedTests.length} archived runs selected; choose ${MAX_COMPARISON_RUNS} or fewer to compare`
    if (selectedArchivedTests.length >= 2) return `Ready to compare ${selectedArchivedTests.length} archived runs`
    if (selectedArchivedTests.length === 1) return 'Select one more archived run to compare'
    if (selectedTests.length > 0) return 'Archive comparison requires archived runs'
    return ''
  }, [selectedArchivedTests.length, selectedTests.length])

  const canCompareSelected = selectedArchivedTests.length >= 2 && selectedArchivedTests.length <= MAX_COMPARISON_RUNS

  const comparisonTestIdKey = useMemo(() => comparisonTestIds.join('|'), [comparisonTestIds])
  const comparisonSnapshotIdKey = useMemo(() => comparisonTestSnapshots.map(test => test.id).join('|'), [comparisonTestSnapshots])

  const comparisonTests = useMemo(
    () => {
      if (comparisonTestIdKey && comparisonSnapshotIdKey === comparisonTestIdKey) {
        return comparisonTestSnapshots
      }
      const testById = new Map(completed.map(test => [test.id, test]))
      return sortTestsByStartAscending(comparisonTestIds.flatMap(id => {
        const test = testById.get(id)
        return test?.archive ? [test] : []
      }))
    },
    [completed, comparisonSnapshotIdKey, comparisonTestIdKey, comparisonTestIds, comparisonTestSnapshots],
  )

  const comparisonSummaryAction = comparisonActionFilter === ALL_COMPARISON_ACTIONS ? undefined : comparisonActionFilter

  const loadComparisonSummaries = useCallback(async (testId: string) => {
    const response = await api.getTestArchiveSummaries(testId, {
      sample_limit: ARCHIVE_CHART_MAX_SAMPLES,
      action: comparisonSummaryAction,
    })
    return response.summaries || []
  }, [api, comparisonSummaryAction])

  useEffect(() => {
    if (comparisonTestIds.length < 2 || comparisonTestIds.length > MAX_COMPARISON_RUNS) {
      setComparisonReports({})
      setComparisonSummaries({})
      setComparisonLoading(false)
      setComparisonError('')
      return
    }
    if (comparisonTests.length !== comparisonTestIds.length) {
      setComparisonReports({})
      setComparisonSummaries({})
      setComparisonLoading(false)
      setComparisonError('Selected comparison runs are no longer available')
      return
    }

    let cancelled = false
    const loadComparison = async () => {
      setComparisonLoading(true)
      setComparisonError('')
      try {
        const reports = await Promise.all(
          comparisonTests.map(async test => [test.id, (await api.getTestArchiveReport(test.id)) as RunArchiveReport] as const),
        )
        const summaries = await Promise.all(
          comparisonTests.map(async test => [test.id, await loadComparisonSummaries(test.id)] as const),
        )
        if (!cancelled) {
          setComparisonReports(Object.fromEntries(reports))
          setComparisonSummaries(Object.fromEntries(summaries))
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setComparisonReports({})
          setComparisonSummaries({})
          setComparisonError(getErrorMessage(error, 'Unable to load comparison reports'))
        }
      } finally {
        if (!cancelled) setComparisonLoading(false)
      }
    }

    loadComparison()
    return () => {
      cancelled = true
    }
  }, [api, comparisonSummaryAction, comparisonTestIdKey, comparisonTestIds.length, comparisonTests, loadComparisonSummaries])

  const [comparisonBaselineId, setComparisonBaselineId] = useState('')
  const comparisonCards = useMemo(() => {
    if (comparisonTests.length < 2 || comparisonTests.length > MAX_COMPARISON_RUNS) return []
    const ordered = [...comparisonTests].sort((a, b) => Number(b.id === comparisonBaselineId) - Number(a.id === comparisonBaselineId))
    return ordered.map(test => ({ test, report: comparisonReports[test.id] }))
  }, [comparisonReports, comparisonTests, comparisonBaselineId])

  const comparisonActionOptions = useMemo(() => {
    if (comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) return []
    const actionSets = comparisonCards.map(({ report }) => new Set(Object.keys((report?.metric_populations ? report.by_request : report?.by_action) || {}).filter(Boolean)))
    if (actionSets.some(set => set.size === 0)) return []
    const [first, ...rest] = actionSets
    return Array.from(first)
      .filter(action => rest.every(set => set.has(action)))
      .sort((left, right) => left.localeCompare(right))
  }, [comparisonCards])

  const comparisonActionFiltered = comparisonActionFilter !== ALL_COMPARISON_ACTIONS

  useEffect(() => {
    if (comparisonActionFilter !== ALL_COMPARISON_ACTIONS && !comparisonActionOptions.includes(comparisonActionFilter)) {
      setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    }
  }, [comparisonActionFilter, comparisonActionOptions])

  const comparisonHasBrowserBreakdowns = useMemo(
    () => !comparisonActionFiltered && comparisonCards.some(({ report }) => getBrowserDeviceEntries(report).length > 0 || getBrowserPlatformEntries(report).length > 0 || getBrowserEngineEntries(report).length > 0),
    [comparisonActionFiltered, comparisonCards],
  )

  const exportComparison = useCallback(async () => {
    if (comparisonCards.length < 2 || comparisonCards.length > MAX_COMPARISON_RUNS || !comparisonCards.every(item => item.report)) {
      return
    }

    const [baseline, ...comparisonRuns] = comparisonCards
    const action = comparisonActionFiltered ? comparisonActionFilter : undefined
    const actionFilename = action ? `-${comparisonFilenameToken(action)}` : ''
    try {
      if (comparisonCards.length === 2) {
        const [current] = comparisonRuns
        const blob = action
          ? await api.downloadTestArchiveComparison(current.test.id, baseline.test.id, comparisonBreakdown, action)
          : await api.downloadTestArchiveComparison(current.test.id, baseline.test.id, comparisonBreakdown)
        await downloadBlob(blob, `archive-comparison-${current.test.id}-${baseline.test.id}-${comparisonBreakdown}${actionFilename}.csv`)
      } else {
        const comparisonIds = comparisonRuns.map(item => item.test.id)
        const blob = action
          ? await api.downloadTestArchiveMultiComparison(baseline.test.id, comparisonIds, comparisonBreakdown, action)
          : await api.downloadTestArchiveMultiComparison(baseline.test.id, comparisonIds, comparisonBreakdown)
        await downloadBlob(blob, `archive-comparison-${baseline.test.id}-${comparisonCards.length}-runs-${comparisonBreakdown}${actionFilename}.csv`)
      }
      setComparisonError('')
    } catch (error: unknown) {
      setComparisonError(getErrorMessage(error, 'Unable to export comparison CSV'))
    }
  }, [api, comparisonActionFilter, comparisonActionFiltered, comparisonBreakdown, comparisonCards])

  const comparisonActionRows = useMemo(() => {
    if (comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) return []
    const [baseline] = comparisonCards
    const baselineActions = (baseline.report?.metric_populations ? baseline.report.by_request : baseline.report?.by_action) || {}
    const actionNames = new Set<string>()
    if (comparisonActionFiltered) {
      actionNames.add(comparisonActionFilter)
    } else {
      comparisonCards.forEach(({ report }) => {
        Object.keys((report?.metric_populations ? report.by_request : report?.by_action) || {}).forEach(name => actionNames.add(name))
      })
    }
    return Array.from(actionNames)
      .map(name => {
        const baselineMetricValue = comparisonTableMetricValue(
          baselineActions[name] as RunArchiveActionStats | undefined,
          comparisonSummaries[baseline.test.id] || [],
          comparisonChartMetric,
          comparisonActionFiltered,
        )
        const runs = comparisonCards.map((card, index) => {
          const stats = (card.report?.metric_populations ? card.report.by_request : card.report?.by_action)?.[name] as RunArchiveActionStats | undefined
          const metricValue = comparisonTableMetricValue(
            stats,
            comparisonSummaries[card.test.id] || [],
            comparisonChartMetric,
            comparisonActionFiltered,
          )
          const metricDelta = metricValue !== undefined && baselineMetricValue !== undefined ? metricValue - baselineMetricValue : undefined
          return {
            test: card.test,
            stats,
            metricValue,
            metricDelta,
            isBaseline: index === 0,
            color: comparisonRunColor(index),
          }
        })
        return {
          name,
          runs,
          maxDelta: Math.max(...runs.map(run => Math.abs(run.metricDelta ?? 0))),
        }
      })
      .sort((a, b) => b.maxDelta - a.maxDelta)
      .slice(0, 8)
  }, [comparisonActionFilter, comparisonActionFiltered, comparisonCards, comparisonChartMetric, comparisonSummaries])

  const comparisonChartData = useMemo(() => {
    if (comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) return []
    const maxLength = Math.max(...comparisonCards.map(card => (comparisonSummaries[card.test.id] || []).length))
    return Array.from({ length: maxLength }, (_, sampleIndex) => {
      const row: Record<string, number | undefined> & { sample: number } = { sample: sampleIndex + 1 }
      comparisonCards.forEach((card, runIndex) => {
        const series = comparisonSummaries[card.test.id] || []
        row[comparisonSummaryDataKey(card.test.id, comparisonChartMetric, runIndex)] = comparisonChartMetricValue(series[sampleIndex], comparisonChartMetric)
      })
      return row
    })
  }, [comparisonCards, comparisonChartMetric, comparisonSummaries])

  const comparisonChartMetricConfig = useMemo(
    () => getComparisonChartMetricConfig(comparisonChartMetric),
    [comparisonChartMetric],
  )

  const comparisonSummarySeries = useMemo(() => {
    if (comparisonCards.length < 2) return []
    return buildComparisonSummarySeries(
      comparisonCards.map(({ test }) => ({ id: test.id, label: formatComparisonRunLabel(test) })),
      comparisonChartMetric,
    )
  }, [comparisonCards, comparisonChartMetric])

  const browserBreakdownChartData = useMemo(() => {
    if (comparisonActionFiltered || comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) {
      return [] as BrowserBreakdownDatum[]
    }

    const segmentMap = new Map<string, BrowserBreakdownDatum>()

    for (const { test, report } of comparisonCards) {
      if (!report) continue

      for (const metrics of buildBrowserBreakdownData(comparisonBreakdown, {
        summary: report.overview.browser,
        devices: report.browser_by_device,
        platforms: report.browser_by_platform,
        engines: report.browser_by_engine,
      })) {
        const current = segmentMap.get(metrics.segment) ?? { segment: metrics.segment }
        current[test.id] = metrics.lcp
        current[`${test.id}_inp`] = metrics.inp
        segmentMap.set(metrics.segment, current)
      }
    }

    return Array.from(segmentMap.values()).sort((left, right) => String(left.segment).localeCompare(String(right.segment)))
  }, [comparisonActionFiltered, comparisonBreakdown, comparisonCards])

  const browserBreakdownChartSeries = useMemo(
    () => buildComparisonBrowserBreakdownSeries(comparisonCards.map(({ test }) => ({ id: test.id, name: formatComparisonRunLabel(test) }))),
    [comparisonCards],
  )

  const browserBreakdownChartTitle = useMemo(
    () => getBrowserBreakdownTitle(comparisonBreakdown, 'Comparison', 'LCP / INP'),
    [comparisonBreakdown],
  )

  const browserBreakdownChartCopy = useMemo(
    () => getBrowserBreakdownChartCopy(browserBreakdownChartSeries),
    [browserBreakdownChartSeries],
  )

  const browserBreakdownSegmentLabel = useMemo(
    () => getBrowserBreakdownSegmentLabel(comparisonBreakdown),
    [comparisonBreakdown],
  )

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDirection(prev => (prev === 'asc' ? 'desc' : 'asc'))
      return
    }
    setSortField(field)
    setSortDirection(field === 'started' || field === 'archive' ? 'desc' : 'asc')
  }

  const toggleSelect = (id: string, event: React.MouseEvent | React.ChangeEvent) => {
    event.stopPropagation()
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const runSelectedComparison = useCallback(() => {
    if (!canCompareSelected) return
    const sortedSelection = sortTestsByStartAscending(selectedArchivedTests)
    setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    setComparisonTestSnapshots(sortedSelection)
    setComparisonTestIds(sortedSelection.map(test => test.id))
  }, [canCompareSelected, selectedArchivedTests])

  const handleDelete = async (id: string, event: React.MouseEvent) => {
    event.stopPropagation()
    await deleteResult(id)
  }

  const deleteResult = async (id: string) => {
    if (deletingResultIdsRef.current.has(id)) return
    if (!(await confirmAction('Delete this scenario result?'))) return
    if (deletingResultIdsRef.current.has(id)) return
    const requestScopeKey = resultCatalogScopeKey
    deletingResultIdsRef.current.add(id)
    setDeletingResultIds(new Set(deletingResultIdsRef.current))
    setError('')
    try {
      await api.deleteTest(id)
      if (resultCatalogScopeKeyRef.current === requestScopeKey) {
        invalidateResultTailAfterDelete([id])
        setDeletedResultIds(current => new Set(current).add(id))
        if (comparisonTestIds.includes(id)) {
          setComparisonTestIds([])
          setComparisonTestSnapshots([])
          setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
        }
        setSelectedIds(prev => {
          const next = new Set(prev)
          next.delete(id)
          return next
        })
      }
    } catch (error: unknown) {
      if (resultCatalogScopeKeyRef.current === requestScopeKey) {
        setError(getErrorMessage(error, 'Unable to delete the result'))
      }
    } finally {
      deletingResultIdsRef.current.delete(id)
      setDeletingResultIds(new Set(deletingResultIdsRef.current))
      await refresh()
    }
  }

  const deleteSelected = async () => {
    const ids = visibleSelectedIds.filter(id => !deletingResultIdsRef.current.has(id))
    if (ids.length === 0 || !(await confirmAction(`Delete ${ids.length} selected results?`))) return
    const requestScopeKey = resultCatalogScopeKey
    ids.forEach(id => deletingResultIdsRef.current.add(id))
    setDeletingResultIds(new Set(deletingResultIdsRef.current))
    setError('')
    const outcomes = await boundedAllSettled(ids, id => api.deleteTest(id))
    const deletedIDs = ids.filter((_, index) => outcomes[index]?.status === 'fulfilled')
    const failed = outcomes.flatMap((outcome, index) => outcome.status === 'rejected'
      ? [{ id: ids[index], reason: getErrorMessage(outcome.reason, 'delete failed') }]
      : [])
    if (deletedIDs.length > 0 && resultCatalogScopeKeyRef.current === requestScopeKey) {
      invalidateResultTailAfterDelete(deletedIDs)
      setDeletedResultIds(current => {
        const next = new Set(current)
        deletedIDs.forEach(id => next.add(id))
        return next
      })
    }
    if (resultCatalogScopeKeyRef.current === requestScopeKey && deletedIDs.some(id => comparisonTestIds.includes(id))) {
      setComparisonTestIds([])
      setComparisonTestSnapshots([])
      setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    }
    if (resultCatalogScopeKeyRef.current === requestScopeKey) {
      setSelectedIds(current => {
        const next = new Set(current)
        deletedIDs.forEach(id => next.delete(id))
        return next
      })
    }
    ids.forEach(id => deletingResultIdsRef.current.delete(id))
    setDeletingResultIds(new Set(deletingResultIdsRef.current))
    await refresh()
    if (failed.length > 0 && resultCatalogScopeKeyRef.current === requestScopeKey) {
      const failureSummary = failed.slice(0, 3).map(item => `${item.id}: ${item.reason}`).join('; ')
      const suffix = failed.length > 3 ? `; ${failed.length - 3} more failed` : ''
      setError(`Deleted ${deletedIDs.length} of ${ids.length} selected results. ${failureSummary}${suffix}`)
    }
  }

  const toggleAllVisible = (checked: boolean) => {
    setSelectedIds(current => {
      const next = new Set(current)
      visibleSelectableIds.forEach(id => checked ? next.add(id) : next.delete(id))
      return next
    })
  }

  const showResultsLoadStatus = resultsSourcesLoading || resultTailLoading || resultsRefreshing || comparisonCards.length >= 2
  const showComparisonLoadedHint = comparisonCards.length >= 2
  const selectedDeletePending = visibleSelectedIds.some(id => deletingResultIds.has(id))
  const showResultsActions = showResultsLoadStatus || showComparisonLoadedHint

  const toggleBaselinePin = useCallback(async (test: TestSummary, event: React.MouseEvent) => {
    event.stopPropagation()
    try {
      await (test.baseline_pinned
        ? api.unpinComparisonBaseline(test.id)
        : api.pinComparisonBaseline(test.id))
      setError('')
      await refresh()
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Unable to update pinned comparison baseline'))
    }
  }, [api, refresh])

  const renderResultRow = (row: ResultSourceRow, measureElement?: (element: HTMLTableRowElement | null) => void, dataIndex?: number) => {
    const test = row.test
    const externalImport = row.externalImport
    const item = row.item
    const hostItem = row.source === 'Local' && item ? hostItemForLocal(item.id) : undefined
    const href = row.source === 'Remote' && item ? remoteItemHref(item) : ''
    const externalArchiveHref = externalImport?.has_archive
      ? `/api/v1/library/results/${encodeURIComponent(externalImport.id)}/archive`
      : ''
    const localItemID = item?.local_item_id
    const resultDestination = row.source === 'Remote'
      ? {
          applicationId: activeApplication?.id,
          releaseId: row.releaseId ? localReleaseByRemoteId.get(row.releaseId) : undefined,
        }
      : {
          applicationId: row.applicationId || activeApplication?.id,
          releaseId: row.releaseId || activeRelease?.id,
        }
    const rowActionBlockedReason = item
      ? transferBlockReasonForItem(item, resultDestination)
      : actionBlockedReason
    const rowActionBlocked = Boolean(rowActionBlockedReason)
    const transferBlockedByConflict = row.syncStatus === 'conflict'
    const transferBlockTitle = transferBlockedByConflict ? 'Compare this conflict before transferring.' : rowActionBlockedReason
    return (
      <tr
        key={row.rowKey}
        ref={measureElement}
        data-index={dataIndex}
        className={`${test ? 'clickable-row' : 'no-hover'} ${test && selectedIds.has(test.id) ? 'selected' : ''}`}
        onClick={test ? () => (onOpenReport ?? onOpenTestArchive ?? onSelectTest)(test.id) : undefined}
      >
        <td className="results-compare-select-cell">
          {test ? (
            <input
              aria-label={`Select ${row.name}`}
              type="checkbox"
              checked={selectedIds.has(test.id)}
              onClick={event => event.stopPropagation()}
              onChange={event => toggleSelect(test.id, event)}
            />
          ) : (
            <span className="cell-muted">-</span>
          )}
        </td>
        <td className="results-source-cell">
          <span className="script-health-token">{row.source}</span>
        </td>
        <td className="cell-name">
          <div>{row.name}</div>
          <div className="results-compact-status" aria-hidden="true">{test ? <StatusBadge status={row.status} stoppedByOperator={Boolean(test.stopped_by_subject)} /> : <span className={`script-health-token ${resultSourceStatusTone(row.status)}`}>{row.status}</span>}</div>
          {test?.archive && <ResultHeadline testId={test.id} />}
          <div className="script-table-meta">
            {test?.ref_key ? (
              <ReferenceKeyBadge value={test.ref_key} />
            ) : test ? (
              <span className="results-run-id" title={test.id}>Run ID {formatShortRunId(test.id)}</span>
            ) : (
              <>
                <span className="script-health-token">result</span>
                {row.releaseName && <span className="script-health-token">{row.releaseName}</span>}
                {item?.content_hash && <span className="script-health-token">hash {shortLibraryHash(item.content_hash)}</span>}
              </>
            )}
          </div>
        </td>
        <td>
          {test ? (
            <StatusBadge status={row.status} stoppedByOperator={Boolean(test.stopped_by_subject)} />
          ) : (
            <span className={`script-health-token ${resultSourceStatusTone(row.status)}`}>{row.status}</span>
          )}
        </td>
        <td>{row.totalVusers ?? '-'}</td>
        <td>{row.groupCount ?? '-'}</td>
        <td className="cell-muted">{row.startedAt ? formatDate(row.startedAt) : '-'}</td>
        <td className="cell-muted">{row.duration || '-'}{!row.test && <small> (source-reported)</small>}</td>
        <td className="cell-muted">
          {test
            ? (test.archive ? `${test.archive.transaction_count} archive records` : 'Unavailable')
            : (externalImport && row.archiveCount !== undefined ? `${row.archiveCount} tx` : (row.hasArchive ? 'Ready' : '-'))}
          {test?.baseline_pinned && (
            <>
              <br />
              <span title={formatBaselinePin(test.baseline_pinned_at)}>Pinned baseline</span>
            </>
          )}
        </td>
        <td>
          <div className="results-row-actions">
            {test ? (
              <>
                <button className="btn btn-sm btn-primary" onClick={event => { event.stopPropagation(); (onOpenReport ?? onOpenTestArchive ?? onSelectTest)(test.id) }}>
                  View results
                </button>
                <RowActions className="results-management">
                <button className="btn btn-sm btn-ghost" onClick={event => toggleBaselinePin(test, event)}>
                  {test.baseline_pinned ? 'Unpin Baseline' : 'Pin Baseline'}
                </button>
                <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={event => handleDelete(test.id, event)} disabled={deletingResultIds.has(test.id)}>
                  {deletingResultIds.has(test.id) ? 'Deleting…' : 'Delete'}
                </button>
                <button
                  className="btn btn-sm btn-secondary"
                  type="button"
                  onClick={event => { event.stopPropagation(); void pushLocalItem(item ?? catalogItemForTest(test)) }}
                  disabled={rowActionBlocked || transferBlockedByConflict}
                  title={transferBlockTitle}
                >
                  Push
                </button>
                {hostItem?.local_item_id && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={event => { event.stopPropagation(); void compareItems(test.id, hostItem) }}>
                    Compare
                  </button>
                )}
                </RowActions>
              </>
            ) : externalImport && item ? (
              <>
                {externalArchiveHref && (
                  <DesktopApiResourceLink
                    className="btn btn-sm btn-secondary"
                    href={externalArchiveHref}
                    filename={`${externalImport.name || externalImport.id}-${externalImport.id}.zip`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    Open Archive
                  </DesktopApiResourceLink>
                )}
                {hostItem?.local_item_id && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={event => { event.stopPropagation(); void compareItems(externalImport.id, hostItem) }}>
                    Compare
                  </button>
                )}
              </>
            ) : item ? (
              <>
                <button
                  className="btn btn-sm btn-secondary"
                  type="button"
                  onClick={event => { event.stopPropagation(); void pullRemoteItem(item, resultDestination) }}
                  disabled={rowActionBlocked || transferBlockedByConflict}
                  title={transferBlockTitle}
                >
                  Pull
                </button>
                {localItemID && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={event => { event.stopPropagation(); void compareItems(localItemID, item) }}>
                    Compare
                  </button>
                )}
                {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer" onClick={event => event.stopPropagation()}>Open in Remote Workspace</a>}
              </>
            ) : (
              <span className="cell-muted">-</span>
            )}
          </div>
        </td>
      </tr>
    )
  }

  if (!activeApplication?.id || !activeRelease?.id) {
    return (
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Results Library</h2>
            <p className="page-subtitle">Select an application and release to browse archived runs.</p>
          </div>
        </div>
        <div className="empty-card">
          <p>No release selected.</p>
          <p className="empty-hint">Results are scoped to one application and one release so the history stays clear.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Results Library</h2>
          <p className="page-subtitle">
            {activeApplication.name} / {activeRelease.name}
          </p>
        </div>
        {showResultsActions && (
          <div className="results-actions">
            {showResultsLoadStatus && (
              <span className={`results-load-status${resultsSourcesLoading || resultTailLoading || resultsRefreshing ? '' : ' idle'}`} aria-live="polite">
                {resultsSourcesLoading ? 'Loading results...' : resultTailLoading ? 'Loading older results...' : resultsRefreshing ? 'Refreshing...' : 'Results refreshed'}
              </span>
            )}
            {showComparisonLoadedHint && (
              <span className="results-compare-hint">Comparison loaded</span>
            )}
          </div>
        )}
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      {(comparisonCards.length >= 2 || comparisonLoading || comparisonError) && (
        <div className="page-section">
          <div className="results-compare-card">
            <div className="results-compare-header">
              <div>
                <h3 className="section-title">Run Comparison</h3>
                <p className="page-subtitle">Compare 2-{MAX_COMPARISON_RUNS} archived runs. Select the baseline below; the oldest selected run is the initial reference.</p>
              </div>
              {comparisonCards.length >= 2 && comparisonCards.every(item => item.report) && (
                <div className="results-compare-controls">
                  <label className="results-compare-filter"><span>Baseline run</span><select className="settings-input" value={comparisonCards[0].test.id} onChange={event => setComparisonBaselineId(event.target.value)}>{comparisonTests.map(run => <option key={run.id} value={run.id}>{formatComparisonRunLabel(run)}</option>)}</select></label>
                  <label className="results-compare-filter">
                    <span>Request / action</span>
                    <select
                      className="settings-input library-source-select"
                      value={comparisonActionFilter}
                      onChange={event => setComparisonActionFilter(event.target.value)}
                    >
                      <option value={ALL_COMPARISON_ACTIONS}>Named transaction population</option>
                      {comparisonActionOptions.map(action => (
                        <option key={action} value={action}>{action}</option>
                      ))}
                    </select>
                  </label>
                  {comparisonHasBrowserBreakdowns && (
                    <TabList className="results-breakdown-toggle" role="tablist" aria-label="Comparison breakdown mode" onKeyDown={handleTabKey}>
                      {(['overall', 'platform', 'engine', 'device'] as ComparisonBreakdown[]).map(mode => (
                        <button
                          key={mode}
                          type="button"
                          role="tab"
                          aria-selected={comparisonBreakdown === mode}
                          className={`results-breakdown-button${comparisonBreakdown === mode ? ' active' : ''}`}
                          onClick={() => setComparisonBreakdown(mode)} tabIndex={(comparisonBreakdown === mode) ? 0 : -1}
                        >
                          {mode === 'overall' ? 'Overall' : mode === 'platform' ? 'Platform' : mode === 'engine' ? 'Engine' : 'Device'}
                        </button>
                      ))}
                    </TabList>
                  )}
                  <button type="button" className="btn btn-secondary" onClick={exportComparison}>
                    Export CSV
                  </button>
                </div>
              )}
            </div>

            {comparisonLoading && <p className="results-compare-status">Loading comparison...</p>}
            {comparisonError && <p className="results-compare-status error">{comparisonError}</p>}

            {comparisonCards.length >= 2 && comparisonCards.every(item => item.report) && (
              <>
                <div className="results-compare-grid">
                  {comparisonCards.map(({ test, report }, index) => (
                    <div key={test.id} className="results-compare-panel" style={{ borderTopColor: comparisonRunColor(index), borderTopWidth: 3 }}>
                      {(() => {
                        const deviceEntries = getBrowserDeviceEntries(report)
                        const platformEntries = getBrowserPlatformEntries(report)
                        const engineEntries = getBrowserEngineEntries(report)
                        const browserRuntimeSummary = formatBrowserRuntimeSummary(report?.manifest.provenance?.runtime_settings?.browser)
                        const actionStats = comparisonActionFiltered ? (report?.metric_populations ? report.by_request : report?.by_action)?.[comparisonActionFilter] : undefined
                        const population = report?.metric_populations?.transaction
                        const namedPopulation = population?.transactions ? population : undefined
                        const cardTransactions = comparisonActionFiltered ? actionStats?.transactions ?? 0 : namedPopulation?.transactions
                        const cardAvgResponseMs = comparisonActionFiltered ? actionStats?.avg_response_ms : namedPopulation?.avg_response_ms
                        const cardErrorPct = comparisonActionFiltered ? actionStats?.error_pct : namedPopulation?.error_pct
                        const cardPeakTPS = comparisonActionFiltered ? actionStats?.peak_tps : namedPopulation?.peak_tps

                        return (
                          <>
                            <div className="results-compare-panel-header">
                              <div>
                                <h4>{test.name}</h4>
                                <p>{formatDate(test.start_time || test.created_at)}</p>
                                <span className="results-compare-run-label results-compare-run-label-inline">
                                  <span className="results-run-dot" style={{ background: comparisonRunColor(index) }} aria-hidden="true" />
                                  {index === 0 ? 'Baseline' : `Run ${index + 1}`}
                                </span>
                                {test.ref_key ? (
                                  <ReferenceKeyBadge value={test.ref_key} />
                                ) : (
                                  <span className="results-run-id" title={test.id}>Run ID {formatShortRunId(test.id)}</span>
                                )}
                              </div>
                              <div className="results-compare-panel-badges">
                                {index === 0 && <span className="results-compare-baseline-chip">Reference</span>}
                                <StatusBadge status={resolveStatus(test, testStatuses)} stoppedByOperator={Boolean(test.stopped_by_subject)} />
                              </div>
                            </div>
                            <div className="results-compare-metrics">
                              <div className="results-compare-metric">
                                <span className="results-compare-label">{comparisonActionFiltered ? 'Requests / actions' : 'Named transactions'}</span>
                                <span className="results-compare-value">{cardTransactions ?? (population ? 0 : 'Unknown')}</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Avg Latency</span>
                                <span className="results-compare-value">{cardAvgResponseMs === undefined ? 'Unknown' : `${cardAvgResponseMs.toFixed(1)} ms`}</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Error Rate</span>
                                <span className="results-compare-value">{cardErrorPct === undefined ? 'Unknown' : `${cardErrorPct.toFixed(2)}%`}</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Peak {comparisonActionFiltered ? 'request' : 'transaction'} attempts/s</span>
                                <span className="results-compare-value">{cardPeakTPS?.toFixed(1) ?? 'Unknown'}</span>
                              </div>
                              {!comparisonActionFiltered && (
                                <>
                                  <div className="results-compare-metric">
                                    <span className="results-compare-label">Peak VUsers</span>
                                    <span className="results-compare-value">{report!.overview.peak_active_vusers}</span>
                                  </div>
                                  <div className="results-compare-metric">
                                    <span className="results-compare-label">CPU Peak</span>
                                    <span className="results-compare-value">{report!.resource_peaks.cpu_process_pct.toFixed(1)}%</span>
                                  </div>
                                </>
                              )}
                              {!comparisonActionFiltered && (hasBrowserOverview(report) || comparisonCards.some(item => hasBrowserOverview(item.report))) && (
                                <>
                                  {browserRuntimeSummary && (
                                    <div className="results-compare-metric">
                                      <span className="results-compare-label">Browser Runtime</span>
                                      <span className="results-compare-value">{browserRuntimeSummary}</span>
                                    </div>
                                  )}
                                  <div className="results-compare-metric">
                                    <span className="results-compare-label">Avg LCP</span>
                                    <span className="results-compare-value">{formatBrowserMetric(report!.overview.browser?.avg_lcp_ms, ' ms')}</span>
                                  </div>
                                  <div className="results-compare-metric">
                                    <span className="results-compare-label">Avg INP</span>
                                    <span className="results-compare-value">{formatBrowserMetric(report!.overview.browser?.avg_inp_ms, ' ms')}</span>
                                  </div>
                                </>
                              )}
                            </div>
                            {!comparisonActionFiltered && comparisonBreakdown === 'platform' && platformEntries.length > 0 && (
                              <>
                                <span className="results-compare-label">Platform Rollup</span>
                                <div className="browser-device-grid archive-browser-device-grid">
                                  {platformEntries.map(metrics => (
                                    <div key={metrics.device_platform || 'unknown'} className="browser-device-card">
                                      <div className="browser-device-header">
                                        <span className="browser-device-name">{formatDevicePlatformLabel(metrics.device_platform)}</span>
                                        <span className="browser-device-tier">{formatBrowserEngineLabel(metrics.browser_engine)} · {metrics.samples} samples</span>
                                      </div>
                                      <div className="browser-device-stats">
                                        <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                                        <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                                        <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              </>
                            )}
                            {!comparisonActionFiltered && comparisonBreakdown === 'engine' && engineEntries.length > 0 && (
                              <>
                                <span className="results-compare-label">Engine Rollup</span>
                                <div className="browser-device-grid archive-browser-device-grid">
                                  {engineEntries.map(metrics => (
                                    <div key={metrics.browser_engine || 'browser'} className="browser-device-card">
                                      <div className="browser-device-header">
                                        <span className="browser-device-name">{formatBrowserEngineLabel(metrics.browser_engine)}</span>
                                        <span className="browser-device-tier">{metrics.samples} samples</span>
                                      </div>
                                      <div className="browser-device-stats">
                                        <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                                        <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                                        <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              </>
                            )}
                            {!comparisonActionFiltered && comparisonBreakdown === 'device' && deviceEntries.length > 0 && (
                              <>
                                <span className="results-compare-label">Devices</span>
                                <div className="browser-device-grid archive-browser-device-grid">
                                  {deviceEntries.map(([profile, metrics]) => (
                                    <div key={profile} className="browser-device-card">
                                      <div className="browser-device-header">
                                        <span className="browser-device-name">{profile}</span>
                                        <span className="browser-device-tier">{formatDevicePlatformLabel(metrics.device_platform)} · {formatBrowserEngineLabel(metrics.browser_engine)} · {metrics.device_tier || 'unknown'} tier</span>
                                      </div>
                                      <div className="browser-device-stats">
                                        <span>LCP {formatBrowserMetric(metrics.avg_lcp_ms, ' ms')}</span>
                                        <span>CLS {formatBrowserScore(metrics.avg_cls_score)}</span>
                                        <span>INP {formatBrowserMetric(metrics.avg_inp_ms, ' ms')}</span>
                                      </div>
                                    </div>
                                  ))}
                                </div>
                              </>
                            )}
                          </>
                        )
                      })()}
                    </div>
                  ))}
                </div>

                <div className="results-compare-table-wrap">
                  {browserBreakdownChartData.length > 0 && comparisonCards.length >= 2 && (
                    <BrowserBreakdownChart
                      title={browserBreakdownChartTitle}
                      copy={browserBreakdownChartCopy}
                      data={browserBreakdownChartData}
                      series={browserBreakdownChartSeries}
                      height={280}
                      segmentLabel={browserBreakdownSegmentLabel}
                    />
                  )}

                  {comparisonChartData.length > 0 && (
                    <div className="results-compare-chart-card">
                      <div className="results-compare-chart-header">
                        <div>
                          <h4 className="results-compare-chart-title">Archived Summary Curves</h4>
                          <p className="results-compare-chart-copy">
                            Showing {comparisonChartMetricConfig.description}; color identifies the run. Curves use up to {ARCHIVE_CHART_MAX_SAMPLES.toLocaleString()} representative samples per run.
                          </p>
                        </div>
                        <TabList className="results-compare-metric-tabs" role="tablist" aria-label="Comparison metric" onKeyDown={handleTabKey}>
                          {COMPARISON_CHART_METRICS.map(metric => (
                            <button
                              key={metric.id}
                              type="button"
                              role="tab"
                              aria-selected={comparisonChartMetric === metric.id}
                              className={`results-compare-metric-tab${comparisonChartMetric === metric.id ? ' active' : ''}`}
                              onClick={() => setComparisonChartMetric(metric.id)} tabIndex={(comparisonChartMetric === metric.id) ? 0 : -1}
                            >
                              {metric.label}
                            </button>
                          ))}
                        </TabList>
                      </div>
                      <ResponsiveContainer width="100%" height={260}>
                        <LineChart data={comparisonChartData} margin={{ top: 10, right: 16, bottom: 12, left: 24 }}>
                          <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                          <XAxis
                            dataKey="sample"
                            stroke="var(--text-muted)"
                            fontSize={11}
                            height={42}
                            label={{ value: 'Sample', position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 11 }}
                          />
                          <YAxis
                            yAxisId="value"
                            stroke="var(--text-muted)"
                            fontSize={11}
                            width={86}
                            label={{ value: comparisonChartYAxisLabel(comparisonChartMetricConfig), angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 11 }}
                          />
                          <Tooltip
                            contentStyle={chartTooltipContentStyle}
                            labelStyle={chartTooltipLabelStyle}
                            labelFormatter={value => `Sample ${value}`}
                            formatter={(value) => formatComparisonChartValue(value, comparisonChartMetricConfig.valueFormat)}
                            isAnimationActive={false}
                          />
                          <Legend />
                          {comparisonSummarySeries.map(series => (
                            <Line
                              key={series.dataKey}
                              yAxisId={series.yAxisId}
                              type="monotone"
                              dataKey={series.dataKey}
                              name={series.name}
                              stroke={series.stroke}
                              dot={false}
                              isAnimationActive={false}
                            />
                          ))}
                        </LineChart>
                      </ResponsiveContainer>
                    </div>
                  )}
                  <table className="results-compare-table">
                    <thead>
                      <tr>
                        <th>Action</th>
                        <th>Run</th>
                        <th>{comparisonTableMetricLabel(comparisonChartMetric)}</th>
                        <th>Errors</th>
                        <th>Delta vs Baseline</th>
                      </tr>
                    </thead>
                    <tbody>
                      {comparisonActionRows.map(row => (
                        <Fragment key={row.name}>
                          {row.runs.map((run, index) => (
                            <tr key={`${row.name}-${run.test.id}`} className={index === 0 ? 'results-compare-action-start' : undefined}>
                              {index === 0 && <td className="cell-name" rowSpan={row.runs.length}>{row.name}</td>}
                              <td>
                                <span className="results-compare-run-label results-compare-run-label-inline">
                                  <span className="results-run-dot" style={{ background: run.color }} aria-hidden="true" />
                                  {formatComparisonRunLabel(run.test)}
                                </span>
                              </td>
                              <td>{formatComparisonTableMetricValue(run.metricValue, comparisonChartMetric)}</td>
                              <td>{run.stats?.errors || 0}</td>
                              <td className={run.isBaseline ? 'cell-muted' : comparisonTableDeltaClass(run.metricDelta, comparisonChartMetric)}>
                                {run.isBaseline ? 'Reference' : formatComparisonTableDelta(run.metricDelta, comparisonChartMetric)}
                              </td>
                            </tr>
                          ))}
                        </Fragment>
                      ))}
                    </tbody>
                  </table>
                </div>
              </>
            )}
          </div>
        </div>
      )}

      <div className="page-section">
        <div className="library-source-panel results-library-panel">
          <LibraryFederationControls
            kind="result"
            filtersActive={Boolean(search || fromDate || toDate || releaseFilter !== (activeRelease?.id || 'all') || statusFilter !== 'all' || archiveFilter !== 'all')}
            federation={federation}
            projectName={activeProjectName}
            onConfigureRemotes={openRemoteSettings}
            searchControl={(<label className="project-redaction-rule-field">
                  <span>Search results</span>
                  <input
                    className="settings-input"
                    type="search"
                    value={search}
                    onChange={event => setSearch(event.target.value)}
                    placeholder="Find runs by name, reference or scenario..."
                  />
                </label>)}
            filterControls={(
              <>
                <label className="project-redaction-rule-field"><span>Started from</span><input className="settings-input" type="date" value={fromDate} max={toDate || undefined} onInput={event => setFromDate(event.currentTarget.value)} onChange={event => setFromDate(event.currentTarget.value)} /></label>
                <label className="project-redaction-rule-field"><span>Started through</span><input className="settings-input" type="date" value={toDate} min={fromDate || undefined} onInput={event => setToDate(event.currentTarget.value)} onChange={event => setToDate(event.currentTarget.value)} /></label>
                <label className="project-redaction-rule-field">
                  <span>Associated Release</span>
                  <select
                    className="settings-input library-source-select library-release-filter"
                    value={releaseFilter}
                    onChange={event => setReleaseFilter(event.target.value)}
                  >
                    <option value="all">All releases</option>
                    {releaseFilter !== 'all' && !releaseOptions.some(option => option.value === releaseFilter) && (
                      <option value={releaseFilter}>{releaseFilter}</option>
                    )}
                    {releaseOptions.map(option => (
                      <option key={option.value} value={option.value}>{option.label}</option>
                    ))}
                  </select>
                </label>

                <label className="project-redaction-rule-field">
                  <span>Status</span>
                  <select className="settings-input library-source-select" value={statusFilter} onChange={event => setStatusFilter(event.target.value as StatusFilter)}>
                    <option value="all">All statuses</option>
                    <option value="complete">Complete</option>
                    <option value="failed">Failed</option>
                    <option value="aborted">Aborted</option>
                  </select>
                </label>
                <label className="project-redaction-rule-field">
                  <span>Archive State</span>
                  <select className="settings-input library-source-select" value={archiveFilter} onChange={event => setArchiveFilter(event.target.value as ArchiveFilter)}>
                    <option value="all">All archive states</option>
                    <option value="ready">Archive ready</option>
                    <option value="pending">Archive unavailable</option>
                  </select>
                </label>
              </>
            )}
            summaryText={resultsSourcesLoading
              ? 'Loading local and remote result sources...'
              : `${filteredRows.length} matching run${filteredRows.length === 1 ? '' : 's'} · ${activeRelease.name}.`}
          />
          {(search || fromDate || toDate || statusFilter !== 'all' || archiveFilter !== 'all' || releaseFilter !== (activeRelease?.id || 'all')) && <div className="settings-inline-actions" aria-label="Active result filters">
            <span>{[search && `Search: ${search}`, fromDate && `From ${fromDate}`, toDate && `Through ${toDate}`, releaseFilter !== (activeRelease?.id || 'all') && `Release: ${releaseFilter === 'all' ? 'All releases' : releaseFilter}`, statusFilter !== 'all' && `Status: ${statusFilter}`, archiveFilter !== 'all' && `Archive: ${archiveFilter === 'ready' ? 'ready' : 'unavailable'}`].filter(Boolean).join(' · ')}</span>
            <button className="btn btn-secondary btn-sm" type="button" onClick={() => { setSearch(''); setFromDate(''); setToDate(''); setStatusFilter('all'); setArchiveFilter('all'); setReleaseFilter(activeRelease?.id || 'all') }}>Reset result filters</button>
          </div>}
          <p className="setting-desc">Lifecycle elapsed uses recorded start and end times; configured duration is never substituted. Report evidence windows may differ.</p>
          {resultsTruncated && (
            <p className="results-truncation-status" role="status" aria-live="polite">
              Loaded {tests.length.toLocaleString()} of {resultListTotal.toLocaleString()} local results. Older results continue loading in the background.
            </p>
          )}
          {visibleSelectedIds.length === 0 && <p className="setting-desc">Select 2-{MAX_COMPARISON_RUNS} archived local results to compare.</p>}<BulkActions count={visibleSelectedIds.length} className="results-table-selection-toolbar"><strong>{selectionState}</strong>
              <button className="btn btn-secondary" onClick={runSelectedComparison} disabled={!canCompareSelected}>
                Run Comparison
              </button>
              <button className="btn btn-ghost" onClick={() => setSelectedIds(new Set())} disabled={visibleSelectedIds.length === 0}>
                Clear
              </button>
              <button className="btn btn-danger" onClick={deleteSelected} disabled={visibleSelectedIds.length === 0 || selectedDeletePending}>
                Delete Selected ({visibleSelectedIds.length})
              </button>
            </BulkActions>
          {resultsSourcesLoading ? (
            <div className="empty-card results-loading-card" role="status" aria-live="polite" aria-busy="true">
              <span className="results-loading-spinner" aria-hidden="true" />
              <div>
                <p>Loading result sources...</p>
                <p className="empty-hint">Fetching completed runs and library catalog entries for this release.</p>
              </div>
            </div>
          ) : sourceRows.length === 0 ? (
            <div className="empty-card">
              <p>No result sources yet.</p>
              <p className="empty-hint">Run a scenario or pull a remote result for this release to populate the library.</p>
            </div>
          ) : filteredRows.length === 0 ? (
            <div className="empty-card">
              <p>No results match the current filters.</p>
              <p className="empty-hint">Clear the search or filter values to bring the result sources back into view.</p>
            </div>
          ) : (
            <div ref={resultsTableRef} className={`data-table library-source-table-wrap${shouldVirtualizeRows ? ' results-virtual-table-wrap' : ''}`}>
              <table data-table-pagination="off" className="library-source-table results-library-source-table">
                <colgroup>
                  <col className="results-select-col" />
                  <col className="results-source-col" />
                  <col className="results-scenario-col" />
                  <col className="results-status-col" />
                  <col className="results-number-col" />
                  <col className="results-number-col" />
                  <col className="results-started-col" />
                  <col className="results-duration-col" />
                  <col className="results-archive-col" />
                  <col className="results-actions-col" />
                </colgroup>
                <thead>
                  <tr>
                    <th className="results-compare-select-header">
                      <SelectAllCheckbox
                        selectedCount={visibleSelectedIds.length}
                        visibleCount={visibleSelectableIds.length}
                        ariaLabel="Select all matching local results"
                        onChange={toggleAllVisible}
                      />
                    </th>
                    <SortHeader label="Source" field="source" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Scenario" field="name" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Status" field="status" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="VUsers" field="total_vusers" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Groups" field="groups" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Started" field="started" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Lifecycle elapsed" field="duration" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Archive" field="archive" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <th className="results-actions-column">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {shouldVirtualizeRows && virtualPaddingTop > 0 && (
                    <tr aria-hidden="true" className="results-virtual-spacer-row">
                  <td colSpan={10} aria-hidden="true" style={{ height: virtualPaddingTop }} />
                    </tr>
                  )}
                  {shouldVirtualizeRows
                    ? virtualRows.map(virtualRow => renderResultRow(
                      sortedRows[virtualRow.index],
                      rowVirtualizer.measureElement as (element: HTMLTableRowElement | null) => void,
                      virtualRow.index,
                    ))
                    : sortedRows.map(row => renderResultRow(row))}
                  {shouldVirtualizeRows && virtualPaddingBottom > 0 && (
                    <tr aria-hidden="true" className="results-virtual-spacer-row">
                  <td colSpan={10} aria-hidden="true" style={{ height: virtualPaddingBottom }} />
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

export default memo(ResultsPage)
