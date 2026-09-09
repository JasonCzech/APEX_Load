// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import { type LoadModelTemplate, type StoredScenario } from '../types'
import TestHub from './TestHub'
import { duplicateScenarioGroupNameIndexes, scenarioGroupNameAfterScriptSelection } from './scenarioGroupUtils'
import { scenarioSeedFromTemplate } from './scenarioTemplateSeed'
import {
  replaceScenarioRuntimeDefaults,
  reseedScenarioGroups,
  runtimeSettingsFromForm,
  scenarioFormStateFromStoredScenario,
} from './scenarioFormModel'
import { applyEffectiveRuntimeSettingsToGroups, copyRuntimeSettingsForProtocol } from './runtimeSettingsApply'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScenarios: vi.fn(),
  listScripts: vi.fn(),
  listDataPools: vi.fn(),
  createDataPool: vi.fn(),
  createDataPoolRow: vi.fn(),
  listLoadModelTemplates: vi.fn(),
  getLoadModelSupportMatrix: vi.fn(),
  validateScenarioLoadModel: vi.fn(),
  estimateTest: vi.fn(),
  createScenario: vi.fn(),
  updateScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listScenarioResults: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  getSystemInventory: vi.fn(),
  getConfigDefaults: vi.fn().mockResolvedValue({ config: {} }),
  getTrafficProfileSources: vi.fn(),
  analyzeCSVDriven: vi.fn(),
  deriveCSVDriven: vi.fn(),
}

const libraryApiMock = {
  listRemotes: vi.fn(),
  localCatalog: vi.fn(),
  remoteCatalog: vi.fn(),
  pullRemoteItem: vi.fn(),
  pushLocalItem: vi.fn(),
  compareItems: vi.fn(),
}

const projectState = vi.hoisted(() => ({
  activeProject: {
    id: 'project-1',
    name: 'Checkout',
    created_at: '',
    updated_at: '',
  } as { id: string; name: string; created_at: string; updated_at: string; domain_id?: string } | null,
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useLibraryApi', () => ({
  useLibraryApi: () => libraryApiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => projectState,
}))

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children }: { children?: ReactNode }) => <svg data-testid="scenario-model-chart">{children}</svg>
  const MockTooltip = () => <g data-testid="scenario-model-tooltip" />
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')
  return {
    Area: ({ dataKey, name }: { dataKey?: string; name?: string }) => <path data-testid="scenario-model-area" data-key={dataKey} data-name={name} />,
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Legend: Mock,
    Line: ({ dataKey, name }: { dataKey?: string; name?: string }) => <path data-testid="scenario-model-line" data-key={dataKey} data-name={name} />,
    LineChart: MockChart,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="scenario-model-responsive">{children}</div>,
    Tooltip: MockTooltip,
    XAxis: ({ label }: { label?: string | { value?: string } }) => <g data-testid="scenario-model-x-axis" data-label={labelText(label)} />,
    YAxis: ({ label, orientation }: { label?: string | { value?: string }; orientation?: string }) => (
      <g data-testid="scenario-model-y-axis" data-label={labelText(label)} data-orientation={orientation ?? 'left'} />
    ),
  }
})

describe('scenarioGroupNameAfterScriptSelection', () => {
  it.each([
    ['', 0, 'Checkout Flow', '', 'Checkout Flow'],
    ['Group 2', 1, 'Payments Flow', '', 'Payments Flow'],
    ['Checkout Flow', 0, 'Payments Flow', 'Checkout Flow', 'Payments Flow'],
    ['Peak Buyers', 0, 'Payments Flow', 'Checkout Flow', 'Peak Buyers'],
  ])('resolves %j for group %i', (currentName, groupIndex, nextScriptName, previousScriptName, expected) => {
    expect(scenarioGroupNameAfterScriptSelection(currentName, groupIndex, nextScriptName, previousScriptName)).toBe(expected)
  })

  it('differentiates generated names when the same script is assigned more than once', () => {
    expect(scenarioGroupNameAfterScriptSelection('Group 2', 1, 'Checkout Flow', '', ['Checkout Flow', 'Group 2']))
      .toBe('Checkout Flow (#2)')
    expect(scenarioGroupNameAfterScriptSelection('Group 3', 2, 'Checkout Flow', '', ['Checkout Flow', 'Checkout Flow (#3)', 'Group 3']))
      .toBe('Checkout Flow (#4)')
  })

  it('finds duplicate names using the controller normalization rules', () => {
    expect([...duplicateScenarioGroupNameIndexes([' Buyers  East ', 'buyers east', 'Sellers'])]).toEqual([0, 1])
  })
})

describe('TestHub', () => {
  let container: HTMLDivElement
  let root: Root

  const clickButton = async (text: string) => {
    const button = Array.from(container.querySelectorAll('button')).find(item => item.textContent?.includes(text))
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const clickWizardStep = async (text: string) => {
    let button = Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
    if (!button) {
      await clickButton('Scenario Builder')
      button = Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
    }
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const clickRunSettingsTab = async (text: string) => {
    const button = Array.from(container.querySelectorAll('.scenario-run-settings-tab')).find(item => item.textContent?.includes(text))
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const wizardStepButton = (text: string) => (
    Array.from(container.querySelectorAll('.scenario-wizard-step')).find(item => item.textContent?.includes(text))
  )

  const wizardStepMarker = (text: string) => (
    wizardStepButton(text)?.querySelector('.scenario-wizard-step-marker')?.textContent || ''
  )

  const wizardStepComplete = (text: string) => (
    wizardStepButton(text)?.classList.contains('complete') || false
  )

  const chooseLibraryScriptForGroup = async (scriptName = 'Checkout Flow', groupIndex = 0) => {
    await clickWizardStep('Scripts')
    const groupCard = Array.from(container.querySelectorAll('.scenario-script-group-list-spacious .scenario-script-group-card'))[groupIndex]
      || Array.from(container.querySelectorAll('.scenario-script-group-card'))[groupIndex]
    expect(groupCard).toBeDefined()
    if (!groupCard) return

    const groupNumber = groupIndex + 1
    const pickerButton = Array.from(groupCard.querySelectorAll('button')).find(button => (
      button.getAttribute('aria-label')?.includes(`Group ${groupNumber}`)
        || button.textContent?.includes('Add Script')
        || button.textContent?.includes(scriptName)
    ))
    expect(pickerButton).toBeDefined()
    if (!pickerButton) return

    await act(async () => {
      pickerButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const useButton = Array.from(container.querySelectorAll('.scenario-existing-script-library button')).find(button => (
      button.textContent?.includes(`Use for Group ${groupNumber}`)
        && button.closest('tr')?.textContent?.includes(scriptName)
    ))
    expect(useButton).toBeDefined()
    if (!useButton) return

    await act(async () => {
      useButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()
  }

  const selectManualShapeByName = async (shapeName: string) => {
    await clickWizardStep('Shape / Source')
    const card = Array.from(container.querySelectorAll('.scenario-shape-catalog-card')).find(item => (
      item.querySelector('.scenario-support-card-header')?.textContent?.includes(shapeName)
    ))
    expect(card).toBeDefined()
    if (!card) return
    const manualButton = Array.from(card.querySelectorAll('button')).find(button => button.textContent?.includes('Use Manual Shape'))
    expect(manualButton).toBeDefined()
    if (!manualButton) return
    await act(async () => {
      manualButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  it('round-trips and edits canonical runtime sections through compact scenario controls', () => {
    const stored: StoredScenario = {
      id: 'canonical-runtime',
      name: 'Canonical runtime',
      duration: '5m',
      scripts: [],
      created_at: '',
      updated_at: '',
      script_count: 0,
      runtime_settings: {
        // Conflicting legacy aliases must be ignored while canonical sections exist.
        iterations: 99,
        auto_transactions: 'none',
        log_level: 'disabled',
        browser: { device_profile: 'legacy-device', headless: true },
        run_logic: {
          iterations: 4,
          auto_transactions: 'per_action',
          workload_model: 'open',
          action_sequence: ['login', 'browse'],
        },
        log: {
          level: 'extended',
          on_error_only: true,
          extended_options: ['advanced_trace'],
        },
        browser_config: {
          engine: 'firefox',
          device_profile: 'pixel_8',
          headless: false,
          client_metrics: true,
          viewport: { width: 1440, height: 900 },
        },
      },
    }

    const form = scenarioFormStateFromStoredScenario(stored, '')
    expect(form.runtime_iterations).toBe(4)
    expect(form.runtime_auto_transactions).toBe('per_action')
    expect(form.runtime_log_level).toBe('extended')
    expect(form.browser_device_profile).toBe('pixel_8')
    expect(form.browser_headless).toBe('false')

    const roundTripped = runtimeSettingsFromForm(form, true)
    expect(roundTripped.run_logic).toEqual(stored.runtime_settings?.run_logic)
    expect(roundTripped.log).toEqual(stored.runtime_settings?.log)
    expect(roundTripped.browser_config).toEqual(stored.runtime_settings?.browser_config)
    expect(roundTripped.iterations).toBeUndefined()
    expect(roundTripped.auto_transactions).toBe('')
    expect(roundTripped.log_level).toBe('')
    expect(roundTripped.browser).toBeUndefined()

    const edited = runtimeSettingsFromForm({
      ...form,
      runtime_iterations: 7,
      runtime_auto_transactions: 'per_iteration',
      runtime_log_level: 'standard',
      browser_device_profile: 'iphone_15_pro',
      browser_headless: 'true',
    }, true)
    expect(edited.run_logic).toEqual({
      iterations: 7,
      auto_transactions: 'per_iteration',
      workload_model: 'open',
      action_sequence: ['login', 'browse'],
    })
    expect(edited.log).toEqual({
      level: 'standard',
      on_error_only: true,
      extended_options: ['advanced_trace'],
    })
    expect(edited.browser_config).toEqual({
      engine: 'firefox',
      device_profile: 'iphone_15_pro',
      headless: true,
      client_metrics: true,
      viewport: { width: 1440, height: 900 },
    })
    expect(edited.iterations).toBeUndefined()
    expect(edited.auto_transactions).toBe('')
    expect(edited.log_level).toBe('')
    expect(edited.browser).toBeUndefined()
  })

  it('round-trips recorded think-time policy and every random pacing bound', () => {
    const stored: StoredScenario = {
      id: 'source-runtime',
      name: 'Imported source runtime',
      duration: '5m',
      scripts: [],
      created_at: '',
      updated_at: '',
      script_count: 0,
      runtime_settings: {
        pacing: {
          type: 'random_interval',
          min_interval_ms: 2500,
          max_interval_ms: 7500,
        },
        think_time: {
          type: 'recorded',
          multiplier: 1.5,
          limit_ms: 4000,
          ignore: false,
        },
      },
    }

    const form = scenarioFormStateFromStoredScenario(stored, '')
    expect(form).toMatchObject({
      runtime_pacing_type: 'random_interval',
      runtime_pacing_min_interval_ms: 2500,
      runtime_pacing_max_interval_ms: 7500,
      runtime_think_time_type: 'recorded',
      runtime_think_time_multiplier: 1.5,
      runtime_think_time_limit_ms: 4000,
      runtime_ignore_think_time: false,
    })
    expect(runtimeSettingsFromForm(form, true)).toMatchObject(stored.runtime_settings!)
    expect(runtimeSettingsFromForm(form, true).think_time).not.toHaveProperty('mean_ms')
  })

  it('canonicalizes legacy normal think time and persists edited distribution fields', () => {
    const stored: StoredScenario = {
      id: 'legacy-normal-runtime',
      name: 'Legacy normal runtime',
      duration: '5m',
      scripts: [],
      created_at: '',
      updated_at: '',
      script_count: 0,
      runtime_settings: {
        think_time: {
          type: 'normal',
          mean_ms: 500,
          std_ms: 75,
          min_ms: 100,
          max_ms: 900,
          multiplier: 1.25,
        },
      },
    }

    const form = scenarioFormStateFromStoredScenario(stored, '')
    expect(form.runtime_think_time_type).toBe('gaussian')
    expect(runtimeSettingsFromForm({
      ...form,
      runtime_think_time_std_ms: 125,
      runtime_think_time_min_ms: 50,
      runtime_think_time_max_ms: 1200,
    }, true).think_time).toEqual({
      type: 'gaussian',
      mean_ms: 500,
      std_ms: 125,
      min_ms: 50,
      max_ms: 1200,
      multiplier: 1.25,
      limit_ms: undefined,
      ignore: false,
    })
  })

  it('preserves canonical pacing and think-time values after an unrelated scenario-default edit', () => {
    const stored: StoredScenario = {
      id: 'scenario-default-sync',
      name: 'Scenario default sync',
      duration: '5m',
      scripts: [],
      created_at: '',
      updated_at: '',
      script_count: 0,
      runtime_settings: {
        pacing: {
          type: 'random_delay',
          min_delay_ms: 100,
          max_delay_ms: 900,
        },
        think_time: {
          type: 'gaussian',
          mean_ms: 250,
          std_ms: 75,
          min_ms: 25,
          max_ms: 900,
          multiplier: 1.5,
          limit_ms: 1200,
          ignore: true,
        },
      },
    }
    const form = scenarioFormStateFromStoredScenario(stored, '')
    const nextDraft = {
      ...runtimeSettingsFromForm(form, true),
      base_url: 'https://orders.example.test',
    }

    const replaced = replaceScenarioRuntimeDefaults(form, nextDraft)
    expect(runtimeSettingsFromForm(replaced, true)).toMatchObject({
      pacing: stored.runtime_settings?.pacing,
      think_time: stored.runtime_settings?.think_time,
      base_url: 'https://orders.example.test',
    })
  })

  it('seeds a template as one canonical runtime profile', () => {
    const template: LoadModelTemplate = {
      id: 'source-template',
      name: 'Source template',
      shape_id: 'closed-vuser',
      description: 'Imported source settings',
      scenario: {
        duration: '5m',
        groups: [{ name: 'Load', vusers: 1 }],
        runtime_settings: {
          pacing: { type: 'fixed_interval', interval_ms: 5000 },
          think_time: { type: 'recorded', multiplier: 1, limit_ms: 3000, ignore: false },
          run_logic: { iterations: 1, auto_transactions: 'none' },
          log: { level: 'standard' },
        },
      },
    }

    const seed = scenarioSeedFromTemplate(template)
    expect(seed.pacing).toEqual({ type: 'fixed_interval', interval_ms: 5000 })
    expect(seed.think_time).toEqual({
      type: 'recorded',
      multiplier: 1,
      limit_ms: 3000,
      ignore: false,
    })
    expect(seed.iterations).toBe(1)
    expect(seed.runtime_settings).toEqual(template.scenario.runtime_settings)
    expect(seed.runtime_settings).not.toBe(template.scenario.runtime_settings)
  })

  it('retains advanced per-group configuration when a workload shape is re-seeded', () => {
    const previous = [{
      name: 'Browser observer',
      script_id: 'browser-script',
      role: 'observer' as const,
      vusers: 2,
      ramp_up: '1m',
      ramp_down: '1m',
      stages: [],
      iterations: 7,
      initialization: 'at_once' as const,
      runtimeSettings: {
        iterations: 3,
        log_level: '',
        auto_transactions: '',
        http: { request_timeout_ms: 2500 },
        browser_config: { engine: 'chromium' },
      },
      assignment: { mode: 'specific_hosts' as const, selected_targets: ['generator-a'] },
    }]

    const reseeded = reseedScenarioGroups([{
      name: 'Stepped browser',
      vusers: 20,
      ramp_up: '2m',
      stages: [{ type: 'hold', duration: '5m', target_vusers: 20 }],
    }], previous)

    expect(reseeded[0]).toMatchObject({
      name: 'Stepped browser',
      script_id: 'browser-script',
      role: 'observer',
      vusers: 20,
      iterations: 7,
      initialization: 'at_once',
      runtimeSettings: {
        iterations: 3,
        http: { request_timeout_ms: 2500 },
        browser_config: { engine: 'chromium' },
      },
      assignment: { mode: 'specific_hosts', selected_targets: ['generator-a'] },
    })
    reseeded[0].runtimeSettings!.http!.request_timeout_ms = 999
    reseeded[0].assignment!.selected_targets![0] = 'changed'
    expect(previous[0].runtimeSettings.http.request_timeout_ms).toBe(2500)
    expect(previous[0].assignment.selected_targets[0]).toBe('generator-a')
  })

  it('filters every protocol-specific runtime section when applying overrides to groups', () => {
    const source = {
      iterations: 4,
      log_level: '',
      auto_transactions: '',
      browser: { headless: true },
      browser_config: { engine: 'chromium' },
      http: { request_timeout_ms: 1500 },
      web_runtime: { additional_attributes: { runner: 'ApexLoad' } },
      run_logic: { global_iterations: 100 },
      env: { REGION: 'east' },
    }

    const kafka = copyRuntimeSettingsForProtocol(source, 'kafka')
    expect(kafka).toMatchObject({ iterations: 4, run_logic: { global_iterations: 100 }, env: { REGION: 'east' } })
    expect(kafka.browser).toBeUndefined()
    expect(kafka.browser_config).toBeUndefined()
    expect(kafka.http).toBeUndefined()
    expect(kafka.web_runtime).toBeUndefined()

    const http = copyRuntimeSettingsForProtocol(source, 'http')
    expect(http.browser).toBeUndefined()
    expect(http.browser_config).toBeUndefined()
    expect(http.http).toEqual({ request_timeout_ms: 1500 })
    expect(http.web_runtime).toEqual({ additional_attributes: { runner: 'ApexLoad' } })

    const browser = copyRuntimeSettingsForProtocol(source, 'browser')
    expect(browser.browser).toEqual({ headless: true })
    expect(browser.browser_config).toEqual({ engine: 'chromium' })
    expect(browser.http).toEqual({ request_timeout_ms: 1500 })
    expect(browser.web_runtime).toEqual({ additional_attributes: { runner: 'ApexLoad' } })
    browser.env!.REGION = 'changed'
    expect(source.env.REGION).toBe('east')
  })

  it('applies an inheriting source effective profile instead of the target script defaults', () => {
    const groups = [
      {
        name: 'Source',
        script_id: 'source-script',
        vusers: 10,
        iterations: 7,
        ramp_up: '',
        ramp_down: '',
        stages: [],
        initialization: 'at_runtime' as const,
        runtimeSettings: undefined,
      },
      {
        name: 'Target',
        script_id: 'target-script',
        vusers: 10,
        iterations: 99,
        ramp_up: '',
        ramp_down: '',
        stages: [],
        initialization: 'at_runtime' as const,
        runtimeSettings: {
          http: { request_timeout_ms: 777 },
          log_level: '',
          auto_transactions: '',
          env: { API_SECRET: 'target-secret' },
          redaction_rules: [{ name: 'target-token', pattern: 'target-secret', replacement: '***' }],
        },
      },
    ]
    const scripts = [
      {
        id: 'source-script',
        name: 'Source script',
        protocol: 'http',
        version: '1.0',
        created_at: '',
        script: {
          name: 'Source script',
          protocol: 'http',
          version: '1.0',
          config: {
            base_url: 'https://source.test',
            pacing: { type: 'fixed_delay', delay_ms: 125 },
            log: { level: 'extended' },
            http: { request_timeout_ms: 1500, tls: { insecure_skip_verify: false } },
            web_runtime: {
              additional_attributes: { source: 'yes' },
              network_simulation: { latency_ms: 10 },
            },
          },
          actions: [],
        },
      },
      {
        id: 'target-script',
        name: 'Target script',
        protocol: 'http',
        version: '1.0',
        created_at: '',
        script: {
          name: 'Target script',
          protocol: 'http',
          version: '1.0',
          config: {
            base_url: 'https://target.test',
            pacing: { type: 'fixed_delay', delay_ms: 999 },
            log: { level: 'disabled' },
            http: { request_timeout_ms: 9999, tls: { insecure_skip_verify: true } },
          },
          actions: [],
        },
      },
    ]

    const result = applyEffectiveRuntimeSettingsToGroups(
      groups,
      scripts,
      {
        base_url: 'https://engine.test',
        log: { level: 'standard', on_error_only: true },
        http: { connect_timeout_ms: 500 },
      },
      {
        log_level: 'standard',
        auto_transactions: '',
        continue_on_error: false,
        http: { idle_timeout_ms: 2500 },
        web_runtime: {
          additional_attributes: { scenario: 'yes' },
          network_simulation: { jitter_ms: 2 },
        },
        env: { SOURCE_SECRET: 'source-secret' },
        redaction_rules: [{ name: 'source-token', pattern: 'source-secret', replacement: '[SOURCE]' }],
      },
      0,
      [1],
    )

    expect(result[0].runtimeSettings).toBeUndefined()
    expect(result[1].runtimeSettings).toMatchObject({
      iterations: 7,
      pacing: { type: 'fixed_delay', delay_ms: 125 },
      log_level: '',
      continue_on_error: false,
      log: { level: 'standard' },
      http: { idle_timeout_ms: 2500 },
      web_runtime: {
        additional_attributes: { scenario: 'yes' },
        network_simulation: { jitter_ms: 2 },
      },
      base_url: 'https://source.test',
      env: { API_SECRET: 'target-secret' },
      redaction_rules: [{ name: 'target-token', pattern: 'target-secret', replacement: '***' }],
    })
    expect(result[1].runtimeSettings?.http?.request_timeout_ms).not.toBe(9999)
    expect(result[1].runtimeSettings?.pacing?.delay_ms).not.toBe(999)
    expect(result[1].runtimeSettings?.env).not.toHaveProperty('SOURCE_SECRET')
    expect(result[1].iterations).toBeUndefined()

    const emptyProtected = applyEffectiveRuntimeSettingsToGroups(
      [groups[0], {
        ...groups[1],
        runtimeSettings: { http: { request_timeout_ms: 777 }, log_level: '', auto_transactions: '', env: {}, redaction_rules: [] },
      }],
      scripts,
      undefined,
      undefined,
      0,
      [1],
    )
    expect(emptyProtected[1].runtimeSettings?.env).toEqual({})
    expect(emptyProtected[1].runtimeSettings?.redaction_rules).toEqual([])

    const absentProtected = applyEffectiveRuntimeSettingsToGroups(
      [groups[0], {
        ...groups[1],
        runtimeSettings: { http: { request_timeout_ms: 777 }, log_level: '', auto_transactions: '' },
      }],
      scripts,
      undefined,
      undefined,
      0,
      [1],
    )
    expect(absentProtected[1].runtimeSettings?.env).toBeUndefined()
    expect(absentProtected[1].runtimeSettings?.redaction_rules).toBeUndefined()
  })

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }

    apiMock.listScenarios.mockResolvedValue({ scenarios: [], count: 0 })
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [], count: 0 })
    apiMock.getTrafficProfileSources.mockResolvedValue({
      sources: [
        { kind: 'file', enabled: true, configured: true, formats: ['csv', 'xlsx'] },
        { kind: 'prometheus', enabled: true, configured: true },
        { kind: 'opensearch', enabled: false, configured: false },
      ],
      limits: { upload_bytes: 25, derive_bytes: 8, upstream_bytes: 16, points_per_series: 10_000, points_per_profile: 50_000 },
    })
    apiMock.listLoadModelTemplates.mockResolvedValue({
      templates: [{
        id: 'open-api-arrival-rate',
		name: 'Goal-Driven API',
        shape_id: 'open-arrival-rate',
        description: 'Throughput-oriented API load shape for queueing-sensitive services.',
        scenario: {
          scenario_mode: 'goal_oriented',
		  duration: '60m',
          goal_config: {
            target_average_tps: 25,
            min_vusers: 5,
            max_vusers: 50,
            p95_latency_ms: 800,
            max_error_pct: 1,
            max_dead_letter_rate_pct: 0.5,
            stop_on_sla_breach: true,
            sla_breach_window: '15s',
          },
          groups: [{ name: 'arrival-rate', vusers: 10, ramp_up: '2m' }],
        },
      }, {
        id: 'chaos-resilience-shakeout',
        name: 'Chaos Resilience With Shakeout Gate',
        shape_id: 'chaos-resilience',
        description: 'Staged resilience starter with a pre-run shakeout gate.',
        scenario: {
          scenario_mode: 'goal_oriented',
          duration: '18m',
          script_shakeout: true,
          resource_baseline: true,
          goal_config: {
            target_average_tps: 15,
            min_vusers: 5,
            max_vusers: 75,
            p95_latency_ms: 1200,
            max_error_pct: 5,
          },
          runtime_settings: {
            error_handling: {
              continue_on_error: true,
              snapshot_on_error: true,
            },
          },
          groups: [{
            name: 'chaos-steady-state',
            vusers: 25,
            ramp_up: '3m',
            ramp_down: '2m',
            stages: [
              { name: 'Baseline', type: 'ramp_up', duration: '3m', target_vusers: 25 },
              { name: 'Turbulence', type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 },
              { name: 'Recovery', type: 'ramp_down', duration: '5m', target_vusers: 5 },
            ],
          }],
        },
      }],
      count: 2,
    })
    apiMock.getLoadModelSupportMatrix.mockResolvedValue({
      support_matrix: [
        {
          id: 'closed-vuser',
          name: 'Closed VUser',
          category: 'vuser',
          status: 'executed',
          apexload_shape: 'Scenario groups with fixed VUsers and duration.',
          fields: ['groups[].vusers', 'duration', 'groups[].ramp_up'],
          notes: 'Primary execution model.',
        },
        {
          id: 'open-arrival-rate',
		  name: 'Goal-Driven Scenario',
          category: 'throughput',
          status: 'executed',
          apexload_shape: 'Open workload runtime using target throughput controls.',
          fields: ['config.pacing.tps', 'goal_config.target_average_tps'],
          notes: 'Runtime emits target TPS and throughput miss flags.',
        },
        {
          id: 'stepped-ramp',
          name: 'Stepped Ramp',
          category: 'ramp',
          status: 'executed',
          apexload_shape: 'Staged segments with hold, ramp-up, and ramp-down.',
          fields: ['groups[].stages', 'groups[].stages[].target_vusers'],
          notes: 'Dedicated staged segments are available in scenario authoring.',
        },
        {
          id: 'production-replay',
          name: 'Production Traffic Profile',
          category: 'profile',
          status: 'executed',
          apexload_shape: 'Production-shaped traffic replay.',
          fields: ['traffic_profile.mappings[].points'],
          notes: 'Preserves the gradual daily traffic curve.',
        },
        {
          id: 'csv-driven',
          name: 'CSV Driven',
          category: 'profile',
          status: 'executed',
          apexload_shape: 'Exact authored CSV target replay.',
          fields: ['traffic_profile.mappings[].points', 'scenario_mode'],
          notes: 'Supports direct VUser or goal-driven TPS schedules.',
        },
        {
          id: 'chaos-resilience',
          name: 'Chaos Resilience',
          category: 'resilience',
          status: 'executed',
          apexload_shape: 'Guarded load envelope with pre-run script shakeout and staged recovery.',
          fields: ['script_shakeout', 'groups[].stages', 'runtime_settings.error_handling.snapshot_on_error'],
          notes: 'Preserves shakeout evidence before full load.',
        },
      ],
      status_counts: { executed: 6 },
      notes: [],
    })
    apiMock.validateScenarioLoadModel.mockResolvedValue({
      status: 'needs_review',
      status_counts: { ready: 2, needs_review: 1 },
      total_vusers: 10,
      peak_target_tps: 25,
      items: [
        { dimension: 'modeled_demand', status: 'ready', summary: 'Closed workload uses 10 VUsers.', target_vusers: 10 },
        { dimension: 'generator_placement', status: 'needs_review', summary: 'Auto-assignment will use configured generators.' },
      ],
    })
    apiMock.estimateTest.mockResolvedValue({
      duration: '1m',
      groups: [],
      assignment: { mode: 'auto' },
      footprint: {
        total_vusers: 10,
        duration: '1m',
        complexity_score: 1.25,
        estimated_cpu_cores: 0.01,
        estimated_memory_mb: 8,
        estimated_network_mbps: 1,
      },
      assignment_recommendation: {
        mode: 'auto',
        recommended_targets: ['gen-a'],
        summary: 'Auto-assignment recommends 1 host for the current scenario footprint.',
      },
    })
    apiMock.createScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.updateScenario.mockResolvedValue({ id: 'scenario-1' })
    apiMock.deleteScenario.mockResolvedValue({ deleted: 'scenario-1' })
    apiMock.listScenarioResults.mockResolvedValue({ tests: [], count: 0 })
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })
    libraryApiMock.localCatalog.mockResolvedValue({ items: [], count: 0 })
    libraryApiMock.remoteCatalog.mockResolvedValue({ catalog: { items: [], count: 0 } })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: false,
      authenticated: false,
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: '',
          operations: [{ id: 'manage_tests', label: 'Manage tests', allowed: true }],
        },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'ready', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.scenarioBuilder.draft')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.scenarioBuilder.draft')
  })

  it('creates a scenario from the typed form payload', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-1')
    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')
    expect(apiMock.listLoadModelTemplates).toHaveBeenCalled()
    expect(apiMock.getLoadModelSupportMatrix).toHaveBeenCalled()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Checkout Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    const loadGroupCard = container.querySelector('.scenario-script-group-card')
    const vusersInput = loadGroupCard ? Array.from(loadGroupCard.querySelectorAll('input')).find(input => input.getAttribute('type') === 'number') : undefined
    expect(vusersInput).toBeDefined()
    if (!vusersInput) return

    await act(async () => {
      setInputValue(vusersInput, '25')
    })

    const durationField = getDurationFieldByLabel(container, 'Duration')
    const durationInput = durationField?.input
    const durationUnit = durationField?.select
    expect(durationInput).toBeDefined()
    expect(durationUnit).toBeDefined()
    if (!durationInput || !durationUnit) return

    await act(async () => {
      setInputValue(durationInput, '5')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith({
      name: 'Checkout Scenario',
      project_id: 'project-1',
      duration: '5m',
      scenario_mode: 'vuser_driven',
      script_shakeout: false,
      resource_baseline: false,
      goal_config: {
        early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m' },
      },
      workload_shape_id: '',
      assignment: { mode: 'auto' },
      groups: [
        {
          name: 'Checkout Flow',
          script_id: 'script-1',
          vusers: 25,
          initialization: 'at_runtime',
        },
      ],
      data_pools: [],
    })
  })

  it('selects a group script from the searchable sortable script library', async () => {
    const scrollIntoView = vi.fn()
    Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: scrollIntoView })
    apiMock.listScripts.mockResolvedValue({
      scripts: [
        {
          id: '',
          name: 'Legacy Empty Id Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-01T12:00:00Z',
          scenario_count: 0,
          associated_scenarios: [],
          associated_releases: [],
        },
        {
          id: 'script-1',
          name: 'Checkout Flow',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T12:00:00Z',
          scenario_count: 0,
          associated_scenarios: [],
          associated_releases: [],
        },
        {
          id: 'script-2',
          name: 'Payments Flow',
          protocol: 'grpc',
          version: '2.0.0',
          created_at: '2026-05-15T12:00:00Z',
          last_run_at: '2026-05-20T15:30:00Z',
          scenario_count: 2,
          associated_scenarios: [{ id: 'scenario-payments', name: 'Payments Peak' }],
          associated_releases: [{ id: 'release-30', name: 'PI30', application_id: 'app-1' }],
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Payments Scenario')
    })

    const firstGroup = container.querySelector('.scenario-script-group-card')
    const chooseScriptButton = Array.from(firstGroup?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Add Script'))
    expect(chooseScriptButton).toBeDefined()
    if (!chooseScriptButton) return
    expect(firstGroup?.textContent).not.toContain('Checkout Flow')
    expect(firstGroup?.textContent).not.toContain('Legacy Empty Id Script')

    await act(async () => {
      chooseScriptButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const searchInput = container.querySelector('.scenario-existing-script-library input[type="search"]') as HTMLInputElement | null
    expect(searchInput).toBeDefined()
    expect(document.activeElement).toBe(searchInput)
    expect(scrollIntoView).toHaveBeenCalled()
    expect(container.textContent).toContain('Choosing for Group 1')
    expect(container.textContent).toContain('Payments Peak')
    expect(container.textContent).toContain('PI30')
    expect(container.textContent).toContain('grpc')
    expect(container.textContent).toContain('May 15, 2026')
    expect(container.textContent).toContain('May 20, 2026')
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'Payments Peak')
    })
    const filteredRows = Array.from(container.querySelectorAll('.scenario-existing-script-library tbody tr'))
    expect(filteredRows).toHaveLength(1)
    expect(filteredRows[0].textContent).toContain('Payments Flow')

    await act(async () => {
      setInputValue(searchInput, '')
    })
    const createdHeader = Array.from(container.querySelectorAll('.scenario-existing-script-library th'))
      .find(header => header.textContent?.includes('Created'))
    expect(createdHeader).toBeDefined()
    if (!createdHeader) return
    expect(createdHeader.getAttribute('aria-sort')).toBe('none')
    const createdSortButton = createdHeader.querySelector('button')
    expect(createdSortButton).toBeDefined()
    if (!createdSortButton) return
    await act(async () => {
      createdSortButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(createdHeader.getAttribute('aria-sort')).toBe('descending')
    const firstSortedRow = container.querySelector('.scenario-existing-script-library tbody tr')
    expect(firstSortedRow?.textContent).toContain('Payments Flow')

    const usePaymentsButton = Array.from(container.querySelectorAll('.scenario-existing-script-library button'))
      .find(button => button.textContent?.includes('Use for Group 1') && button.closest('tr')?.textContent?.includes('Payments Flow'))
    expect(usePaymentsButton).toBeDefined()
    if (!usePaymentsButton) return

    await act(async () => {
      usePaymentsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    expect(container.querySelector('.scenario-script-group-card')?.textContent).toContain('Payments Flow')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Payments Scenario',
      groups: [expect.objectContaining({
        name: 'Payments Flow',
        script_id: 'script-2',
      })],
    }))
  })

  it('opens directly to the scenario builder when requested', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()

    expect(container.querySelector('.scenario-builder')).toBeTruthy()
    expect(container.querySelector('.library-table-wrapper')).toBeFalsy()
    const startActionLabels = Array.from(container.querySelectorAll('.scenario-builder-actions button'))
      .map(button => button.textContent?.trim())
    expect(startActionLabels).not.toContain('Back')
    expect(startActionLabels).toContain('Next')
    const builderToggle = Array.from(container.querySelectorAll('.scenario-page-toggle button'))
      .find(button => button.textContent === 'Scenario Builder')
    expect(builderToggle?.getAttribute('aria-pressed')).toBe('true')

    await clickWizardStep('Shape / Source')

    const sourceActionLabels = Array.from(container.querySelectorAll('.scenario-builder-actions button'))
      .map(button => button.textContent?.trim())
    expect(sourceActionLabels).toContain('Back')
  })

  it('hides the duplicate builder page title when the static topbar owns it', async () => {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />
        </TopbarContributionProvider>,
      )
    })

    await flushPromises()

    expect(container.querySelector('.scenario-builder')).toBeTruthy()
    expect(container.querySelector('.page-header')).toBeNull()
    expect(container.querySelector('.scenario-builder .section-title')).toBeNull()
  })

  it('does not mark skipped mandatory wizard steps as complete', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    expect(wizardStepMarker('Start')).toBe('1')
    expect(wizardStepMarker('Scripts')).toBe('2')
    expect(wizardStepMarker('Shape / Source')).toBe('3')
    expect(wizardStepComplete('Start')).toBe(false)
    expect(wizardStepComplete('Shape / Source')).toBe(false)
    expect(wizardStepComplete('Scripts')).toBe(false)
  })

  it('replaces wizard step numbers with checkmarks only when required fields are complete', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()

    await clickButton('Build from Workload Shape')
    expect(wizardStepMarker('Start')).toBe('✓')
    expect(wizardStepComplete('Start')).toBe(true)
    expect(wizardStepMarker('Scripts')).toBe('2')
    expect(wizardStepComplete('Scripts')).toBe(false)

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Completion Marker Scenario')
    })
    await chooseLibraryScriptForGroup()

    expect(wizardStepMarker('Scripts')).toBe('✓')
    expect(wizardStepComplete('Scripts')).toBe(true)
    expect(wizardStepMarker('Shape / Source')).toBe('3')

    await selectManualShapeByName('Closed VUser')
    expect(wizardStepMarker('Shape / Source')).toBe('✓')
    expect(wizardStepComplete('Shape / Source')).toBe(true)
    expect(wizardStepMarker('Run Settings')).toBe('✓')
    expect(wizardStepComplete('Run Settings')).toBe(true)

    await clickWizardStep('Run Settings')
    expect(wizardStepMarker('Run Settings')).toBe('✓')
    expect(wizardStepComplete('Run Settings')).toBe(true)
  })

  it('shows the configured duration and script-group values on review', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')
    expect(nameInput).not.toBeNull()
    if (!nameInput) return
    await act(async () => {
      setInputValue(nameInput, 'Review Parity Scenario')
    })
    await chooseLibraryScriptForGroup()

    await clickWizardStep('Run Settings')
    const duration = getDurationFieldByLabel(container, 'Duration')
    const vusers = findControlByLabel<HTMLInputElement>(container, 'VUsers', 'input[type="number"]')
    expect(duration).not.toBeNull()
    expect(vusers).toBeDefined()
    if (!duration || !vusers) return
    await act(async () => {
      setInputValue(duration.input, '3')
      setSelectValue(duration.select, 'm')
      setInputValue(vusers, '17')
    })

    await clickWizardStep('Review')

    const reviewGroups = container.querySelector<HTMLTableElement>('table[aria-label="Scenario review groups"]')
    expect(reviewGroups).not.toBeNull()
    expect(container.querySelector('.scenario-review-grid')?.textContent).toContain('Duration3m')
    const reviewCells = Array.from(reviewGroups?.querySelectorAll('tbody td') || []).map(cell => cell.textContent)
    expect(reviewCells).toEqual(['Checkout Flow', 'Checkout Flow', 'Load', '17', 'None', 'None', 'Per VUser'])
  })

  it('seeds goal-oriented run settings and shows shape guidance when an open-arrival-rate shape is selected manually', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Goal-Driven Scenario')

    // Guidance banner reflects the chosen shape on the Scripts step.
    const guidance = container.querySelector('.scenario-shape-guidance')
    expect(guidance).toBeTruthy()
    expect(guidance?.textContent).toContain('Goal-Driven Scenario')
    expect(guidance?.textContent).toContain('throughput')

    await clickWizardStep('Run Settings')
    const durationField = getDurationFieldByLabel(container, 'Duration')
	expect(durationField?.input.value).toBe('1')
	expect(durationField?.select.value).toBe('h')
		expect(container.textContent).not.toContain('Scenario Mode')
		expect(container.textContent).toContain('Goal-Driven Scenario Setup')
		expect((container.querySelector('input[aria-label="Average response time for Group 1"]') as HTMLInputElement | null)?.value).toBe('')
    expect(container.querySelector('.scenario-builder-preview')?.textContent).toContain('Sizing incomplete')
    const artInput = container.querySelector<HTMLInputElement>('[aria-label="Average response time for Group 1"]')
    if (!artInput) throw new Error('Average response time input missing')
    await act(async () => setInputValue(artInput, '400'))
    expect(container.querySelector('.scenario-builder-preview')?.textContent).not.toContain('Sizing incomplete')
    await act(async () => setInputValue(artInput, '0'))
    expect(container.querySelector('.scenario-builder-preview')?.textContent).toContain('Sizing incomplete')

    for (const label of [
      'Max Error Count',
      'Max LCP (ms)',
      'Max INP (ms)',
      'Max CLS',
      'Max Throttled Requests',
      'Max End-to-End Latency (ms)',
      'Max Partition Skew %',
      'Max Redelivery Rate %',
      'Max Dead-Letter Rate %',
	  'Breach window',
	  'Stop on sustained guardrail breach',
      'Stop when total errors exceed max error count',
    ]) {
      expect(container.textContent).toContain(label)
    }
  })

  it('preserves persisted Goal-Driven VUser guardrails when throughput changes', async () => {
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'goal-driven-edit',
        name: 'Authored Goal Guardrails',
        workload_shape_id: 'open-arrival-rate',
        duration: '60m',
        scenario_mode: 'goal_oriented',
        goal_config: {
          target_average_tps: 25,
          min_vusers: 3,
          max_vusers: 99,
          workload_shape_config: {
            goal_driven: {
              groups: { group_0: { target_tps: 25, art: { basis: 'manual', art_ms: 400 } } },
            },
          },
        },
        scripts: [{ order: 0, script_id: 'script-1', script_name: 'Checkout Flow', name: 'Checkout', vusers: 10 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        updated_at: '2026-05-12T00:00:00Z',
        script_count: 1,
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()
    await clickButton('Edit')
    await clickWizardStep('Run Settings')

    const target = container.querySelector('input[aria-label="Target TPS for Checkout"]') as HTMLInputElement | null
    expect(target).not.toBeNull()
    if (!target) return
    await act(async () => setInputValue(target, '50'))

    expect(findControlByLabel<HTMLInputElement>(container, 'Min VUsers', 'input')?.value).toBe('3')
    expect(findControlByLabel<HTMLInputElement>(container, 'Max VUsers', 'input')?.value).toBe('99')
  })

  it('rejects a persistent goal-oriented scenario without a goal or guardrail', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    await act(async () => {
      setInputValue(nameInput, 'Invalid Empty Goal')
    })
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    const modeSelect = findControlByLabel<HTMLSelectElement>(container, 'Scenario Mode', 'select')
    expect(modeSelect).toBeDefined()
    if (!modeSelect) return
    await act(async () => {
      setSelectValue(modeSelect, 'goal_oriented')
    })
    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Goal-oriented scenarios require at least one goal or guardrail setting')
  })

  it('seeds stair-step stages when a stepped-ramp shape is selected manually', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Stepped Ramp')
    await clickWizardStep('Run Settings')

    const stageRows = container.querySelectorAll('.stepped-ramp-stage-preview tbody tr')
    expect(stageRows.length).toBe(6)
    expect(stageRows[0].textContent).toContain('10')
    expect(stageRows[5].textContent).toContain('60')
    expect(container.textContent).toContain('6 steps · +10 VUsers per step · 10m each')
    expect(findControlByLabel<HTMLSelectElement>(container, 'Scenario Mode', 'select')).toBeUndefined()
    expect(container.textContent).not.toContain('Goal oriented')
  })

  it('guides Closed VUser phases, retains assurance controls, and saves authoring intent', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Closed VUser')
    await clickWizardStep('Scripts')
    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')
    expect(nameInput).not.toBeNull()
    if (!nameInput) return
    await act(async () => setInputValue(nameInput, 'Closed Profile Scenario'))
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    expect(container.textContent).toContain('Closed VUser Setup')
    expect(container.textContent).toContain('5m total')
    expect(findControlByLabel<HTMLInputElement>(container, 'Peak VUsers', 'input')?.value).toBe('5')
    expect(getDurationFieldByLabel(container, 'Ramp-up')?.input.value).toBe('2')
    expect(getDurationFieldByLabel(container, 'Sustained load')?.input.value).toBe('3')
    expect(container.querySelectorAll('.closed-vuser-phase-preview tbody tr')).toHaveLength(2)
    expect(findControlByLabel<HTMLSelectElement>(container, 'Scenario Mode', 'select')).toBeUndefined()
    for (const assurance of ['Script shakeout gate', 'Resource baseline windows', 'Continue on action errors', 'Snapshot on error']) {
      expect(container.textContent).toContain(assurance)
    }

    const rampDownToggle = container.querySelector<HTMLInputElement>('.closed-vuser-ramp-toggle input[type="checkbox"]') ?? undefined
    expect(rampDownToggle).toBeDefined()
    if (!rampDownToggle) return
    const rampUpField = getDurationFieldByLabel(container, 'Ramp-up')
    expect(rampUpField).not.toBeNull()
    if (!rampUpField) return
    await act(async () => setInputValue(rampUpField.input, '3'))
    await act(async () => rampDownToggle.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(getDurationFieldByLabel(container, 'Ramp-down')?.input.value).toBe('3')
    expect(container.querySelectorAll('.closed-vuser-phase-preview tbody tr')).toHaveLength(3)
    expect(container.textContent).toContain('9m total')

    await act(async () => rampDownToggle.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await act(async () => setInputValue(rampUpField.input, '4'))
    await act(async () => rampDownToggle.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(getDurationFieldByLabel(container, 'Ramp-down')?.input.value).toBe('3')
    expect(container.textContent).toContain('10m total')

    await clickButton('Save draft')
    await flushPromises()
    const payload = apiMock.createScenario.mock.calls[apiMock.createScenario.mock.calls.length - 1]?.[0]
    expect(payload).toMatchObject({
      duration: '10m',
      scenario_mode: 'vuser_driven',
      goal_config: expect.objectContaining({
        early_stop: { enabled: false, error_rate_pct: 5, breach_window: '1m' },
        workload_shape_config: {
          closed_vuser: expect.objectContaining({
            scope: 'scenario',
            scenario: { peak_vusers: 5, ramp_up: '4m', sustain: '3m', ramp_down_enabled: true, ramp_down: '3m' },
          }),
        },
      }),
    })
    expect(payload.groups[0]).toMatchObject({ vusers: 5, ramp_up: '4m', ramp_down: '3m' })
    expect(payload.groups[0].stages.map((stage: { type: string }) => stage.type)).toEqual(['ramp_up', 'hold', 'ramp_down'])
  })

  it('updates the guided stepped-ramp stages immediately and saves authoring intent', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Stepped Ramp')
    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    await act(async () => {
      setInputValue(nameInput, 'Adaptive Stepped Scenario')
    })
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    const endingLoadInput = findControlByLabel<HTMLInputElement>(container, 'Ending load level', 'input')
    const durationField = getDurationFieldByLabel(container, 'Duration')
    expect(endingLoadInput).toBeDefined()
    expect(durationField).not.toBeNull()
    if (!endingLoadInput || !durationField) return

    await act(async () => {
      setInputValue(endingLoadInput, '30')
    })

    let stageRows = container.querySelectorAll('.stepped-ramp-stage-preview tbody tr')
    expect(stageRows.length).toBe(6)
    expect(stageRows[5].textContent).toContain('30')

    await act(async () => {
      setInputValue(durationField.input, '2')
      setSelectValue(durationField.select, 'h')
    })
    stageRows = container.querySelectorAll('.stepped-ramp-stage-preview tbody tr')
    expect(stageRows.length).toBe(6)
    expect(container.textContent).toContain('20m each')

    await clickButton('Save draft')
    await flushPromises()

    const payload = apiMock.createScenario.mock.calls[apiMock.createScenario.mock.calls.length - 1]?.[0]
    expect(payload.name).toBe('Adaptive Stepped Scenario')
    expect(payload.duration).toBe('2h')
    expect(payload.scenario_mode).toBe('vuser_driven')
    expect(payload.goal_config.early_stop).toEqual({ enabled: false, error_rate_pct: 5, breach_window: '1m' })
    expect(payload.goal_config.workload_shape_config.stepped_ramp).toMatchObject({
      scope: 'scenario',
      scenario: { start_vusers: 0, end_vusers: 30, step_count: 6 },
    })
    expect(payload.groups[0]).toMatchObject({ script_id: 'script-1', vusers: 30 })
    expect(payload.groups[0].stages).toHaveLength(6)
    expect(payload.groups[0].stages.every((stage: { type: string; duration: string }) => stage.type === 'hold' && stage.duration === '20m')).toBe(true)
  })

  it('retains a group-level stepped-ramp definition when the group is renamed', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
    await selectManualShapeByName('Stepped Ramp')
    await clickWizardStep('Scripts')
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    await clickButton('Configure each group')

    const endingLoadInput = findControlByLabel<HTMLInputElement>(container, 'Ending load level', 'input')
    expect(endingLoadInput).toBeDefined()
    if (!endingLoadInput) return
    await act(async () => setInputValue(endingLoadInput, '42'))

    await clickWizardStep('Scripts')
    const groupNameInput = findControlByLabel<HTMLInputElement>(container, 'Group Name', 'input')
    expect(groupNameInput).toBeDefined()
    if (!groupNameInput) return
    await act(async () => setInputValue(groupNameInput, 'Renamed Checkout'))

    await clickWizardStep('Run Settings')
    expect(findControlByLabel<HTMLInputElement>(container, 'Ending load level', 'input')?.value).toBe('42')
    expect(container.textContent).toContain('0 → 42 VUsers')
  })

  it('re-seeds the form from a new shape while preserving the chosen script', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickButton('Build from Workload Shape')
	await selectManualShapeByName('Goal-Driven Scenario')
    await chooseLibraryScriptForGroup()

    // Switch to a different shape from the catalog.
    await clickWizardStep('Shape / Source')
    await selectManualShapeByName('Stepped Ramp')

    // Script selection survives the re-seed.
    await clickWizardStep('Scripts')
    const groupCard = container.querySelector('.scenario-script-group-list-spacious .scenario-script-group-card')
    expect(groupCard?.textContent).toContain('Checkout Flow')

    // Load model now reflects the stepped-ramp 60-minute default.
    await clickWizardStep('Run Settings')
		const duration = getDurationFieldByLabel(container, 'Duration')
    expect(duration?.input.value).toBe('1')
		expect(duration?.select.value).toBe('h')
  })

  it('groups Scenario Builder run settings into focused tabs', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Run Settings')

    const tabs = Array.from(container.querySelectorAll('.scenario-run-settings-tab'))
    expect(tabs.map(tab => tab.textContent)).toEqual([
      'Scenario Settings',
      'Browser/Script Runtime',
      'Controller/Generator',
    ])
    expect(tabs[0].getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Load Per Group')
    expect(container.textContent).toContain('Pre-Run Assurance')
    expect(container.textContent).toContain('Advanced Stair-Step Ramp')
    expect(container.textContent).not.toContain('Automatic placement')

    const workspace = container.querySelector('.scenario-builder-workspace.with-preview')
    expect(workspace).toBeTruthy()
    expect(workspace?.querySelector('.scenario-builder-main .scenario-run-settings-tabs')).toBeTruthy()
    expect(workspace?.querySelector('.scenario-builder-preview .scenario-model-preview-panel')).toBeTruthy()

    await clickRunSettingsTab('Controller/Generator')

    expect(container.textContent).toContain('Controller / Generator Assignment')
    expect(container.textContent).toContain('Automatic placement')
    expect(container.textContent).not.toContain('Load Per Group')

    await clickRunSettingsTab('Browser/Script Runtime')

    expect(container.textContent).toContain('Scenario runtime defaults')
    expect(container.textContent).toContain('Iterations')
    expect(container.textContent).toContain('Pacing')
    expect(container.textContent).toContain('Auto-transactions')
    expect(container.textContent).toContain('Think Time')
    expect(container.textContent).not.toContain('Automatic placement')
  })

  it.each([
    ['custom', null],
    ['closed VUser', 'Closed VUser'],
    ['goal driven', 'Goal-Driven Scenario'],
    ['stepped', 'Stepped Ramp'],
    ['production profile', 'Production Traffic Profile'],
    ['CSV driven', 'CSV Driven'],
  ])('renders shared safety settings exactly once and retains them for %s scenarios', async (_label, shapeName) => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    if (shapeName) await selectManualShapeByName(shapeName)
    await clickWizardStep('Run Settings')

    expect(container.querySelectorAll('[data-testid="protect-the-run"]')).toHaveLength(1)
    expect(container.querySelectorAll('[data-testid="pre-run-assurance"]')).toHaveLength(1)

    const protectToggle = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Stop early on excessive errors'))?.querySelector('input')
    const shakeoutToggle = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Script shakeout gate'))?.querySelector('input')
    expect(protectToggle).toBeDefined()
    expect(shakeoutToggle).toBeDefined()
    if (!protectToggle || !shakeoutToggle) return

    await act(async () => {
      protectToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      shakeoutToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const threshold = container.querySelector<HTMLInputElement>('[aria-label="Protect the Run error rate threshold"]')
    expect(threshold?.disabled).toBe(false)
    if (!threshold) return
    await act(async () => setInputValue(threshold, '7.5'))

    await clickRunSettingsTab('Browser/Script Runtime')
    expect(container.querySelectorAll('[data-testid="protect-the-run"]')).toHaveLength(0)
    expect(container.querySelectorAll('[data-testid="pre-run-assurance"]')).toHaveLength(0)
    await clickRunSettingsTab('Scenario Settings')

    expect(container.querySelectorAll('[data-testid="protect-the-run"]')).toHaveLength(1)
    expect(container.querySelectorAll('[data-testid="pre-run-assurance"]')).toHaveLength(1)
    expect(container.querySelector<HTMLInputElement>('[aria-label="Protect the Run error rate threshold"]')?.value).toBe('7.5')
    expect(Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Script shakeout gate'))?.querySelector('input')?.checked).toBe(true)
  })

  it('shows multi-group Scenario tab load settings in horizontal group tabs', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')
    await chooseLibraryScriptForGroup()
    await clickButton('Add Group')
    await clickWizardStep('Run Settings')

    const groupTabs = Array.from(container.querySelectorAll('.scenario-group-tab'))
    expect(groupTabs).toHaveLength(2)
    expect(groupTabs.map(tab => tab.textContent)).toEqual([
      'Checkout FlowCheckout Flow · 10 VUsers',
      'Group 2Unassigned script · 10 VUsers',
    ])
    expect(groupTabs[1].getAttribute('aria-selected')).toBe('true')

    const loadPanel = container.querySelector('.scenario-load-group-panel')
    expect(loadPanel?.querySelectorAll('.scenario-script-group-card')).toHaveLength(1)
    expect(loadPanel?.textContent).toContain('Group 2')
    expect(loadPanel?.textContent).not.toContain('Group 1')

    await act(async () => {
      groupTabs[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('.scenario-load-group-panel .scenario-script-group-card')).toHaveLength(1)
    expect(container.querySelector('.scenario-load-group-panel')?.textContent).toContain('Checkout Flow')
    expect(container.querySelector('.scenario-load-group-panel')?.textContent).not.toContain('Group 2')
    expect(Array.from(container.querySelectorAll('.scenario-group-tab'))[0].getAttribute('aria-selected')).toBe('true')
  })

  it('renumbers a generated group name before assigning its script after an earlier group is removed', async () => {
    await act(async () => {
      root.render(<TestHub initialView="builder" testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()
    await clickWizardStep('Scripts')
    await chooseLibraryScriptForGroup()
    await clickButton('Add Group')

    const removeButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.scenario-script-group-card button'))
      .filter(button => button.textContent === 'Remove')
    expect(removeButtons).toHaveLength(2)
    await act(async () => {
      removeButtons[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const remainingGroup = container.querySelector('.scenario-script-group-card')
    expect((remainingGroup?.querySelector('input[placeholder="Group 1"]') as HTMLInputElement | null)?.value).toBe('Group 1')
    await chooseLibraryScriptForGroup()
    expect(container.querySelector('.scenario-script-group-card strong')?.textContent).toBe('Checkout Flow')
  })

  it('persists script runtime settings from the Browser/Script Runtime tab', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Runtime Defaults Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Browser/Script Runtime')
    const scope = findControlByLabel<HTMLSelectElement>(container, 'Runtime scope', 'select')!
    await act(async () => setSelectValue(scope, '0'))

    // The runtime editor groups overrides into sub-tabs; drive each one.
    await clickRunSettingsTab('Run Logic')
    const iterationsInput = findControlByLabel<HTMLInputElement>(container, 'Iterations', 'input')
    const autoTransactionsSelect = findControlByLabel<HTMLSelectElement>(container, 'Auto-transactions', 'select')
    expect(iterationsInput).toBeDefined()
    expect(autoTransactionsSelect).toBeDefined()
    if (!iterationsInput || !autoTransactionsSelect) return
    await act(async () => {
      setInputValue(iterationsInput, '3')
      setSelectValue(autoTransactionsSelect, 'per_iteration')
    })

    await clickRunSettingsTab('Iteration Pacing')
    const pacingSelect = findControlByLabel<HTMLSelectElement>(container, 'Pacing', 'select')
    expect(pacingSelect).toBeDefined()
    if (!pacingSelect) return
    await act(async () => { setSelectValue(pacingSelect, 'fixed_delay') })
    const pacingDelayInput = findControlByLabel<HTMLInputElement>(container, 'Pacing delay (seconds)', 'input')
    expect(pacingDelayInput).toBeDefined()
    if (!pacingDelayInput) return
    await act(async () => { setInputValue(pacingDelayInput, '0.75') })

    await clickRunSettingsTab('Think Time')
    const distributionSelect = findControlByLabel<HTMLSelectElement>(container, 'Distribution', 'select')
    const meanInput = findControlByLabel<HTMLInputElement>(container, 'Mean (ms)', 'input')
    expect(distributionSelect).toBeDefined()
    expect(meanInput).toBeDefined()
    if (!distributionSelect || !meanInput) return
    await act(async () => {
      setSelectValue(distributionSelect, 'fixed')
      setInputValue(meanInput, '250')
    })

    await clickRunSettingsTab('Error Handling')
    const logLevelSelect = findControlByLabel<HTMLSelectElement>(container, 'Log level', 'select')
    expect(logLevelSelect).toBeDefined()
    if (!logLevelSelect) return
    await act(async () => { setSelectValue(logLevelSelect, 'extended') })

    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Runtime Defaults Scenario',
      groups: expect.arrayContaining([
        expect.objectContaining({
          runtime_settings: expect.objectContaining({
            pacing: { type: 'fixed_delay', delay_ms: 750 },
            run_logic: expect.objectContaining({ iterations: 3, auto_transactions: 'per_iteration' }),
            log: expect.objectContaining({ level: 'extended' }),
            think_time: expect.objectContaining({ type: 'fixed', mean_ms: 250 }),
          }),
        }),
      ]),
    }))
  })

  it.each(['Scenario Library', 'Execution Plans'])('does not resurrect a canceled saved scenario as a new draft after visiting %s', async destination => {
    const stored: StoredScenario = { id: 'existing', name: 'Existing scenario', duration: '5m', scripts: [{ order: 0, script_id: 'script-1', vusers: 12 }], created_at: '', updated_at: '', script_count: 1 }
    apiMock.listScenarios.mockResolvedValue({ scenarios: [stored], count: 1 })
    const onSelectTest = vi.fn()
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} initialView="library" />))
    await flushPromises()
    await clickButton('Edit')
    await clickButton(destination)
    // A history transition reuses the component, just like App's scenario route.
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} initialView="builder" />))
    await clickWizardStep('Scripts')
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')?.value).toBe('')
    expect(container.querySelector('.scenario-script-select-button')?.textContent).toContain('Add Script')
    expect(apiMock.createScenario).not.toHaveBeenCalled()
  })

  it('does not attach a late data-pool result to a replacement draft', async () => {
    let finish!: () => void
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({ resources: [{ id: 'authoring', operations: [{ id: 'manage_tests', allowed: true }, { id: 'manage_scripts', allowed: true }] }] })
    apiMock.createDataPool.mockResolvedValueOnce({ id: 'late-pool', name: 'Old pool' })
    apiMock.createDataPoolRow.mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve }))
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} initialView="builder" />))
    await flushPromises()
    await clickWizardStep('Data')
    const poolNameInput = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('New data pool name'))?.querySelector<HTMLInputElement>('input')
    if (!poolNameInput) throw new Error('New data pool name input not found')
    await act(async () => setInputValue(poolNameInput, 'Old pool'))
    await clickButton('Create and attach data pool')
    await flushPromises()
    expect(apiMock.createDataPoolRow).toHaveBeenCalled()
    await clickButton('Start Over')
    await act(async () => finish())
    await clickWizardStep('Data')
    expect(findControlByLabel<HTMLSelectElement>(container, 'Data Pool', 'select')?.value).toBe('')
    expect(Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('New data pool name'))?.querySelector<HTMLInputElement>('input')?.value).toBe('')
    confirm.mockRestore()
  })

  it('keeps runtime focus on a surviving group and clears it when that group is removed', async () => {
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await clickButton('Add Group')
    await chooseLibraryScriptForGroup('Checkout Flow', 1)
    await clickButton('Add Group')
    await chooseLibraryScriptForGroup('Checkout Flow', 2)
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Browser/Script Runtime')
    await act(async () => setSelectValue(container.querySelector<HTMLSelectElement>('[aria-label="Runtime scope"]')!, '1'))
    await clickWizardStep('Scripts')
    await act(async () => container.querySelector<HTMLButtonElement>('.scenario-script-group-card .scenario-script-group-title button')!.click())
    await clickWizardStep('Run Settings')
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Runtime scope"]')?.value).toBe('0')
    expect(container.textContent).toContain('Runtime overrides · Checkout Flow (#2)')
    await clickWizardStep('Scripts')
    await act(async () => container.querySelector<HTMLButtonElement>('.scenario-script-group-card .scenario-script-group-title button')!.click())
    await clickWizardStep('Run Settings')
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Runtime scope"]')?.value).toBe('defaults')
  })

  it('promotes a stored compact group iteration edit to one canonical runtime value', async () => {
    const storedScenario: StoredScenario = {
      id: 'scenario-compact-iterations',
      name: 'Compact iterations',
      duration: '5m',
      scripts: [{
        order: 0,
        script_id: 'script-1',
        script_name: 'Checkout Flow',
        protocol: 'http',
        vusers: 10,
        iterations: 7,
      }],
      created_at: '2026-05-12T00:00:00Z',
      updated_at: '2026-05-12T00:00:00Z',
      script_count: 1,
    }
    apiMock.listScenarios.mockResolvedValue({ scenarios: [storedScenario], count: 1 })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Browser/Script Runtime')
    const scope = findControlByLabel<HTMLSelectElement>(container, 'Runtime scope', 'select')!
    await act(async () => setSelectValue(scope, '0'))
    await clickRunSettingsTab('Run Logic')

    const iterationsInput = findControlByLabel<HTMLInputElement>(container, 'Iterations', 'input')
    expect(iterationsInput?.value).toBe('7')
    if (!iterationsInput) return
    await act(async () => {
      setInputValue(iterationsInput, '5')
    })

    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.updateScenario).toHaveBeenCalledWith('scenario-compact-iterations', expect.any(Object))
    const updateCall = apiMock.updateScenario.mock.calls.find(call => call[0] === 'scenario-compact-iterations')
    const groupPayload = updateCall?.[1]?.groups?.[0]
    expect(groupPayload).toBeDefined()
    expect(groupPayload).not.toHaveProperty('iterations')
    expect(groupPayload?.runtime_settings).not.toHaveProperty('iterations')
    expect(groupPayload?.runtime_settings?.run_logic).toEqual(expect.objectContaining({ iterations: 5 }))
  })

  it('shows saved assignment defaults in the scenario library', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Pinned Checkout',
        duration: '5m',
        assignment: {
          mode: 'specific_hosts',
          selected_targets: ['gen-a', 'gen-b'],
        },
        scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
        data_pools: [],
        associated_releases: [{ id: 'release-29', name: 'PI29', application_id: 'app-1' }],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_test_id: 'run-checkout-1',
        last_run_at: '2026-05-13T00:00:00Z',
        last_run_ended_at: '2026-05-13T00:05:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Scenario Library')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Library Sources')
    expect(container.textContent).toContain('Associated Release')
    expect(container.textContent).toContain('Search scenarios')
    expect(container.textContent).not.toContain('Asset Type')
    const libraryHeaders = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(libraryHeaders.some(header => header.includes('Scenario'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Test ID'))).toBe(false)
    expect(libraryHeaders.some(header => header.includes('Workload'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Associated Releases'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Updated'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Start'))).toBe(false)
    expect(libraryHeaders.some(header => header.includes('Finish'))).toBe(false)
    expect(libraryHeaders.some(header => header.includes('Last Run'))).toBe(false)
    expect(container.textContent).toContain('Pinned Checkout')
    expect(container.textContent).toContain('Specific Hosts')
    expect(container.textContent).toContain('gen-a, gen-b')
    expect(container.textContent).not.toContain('run-checkout-1')
    expect(container.textContent).not.toContain(new Date('2026-05-13T00:00:00Z').toLocaleString())
    expect(container.textContent).not.toContain(new Date('2026-05-13T00:05:00Z').toLocaleString())
    expect(container.textContent).toContain('PI29')
    expect(container.textContent).toContain('No data pools')
    expect(container.textContent).toContain('0 results')
    expect(container.textContent).not.toContain('Automatic placement')
    expect(container.textContent).not.toContain('Uses controller recommendations')

    const searchInput = container.querySelector('input[type="search"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'gen-b')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Pinned Checkout')

    await act(async () => {
      setInputValue(searchInput, 'does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match these filters.')

    await act(async () => {
      setInputValue(searchInput, '')
    })

    const viewRunsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View Runs'))
    expect(viewRunsButton).toBeDefined()
    if (!viewRunsButton) return

    await act(async () => {
      viewRunsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScenarioResults).toHaveBeenCalledWith('scenario-1')
    expect(container.textContent).toContain('No runs for this scenario yet.')
  })

  it('waits for an active project and ignores a stale project response after switching', async () => {
    const onSelectTest = vi.fn()
    const projectAResponse = deferred<{ scenarios: Array<Record<string, unknown>>; count: number }>()
    projectState.activeProject = null
    apiMock.listScenarios.mockImplementation((projectId?: string) => {
      if (projectId === 'project-a') return projectAResponse.promise
      if (projectId === 'project-b') {
        return Promise.resolve({
          scenarios: [scenarioFixture('scenario-b', 'Project B Scenario')],
          count: 1,
        })
      }
      return Promise.resolve({ scenarios: [], count: 0 })
    })
    apiMock.listScripts.mockImplementation((projectId?: string) => Promise.resolve({
      scripts: projectId ? [{ id: `script-${projectId}`, name: `${projectId} script`, protocol: 'http', version: '1.0.0', created_at: '' }] : [],
      count: projectId ? 1 : 0,
    }))

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    expect(apiMock.listScenarios).not.toHaveBeenCalled()
    expect(apiMock.listScripts).not.toHaveBeenCalled()
    expect(apiMock.listDataPools).not.toHaveBeenCalled()
    expect(apiMock.getAuthEffectiveAccess).not.toHaveBeenCalled()

    await act(async () => {
      projectState.activeProject = { id: 'project-a', name: 'Project A', created_at: '', updated_at: '' }
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()
    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-a')

    await act(async () => {
      projectState.activeProject = { id: 'project-b', name: 'Project B', created_at: '', updated_at: '' }
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    expect(apiMock.listScenarios).toHaveBeenCalledWith('project-b')
    expect(container.textContent).toContain('Project B Scenario')

    await act(async () => {
      projectAResponse.resolve({
        scenarios: [scenarioFixture('scenario-a', 'Project A Scenario')],
        count: 1,
      })
      await projectAResponse.promise
    })
    await flushPromises()

    expect(container.textContent).toContain('Project B Scenario')
    expect(container.textContent).not.toContain('Project A Scenario')
  })

  it('resets a pristine scenario edit before authoring in a different project', async () => {
    apiMock.listScenarios.mockImplementation((projectId?: string) => Promise.resolve({
      scenarios: projectId === 'project-a'
        ? [scenarioFixture('scenario-a', 'Project A Scenario')]
        : [],
      count: projectId === 'project-a' ? 1 : 0,
    }))
    projectState.activeProject = { id: 'project-a', name: 'Project A', created_at: '', updated_at: '' }
    const renderHub = () => root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)

    await act(async () => renderHub())
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Edit')?.click()
    })
    expect(container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')?.value).toBe('Project A Scenario')

    projectState.activeProject = { id: 'project-b', name: 'Project B', created_at: '', updated_at: '' }
    await act(async () => renderHub())
    await flushPromises()
    expect(container.textContent).toContain('Start With A Model')
    expect(container.textContent).not.toContain('Project A Scenario')

    await clickWizardStep('Scripts')
    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')
    expect(nameInput?.value).toBe('')
    if (!nameInput) return
    await act(async () => setInputValue(nameInput, 'Project B Scenario'))
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Review')
    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledTimes(1)
    expect(apiMock.updateScenario).not.toHaveBeenCalled()
  })

  it('gives the delete confirmation dialog a trapped, dismissible focus lifecycle', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [scenarioFixture('scenario-delete', 'Delete Me')],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    const deleteTrigger = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Delete')
    expect(deleteTrigger).toBeDefined()
    if (!deleteTrigger) return
    deleteTrigger.focus()

    await act(async () => {
      deleteTrigger.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushTimeouts()

    const dialog = container.querySelector('[role="dialog"]') as HTMLDivElement | null
    expect(dialog).not.toBeNull()
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.getAttribute('aria-labelledby')).toBe('delete-scenario-dialog-title')
    expect(dialog?.getAttribute('aria-describedby')).toBe('delete-scenario-dialog-description')
    expect(dialog?.getAttribute('tabindex')).toBe('-1')
    expect(document.activeElement?.textContent).toBe('Cancel')

    const dialogButtons = Array.from(dialog?.querySelectorAll('button') || [])
    const firstButton = dialogButtons[0]
    const lastButton = dialogButtons[dialogButtons.length - 1]
    expect(firstButton?.textContent).toBe('Cancel')
    expect(lastButton?.textContent).toBe('Delete Scenario')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(lastButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(firstButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    })
    await flushPromises()

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(deleteTrigger)
  })

  it('selects all visible scenarios and preserves failed rows after a partial bulk delete', async () => {
    vi.stubGlobal('confirm', vi.fn(() => true))
    let scenarios = [
      scenarioFixture('scenario-delete', 'Delete Me'),
      scenarioFixture('scenario-locked', 'Linked Results Scenario'),
    ]
    apiMock.listScenarios.mockImplementation(async () => ({ scenarios, count: scenarios.length }))
    apiMock.deleteScenario.mockImplementation(async (id: string) => {
      if (id === 'scenario-locked') throw new Error('scenario has linked results')
      scenarios = scenarios.filter(scenario => scenario.id !== id)
      return { deleted: id }
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    const selectAll = container.querySelector<HTMLInputElement>('input[aria-label="Select all visible scenarios"]')
    expect(selectAll).not.toBeNull()
    await act(async () => {
      selectAll?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Delete Selected (2)')

    await clickButton('Delete Selected (2)')
    await flushPromises()

    expect(apiMock.deleteScenario).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('Delete Me')
    expect(container.textContent).toContain('Linked Results Scenario')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Select Linked Results Scenario"]')?.checked).toBe(true)
    expect(container.textContent).toContain('Deleted 1 of 2 selected scenarios')
  })

  it('filters the scenario library by associated release', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [
        {
          id: 'scenario-checkout',
          name: 'Checkout Release Scenario',
          duration: '5m',
          assignment: { mode: 'auto' },
          scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
          data_pools: [],
          associated_releases: [{ id: 'release-29', name: 'PI29', application_id: 'app-1' }],
          result_count: 0,
          created_at: '2026-05-12T00:00:00Z',
          last_run_at: '2026-05-13T00:00:00Z',
        },
        {
          id: 'scenario-payments',
          name: 'Payments Release Scenario',
          duration: '5m',
          assignment: { mode: 'auto' },
          scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 10 }],
          data_pools: [],
          associated_releases: [{ id: 'release-30', name: 'PI30', application_id: 'app-1' }],
          result_count: 0,
          created_at: '2026-05-13T00:00:00Z',
          last_run_at: '',
        },
      ],
      count: 2,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    const releaseFilter = container.querySelector('select.library-release-filter') as HTMLSelectElement | null
    expect(releaseFilter).toBeTruthy()
    if (!releaseFilter) return

    expect(Array.from(releaseFilter.options).map(option => option.textContent)).toEqual(['All releases', 'PI29', 'PI30'])

    await act(async () => {
      setSelectValue(releaseFilter, 'PI29')
    })

    expect(container.textContent).toContain('1 matching scenario')
    expect(container.textContent).toContain('Checkout Release Scenario')
    expect(container.textContent).not.toContain('Payments Release Scenario')

    await act(async () => {
      setSelectValue(releaseFilter, 'PI30')
    })

    expect(container.textContent).toContain('Payments Release Scenario')
    expect(container.textContent).not.toContain('Checkout Release Scenario')

    await act(async () => {
      setSelectValue(releaseFilter, 'release-that-does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scenarios')
    expect(container.textContent).toContain('No scenarios match these filters.')
  })

  it('does not warn about optional remotes in a local scenario library', async () => {
    const onSelectTest = vi.fn()
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onNavigate={onNavigate} />)
    })

    await flushPromises()

    const noRemoteButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('No Remote Workspace'))
    const credentialsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Credentials Missing'))
    expect(noRemoteButton).toBeUndefined()
    expect(credentialsButton).toBeUndefined()
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('clones an existing scenario draft with data pool bindings from the wizard source step', async () => {
    const onSelectTest = vi.fn()
    const onOpenCommandCenter = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [
        { id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
        { id: 'script-2', name: 'Payments Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
      ],
      count: 2,
    })
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-source',
        name: 'Pinned Checkout',
        duration: '7m',
        assignment: { mode: 'auto' },
        scripts: [{ order: 0, script_id: 'script-1', script_name: 'Checkout Flow', name: 'Checkout', vusers: 20, ramp_up: '1m', initialization: 'at_runtime' }],
        data_pools: [{ pool_id: 'pool-1', pool_name: 'Customers', alias: 'customers', access_mode: 'consume', selection_strategy: 'unique' }],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T12:00:00Z',
        updated_at: '2026-05-12T12:00:00Z',
        last_run_test_id: 'run-1',
        last_run_at: '2026-05-13T00:01:00Z',
        last_run_ended_at: '2026-05-13T00:08:00Z',
        script_count: 1,
      }],
      count: 1,
    })
    apiMock.listScenarioResults.mockResolvedValueOnce({
      tests: [{
        id: 'run-1',
        name: 'Checkout Load 001',
        status: 'COMPLETE',
        release_id: 'release-1',
        groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'http', vusers: 20 }],
        total_vusers: 20,
        duration: '7m',
        created_at: '2026-05-13T00:00:00Z',
        start_time: '2026-05-13T00:01:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onOpenCommandCenter={onOpenCommandCenter} />)
    })

    await flushPromises()

    await clickButton('Scenario Builder')
    await clickButton('Model from Existing Scenario')
    const authoredName = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.placeholder === 'Scenario name')
    expect(authoredName).toBeDefined()
    if (!authoredName) return
    await act(async () => {
      setInputValue(authoredName, 'Payments Against Checkout Model')
    })
    await chooseLibraryScriptForGroup('Payments Flow')
    await clickWizardStep('Shape / Source')
    const cloneRow = Array.from(container.querySelectorAll('.scenario-clone-table tbody tr')).find(row => row.textContent?.includes('Pinned Checkout'))
    expect(cloneRow).toBeDefined()
    await act(async () => {
      cloneRow?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('20 peak VUsers')
    expect(container.textContent).toContain('Choose Scenario To Model')
    expect(container.textContent).not.toContain('Existing Scripts Library')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Associated Releases')
    expect(container.textContent).toContain('Updated')
    expect(container.textContent).not.toContain('Test ID')
    expect(container.textContent).toContain('Start')
    expect(container.textContent).not.toContain('Finish')
    expect(container.textContent).not.toContain('Last Run')
    expect(container.textContent).not.toContain('Automatic placement')
    expect(container.textContent).not.toContain('Uses controller recommendations')
    expect(container.querySelector('.scenario-clone-card')).toBeNull()
    expect(apiMock.listScenarioResults).toHaveBeenCalledWith('scenario-source')
    const selectedCloneRow = Array.from(container.querySelectorAll('.scenario-clone-table tr.selected')).find(row => row.textContent?.includes('Pinned Checkout'))
    expect(selectedCloneRow).toBeDefined()
    expect(selectedCloneRow?.textContent).toContain('Local')
    expect(selectedCloneRow?.textContent).not.toContain('run-1')
    expect(selectedCloneRow?.textContent).toContain('Checkout Flow')
    expect(selectedCloneRow?.textContent).toContain('No releases yet')
    expect(selectedCloneRow?.textContent).toContain('May 12, 2026')
    expect(selectedCloneRow?.textContent).not.toContain(new Date('2026-05-13T00:01:00Z').toLocaleString())
    expect(selectedCloneRow?.textContent).not.toContain(new Date('2026-05-13T00:08:00Z').toLocaleString())
    const chart = container.querySelector('[data-testid="scenario-model-chart"]')
    const runPanel = container.querySelector('.scenario-source-runs-panel')
    expect(runPanel).toBeDefined()
    const runPanelText = Array.from(container.querySelectorAll('.scenario-source-runs-panel')).map(panel => panel.textContent || '').join(' ')
    expect(runPanelText).toContain('Past Scenario Runs')
    expect(runPanelText).toContain('Checkout Load 001')
    if (chart && runPanel) {
      expect((chart.compareDocumentPosition(runPanel) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0).toBe(true)
    }
    const runPanelButtons = Array.from(runPanel?.querySelectorAll('button') ?? []) as HTMLButtonElement[]
    const openRunButton = runPanelButtons.find(button => button.textContent?.trim() === 'Open')
    const runDashboardButton = runPanelButtons.find(button => button.textContent?.trim() === 'Run Dashboard')
    expect(openRunButton).toBeDefined()
    expect(runDashboardButton).toBeDefined()

    await act(async () => {
      openRunButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onSelectTest).toHaveBeenCalledWith('run-1')

    await act(async () => {
      runDashboardButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onOpenCommandCenter).toHaveBeenCalledWith('run-1')

    await clickWizardStep('Scripts')

    expect(Array.from(container.querySelectorAll('input')).some(input => input.value === 'Payments Against Checkout Model')).toBe(true)
    expect(container.textContent).not.toContain('Choose Scenario To Model')
    expect(container.textContent).toContain('Existing Scripts Library')

    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Payments Against Checkout Model',
      duration: '7m',
      groups: [expect.objectContaining({
        name: 'Payments Flow',
        script_id: 'script-2',
        vusers: 20,
        ramp_up: '1m',
      })],
      data_pools: [expect.objectContaining({
        pool_id: 'pool-1',
        alias: 'customers',
        access_mode: 'read_only',
        selection_strategy: 'unique',
      })],
    }))
  })

  it('edits scenarios with multiple script groups without dropping secondary scripts', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [
        { id: 'script-1', name: 'Checkout Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
        { id: 'script-2', name: 'Payments Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' },
      ],
      count: 2,
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-multi',
        name: 'Checkout Plus Payments',
        duration: '8m',
        assignment: { mode: 'auto' },
        scripts: [
          { order: 0, script_id: 'script-1', script_name: 'Checkout Flow', name: 'Checkout', vusers: 25, ramp_up: '2m', initialization: 'at_runtime' },
          { order: 1, script_id: 'script-2', script_name: 'Payments Flow', name: 'Payments', vusers: 5, ramp_up: '1m', initialization: 'at_once' },
        ],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        updated_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
        script_count: 2,
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    if (!editButton) return

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(wizardStepMarker('Shape / Source')).toBe('✓')
    expect(wizardStepComplete('Shape / Source')).toBe(true)

    const groupCards = Array.from(container.querySelectorAll('.scenario-script-group-card'))
    expect(groupCards).toHaveLength(2)
    expect(groupCards[0].textContent).toContain('Checkout Flow')
    expect(groupCards[1].textContent).toContain('Payments Flow')
    const secondGroupInitSelect = Array.from(groupCards[1].querySelectorAll('select')).find(select => select.textContent?.includes('Init/teardown once per group')) as HTMLSelectElement | undefined
    expect(secondGroupInitSelect?.value).toBe('at_once')

    await clickWizardStep('Run Settings')
    const paymentGroupTab = Array.from(container.querySelectorAll('.scenario-group-tab')).find(tab => tab.textContent?.includes('Payments'))
    expect(paymentGroupTab).toBeDefined()
    if (!paymentGroupTab) return

    await act(async () => {
      paymentGroupTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const loadGroupCards = Array.from(container.querySelectorAll('.scenario-load-group-panel .scenario-script-group-card'))
    expect(loadGroupCards).toHaveLength(1)
    expect(loadGroupCards[0].textContent).toContain('Payments')
    const secondGroupVUsers = Array.from(loadGroupCards[0].querySelectorAll('input')).find(input => input.type === 'number')
    expect(secondGroupVUsers).toBeDefined()
    if (!secondGroupVUsers) return

    await act(async () => {
      setInputValue(secondGroupVUsers, '15')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateScenario).toHaveBeenCalledWith('scenario-multi', expect.objectContaining({
      name: 'Checkout Plus Payments',
      duration: '8m',
      groups: [
        expect.objectContaining({
          name: 'Checkout',
          script_id: 'script-1',
          vusers: 25,
          ramp_up: '2m',
          initialization: 'at_runtime',
        }),
        expect.objectContaining({
          name: 'Payments',
          script_id: 'script-2',
          vusers: 15,
          ramp_up: '1m',
          initialization: 'at_once',
        }),
      ],
    }))
  })

  it('edits and clones the complete stored scenario aggregate without clearing hidden fields', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Orders Flow', protocol: 'kafka', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-2', name: 'Orders', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    const storedScenario = {
      id: 'scenario-fidelity',
      name: 'Messaging Fidelity',
      duration: '8m',
      start_delay: '45s',
      scenario_mode: 'goal_oriented' as const,
      assignment: { mode: 'region_weighted' as const, region_weights: { east: 70, west: 30 } },
      goal_config: {
        target_average_tps: 80,
        max_throttled_requests: 3,
        max_end_to_end_latency_ms: 250,
        max_partition_skew_pct: 15,
        max_redelivery_rate_pct: 2,
        max_dead_letter_rate_pct: 1,
        stop_on_sla_breach: true,
      },
		runtime_settings: {
			iterations: 4,
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'normal', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200 },
			error_handling: { max_retries: 3, retry_delay_ms: 150, retry_backoff: 'exponential' },
        run_logic: { iterations: 4, workload_model: 'closed' },
        http: { version: '2' },
        web_runtime: { source_ips: ['10.0.0.8'] },
        base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
        redaction_rules: [{ name: 'token', pattern: 'secret-[0-9]+', replacement: '[MASKED]' }],
      },
      scripts: [{
        order: 0,
        script_id: 'script-1',
        script_name: 'Orders Flow',
        protocol: 'kafka',
        name: 'Orders',
        vusers: 12,
        ramp_up: '1m',
        ramp_down: '30s',
        iterations: 7,
        initialization: 'at_once' as const,
			runtime_settings: {
				run_logic: { iterations: 7, workload_model: 'closed' },
				env: { TOPIC: 'orders' },
			},
			assignment: {
				mode: 'auto' as const,
				required_labels: { tier: 'messaging' },
			},
      }],
      data_pools: [
        { id: 'binding-1', pool_id: 'pool-1', pool_name: 'Customers', alias: 'customers', access_mode: 'consume' as const, selection_strategy: 'unique' as const },
        { id: 'binding-2', pool_id: 'pool-2', pool_name: 'Orders', alias: 'orders', access_mode: 'read_only' as const, selection_strategy: 'sequential' as const },
      ],
      associated_releases: [],
      result_count: 0,
      created_at: '2026-05-12T00:00:00Z',
      updated_at: '2026-05-12T00:00:00Z',
      script_count: 1,
    }
    apiMock.listScenarios.mockResolvedValue({ scenarios: [storedScenario], count: 1 })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await clickButton('Save draft')
    await flushPromises()

    const fidelityPayload = expect.objectContaining({
      name: 'Messaging Fidelity',
      start_delay: '45s',
      assignment: { mode: 'region_weighted', region_weights: { east: 70, west: 30 } },
      groups: [expect.objectContaining({
        name: 'Orders',
        script_id: 'script-1',
        ramp_down: '30s',
        initialization: 'at_once',
			runtime_settings: expect.objectContaining({
				run_logic: { iterations: 7, workload_model: 'closed' },
				env: { TOPIC: 'orders' },
			}),
			assignment: {
				mode: 'auto',
				required_labels: { tier: 'messaging' },
			},
      })],
      data_pools: [
        expect.objectContaining({ id: 'binding-1', pool_id: 'pool-1', alias: 'customers', access_mode: 'read_only' }),
        expect.objectContaining({ id: 'binding-2', pool_id: 'pool-2', alias: 'orders', access_mode: 'read_only' }),
      ],
      goal_config: expect.objectContaining({
        max_throttled_requests: 3,
        max_end_to_end_latency_ms: 250,
        max_partition_skew_pct: 15,
        max_redelivery_rate_pct: 2,
        max_dead_letter_rate_pct: 1,
      }),
		runtime_settings: expect.objectContaining({
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'gaussian', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200, ignore: false },
			error_handling: { max_retries: 3, retry_delay_ms: 150, retry_backoff: 'exponential' },
        run_logic: { iterations: 4, workload_model: 'closed' },
        http: { version: '2' },
        web_runtime: { source_ips: ['10.0.0.8'] },
        base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
        redaction_rules: [{ name: 'token', pattern: 'secret-[0-9]+', replacement: '[MASKED]' }],
      }),
    })
    expect(apiMock.updateScenario).toHaveBeenCalledWith('scenario-fidelity', fidelityPayload)
    const firstFidelityUpdate = apiMock.updateScenario.mock.calls.find(call => call[0] === 'scenario-fidelity')?.[1]
    expect(firstFidelityUpdate?.groups?.[0]).not.toHaveProperty('iterations')

    // Re-open the same durable weighted assignment. The root editor must expose
    // and persist the weights and loss-tolerance policy instead of showing them
    // as read-only preservation text.
    const editAgain = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editAgain).toBeDefined()
    await act(async () => {
      editAgain?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Controller/Generator')
    const rootPlacement = container.querySelector('.scenario-root-placement-editor')
    const placementModes = Array.from(rootPlacement?.querySelectorAll('[aria-label="Scenario generator placement"] label') || [])
    expect(placementModes.map(label => label.textContent)).toEqual(expect.arrayContaining([
      expect.stringContaining('Automatic placement'),
      expect.stringContaining('Pin to host(s)'),
      expect.stringContaining('Region-weighted placement'),
      expect.stringContaining('Load-zone-weighted placement'),
      expect.stringContaining('Require capability'),
    ]))
    const weightedRadio = placementModes.find(label => label.textContent?.includes('Region-weighted placement'))?.querySelector<HTMLInputElement>('input')
    expect(weightedRadio?.checked).toBe(true)
    const eastWeight = rootPlacement?.querySelector<HTMLInputElement>('[aria-label="Region east weight"]')
    const tolerateLoss = Array.from(rootPlacement?.querySelectorAll('label') || [])
      .find(label => label.textContent?.includes('Tolerate generator loss'))
      ?.querySelector<HTMLInputElement>('input')
    expect(eastWeight).toBeDefined()
    expect(tolerateLoss?.checked).toBe(false)
    if (!eastWeight || !tolerateLoss) return
    await act(async () => {
      setInputValue(eastWeight, '85')
      tolerateLoss.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickButton('Save draft')
    await flushPromises()
    expect(apiMock.updateScenario).toHaveBeenLastCalledWith('scenario-fidelity', expect.objectContaining({
      assignment: {
        mode: 'region_weighted',
        region_weights: { east: 85, west: 30 },
        tolerate_generator_loss: true,
      },
    }))

    // Re-open once more and explicitly choose automatic placement. The hidden
    // weighted section must be removed from the serialized assignment.
    const editForAuto = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editForAuto).toBeDefined()
    await act(async () => {
      editForAuto?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickWizardStep('Run Settings')
    await clickRunSettingsTab('Controller/Generator')
    const autoRootPlacement = container.querySelector('.scenario-root-placement-editor')
    const autoRadio = Array.from(autoRootPlacement?.querySelectorAll('[aria-label="Scenario generator placement"] label') || [])
      .find(label => label.textContent?.includes('Automatic placement'))
      ?.querySelector<HTMLInputElement>('input')
    expect(autoRadio).toBeDefined()
    await act(async () => {
      autoRadio?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickButton('Save draft')
    await flushPromises()
    expect(apiMock.updateScenario).toHaveBeenLastCalledWith('scenario-fidelity', expect.objectContaining({
      assignment: { mode: 'auto' },
    }))

    await act(async () => {
      const cancel = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Cancel')
      cancel?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    const duplicateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Duplicate')
    expect(duplicateButton).toBeDefined()
    await act(async () => {
      duplicateButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Messaging Fidelity (Copy)',
      start_delay: '45s',
		groups: [expect.objectContaining({
			script_id: 'script-1',
			runtime_settings: expect.objectContaining({ env: { TOPIC: 'orders' } }),
			assignment: expect.objectContaining({
				mode: 'auto',
				required_labels: { tier: 'messaging' },
			}),
      })],
      data_pools: [
        expect.objectContaining({ pool_id: 'pool-1', alias: 'customers' }),
        expect.objectContaining({ pool_id: 'pool-2', alias: 'orders' }),
      ],
      goal_config: expect.objectContaining({ max_throttled_requests: 3, max_dead_letter_rate_pct: 1 }),
		runtime_settings: expect.objectContaining({
			pacing: { type: 'random_delay', min_delay_ms: 100, max_delay_ms: 900 },
			think_time: { type: 'gaussian', mean_ms: 250, std_ms: 75, min_ms: 25, max_ms: 900, multiplier: 1.5, limit_ms: 1200, ignore: false },
			base_url: 'https://orders.example.test',
        env: { REGION: 'east' },
      }),
    }))
    const clonedCalls = apiMock.createScenario.mock.calls
    const clonedPayload = clonedCalls[clonedCalls.length - 1]?.[0]
    expect(clonedPayload.groups?.[0]).not.toHaveProperty('iterations')
    expect(clonedPayload.data_pools.every((binding: { id?: string }) => binding.id === undefined)).toBe(true)
  })

  it('clearing the wizard pool selection removes only the visible binding and preserves the rest', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Orders Flow', protocol: 'http', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-2', name: 'Orders', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-multi-pool',
        name: 'Multi Pool',
        duration: '5m',
        assignment: { mode: 'auto' },
        scripts: [{ order: 0, script_id: 'script-1', script_name: 'Orders Flow', name: 'Orders', vusers: 5, ramp_up: '1m', initialization: 'at_runtime' }],
        data_pools: [
          { id: 'binding-1', pool_id: 'pool-1', pool_name: 'Customers', alias: 'customers', access_mode: 'read_only' as const, selection_strategy: 'sequential' as const },
          { id: 'binding-2', pool_id: 'pool-2', pool_name: 'Orders', alias: 'orders', access_mode: 'read_only' as const, selection_strategy: 'unique' as const },
        ],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        updated_at: '2026-05-12T00:00:00Z',
        script_count: 1,
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeDefined()
    await act(async () => {
      editButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await clickWizardStep('Data')
    const poolSelect = findControlByLabel<HTMLSelectElement>(container, 'Data Pool', 'select')
    expect(poolSelect).toBeDefined()
    expect(poolSelect?.value).toBe('pool-1')
    if (!poolSelect) return
    await act(async () => {
      setSelectValue(poolSelect, '')
    })

    await clickButton('Save draft')
    await flushPromises()

    const payload = apiMock.updateScenario.mock.calls.find(call => call[0] === 'scenario-multi-pool')?.[1]
    expect(payload).toBeDefined()
    expect(payload?.data_pools).toEqual([
      expect.objectContaining({ pool_id: 'pool-2', alias: 'orders', access_mode: 'read_only' }),
    ])
  })

  it('creates a browser scenario with mobile runtime overrides', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-browser', name: 'Mobile Checkout', protocol: 'browser', version: '1.0.0', created_at: '2026-05-12T00:00:00Z' }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Mobile Browser Scenario')
    })

    await chooseLibraryScriptForGroup('Mobile Checkout')
    await clickWizardStep('Run Settings')

    expect(container.textContent).toContain('Browser/Script Runtime')
    expect(container.textContent).not.toContain('Device profile')

    await clickRunSettingsTab('Browser/Script Runtime')
    const scope = findControlByLabel<HTMLSelectElement>(container, 'Runtime scope', 'select')!
    await act(async () => setSelectValue(scope, '0'))

    expect(container.textContent).toContain('Runtime overrides')

    // Browser device settings fold into the HTTP Transport tab's "Browser emulation" sub-tab.
    await clickRunSettingsTab('HTTP Transport')
    await act(async () => {
      const browserEmulationTab = Array.from(container.querySelectorAll('.http-transport-tab')).find(tab => tab.textContent?.includes('Browser (Playwright)'))
      browserEmulationTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const profileSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Device profile')) as HTMLSelectElement | undefined
    const orientationSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Orientation')) as HTMLSelectElement | undefined
    const touchSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Touch mode')) as HTMLSelectElement | undefined
    const headlessSelect = Array.from(container.querySelectorAll('select')).find(select => select.parentElement?.textContent?.includes('Headless')) as HTMLSelectElement | undefined

    expect(profileSelect).toBeDefined()
    expect(orientationSelect).toBeDefined()
    expect(touchSelect).toBeDefined()
    expect(headlessSelect).toBeDefined()
    if (!profileSelect || !orientationSelect || !touchSelect || !headlessSelect) return

    await act(async () => {
      setSelectValue(profileSelect, 'iphone_se')
      setSelectValue(orientationSelect, 'portrait')
      setSelectValue(touchSelect, 'true')
      setSelectValue(headlessSelect, 'false')
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const browserConfigMatcher = expect.objectContaining({
      device_profile: 'iphone_se',
      orientation: 'portrait',
      touch_mode: true,
      headless: false,
    })
    // The tabbed runtime editor persists browser device settings into the
    // per-group browser_config section.
    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Mobile Browser Scenario',
      groups: expect.arrayContaining([
        expect.objectContaining({
          runtime_settings: expect.objectContaining({ browser_config: browserConfigMatcher }),
        }),
      ]),
    }))
    // NOTE: the per-script capacity estimate (estimateTest) no longer carries the
    // per-group runtime overrides after the tabbed-runtime-editor refactor, so the
    // previous assertion that the estimate reflected browser_config no longer holds.
  })

  it('shows saved browser runtime defaults in the scenario library', async () => {
    const onSelectTest = vi.fn()
    apiMock.listScenarios.mockResolvedValue({
      scenarios: [{
        id: 'scenario-1',
        name: 'Mobile Checkout',
        duration: '5m',
        assignment: { mode: 'auto' },
        runtime_settings: {
          browser: {
            device_profile: 'iphone_se',
            orientation: 'portrait',
            touch_mode: true,
            headless: false,
          },
        },
        scripts: [{ script_id: 'script-browser', script_name: 'Mobile Checkout', vusers: 5 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '2026-05-13T00:00:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Mobile Checkout')
    expect(container.textContent).toContain('Browser')
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')
  })

  it('applies workload templates and renders load-model validation findings', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Choose Workload Shape')
    expect(container.querySelector<HTMLDetailsElement>('.scenario-advanced-shapes')?.open).toBe(false)
    expect(container.querySelector('.scenario-advanced-shapes')?.textContent).toContain('Stepped Ramp')
    expect(container.querySelector('.scenario-advanced-shapes')?.textContent).not.toContain('Closed VUser')
	const templateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Goal-Driven API'))
    expect(templateButton).toBeDefined()
    if (!templateButton) return

    await act(async () => {
      templateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')
	const durationField = getDurationFieldByLabel(container, 'Duration')

	expect(durationField?.input.value).toBe('1')
	expect(durationField?.select.value).toBe('h')
	expect(container.textContent).not.toContain('Scenario Mode')
	expect(container.textContent).toContain('Goal-Driven Scenario Setup')

    const runSettingsValidateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Load Model'))
    expect(runSettingsValidateButton).toBeUndefined()

    await clickWizardStep('Review')

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Load Model'))
    expect(validateButton).toBeDefined()
    if (!validateButton) return

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateScenarioLoadModel).toHaveBeenCalledWith(expect.objectContaining({
	  duration: '60m',
      scenario_mode: 'goal_oriented',
      goal_config: expect.objectContaining({
        target_average_tps: 25,
		min_vusers: 10,
		max_vusers: 20,
        max_dead_letter_rate_pct: 0.5,
        stop_on_sla_breach: true,
        sla_breach_window: '15s',
      }),
      groups: [expect.objectContaining({
        script_id: 'script-1',
        vusers: 10,
        ramp_up: '2m',
      })],
    }))
    expect(container.textContent).toContain('Needs Review')
    expect(container.textContent).toContain('Generator Placement')
    expect(container.textContent).toContain('Auto-assignment will use configured generators.')
  })

  it('applies the chaos resilience template with shakeout and evidence settings', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')
    await clickButton('Chaos Resilience With Shakeout Gate')
    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Chaos Checkout Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    expect(getDurationFieldByLabel(container, 'Duration')?.input.value).toBe('18')
    expect(getDurationFieldByLabel(container, 'Duration')?.select.value).toBe('m')
    expect(container.textContent).toContain('Pre-Run Assurance')
    expect(container.textContent).toContain('Script shakeout gate')
    expect(container.textContent).toContain('Snapshot on error')
    expect(container.textContent).toContain('Goal Controller')

    await clickButton('Save draft')
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Chaos Checkout Scenario',
      duration: '18m',
      scenario_mode: 'goal_oriented',
      script_shakeout: true,
      resource_baseline: true,
      goal_config: expect.objectContaining({
        target_average_tps: 15,
        max_error_pct: 5,
      }),
      runtime_settings: {
        error_handling: {
          continue_on_error: true,
          snapshot_on_error: true,
        },
      },
      groups: [expect.objectContaining({
        name: 'chaos-steady-state',
        script_id: 'script-1',
        vusers: 25,
        ramp_up: '3m',
        ramp_down: '2m',
        stages: [
          { name: 'Baseline', type: 'ramp_up', duration: '3m', target_vusers: 25 },
          { name: 'Turbulence', type: 'hold', duration: '10m', target_vusers: 25, target_tps: 15 },
          { name: 'Recovery', type: 'ramp_down', duration: '5m', target_vusers: 5 },
        ],
      })],
    }))
  })

  it('renders the unified workload shape catalog in scenario authoring', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Choose Workload Shape')
    expect(container.querySelector<HTMLDetailsElement>('.scenario-advanced-shapes')?.open).toBe(false)
    expect(container.querySelector('.scenario-advanced-shapes')?.textContent).toContain('Stepped Ramp')
    expect(container.querySelector('.scenario-advanced-shapes')?.textContent).not.toContain('Closed VUser')
    expect(container.textContent).toContain('Closed VUser')
    expect(container.textContent).toContain('Supported')
    expect(container.textContent).toContain('groups[].vusers')
    expect(container.textContent).toContain('Stepped Ramp')
    expect(container.textContent).toContain('Supported')
    expect(container.textContent).toContain('groups[].stages[].target_vusers')
    expect(container.textContent).toContain('Chaos Resilience')
    expect(container.textContent).toContain('script_shakeout')
    expect(container.querySelectorAll('.scenario-shape-visual')).toHaveLength(6)
    expect(container.querySelector('[aria-label="Closed VUser workload shape"]')).toBeTruthy()
    const productionProfileVisual = container.querySelector('[aria-label="Production Traffic Profile workload shape"]')
    expect(productionProfileVisual?.querySelector('.scenario-shape-line')?.getAttribute('d'))
      .toBe('M12 50C22 49 29 45 36 38C44 29 51 20 62 17C74 14 84 20 91 29C99 38 103 45 108 49')

	await clickButton('Goal-Driven API')
    expect(container.querySelector('.scenario-builder-preview')?.textContent).toContain('Sizing incomplete')
    expect(Array.from(container.querySelectorAll('[data-testid="scenario-model-y-axis"]')).map(axis => axis.getAttribute('data-label'))).toEqual(['TPS'])
    expect(container.querySelector('[data-testid="scenario-model-y-axis"]')?.getAttribute('data-orientation')).toBe('left')
    expect(container.textContent).not.toContain('peak VUsers')
    expect(container.textContent).toContain('VUser demand is determined by the goal controller at runtime')
  })

  it('opens the production traffic workflow from the workload shape catalog', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await selectManualShapeByName('Production Traffic Profile')
    await flushPromises()

    expect(container.textContent).toContain('Build From Production Traffic')
    expect(container.querySelector('.traffic-profile-panel')).toBeTruthy()
    expect(container.textContent).toContain('CSV / XLSX')
  })

  it('converts a newly disabled selected shape to Custom Workload', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} featurePolicySignal={0} />)
    })
    await flushPromises()
    await clickWizardStep('Shape / Source')
	await clickButton('Goal-Driven API')
    expect(container.querySelector('.scenario-builder-preview')?.textContent).toContain('Sizing incomplete')

    apiMock.getLoadModelSupportMatrix.mockResolvedValueOnce({
      support_matrix: [{
        id: 'closed-vuser',
        name: 'Closed VUser',
        category: 'vuser',
        status: 'executed',
        apexload_shape: 'Scenario groups with fixed VUsers and duration.',
        fields: ['groups[].vusers'],
        notes: 'Primary execution model.',
      }],
      status_counts: { executed: 1 },
      notes: [],
    })
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} featurePolicySignal={1} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Compatible draft values were retained as a Custom Workload.')
  })

  it('toggles between the scenario library and builder wizard views', async () => {
    const onSelectTest = vi.fn()
    const onScenarioViewChange = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onScenarioViewChange={onScenarioViewChange} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Scenario Library')
    expect(container.querySelector('.scenario-builder')).toBeNull()
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Scenario Builder')

    expect(onScenarioViewChange).toHaveBeenLastCalledWith('builder')
    expect(container.textContent).toContain('Scenario Builder')
    expect(container.textContent).not.toContain('Model Preview')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Shape / Source')

    expect(container.textContent).toContain('Model Preview')
    expect(container.querySelector('[data-testid="scenario-model-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(Array.from(container.querySelectorAll('[data-testid="scenario-model-y-axis"]')).map(axis => axis.getAttribute('data-label'))).toContain('VUsers')
    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Scripts')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).toContain('Existing Scripts Library')
    expect(container.textContent).toContain('Checkout Flow')

    await clickWizardStep('Run Settings')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Data')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickWizardStep('Review')

    expect(container.textContent).not.toContain('Scenario Library (')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Cancel')

    expect(container.textContent).toContain('Scenario Library')
    expect(container.textContent).not.toContain('Existing Scripts Library')

    await clickButton('Scenario Library')

    expect(onScenarioViewChange).toHaveBeenLastCalledWith('library')
    expect(container.textContent).toContain('Scenario Library')
    expect(container.querySelector('.scenario-builder')).toBeNull()
  })

  it('only enables adding another group after every existing group has a script', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()
    await clickWizardStep('Scripts')

    const groupList = container.querySelector('.scenario-script-group-list.scenario-script-group-list-spacious')
    expect(groupList).toBeDefined()
    const addGroupButton = groupList?.querySelector<HTMLButtonElement>('.scenario-add-group-card')
    expect(addGroupButton?.disabled).toBe(true)
    expect(addGroupButton?.textContent).toContain('Add a script to every existing group first')

    const libraryAddButton = Array.from(container.querySelectorAll<HTMLButtonElement>('.scenario-existing-script-library button'))
      .find(button => button.textContent?.includes('Add Group'))
    expect(libraryAddButton?.disabled).toBe(true)

    await clickButton('Add Group')
    expect(groupList?.querySelectorAll('.scenario-script-group-card')).toHaveLength(1)

    await chooseLibraryScriptForGroup()
    expect(addGroupButton?.disabled).toBe(false)

    await clickButton('Add Group')
    expect(groupList?.querySelectorAll('.scenario-script-group-card')).toHaveLength(2)
    expect(addGroupButton?.disabled).toBe(true)

    await chooseLibraryScriptForGroup('Checkout Flow', 1)
    expect(addGroupButton?.disabled).toBe(false)
    const groupNameInputs = Array.from(groupList?.querySelectorAll<HTMLInputElement>('input[placeholder^="Group"]') || [])
    expect(groupNameInputs.map(input => input.value)).toEqual(['Checkout Flow', 'Checkout Flow (#2)'])

    await act(async () => {
      setInputValue(groupNameInputs[1], '  checkout   flow ')
    })
    expect(groupNameInputs.every(input => input.getAttribute('aria-invalid') === 'true')).toBe(true)
    expect(container.textContent).toContain('Group names must be unique. Rename duplicate groups before continuing.')
    expect(addGroupButton?.disabled).toBe(true)
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save draft'))
    expect(saveButton?.disabled).toBe(true)
  })

  it.each(['Required variables are missing', 'Controller unavailable'])('retains a failed save error: %s', async message => {
    const handoff = vi.fn()
    apiMock.createScenario.mockRejectedValueOnce(new Error(message))
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={() => {}} onCreateTestFromScenario={handoff} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')!, 'Failure recovery'))
    await clickWizardStep('Review')
    await clickButton('Save & Create Test Run')
    expect(container.querySelector('[role="alert"]')?.textContent).toBe(message)
    expect(document.activeElement).toBe(container.querySelector('[role="alert"]'))
    expect(container.textContent).not.toContain('Scenario was saved')
    expect(handoff).not.toHaveBeenCalled()
    expect(container.querySelector('.scenario-builder')).not.toBeNull()
    await clickButton('Save draft')
    expect(apiMock.createScenario).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Saved Failure recovery')
  })

  it('locks the draft during save and never creates duplicates from repeated clicks', async () => {
    let finish!: (value: { id: string }) => void
    apiMock.createScenario.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={() => {}} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')!, 'Single save'))
    const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save draft')!
    await act(async () => { save.click(); save.click() })
    expect(apiMock.createScenario).toHaveBeenCalledTimes(1)
    expect(container.querySelector('fieldset')?.disabled).toBe(true)
    await act(async () => finish({ id: 'saved-once' }))
    expect(container.textContent).toContain('Saved Single save')
  })

  it('does not claim success when a successful save response has no id', async () => {
    apiMock.createScenario.mockResolvedValueOnce({})
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={() => {}} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[placeholder="Scenario name"]')!, 'Incomplete response'))
    await clickButton('Save draft')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Check the scenario library before retrying')
    expect(container.querySelector('.scenario-builder')).not.toBeNull()
  })

  it.each(['csv_driven', 'traffic_profile', 'existing'])('invalidates the previous model when switching to %s', async source => {
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={() => {}} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await selectManualShapeByName('Closed VUser')
    await clickWizardStep('Shape / Source')
    const select = findControlByLabel<HTMLSelectElement>(container, 'Workload source', 'select')!
    await act(async () => setSelectValue(select, source))
    expect(container.querySelector('.scenario-builder-preview')).toBeNull()
    expect(wizardStepComplete('Shape')).toBe(false)
    expect(wizardStepComplete('Run Settings')).toBe(false)
    expect(container.textContent).toContain('Choose and apply a workload source')
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save draft')?.disabled).toBe(true)
    await act(async () => setSelectValue(select, 'shape'))
    await selectManualShapeByName('Closed VUser')
    expect(container.querySelector('.scenario-builder-preview')).not.toBeNull()
  })

  it('drops an in-flight validation result after the draft changes', async () => {
    let finish!: (report: unknown) => void
    apiMock.validateScenarioLoadModel.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    await act(async () => root.render(<TestHub testStatuses={{}} onSelectTest={() => {}} initialView="builder" />))
    await flushPromises()
    await chooseLibraryScriptForGroup()
    await clickWizardStep('Review')
    await clickButton('Validate Load Model')
    await act(async () => setInputValue(container.querySelector<HTMLInputElement>('input[aria-label="Scenario name"]')!, 'Changed while validating'))
    await act(async () => finish({ status: 'ready', items: [], total_vusers: 10 }))
    expect(container.querySelector('.scenario-load-validation')).toBeNull()
    expect(container.querySelector('.scenario-builder-progress')?.textContent).toContain('Not validated')
  })

  it('saves from review and opens a new test run for the saved scenario', async () => {
    const onSelectTest = vi.fn()
    const onCreateTestFromScenario = vi.fn()
    apiMock.createScenario.mockResolvedValueOnce({ id: 'scenario-new-run' })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} onCreateTestFromScenario={onCreateTestFromScenario} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Review Handoff Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Review')

    const saveAndCreateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save & Create Test Run'))
    expect(saveAndCreateButton).toBeDefined()
    if (!saveAndCreateButton) return

    await act(async () => {
      saveAndCreateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Review Handoff Scenario',
    }))
    expect(onCreateTestFromScenario).toHaveBeenCalledWith('scenario-new-run')
  })

  it('creates a scenario with explicit staged load segments', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Staged Checkout Scenario')
    })

    await chooseLibraryScriptForGroup()
    await clickWizardStep('Run Settings')

    const enableStagesButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Enable Stages'))
    expect(enableStagesButton).toBeDefined()
    if (!enableStagesButton) return

    await act(async () => {
      enableStagesButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Peak staged VUsers: 10')
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Staged Checkout Scenario',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        vusers: 10,
        stages: [
          { name: 'Warmup', type: 'ramp_up', duration: '24s', target_vusers: 10 },
          { name: 'Steady', type: 'hold', duration: '24s', target_vusers: 10 },
          { name: 'Cooldown', type: 'ramp_down', duration: '12s', target_vusers: 1 },
        ],
      })],
    }))
  })

  it('blocks saving when stair-step stages exceed scenario duration', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Blocked Stage Scenario')
    })

    await clickWizardStep('Run Settings')
    await clickButton('Enable Stages')

    let durationField = getDurationFieldByLabel(container, 'Duration')
    expect(durationField).not.toBeNull()
    if (!durationField) return

    const durationInput = durationField.input
    await act(async () => {
      setInputValue(durationInput, '30')
    })
    await flushPromises()

    durationField = getDurationFieldByLabel(container, 'Duration')
    expect(durationField).not.toBeNull()
    if (!durationField) return

    const durationUnitSelect = durationField.select
    await act(async () => {
      setSelectValue(durationUnitSelect, 's')
    })

    expect(container.textContent).toContain('longer than')
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft')) as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(true)
  })

  it('persists once-only init and teardown scope from the scenario form', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    await clickWizardStep('Scripts')

    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    const initializationSelect = Array.from(container.querySelectorAll('select')).find(select => select.textContent?.includes('Init/teardown once per group')) as HTMLSelectElement | undefined
    expect(nameInput).toBeDefined()
    expect(initializationSelect).toBeDefined()
    if (!nameInput || !initializationSelect) return

    await act(async () => {
      setInputValue(nameInput, 'Once Setup Scenario')
      setSelectValue(initializationSelect, 'at_once')
    })

    expect(initializationSelect.value).toBe('at_once')

    await chooseLibraryScriptForGroup()
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save draft'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Once Setup Scenario',
      groups: [expect.objectContaining({
        script_id: 'script-1',
        initialization: 'at_once',
      })],
    }))
  })

  it('renders scenarios as read-only when manage_tests is denied', async () => {
    const onSelectTest = vi.fn()
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: false,
      authenticated: true,
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: '',
          operations: [{ id: 'manage_tests', label: 'Manage tests', allowed: false }],
        },
      ],
    })
    apiMock.listScenarios.mockResolvedValueOnce({
      scenarios: [{
        id: 'scenario-1',
        name: 'Read Only Checkout',
        duration: '5m',
        assignment: { mode: 'auto' },
        scripts: [{ script_id: 'script-1', script_name: 'Checkout Flow', vusers: 25 }],
        data_pools: [],
        associated_releases: [],
        result_count: 0,
        created_at: '2026-05-12T00:00:00Z',
        last_run_at: '',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Read Only Checkout')
    expect(container.textContent).not.toContain('+ New Scenario')
    expect(container.textContent).not.toContain('Duplicate')
    expect(container.textContent).not.toContain('Delete')
    expect(container.textContent).toContain('Read-only')
  })

  it('shows the scenario name in the builder header on every step once it is set', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name')
    expect(nameInput).toBeDefined()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'Header Visible Scenario')
    })

    const headerName = () => container.querySelector('.scenario-builder-name')?.textContent
    expect(headerName()).toBe('Header Visible Scenario')

    await clickWizardStep('Run Settings')
    expect(headerName()).toBe('Header Visible Scenario')

    await clickWizardStep('Review')
    expect(headerName()).toBe('Header Visible Scenario')
  })

  it('prompts for a scenario name on the review step instead of erroring when saving without one', async () => {
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={onSelectTest} />)
    })
    await flushPromises()

    await clickWizardStep('Scripts')
    await chooseLibraryScriptForGroup()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save draft')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // No scenario is created; the user is taken to review with a name prompt.
    expect(apiMock.createScenario).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('required')
    expect(container.textContent).toContain('Name the scenario before saving.')
    const promptInput = container.querySelector('.scenario-name-prompt input') as HTMLInputElement | null
    expect(promptInput).toBeTruthy()
    if (!promptInput) return

    await act(async () => {
      setInputValue(promptInput, 'Late Named Scenario')
    })

    const saveButtonAfter = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save draft')
    await act(async () => {
      saveButtonAfter?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createScenario).toHaveBeenCalledWith(expect.objectContaining({ name: 'Late Named Scenario' }))
  })

  it('keeps the review scenario name input mounted while typing', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    await clickWizardStep('Review')
    const nameInput = container.querySelector('.scenario-name-prompt input') as HTMLInputElement | null
    expect(nameInput).toBeTruthy()
    if (!nameInput) return

    await act(async () => {
      setInputValue(nameInput, 'N')
    })

    expect(container.querySelector('.scenario-name-prompt input')).toBe(nameInput)

    await act(async () => {
      setInputValue(nameInput, 'New Scenario')
    })

    expect(nameInput.value).toBe('New Scenario')
    expect(container.querySelector('.scenario-name-prompt input')).toBe(nameInput)
  })

  it('starts over without restoring or persisting an in-progress builder draft', async () => {
    await act(async () => {
      root.render(<TestHub testStatuses={{}} onSelectTest={vi.fn()} />)
    })
    await flushPromises()

    window.localStorage.setItem('apexload.scenarioBuilder.draft', JSON.stringify({ form: { name: 'Stale Draft' } }))

    // Enter the builder and make progress; the stale persisted draft is ignored.
    await clickWizardStep('Scripts')
    const nameInput = Array.from(container.querySelectorAll('input')).find(input => input.placeholder === 'Scenario name') as HTMLInputElement | undefined
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    expect(nameInput.value).toBe('')
    await act(async () => {
      setInputValue(nameInput, 'Draft Scenario')
    })
    await flushPromises()

    const startOverButton = () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Start Over')
    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeDefined()

    // Declining the confirmation leaves progress untouched without writing a draft.
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await clickButton('Start Over')
    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeDefined()

    // Confirming discards progress and resets the wizard to a blank Start step.
    confirmSpy.mockReturnValue(true)
    await clickButton('Start Over')
    await flushPromises()

    expect(window.localStorage.getItem('apexload.scenarioBuilder.draft')).toBeNull()
    expect(startOverButton()).toBeUndefined()
    expect(wizardStepButton('Start')?.classList.contains('active')).toBe(true)
    expect(wizardStepComplete('Start')).toBe(false)

    confirmSpy.mockRestore()
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function flushTimeouts() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event(input.type === 'number' ? 'change' : 'input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const prototype = Object.getPrototypeOf(select) as HTMLSelectElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function findControlByLabel<T extends HTMLElement>(container: HTMLElement, label: string, selector: string): T | undefined {
  const field = Array.from(container.querySelectorAll('label')).find(item => (
    item.querySelector('span')?.textContent === label
  ))
  return field?.querySelector(selector) as T | undefined
}

function getDurationFieldByLabel(container: HTMLElement, label: string) {
  const field = Array.from(container.querySelectorAll('.duration-field')).find(item => (
    item.querySelector('.duration-field-label')?.textContent === label
  ))
  if (!field) return null
  return {
    input: field.querySelector('input') as HTMLInputElement,
    select: field.querySelector('select') as HTMLSelectElement,
  }
}

function scenarioFixture(id: string, name: string) {
  return {
    id,
    name,
    duration: '5m',
    assignment: { mode: 'auto' },
    scripts: [],
    data_pools: [],
    associated_releases: [],
    result_count: 0,
    created_at: '2026-05-12T00:00:00Z',
    updated_at: '2026-05-12T00:00:00Z',
    last_run_at: '',
    last_run_ended_at: '',
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}
