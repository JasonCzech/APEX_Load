// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getTestArchiveReport: vi.fn(),
  getTestArchiveTransactions: vi.fn(),
  downloadTestArchive: vi.fn(),
  downloadTestArchiveArtifact: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

function ArchiveHarness({ test }: { test: TestSummary | null }) {
  const model = useExecutionArchiveModel(test)

  return (
    <div>
      <span data-testid="loading">{model.archiveLoading ? 'loading' : 'idle'}</span>
      <span data-testid="action">{model.selectedArchiveAction || 'none'}</span>
      <span data-testid="group">{model.selectedArchiveGroup || 'all'}</span>
      <span data-testid="offset">{String(model.archiveTransactionsOffset)}</span>
      <span data-testid="rows">{String(model.archiveTransactions.length)}</span>
      <span data-testid="page-start">{String(model.archivePageStart)}</span>
      <span data-testid="page-end">{String(model.archivePageEnd)}</span>
      <span data-testid="error">{model.archiveError || 'none'}</span>
      <button data-testid="next" onClick={() => model.onNextPage()}>next</button>
      <button data-testid="set-group" onClick={() => model.onSelectedArchiveGroupChange('Checkout')}>group</button>
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
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
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
      limit: 25,
      offset: 0,
    })

    await act(async () => {
      root.render(<ArchiveHarness test={buildArchivedTest()} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveReport).toHaveBeenCalledWith('test-1')
    expect(apiMock.getTestArchiveTransactions).toHaveBeenCalledWith('test-1', {
      action: 'Login',
      group: undefined,
      success: undefined,
      limit: 25,
      offset: 0,
    })
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('action')).toBe('Login')
    expect(readTestId('rows')).toBe('1')
    expect(readTestId('page-start')).toBe('1')
    expect(readTestId('page-end')).toBe('1')
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
      limit: 25,
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
      group: undefined,
      success: undefined,
      limit: 25,
      offset: 25,
    })
    expect(readTestId('offset')).toBe('25')

    await act(async () => {
      container.querySelector('[data-testid="set-group"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getTestArchiveTransactions).toHaveBeenLastCalledWith('test-1', {
      action: 'Login',
      group: 'Checkout',
      success: undefined,
      limit: 25,
      offset: 0,
    })
    expect(readTestId('group')).toBe('Checkout')
    expect(readTestId('offset')).toBe('0')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

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

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}