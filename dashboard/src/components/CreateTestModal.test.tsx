// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestModal from './CreateTestModal'
import { hasGoalModeConfiguration, validateGoalConfig } from './goalConfigValidation'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import type { ScenarioDraft } from '../types'

const apiMock = {
  estimateTest: vi.fn(),
  createTest: vi.fn(),
  getSystemInventory: vi.fn(),
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

    const pacingSelect = Array.from(container.querySelectorAll('select')).find(
      select => select.parentElement?.textContent?.includes('Pacing'),
    ) as HTMLSelectElement | undefined
    const thinkTimeInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'number' && input.parentElement?.textContent?.includes('Think Time Override'),
    ) as HTMLInputElement | undefined
    const continueOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Continue on error'),
    ) as HTMLInputElement | undefined
    const snapshotOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Snapshot on error'),
    ) as HTMLInputElement | undefined

    expect(pacingSelect).toBeDefined()
    expect(thinkTimeInput).toBeDefined()
    expect(continueOnErrorCheckbox).toBeDefined()
    expect(snapshotOnErrorCheckbox).toBeDefined()
    if (!pacingSelect || !thinkTimeInput || !continueOnErrorCheckbox || !snapshotOnErrorCheckbox) {
      return
    }
    expect(continueOnErrorCheckbox.checked).toBe(false)
    expect(snapshotOnErrorCheckbox.checked).toBe(false)

    await act(async () => {
      pacingSelect.value = 'fixed_delay'
      pacingSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const delayInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'number' && input.parentElement?.textContent?.includes('Pacing delay'),
    ) as HTMLInputElement | undefined

    expect(delayInput).toBeDefined()
    if (!delayInput) {
      return
    }

    await act(async () => {
      setInputValue(delayInput, '1200')
      setInputValue(thinkTimeInput, '900')
      continueOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      snapshotOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const submitButton = Array.from(container.querySelectorAll('button')).find(
      button => button.textContent?.includes('Create Queued Run'),
    ) as HTMLButtonElement | undefined

    expect(submitButton).toBeDefined()
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
      runtime_settings: {
        pacing: { type: 'fixed_delay', delay_ms: 1200 },
        think_time: { type: 'fixed', mean_ms: 900, ignore: false },
        error_handling: { continue_on_error: true, snapshot_on_error: true },
      },
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

    await click('Create Queued Run')
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
    expect(eastWeight).toBeDefined()
    expect(lossTolerance?.checked).toBe(true)
    if (!eastWeight || !lossTolerance) return

    await act(async () => {
      setInputValue(eastWeight, '80')
      lossTolerance.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await click('Create Queued Run')

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
    await click('Create Queued Run')
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

    await click('Create Revised Copy')

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
      [{ stop_on_max_errors: true }, 'max error count'],
    ] as const

    for (const [goalConfig, expectedMessage] of invalidCases) {
      expect(validateGoalConfig(goalConfig)).toContain(expectedMessage)
    }
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
    expect(nameInput).toBeDefined()
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
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Create Queued Run')
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
