// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ResultsPage from './ResultsPage'
import { downloadBlob } from '../utils/fileUtils'

const apiMock = {
  listTests: vi.fn(),
  getTestArchiveReport: vi.fn(),
  getTestArchiveSummaries: vi.fn(),
  deleteTest: vi.fn(),
  downloadTestArchive: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
;(globalThis as typeof globalThis & { ResizeObserver?: typeof ResizeObserver }).ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as typeof ResizeObserver

describe('ResultsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    apiMock.listTests.mockResolvedValue({
      tests: [
        {
          id: 'run-a',
          name: 'iPhone Checkout',
          status: 'COMPLETE',
          total_vusers: 10,
          duration: '5m',
          created_at: '2026-05-12T18:00:00Z',
          ended_at: '2026-05-12T18:05:00Z',
          groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout', protocol: 'browser', vusers: 10 }],
          archive: { transaction_count: 1000 },
        },
        {
          id: 'run-b',
          name: 'iPad Checkout',
          status: 'COMPLETE',
          total_vusers: 12,
          duration: '5m',
          created_at: '2026-05-12T19:00:00Z',
          ended_at: '2026-05-12T19:05:00Z',
          groups: [{ name: 'buyers', script_id: 'script-2', script_name: 'Checkout', protocol: 'browser', vusers: 12 }],
          archive: { transaction_count: 1100 },
        },
      ],
    })
    apiMock.getTestArchiveReport.mockImplementation(async (id: string) => ({
      manifest: { test_id: id },
      overview: {
        transactions: id === 'run-a' ? 1000 : 1100,
        errors: 0,
        error_pct: 0,
        avg_response_ms: id === 'run-a' ? 95 : 102,
        min_response_ms: 80,
        max_response_ms: 140,
        resource_samples: 0,
        interval_summaries: 0,
        peak_tps: id === 'run-a' ? 30 : 28,
        peak_active_vusers: id === 'run-a' ? 10 : 12,
        browser: {
          samples: 2,
          avg_lcp_ms: id === 'run-a' ? 240 : 280,
          avg_inp_ms: id === 'run-a' ? 56 : 64,
        },
      },
      by_action: {},
      browser_by_engine: id === 'run-a'
        ? {
            webkit: {
              samples: 5,
              browser_engine: 'webkit',
              avg_lcp_ms: 255,
              avg_cls_score: 0.03,
              avg_inp_ms: 69,
            },
            chromium: {
              samples: 1,
              browser_engine: 'chromium',
              avg_lcp_ms: 190,
              avg_cls_score: 0.015,
              avg_inp_ms: 41,
            },
          }
        : {
            webkit: {
              samples: 2,
              browser_engine: 'webkit',
              avg_lcp_ms: 280,
              avg_cls_score: 0.018,
              avg_inp_ms: 64,
            },
          },
      browser_by_platform: {
        [id === 'run-a' ? 'ios' : 'ipados']: {
          samples: id === 'run-a' ? 4 : 2,
          device_platform: id === 'run-a' ? 'ios' : 'ipados',
          browser_engine: 'webkit',
          avg_lcp_ms: id === 'run-a' ? 250 : 280,
          avg_cls_score: id === 'run-a' ? 0.029 : 0.018,
          avg_inp_ms: id === 'run-a' ? 68 : 64,
        },
      },
      browser_by_device: {
        [id === 'run-a' ? 'iPhone 14 Pro' : 'iPad Air']: {
          samples: 2,
          device_profile: id === 'run-a' ? 'iPhone 14 Pro' : 'iPad Air',
          device_platform: id === 'run-a' ? 'ios' : 'ipados',
          device_tier: id === 'run-a' ? 'mobile' : 'tablet',
          browser_engine: 'webkit',
          avg_lcp_ms: id === 'run-a' ? 240 : 280,
          avg_cls_score: id === 'run-a' ? 0.023 : 0.018,
          avg_inp_ms: id === 'run-a' ? 56 : 64,
        },
        ...(id === 'run-a'
          ? {
              'iPhone SE': {
                samples: 1,
                device_profile: 'iPhone SE',
                device_platform: 'ios',
                device_tier: 'low',
                browser_engine: 'webkit',
                avg_lcp_ms: 260,
                avg_cls_score: 0.031,
                avg_inp_ms: 72,
              },
            }
          : {}),
      },
      resource_peaks: {
        cpu_process_pct: 10,
        mem_process_mb: 100,
        net_sent_kbps: 0,
        net_recv_kbps: 0,
        disk_read_kbps: 0,
        disk_write_kbps: 0,
      },
    }))
    apiMock.getTestArchiveSummaries.mockResolvedValue({ summaries: [] })
    apiMock.deleteTest.mockResolvedValue(undefined)
    apiMock.downloadTestArchive.mockResolvedValue(new Blob())

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

  it('shows archived browser metrics in the two-run comparison cards', async () => {
    await act(async () => {
      root.render(
        <ResultsPage
          testStatuses={{}}
          onSelectTest={vi.fn()}
          activeApplication={{ id: 'app-1', name: 'Storefront', created_at: '2026-05-01T00:00:00Z', updated_at: '2026-05-01T00:00:00Z' }}
          activeRelease={{ id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '2026-05-02T00:00:00Z', updated_at: '2026-05-02T00:00:00Z' }}
        />,
      )
    })

    await act(async () => {
      await Promise.resolve()
    })

    const rowCheckboxes = Array.from(container.querySelectorAll('tbody input[type="checkbox"]')) as HTMLInputElement[]
    expect(rowCheckboxes).toHaveLength(2)

    await act(async () => {
      rowCheckboxes[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[0].dispatchEvent(new Event('change', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      rowCheckboxes[1].dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })

    await act(async () => {
      await Promise.resolve()
    })

    const platformToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Platform') as HTMLButtonElement | undefined
    const engineToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Engine') as HTMLButtonElement | undefined
    const overallToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Overall') as HTMLButtonElement | undefined
    const deviceToggle = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Device') as HTMLButtonElement | undefined
    const exportButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Export CSV') as HTMLButtonElement | undefined

    expect(platformToggle).toBeDefined()
    expect(engineToggle).toBeDefined()
    expect(overallToggle).toBeDefined()
    expect(deviceToggle).toBeDefined()
    expect(exportButton).toBeDefined()

    expect(container.textContent).toContain('Run Comparison')
    expect(container.textContent).toContain('Avg LCP')
    expect(container.textContent).toContain('240.0 ms')
    expect(container.textContent).toContain('280.0 ms')
    expect(container.textContent).toContain('Avg INP')
    expect(container.textContent).toContain('56.0 ms')
    expect(container.textContent).toContain('64.0 ms')
    expect(container.textContent).not.toContain('Platform Rollup')
    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('iPhone 14 Pro')
    expect(container.textContent).toContain('iPhone SE')
    expect(container.textContent).toContain('iOS · WebKit · mobile tier')
    expect(container.textContent).toContain('iPad Air')
    expect(container.textContent).toContain('iPadOS · WebKit · tablet tier')

    if (!platformToggle || !engineToggle || !overallToggle || !deviceToggle) {
      return
    }

    await act(async () => {
      platformToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Platform Rollup')
    expect(container.textContent).toContain('iOS')
    expect(container.textContent).toContain('WebKit · 4 samples')
    expect(container.textContent).toContain('250.0 ms')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    await act(async () => {
      engineToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Engine Rollup')
    expect(container.textContent).toContain('Chromium')
    expect(container.textContent).toContain('190.0 ms')
    expect(container.textContent).toContain('WebKit')
    expect(container.textContent).toContain('255.0 ms')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    if (!exportButton) {
      return
    }

    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(downloadBlob).toHaveBeenCalledTimes(1)
    const [blob, filename] = vi.mocked(downloadBlob).mock.calls[0]
    expect(filename).toBe('browser-comparison-engine.csv')
    const csv = await blob.text()
    expect(csv).toContain('breakdown,segment_key,device_platform,browser_engine')
    expect(csv).toContain('engine,webkit,,webkit')
    expect(csv).toContain('engine,chromium,,chromium')

    await act(async () => {
      overallToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Platform Rollup')
    expect(container.textContent).not.toContain('Devices')
    expect(container.textContent).not.toContain('iPhone 14 Pro')

    await act(async () => {
      deviceToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('iPhone 14 Pro')
  })
})