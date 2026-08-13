// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import { transactionChartSeriesKey } from './transactionChartSeries'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getTestArchiveSummaries: vi.fn(),
}

vi.mock('../hooks/api/useTestApi', () => ({
  useTestApi: () => apiMock,
}))

function SummaryHistoryHarness({ test }: { test: TestSummary | null }) {
  const model = useExecutionArchiveSummaryHistory(test)

  return (
    <div>
      <span data-testid="loading">{model.archiveSummariesLoading ? 'loading' : 'idle'}</span>
      <span data-testid="count">{String(model.archiveSummaryHistory.length)}</span>
      <span data-testid="art">{model.archiveSummaryHistory[0]?.avgMs ?? 'none'}</span>
      <span data-testid="p95">{model.archiveSummaryHistory[1]?.p95 ?? 'none'}</span>
      <span data-testid="transaction-tps">{model.archiveSummaryHistory[1]?.[transactionChartSeriesKey('Search', 'tps')] ?? 'none'}</span>
      <span data-testid="error">{model.archiveSummaryError || 'none'}</span>
    </div>
  )
}

describe('useExecutionArchiveSummaryHistory', () => {
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

  it('loads a bounded server-downsampled summary series for completed runs', async () => {
    apiMock.getTestArchiveSummaries.mockResolvedValueOnce({
      summaries: [
        buildSummary('2026-05-12T20:00:00Z', 120, 150),
        buildSummary('2026-05-12T20:00:01Z', 140, 180),
      ],
      total: 2,
      count: 2,
      limit: 5000,
      offset: 0,
      sampled: true,
    })

    await act(async () => {
      root.render(<SummaryHistoryHarness test={buildArchivedTest()} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledTimes(1)
    expect(apiMock.getTestArchiveSummaries).toHaveBeenCalledWith('test-1', { sample_limit: 5000 })
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('count')).toBe('2')
    expect(readTestId('art')).toBe('120')
    expect(readTestId('p95')).toBe('180')
    expect(readTestId('transaction-tps')).toBe('3')
  })

  it('does not load summaries for non-terminal runs', async () => {
    await act(async () => {
      root.render(<SummaryHistoryHarness test={{ ...buildArchivedTest(), status: 'RUNNING' }} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveSummaries).not.toHaveBeenCalled()
    expect(readTestId('count')).toBe('0')
  })

  it('does not expose test A summaries while a deferred test B request is loading', async () => {
    let resolveTestB!: (response: { summaries: ReturnType<typeof buildSummary>[] }) => void
    const testBResponse = new Promise<{ summaries: ReturnType<typeof buildSummary>[] }>(resolve => {
      resolveTestB = resolve
    })
    apiMock.getTestArchiveSummaries
      .mockResolvedValueOnce({ summaries: [buildSummary('2026-05-12T20:00:00Z', 120, 150, 'test-a')] })
      .mockReturnValueOnce(testBResponse)

    await act(async () => {
      root.render(<SummaryHistoryHarness test={buildArchivedTest('test-a')} />)
    })
    await flushPromises()

    expect(readTestId('count')).toBe('1')
    expect(readTestId('art')).toBe('120')

    await act(async () => {
      root.render(<SummaryHistoryHarness test={buildArchivedTest('test-b')} />)
    })

    expect(apiMock.getTestArchiveSummaries).toHaveBeenLastCalledWith('test-b', { sample_limit: 5000 })
    expect(readTestId('loading')).toBe('loading')
    expect(readTestId('count')).toBe('0')
    expect(readTestId('art')).toBe('none')
    expect(readTestId('transaction-tps')).toBe('none')

    await act(async () => {
      resolveTestB({ summaries: [buildSummary('2026-05-12T21:00:00Z', 240, 280, 'test-b')] })
      await Promise.resolve()
    })

    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('count')).toBe('1')
    expect(readTestId('art')).toBe('240')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

function buildArchivedTest(testID = 'test-1'): TestSummary {
  return {
    id: testID,
    name: 'Checkout',
    status: 'COMPLETE',
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: '2026-05-12T20:00:00Z',
    archive: {
      test_id: testID,
      schema_version: 'apexload.archive.v1',
      archive_path: '/tmp/archive',
      manifest_path: '/tmp/archive/manifest.json',
      transaction_count: 1,
      resource_sample_count: 0,
      interval_summary_count: 2,
      created_at: '2026-05-12T20:00:00Z',
      completed_at: '2026-05-12T20:01:00Z',
    },
  }
}

function buildSummary(timestamp: string, avgResponse: number, p95: number, testID = 'test-1') {
  return {
    timestamp,
    test_id: testID,
    test_name: 'Checkout',
    active_vusers: 10,
    tps: 4,
    avg_response_ms: avgResponse,
    error_pct: 0,
    total_transactions: 10,
    total_errors: 0,
    p50_ms: 90,
    p90_ms: 130,
    p95_ms: p95,
    p99_ms: 220,
    transactions: {
      Search: { name: 'Search', tps: 3, count: 4, errors: 1, avg_ms: 125 },
    },
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
