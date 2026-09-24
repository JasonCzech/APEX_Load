// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import { saveApiResourceViaBridge, startDesktopBrowserDownload } from '../desktop/desktopFiles'
import { setDesktopApiCapability } from '../hooks/apiClient'
import { downloadBlob } from '../utils/fileUtils'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getTestArchiveReport: vi.fn(),
  getTestArchiveTransactions: vi.fn(),
  getTestArchiveAnalysis: vi.fn(),
  downloadTestArchive: vi.fn(),
  downloadTestArchiveBrowserDiagnostics: vi.fn(),
  downloadTestArchiveArtifact: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

vi.mock('../desktop/desktopFiles', () => ({
  saveApiResourceViaBridge: vi.fn(),
  startDesktopBrowserDownload: vi.fn(),
}))

function ArchiveHarness({ test, baselineId }: { test: TestSummary | null; baselineId?: string }) {
  const model = useExecutionArchiveModel(test, true, baselineId)

  return (
    <div>
      <span data-testid="loading">{model.archiveLoading ? 'loading' : 'idle'}</span>
      <span data-testid="action">{model.selectedArchiveAction || 'none'}</span>
      <span data-testid="soap-operation">{model.selectedArchiveSOAPOperation || 'all'}</span>
      <span data-testid="group">{model.selectedArchiveGroup || 'all'}</span>
      <span data-testid="offset">{String(model.archiveTransactionsOffset)}</span>
      <span data-testid="rows">{String(model.archiveTransactions.length)}</span>
      <span data-testid="analysis-loading">{model.archiveAnalysisLoading ? 'loading' : 'idle'}</span>
      <span data-testid="analysis-nodes">{String(model.archiveAnalysis?.graph.nodes.length ?? 0)}</span>
      <span data-testid="analysis-error">{model.archiveAnalysisError || 'none'}</span>
      <span data-testid="page-start">{String(model.archivePageStart)}</span>
      <span data-testid="page-end">{String(model.archivePageEnd)}</span>
      <span data-testid="expanded">{model.expandedArchiveEvent || 'none'}</span>
      <span data-testid="sort">{model.archiveTransactionSort ? `${model.archiveTransactionSort.key}:${model.archiveTransactionSort.direction}` : 'none'}</span>
      <span data-testid="status-filter">{model.archiveTransactionFilters.status || 'none'}</span>
      <span data-testid="error">{model.archiveError || 'none'}</span>
      <button data-testid="next" onClick={() => model.onNextPage()}>next</button>
      <button data-testid="inspect" onClick={() => model.onExpandedArchiveEventChange('row-1')}>inspect</button>
      <button data-testid="set-group" onClick={() => model.onSelectedArchiveGroupChange('Checkout')}>group</button>
      <button data-testid="sort-duration" onClick={() => model.onArchiveTransactionSortChange('duration')}>sort duration</button>
      <button data-testid="filter-status" onClick={() => model.onArchiveTransactionFilterChange('status', '500')}>filter status</button>
      <button data-testid="clear-filters" onClick={() => model.onArchiveTransactionFiltersReset()}>clear filters</button>
    </div>
  )
}

describe('useExecutionArchiveModel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getTestArchiveAnalysis.mockResolvedValue(buildArchiveAnalysisResponse())
    apiMock.getTestArchiveReport.mockResolvedValue(buildEmptyArchiveReport())
    vi.mocked(saveApiResourceViaBridge).mockResolvedValue('unavailable')
    vi.mocked(startDesktopBrowserDownload).mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    setDesktopApiCapability('')
    vi.clearAllMocks()
  })

  it('loads archive report, auto-selects the first action, and fetches transactions', async () => {
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 3,
        resource_sample_count: 1,
        interval_summary_count: 1,
      },
      overview: {
        transactions: 3,
        errors: 1,
        error_pct: 33.3,
        avg_response_ms: 120,
        min_response_ms: 80,
        max_response_ms: 220,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 15,
        peak_active_vusers: 10,
      },
      by_action: {
        Login: {
          transactions: 3,
          errors: 1,
          error_pct: 33.3,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
        Browse: {
          transactions: 2,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 90,
          min_response_ms: 75,
          max_response_ms: 110,
        },
      },
      by_soap_operation: {
        CreateOrder: {
          transactions: 2,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 100,
          min_response_ms: 80,
          max_response_ms: 120,
        },
      },
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 200,
        net_sent_kbps: 11,
        net_recv_kbps: 12,
        disk_read_kbps: 13,
        disk_write_kbps: 14,
      },
    })
    apiMock.getTestArchiveTransactions.mockResolvedValue({
      transactions: [
        {
          timestamp: '2026-05-12T20:05:00Z',
          test_id: 'test-1',
          test_name: 'Checkout',
          group: 'Web',
          action_name: 'Login',
          duration_ms: 123,
          success: true,
        },
      ],
      total: 3,
      count: 1,
      limit: 10,
      offset: 0,
    })

    await act(async () => {
      root.render(<ArchiveHarness test={buildArchivedTest()} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveReport).toHaveBeenCalledWith('test-1')
    expect(apiMock.getTestArchiveTransactions).toHaveBeenCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 0,
    })
    expect(apiMock.getTestArchiveAnalysis).toHaveBeenCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 0,
      baseline_id: undefined,
    })
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('analysis-loading')).toBe('idle')
    expect(readTestId('action')).toBe('Login')
    expect(readTestId('rows')).toBe('1')
    expect(readTestId('analysis-nodes')).toBe('1')
    expect(readTestId('page-start')).toBe('1')
    expect(readTestId('page-end')).toBe('1')
  })

  it('uses an explicit comparison baseline instead of the pinned baseline, including clearing it', async () => {
    const test = { ...buildArchivedTest(), comparison_baseline: { source_test_id: 'pinned', source_test_name: 'Pinned' } as TestSummary['comparison_baseline'] }
    apiMock.getTestArchiveReport.mockResolvedValue({ ...buildEmptyArchiveReport(), by_action: { Login: { transactions: 1, errors: 0, error_pct: 0, avg_response_ms: 10, min_response_ms: 10, max_response_ms: 10 } } })
    apiMock.getTestArchiveTransactions.mockResolvedValue({ transactions: [], total: 0 })
    await act(async () => { root.render(<ArchiveHarness test={test} baselineId="chosen" />) })
    await flushPromises()
    expect(apiMock.getTestArchiveAnalysis).toHaveBeenLastCalledWith(test.id, expect.objectContaining({ baseline_id: 'chosen' }))
    await act(async () => { container.querySelector<HTMLButtonElement>('[data-testid="set-group"]')?.click() })
    await flushPromises()
    expect(apiMock.getTestArchiveAnalysis).toHaveBeenLastCalledWith(test.id, expect.objectContaining({ baseline_id: 'chosen', group: 'Checkout', action: 'Login' }))
    await act(async () => { root.render(<ArchiveHarness test={test} baselineId="" />) })
    await flushPromises()
    expect(apiMock.getTestArchiveAnalysis).toHaveBeenLastCalledWith(test.id, expect.objectContaining({ baseline_id: undefined }))
  })

  it('falls back to current-run archive analysis when a baseline archive cannot be read', async () => {
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 3,
        resource_sample_count: 1,
        interval_summary_count: 1,
      },
      overview: {
        transactions: 3,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 120,
        min_response_ms: 80,
        max_response_ms: 220,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 15,
        peak_active_vusers: 10,
      },
      by_action: {
        Login: {
          transactions: 3,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
      },
      by_soap_operation: {},
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 200,
        net_sent_kbps: 11,
        net_recv_kbps: 12,
        disk_read_kbps: 13,
        disk_write_kbps: 14,
      },
    })
    apiMock.getTestArchiveTransactions.mockResolvedValue({
      transactions: [],
      total: 0,
      count: 0,
      limit: 10,
      offset: 0,
    })
    apiMock.getTestArchiveAnalysis
      .mockRejectedValueOnce(new Error('baseline archive not found'))
      .mockResolvedValueOnce(buildArchiveAnalysisResponse())

    await act(async () => {
      root.render(<ArchiveHarness test={{
        ...buildArchivedTest(),
        comparison_baseline: {
          source_test_id: 'baseline-1',
          source_test_name: 'Baseline',
        },
      }} />)
    })
    await flushPromises()

    expect(apiMock.getTestArchiveAnalysis).toHaveBeenNthCalledWith(1, 'test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 0,
      baseline_id: 'baseline-1',
    })
    expect(apiMock.getTestArchiveAnalysis).toHaveBeenNthCalledWith(2, 'test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 0,
    })
    expect(readTestId('analysis-nodes')).toBe('1')
    expect(readTestId('analysis-error')).toContain('Showing current archive only')
  })

  it('keeps an expanded transaction inspection open across test object refreshes', async () => {
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 3,
        resource_sample_count: 1,
        interval_summary_count: 1,
      },
      overview: {
        transactions: 3,
        errors: 1,
        error_pct: 33.3,
        avg_response_ms: 120,
        min_response_ms: 80,
        max_response_ms: 220,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 15,
        peak_active_vusers: 10,
      },
      by_action: {
        Login: {
          transactions: 3,
          errors: 1,
          error_pct: 33.3,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
      },
      by_soap_operation: {},
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 200,
        net_sent_kbps: 11,
        net_recv_kbps: 12,
        disk_read_kbps: 13,
        disk_write_kbps: 14,
      },
    })
    apiMock.getTestArchiveTransactions.mockResolvedValue({
      transactions: [
        {
          timestamp: '2026-05-12T20:05:00Z',
          test_id: 'test-1',
          test_name: 'Checkout',
          group: 'Web',
          action_name: 'Login',
          duration_ms: 123,
          success: true,
        },
      ],
      total: 3,
      count: 1,
      limit: 10,
      offset: 0,
    })

    await act(async () => {
      root.render(<ArchiveHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await click('inspect')
    expect(readTestId('expanded')).toBe('row-1')

    await act(async () => {
      root.render(<ArchiveHarness test={{ ...buildArchivedTest(), name: 'Checkout Journey Refreshed' }} />)
    })
    await flushPromises()

    expect(readTestId('expanded')).toBe('row-1')
    expect(apiMock.getTestArchiveTransactions).toHaveBeenCalledTimes(1)

    await click('set-group')
    await flushPromises()

    expect(readTestId('expanded')).toBe('none')
  })

  it('resets pagination when archive filters change', async () => {
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 60,
        resource_sample_count: 1,
        interval_summary_count: 1,
      },
      overview: {
        transactions: 60,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 120,
        min_response_ms: 80,
        max_response_ms: 220,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 15,
        peak_active_vusers: 10,
      },
      by_action: {
        Login: {
          transactions: 60,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
      },
      by_soap_operation: {
        CreateOrder: {
          transactions: 60,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
      },
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 200,
        net_sent_kbps: 11,
        net_recv_kbps: 12,
        disk_read_kbps: 13,
        disk_write_kbps: 14,
      },
    })
    apiMock.getTestArchiveTransactions.mockResolvedValue({
      transactions: [
        {
          timestamp: '2026-05-12T20:05:00Z',
          test_id: 'test-1',
          test_name: 'Checkout',
          group: 'Web',
          action_name: 'Login',
          duration_ms: 123,
          success: true,
        },
      ],
      total: 60,
      count: 1,
      limit: 10,
      offset: 0,
    })

    await act(async () => {
      root.render(<ArchiveHarness test={buildArchivedTest()} />)
    })

    await flushPromises()

    await act(async () => {
      container.querySelector('[data-testid="next"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 10,
    })
    expect(readTestId('offset')).toBe('10')

    await act(async () => {
      container.querySelector('[data-testid="set-group"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: 'Checkout',
      success: undefined,
      ...defaultArchiveTransactionQuery(),
      limit: 10,
      offset: 0,
    })
    expect(readTestId('group')).toBe('Checkout')
    expect(readTestId('offset')).toBe('0')
  })

  it('sends column sort and filter options to the archive transaction query', async () => {
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 60,
        resource_sample_count: 1,
        interval_summary_count: 1,
      },
      overview: {
        transactions: 60,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 120,
        min_response_ms: 80,
        max_response_ms: 220,
        resource_samples: 1,
        interval_summaries: 1,
        peak_tps: 15,
        peak_active_vusers: 10,
      },
      by_action: {
        Login: {
          transactions: 60,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 220,
        },
      },
      by_soap_operation: {},
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 200,
        net_sent_kbps: 11,
        net_recv_kbps: 12,
        disk_read_kbps: 13,
        disk_write_kbps: 14,
      },
    })
    apiMock.getTestArchiveTransactions.mockResolvedValue({
      transactions: [
        {
          timestamp: '2026-05-12T20:05:00Z',
          test_id: 'test-1',
          test_name: 'Checkout',
          group: 'Web',
          action_name: 'Login',
          duration_ms: 123,
          success: false,
          status_code: 500,
        },
      ],
      total: 60,
      count: 1,
      limit: 10,
      offset: 0,
    })

    await act(async () => {
      root.render(<ArchiveHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await click('next')
    await flushPromises()
    expect(readTestId('offset')).toBe('10')

    await click('sort-duration')
    await flushPromises()

    expect(readTestId('sort')).toBe('duration:asc')
    expect(readTestId('offset')).toBe('0')
    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery({ sort_by: 'duration', sort_dir: 'asc' }),
      limit: 10,
      offset: 0,
    })

    await click('filter-status')
    await flushPromises()

    expect(readTestId('status-filter')).toBe('500')
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 320))
    })
    await flushPromises()
    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery({ status: '500', sort_by: 'duration', sort_dir: 'asc' }),
      limit: 10,
      offset: 0,
    })

    await click('clear-filters')
    await flushPromises()

    expect(readTestId('status-filter')).toBe('none')
    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      soap_operation: undefined,
      group: undefined,
      success: undefined,
      ...defaultArchiveTransactionQuery({ sort_by: 'duration', sort_dir: 'asc' }),
      limit: 10,
      offset: 0,
    })
  })

  it('downloads browser diagnostics bundles for archived tests', async () => {
    const blob = new Blob(['zip'], { type: 'application/zip' })
    apiMock.downloadTestArchiveBrowserDiagnostics.mockResolvedValue(blob)
    apiMock.getTestArchiveReport.mockResolvedValue({
      manifest: {
        schema_version: 'v1',
        test_id: 'test-1',
        test_name: 'Checkout Journey',
        created_at: '2026-05-12T20:00:00Z',
        transactions_file: 'transactions.jsonl',
        resource_samples_file: 'resources.jsonl',
        interval_summaries_file: 'interval_summaries.jsonl',
        transaction_count: 0,
        resource_sample_count: 0,
        interval_summary_count: 0,
      },
      overview: {
        transactions: 0,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 0,
        min_response_ms: 0,
        max_response_ms: 0,
        resource_samples: 0,
        interval_summaries: 0,
        peak_tps: 0,
        peak_active_vusers: 0,
      },
      by_action: {},
      by_soap_operation: {},
      resource_peaks: {
        cpu_process_pct: 0,
        mem_process_mb: 0,
        net_sent_kbps: 0,
        net_recv_kbps: 0,
        disk_read_kbps: 0,
        disk_write_kbps: 0,
      },
    })

    let model: ReturnType<typeof useExecutionArchiveModel> | null = null

    function DownloadHarness({ test }: { test: TestSummary | null }) {
      model = useExecutionArchiveModel(test)
      return null
    }

    await act(async () => {
      root.render(<DownloadHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await act(async () => {
      await model?.onDownloadBrowserDiagnostics()
    })

    expect(apiMock.downloadTestArchiveBrowserDiagnostics).toHaveBeenCalledWith('test-1')
    expect(downloadBlob).toHaveBeenCalledWith(blob, 'checkout-journey-test-1-browser-diagnostics.zip')
  })

  it('preserves a declared CSV export filename in browser and native downloads', async () => {
    const report = buildEmptyArchiveReport()
    apiMock.getTestArchiveReport.mockResolvedValue({ ...report, manifest: { ...report.manifest, csv_exports: [{ id: 'trace', group: 'users', file_name: 'traceId-results.csv', status: 'complete', rows: 6 }] } })
    const blob = new Blob(['traceId,latencyMs\n'])
    apiMock.downloadTestArchiveArtifact.mockResolvedValue(blob)
    let model: ReturnType<typeof useExecutionArchiveModel> | null = null
    const archived = buildArchivedTest()
    function DownloadHarness() { model = useExecutionArchiveModel(archived); return null }
    await act(async () => { root.render(<DownloadHarness />) })
    await flushPromises()
    await act(async () => { await model?.onDownloadArchiveArtifact('traceId-results.csv') })
    expect(downloadBlob).toHaveBeenCalledWith(blob, 'traceId-results.csv')
    expect(saveApiResourceViaBridge).toHaveBeenCalledWith('/api/v1/tests/test-1/archive/files/traceId-results.csv', 'traceId-results.csv')
  })

  it('streams archive downloads through the native bridge without creating browser blobs', async () => {
    vi.mocked(saveApiResourceViaBridge).mockResolvedValue('saved')
    let model: ReturnType<typeof useExecutionArchiveModel> | null = null

    function DownloadHarness({ test }: { test: TestSummary | null }) {
      model = useExecutionArchiveModel(test)
      return null
    }

    await act(async () => {
      root.render(<DownloadHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await act(async () => {
      await model?.onDownloadArchive()
      await model?.onDownloadBrowserDiagnostics()
      await model?.onDownloadArchiveArtifact('transactions.jsonl')
    })

    expect(saveApiResourceViaBridge).toHaveBeenNthCalledWith(
      1,
      '/api/v1/tests/test-1/archive',
      'checkout-journey-test-1.zip',
    )
    expect(saveApiResourceViaBridge).toHaveBeenNthCalledWith(
      2,
      '/api/v1/tests/test-1/archive/browser-diagnostics',
      'checkout-journey-test-1-browser-diagnostics.zip',
    )
    expect(saveApiResourceViaBridge).toHaveBeenNthCalledWith(
      3,
      '/api/v1/tests/test-1/archive/files/transactions.jsonl',
      'checkout-journey-test-1-transactions.jsonl',
    )
    expect(apiMock.downloadTestArchive).not.toHaveBeenCalled()
    expect(apiMock.downloadTestArchiveBrowserDiagnostics).not.toHaveBeenCalled()
    expect(apiMock.downloadTestArchiveArtifact).not.toHaveBeenCalled()
    expect(downloadBlob).not.toHaveBeenCalled()
  })

  it('streams archive downloads through navigation URLs in the desktop bootstrap browser', async () => {
    setDesktopApiCapability('browser-session-secret')
    let model: ReturnType<typeof useExecutionArchiveModel> | null = null

    function DownloadHarness({ test }: { test: TestSummary | null }) {
      model = useExecutionArchiveModel(test)
      return null
    }

    await act(async () => {
      root.render(<DownloadHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await act(async () => {
      await model?.onDownloadArchive()
      await model?.onDownloadBrowserDiagnostics()
      await model?.onDownloadArchiveArtifact('transactions.jsonl')
    })

    expect(startDesktopBrowserDownload).toHaveBeenNthCalledWith(
      1,
      '/api/v1/tests/test-1/archive',
      'checkout-journey-test-1.zip',
    )
    expect(startDesktopBrowserDownload).toHaveBeenNthCalledWith(
      2,
      '/api/v1/tests/test-1/archive/browser-diagnostics',
      'checkout-journey-test-1-browser-diagnostics.zip',
    )
    expect(startDesktopBrowserDownload).toHaveBeenNthCalledWith(
      3,
      '/api/v1/tests/test-1/archive/files/transactions.jsonl',
      'checkout-journey-test-1-transactions.jsonl',
    )
    expect(apiMock.downloadTestArchive).not.toHaveBeenCalled()
    expect(apiMock.downloadTestArchiveBrowserDiagnostics).not.toHaveBeenCalled()
    expect(apiMock.downloadTestArchiveArtifact).not.toHaveBeenCalled()
    expect(downloadBlob).not.toHaveBeenCalled()
  })

  it('stops a cancelled native archive save without fetching a browser blob', async () => {
    vi.mocked(saveApiResourceViaBridge).mockResolvedValue('cancelled')
    let model: ReturnType<typeof useExecutionArchiveModel> | null = null

    function DownloadHarness({ test }: { test: TestSummary | null }) {
      model = useExecutionArchiveModel(test)
      return null
    }

    await act(async () => {
      root.render(<DownloadHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await act(async () => {
      await model?.onDownloadArchive()
    })

    expect(apiMock.downloadTestArchive).not.toHaveBeenCalled()
    expect(downloadBlob).not.toHaveBeenCalled()
  })

  it('surfaces native archive save failures without silently using a browser download', async () => {
    vi.mocked(saveApiResourceViaBridge).mockRejectedValue(new Error('disk is full'))
    let model: ReturnType<typeof useExecutionArchiveModel> | null = null

    function DownloadHarness({ test }: { test: TestSummary | null }) {
      model = useExecutionArchiveModel(test)
      return <span data-testid="native-download-error">{model.archiveError}</span>
    }

    await act(async () => {
      root.render(<DownloadHarness test={buildArchivedTest()} />)
    })
    await flushPromises()

    await act(async () => {
      await model?.onDownloadArchive()
    })

    expect(readTestId('native-download-error')).toBe('disk is full')
    expect(apiMock.downloadTestArchive).not.toHaveBeenCalled()
    expect(downloadBlob).not.toHaveBeenCalled()
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})

function defaultArchiveTransactionQuery(overrides: Record<string, string | undefined> = {}) {
  return {
	region: undefined,
	network_profile: undefined,
	source_transaction: undefined,
	hierarchy: undefined,
	phase: undefined,
	has_detail: undefined,
    time_from: undefined,
    time_to: undefined,
    status: undefined,
    min_duration_ms: undefined,
    max_duration_ms: undefined,
    min_bytes: undefined,
    max_bytes: undefined,
    error_contains: undefined,
    sort_by: undefined,
    sort_dir: undefined,
    ...overrides,
  }
}

function buildArchivedTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Checkout Journey',
    status: 'COMPLETE',
    total_vusers: 10,
    duration: '5m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [
      {
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 10,
      },
    ],
    archive: {
      test_id: 'test-1',
      schema_version: 'v1',
      archive_path: '/tmp/archive.zip',
      manifest_path: '/tmp/manifest.json',
      transaction_count: 60,
      resource_sample_count: 1,
      interval_summary_count: 1,
      created_at: '2026-05-12T20:00:00Z',
      completed_at: '2026-05-12T20:05:00Z',
    },
  }
}

function buildEmptyArchiveReport() {
  return {
    manifest: {
      schema_version: 'v1',
      test_id: 'test-1',
      test_name: 'Checkout Journey',
      created_at: '2026-05-12T20:00:00Z',
      transactions_file: 'transactions.jsonl',
      resource_samples_file: 'resources.jsonl',
      interval_summaries_file: 'interval_summaries.jsonl',
      transaction_count: 0,
      resource_sample_count: 0,
      interval_summary_count: 0,
    },
    overview: {
      transactions: 0,
      errors: 0,
      error_pct: 0,
      avg_response_ms: 0,
      min_response_ms: 0,
      max_response_ms: 0,
      resource_samples: 0,
      interval_summaries: 0,
      peak_tps: 0,
      peak_active_vusers: 0,
    },
    by_action: {},
    by_soap_operation: {},
    resource_peaks: {
      cpu_process_pct: 0,
      mem_process_mb: 0,
      net_sent_kbps: 0,
      net_recv_kbps: 0,
      disk_read_kbps: 0,
      disk_write_kbps: 0,
    },
  }
}

function buildArchiveAnalysisResponse() {
  return {
    schema_version: 'apexload.archive_analysis.v1',
    test_id: 'test-1',
    graph: {
      nodes: [
        {
          id: 'action:Login',
          kind: 'action',
          label: 'Login',
          runs: [{
            run_id: 'test-1',
            transactions: 3,
            errors: 0,
            error_pct: 0,
            avg_response_ms: 120,
          }],
        },
      ],
      edges: [],
    },
    transactions: [],
    count: 0,
    total: 0,
    limit: 10,
    offset: 0,
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}
