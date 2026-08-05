import { useCallback, useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import {
  RunArchiveAnalysisResponse,
  RunArchiveActionStats,
  RunArchiveReport,
  TestSummary,
  TransactionEvent,
} from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { downloadBlob } from '../utils/fileUtils'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { saveApiResourceViaBridge, startDesktopBrowserDownload } from '../desktop/desktopFiles'
import { hasDesktopApiCapability } from '../hooks/apiClient'

export const ARCHIVE_PAGE_SIZE_DEFAULT = 10
export const ARCHIVE_PAGE_SIZE_OPTIONS = [10, 25, 50] as const

export type ArchiveTransactionSortKey = 'time' | 'status' | 'duration' | 'bytes' | 'error'
export type ArchiveTransactionSortDir = 'asc' | 'desc'
export type ArchiveTransactionSort = {
  key: ArchiveTransactionSortKey
  direction: ArchiveTransactionSortDir
} | null

export type ArchiveTransactionFilterField = keyof ArchiveTransactionFilters

export interface ArchiveTransactionFilters {
  timeFrom: string
  timeTo: string
  status: string
  minDurationMs: string
  maxDurationMs: string
  minBytes: string
  maxBytes: string
  errorContains: string
}

export const DEFAULT_ARCHIVE_TRANSACTION_FILTERS: ArchiveTransactionFilters = {
  timeFrom: '',
  timeTo: '',
  status: '',
  minDurationMs: '',
  maxDurationMs: '',
  minBytes: '',
  maxBytes: '',
  errorContains: '',
}

interface ExecutionArchiveModel {
  hasArchive: boolean
  archiveError: string
  archiveLoading: boolean
  archiveReport: RunArchiveReport | null
  archiveAnalysis: RunArchiveAnalysisResponse | null
  archiveAnalysisLoading: boolean
  archiveAnalysisError: string
  archiveActionEntries: Array<[string, RunArchiveActionStats]>
  selectedArchiveAction: string
  onSelectedArchiveActionChange: (value: string) => void
  archiveSOAPOperationEntries: Array<[string, RunArchiveActionStats]>
  selectedArchiveSOAPOperation: string
  onSelectedArchiveSOAPOperationChange: (value: string) => void
  archiveGroups: string[]
  selectedArchiveGroup: string
  onSelectedArchiveGroupChange: (value: string) => void
  archiveErrorsOnly: boolean
  onArchiveErrorsOnlyChange: (value: boolean) => void
  archiveTransactionSort: ArchiveTransactionSort
  onArchiveTransactionSortChange: (key: ArchiveTransactionSortKey) => void
  archiveTransactionFilters: ArchiveTransactionFilters
  archiveTransactionFiltersActive: boolean
  onArchiveTransactionFilterChange: (field: ArchiveTransactionFilterField, value: string) => void
  onArchiveTransactionFiltersReset: () => void
  archiveTransactionsLoading: boolean
  archiveTransactionsTotal: number
  archivePageStart: number
  archivePageEnd: number
  archiveTransactionsOffset: number
  archivePageSize: number
  onPageSizeChange: (size: number) => void
  onPreviousPage: () => void
  onNextPage: () => void
  archiveTransactions: TransactionEvent[]
  expandedArchiveEvent: string | null
  onExpandedArchiveEventChange: (value: string | null) => void
  onDownloadArchive: () => Promise<void>
  onDownloadBrowserDiagnostics: () => Promise<void>
  onDownloadArchiveArtifact: (artifactName: string) => Promise<void>
}

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

function buildArchiveDownloadName(test: TestSummary): string {
  return `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}`
}

function parseNumberFilter(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : undefined
}

function parseTimeFilter(value: string): string | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = new Date(trimmed)
  if (Number.isNaN(parsed.getTime())) return undefined
  return parsed.toISOString()
}

export function useExecutionArchiveModel(test: TestSummary | null): ExecutionArchiveModel {
  const api = useApi()
  const [archiveReport, setArchiveReport] = useState<RunArchiveReport | null>(null)
  const [archiveAnalysis, setArchiveAnalysis] = useState<RunArchiveAnalysisResponse | null>(null)
  const [archiveTransactions, setArchiveTransactions] = useState<TransactionEvent[]>([])
  const [archiveTransactionsTotal, setArchiveTransactionsTotal] = useState(0)
  const [archiveLoading, setArchiveLoading] = useState(false)
  const [archiveAnalysisLoading, setArchiveAnalysisLoading] = useState(false)
  const [archiveTransactionsLoading, setArchiveTransactionsLoading] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const [archiveAnalysisError, setArchiveAnalysisError] = useState('')
  const [selectedArchiveAction, setSelectedArchiveAction] = useState('')
  const [selectedArchiveSOAPOperation, setSelectedArchiveSOAPOperation] = useState('')
  const [selectedArchiveGroup, setSelectedArchiveGroup] = useState('')
  const [archiveErrorsOnly, setArchiveErrorsOnly] = useState(false)
  const [archiveTransactionSort, setArchiveTransactionSort] = useState<ArchiveTransactionSort>(null)
  const [archiveTransactionFilters, setArchiveTransactionFilters] = useState<ArchiveTransactionFilters>(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
  const [committedArchiveTransactionFilters, setCommittedArchiveTransactionFilters] = useState<ArchiveTransactionFilters>(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
  const [archiveTransactionsOffset, setArchiveTransactionsOffset] = useState(0)
  const [archivePageSize, setArchivePageSize] = useState(ARCHIVE_PAGE_SIZE_DEFAULT)
  const [expandedArchiveEvent, setExpandedArchiveEvent] = useState<string | null>(null)

  const hasArchive = Boolean(test?.archive)
  const testID = test?.id || ''
  const testStatus = test?.status
  const archiveCompletedAt = test?.archive?.completed_at || ''
  const archiveAnalysisBaselineID = test?.comparison_baseline?.source_test_id || ''
  const archiveTransactionQueryParams = useMemo(() => ({
    action: selectedArchiveAction,
    soap_operation: selectedArchiveSOAPOperation || undefined,
    group: selectedArchiveGroup || undefined,
    success: archiveErrorsOnly ? false : undefined,
    time_from: parseTimeFilter(committedArchiveTransactionFilters.timeFrom),
    time_to: parseTimeFilter(committedArchiveTransactionFilters.timeTo),
    status: committedArchiveTransactionFilters.status.trim() || undefined,
    min_duration_ms: parseNumberFilter(committedArchiveTransactionFilters.minDurationMs),
    max_duration_ms: parseNumberFilter(committedArchiveTransactionFilters.maxDurationMs),
    min_bytes: parseNumberFilter(committedArchiveTransactionFilters.minBytes),
    max_bytes: parseNumberFilter(committedArchiveTransactionFilters.maxBytes),
    error_contains: committedArchiveTransactionFilters.errorContains.trim() || undefined,
    sort_by: archiveTransactionSort?.key,
    sort_dir: archiveTransactionSort?.direction,
    limit: archivePageSize,
    offset: archiveTransactionsOffset,
  }), [
    archiveErrorsOnly,
    archivePageSize,
    committedArchiveTransactionFilters,
    archiveTransactionSort,
    archiveTransactionsOffset,
    selectedArchiveAction,
    selectedArchiveGroup,
    selectedArchiveSOAPOperation,
  ])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setCommittedArchiveTransactionFilters(archiveTransactionFilters)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [archiveTransactionFilters])

  useEffect(() => {
    if (!hasArchive || !isTerminalStatus(testStatus)) {
      setArchiveReport(null)
      setArchiveAnalysis(null)
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      setArchiveLoading(false)
      setArchiveAnalysisLoading(false)
      setArchiveTransactionsLoading(false)
      setArchiveError('')
      setArchiveAnalysisError('')
      setSelectedArchiveAction('')
      setSelectedArchiveSOAPOperation('')
      setSelectedArchiveGroup('')
      setArchiveErrorsOnly(false)
      setArchiveTransactionSort(null)
      setArchiveTransactionFilters(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
      setCommittedArchiveTransactionFilters(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
      setArchiveTransactionsOffset(0)
      setExpandedArchiveEvent(null)
      return
    }

    let cancelled = false

    const loadArchiveReport = async () => {
      setExpandedArchiveEvent(null)
      setArchiveLoading(true)
      setArchiveError('')
      try {
        const report = await api.getTestArchiveReport(testID)
        if (cancelled) return
        const actionNames = Object.keys(report.by_action || {})
        const operationNames = Object.keys(report.by_soap_operation || {})
        setArchiveReport(report)
        setSelectedArchiveAction((current) => (current && actionNames.includes(current) ? current : actionNames[0] || ''))
        setSelectedArchiveSOAPOperation((current) => (current && operationNames.includes(current) ? current : ''))
        setArchiveTransactionsOffset(0)
      } catch (error: unknown) {
        if (cancelled) return
        setArchiveReport(null)
        setArchiveError(getErrorMessage(error, 'Unable to load archive report'))
      } finally {
        if (!cancelled) {
          setArchiveLoading(false)
        }
      }
    }

    loadArchiveReport()

    return () => {
      cancelled = true
    }
  }, [api, archiveCompletedAt, hasArchive, testID, testStatus])

  useEffect(() => {
    if (!hasArchive || !isTerminalStatus(testStatus) || !selectedArchiveAction) {
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      setArchiveTransactionsLoading(false)
      return
    }

    let cancelled = false

    const loadArchiveTransactions = async () => {
      setArchiveTransactionsLoading(true)
      try {
        const response = await api.getTestArchiveTransactions(testID, archiveTransactionQueryParams)
        if (cancelled) return
        setArchiveTransactions(response.transactions || [])
        setArchiveTransactionsTotal(response.total || 0)
      } catch (error: unknown) {
        if (cancelled) return
        setArchiveTransactions([])
        setArchiveTransactionsTotal(0)
        setArchiveError(getErrorMessage(error, 'Unable to load archive transactions'))
      } finally {
        if (!cancelled) {
          setArchiveTransactionsLoading(false)
        }
      }
    }

    loadArchiveTransactions()

    return () => {
      cancelled = true
    }
  }, [api, archiveTransactionQueryParams, hasArchive, selectedArchiveAction, testID, testStatus])

  useEffect(() => {
    if (!hasArchive || !isTerminalStatus(testStatus) || !selectedArchiveAction) {
      setArchiveAnalysis(null)
      setArchiveAnalysisLoading(false)
      setArchiveAnalysisError('')
      return
    }

    let cancelled = false

    const loadArchiveAnalysis = async () => {
      const baselineID = archiveAnalysisBaselineID && archiveAnalysisBaselineID !== testID ? archiveAnalysisBaselineID : undefined
      setArchiveAnalysisLoading(true)
      setArchiveAnalysisError('')
      try {
        const response = await api.getTestArchiveAnalysis(testID, {
          ...archiveTransactionQueryParams,
          baseline_id: baselineID,
        })
        if (cancelled) return
        setArchiveAnalysis(response)
      } catch (error: unknown) {
        if (cancelled) return
        if (baselineID) {
          try {
            const response = await api.getTestArchiveAnalysis(testID, archiveTransactionQueryParams)
            if (cancelled) return
            setArchiveAnalysis(response)
            setArchiveAnalysisError(`${getErrorMessage(error, 'Unable to load baseline archive analysis')} Showing current archive only.`)
          } catch (fallbackError: unknown) {
            if (cancelled) return
            setArchiveAnalysis(null)
            setArchiveAnalysisError(getErrorMessage(fallbackError, 'Unable to load archive analysis'))
          }
          return
        }
        setArchiveAnalysis(null)
        setArchiveAnalysisError(getErrorMessage(error, 'Unable to load archive analysis'))
      } finally {
        if (!cancelled) {
          setArchiveAnalysisLoading(false)
        }
      }
    }

    loadArchiveAnalysis()

    return () => {
      cancelled = true
    }
  }, [api, archiveAnalysisBaselineID, archiveTransactionQueryParams, hasArchive, selectedArchiveAction, testID, testStatus])

  const archiveActionEntries = useMemo(() => {
    if (!archiveReport) return []
    return Object.entries(archiveReport.by_action || {}).sort((a, b) => b[1].transactions - a[1].transactions)
  }, [archiveReport])

  const archiveSOAPOperationEntries = useMemo(() => {
    if (!archiveReport) return []
    return Object.entries(archiveReport.by_soap_operation || {}).sort((a, b) => b[1].transactions - a[1].transactions)
  }, [archiveReport])

  const archiveGroups = useMemo(() => test?.groups?.map((group) => group.name) || [], [test?.groups])

  const archivePageStart = archiveTransactionsTotal === 0 ? 0 : archiveTransactionsOffset + 1
  const archivePageEnd = Math.min(archiveTransactionsOffset + archiveTransactions.length, archiveTransactionsTotal)
  const archiveTransactionFiltersActive = Object.values(archiveTransactionFilters).some(value => value.trim() !== '')

  const onSelectedArchiveActionChange = useCallback((value: string) => {
    setSelectedArchiveAction(value)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onSelectedArchiveSOAPOperationChange = useCallback((value: string) => {
    setSelectedArchiveSOAPOperation(value)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onSelectedArchiveGroupChange = useCallback((value: string) => {
    setSelectedArchiveGroup(value)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onArchiveErrorsOnlyChange = useCallback((value: boolean) => {
    setArchiveErrorsOnly(value)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onArchiveTransactionSortChange = useCallback((key: ArchiveTransactionSortKey) => {
    setArchiveTransactionSort(current => {
      if (current?.key === key) {
        return { key, direction: current.direction === 'asc' ? 'desc' : 'asc' }
      }
      return { key, direction: key === 'time' ? 'desc' : 'asc' }
    })
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onArchiveTransactionFilterChange = useCallback((field: ArchiveTransactionFilterField, value: string) => {
    setArchiveTransactionFilters(current => ({
      ...current,
      [field]: value,
    }))
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onArchiveTransactionFiltersReset = useCallback(() => {
    setArchiveTransactionFilters(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
    setCommittedArchiveTransactionFilters(DEFAULT_ARCHIVE_TRANSACTION_FILTERS)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onPageSizeChange = useCallback((size: number) => {
    setArchivePageSize(size)
    setArchiveTransactionsOffset(0)
    setExpandedArchiveEvent(null)
  }, [])

  const onPreviousPage = useCallback(() => {
    setArchiveTransactionsOffset((current) => Math.max(0, current - archivePageSize))
    setExpandedArchiveEvent(null)
  }, [archivePageSize])

  const onNextPage = useCallback(() => {
    setArchiveTransactionsOffset((current) => current + archivePageSize)
    setExpandedArchiveEvent(null)
  }, [archivePageSize])

  const onDownloadArchive = useCallback(async () => {
    if (!test) return
    try {
      const filename = `${buildArchiveDownloadName(test)}.zip`
      const resourcePath = `/api/v1/tests/${encodeURIComponent(test.id)}/archive`
      const nativeOutcome = await saveApiResourceViaBridge(resourcePath, filename)
      if (nativeOutcome !== 'unavailable') return
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        await startDesktopBrowserDownload(resourcePath, filename)
        return
      }
      const blob = await api.downloadTestArchive(test.id)
      await downloadBlob(blob, filename)
    } catch (error: unknown) {
      setArchiveError(getErrorMessage(error, 'Unable to download archive bundle'))
    }
  }, [api, test])

  const onDownloadBrowserDiagnostics = useCallback(async () => {
    if (!test) return
    try {
      const filename = `${buildArchiveDownloadName(test)}-browser-diagnostics.zip`
      const resourcePath = `/api/v1/tests/${encodeURIComponent(test.id)}/archive/browser-diagnostics`
      const nativeOutcome = await saveApiResourceViaBridge(resourcePath, filename)
      if (nativeOutcome !== 'unavailable') return
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        await startDesktopBrowserDownload(resourcePath, filename)
        return
      }
      const blob = await api.downloadTestArchiveBrowserDiagnostics(test.id)
      await downloadBlob(blob, filename)
    } catch (error: unknown) {
      setArchiveError(getErrorMessage(error, 'Unable to download browser diagnostics bundle'))
    }
  }, [api, test])

  const onDownloadArchiveArtifact = useCallback(async (artifactName: string) => {
    if (!test) return
    try {
      const filename = `${buildArchiveDownloadName(test)}-${artifactName}`
      const resourcePath = `/api/v1/tests/${encodeURIComponent(test.id)}/archive/files/${encodeURIComponent(artifactName)}`
      const nativeOutcome = await saveApiResourceViaBridge(resourcePath, filename)
      if (nativeOutcome !== 'unavailable') return
      if (!isDesktopSurface() && hasDesktopApiCapability()) {
        await startDesktopBrowserDownload(resourcePath, filename)
        return
      }
      const blob = await api.downloadTestArchiveArtifact(test.id, artifactName)
      await downloadBlob(blob, filename)
    } catch (error: unknown) {
      setArchiveError(getErrorMessage(error, `Unable to download ${artifactName}`))
    }
  }, [api, test])

  return {
    hasArchive,
    archiveError,
    archiveLoading,
    archiveReport,
    archiveAnalysis,
    archiveAnalysisLoading,
    archiveAnalysisError,
    archiveActionEntries,
    selectedArchiveAction,
    onSelectedArchiveActionChange,
    archiveSOAPOperationEntries,
    selectedArchiveSOAPOperation,
    onSelectedArchiveSOAPOperationChange,
    archiveGroups,
    selectedArchiveGroup,
    onSelectedArchiveGroupChange,
    archiveErrorsOnly,
    onArchiveErrorsOnlyChange,
    archiveTransactionSort,
    onArchiveTransactionSortChange,
    archiveTransactionFilters,
    archiveTransactionFiltersActive,
    onArchiveTransactionFilterChange,
    onArchiveTransactionFiltersReset,
    archiveTransactionsLoading,
    archiveTransactionsTotal,
    archivePageStart,
    archivePageEnd,
    archiveTransactionsOffset,
    archivePageSize,
    onPageSizeChange,
    onPreviousPage,
    onNextPage,
    archiveTransactions,
    expandedArchiveEvent,
    onExpandedArchiveEventChange: setExpandedArchiveEvent,
    onDownloadArchive,
    onDownloadBrowserDiagnostics,
    onDownloadArchiveArtifact,
  }
}
