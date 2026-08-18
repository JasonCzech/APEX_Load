// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestRunManifest } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import TestExecutionManifestCard from './TestExecutionManifestCard'

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: vi.fn(),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecutionManifestCard', () => {
  let container: HTMLDivElement
  let root: Root
  let writeText: ReturnType<typeof vi.fn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    vi.mocked(downloadBlob).mockResolvedValue('saved')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('renders manifest sections and supports JSON copy and download', async () => {
    await renderCard(makeManifest())

    expect(container.textContent).toContain('Test Manifest')
    expect(container.textContent).toContain('Checkout Run')
    expect(container.textContent).toContain('Initial Configuration')
    expect(container.textContent).toContain('Current Configuration')
    expect(container.textContent).toContain('Scripts & Groups')
    expect(container.textContent).toContain('Checkout Flow')
    expect(container.textContent).toContain('Load Generators')
    expect(container.textContent).toContain('generator-a')
    expect(container.textContent).toContain('Runtime Changes')
    expect(container.textContent).toContain('VUsers changed')
    expect(container.textContent).toContain('Data Quality')
    expect(container.textContent).toContain('older archive warning')

    await click(buttonNamed('Copy JSON'))
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"test_id": "test-1"'))
    expect(container.textContent).toContain('Copied')

    await click(buttonNamed('Download JSON'))
    expect(downloadBlob).toHaveBeenCalledTimes(1)
    expect(vi.mocked(downloadBlob).mock.calls[0][1]).toBe('apexload-test-manifest-run-20260607-001.json')
  })

  it('shows runtime empty state and load errors', async () => {
    await renderCard({ ...makeManifest(), runtime_changes: [] }, { error: 'Unable to load audit events' })

    expect(container.textContent).toContain('Unable to load audit events')
    expect(container.textContent).toContain('No runtime changes recorded.')
  })

  it('falls back to a safe download filename when older manifests lack run labels', async () => {
    const manifest = {
      ...makeManifest(),
      test_id: undefined,
      ref_key: undefined,
      test_name: undefined,
    } as unknown as TestRunManifest

    await renderCard(manifest)
    await click(buttonNamed('Download JSON'))

    expect(vi.mocked(downloadBlob).mock.calls[0][1]).toBe('apexload-test-manifest-test-run.json')
  })

  it('surfaces a native manifest save failure', async () => {
    vi.mocked(downloadBlob).mockRejectedValueOnce(new Error('disk is full'))

    await renderCard(makeManifest())
    await click(buttonNamed('Download JSON'))

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not save the test manifest: disk is full')
  })

  it('shows unavailable feedback when clipboard copy does not settle', async () => {
    vi.useFakeTimers()
    writeText.mockReturnValue(new Promise<void>(() => {}))

    await renderCard(makeManifest())
    await click(buttonNamed('Copy JSON'))

    expect(container.textContent).toContain('Copying...')

    await act(async () => {
      vi.advanceTimersByTime(1500)
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Copy unavailable')
  })

  it('highlights groups whose continue-on-error setting diverges from the scenario policy', async () => {
    await renderCard({
      ...makeManifest(),
      continue_on_error: {
        scenario_value: true,
        scenario_explicit: false,
        conflicts: [{
          group_name: 'search',
          script_id: 'script-2',
          script_name: 'Search',
          effective: false,
          source: 'group',
          detail: 'continue-on-error conflict: group "search" runs disabled',
        }],
      },
    })

    expect(container.textContent).toContain('Continue On Error')
    expect(container.textContent).toContain('scenario default')
    expect(container.textContent).toContain('search')
    expect(container.textContent).toContain('Group runtime settings')
  })

  it('reports agreement when no group diverges from the scenario policy', async () => {
    await renderCard({
      ...makeManifest(),
      continue_on_error: { scenario_value: false, scenario_explicit: true },
    })

    expect(container.textContent).toContain('set in the scenario settings')
    expect(container.textContent).toContain('Every group matches the scenario policy.')
  })

  async function renderCard(manifest: TestRunManifest | null, options: { error?: string; loading?: boolean } = {}) {
    await act(async () => {
      root.render(
        <TestExecutionManifestCard
          manifest={manifest}
          error={options.error || ''}
          loading={options.loading || false}
          onReload={vi.fn()}
        />,
      )
    })
  }

  function buttonNamed(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
    })
  }
})

function makeManifest(): TestRunManifest {
  return {
    schema_version: 'apexload.test_manifest.v1',
    test_id: 'test-1',
    ref_key: 'RUN-20260607-001',
    test_name: 'Checkout Run',
    status: 'RUNNING',
    source: 'live',
    generated_at: '2026-06-07T14:10:00Z',
    identity: {
      application_id: 'app-1',
      release_id: 'rel-1',
      scenario_id: 'scenario-1',
      project_id: 'project-1',
      created_at: '2026-06-07T14:00:00Z',
      started_at: '2026-06-07T14:01:00Z',
      started_by_display_name: 'Ops User',
    },
    initial_configuration: {
      status: 'RUNNING',
      duration: '5m0s',
      total_vusers: 10,
      start_delay: '30s',
      scenario_mode: 'vuser_driven',
      script_shakeout: true,
      resource_baseline: true,
      groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 10 }],
      runtime_settings: { log_level: 'standard' },
    },
    current_configuration: {
      status: 'RUNNING',
      duration: '10m0s',
      total_vusers: 25,
      active_vusers: 22,
      scenario_mode: 'vuser_driven',
      groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 25, ramp_up: '1m', ramp_down: '30s' }],
    },
    scripts: [
      { group_name: 'buyers', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 25, ramp_up: '1m', ramp_down: '30s', initialization: 'at_runtime' },
    ],
    load_generators: {
      assignment: { mode: 'specific_hosts', selected_targets: ['generator-a'], shard_strategy: 'capacity_weighted' },
      generator_shards: [{ group: 'buyers', target: 'generator-a', vusers: 25, shard_strategy: 'capacity_weighted' }],
      generator_inventory: [{
        target: 'generator-a',
        generator_id: 'gen-a',
        status: 'ready',
        available_capacity: 1000,
        active_vusers: 25,
        cpu_percent: 20,
        memory_bytes: 1024,
        goroutine_count: 50,
        cpu_limit_cores: 4,
        memory_limit_bytes: 8589934592,
        max_vusers: 10000,
      }],
      execution_settings: { general: { max_concurrent_tests: 2 } },
    },
    runtime_changes: [{
      id: 'evt-1',
      action: 'test.vusers.scale',
      label: 'VUsers changed',
      status: 'success',
      actor_display_name: 'Ops User',
      details: { group: 'buyers', target_vusers: 25 },
      created_at: '2026-06-07T14:05:00Z',
    }],
    archive: {
      test_manifest_file: 'test_manifest.json',
      transaction_count: 50,
      completed_at: '2026-06-07T14:10:00Z',
    },
    warnings: ['older archive warning'],
  }
}
