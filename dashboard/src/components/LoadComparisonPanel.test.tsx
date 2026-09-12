// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { LoadComparisonPanel } from './LoadComparisonPanel'
import type { LoadComparisonTestOption } from './LoadComparisonPanel'
import type { LoadComparisonResponse, LoadComparisonScope } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children, data }: { children?: ReactNode; data?: unknown[] }) => (
    <svg data-testid="mock-load-comparison-chart" data-length={String(data?.length ?? '')}>{children}</svg>
  )
  return {
    CartesianGrid: Mock,
    Legend: Mock,
    LineChart: MockChart,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="mock-responsive-container">{children}</div>,
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
    Line: ({ dataKey, name }: { dataKey?: string; name?: string }) => (
      <path data-testid="mock-line" data-key={dataKey ?? ''} data-name={name ?? ''} />
    ),
  }
})

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

const flush = async () => {
  await act(async () => {
    await Promise.resolve()
  })
}

describe('LoadComparisonPanel', () => {
  it('renders transaction load comparison charts by release', async () => {
    const getLoadComparison = vi.fn().mockResolvedValue(transactionComparison())

    await act(async () => {
      root.render(<LoadComparisonPanel applicationId="app-1" getLoadComparison={getLoadComparison} />)
    })
    await flush()

    expect(getLoadComparison).toHaveBeenCalledWith('app-1', 'transaction', '')
    expect(container.textContent).toContain('Load Comparison')
    expect(container.textContent).toContain('this grouping does not establish named transaction attempt evidence')
    expect(container.textContent).toContain('TPS Under Load')
    expect(container.textContent).toContain('ART Under Load')
    expect(container.querySelector<HTMLSelectElement>('select[aria-label="Select transaction for load comparison"]')?.value).toBe('Checkout Flow')
    expect(container.querySelectorAll('[data-testid="mock-load-comparison-chart"]')).toHaveLength(2)
    const lineNames = Array.from(container.querySelectorAll('[data-testid="mock-line"]')).map(line => line.getAttribute('data-name'))
    expect(lineNames).toContain('Sprint 24')
    expect(lineNames).toContain('Sprint 25')
    expect(summaryCardRows()).toEqual([
      ['2', 'Graph Releases'],
      ['2', 'Graph Runs'],
      ['2', 'Load Levels'],
      ['80', 'Transactions'],
      ['14.0', 'Peak TPS'],
      ['134 ms', 'Avg ART'],
      ['1 (1.25%)', 'Errors'],
    ])
  })

  it('labels action evidence separately and explains a single-run baseline', async () => {
    const response = transactionComparison()
    response.options[0].source_kind = 'action'
    response.releases = [{ ...response.releases[0], run_count: 1 }]
    const getLoadComparison = vi.fn().mockResolvedValue(response)
    await act(async () => { root.render(<LoadComparisonPanel applicationId="app-1" getLoadComparison={getLoadComparison} />) })
    await flush()
    expect(container.textContent).toContain('Population: request / action samples, grouped by action name.')
    expect(container.textContent).toContain('whole-run totals across stored populations')
    expect(container.textContent).toContain('one point cannot establish a trend')
  })

  it('switches between transactions and groups and reloads selected keys', async () => {
    const getLoadComparison = vi.fn((applicationId: string, scope: LoadComparisonScope = 'transaction', key = '') => {
      if (scope === 'group') {
        return Promise.resolve(groupComparison(key || 'Checkout'))
      }
      return Promise.resolve(transactionComparison(key || 'Checkout Flow'))
    })

    await act(async () => {
      root.render(<LoadComparisonPanel applicationId="app-1" getLoadComparison={getLoadComparison} />)
    })
    await flush()

    await act(async () => {
      buttonByText('Groups')?.click()
    })
    await flush()

    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'group', '')
    const groupSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Select group for load comparison"]')
    expect(groupSelect?.value).toBe('Checkout')
    expect(groupSelect?.textContent).toContain('Browse')

    await act(async () => {
      if (groupSelect) {
        groupSelect.value = 'Browse'
        groupSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    await flush()

    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'group', 'Browse')
    expect(summaryCardRows()).toEqual([
      ['2', 'Graph Releases'],
      ['2', 'Graph Runs'],
      ['2', 'Load Levels'],
      ['80', 'Transactions'],
      ['14.0', 'Peak TPS'],
      ['134 ms', 'Avg ART'],
      ['1 (1.25%)', 'Errors'],
    ])
  })

  it('limits comparison requests to the selected test runs', async () => {
    const testOptions = loadComparisonTestOptions()
    const getLoadComparison = vi.fn().mockResolvedValue(transactionComparison())

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={testOptions}
          selectedTestIds={['run-a', 'run-b']}
        />,
      )
    })
    await flush()

    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { testIds: ['run-a', 'run-b'] })
    expect(container.textContent).toContain('2 of 2 runs selected')
    expect(container.querySelector('.load-comparison-run-list')).toBeNull()
    expect(container.querySelector('.load-comparison-checkbox')).toBeNull()

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={testOptions}
          selectedTestIds={['run-b']}
        />,
      )
    })
    await flush()

    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { testIds: ['run-b'] })
    expect(container.textContent).toContain('1 of 2 runs selected')

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={testOptions}
          selectedTestIds={[]}
        />,
      )
    })
    await flush()

    expect(container.textContent).toContain('Select at least one archived run to compare')
    expect(getLoadComparison).toHaveBeenCalledTimes(2)
  })

  it('does not reload when parent polling preserves the same selected run ids', async () => {
    const getLoadComparison = vi.fn().mockResolvedValue(transactionComparison())

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={loadComparisonTestOptions()}
        />,
      )
    })
    await flush()

    expect(getLoadComparison).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={loadComparisonTestOptions()}
        />,
      )
    })
    await flush()

    expect(getLoadComparison).toHaveBeenCalledTimes(1)
  })

  it('renders empty and error states cleanly', async () => {
    const getLoadComparison = vi.fn().mockResolvedValue({
      application_id: 'app-1',
      scope: 'transaction',
      options: [],
      load_levels: [],
      releases: [],
    } satisfies LoadComparisonResponse)

    await act(async () => {
      root.render(<LoadComparisonPanel applicationId="app-1" getLoadComparison={getLoadComparison} />)
    })
    await flush()

    expect(container.textContent).toContain('No archived transactions under load yet')

    getLoadComparison.mockRejectedValueOnce(new Error('comparison unavailable'))
    await act(async () => {
      buttonByText('Groups')?.click()
    })
    await flush()

    expect(container.textContent).toContain('comparison unavailable')
  })

  it('retries through the same shared fetch, preserving run scoping', async () => {
    const getLoadComparison = vi.fn().mockRejectedValueOnce(new Error('comparison unavailable'))

    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
          testOptions={loadComparisonTestOptions()}
          selectedTestIds={['run-b']}
        />,
      )
    })
    await flush()

    expect(container.textContent).toContain('comparison unavailable')
    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { testIds: ['run-b'] })

    getLoadComparison.mockResolvedValueOnce(transactionComparison())
    await act(async () => {
      buttonByText('Retry')?.click()
    })
    await flush()
    await flush()

    // Retry must go through the same request builder as the effect, not a divergent copy.
    expect(getLoadComparison).toHaveBeenCalledTimes(2)
    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'transaction', '', { testIds: ['run-b'] })
    expect(container.textContent).not.toContain('comparison unavailable')
  })
})

function transactionComparison(selectedKey = 'Checkout Flow'): LoadComparisonResponse {
  return {
    application_id: 'app-1',
    scope: 'transaction',
    selected_key: selectedKey,
    options: [
      { key: 'Checkout Flow', label: 'Checkout Flow', scope: 'transaction', source_kind: 'source_transaction', release_count: 2, run_count: 2, transaction_count: 80, sample_count: 4 },
      { key: 'Search', label: 'Search', scope: 'transaction', source_kind: 'action', release_count: 1, run_count: 1, transaction_count: 10, sample_count: 1 },
    ],
    load_levels: [
      { id: 'vusers-5', label: '5 VUsers', min_vusers: 5, max_vusers: 5, exact: true, sample_count: 2 },
      { id: 'vusers-10', label: '10 VUsers', min_vusers: 10, max_vusers: 10, exact: true, sample_count: 2 },
    ],
    releases: [
      {
        release_id: 'rel-1',
        release_name: 'Sprint 24',
        run_count: 1,
        transaction_count: 40,
        points: [
          { load_level_id: 'vusers-5', load_label: '5 VUsers', tps: 8, avg_response_ms: 120, transactions: 20, errors: 0, error_pct: 0, run_count: 1, sample_count: 1 },
          { load_level_id: 'vusers-10', load_label: '10 VUsers', tps: 12, avg_response_ms: 160, transactions: 20, errors: 1, error_pct: 5, run_count: 1, sample_count: 1 },
        ],
      },
      {
        release_id: 'rel-2',
        release_name: 'Sprint 25',
        run_count: 1,
        transaction_count: 40,
        points: [
          { load_level_id: 'vusers-5', load_label: '5 VUsers', tps: 10, avg_response_ms: 110, transactions: 20, errors: 0, error_pct: 0, run_count: 1, sample_count: 1 },
          { load_level_id: 'vusers-10', load_label: '10 VUsers', tps: 14, avg_response_ms: 145, transactions: 20, errors: 0, error_pct: 0, run_count: 1, sample_count: 1 },
        ],
      },
    ],
  }
}

function groupComparison(selectedKey = 'Checkout'): LoadComparisonResponse {
  return {
    ...transactionComparison(),
    scope: 'group',
    selected_key: selectedKey,
    options: [
      { key: 'Checkout', label: 'Checkout', scope: 'group', source_kind: 'group', release_count: 2, run_count: 2, transaction_count: 120, sample_count: 4 },
      { key: 'Browse', label: 'Browse', scope: 'group', source_kind: 'group', release_count: 1, run_count: 1, transaction_count: 30, sample_count: 2 },
    ],
  }
}

function loadComparisonTestOptions(): LoadComparisonTestOption[] {
  return [
    {
      id: 'run-a',
      name: 'Checkout Load A',
      releaseId: 'rel-1',
      releaseName: 'Sprint 24',
      completedAt: '2026-06-01T12:00:00Z',
      vusers: 5,
      transactionCount: 40,
    },
    {
      id: 'run-b',
      name: 'Checkout Load B',
      releaseId: 'rel-2',
      releaseName: 'Sprint 25',
      completedAt: '2026-06-02T12:00:00Z',
      vusers: 10,
      transactionCount: 40,
    },
  ]
}

function buttonByText(label: string) {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)
}

function summaryCardRows() {
  return Array.from(container.querySelectorAll('.load-comparison-summary-card')).map(card => [
    card.querySelector('.load-comparison-summary-value')?.textContent?.trim() || '',
    card.querySelector('.load-comparison-summary-label')?.textContent?.trim() || '',
  ])
}
