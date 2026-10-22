// @vitest-environment jsdom

import { act, StrictMode, useEffect } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { ApiError } from '../hooks/apiClient'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestModal from './CreateTestModal'
import { hasGoalModeConfiguration, validateGoalConfig } from './goalConfigValidation'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { useNavigationGuard, useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'
import type { ScenarioDraft, TestSummary } from '../types'

const monitorRequest = vi.hoisted(() => vi.fn())
const confirmActionMock = vi.hoisted(() => vi.fn<(message: string, options?: unknown) => Promise<boolean>>(async () => true))
vi.mock('./ux/confirmAction', () => ({ confirmAction: confirmActionMock }))
vi.mock('../hooks/apiClient', async importOriginal => ({ ApiError: (await importOriginal<typeof import('../hooks/apiClient')>()).ApiError, request: monitorRequest }))

const apiMock = {
  estimateTest: vi.fn(),
  createTest: vi.fn(),
  // Keyed creates reach the createTest mock; intent tests assert the key.
  createTestKeyed: vi.fn(async (...args: [data: unknown, key?: string]): Promise<{ kind: 'created' | 'replayed'; resource: unknown }> => ({ kind: 'created', resource: await apiMock.createTest(args[0]) })),
  getSystemInventory: vi.fn(),
  listELKConnections: vi.fn(),
  listDynatraceConnections: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./CreateTestCapacityPreview', () => ({
  default: () => <div data-testid="capacity-preview" />,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CreateTestModal', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    monitorRequest.mockImplementation((path: string) => Promise.resolve(path.includes('/analysis/capabilities') ? { monitoring_enabled: false } : []))
    vi.useFakeTimers()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.estimateTest.mockResolvedValue({
      duration: '1m',
      groups: [],
      assignment: { mode: 'auto' },
      footprint: {
        total_vusers: 10,
        duration: '1m',
        complexity_score: 1.2,
        estimated_cpu_cores: 0.1,
        estimated_memory_mb: 32,
        estimated_network_mbps: 1,
      },
      assignment_recommendation: {
        mode: 'auto',
        recommended_targets: ['gen-a'],
        summary: 'Auto-assignment recommends one host.',
      },
    })
    apiMock.createTest.mockResolvedValue({ id: 'test-1' })
    apiMock.listELKConnections.mockResolvedValue({ connections: [], count: 0 })
    apiMock.listDynatraceConnections.mockResolvedValue({ connections: [], count: 0 })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it.each([
    ['inherit', undefined],
    ['off', []],
    ['custom', [{ source_id: 'elastic-metrics', required: true }, { source_id: 'opensearch-logs', required: false }]],
  ] as const)('preserves %s monitor bindings when revising a queued run', async (_mode, bindings) => {
    const initialScenario = weightedScenarioDraft()
    initialScenario.revisionContext = { projectId: 'p', sourceTestId: 'original', folderId: null, dataPools: [], monitorBindings: bindings?.map(binding => ({ ...binding })) }
    await act(async () => root.render(<CreateTestModal mode="revise-queued-run" scripts={[{ id: 'script-1', name: 'Checkout', protocol: 'http', created_at: '' } as never]} initialScenario={initialScenario} onClose={vi.fn()} onCreated={vi.fn()} />))
    await click('Create revised copy')
    const payload = apiMock.createTest.mock.calls[0][0]
    if (bindings === undefined) expect(payload).not.toHaveProperty('monitor_bindings')
    else expect(payload.monitor_bindings).toEqual(bindings)
  })

  it('allows several optional sources and an explicitly required source in the modal', async () => {
    monitorRequest.mockImplementation((path: string) => Promise.resolve(path.includes('/analysis/capabilities') ? { monitoring_enabled: true } : [
      { id: 'elastic-metrics', name: 'Elastic metrics', pack: 'elastic', enabled: true },
      { id: 'opensearch-logs', name: 'OpenSearch logs', pack: 'elastic', enabled: true },
    ]))
    await act(async () => root.render(<CreateTestModal projectId="p" scripts={[{ id: 'script-1', name: 'Checkout', protocol: 'http', created_at: '' } as never]} initialScenario={weightedScenarioDraft()} onClose={vi.fn()} onCreated={vi.fn()} />))
    const bindings = container.querySelector('[aria-label="Run monitor selection"]')!
    const mode = bindings.querySelector('select')!
    await act(async () => { mode.value = 'custom'; mode.dispatchEvent(new Event('change', { bubbles: true })) })
    const check = async (name: string) => {
      const input = Array.from(bindings.querySelectorAll('label')).find(label => label.textContent === name)!.querySelector<HTMLInputElement>('input')!
      await act(async () => input.click())
    }
    await check('Elastic metrics (elastic)')
    await check('OpenSearch logs (elastic)')
    await check('Require Elastic metrics readiness and complete evidence')
    await click('Create queued run')
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({ monitor_bindings: [{ source_id: 'elastic-metrics', required: true }, { source_id: 'opensearch-logs', required: false }] }))
  })

  it('submits richer runtime overrides in the create payload', async () => {
    const onCreated = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          onClose={vi.fn()}
          onCreated={onCreated}
        />,
      )
    })

    await act(async () => {
      vi.advanceTimersByTime(300)
      await Promise.resolve()
    })

    // The test-level overrides mount the shared runtime editor: drive its tabs.
    const clickRuntimeTab = async (label: string) => {
      const tab = Array.from(container.querySelectorAll('button[role="tab"]')).find(
        button => button.textContent?.trim() === label,
      ) as HTMLButtonElement | undefined
      expect(tab, `runtime tab ${label}`).toBeDefined()
      await act(async () => {
        tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }
    const controlByLabel = <T extends HTMLElement>(labelText: string, selector: string): T | undefined =>
      Array.from(container.querySelectorAll('label'))
        .find(label => label.textContent?.includes(labelText))
        ?.querySelector(selector) as T | undefined

    await clickRuntimeTab('Iteration Pacing')
    const pacingSelect = controlByLabel<HTMLSelectElement>('Pacing', 'select')
    expect(pacingSelect).toBeTruthy()
    if (!pacingSelect) return
    await act(async () => {
      pacingSelect.value = 'fixed_delay'
      pacingSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const delayInput = controlByLabel<HTMLInputElement>('Pacing delay (seconds)', 'input')
    expect(delayInput).toBeTruthy()
    if (!delayInput) return
    await act(async () => {
      setInputValue(delayInput, '1.2')
    })

    await clickRuntimeTab('Think Time')
    const distributionSelect = controlByLabel<HTMLSelectElement>('Distribution', 'select')
    expect(distributionSelect).toBeTruthy()
    if (!distributionSelect) return
    await act(async () => {
      distributionSelect.value = 'fixed'
      distributionSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const thinkTimeInput = controlByLabel<HTMLInputElement>('Mean (ms)', 'input')
    expect(thinkTimeInput).toBeTruthy()
    if (!thinkTimeInput) return
    await act(async () => {
      setInputValue(thinkTimeInput, '900')
    })

    await clickRuntimeTab('Error Handling')
    const continueOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Continue on error'),
    ) as HTMLInputElement | undefined
    const snapshotOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Snapshot on error'),
    ) as HTMLInputElement | undefined
    expect(continueOnErrorCheckbox).toBeTruthy()
    expect(snapshotOnErrorCheckbox).toBeTruthy()
    if (!continueOnErrorCheckbox || !snapshotOnErrorCheckbox) return
    // Continue-on-error is enabled for every scenario unless it is explicitly
    // disabled, so the box starts checked; clicking it is the explicit opt-out,
    // which has to reach the payload as a literal false rather than as silence.
    expect(continueOnErrorCheckbox.checked).toBe(true)
    expect(snapshotOnErrorCheckbox.checked).toBe(false)
    await act(async () => {
      continueOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      snapshotOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const submitButton = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.includes('Create queued run'),
    ) as HTMLButtonElement | undefined

    expect(submitButton).toBeTruthy()
    if (!submitButton) {
      return
    }

    await act(async () => {
      submitButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      duration: '1m',
      scenario_mode: 'vuser_driven',
      runtime_settings: expect.objectContaining({
        pacing: { type: 'fixed_delay', delay_ms: 1200 },
        think_time: expect.objectContaining({ type: 'fixed', mean_ms: 900 }),
        error_handling: expect.objectContaining({ continue_on_error: false, snapshot_on_error: true }),
      }),
    }))
    expect(onCreated).toHaveBeenCalledWith({ id: 'test-1' })
  })

  it('blocks an observer setup above the backend diagnostic envelope', async () => {
    const initialScenario: ScenarioDraft = {
      ...weightedScenarioDraft(),
      groups: [{
        ...weightedScenarioDraft().groups[0],
        scriptId: 'script-browser',
        role: 'observer',
        vusers: 11,
      }],
    }
    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-browser', name: 'Browser Journey', protocol: 'browser', created_at: '2026-05-12T00:00:00Z' } as never]}
          initialScenario={initialScenario}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    await act(async () => {
      container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
      await Promise.resolve()
    })

    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('observer canaries must use between 1 and 10 VUsers')
  })

  it('blocks aggregate staged demand above the backend one-million VUser limit', async () => {
    const initialScenario: ScenarioDraft = {
      ...weightedScenarioDraft(),
      assignment: { mode: 'auto' },
      groups: [
        { ...weightedScenarioDraft().groups[0], name: 'Browse', scriptId: 'script-1', vusers: 600_000 },
        {
          ...weightedScenarioDraft().groups[0],
          name: 'Checkout',
          scriptId: 'script-2',
          vusers: 400_001,
          stages: [{ type: 'hold', duration: '2m', target_vusers: 400_001 }],
        },
      ],
    }
    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[
            { id: 'script-1', name: 'Browse Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never,
            { id: 'script-2', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never,
          ]}
          initialScenario={initialScenario}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    await act(async () => {
      container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
      await Promise.resolve()
    })

    expect(apiMock.createTest).not.toHaveBeenCalled()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('aggregate peak demand cannot exceed 1,000,000 VUsers')
  })

  it('shows and preserves a weighted queued-run assignment on a no-op revision', async () => {
    const initialScenario = weightedScenarioDraft()
    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          initialScenario={initialScenario}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    const placement = container.querySelector('[aria-label="Scenario generator placement"]')
    const weighted = Array.from(placement?.querySelectorAll('label') || [])
      .find(label => label.textContent?.includes('Region-weighted placement'))
    const auto = Array.from(placement?.querySelectorAll('label') || [])
      .find(label => label.textContent?.includes('Automatic placement'))
    expect(weighted?.querySelector('input')?.checked).toBe(true)
    expect(auto?.querySelector('input')?.checked).toBe(false)
    expect(container.textContent).toContain('east=70, west=30')

    await click('Create queued run')
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      assignment: {
        mode: 'region_weighted',
        region_weights: { east: 70, west: 30 },
        tolerate_generator_loss: true,
      },
    }))
  })

  it('authors the complete queued-run root placement instead of preserving weighted values read-only', async () => {
    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          initialScenario={weightedScenarioDraft()}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    const placement = container.querySelector('[aria-label="Scenario generator placement"]')
    expect(Array.from(placement?.querySelectorAll('label') || []).map(label => label.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Automatic placement'),
      expect.stringContaining('Pin to host(s)'),
      expect.stringContaining('Region-weighted placement'),
      expect.stringContaining('Load-zone-weighted placement'),
      expect.stringContaining('Require capability'),
    ]))

    const eastWeight = placement?.parentElement?.querySelector<HTMLInputElement>('[aria-label="Region east weight"]')
    const lossTolerance = Array.from(placement?.parentElement?.querySelectorAll('label') || [])
      .find(label => label.textContent?.includes('Tolerate generator loss'))
      ?.querySelector<HTMLInputElement>('input')
    expect(eastWeight).toBeTruthy()
    expect(lossTolerance?.checked).toBe(true)
    expect(lossTolerance).toBeTruthy()
    if (!eastWeight || !lossTolerance) return

    await act(async () => {
      setInputValue(eastWeight, '80')
      lossTolerance.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await click('Create queued run')

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      assignment: {
        mode: 'region_weighted',
        region_weights: { east: 80, west: 30 },
      },
    }))
  })

  it('clears queued-run weighting when the operator explicitly chooses auto', async () => {
    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          initialScenario={weightedScenarioDraft()}
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    await clickPlacementMode('Automatic placement')
    await click('Create queued run')
    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      assignment: { mode: 'auto', tolerate_generator_loss: true },
    }))
  })

  it('preserves hierarchy provenance and the data-pool snapshot on a queued-run revision', async () => {
    const initialScenario = {
      ...weightedScenarioDraft(),
      revisionContext: {
        sourceTestId: 'test-original',
        applicationId: 'application-original',
        releaseId: 'release-original',
        scenarioId: 'scenario-original',
        projectId: 'project-original',
        folderId: null,
        dataPools: [{
          id: 'binding-original',
          scenario_id: 'scenario-original',
          pool_id: 'pool-original',
          pool_name: 'Original Orders',
          alias: 'orders',
          access_mode: 'read_only',
          selection_strategy: 'sequential',
        }],
      },
    } as never

    await act(async () => {
      root.render(
        <CreateTestModal
          scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
          projectId="project-current"
          folderId="folder-current"
          initialScenario={initialScenario}
          mode="revise-queued-run"
          onClose={vi.fn()}
          onCreated={vi.fn()}
        />,
      )
    })

    await click('Create revised copy')

    expect(apiMock.createTest).toHaveBeenCalledWith(expect.objectContaining({
      application_id: 'application-original',
      release_id: 'release-original',
      scenario_id: 'scenario-original',
      revision_source_test_id: 'test-original',
      project_id: 'project-original',
      folder_id: null,
      data_pools: [{
        id: 'binding-original',
        scenario_id: 'scenario-original',
        pool_id: 'pool-original',
        pool_name: 'Original Orders',
        alias: 'orders',
        access_mode: 'read_only',
        selection_strategy: 'sequential',
      }],
    }))
  })

  it('submits the complete queued-copy snapshot after an actual unrelated name edit', async () => {
    const sections = { results: { message_detail: { mode: 'off' as const, include_resources: false, sample_rate: 0, max_items: 0 }, property_analytics: [{ name: 'category', extraction: 'category', values: ['original'] }] }, websocket: { callback_interval_ms: 0, compression: false, reconnect: false } }
    const source: TestSummary = { id: 'queued-source', project_id: 'project-original', application_id: 'application-original', release_id: 'release-original', name: 'Original queued source', status: 'QUEUED', created_at: '2026-10-05T00:00:00Z', duration: '1m', total_vusers: 5, scenario_mode: 'vuser_driven', runtime_settings: sections, goal_config: { transaction_response_time_sla_ms: 0.001, workload_shape_config: { closed_vuser: { scope: 'scenario', scenario: { peak_vusers: 5, ramp_up: '3s', sustain: '57s', ramp_down_enabled: false, ramp_down: '0s' } } } }, groups: [
      { name: 'orders', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', role: 'load', vusers: 3, ramp_up: '3s', ramp_down: '4s', start_offset: '1s', transaction_response_time_sla_ms: 1250, initialization: 'at_once', runtime_settings: sections },
      { name: 'staged', script_id: 'script-1', script_name: 'Checkout Script', protocol: 'http', role: 'load', vusers: 2, start_offset: '2s', transaction_response_time_sla_ms: 1500, stages: [{ type: 'hold', duration: '50s', target_vusers: 2 }], initialization: 'at_runtime', runtime_settings: { results: {}, websocket: {} } },
    ] }
    const before = structuredClone(source)
    await act(async () => root.render(<CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: source.created_at } as never]} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const name = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Scenario Name'))?.querySelector('input')
    expect(name).toBeDefined()
    await act(async () => setInputValue(name!, 'Revised name only'))
    await click('Create revised copy')
    expect(container.querySelector('[role="alert"]')?.textContent || Array.from(container.querySelectorAll<HTMLInputElement>('input')).filter(input => !input.checkValidity()).map(input => `${input.outerHTML}:${input.validationMessage}`).join('\n')).toBe('')
    expect(apiMock.createTest).toHaveBeenCalledTimes(1)
    const sent = JSON.parse(JSON.stringify(apiMock.createTest.mock.calls[0][0]))
    expect(sent).toMatchObject({ name: 'Revised name only', revision_source_test_id: source.id, project_id: source.project_id, application_id: source.application_id, release_id: source.release_id, runtime_settings: sections, goal_config: source.goal_config })
    expect(sent.groups).toMatchObject([{ name: 'orders', ramp_up: '3s', ramp_down: '4s', start_offset: '1s', transaction_response_time_sla_ms: 1250, initialization: 'at_once', runtime_settings: sections }, { name: 'staged', start_offset: '2s', transaction_response_time_sla_ms: 1500, stages: source.groups[1].stages, initialization: 'at_runtime', runtime_settings: { results: {}, websocket: {} } }])
    expect(source).toEqual(before)
    apiMock.createTest.mock.calls[0][0].runtime_settings.results.property_analytics[0].values[0] = 'copy-only'
    expect(source.runtime_settings?.results?.property_analytics?.[0].values).toEqual(['original'])
  })

  it('retains safe source capture IDs through strict mount, unrelated edit and actual copy submission', async () => {
    apiMock.listELKConnections.mockResolvedValue({ connections: [{ id: 'source-elk', name: 'Source logs', enabled: true, status: 'ready' }], count: 1 })
    // The actual modern response distinguishes legacy configuration from native readiness.
    apiMock.listDynatraceConnections.mockResolvedValue({ connections: [{ id: 'source-dt', name: 'Source SaaS', status: 'unverified', legacy_capture_status: 'ready' }], count: 1 })
    const source = captureSource()
    const before = structuredClone(source)
    await act(async () => root.render(<StrictMode><CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} /></StrictMode>))
    expect(captureSelect('Elastic log capture').value).toBe('source-elk')
    expect(captureSelect('Dynatrace SaaS capture').value).toBe('source-dt')
    expect(captureSelect('Dynatrace SaaS capture').selectedOptions[0].textContent).toBe('Source SaaS')
    expect(apiMock.listELKConnections).toHaveBeenCalledWith('project-original')
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Renamed capture copy'))
    await click('Create revised copy')
    expect(apiMock.createTest).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ elk_capture: { connection_id: 'source-elk' }, dynatrace_capture: { connection_id: 'source-dt' } }))
    expect(source).toEqual(before)
  })

  it.each(['missing', 'failed'] as const)('retains %s source selections until capture is explicitly turned off', async failure => {
    if (failure === 'failed') {
      apiMock.listELKConnections.mockRejectedValue(new Error('List unavailable'))
      apiMock.listDynatraceConnections.mockRejectedValue(new Error('List unavailable'))
    }
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(captureSource())} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const submit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Create revised copy'))!
    for (const label of ['Elastic log capture', 'Dynatrace SaaS capture']) {
      const select = captureSelect(label)
      expect(select.value).not.toBe('')
      expect(select.selectedOptions[0].textContent).toContain('review required')
    }
    expect(submit.disabled).toBe(true)
    expect(apiMock.createTest).not.toHaveBeenCalled()
    for (const label of ['Elastic log capture', 'Dynatrace SaaS capture']) await act(async () => { const select = captureSelect(label); select.value = ''; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(submit.disabled).toBe(false)
    await click('Create revised copy')
    expect(apiMock.createTest).toHaveBeenCalledOnce()
    expect(apiMock.createTest.mock.calls[0][0]).not.toHaveProperty('elk_capture')
    expect(apiMock.createTest.mock.calls[0][0]).not.toHaveProperty('dynatrace_capture')
  })

  it('retains source capture review when peer listing APIs are unavailable', async () => {
    const originalELK = apiMock.listELKConnections
    const originalDynatrace = apiMock.listDynatraceConnections
    Reflect.deleteProperty(apiMock, 'listELKConnections')
    Reflect.deleteProperty(apiMock, 'listDynatraceConnections')
    try {
      await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(captureSource())} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
      expect(captureSelect('Elastic log capture').value).toBe('source-elk')
      expect(captureSelect('Dynatrace SaaS capture').value).toBe('source-dt')
      expect(container.textContent).toContain('review required')
      expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Create revised copy'))?.disabled).toBe(true)
      expect(apiMock.createTest).not.toHaveBeenCalled()
    } finally {
      Reflect.set(apiMock, 'listELKConnections', originalELK)
      Reflect.set(apiMock, 'listDynatraceConnections', originalDynatrace)
    }
  })

  it('clears capture IDs only on a real project change and ignores old project list completions', async () => {
    let finishOld!: (value: { connections: { id: string; name: string; status: string }[]; count: number }) => void
    apiMock.listELKConnections.mockImplementation((project: string) => project === 'project-old' ? Promise.resolve({ connections: [{ id: 'old-elk', name: 'Old logs', enabled: true, status: 'ready' }], count: 1 }) : Promise.resolve({ connections: [], count: 0 }))
    apiMock.listDynatraceConnections.mockImplementation((project: string) => project === 'project-old' ? Promise.resolve({ connections: [{ id: 'old-dt', name: 'Old SaaS', status: 'ready' }], count: 1 }) : Promise.resolve({ connections: [], count: 0 }))
    const render = async (project: string) => { await act(async () => root.render(<CreateTestModal projectId={project} scripts={captureScripts} onClose={vi.fn()} onCreated={vi.fn()} />)) }
    await render('project-old')
    for (const [label, id] of [['Elastic log capture', 'old-elk'], ['Dynatrace SaaS capture', 'old-dt']]) await act(async () => { const select = captureSelect(label); select.value = id; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await render('project-old')
    expect(captureSelect('Elastic log capture').value).toBe('old-elk')
    // Start an old-project refresh, then move scope before it completes.
    apiMock.listELKConnections.mockImplementation((project: string) => project === 'project-old' ? new Promise(resolve => { finishOld = resolve }) : Promise.resolve({ connections: [], count: 0 }))
    await render('project-intermediate')
    await render('project-old')
    await render('project-new')
    expect(captureSelect('Elastic log capture').value).toBe('')
    expect(captureSelect('Dynatrace SaaS capture').value).toBe('')
    await act(async () => finishOld({ connections: [{ id: 'old-elk', name: 'Stale old logs', status: 'ready' }], count: 1 }))
    expect(container.textContent).not.toContain('Stale old logs')
    await click('Create queued run')
    expect(apiMock.createTest.mock.calls[0][0].project_id).toBe('project-new')
    expect(apiMock.createTest.mock.calls[0][0]).not.toHaveProperty('elk_capture')
    expect(apiMock.createTest.mock.calls[0][0]).not.toHaveProperty('dynatrace_capture')
  })

  it.each([true, false])('submits an empty queued-copy goal only when clearing an originally present config (%s)', async hadConfig => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, scenario_mode: 'goal_oriented', ...(hadConfig ? { goal_config: { p95_latency_ms: 1000 } } : {}) } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const p95 = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('P95 Latency Ceiling'))?.querySelector('input')
    if (!p95) throw new Error('P95 latency control not found')
    if (hadConfig) { expect(p95.value).toBe('1000'); await act(async () => setInputValue(p95, '')) }
    await click('VUser-Driven')
    await click('Create revised copy')
    expect(apiMock.createTest).toHaveBeenCalledOnce()
    const sent = JSON.parse(JSON.stringify(apiMock.createTest.mock.calls[0][0]))
    if (hadConfig) expect(sent.goal_config).toEqual({})
    else expect(sent).not.toHaveProperty('goal_config')
    expect(source.goal_config).toEqual(hadConfig ? { p95_latency_ms: 1000 } : undefined)
  })

  it('offers a goal run only the ceilings its groups can produce', async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, scenario_mode: 'goal_oriented', goal_config: { p95_latency_ms: 1000 } } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const labels = Array.from(container.querySelectorAll('.goal-settings label > span')).map(span => span.textContent)
    expect(labels).toContain('Max Error Count')
    expect(labels).not.toContain('Max LCP (ms)')
    expect(labels).not.toContain('Max Partition Skew %')
  })

  it('offers a traffic-profile run its limits and guardrails, not a throughput target', async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, scenario_mode: 'goal_oriented', goal_config: { min_vusers: 1, max_vusers: 20 } } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} profileManaged mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const labels = Array.from(container.querySelectorAll('.goal-settings label > span')).map(span => span.textContent)
    expect(labels).not.toContain('Target Avg TPS')
    expect(labels).toContain('Max VUsers')
    expect(container.querySelector('.goal-settings h4')?.textContent).toBe('Limits and guardrails')
  })

  it('treats a draft carrying a TPS traffic profile as profile-managed', async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, scenario_mode: 'goal_oriented', goal_config: { min_vusers: 1, max_vusers: 20 } } as TestSummary
    const profile = { version: 1, runtime_target: 'tps', mappings: [{ series_key: 'orders', group_name: 'orders', peak_target: 4, points: [{ offset_ms: 0, target: 4 }] }] } as unknown as NonNullable<ScenarioDraft['trafficProfile']>
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={{ ...buildScenarioDraftFromTest(source), trafficProfile: profile }} onClose={vi.fn()} onCreated={vi.fn()} />))
    const labels = Array.from(container.querySelectorAll('.goal-settings label > span')).map(span => span.textContent)
    expect(labels).not.toContain('Target Avg TPS')
    expect(labels).toContain('Min VUsers')
  })

  const captureScripts = [{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]
  function captureSource(): TestSummary {
    return { id: 'capture-source', project_id: 'project-original', name: 'Capture source', status: 'QUEUED', created_at: '2026-10-05T00:00:00Z', duration: '1m', total_vusers: 1, elk_capture: { connection_id: 'source-elk', connection_name: 'Source logs' }, dynatrace_capture: { connection_id: 'source-dt', connection_name: 'Source SaaS' }, groups: [{ name: 'orders', script_id: 'script-1', vusers: 1 }] } as TestSummary
  }
  function captureSelect(label: string): HTMLSelectElement {
    const select = Array.from(container.querySelectorAll('label')).find(candidate => candidate.querySelector('span')?.textContent === label)?.querySelector('select')
    expect(select).toBeDefined()
    return select!
  }

  it('accepts valid zero-valued SLA thresholds and detects them as configured', () => {
    const validZeros = {
      max_error_pct: 0,
      max_throttled_requests: 0,
      max_partition_skew_pct: 0,
      max_redelivery_rate_pct: 0,
      max_dead_letter_rate_pct: 0,
      stop_on_sla_breach: true,
    }

    expect(hasGoalModeConfiguration(validZeros)).toBe(true)
    expect(validateGoalConfig(validZeros)).toBeNull()
    expect(hasGoalModeConfiguration({ max_throttled_requests: 0 })).toBe(true)
    expect(validateGoalConfig({ max_throttled_requests: 0, stop_on_sla_breach: true })).toBeNull()
  })

  it('validates every goal threshold range and non-finite values', () => {
    const invalidCases = [
      [{ target_average_tps: 0 }, 'target average TPS'],
      [{ target_average_tps: Number.NaN }, 'target average TPS'],
      [{ p95_latency_ms: 0 }, 'P95 latency'],
      [{ max_error_pct: -0.1 }, 'max error %'],
      [{ max_error_pct: 100.1 }, 'max error %'],
      [{ max_avg_server_processing_ms: 0 }, 'server processing'],
      [{ max_avg_transfer_ms: 0 }, 'transfer time'],
      [{ min_vusers: 0 }, 'min VUsers'],
      [{ min_vusers: 1_000_001 }, 'min VUsers'],
      [{ max_vusers: 1_000_001 }, 'max VUsers'],
      [{ min_vusers: 20, max_vusers: 10 }, 'less than or equal'],
      [{ max_error_count: 0 }, 'max error count'],
      [{ max_error_count: Number.MAX_SAFE_INTEGER + 1 }, 'safe integer'],
      [{ max_lcp_ms: 0 }, 'max LCP'],
      [{ max_inp_ms: 0 }, 'max INP'],
      [{ max_cls: 0 }, 'max CLS'],
      [{ max_throttled_requests: -1 }, 'max throttled requests'],
      [{ max_throttled_requests: Number.MAX_SAFE_INTEGER + 1 }, 'safe integer'],
      [{ max_end_to_end_latency_ms: 0 }, 'end-to-end latency'],
      [{ max_partition_skew_pct: -0.1 }, 'partition skew'],
      [{ max_partition_skew_pct: 100.1 }, 'partition skew'],
      [{ max_redelivery_rate_pct: 100.1 }, 'redelivery rate'],
      [{ max_dead_letter_rate_pct: -0.1 }, 'dead-letter rate'],
      [{ sla_breach_window: 'not-a-duration' }, 'SLA breach window'],
      [{ stop_on_sla_breach: true }, 'SLA threshold'],
      [{ transaction_response_time_sla_ms: 3000, stop_on_sla_breach: true }, 'SLA threshold'],
      [{ stop_on_max_errors: true }, 'max error count'],
    ] as const

    for (const [goalConfig, expectedMessage] of invalidCases) {
      expect(validateGoalConfig(goalConfig)).toContain(expectedMessage)
    }
  })

  it.each([false, true])('acknowledges only its own successful save before guarded navigation (other dirty editor: %s)', async otherDirty => {
    const selected = vi.fn()
    const external = { id: 'other-editor', label: 'Other draft', isDirty: () => otherDirty, save: () => false, discard: vi.fn() }
    function Harness() {
      const { runGuarded } = useNavigationGuardRunner()
      useNavigationGuard(external, otherDirty)
      return <CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={test => runGuarded(() => selected(test.id))} />
    }
    await act(async () => root.render(<NavigationGuardProvider><Harness /></NavigationGuardProvider>))
    const input = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(input, 'Saved queued draft'))
    await click('Create queued run')
    expect(apiMock.createTest).toHaveBeenCalledOnce()
    if (otherDirty) {
      expect(selected).not.toHaveBeenCalled()
      expect(container.textContent).toContain('Other draft has unsaved changes')
      expect(container.textContent).not.toContain('Create queued run has unsaved changes')
    } else {
      expect(selected).toHaveBeenCalledExactlyOnceWith('test-1')
      expect(container.textContent).not.toContain('Unsaved Changes')
    }
  })

  it('retains an editable late draft and guard when the submitted snapshot succeeds', async () => {
    let complete!: (value: TestSummary) => void
    apiMock.createTest.mockReturnValue(new Promise<TestSummary>(resolve => { complete = resolve }))
    const selected = vi.fn()
    function Harness() {
      const { runGuarded } = useNavigationGuardRunner()
      return <><button onClick={() => runGuarded(selected)}>Leave draft</button><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={selected} /></>
    }
    await act(async () => root.render(<NavigationGuardProvider><Harness /></NavigationGuardProvider>))
    const input = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(input, 'Submitted name'))
    await click('Create queued run')
    expect(apiMock.createTest.mock.calls[0][0].name).toBe('Submitted name')
    expect(input.disabled).toBe(false)
    await act(async () => setInputValue(input, 'Later unsaved name'))
    await act(async () => complete({ id: 'created-receipt' } as TestSummary))
    expect(input.value).toBe('Later unsaved name')
    expect(selected).not.toHaveBeenCalled()
    expect(container.textContent).toContain('created-receipt')
    expect(container.textContent).toContain('Edits made while saving remain unsaved')
    await click('Leave draft')
    expect(selected).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Create queued run has unsaved changes')
    expect(apiMock.createTest).toHaveBeenCalledOnce()
  })

  it('joins a history guard save to the held direct operation without creating a second run', async () => {
    let complete!: (value: TestSummary) => void
    apiMock.createTest.mockReturnValue(new Promise<TestSummary>(resolve => { complete = resolve }))
    const selected = vi.fn()
    function Harness() {
      const { runGuarded } = useNavigationGuardRunner()
      useEffect(() => {
        const onPopState = () => runGuarded(selected)
        window.addEventListener('popstate', onPopState)
        return () => window.removeEventListener('popstate', onPopState)
      }, [runGuarded])
      return <CreateTestModal scripts={captureScripts} onClose={vi.fn()} onCreated={selected} />
    }
    await act(async () => root.render(<NavigationGuardProvider><Harness /></NavigationGuardProvider>))
    const input = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(input, 'Submitted once'))
    await click('Create queued run')
    await act(async () => setInputValue(input, 'Later guarded edits'))
    await act(async () => window.dispatchEvent(new PopStateEvent('popstate')))
    expect(container.textContent).toContain('Create queued run has unsaved changes')
    await click('Save changes')
    try {
      expect(apiMock.createTest).toHaveBeenCalledOnce()
      expect(apiMock.createTest.mock.calls[0][0].name).toBe('Submitted once')
    } finally {
      await act(async () => complete({ id: 'single-created-receipt' } as TestSummary))
    }
    expect(selected).not.toHaveBeenCalled()
    expect(input.value).toBe('Later guarded edits')
    expect(container.textContent).toContain('single-created-receipt')
    expect(container.textContent).toContain('Save did not complete')
    await click('Keep editing')
    expect(input.value).toBe('Later guarded edits')
    expect(apiMock.createTest).toHaveBeenCalledOnce()
  })

  it('preserves iteration completion and its empty duration in the actual modal estimate and copy request', async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, termination_mode: 'iterations', duration: '', groups: [{ name: 'orders', script_id: 'script-1', vusers: 1, iterations: 3, iteration_override: 3, iteration_override_known: true }] } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    expect(container.textContent).toContain('All per-user iterations')
    const duration = container.querySelector<HTMLInputElement>('input[aria-label="Steady State Duration"]')!
    expect(duration.disabled).toBe(true)
    expect(duration.value).toBe('')
    await act(async () => vi.advanceTimersByTimeAsync(300))
    expect(apiMock.estimateTest).toHaveBeenCalledWith(expect.objectContaining({ termination_mode: 'iterations', duration: '', groups: [expect.objectContaining({ iterations: 3 })] }))
    await click('Create revised copy')
    const sent = apiMock.createTest.mock.calls[0][0]
    expect(sent).toMatchObject({ termination_mode: 'iterations', duration: '', revision_source_test_id: 'capture-source', groups: [expect.objectContaining({ iterations: 3 })] })
    expect(sent).not.toHaveProperty('start_delay')
    expect(sent).not.toHaveProperty('goal_config')
    expect(sent).not.toHaveProperty('runtime_settings')
  })

  it('retains finite iteration completion while clearing a positive source delay', async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, termination_mode: 'iterations', duration: '', start_delay: '10s', groups: [{ name: 'orders', script_id: 'script-1', vusers: 1, iterations: 3, iteration_override: 3, iteration_override_known: true }] } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const delay = container.querySelector<HTMLInputElement>('input[aria-label="Global Start Delay"]')!
    expect(delay.value).toBe('10')
    await act(async () => setInputValue(delay, ''))
    await click('Create revised copy')
    const sent = apiMock.createTest.mock.calls[0][0]
    expect(sent).toMatchObject({ termination_mode: 'iterations', duration: '', start_delay: '0s', revision_source_test_id: 'capture-source', groups: [expect.objectContaining({ iterations: 3 })] })
    expect(sent).not.toHaveProperty('goal_config')
    expect(source.start_delay).toBe('10s')
  })

  it('does not release or navigate a replacement modal when an unmounted save completes', async () => {
    let complete!: (value: TestSummary) => void
    apiMock.createTest.mockReturnValue(new Promise<TestSummary>(resolve => { complete = resolve }))
    const selected = vi.fn()
    function Harness({ incarnation }: { incarnation: number }) {
      const { runGuarded } = useNavigationGuardRunner()
      return <><button onClick={() => runGuarded(selected)}>Leave draft</button><CreateTestModal key={incarnation} scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={selected} /></>
    }
    const render = async (incarnation: number) => { await act(async () => root.render(<NavigationGuardProvider><Harness incarnation={incarnation} /></NavigationGuardProvider>)) }
    await render(1)
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Submitted old draft'))
    await click('Create queued run')
    await render(2)
    const current = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(current, 'Replacement unsaved draft'))
    await act(async () => complete({ id: 'old-created-receipt' } as TestSummary))
    expect(selected).not.toHaveBeenCalled()
    expect(current.value).toBe('Replacement unsaved draft')
    await click('Leave draft')
    expect(selected).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Create queued run has unsaved changes')
  })

  it('retries a queued-run create under its draft-owned key and starts another only on Save as new (AUTH-1)', async () => {
    window.localStorage.clear()
    apiMock.createTest.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await act(async () => root.render(<NavigationGuardProvider><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={vi.fn()} /></NavigationGuardProvider>))
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Keyed run'))
    await click('Create queued run')
    const key = apiMock.createTestKeyed.mock.calls[0][1]
    expect(key).toMatch(/^test-create-/)
    apiMock.createTestKeyed.mockRejectedValueOnce(new ApiError('reused', 409, { error: 'reused', code: 'idempotency_key_reused' }))
    await click('Create queued run')
    expect(apiMock.createTestKeyed.mock.calls[1][1]).toBe(key)
    expect(container.textContent).toContain('may already have created the run')
    apiMock.createTest.mockResolvedValueOnce({ id: 'run-2', name: 'Keyed run' })
    await click('Save as new')
    expect(apiMock.createTestKeyed).toHaveBeenCalledTimes(3)
    expect(apiMock.createTestKeyed.mock.calls[2][1]).not.toBe(key)
  })

  it('reports a replayed run instead of presenting it as a new queued run (AUTH-1)', async () => {
    window.localStorage.clear()
    const onCreated = vi.fn()
    apiMock.createTestKeyed.mockResolvedValueOnce({ kind: 'replayed', resource: { id: 'run-1', ref_key: 'RUN-1', status: 'RUNNING' } as never })
    await act(async () => root.render(<NavigationGuardProvider><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={onCreated} /></NavigationGuardProvider>))
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Replayed run'))
    await click('Create queued run')
    expect(onCreated).not.toHaveBeenCalled()
    expect(container.textContent).toContain('This draft already created run RUN-1 (RUNNING); no new run was queued.')
    await click('Open run')
    expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'run-1' }))
  })

  it("shows the joint evidence a revised copy keeps from its source run (NA-B)", async () => {
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, joint_evidence: { population: 'action', dimensions: ['name', 'group', 'property:operation'] } } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    // No edit here stops a promotion, so there is nothing to warn about.
    expect(container.querySelector('[data-testid="revision-joint-evidence"]')?.textContent).toBe("Joint evidence: Actions by Name, Group and Property: operation. The revised copy keeps the source run's projection.")
    // The copy inherits it: the request does not resend it.
    await click('Create revised copy')
    expect(apiMock.createTest.mock.calls[0][0]).not.toHaveProperty('joint_evidence')
  })

  it("warns when a revised copy's edits stop promoting the kept property category (NA-B)", async () => {
    const scripts = [
      { id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z', script: { config: { results: { property_analytics: [{ name: 'operation', extraction: 'operation_type', values: ['checkout'] }] } } } },
      { id: 'script-2', name: 'Browse Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' },
    ] as never[]
    const source = { ...captureSource(), elk_capture: undefined, dynatrace_capture: undefined, joint_evidence: { population: 'action', dimensions: ['name', 'property:operation'] },
      groups: [{ name: 'orders', script_id: 'script-1', vusers: 1 }, { name: 'browse', script_id: 'script-2', vusers: 1 }] } as TestSummary
    await act(async () => root.render(<CreateTestModal scripts={scripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
    const notice = () => container.querySelector('[data-testid="revision-joint-evidence"]')?.textContent
    expect(notice()).toBe("Joint evidence: Actions by Name and Property: operation. The revised copy keeps the source run's projection.")
    await act(async () => { container.querySelector<HTMLButtonElement>('button[title="Remove group"]')!.click() })
    expect(notice()).toContain('These changes stop promoting "operation": restore its promotion, or the copy may be refused.')
  })

  it('replays rather than queues another when a replayed draft is created again (AUTH-1)', async () => {
    window.localStorage.clear()
    const replay = { kind: 'replayed' as const, resource: { id: 'run-1', ref_key: 'RUN-1', status: 'QUEUED' } as never }
    apiMock.createTestKeyed.mockResolvedValueOnce(replay)
    await act(async () => root.render(<NavigationGuardProvider><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={vi.fn()} /></NavigationGuardProvider>))
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Replayed run'))
    await click('Create queued run')
    const key = apiMock.createTestKeyed.mock.calls[0][1]
    // The outcome is known: no reload record keeps the key.
    expect(window.localStorage.getItem('apexload.authoring.create-intents.v1') || '').not.toContain(String(key))
    apiMock.createTestKeyed.mockResolvedValueOnce(replay)
    await click('Create queued run')
    expect(apiMock.createTestKeyed.mock.calls[1][1]).toBe(key)
    expect(container.textContent).toContain('no new run was queued. To queue another, choose Save as new.')
    // A changed draft is refused under the same key, and the run it made stays one click away.
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Replayed run, changed'))
    apiMock.createTestKeyed.mockRejectedValueOnce(new ApiError('reused', 409, { error: 'reused', code: 'idempotency_key_reused' }))
    await click('Create queued run')
    expect(apiMock.createTestKeyed.mock.calls[2][1]).toBe(key)
    expect(container.textContent).toContain('This draft already created run RUN-1, and it has changed since. To queue another run with these changes, choose Save as new.')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Open run')).toBe(true)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent === 'Save as new')).toHaveLength(1)
    // Once that run is deleted, the notice that offered to open it goes.
    apiMock.createTestKeyed.mockRejectedValueOnce(new ApiError('deleted', 410, { error: 'deleted', code: 'idempotency_resource_deleted' }))
    await click('Create queued run')
    expect(container.textContent).toContain('was deleted and will not be recreated')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Open run')).toBe(false)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent === 'Save as new')).toHaveLength(1)
    apiMock.createTest.mockResolvedValueOnce({ id: 'run-2', name: 'Replayed run, changed' })
    await click('Save as new')
    expect(apiMock.createTestKeyed).toHaveBeenCalledTimes(5)
    expect(apiMock.createTestKeyed.mock.calls[4][1]).not.toBe(key)
  })

  it("never recalls one queued run's revise key for another revised with the same settings (AUTH-1)", async () => {
    window.localStorage.clear()
    const sourceA = { ...captureSource(), id: 'source-a', elk_capture: undefined, dynatrace_capture: undefined } as TestSummary
    const sourceB = { ...sourceA, id: 'source-b' } as TestSummary
    const revise = async (source: TestSummary) => {
      await act(async () => root.render(<></>))
      await act(async () => root.render(<CreateTestModal scripts={captureScripts} initialScenario={buildScenarioDraftFromTest(source)} mode="revise-queued-run" onClose={vi.fn()} onCreated={vi.fn()} />))
      apiMock.createTest.mockRejectedValueOnce(new TypeError('Failed to fetch'))
      await click('Create revised copy')
      const calls = apiMock.createTestKeyed.mock.calls
      return calls[calls.length - 1]?.[1]
    }
    const keyA = await revise(sourceA)
    expect(keyA).toMatch(/^test-create-/)
    expect(await revise(sourceB)).not.toBe(keyA)
    // Reopening the same source's revision recalls its own unanswered key.
    expect(await revise(sourceA)).toBe(keyA)
  })

  it('offers Save as new only beside the refusal it answers (AUTH-1)', async () => {
    window.localStorage.clear()
    await act(async () => root.render(<NavigationGuardProvider><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={vi.fn()} /></NavigationGuardProvider>))
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!, 'Refused run'))
    apiMock.createTestKeyed.mockRejectedValueOnce(new ApiError('reused', 409, { error: 'reused', code: 'idempotency_key_reused' }))
    await click('Create queued run')
    const saveAsNew = () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save as new')
    expect(saveAsNew()).toBeDefined()
    // Another failure is not that refusal, and offers no Save as new.
    apiMock.createTestKeyed.mockRejectedValueOnce(new ApiError('The run exceeds project capacity', 409, { error: 'The run exceeds project capacity', code: 'capacity_exceeded' }))
    await click('Create queued run')
    expect(container.textContent).toContain('The run exceeds project capacity')
    expect(saveAsNew()).toBeUndefined()
  })

  it('opens a replayed run over later edits only after they are explicitly discarded (AUTH-1)', async () => {
    window.localStorage.clear()
    const onCreated = vi.fn()
    apiMock.createTestKeyed.mockResolvedValueOnce({ kind: 'replayed', resource: { id: 'run-1', ref_key: 'RUN-1', status: 'QUEUED' } as never })
    await act(async () => root.render(<NavigationGuardProvider><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={onCreated} /></NavigationGuardProvider>))
    const input = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(input, 'Replayed run'))
    await click('Create queued run')
    await act(async () => setInputValue(input, 'Edited after the replay'))
    confirmActionMock.mockResolvedValueOnce(false)
    await click('Open run')
    // Opening the existing run never saves the later edits: it only offers
    // to discard them.
    expect(confirmActionMock).toHaveBeenCalledWith(expect.stringContaining('Discard the edits made since and open run RUN-1'), expect.objectContaining({ confirmLabel: 'Discard edits' }))
    expect(onCreated).not.toHaveBeenCalled()
    confirmActionMock.mockResolvedValueOnce(true)
    await click('Open run')
    expect(onCreated).toHaveBeenCalledTimes(1)
    expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: 'run-1' }))
    expect(apiMock.createTestKeyed).toHaveBeenCalledTimes(1)
  })

  it('retains its navigation guard and draft after a refused create', async () => {
    apiMock.createTest.mockRejectedValue(new Error('Source has started'))
    const selected = vi.fn()
    function Harness() {
      const { runGuarded } = useNavigationGuardRunner()
      return <><button onClick={() => runGuarded(selected)}>Leave draft</button><CreateTestModal scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]} onClose={vi.fn()} onCreated={selected} /></>
    }
    await act(async () => root.render(<NavigationGuardProvider><Harness /></NavigationGuardProvider>))
    const input = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')!
    await act(async () => setInputValue(input, 'Refused draft'))
    await click('Create queued run')
    expect(container.textContent).toContain('Source has started')
    expect(input.value).toBe('Refused draft')
    await click('Leave draft')
    expect(selected).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Create queued run has unsaved changes')
  })

  it('prompts before closing a dirty queued-run draft', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <CreateTestModal
            scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
            onClose={onClose}
            onCreated={vi.fn()}
          />
        </NavigationGuardProvider>,
      )
    })

    const nameInput = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.placeholder?.includes('auto-generated'))
    expect(nameInput).toBeTruthy()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Dirty queued run')
    })

    await click('Cancel')

    expect(onClose).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Unsaved Changes')

    await click('Keep editing')
    expect(container.textContent).not.toContain('Unsaved Changes')

    await click('Cancel')
    await click('Discard changes')

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes a clean queued-run draft without prompting', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <CreateTestModal
            scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
            onClose={onClose}
            onCreated={vi.fn()}
          />
        </NavigationGuardProvider>,
      )
    })

    await click('Cancel')

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Unsaved Changes')
  })

  it('exposes a labelled modal, focuses the scenario name, and supports Escape', async () => {
    const onClose = vi.fn()
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <CreateTestModal
            scripts={[{ id: 'script-1', name: 'Checkout Script', protocol: 'http', created_at: '2026-05-12T00:00:00Z' } as never]}
            onClose={onClose}
            onCreated={vi.fn()}
          />
        </NavigationGuardProvider>,
      )
    })
    await act(async () => {
      vi.advanceTimersByTime(0)
    })

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    const scenarioName = container.querySelector<HTMLInputElement>('input[placeholder*="auto-generated"]')
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Create queued run')
    expect(document.activeElement).toBe(scenarioName)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(onClose).toHaveBeenCalledOnce()
  })

  async function click(label: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(button).toBeDefined()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => Promise.resolve())
  }

  async function clickPlacementMode(label: string) {
    const placement = container.querySelector('[aria-label="Scenario generator placement"]')
    const input = Array.from(placement?.querySelectorAll('label') || [])
      .find(candidate => candidate.textContent?.includes(label))
      ?.querySelector<HTMLInputElement>('input')
    expect(input).toBeDefined()
    await act(async () => {
      input?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})

function weightedScenarioDraft(): ScenarioDraft {
  return {
    name: 'Weighted queued run',
    duration: '2m',
    startDelay: '',
    scenarioMode: 'vuser_driven',
    scriptShakeout: false,
    resourceBaseline: false,
    assignment: {
      mode: 'region_weighted',
      region_weights: { east: 70, west: 30 },
      tolerate_generator_loss: true,
    },
    goalConfig: {},
    runtimeSettings: { log_level: '', auto_transactions: '' },
    groups: [{
      name: 'Checkout',
      scriptId: 'script-1',
      vusers: 10,
      rampUp: '',
      rampDown: '',
      initialization: 'at_runtime',
    }],
  }
}

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}
