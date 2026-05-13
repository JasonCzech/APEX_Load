import { useCallback, useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import {
  RunArchiveActionStats,
  RunArchiveReport,
  TestSummary,
  TransactionEvent,
} from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { downloadBlob } from '../utils/fileUtils'

export const ARCHIVE_PAGE_SIZE = 25

interface ExecutionArchiveModel {
  hasArchive: boolean
  archiveError: string
  archiveLoading: boolean
  archiveReport: RunArchiveReport | null
  archiveActionEntries: Array<[string, RunArchiveActionStats]>
  selectedArchiveAction: string
  onSelectedArchiveActionChange: (value: string) => void
  archiveGroups: string[]
  selectedArchiveGroup: string
  onSelectedArchiveGroupChange: (value: string) => void
  archiveErrorsOnly: boolean
  onArchiveErrorsOnlyChange: (value: boolean) => void
  archiveTransactionsLoading: boolean
  archiveTransactionsTotal: number
  archivePageStart: number
  archivePageEnd: number
  archiveTransactionsOffset: number
  archivePageSize: number
  onPreviousPage: () => void
  onNextPage: () => void
  archiveTransactions: TransactionEvent[]
  expandedArchiveEvent: string | null
  onExpandedArchiveEventChange: (value: string | null) => void
  onDownloadArchive: () => Promise<void>
  onDownloadArchiveArtifact: (artifactName: string) => Promise<void>
}

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

function buildArchiveDownloadName(test: TestSummary): string {
  return `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}`
}

export function useExecutionArchiveModel(test: TestSummary | null): ExecutionArchiveModel {
  const api = useApi()
  const [archiveReport, setArchiveReport] = useState<RunArchiveReport | null>(null)
  const [archiveTransactions, setArchiveTransactions] = useState<TransactionEvent[]>([])
  const [archiveTransactionsTotal, setArchiveTransactionsTotal] = useState(0)
  const [archiveLoading, setArchiveLoading] = useState(false)
  const [archiveTransactionsLoading, setArchiveTransactionsLoading] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const [selectedArchiveAction, setSelectedArchiveAction] = useState('')
  const [selectedArchiveGroup, setSelectedArchiveGroup] = useState('')
  const [archiveErrorsOnly, setArchiveErrorsOnly] = useState(false)
  const [archiveTransactionsOffset, setArchiveTransactionsOffset] = useState(0)
  const [expandedArchiveEvent, setExpandedArchiveEvent] = useState<string | null>(null)

  const hasArchive = Boolean(test?.archive)

  useEffect(() => {
    if (!test?.archive || !isTerminalStatus(test.status)) {
      setArchiveReport(null)
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      setArchiveLoading(false)
      setArchiveTransactionsLoading(false)
      setArchiveError('')
      setSelectedArchiveAction('')
      setSelectedArchiveGroup('')
      setArchiveErrorsOnly(false)
      setArchiveTransactionsOffset(0)
      setExpandedArchiveEvent(null)
      return
    }

    let cancelled = false

    const loadArchiveReport = async () => {
      setArchiveLoading(true)
      setArchiveError('')
      try {
        const report = await api.getTestArchiveReport(test.id)
        if (cancelled) return
        const actionNames = Object.keys(report.by_action || {})
        setArchiveReport(report)
        setSelectedArchiveAction((current) => (current && actionNames.includes(current) ? current : actionNames[0] || ''))
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
  }, [api, test])

  useEffect(() => {
    if (!test?.archive || !isTerminalStatus(test.status) || !selectedArchiveAction) {
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      setArchiveTransactionsLoading(false)
      return
    }

    let cancelled = false

    const loadArchiveTransactions = async () => {
      setArchiveTransactionsLoading(true)
      try {
        const response = await api.getTestArchiveTransactions(test.id, {
          action: selectedArchiveAction,
          group: selectedArchiveGroup || undefined,
          success: archiveErrorsOnly ? false : undefined,
          limit: ARCHIVE_PAGE_SIZE,
          offset: archiveTransactionsOffset,
        })
        if (cancelled) return
        setArchiveTransactions(response.transactions || [])
        setArchiveTransactionsTotal(response.total || 0)
        setExpandedArchiveEvent(null)
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
  }, [api, archiveErrorsOnly, archiveTransactionsOffset, selectedArchiveAction, selectedArchiveGroup, test])

  const archiveActionEntries = useMemo(() => {
    if (!archiveReport) return []
    return Object.entries(archiveReport.by_action || {}).sort((a, b) => b[1].transactions - a[1].transactions)
  }, [archiveReport])

  const archiveGroups = useMemo(() => test?.groups?.map((group) => group.name) || [], [test?.groups])

  const archivePageStart = archiveTransactionsTotal === 0 ? 0 : archiveTransactionsOffset + 1
  const archivePageEnd = Math.min(archiveTransactionsOffset + archiveTransactions.length, archiveTransactionsTotal)

  const onSelectedArchiveActionChange = useCallback((value: string) => {
    setSelectedArchiveAction(value)
    setArchiveTransactionsOffset(0)
  }, [])

  const onSelectedArchiveGroupChange = useCallback((value: string) => {
    setSelectedArchiveGroup(value)
    setArchiveTransactionsOffset(0)
  }, [])

  const onArchiveErrorsOnlyChange = useCallback((value: boolean) => {
    setArchiveErrorsOnly(value)
    setArchiveTransactionsOffset(0)
  }, [])

  const onPreviousPage = useCallback(() => {
    setArchiveTransactionsOffset((current) => Math.max(0, current - ARCHIVE_PAGE_SIZE))
  }, [])

  const onNextPage = useCallback(() => {
    setArchiveTransactionsOffset((current) => current + ARCHIVE_PAGE_SIZE)
  }, [])

  const onDownloadArchive = useCallback(async () => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchive(test.id)
      downloadBlob(blob, `${buildArchiveDownloadName(test)}.zip`)
    } catch (error: unknown) {
      setArchiveError(getErrorMessage(error, 'Unable to download archive bundle'))
    }
  }, [api, test])

  const onDownloadArchiveArtifact = useCallback(async (artifactName: string) => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchiveArtifact(test.id, artifactName)
      downloadBlob(blob, `${buildArchiveDownloadName(test)}-${artifactName}`)
    } catch (error: unknown) {
      setArchiveError(getErrorMessage(error, `Unable to download ${artifactName}`))
    }
  }, [api, test])

  return {
    hasArchive,
    archiveError,
    archiveLoading,
    archiveReport,
    archiveActionEntries,
    selectedArchiveAction,
    onSelectedArchiveActionChange,
    archiveGroups,
    selectedArchiveGroup,
    onSelectedArchiveGroupChange,
    archiveErrorsOnly,
    onArchiveErrorsOnlyChange,
    archiveTransactionsLoading,
    archiveTransactionsTotal,
    archivePageStart,
    archivePageEnd,
    archiveTransactionsOffset,
    archivePageSize: ARCHIVE_PAGE_SIZE,
    onPreviousPage,
    onNextPage,
    archiveTransactions,
    expandedArchiveEvent,
    onExpandedArchiveEventChange: setExpandedArchiveEvent,
    onDownloadArchive,
    onDownloadArchiveArtifact,
  }
}