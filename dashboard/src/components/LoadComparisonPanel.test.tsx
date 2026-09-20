// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  LoadComparisonPanel,
  type LoadComparisonTestOption,
} from './LoadComparisonPanel'
import type { LoadComparisonResponse, LoadComparisonScope } from '../types'
import { downloadTextFile } from '../utils/fileUtils'
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
vi.mock('../utils/fileUtils', () => ({
  downloadTextFile: vi.fn().mockResolvedValue('saved'),
}))
vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  return {
    CartesianGrid: Mock,
    Legend: Mock,
    ReferenceLine: Mock,
    LineChart: ({ children }: { children?: ReactNode }) => (
      <svg data-testid="chart">{children}</svg>
    ),
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => (
      <div>{children}</div>
    ),
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
    Line: ({ connectNulls }: { connectNulls?: boolean }) => (
      <path data-gaps={String(!connectNulls)} />
    ),
  }
})
let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  vi.clearAllMocks()
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
})
const flush = async () => {
  await act(async () => {
    await Promise.resolve()
  })
}
async function choose(label: string, value: string) {
  const field = Array.from(container.querySelectorAll('label'))
    .find((node) => node.textContent?.startsWith(label))
    ?.querySelector('select')
  expect(field).toBeTruthy()
  await act(async () => {
    field!.value = value
    field!.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
describe('LoadComparisonPanel', () => {
  it('renders three aligned charts and an accessible evidence table', async () => {
    const getLoadComparison = vi.fn().mockResolvedValue(transactionComparison())
    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
        />,
      )
    })
    expect(getLoadComparison).toHaveBeenCalledWith('app-1', 'transaction', '', {
      population: 'action',
    })
    expect(container.querySelectorAll('[data-testid="chart"]')).toHaveLength(3)
    expect(container.textContent).toContain('Successful throughput')
    expect(container.textContent).toContain(
      'Measured values and changes at matching load',
    )
    expect(container.textContent).toContain('Comparison: Unverified')
    expect(container.querySelectorAll('[data-gaps="true"]')).toHaveLength(6)
    await choose('Latency statistic', 'average_ms')
    expect(container.textContent).toContain('-10.0 ms')
  })
  it('switches populations and groups without displaying stale measurements', async () => {
    const getLoadComparison = vi.fn(
      (app: string, scope: LoadComparisonScope = 'transaction', key = '') =>
        Promise.resolve(
          scope === 'group'
            ? groupComparison(key || 'Checkout')
            : transactionComparison(key || 'Checkout Flow'),
        ),
    )
    await act(async () => {
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
        />,
      )
    })
    await choose('Metric population', 'transaction')
    expect(getLoadComparison).toHaveBeenLastCalledWith(
      'app-1',
      'transaction',
      '',
      { population: 'transaction' },
    )
    await act(async () => buttonByText('Groups')!.click())
    expect(getLoadComparison).toHaveBeenLastCalledWith('app-1', 'group', '', {})
    await choose('Group', 'Browse')
    expect(getLoadComparison).toHaveBeenLastCalledWith(
      'app-1',
      'group',
      'Browse',
      {},
    )
  })
  it('does not present errors without a denominator as a zero rate', async () => {
    const response = transactionComparison()
    response.releases[0].points[0] = {
      ...response.releases[0].points[0],
      transactions: 0,
      errors: 77,
      evidence_status: 'inconsistent_counts',
    }
    await act(async () =>
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={vi.fn().mockResolvedValue(response)}
        />,
      ),
    )
    expect(container.textContent).toContain(
      '77 errors; matching attempt count unavailable',
    )
    expect(container.textContent).not.toContain('77 (0.00%)')
  })
  it('preserves selected-run constraints on retry and does not fetch an empty selection', async () => {
    const getLoadComparison = vi
      .fn()
      .mockRejectedValueOnce(new Error('unavailable'))
      .mockResolvedValue(transactionComparison())
    const render = (ids: string[]) => (
      <LoadComparisonPanel
        applicationId="app-1"
        getLoadComparison={getLoadComparison}
        testOptions={loadComparisonTestOptions()}
        selectedTestIds={ids}
      />
    )
    await act(async () => root.render(render(['run-b'])))
    await act(async () => buttonByText('Retry')!.click())
    expect(getLoadComparison).toHaveBeenLastCalledWith(
      'app-1',
      'transaction',
      '',
      { population: 'action', testIds: ['run-b'] },
    )
    await act(async () => root.render(render(['run-b'])))
    expect(getLoadComparison).toHaveBeenCalledTimes(2)
    await act(async () => root.render(render([])))
    expect(getLoadComparison).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Select at least one archived run')
  })
  it('ignores a stale response after a population switch', async () => {
    let resolve: (value: LoadComparisonResponse) => void = () => {}
    const stale = new Promise<LoadComparisonResponse>((done) => {
      resolve = done
    })
    const fresh = {
      ...transactionComparison(),
      selected_key: 'Fresh',
      options: [
        { ...transactionComparison().options[0], key: 'Fresh', label: 'Fresh' },
      ],
    }
    const getLoadComparison = vi
      .fn()
      .mockReturnValueOnce(stale)
      .mockResolvedValue(fresh)
    await act(async () =>
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={getLoadComparison}
        />,
      ),
    )
    await choose('Metric population', 'transaction')
    await act(async () => resolve(transactionComparison()))
    expect(container.textContent).toContain('Fresh')
    expect(container.textContent).not.toContain('Checkout Flow')
  })
  it('exports only the displayed comparison with scope and excluded evidence', async () => {
    const response = {
      ...transactionComparison(),
      warnings: ['Archive omitted'],
      skipped_runs: [{ test_id: 'bad', reason: 'Unreadable' }],
    }
    await act(async () =>
      root.render(
        <LoadComparisonPanel
          applicationId="app-1"
          getLoadComparison={vi.fn().mockResolvedValue(response)}
        />,
      ),
    )
    await act(async () => buttonByText('Export this comparison')!.click())
    await flush()
    expect(downloadTextFile).toHaveBeenCalledTimes(1)
    const csv = vi.mocked(downloadTextFile).mock.calls[0][0]
    expect(csv).toContain('"Population","action"')
    expect(csv).toContain('"Baseline","rel-1"')
    expect(csv).toContain('"Excluded run","bad","Unreadable"')
  })
})
function transactionComparison(
  selectedKey = 'Checkout Flow',
): LoadComparisonResponse {
  return {
    application_id: 'app-1',
    scope: 'transaction',
    selected_key: selectedKey,
    options: [
      {
        key: 'Checkout Flow',
        label: 'Checkout Flow',
        scope: 'transaction',
        source_kind: 'source_transaction',
        release_count: 2,
        run_count: 2,
        transaction_count: 80,
        sample_count: 4,
      },
      {
        key: 'Search',
        label: 'Search',
        scope: 'transaction',
        source_kind: 'action',
        release_count: 1,
        run_count: 1,
        transaction_count: 10,
        sample_count: 1,
      },
    ],
    load_levels: [
      {
        id: 'vusers-5',
        label: '5 VUsers',
        min_vusers: 5,
        max_vusers: 5,
        exact: true,
        sample_count: 2,
      },
      {
        id: 'vusers-10',
        label: '10 VUsers',
        min_vusers: 10,
        max_vusers: 10,
        exact: true,
        sample_count: 2,
      },
    ],
    releases: [
      {
        release_id: 'rel-1',
        release_name: 'Sprint 24',
        run_count: 1,
        transaction_count: 40,
        points: [
          {
            load_level_id: 'vusers-5',
            load_label: '5 VUsers',
            tps: 8,
            avg_response_ms: 120,
            transactions: 20,
            errors: 0,
            error_pct: 0,
            run_count: 1,
            sample_count: 1,
          },
          {
            load_level_id: 'vusers-10',
            load_label: '10 VUsers',
            tps: 12,
            avg_response_ms: 160,
            transactions: 20,
            errors: 1,
            error_pct: 5,
            run_count: 1,
            sample_count: 1,
          },
        ],
      },
      {
        release_id: 'rel-2',
        release_name: 'Sprint 25',
        run_count: 1,
        transaction_count: 40,
        points: [
          {
            load_level_id: 'vusers-5',
            load_label: '5 VUsers',
            tps: 10,
            avg_response_ms: 110,
            transactions: 20,
            errors: 0,
            error_pct: 0,
            run_count: 1,
            sample_count: 1,
          },
          {
            load_level_id: 'vusers-10',
            load_label: '10 VUsers',
            tps: 14,
            avg_response_ms: 145,
            transactions: 20,
            errors: 0,
            error_pct: 0,
            run_count: 1,
            sample_count: 1,
          },
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
      {
        key: 'Checkout',
        label: 'Checkout',
        scope: 'group',
        source_kind: 'group',
        release_count: 2,
        run_count: 2,
        transaction_count: 120,
        sample_count: 4,
      },
      {
        key: 'Browse',
        label: 'Browse',
        scope: 'group',
        source_kind: 'group',
        release_count: 1,
        run_count: 1,
        transaction_count: 30,
        sample_count: 2,
      },
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
  return Array.from(container.querySelectorAll('button')).find(
    (button) => button.textContent === label,
  )
}
