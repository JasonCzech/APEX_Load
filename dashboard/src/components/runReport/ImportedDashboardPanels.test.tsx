// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ImportedDashboardGraphsResponse, TestSummary } from '../../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const getImportedDashboardGraphs = vi.fn<() => Promise<ImportedDashboardGraphsResponse>>()

vi.mock('../../hooks/api/useSystemApi', () => ({
  useSystemApi: () => ({ getImportedDashboardGraphs }),
}))

// ResizeObserver is required by recharts' ResponsiveContainer in jsdom.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub

import ImportedDashboardPanels from './ImportedDashboardPanels'

function completedTest(): TestSummary {
  return {
    id: 'run-1', name: 'Run', status: 'COMPLETE', total_vusers: 10, duration: '10m',
    project_id: 'proj-a', created_at: '2026-06-12T12:00:00Z', started_at: '2026-06-12T12:00:00Z', ended_at: '2026-06-12T12:10:00Z',
    groups: [{ name: 'g1', protocol: 'http', script_id: 's1', script_name: 'S1', vusers: 10 }],
  }
}

describe('ImportedDashboardPanels', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    getImportedDashboardGraphs.mockReset()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
  })

  const render = async (test: TestSummary | null) => {
    await act(async () => {
      root.render(<ImportedDashboardPanels test={test} />)
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  it('renders nothing when the run had no imported dashboards', async () => {
    getImportedDashboardGraphs.mockResolvedValue({ enabled: true, configured: true, generated_at: '', dashboards: [] })
    await render(completedTest())
    expect(container.textContent).not.toContain('Imported Dashboards')
  })

  it('renders the snapshot panels (line card + stat tile) for a completed run', async () => {
    getImportedDashboardGraphs.mockResolvedValue({
      enabled: true, configured: true, generated_at: '',
      dashboards: [{
        dashboard_id: 'd1', dashboard_title: 'Infra',
        panels: [
          { panel_id: 1, title: 'CPU', kind: 'line', series: [{ key: 'k', label: 'a', values: [{ timestamp: '', timestamp_ms: 1, value: 5 }] }] },
          { panel_id: 2, title: 'Up', kind: 'single_value', value: 1 },
        ],
      }],
    })
    await render(completedTest())
    expect(container.textContent).toContain('Imported Dashboards')
    expect(container.textContent).toContain('Infra')
    expect(container.textContent).toContain('CPU')
    expect(container.textContent).toContain('Up')
  })
})
