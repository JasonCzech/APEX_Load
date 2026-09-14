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

  it('configures displayed metrics and VUser load points', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130, cpuProcess: 18 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, cpuProcess: 22 }),
            point('10:00:02', 15, { avgMs: 160, tps: 5, p95: 220, cpuProcess: 28 }),
          ]}
        />,
      )
    })

    expect(rowLabels()).toContain('TPS')
    expect(bucketLabels()).toContain('10 VUsers')

    const configureButton = buttonByText('Configure Map')
    await act(async () => {
      configureButton?.click()
    })

    expect(container.textContent).toContain('Heatmap Configuration')

    const tpsCheckbox = container.querySelector<HTMLInputElement>('input[value="tps"]')
    const tenVusersCheckbox = container.querySelector<HTMLInputElement>('input[value="vusers-10"]')
    expect(tpsCheckbox?.checked).toBe(true)
    expect(tenVusersCheckbox?.checked).toBe(true)

    await act(async () => {
      tpsCheckbox?.click()
      tenVusersCheckbox?.click()
    })

    expect(rowLabels()).not.toContain('TPS')
    expect(bucketLabels()).not.toContain('10 VUsers')
    expect(container.textContent).toContain('3/4 metrics')
    expect(container.textContent).toContain('2/3 loads')
  })

  it('traps drawer focus, closes on Escape, and restores the Configure Map trigger', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4 }),
          ]}
        />,
      )
    })

    const configureButton = buttonByText('Configure Map')
    configureButton?.focus()
    await act(async () => configureButton?.click())
    await flushFocus()

    const drawer = container.querySelector<HTMLElement>('.load-impact-graph-drawer')
    const closeButton = drawer?.querySelector<HTMLButtonElement>('.load-impact-graph-drawer-header button')
    expect(drawer?.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(closeButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    })
    expect(drawer?.contains(document.activeElement)).toBe(true)
    expect(document.activeElement).not.toBe(closeButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.querySelector('.load-impact-graph-drawer')).toBeNull()
    expect(document.activeElement).toBe(configureButton)
  })

  it('sorts heatmap rows alphabetically without ranking mixed-unit values', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130, cpuProcess: 18 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, cpuProcess: 22 }),
          ]}
        />,
      )
    })

    expect(rowLabels()[0]).toBe('ARTms')

    expect(sortButtonByLabel('5 VUsers')).toBeNull()

    await act(async () => {
      sortButtonByLabel('Metric')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(rowLabels().slice(0, 4)).toEqual(['ARTms', 'CPU VUsers%', 'P95ms', 'TPS'])
  })

  it('highlights the matching metric row and VUser load column on cell hover and focus', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130, cpuProcess: 18 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, cpuProcess: 22 }),
          ]}
        />,
      )
    })

    const p95AtTenVusers = container.querySelector<HTMLButtonElement>('button[aria-label^="P95 at 10 VUsers"]')
    expect(p95AtTenVusers).not.toBeNull()

    await act(async () => {
      p95AtTenVusers?.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
    })

    expect(activeRowHeader()?.textContent).toContain('P95')
    expect(activeColumnHeader()?.textContent).toContain('10 VUsers')
    expect(p95AtTenVusers?.closest('td')?.className).toContain('load-impact-cell-row-context')
    expect(p95AtTenVusers?.closest('td')?.className).toContain('load-impact-cell-column-context')

    await act(async () => {
      p95AtTenVusers?.dispatchEvent(new MouseEvent('mouseout', { bubbles: true }))
    })

    expect(activeRowHeader()).toBeNull()
    expect(activeColumnHeader()).toBeNull()

    await act(async () => {
      p95AtTenVusers?.focus()
    })

    expect(activeRowHeader()?.textContent).toContain('P95')
    expect(activeColumnHeader()?.textContent).toContain('10 VUsers')

    await act(async () => {
      p95AtTenVusers?.blur()
    })

    expect(activeRowHeader()).toBeNull()
    expect(activeColumnHeader()).toBeNull()
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
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('2 telemetry intervals')
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('Mean interval ART')
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
    expect(container.querySelector('.load-impact-detail')?.textContent).toContain('1 telemetry interval')
  })

  it('explains relative scale and treats a constant metric as no variation', async () => {
    await act(async () => {
      root.render(<LoadImpactHeatmap points={[point('10:00:00', 5, { avgMs: 100, errorPct: 0 })]} />)
    })

    expect(container.textContent).toContain('Each metric uses its own observed range')
    expect(container.textContent).toContain('One observed load level')
    expect(container.textContent).toContain('No variation · 1 telemetry interval')
  })

  it('offers a key operator metrics preset in configuration', async () => {
    await act(async () => {
      root.render(
        <LoadImpactHeatmap points={[
          point('10:00:00', 5, { avgMs: 100, p50: 90, p90: 110, p95: 120, p99: 130, tps: 3, errorPct: 1, cpuProcess: 18, memProcess: 256 }),
        ]} />,
      )
    })

    await act(async () => buttonByText('Configure Map')?.click())
    await act(async () => buttonByText('Key operator metrics')?.click())

    expect(container.querySelector<HTMLInputElement>('input[value="p50"]')?.checked).toBe(false)
    expect(container.querySelector<HTMLInputElement>('input[value="p95"]')?.checked).toBe(true)
    expect(container.querySelector<HTMLInputElement>('input[value="memProcess"]')?.checked).toBe(true)
  })

  function rowLabels() {
    return Array.from(container.querySelectorAll('.load-impact-table tbody th')).map(node => node.textContent)
  }

  function bucketLabels() {
    return Array.from(container.querySelectorAll('.load-impact-table thead th span')).map(node => node.textContent)
  }

  function buttonByText(text: string) {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === text) ?? null
  }

  function sortButtonByLabel(text: string) {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('.load-impact-sort-header'))
      .find(button => button.textContent?.includes(text)) ?? null
  }

  function activeColumnHeader() {
    return container.querySelector<HTMLTableCellElement>('.load-impact-table thead th[data-axis-active="true"]')
  }

  function activeRowHeader() {
    return container.querySelector<HTMLTableCellElement>('.load-impact-table tbody th[data-axis-active="true"]')
  }
})

async function flushFocus() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}

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
