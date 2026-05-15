// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SettingsPage from './SettingsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getSystemSettings: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  updateSystemSettings: vi.fn(),
  listDataPools: vi.fn(),
  getDataPool: vi.fn(),
  createDataPool: vi.fn(),
  updateDataPool: vi.fn(),
  deleteDataPool: vi.fn(),
  createDataPoolRow: vi.fn(),
  updateDataPoolRow: vi.fn(),
  deleteDataPoolRow: vi.fn(),
  listTests: vi.fn(),
}

const applicationApiMock = {
  listReleases: vi.fn(),
  updateApplication: vi.fn(),
  deleteApplication: vi.fn(),
  updateRelease: vi.fn(),
  archiveRelease: vi.fn(),
  deleteRelease: vi.fn(),
}

const applicationContextMock = {
  applications: [],
  releases: [],
  activeApplication: null,
  activeRelease: null,
  setActiveApplication: vi.fn(),
  setActiveRelease: vi.fn(),
  refreshApplications: vi.fn(),
  refreshReleases: vi.fn(),
  createAndSelectApplication: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationContextMock,
}))

describe('SettingsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getSystemSettings.mockResolvedValue({
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
      },
      thresholds: {
        max_error_rate_pct: undefined,
        max_p99_latency_ms: undefined,
        max_avg_server_processing_ms: undefined,
        max_avg_transfer_ms: undefined,
      },
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 4,
          memory_mb: 8192,
          network_mbps: 1000,
          max_vusers: 10000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 1,
        },
      },
    })
    apiMock.getHealth.mockResolvedValue({ status: 'healthy', version: '0.1.0', ws_clients: 1, test_count: 0 })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'controller-1',
        status: 'healthy',
        address: ':8080',
        version: '0.1.0',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'stormrunner-generator-0.stormrunner-generator:9090',
          generator_id: 'stormrunner-generator-0',
          status: 'connected',
          available_capacity: 9600,
          active_vusers: 400,
          cpu_percent: 12,
          memory_bytes: 128 * 1024 * 1024,
          goroutine_count: 120,
	          cpu_limit_cores: 4,
	          memory_limit_bytes: 8 * 1024 * 1024 * 1024,
	          max_vusers: 10000,
        },
        {
          target: 'stormrunner-generator-1.stormrunner-generator:9090',
          generator_id: 'stormrunner-generator-1',
          status: 'connected',
          available_capacity: 7500,
          active_vusers: 500,
          cpu_percent: 15,
          memory_bytes: 140 * 1024 * 1024,
          goroutine_count: 122,
	          cpu_limit_cores: 2,
	          memory_limit_bytes: 4 * 1024 * 1024 * 1024,
	          max_vusers: 8000,
        },
      ],
    })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [] })
    apiMock.getDataPool.mockResolvedValue({ rows: [] })
    apiMock.listTests.mockResolvedValue({ tests: [] })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [] })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('defaults generator capacity fields to the connected live pod capacity when settings are still at the stock defaults', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')
    const availableGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')

    expect(cpuCoresInput?.value).toBe('2')
    expect(memoryInput?.value).toBe('4096')
    expect(maxVUsersInput?.value).toBe('8000')
    expect(availableGeneratorsInput?.value).toBe('2')
    expect(container.textContent).toContain('2 CPU / 4096 MB')
    expect(container.textContent).toContain('Up to date')
    expect(apiMock.getSystemInventory).toHaveBeenCalledTimes(1)
  })

  it('applies live pod capacity to the generator cpu, memory, max vusers, and available generators fields when requested manually', async () => {
    apiMock.getSystemSettings.mockResolvedValue({
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
      },
      thresholds: {
        max_error_rate_pct: undefined,
        max_p99_latency_ms: undefined,
        max_avg_server_processing_ms: undefined,
        max_avg_transfer_ms: undefined,
      },
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 6,
          memory_mb: 12288,
          network_mbps: 1000,
          max_vusers: 12000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 3,
        },
      },
    })

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const livePresetButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Live Pod Capacity'))
    expect(livePresetButton).toBeDefined()
    if (!livePresetButton) return

    await act(async () => {
      livePresetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')
    const availableGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')

    expect(cpuCoresInput?.value).toBe('2')
    expect(memoryInput?.value).toBe('4096')
    expect(maxVUsersInput?.value).toBe('8000')
    expect(availableGeneratorsInput?.value).toBe('2')
    expect(container.textContent).toContain('CPU set to 2')
    expect(container.textContent).toContain('memory set to 4,096 MB')
    expect(apiMock.getSystemInventory).toHaveBeenCalledTimes(1)
  })
})

function findSettingInput(root: Element, label: string) {
  const rows = Array.from(root.querySelectorAll('.setting-row'))
  const row = rows.find(item => item.textContent?.includes(label))
  return row?.querySelector('input') as HTMLInputElement | null
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}