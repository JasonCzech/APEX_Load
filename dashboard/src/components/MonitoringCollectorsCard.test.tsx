// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringCollectorsCard from './MonitoringCollectorsCard'
import { MonitoringCollector } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const collectorsApiMock = {
  listCollectors: vi.fn(),
  getCollector: vi.fn(),
  createCollector: vi.fn(),
  patchCollector: vi.fn(),
  deleteCollector: vi.fn(),
  rotateCollectorToken: vi.fn(),
  getCollectorMetrics: vi.fn(),
}

vi.mock('../hooks/api/useMonitoringCollectorsApi', () => ({
  useMonitoringCollectorsApi: () => collectorsApiMock,
}))

let container: HTMLElement
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

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function collector(overrides: Partial<MonitoringCollector>): MonitoringCollector {
  return {
    id: 'c1',
    project_id: 'proj-a',
    name: 'edge-eu',
    health: 'healthy',
    allowed_metric_packs: ['oracle-db'],
    created_at: '',
    updated_at: '',
    ...overrides,
  }
}

describe('MonitoringCollectorsCard', () => {
  it('shows the empty state and Register action for an active project', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({ collectors: [], count: 0 })
    await act(async () => {
      root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />)
    })
    await flush()
    expect(collectorsApiMock.listCollectors).toHaveBeenCalledWith({ project_id: 'proj-a' })
    expect(container.textContent).toContain('No monitoring collectors registered')
    expect(container.textContent).toContain('Register Collector')
  })

  it('prompts to select a project when none is active and does not fetch', async () => {
    await act(async () => {
      root.render(<MonitoringCollectorsCard canManage />)
    })
    await flush()
    expect(collectorsApiMock.listCollectors).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Select a project')
  })

  it('renders collectors with health pills and hides management when not permitted', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({
      collectors: [collector({ name: 'edge-eu', health: 'healthy' })],
      count: 1,
    })
    await act(async () => {
      root.render(<MonitoringCollectorsCard projectId="proj-a" canManage={false} />)
    })
    await flush()
    expect(container.textContent).toContain('edge-eu')
    expect(container.querySelector('.pp-pill-success')).toBeTruthy()
    expect(container.textContent).not.toContain('Register Collector')
    expect(container.textContent).not.toContain('Rotate token')
  })

  it('shows the live metrics snapshot when Metrics is clicked (read-only, no manage role)', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({
      collectors: [collector({ id: 'c1', name: 'edge-eu', health: 'healthy' })],
      count: 1,
    })
    collectorsApiMock.getCollectorMetrics.mockResolvedValue({
      collector_id: 'c1',
      health: 'healthy',
      received_at: '2026-07-02T10:30:00Z',
      age_seconds: 3,
      stale: false,
      packs: [{ pack: 'linux-hosts', timestamp: '2026-07-02T10:30:00Z', samples: { cpu_pct: 12.5, load_avg_1m: 0.4 } }],
    })
    await act(async () => {
      root.render(<MonitoringCollectorsCard projectId="proj-a" canManage={false} />)
    })
    await flush()
    const metricsBtn = [...container.querySelectorAll('button')].find(b => b.textContent === 'Metrics')
    expect(metricsBtn).toBeTruthy()
    await act(async () => {
      ;(metricsBtn as HTMLElement).click()
    })
    await flush()
    expect(collectorsApiMock.getCollectorMetrics).toHaveBeenCalledWith('c1')
    expect(container.textContent).toContain('online')
    expect(container.textContent).toContain('linux-hosts')
    expect(container.textContent).toContain('cpu_pct')
  })

  it('maps unreachable health to the muted pill', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({
      collectors: [collector({ health: 'unreachable' })],
      count: 1,
    })
    await act(async () => {
      root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />)
    })
    await flush()
    expect(container.querySelector('.pp-pill-muted')).toBeTruthy()
    expect(container.querySelector('.pp-pill-success')).toBeFalsy()
  })
})
