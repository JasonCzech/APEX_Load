import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { downloadBlob } from '../utils/fileUtils'
import { isCompletedTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { Application, BrowserDeviceLiveMetrics, BrowserEngineLiveMetrics, BrowserPlatformLiveMetrics, IntervalSummaryRecord, Release, RunArchiveActionStats, RunArchiveReport, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import BrowserBreakdownChart from './BrowserBreakdownChart'
import { SortHeader } from './SortHeader'
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

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onOpenTestArchive?: (id: string) => void
  onNavigate?: (page: NavPage) => void
  activeApplication?: Application | null
  activeRelease?: Release | null
}

type SortField = 'source' | 'name' | 'status' | 'total_vusers' | 'groups' | 'started' | 'duration' | 'archive'
type StatusFilter = 'all' | 'complete' | 'failed' | 'aborted'
type ArchiveFilter = 'all' | 'ready' | 'pending'
type ComparisonBreakdown = BrowserBreakdown
const MAX_COMPARISON_RUNS = 10
const ALL_COMPARISON_ACTIONS = 'all'

type ResultSourceRow = {
  rowKey: string
  source: LibraryAssetSourceKind
  syncStatus: string
  name: string
  status: string
  startedAt?: string
  duration?: string
  releaseId?: string
  releaseName?: string
  hasArchive: boolean
  archiveCount?: number
  totalVusers?: number
  groupCount?: number
  test?: TestSummary
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
    release_id: test.release_id,
    status: test.status,
    execution_run_date: test.start_time || test.started_at || test.created_at,
    updated_at: test.ended_at || test.created_at,
    has_archive: Boolean(test.archive),
  }
}

function resultReleaseValue(row: ResultSourceRow) {
  return row.releaseId || row.releaseName || '__unassigned__'
}

function resultReleaseLabel(row: ResultSourceRow) {
  return row.releaseName || row.releaseId || 'Unassigned'
}

function normalizedReleaseKey(value?: string) {
  return (value || '').trim().toLowerCase()
}

function rowMatchesReleaseFilter(row: ResultSourceRow, releaseFilter: string, activeRelease?: Release | null) {
  if (releaseFilter === 'all') return true
  if (resultReleaseValue(row) === releaseFilter || resultReleaseLabel(row) === releaseFilter) return true
  if (releaseFilter === activeRelease?.id) {
    return normalizedReleaseKey(row.releaseName) === normalizedReleaseKey(activeRelease.name)
  }
  return false
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

function peakComparisonTPS(summaries: IntervalSummaryRecord[]) {
  return summaries.reduce((peak, summary) => Math.max(peak, summary.tps || 0), 0)
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

function ResultsPage({ testStatuses, onSelectTest, onOpenCommandCenter, onOpenTestArchive, onNavigate, activeApplication, activeRelease }: Props) {
  const api = useApi()
  const testListParams = useMemo(() => activeApplication?.id && activeRelease?.id ? {
    project_id: activeApplication.project_id,
    application_id: activeApplication.id,
    release_id: activeRelease.id,
    summary: true,
    limit: 1000,
  } : null, [activeApplication?.id, activeApplication?.project_id, activeRelease?.id])
  const {
    tests,
    count: resultListCount,
    total: resultListTotal,
    offset: resultListOffset,
    error: listError,
    loading: resultsLoading,
    refreshing: resultsRefreshing,
    refresh,
  } = useSharedTestList(testListParams, { loader: api.listTests, pollIntervalMs: 10000 })
  const resultsTruncated = resultListOffset + resultListCount < resultListTotal
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
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
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [archiveFilter, setArchiveFilter] = useState<ArchiveFilter>('all')
  const [releaseFilter, setReleaseFilter] = useState('all')
  const error = operationError || listError
  const openRemoteSettings = useCallback(() => {
    try { window.localStorage.setItem('apexload.settings.focusSection', 'integrations') } catch { /* storage unavailable */ }
    onNavigate?.('settings')
  }, [onNavigate])

  const libraryProjectId = activeRelease?.project_id || activeApplication?.project_id
  const federation = useLibraryFederation({ kind: 'result', projectId: libraryProjectId, onRefreshLocal: () => refresh() })
  const {
    sourceFilter,
    localItems,
    remoteItems,
    actionBlocked,
    actionBlockedReason,
    pullRemoteItem,
    pushLocalItem,
    compareItems,
    hostItemForLocal,
    syncStatusForLocal,
    remoteItemHref,
  } = federation

  useEffect(() => {
    setSelectedIds(new Set())
    setComparisonTestIds([])
    setComparisonTestSnapshots([])
    setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    setReleaseFilter(activeRelease?.id || 'all')
  }, [activeRelease?.id])

  const completed = useMemo(
    () =>
      tests.filter(test => {
        const status = resolveStatus(test, testStatuses)
        return isCompletedStatus(status)
      }),
    [tests, testStatuses],
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
          duration: test.duration,
          releaseId: test.release_id,
          releaseName: item.release_name || (test.release_id === activeRelease?.id ? activeRelease?.name : undefined),
          hasArchive: Boolean(test.archive),
          archiveCount: test.archive?.transaction_count,
          totalVusers: test.total_vusers,
          groupCount: test.groups?.length || 1,
          test,
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
            releaseId: item.release_id,
            releaseName: item.release_name,
            hasArchive: Boolean(item.has_archive),
            item,
          })
        })
    }

    return rows
  }, [activeRelease?.id, activeRelease?.name, completed, localCatalogById, remoteItems, sourceFilter, syncStatusForLocal, testStatuses])

  const releaseOptions = useMemo(() => {
    const options = new Map<string, string>()
    sourceRows.forEach(row => {
      options.set(resultReleaseValue(row), resultReleaseLabel(row))
    })
    if (activeRelease?.id) {
      options.set(activeRelease.id, activeRelease.name)
    }
    return Array.from(options.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((left, right) => left.label.localeCompare(right.label))
  }, [activeRelease?.id, activeRelease?.name, sourceRows])

  const filteredRows = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase()
    return sourceRows.filter(row => {
      if (!rowMatchesReleaseFilter(row, releaseFilter, activeRelease)) return false
      if (statusFilter !== 'all' && row.status.toLowerCase() !== statusFilter) return false
      if (archiveFilter === 'ready' && !row.hasArchive) return false
      if (archiveFilter === 'pending' && row.hasArchive) return false
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
  }, [activeRelease, archiveFilter, releaseFilter, search, sourceRows, statusFilter])

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
          aValue = a.duration || ''
          bValue = b.duration || ''
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

  const selectedTests = useMemo(
    () => sortedRows.flatMap(row => (row.test && selectedIds.has(row.test.id) ? [row.test] : [])),
    [selectedIds, sortedRows],
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
          comparisonTests.map(async test => [test.id, ((await api.getTestArchiveSummaries(test.id, { limit: 120, offset: 0, action: comparisonSummaryAction })).summaries || []) as IntervalSummaryRecord[]] as const),
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
  }, [api, comparisonSummaryAction, comparisonTestIdKey, comparisonTests])

  const comparisonCards = useMemo(() => {
    if (comparisonTests.length < 2 || comparisonTests.length > MAX_COMPARISON_RUNS) return []
    return comparisonTests.map(test => ({ test, report: comparisonReports[test.id] }))
  }, [comparisonReports, comparisonTests])

  const comparisonActionOptions = useMemo(() => {
    if (comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) return []
    const actionSets = comparisonCards.map(({ report }) => new Set(Object.keys(report?.by_action || {}).filter(Boolean)))
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
        downloadBlob(blob, `archive-comparison-${current.test.id}-${baseline.test.id}-${comparisonBreakdown}${actionFilename}.csv`)
      } else {
        const comparisonIds = comparisonRuns.map(item => item.test.id)
        const blob = action
          ? await api.downloadTestArchiveMultiComparison(baseline.test.id, comparisonIds, comparisonBreakdown, action)
          : await api.downloadTestArchiveMultiComparison(baseline.test.id, comparisonIds, comparisonBreakdown)
        downloadBlob(blob, `archive-comparison-${baseline.test.id}-${comparisonCards.length}-runs-${comparisonBreakdown}${actionFilename}.csv`)
      }
      setComparisonError('')
    } catch (error: unknown) {
      setComparisonError(getErrorMessage(error, 'Unable to export comparison CSV'))
    }
  }, [api, comparisonActionFilter, comparisonActionFiltered, comparisonBreakdown, comparisonCards])

  const comparisonActionRows = useMemo(() => {
    if (comparisonCards.length < 2 || !comparisonCards.every(item => item.report)) return []
    const [baseline] = comparisonCards
    const baselineActions = baseline.report?.by_action || {}
    const actionNames = new Set<string>()
    if (comparisonActionFiltered) {
      actionNames.add(comparisonActionFilter)
    } else {
      comparisonCards.forEach(({ report }) => {
        Object.keys(report?.by_action || {}).forEach(name => actionNames.add(name))
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
          const stats = card.report?.by_action?.[name] as RunArchiveActionStats | undefined
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
    if (!confirm('Delete this scenario result?')) return
    await api.deleteTest(id)
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
    await refresh()
  }

  const deleteSelected = async () => {
    if (!confirm(`Delete ${selectedIds.size} selected results?`)) return
    await Promise.all(Array.from(selectedIds).map(id => api.deleteTest(id)))
    if (Array.from(selectedIds).some(id => comparisonTestIds.includes(id))) {
      setComparisonTestIds([])
      setComparisonTestSnapshots([])
      setComparisonActionFilter(ALL_COMPARISON_ACTIONS)
    }
    setSelectedIds(new Set())
    await refresh()
  }

  const showResultsLoadStatus = resultsLoading || resultsRefreshing || comparisonCards.length >= 2
  const showComparisonLoadedHint = comparisonCards.length >= 2
  const showDeleteSelectedAction = selectedIds.size > 0
  const showResultsActions = showResultsLoadStatus || showComparisonLoadedHint || showDeleteSelectedAction

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

  const renderResultRow = (row: ResultSourceRow, measureElement?: (element: HTMLTableRowElement | null) => void) => {
    const test = row.test
    const item = row.item
    const hostItem = test ? hostItemForLocal(test.id) : undefined
    const href = row.source === 'Remote' && item ? remoteItemHref(item) : ''
    const localItemID = item?.local_item_id
    const transferBlockedByConflict = row.syncStatus === 'conflict'
    const transferBlockTitle = transferBlockedByConflict ? 'Compare this conflict before transferring.' : actionBlockedReason
    return (
      <tr
        key={row.rowKey}
        ref={measureElement}
        className={`${test ? 'clickable-row' : 'no-hover'} ${test && selectedIds.has(test.id) ? 'selected' : ''}`}
        onClick={test ? () => onSelectTest(test.id) : undefined}
      >
        <td className="results-compare-select-cell">
          {test ? (
            <input
              aria-label={`Select ${row.name} for comparison`}
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
            <StatusBadge status={row.status} />
          ) : (
            <span className={`script-health-token ${resultSourceStatusTone(row.status)}`}>{row.status}</span>
          )}
        </td>
        <td>{row.totalVusers ?? '-'}</td>
        <td>{row.groupCount ?? '-'}</td>
        <td className="cell-muted">{row.startedAt ? formatDate(row.startedAt) : '-'}</td>
        <td className="cell-muted">{row.duration || '-'}</td>
        <td className="cell-muted">
          {test ? (test.archive ? `${test.archive.transaction_count} tx` : 'Pending') : (row.hasArchive ? 'Ready' : '-')}
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
                <button className="btn btn-sm btn-ghost" onClick={event => toggleBaselinePin(test, event)}>
                  {test.baseline_pinned ? 'Unpin Baseline' : 'Pin Baseline'}
                </button>
                {test.archive && onOpenTestArchive && (
                  <button className="btn btn-sm btn-secondary" onClick={event => { event.stopPropagation(); onOpenTestArchive(test.id) }}>
                    Run Archive
                  </button>
                )}
                <button className="btn btn-sm btn-primary" onClick={event => { event.stopPropagation(); (onOpenCommandCenter ?? onSelectTest)(test.id) }}>
                  Run Dashboard
                </button>
                <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={event => handleDelete(test.id, event)}>
                  Delete
                </button>
                <button
                  className="btn btn-sm btn-secondary"
                  type="button"
                  onClick={event => { event.stopPropagation(); void pushLocalItem(item ?? catalogItemForTest(test)) }}
                  disabled={actionBlocked || transferBlockedByConflict}
                  title={transferBlockTitle}
                >
                  Push
                </button>
                {hostItem?.local_item_id && (
                  <button className="btn btn-sm btn-ghost" type="button" onClick={event => { event.stopPropagation(); void compareItems(test.id, hostItem) }}>
                    Compare
                  </button>
                )}
              </>
            ) : item ? (
              <>
                <button
                  className="btn btn-sm btn-secondary"
                  type="button"
                  onClick={event => { event.stopPropagation(); void pullRemoteItem(item) }}
                  disabled={actionBlocked || transferBlockedByConflict}
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
              <span className={`results-load-status${resultsLoading || resultsRefreshing ? '' : ' idle'}`} aria-live="polite">
                {resultsLoading ? 'Loading results...' : resultsRefreshing ? 'Refreshing...' : 'Results refreshed'}
              </span>
            )}
            {showComparisonLoadedHint && (
              <span className="results-compare-hint">Comparison loaded</span>
            )}
            {showDeleteSelectedAction && (
              <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} onClick={deleteSelected}>
                Delete Selected ({selectedIds.size})
              </button>
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
                <p className="page-subtitle">Compare 2-{MAX_COMPARISON_RUNS} archived runs. The oldest run is the baseline.</p>
              </div>
              {comparisonCards.length >= 2 && comparisonCards.every(item => item.report) && (
                <div className="results-compare-controls">
                  <label className="results-compare-filter">
                    <span>Transaction</span>
                    <select
                      className="settings-input library-source-select"
                      value={comparisonActionFilter}
                      onChange={event => setComparisonActionFilter(event.target.value)}
                    >
                      <option value={ALL_COMPARISON_ACTIONS}>All transactions</option>
                      {comparisonActionOptions.map(action => (
                        <option key={action} value={action}>{action}</option>
                      ))}
                    </select>
                  </label>
                  {comparisonHasBrowserBreakdowns && (
                    <div className="results-breakdown-toggle" role="tablist" aria-label="Comparison breakdown mode">
                      {(['overall', 'platform', 'engine', 'device'] as ComparisonBreakdown[]).map(mode => (
                        <button
                          key={mode}
                          type="button"
                          role="tab"
                          aria-selected={comparisonBreakdown === mode}
                          className={`results-breakdown-button${comparisonBreakdown === mode ? ' active' : ''}`}
                          onClick={() => setComparisonBreakdown(mode)}
                        >
                          {mode === 'overall' ? 'Overall' : mode === 'platform' ? 'Platform' : mode === 'engine' ? 'Engine' : 'Device'}
                        </button>
                      ))}
                    </div>
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
                        const actionStats = comparisonActionFiltered ? report?.by_action?.[comparisonActionFilter] : undefined
                        const runSummaries = comparisonSummaries[test.id] || []
                        const cardTransactions = comparisonActionFiltered ? actionStats?.transactions ?? 0 : report!.overview.transactions
                        const cardAvgResponseMs = comparisonActionFiltered ? actionStats?.avg_response_ms ?? 0 : report!.overview.avg_response_ms
                        const cardErrorPct = comparisonActionFiltered ? actionStats?.error_pct ?? 0 : report!.overview.error_pct
                        const cardPeakTPS = comparisonActionFiltered ? peakComparisonTPS(runSummaries) : report!.overview.peak_tps

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
                                <StatusBadge status={resolveStatus(test, testStatuses)} />
                              </div>
                            </div>
                            <div className="results-compare-metrics">
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Transactions</span>
                                <span className="results-compare-value">{cardTransactions}</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Avg Latency</span>
                                <span className="results-compare-value">{cardAvgResponseMs.toFixed(1)} ms</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Error Rate</span>
                                <span className="results-compare-value">{cardErrorPct.toFixed(2)}%</span>
                              </div>
                              <div className="results-compare-metric">
                                <span className="results-compare-label">Peak TPS</span>
                                <span className="results-compare-value">{cardPeakTPS.toFixed(1)}</span>
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
                            Showing {comparisonChartMetricConfig.description}; color identifies the run.
                          </p>
                        </div>
                        <div className="results-compare-metric-tabs" role="tablist" aria-label="Comparison metric">
                          {COMPARISON_CHART_METRICS.map(metric => (
                            <button
                              key={metric.id}
                              type="button"
                              role="tab"
                              aria-selected={comparisonChartMetric === metric.id}
                              className={`results-compare-metric-tab${comparisonChartMetric === metric.id ? ' active' : ''}`}
                              onClick={() => setComparisonChartMetric(metric.id)}
                            >
                              {metric.label}
                            </button>
                          ))}
                        </div>
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
            federation={federation}
            onConfigureRemotes={openRemoteSettings}
            filterControls={(
              <>
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
                  <span>Search Release</span>
                  <input
                    className="settings-input"
                    type="search"
                    value={search}
                    onChange={event => setSearch(event.target.value)}
                    placeholder="Find releases or results..."
                  />
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
                    <option value="pending">Archive pending</option>
                  </select>
                </label>
              </>
            )}
            summaryText={resultsLoading
              ? 'Loading local and remote result sources...'
              : `${filteredRows.length} result source${filteredRows.length === 1 ? '' : 's'} for ${activeRelease.name}.`}
          />
          {resultsTruncated && (
            <p className="results-truncation-status" role="status" aria-live="polite">
              Showing the newest {resultListCount.toLocaleString()} of {resultListTotal.toLocaleString()} local results. Search and filters apply to this loaded set.
            </p>
          )}
          <div className="results-table-selection-toolbar">
            <div className="results-table-selection-summary">
              <span>{selectedIds.size} selected</span>
              <strong>{selectionState || `Select 2-${MAX_COMPARISON_RUNS} archived local results to compare`}</strong>
            </div>
            <div className="results-table-selection-actions">
              <button className="btn btn-secondary" onClick={runSelectedComparison} disabled={!canCompareSelected}>
                Run Comparison
              </button>
              <button className="btn btn-ghost" onClick={() => setSelectedIds(new Set())} disabled={selectedIds.size === 0}>
                Clear
              </button>
            </div>
          </div>
          {resultsLoading ? (
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
              <table className="library-source-table results-library-source-table">
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
                      <span>Compare</span>
                    </th>
                    <SortHeader label="Source" field="source" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Scenario" field="name" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Status" field="status" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="VUsers" field="total_vusers" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Groups" field="groups" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Started" field="started" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <SortHeader label="Duration" field="duration" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} numeric />
                    <SortHeader label="Archive" field="archive" sort={{ field: sortField, direction: sortDirection }} onSort={handleSort} />
                    <th className="results-actions-column">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {shouldVirtualizeRows && virtualPaddingTop > 0 && (
                    <tr aria-hidden="true" className="results-virtual-spacer-row">
                      <td colSpan={10} style={{ height: virtualPaddingTop }} />
                    </tr>
                  )}
                  {shouldVirtualizeRows
                    ? virtualRows.map(virtualRow => renderResultRow(
                      sortedRows[virtualRow.index],
                      rowVirtualizer.measureElement as (element: HTMLTableRowElement | null) => void,
                    ))
                    : sortedRows.map(row => renderResultRow(row))}
                  {shouldVirtualizeRows && virtualPaddingBottom > 0 && (
                    <tr aria-hidden="true" className="results-virtual-spacer-row">
                      <td colSpan={10} style={{ height: virtualPaddingBottom }} />
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
