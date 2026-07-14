// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import { useExecutionArchiveResourceHistory } from './useExecutionArchiveResourceHistory'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getTestArchiveResources: vi.fn(),
}

vi.mock('../hooks/api/useTestApi', () => ({
  useTestApi: () => apiMock,
}))

function ResourceHistoryHarness({ test }: { test: TestSummary | null }) {
  const model = useExecutionArchiveResourceHistory(test)

  return (
    <div>
      <span data-testid="loading">{model.archiveResourcesLoading ? 'loading' : 'idle'}</span>
      <span data-testid="count">{String(model.archiveResourceHistory.length)}</span>
      <span data-testid="cpu">{model.archiveResourceHistory[0]?.cpuProcess ?? 'none'}</span>
      <span data-testid="disk">{model.archiveResourceHistory[1]?.diskWrite ?? 'none'}</span>
      <span data-testid="error">{model.archiveResourceError || 'none'}</span>
    </div>
  )
}

describe('useExecutionArchiveResourceHistory', () => {
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

  it('loads a bounded server-downsampled resource series for completed runs', async () => {
    apiMock.getTestArchiveResources.mockResolvedValueOnce({
      resources: [
        buildResourceSample('2026-05-12T20:00:00Z', 11, 3),
        buildResourceSample('2026-05-12T20:00:01Z', 12, 4),
      ],
      total: 2,
      count: 2,
      limit: 5000,
      offset: 0,
      sampled: true,
    })

    await act(async () => {
      root.render(<ResourceHistoryHarness test={buildArchivedTest()} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveResources).toHaveBeenCalledTimes(1)
    expect(apiMock.getTestArchiveResources).toHaveBeenCalledWith('test-1', { sample_limit: 5000 })
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('count')).toBe('2')
    expect(readTestId('cpu')).toBe('11')
    expect(readTestId('disk')).toBe('4')
  })

  it('does not load resources for non-terminal runs', async () => {
    await act(async () => {
      root.render(<ResourceHistoryHarness test={{ ...buildArchivedTest(), status: 'RUNNING' }} />)
    })

    await flushPromises()

    expect(apiMock.getTestArchiveResources).not.toHaveBeenCalled()
    expect(readTestId('count')).toBe('0')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})

function buildArchivedTest(): TestSummary {
  return {
    id: 'test-1',
    name: 'Checkout',
    status: 'COMPLETE',
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: '2026-05-12T20:00:00Z',
    archive: {
      test_id: 'test-1',
      schema_version: 'apexload.archive.v1',
      archive_path: '/tmp/archive',
      manifest_path: '/tmp/archive/manifest.json',
      transaction_count: 1,
      resource_sample_count: 2,
      interval_summary_count: 2,
      created_at: '2026-05-12T20:00:00Z',
      completed_at: '2026-05-12T20:01:00Z',
    },
  }
}

function buildResourceSample(timestamp: string, cpuProcess: number, diskWrite: number) {
  return {
    timestamp,
    active_vusers: 1,
    cpu_process_pct: cpuProcess,
    cpu_free_pct: 70,
    mem_process_mb: 256,
    mem_free_mb: 4096,
    net_sent_kbps: 12,
    net_recv_kbps: 18,
    disk_read_kbps: 2,
    disk_write_kbps: diskWrite,
  }
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
