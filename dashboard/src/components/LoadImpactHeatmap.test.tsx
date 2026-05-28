// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { MetricsHistoryPoint } from '../types'
import LoadImpactHeatmap from './LoadImpactHeatmap'

vi.mock('./LoadImpact3DHeatmap', () => ({
  default: ({ model, onSelectedCellKeyChange }: {
    model: {
      rows: Array<{
        cells: Array<{
          metric: { id: string; label: string }
          bucket: { id: string; label: string }
          sampleCount: number
        }>
      }>
    }
    onSelectedCellKeyChange: (key: string) => void
  }) => {
    const cell = model.rows
      .flatMap(row => row.cells)
      .find(candidate => candidate.sampleCount > 0)
    const key = cell ? `${cell.metric.id}:${cell.bucket.id}` : ''

    return (
      <div data-testid="mock-3d-heatmap">
        <button type="button" onClick={() => onSelectedCellKeyChange(key)}>
          Select 3D cell
        </button>
      </div>
    )
  },
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LoadImpactHeatmap', () => {
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
  })

  it('renders metric rows only when samples exist', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, cpuProcess: 22 }),
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('Load Impact')
    expect(container.textContent).toContain('5 VUsers')
    expect(container.textContent).toContain('10 VUsers')
    expect(rowLabels()).toContain('ARTms')
    expect(rowLabels()).toContain('P95ms')
    expect(rowLabels()).toContain('TPS')
    expect(rowLabels()).toContain('CPU VUsers%')
    expect(rowLabels()).not.toContain('Memory VUsersMB')
  })

  it('opens the data point table when a populated heatmap cell is selected', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { timestampMs: Date.parse('2026-05-12T20:00:00Z'), avgMs: 100, tps: 3, p95: 130, errorPct: 1, cpuProcess: 18, memProcess: 256 }),
            point('10:00:01', 5, { timestampMs: Date.parse('2026-05-12T20:00:01Z'), avgMs: 140, tps: 4, p95: 180, errorPct: 2, cpuProcess: 22, memProcess: 300 }),
          ]}
        />,
      )
    })

    const artButton = container.querySelector<HTMLButtonElement>('button[aria-label^="ART at 5 VUsers"]')
    expect(artButton).not.toBeNull()

    await act(async () => {
      artButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('ART at 5 VUsers')
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('2 data points')
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('120ms')
    expect(container.querySelector('.load-impact-detail-table')?.textContent).toContain('100ms')
    expect(container.querySelector('.load-impact-detail-table')?.textContent).toContain('140ms')
  })

  it('shows loading and empty states without claiming data exists', async () => {
    await act(async () => {
      root.render(<LoadImpactHeatmap points={[]} loading />)
    })

    expect(container.textContent).toContain('Loading load impact samples')

    await act(async () => {
      root.render(<LoadImpactHeatmap points={[]} emptyMessage="No load-impact data yet." />)
    })

    expect(container.textContent).toContain('No load-impact data yet.')
  })

  it('switches to 3D mode and keeps cell selection wired to the detail table', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180 }),
          ]}
        />,
      )
    })

    const tab3d = buttonByText('3D')
    expect(tab3d).not.toBeNull()

    await act(async () => {
      tab3d?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="mock-3d-heatmap"]')).not.toBeNull()
    expect(container.querySelector('.load-impact-table')).toBeNull()
    expect(tab3d?.getAttribute('aria-selected')).toBe('true')

    const select3dCell = buttonByText('Select 3D cell')
    await act(async () => {
      select3dCell?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('ART at 5 VUsers')
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('1 data points')
  })

  function rowLabels() {
    return Array.from(container.querySelectorAll('.load-impact-table tbody th')).map(node => node.textContent)
  }

  function buttonByText(text: string) {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === text) ?? null
  }
})

function point(time: string, vusers: number, values: Partial<MetricsHistoryPoint> = {}): MetricsHistoryPoint {
  return {
    time,
    vusers,
    tps: undefined as unknown as number,
    avgMs: undefined as unknown as number,
    errorPct: undefined as unknown as number,
    p50: undefined as unknown as number,
    p90: undefined as unknown as number,
    p99: undefined as unknown as number,
    ...values,
  }
}
