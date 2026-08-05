// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringCollectorsCard from './MonitoringCollectorsCard'
import { MonitoringCollector } from '../types'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

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
		expect(collectorsApiMock.listCollectors).toHaveBeenCalledWith({ project_id: 'proj-a', limit: 100, offset: 0 })
    expect(container.textContent).toContain('No monitoring collectors registered')
    expect(container.textContent).toContain('Register Collector')
  })

  it('exposes the register form as a labelled modal and closes it with Escape', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({ collectors: [], count: 0 })
    await act(async () => {
      root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />)
    })
    await flush()
    const registerButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Register Collector')
    await act(async () => registerButton?.click())

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('collector-create-dialog-title')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')

    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('[role="dialog"]')).toBeNull()
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

  it('ignores a late collector response from the previous project', async () => {
    let resolveProjectA: ((value: { collectors: MonitoringCollector[]; count: number }) => void) | undefined
    collectorsApiMock.listCollectors.mockImplementation(({ project_id }: { project_id: string }) => {
      if (project_id === 'proj-a') {
        return new Promise(resolve => { resolveProjectA = resolve })
      }
      return Promise.resolve({ collectors: [collector({ id: 'c-b', project_id: 'proj-b', name: 'project-b-collector' })], count: 1 })
    })

    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />) })
    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-b" canManage />) })
    await flush()
    expect(container.textContent).toContain('project-b-collector')

    await act(async () => {
      resolveProjectA?.({ collectors: [collector({ name: 'stale-project-a-collector' })], count: 1 })
    })
    await flush()
    expect(container.textContent).toContain('project-b-collector')
    expect(container.textContent).not.toContain('stale-project-a-collector')
  })

  it('closes and clears a registration draft when the project changes', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({ collectors: [], count: 0 })
    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />) })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Register Collector')?.click()
    })
    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="e.g. edge-eu-collector"]')
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(nameInput, 'project-a-draft')
      nameInput?.dispatchEvent(new Event('input', { bubbles: true }))
    })

    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-b" canManage />) })
    await flush()
    expect(container.querySelector('[role="dialog"]')).toBeNull()

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Register Collector')?.click()
    })
    expect(container.querySelector<HTMLInputElement>('input[placeholder="e.g. edge-eu-collector"]')?.value).toBe('')
  })

  it('keeps the last good collector list when a same-project refresh fails', async () => {
    collectorsApiMock.listCollectors.mockResolvedValueOnce({ collectors: [collector({ name: 'edge-eu' })], count: 1 })
    collectorsApiMock.rotateCollectorToken.mockResolvedValue({ token: 'rotated-token' })
    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />) })
    await flush()
    collectorsApiMock.listCollectors.mockRejectedValueOnce(new ApiError('collector refresh unavailable', 503))

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rotate token')?.click()
    })
    await flush()
    expect(container.textContent).toContain('edge-eu')
    expect(container.textContent).toContain('collector refresh unavailable')
  })

  it('keeps a one-time token visible and explains how to copy it when clipboard access fails', async () => {
    collectorsApiMock.listCollectors.mockResolvedValue({ collectors: [collector({ name: 'edge-eu' })], count: 1 })
    collectorsApiMock.rotateCollectorToken.mockResolvedValue({ token: 'rotated-token' })
    Object.defineProperty(document, 'execCommand', { configurable: true, value: vi.fn(() => false) })
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
    })
    await act(async () => { root.render(<MonitoringCollectorsCard projectId="proj-a" canManage />) })
    await flush()

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Rotate token')?.click()
    })
    await flush()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Copy')?.click()
    })
    await flush()

    expect(container.textContent).toContain('rotated-token')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Select the token and copy it manually')
  })

	it('guards a dirty collector registration before closing the editor', async () => {
		collectorsApiMock.listCollectors.mockResolvedValue({ collectors: [], count: 0 })
		await act(async () => {
			root.render(
				<NavigationGuardProvider>
					<MonitoringCollectorsCard projectId="proj-a" canManage />
				</NavigationGuardProvider>,
			)
		})
		await flush()
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Register Collector')?.click()
		})
		const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="e.g. edge-eu-collector"]')
		await act(async () => {
			Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(nameInput, 'edge-draft')
			nameInput?.dispatchEvent(new Event('input', { bubbles: true }))
		})
		await act(async () => {
			Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Cancel')?.click()
		})

		expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
		expect(container.querySelector<HTMLInputElement>('input[placeholder="e.g. edge-eu-collector"]')?.value).toBe('edge-draft')
		const discard = Array.from(document.querySelectorAll<HTMLButtonElement>('.unsaved-changes-modal button'))
			.find(button => button.textContent === 'Discard changes')
		await act(async () => discard?.click())
		expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
		expect(container.querySelector('input[placeholder="e.g. edge-eu-collector"]')).toBeNull()
	})
})
