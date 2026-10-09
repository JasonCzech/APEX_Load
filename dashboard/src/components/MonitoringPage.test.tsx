// @vitest-environment jsdom

import { request } from '../hooks/apiClient'
import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import { NavigationGuardRunnerContext } from '../hooks/NavigationGuardProvider'
import MonitoringPage, { buildRunCalendarModel, mergeDashboardService } from './MonitoringPage'
import type { RunCalendarEvent, SystemReadinessResponse, SystemServicesResponse } from '../types'

vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: vi.fn() }))

const projectMockState = vi.hoisted(() => ({ activeProjectId: 'project-1' }))

const apiMock = {
  getAPMIntegrationProfiles: vi.fn(),
  getHealth: vi.fn(),
  getInfrastructureMonitorPacks: vi.fn(),
  getFleetAlerts: vi.fn(),
  ackFleetAlert: vi.fn(),
  getFleetCapacity: vi.fn(),
  getFleetProvisioner: vi.fn(),
  listAuthAuditEvents: vi.fn(),
  getRestartCapabilities: vi.fn(),
  getRunCalendar: vi.fn(),
  getScheduleHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getSystemReadiness: vi.fn(),
  getSystemServices: vi.fn(),
  executeRestart: vi.fn(),
  listTests: vi.fn(),
}

const capacityApiMock = {
  listTimeslots: vi.fn(),
  getTimeslot: vi.fn(),
  createTimeslot: vi.fn(),
  updateTimeslot: vi.fn(),
  deleteTimeslot: vi.fn(),
  checkTimeslotAvailability: vi.fn(),
  approveTimeslot: vi.fn(),
  rejectTimeslot: vi.fn(),
  bindTimeslot: vi.fn(),
}

const fleetApiMock = {
  listFleetPools: vi.fn(),
  listGeneratorMetadata: vi.fn(),
  getProvisioner: vi.fn(),
  listProvisionRequests: vi.fn(),
  createProvisionRequest: vi.fn(),
  cancelProvisionRequest: vi.fn(),
  retryProvisionRequest: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useCapacityApi', () => ({
  useCapacityApi: () => capacityApiMock,
}))

vi.mock('../hooks/api/useFleetApi', () => ({
  useFleetApi: () => fleetApiMock,
}))

const clustersApiMock = vi.hoisted(() => ({ listClusters: vi.fn() }))
vi.mock('../hooks/api/useClustersApi', () => ({
  useClustersApi: () => clustersApiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: projectMockState.activeProjectId, name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('MonitoringPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
    vi.mocked(request).mockResolvedValue({ graphs: [], enabled: false, configured: false, incidents: [], limit: 50, offset: 0, global_policy_count: 0, last_evaluated_at: null, generated_at: new Date().toISOString(), active_count: 0, unacknowledged_count: 0, danger_count: 0 })
    projectMockState.activeProjectId = 'project-1'
    apiMock.getHealth.mockResolvedValue({
      status: 'ok',
      version: '1.2.3',
      display_version: 'v1.2.3',
      build_time: '2026-06-04T14:00:00Z',
      ws_clients: 2,
      test_count: 1,
    })
    apiMock.getFleetProvisioner.mockResolvedValue(null)
    apiMock.getRestartCapabilities.mockResolvedValue({
      enabled: true,
      execution_mode: 'distributed',
      namespace: 'apex',
      controller_deployment: 'apexload-controller',
      generator_stateful_set: 'apexload-generator',
      allow_force: true,
      supports: {
        controller_workload: true,
        generator_workload: true,
        generator_pod: true,
      },
    })
    apiMock.executeRestart.mockResolvedValue({ status: 'success', message: 'restart requested' })
    apiMock.getAPMIntegrationProfiles.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      configured_count: 1,
      generic_correlation: {
        propagation: 'W3C trace-context',
        correlation_fields: ['traceparent', 'trace_id', 'span_id', 'correlation_id'],
      },
      profiles: [
        {
          id: 'opentelemetry',
          name: 'OpenTelemetry',
          vendor: 'OpenTelemetry',
          status: 'supported',
          correlation: 'W3C trace-context',
          required_fields: ['traceparent', 'trace_id', 'span_id'],
          evidence: ['/metrics'],
          setup_actions: ['Enable trace propagation'],
          limitations: [],
        },
        {
          id: 'dynatrace',
          name: 'Dynatrace',
          vendor: 'Dynatrace',
          status: 'available',
          correlation: 'W3C trace-context',
          required_fields: ['trace_id'],
          evidence: [],
          setup_actions: ['Configure Dynatrace exporter'],
          limitations: ['Requires external tenant configuration'],
        },
      ],
    })
    apiMock.getInfrastructureMonitorPacks.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      packs: [
        {
          id: 'kubernetes',
          name: 'Kubernetes',
          status: 'supported',
          source: 'prometheus',
          resource_type: 'cluster',
          signals: ['pod saturation', 'active slots'],
          evidence: ['/api/v1/fleet/capacity'],
          setup_actions: ['Enable Kubernetes metrics'],
          limitations: [],
        },
        {
          id: 'jvm',
          name: 'JVM',
          status: 'integration_contract',
          source: 'prometheus',
          resource_type: 'runtime',
          signals: ['heap used', 'gc pause'],
          evidence: [],
          setup_actions: ['Attach JVM exporter'],
          limitations: ['Requires target-side instrumentation'],
        },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'controller-1',
        status: 'healthy',
        address: ':8080',
        version: 'dev',
        execution_mode: 'distributed',
      },
      generators: [],
    })
    apiMock.getSystemReadiness.mockResolvedValue({
      status: 'degraded',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'ready',
          message: 'Kubernetes API and pod-list permission are available',
          details: { enabled: true, configured: true, namespace: 'apex', pod_count: 2, probe_latency_ms: 4.2 },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'postgres', probe_latency_ms: 1.4 } },
        { name: 'cache_backend', status: 'ready', message: 'redis cache backend is reachable', details: { cache_backend: 'redis', probe_latency_ms: 0.8 } },
        { name: 'archive_writer', status: 'ready', message: 'archive root is writable' },
        { name: 'generator_rpc', status: 'degraded', message: 'some generator RPC targets are not reachable' },
        { name: 'telemetry_fanout', status: 'ready', message: 'telemetry fan-out is initialized' },
        { name: 'auth_mode', status: 'degraded', message: 'development auth bypass is enabled' },
        { name: 'scheduler_state', status: 'ready', message: 'scheduler state is initialized' },
      ],
    })
    apiMock.getSystemServices.mockResolvedValue({
      generated_at: '2026-05-18T12:05:00Z',
      services: [
        {
          id: 'controller-api',
          name: 'Controller API',
          kind: 'backend',
          status: 'running',
          address: ':8080',
          url: 'http://127.0.0.1:8080',
          version: 'dev',
          source: 'controller',
        },
        {
          id: 'recorder-proxy',
          name: 'Recorder Proxy',
          kind: 'backend',
          status: 'idle',
          address: ':8081',
          url: 'http://127.0.0.1:8081',
          source: 'recorder',
        },
      ],
    })
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 0,
      enabled: 0,
      due: 0,
      blocked: 0,
      failed: 0,
      pending_approval: 0,
      status_counts: {},
    })
    apiMock.getRunCalendar.mockResolvedValue({
      generated_at: new Date().toISOString(),
      range: {
        from: new Date().toISOString(),
        to: new Date().toISOString(),
        requested_from: new Date().toISOString(),
        requested_to: new Date().toISOString(),
        future_ceiling: new Date(Date.now() + 366 * 24 * 60 * 60 * 1000).toISOString(),
      },
      events: [],
      count: 0,
      status_counts: {},
      source_counts: {},
    })
    apiMock.getFleetAlerts.mockResolvedValue({ alerts: [], count: 0, unacknowledged_count: 0 })
    apiMock.listAuthAuditEvents.mockResolvedValue({ events: [], count: 0 })
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    fleetApiMock.getProvisioner.mockResolvedValue(null)
    fleetApiMock.listProvisionRequests.mockResolvedValue({ provisioning_requests: [], count: 0 })
    apiMock.getFleetCapacity.mockResolvedValue({
      project_id: 'project-1',
      total_generators: 0,
      eligible_generators: 0,
      active_slots: 0,
      available_slots: 0,
      reserved_slots: 0,
      max_slots: 0,
      compatible_features: [],
      rejection_reasons: [],
    })
    apiMock.listTests.mockResolvedValue({ tests: [] })
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

  const clickMonitorTab = async (label: string) => {
    const tab = Array.from(container.querySelectorAll<HTMLButtonElement>('button[role="tab"]'))
      .find(button => button.textContent?.startsWith(label))
    expect(tab).not.toBeUndefined()
    await act(async () => {
      tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  const visibleMonitorCardIds = () => Array.from(container.querySelectorAll<HTMLElement>('.monitoring-grid .monitor-card'))
    .map(card => card.dataset.cardId)

  async function flushAsyncWork() {
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 0))
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  function infrastructureReadinessRow(label: string): HTMLElement | undefined {
    return Array.from(container.querySelectorAll<HTMLElement>('.monitor-infrastructure-row'))
      .find(row => row.querySelector('.monitor-infrastructure-name')?.textContent === label)
  }

  function readyReadinessFixture(): SystemReadinessResponse {
    return {
      status: 'ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'ready',
          message: 'Kubernetes API and pod-list permission are available',
          details: { enabled: true, configured: true, namespace: 'apex' },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'postgres' } },
        { name: 'cache_backend', status: 'ready', message: 'redis cache backend is reachable', details: { cache_backend: 'redis' } },
      ],
    }
  }

  function TopbarContributionHarness() {
    const contribution = useActiveTopbarContribution()
    return (
      <div data-testid="topbar-contribution">
        {contribution?.titleNode || <span>{contribution?.title}</span>}
        {contribution?.controls}
        {contribution?.meta?.map(chip => (
          <span key={chip.id} className={chip.tone}>{chip.label}</span>
        ))}
      </div>
    )
  }

  it('shows configured capacity on a directly opened Fleet tab without live generator inventory', async () => {
    apiMock.getHealth.mockResolvedValue({
      status: 'ok', version: '1.2.3', ws_clients: 2, test_count: 1,
      capacity_availability: { configured_available_generators: 12, effective_available_generators: 12, reserved_generators: 0, shared_available_generators: 0, source: 'configured' },
    } as Awaited<ReturnType<typeof apiMock.getHealth>>)

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} initialCategoryId="fleet" />)
    })
    await flushAsyncWork()

    expect(apiMock.getHealth).toHaveBeenCalled()
    expect(container.textContent).toContain('12 configured')
    expect(container.textContent).toContain('Configured inventory · no live generator inventory')
  })

  it('shows controller, Vite server, and UX build metadata on the system card', async () => {
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const systemCard = container.querySelector<HTMLElement>('[data-card-id="system-health"]')
    expect(systemCard).not.toBeNull()

    const buildRows = Array.from(systemCard!.querySelectorAll<HTMLElement>('.monitor-build-row'))
    expect(buildRows).toHaveLength(3)
    expect(buildRows.map(row => row.querySelector('.monitor-build-name')?.textContent)).toEqual([
      'Controller',
      'Vite Server',
      'UX',
    ])

    const controllerRow = buildRows[0]
    expect(controllerRow.textContent).toContain('v1.2.3')
    expect(controllerRow.textContent).toContain('Built')
    expect(controllerRow.textContent).toContain('2026')
    expect(controllerRow.querySelector('.monitor-build-date')?.textContent).toContain(':')
    expect(systemCard!.querySelector('details.monitor-build-details')).toBeNull()
    expect(systemCard!.querySelector('.monitor-build-heading')?.textContent).toBe('Versions and build details')
    expect(buildRows[1].textContent).toMatch(/v\d+\.\d+\.\d+/)
    expect(buildRows[1].textContent).not.toContain('Built -')
    expect(buildRows[2].textContent).toContain('v0.1.0')
    expect(buildRows[2].textContent).not.toContain('Built -')
  })

  it('renders consolidated control plane diagnostics and admin restart action', async () => {
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    const controlPlaneCard = container.querySelector<HTMLElement>('[data-card-id="system-health"]')
    expect(controlPlaneCard).not.toBeNull()
    expect(controlPlaneCard!.textContent).toContain('Control Plane')
    expect(controlPlaneCard!.textContent).toContain('healthy')
    expect(controlPlaneCard!.textContent).toContain('API')
    expect(controlPlaneCard!.textContent).toContain('ok')
    expect(controlPlaneCard!.textContent).toContain('WebSocket')
    expect(controlPlaneCard!.textContent).toContain('Connected')
    expect(controlPlaneCard!.textContent).toContain('/api/v1/ws')
    expect(controlPlaneCard!.textContent).toContain('Tracked Tests')
    expect(controlPlaneCard!.textContent).toContain('WS Clients')
    expect(controlPlaneCard!.textContent).toContain('Generator Inventory')
    expect(controlPlaneCard!.textContent).toContain('0/0 connected')
    expect(controlPlaneCard!.textContent).toContain('Execution')
    expect(controlPlaneCard!.textContent).toContain('distributed')
    expect(controlPlaneCard!.textContent).toContain('Instance')
    expect(controlPlaneCard!.textContent).toContain('controller-1')
    expect(controlPlaneCard!.textContent).toContain('Address')
    expect(controlPlaneCard!.textContent).toContain(window.location.host)
    expect(controlPlaneCard!.textContent).toContain('Restart Controller')
  })

  it('shows a readable controller instance name and browser-reachable address', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'apexload-controller-0/ab12cd34', status: 'healthy', address: '0.0.0.0:8080', version: 'dev', execution_mode: 'distributed' },
      generators: [],
    })
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const controlPlaneCard = container.querySelector<HTMLElement>('[data-card-id="system-health"]')
    expect(controlPlaneCard?.querySelector('.monitor-row:nth-of-type(8) .monitor-value')?.textContent).toBe('apexload-controller-0')
    expect(controlPlaneCard?.querySelector('.monitor-row:nth-of-type(9) .monitor-value')?.textContent).toBe(window.location.host)
    expect(controlPlaneCard?.textContent).not.toContain('0.0.0.0:8080')
  })

  it('does not load or show restart controls for non-admin monitoring sessions', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'healthy', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [generatorInventoryFixture()],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(apiMock.getRestartCapabilities).not.toHaveBeenCalled()

    await clickMonitorTab('Fleet')
    await openGeneratorInventoryModal()

    expect(container.textContent).not.toContain('Restart Pod')
    expect(container.textContent).not.toContain('Restart Generator Fleet')
  })

  it('hides restart controls when restart capability is disabled', async () => {
    apiMock.getRestartCapabilities.mockResolvedValue({
      enabled: false,
      disabled_reason: 'Kubernetes restart controls are disabled.',
      execution_mode: 'distributed',
      allow_force: false,
      supports: {
        controller_workload: false,
        generator_workload: false,
        generator_pod: false,
      },
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'healthy', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [generatorInventoryFixture()],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    expect(apiMock.getRestartCapabilities).toHaveBeenCalled()

    await clickMonitorTab('Fleet')
    await openGeneratorInventoryModal()

    expect(container.textContent).toContain('Kubernetes restart controls are disabled.')
    expect(container.textContent).not.toContain('Restart Pod')
    expect(container.textContent).not.toContain('Restart Generator Fleet')
  })

  it('submits a generator pod restart after reason and target confirmation', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'healthy', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [generatorInventoryFixture()],
    })
    apiMock.executeRestart.mockResolvedValueOnce({ status: 'success', message: 'generator pod restart requested' })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    await clickMonitorTab('Fleet')
    await openGeneratorInventoryModal()
    await clickButtonContaining('Restart Pod')

    expect(container.textContent).toContain('Restart Generator Pod')
    const modal = container.querySelector<HTMLElement>('.destructive-confirm-modal')
    expect(modal).not.toBeNull()
    const submit = Array.from(modal!.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Restart Pod'))
    expect(submit).not.toBeUndefined()
    expect(submit!.disabled).toBe(true)

    await changeInputByPlaceholder('Operational reason', 'stuck worker')
    await changeInputByPlaceholder('apexload-generator-0', 'apexload-generator-0')
    expect(submit!.disabled).toBe(false)

    await act(async () => {
      submit!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.executeRestart).toHaveBeenCalledWith({
      target_type: 'generator',
      scope: 'pod',
      target: 'apexload-generator-0',
      reason: 'stuck worker',
      confirmation: 'apexload-generator-0',
      force: false,
    })
    expect(container.textContent).toContain('generator pod restart requested')
  })

  it('shows blocked restart errors and allows force re-confirmation', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'healthy', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [generatorInventoryFixture({ active_vusers: 12 })],
    })
    apiMock.executeRestart
      .mockRejectedValueOnce({ message: 'Conflict', payload: { message: 'generator workload has active VUsers or active runs; force is required' } })
      .mockResolvedValueOnce({ status: 'success', message: 'generator workload restart requested' })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    await clickMonitorTab('Fleet')
    await openGeneratorInventoryModal()
    await clickButtonContaining('Restart Generator Fleet')

    expect(container.textContent).toContain('Allows the rollout even when generators have active VUsers or runs are active.')
    await changeInputByPlaceholder('Operational reason', 'roll stuck fleet')
    await changeInputByPlaceholder('apexload-generator', 'apexload-generator')
    await submitDestructiveModal('Restart Fleet')

    expect(container.textContent).toContain('force is required')

    const checkbox = container.querySelector<HTMLInputElement>('.destructive-confirm-modal input[type="checkbox"]')
    expect(checkbox).not.toBeNull()
    await act(async () => {
      checkbox!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await submitDestructiveModal('Restart Fleet')

    expect(apiMock.executeRestart).toHaveBeenLastCalledWith(expect.objectContaining({ force: true }))
    expect(container.textContent).toContain('generator workload restart requested')
  })

  it('publishes advisory warnings after the floating Monitoring section toggle', async () => {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />
        </TopbarContributionProvider>,
      )
    })

    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    expect(topbar?.textContent).toContain('Monitoring')
    expect(topbar?.querySelector('[aria-label="Monitor categories"]')?.classList.contains('topbar-page-section-toggle')).toBe(true)
    // Analysis is no longer a tab; its workspace appears on Overview when enabled.
    expect(Array.from(topbar?.querySelectorAll('[aria-label="Monitor categories"] [role="tab"]') || []).map(tab => tab.textContent?.trim())).toEqual(['Overview', 'Fleet', 'Telemetry', 'Alerts', 'Schedules'])
    expect(topbar?.textContent).toContain('1 advisory warning')
    expect(topbar?.querySelector('.monitoring-topbar-controls .warning')?.textContent).toBe('1 advisory warning')
    const topbarText = topbar?.textContent ?? ''
    expect(topbarText.indexOf('Overview')).toBeGreaterThan(topbarText.indexOf('Monitoring'))
    expect(topbarText.indexOf('1 advisory warning')).toBeGreaterThan(topbarText.indexOf('Schedules'))
    expect(container.querySelector('.page-header')?.textContent).not.toContain('Updated')
    expect(container.querySelector('.monitor-update-footer .monitor-last-refreshed')?.textContent).toContain('Updated')
    expect(container.textContent).not.toContain('Advisory Panel')
    expect(container.textContent).not.toContain('Customize Cards')
  })

  async function openGeneratorInventoryModal() {
    await clickButtonContaining('View details')
  }

  async function clickButtonContaining(label: string) {
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(button).not.toBeUndefined()
    await act(async () => {
      button!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  async function changeInputByPlaceholder(placeholder: string, value: string) {
    const input = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(candidate => candidate.placeholder === placeholder)
    expect(input).not.toBeUndefined()
    await act(async () => {
      setInputValue(input!, value)
      input!.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  function setInputValue(input: HTMLInputElement, value: string) {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
  }

  async function submitDestructiveModal(label: string) {
    const modal = container.querySelector<HTMLElement>('.destructive-confirm-modal')
    expect(modal).not.toBeNull()
    const button = Array.from(modal!.querySelectorAll<HTMLButtonElement>('button'))
      .find(candidate => candidate.textContent?.includes(label))
    expect(button).not.toBeUndefined()
    await act(async () => {
      button!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function generatorInventoryFixture(overrides: Record<string, unknown> = {}) {
    return {
      target: '10.0.0.1:9090',
      generator_id: 'apexload-generator-0',
      pod_name: 'apexload-generator-0',
      restartable: true,
      status: 'connected',
      available_capacity: 9800,
      active_vusers: 0,
      cpu_percent: 12.3,
      memory_bytes: 256 * 1024 * 1024,
      goroutine_count: 512,
      cpu_limit_cores: 2,
      memory_limit_bytes: 2 * 1024 * 1024 * 1024,
      max_vusers: 10000,
      ...overrides,
    }
  }

  it('renders sampled telemetry diagnostics in the monitoring grid', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          telemetryDiagnostics={[
            {
              test_id: 'test-1',
              test_name: 'Checkout',
              timestamp: '2026-05-12T20:05:01Z',
              diagnostic: {
                severity: 'danger',
                summary: 'collector dropped 4 metric samples',
                conditions: ['collector_drops'],
                samples: [
                  {
                    issue: 'error',
                    transaction: 'Checkout',
                    dependency_target: 'payments.example.test',
                    errors: 4,
                    count: 10,
                    avg_ms: 640,
                    p99_ms: 920,
                  },
                ],
              },
            },
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('Overview')
    expect(container.textContent).toContain('Fleet')
    expect(container.textContent).toContain('Telemetry')
    expect(container.textContent).toContain('Schedules')
    expect(container.textContent).toContain('Control Plane')
    expect(container.textContent).toContain('Readiness & Services')
    expect(container.textContent).toContain('Ready Checks')
    expect(container.textContent).toContain('6/8')
    const advisoryBadge = container.querySelector('.monitor-title-advisory')
    expect(advisoryBadge).toBeTruthy()
    expect(advisoryBadge?.className).toContain('danger')
    expect(advisoryBadge?.textContent).toContain('2 issues need attention')
    expect(container.querySelector('.monitor-health-banner')).toBeNull()
    expect(container.textContent).not.toContain('Advisory Panel')
    expect(container.textContent).not.toContain('Customize Cards')
    expect(container.textContent).toContain('Generator RPC: some generator RPC targets are not reachable')
    expect(container.textContent).toContain('Auth Mode: development auth bypass is enabled')
    expect(container.textContent).toContain('Generator Inventory')
    expect(container.textContent).toContain('0/0 connected')
    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])

    await clickMonitorTab('Telemetry')

    expect(visibleMonitorCardIds()).toEqual(['run-monitor-sources', 'telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors', 'elk-connections', 'dynatrace-connections'])
    expect(container.textContent).toContain('Telemetry Diagnostics')
    expect(container.textContent).toContain('danger')
    expect(container.textContent).toContain('1 danger')
    expect(container.textContent).toContain('Checkout: collector dropped 4 metric samples')
    expect(container.textContent).toContain('View details')

    const telemetryButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View details'))
    expect(telemetryButton).not.toBeUndefined()

    await act(async () => {
      telemetryButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('collector_drops')
    expect(container.textContent).toContain('error')
    expect(container.textContent).toContain('4/10 errors')
    expect(container.textContent).toContain('payments.example.test')
    expect(container.textContent).toContain('Close')
    expect(apiMock.getRunCalendar).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'project-1' }))
    expect(apiMock.getFleetAlerts).toHaveBeenCalled()
    expect(apiMock.getFleetCapacity).toHaveBeenCalledWith({ project_id: 'project-1' })
    expect(apiMock.getSystemReadiness).toHaveBeenCalled()
    expect(apiMock.getSystemServices).toHaveBeenCalled()

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Close')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await clickMonitorTab('Fleet')

    // Normalized cloud identities and execution targets precede the legacy
    // cluster registry, pools, and host allocation workflow.
    expect(visibleMonitorCardIds()).toEqual(['capacity', 'generators', 'fleet-alerts', 'fleet-audit'])
    expect(container.textContent).toContain('Generators')
    expect(container.textContent).toContain('Fleet Alerts')
    expect(container.textContent).toContain('No generator inventory was returned by the controller.')
  })

  it('supports a controlled category so monitor tabs remain synchronized with the route', async () => {
    const onCategoryChange = vi.fn()
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          activeCategoryId="overview"
          onCategoryChange={onCategoryChange}
        />,
      )
    })

    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])
    await clickMonitorTab('Telemetry')
    expect(onCategoryChange).toHaveBeenCalledWith('telemetry')
    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])

    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          activeCategoryId="telemetry"
          onCategoryChange={onCategoryChange}
        />,
      )
    })
    expect(visibleMonitorCardIds()).toEqual(['run-monitor-sources', 'telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors', 'elk-connections', 'dynatrace-connections'])
    expect(container.querySelector('.monitor-page-title')?.textContent).toContain('Telemetry')
  })

  it('runs category changes through the global navigation guard', async () => {
    let pending: (() => void) | null = null
    await act(async () => {
      root.render(
        <NavigationGuardRunnerContext.Provider value={{
          hasDirtyGuard: () => true,
          markClean: () => undefined,
          runGuarded: proceed => { pending = proceed },
        }}>
          <MonitoringPage connectionState="connected" />
        </NavigationGuardRunnerContext.Provider>,
      )
    })

    await clickMonitorTab('Telemetry')
    expect(pending).not.toBeNull()
    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])
    await act(async () => (pending as unknown as () => void)())
    expect(visibleMonitorCardIds()).toEqual(['run-monitor-sources', 'telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors', 'elk-connections', 'dynatrace-connections'])
  })

  it('summarizes the worst telemetry condition the header counts, not the newest sample', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          initialCategoryId="telemetry"
          telemetryDiagnostics={[
            { test_id: 'run-1', test_name: 'Checkout', timestamp: '2026-05-12T20:01:00Z', diagnostic: { severity: 'healthy', summary: 'live telemetry healthy', conditions: [], samples: [] } },
            { test_id: 'run-1', test_name: 'Checkout', timestamp: '2026-05-12T20:00:00Z', diagnostic: { severity: 'danger', summary: 'Publisher dropped samples', conditions: ['dropped_samples'], samples: [] } },
          ]}
        />,
      )
    })
    const summary = container.querySelector<HTMLElement>('.telemetry-summary-details')
    expect(summary?.textContent).toContain('Latest danger')
    expect(summary?.textContent).toContain('Publisher dropped samples')
    expect(summary?.textContent).not.toContain('live telemetry healthy')
  })

  it('traps focus in telemetry details, closes on Escape, and restores the trigger', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          initialCategoryId="telemetry"
          telemetryDiagnostics={[{
            test_id: 'run-1',
            test_name: 'Checkout',
            timestamp: '2026-05-12T20:00:00Z',
            diagnostic: { severity: 'warning', summary: 'Collector delay', conditions: ['collector_delay'], samples: [] },
          }]}
        />,
      )
    })

    const trigger = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('View details'))
    expect(trigger).toBeDefined()
    await act(async () => {
      trigger?.focus()
      trigger?.click()
    })
    await flushAsyncWork()

    const dialog = container.querySelector<HTMLElement>('[aria-labelledby="telemetry-diagnostics-title"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.contains(document.activeElement)).toBe(true)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    await flushAsyncWork()
    expect(container.querySelector('[aria-labelledby="telemetry-diagnostics-title"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('preserves last-known-good values when a partial refresh fails', async () => {
    apiMock.getFleetCapacity.mockResolvedValue({
      project_id: 'project-1',
      total_generators: 1,
      eligible_generators: 1,
      active_slots: 1,
      available_slots: 7,
      reserved_slots: 0,
      max_slots: 8,
      compatible_features: [],
      rejection_reasons: [],
    })
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" />)
    })
    await flushAsyncWork()
    expect(container.textContent).toContain('7 available')

    apiMock.getFleetCapacity.mockRejectedValue(new Error('capacity temporarily unavailable'))
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'))
    })
    await flushAsyncWork()

    expect(container.textContent).toContain('7 available')
    const capacityCard = container.querySelector<HTMLElement>('[data-card-id="capacity"]')
    expect(capacityCard?.classList.contains('stale')).toBe(true)
    expect(capacityCard?.textContent).toContain('Stale')
    expect(container.querySelector('.monitor-last-refreshed')?.textContent).toContain('stale card')
  })

  it('does not report fleet alerts as clear when the alert feed is unavailable', async () => {
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'controller-1', status: 'healthy', address: ':8080', version: 'dev', execution_mode: 'distributed' },
      generators: [{
        target: 'generator-a:9090',
        generator_id: 'generator-a',
        status: 'connected',
        available_capacity: 8,
        active_vusers: 0,
        cpu_percent: 10,
        memory_bytes: 1024,
        goroutine_count: 12,
        cpu_limit_cores: 2,
        memory_limit_bytes: 2048,
        max_vusers: 8,
      }],
    })
    apiMock.getFleetAlerts.mockRejectedValue(new Error('fleet alerts unavailable'))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" />)
    })
    await flushAsyncWork()

    const alertCard = container.querySelector<HTMLElement>('[data-card-id="fleet-alerts"]')
    expect(alertCard).not.toBeNull()
    expect(alertCard?.textContent).toContain('Fleet alert status is unavailable')
    expect(alertCard?.textContent).not.toContain('No fleet placement alerts detected')
  })

  it('retries every initial category when a hidden mount becomes visible', async () => {
    let visibility: DocumentVisibilityState = 'hidden'
    const visibilitySpy = vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)

    try {
      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" />)
      })
      await flushAsyncWork()

      expect(apiMock.getHealth).not.toHaveBeenCalled()
      expect(apiMock.getFleetAlerts).not.toHaveBeenCalled()
      expect(apiMock.getAPMIntegrationProfiles).not.toHaveBeenCalled()
      expect(apiMock.getScheduleHealth).not.toHaveBeenCalled()

      visibility = 'visible'
      await act(async () => {
        document.dispatchEvent(new Event('visibilitychange'))
      })
      await flushAsyncWork()
      await flushAsyncWork()

      expect(apiMock.getHealth).toHaveBeenCalledTimes(1)
      expect(apiMock.getFleetAlerts).toHaveBeenCalledTimes(1)
      expect(apiMock.getAPMIntegrationProfiles).toHaveBeenCalledTimes(1)
      expect(apiMock.getScheduleHealth).toHaveBeenCalledTimes(1)
    } finally {
      visibilitySpy.mockRestore()
    }
  })

  it('coalesces slow polling instead of starting overlapping refreshes', async () => {
    vi.useFakeTimers()
    let resolveHealth: ((value: Awaited<ReturnType<typeof apiMock.getHealth>>) => void) | undefined
    const slowHealth = new Promise<Awaited<ReturnType<typeof apiMock.getHealth>>>(resolve => {
      resolveHealth = resolve
    })
    apiMock.getHealth.mockReturnValue(slowHealth)

    try {
      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" />)
        await Promise.resolve()
      })

      await act(async () => {
        vi.advanceTimersByTime(12_000)
        await Promise.resolve()
      })

      expect(apiMock.getHealth).toHaveBeenCalledTimes(1)

      await act(async () => {
        resolveHealth?.({
          status: 'ok',
          version: '1.2.3',
          display_version: 'v1.2.3',
          build_time: '2026-06-04T14:00:00Z',
          ws_clients: 2,
          test_count: 1,
        })
        await Promise.resolve()
        await Promise.resolve()
      })

      expect(apiMock.getHealth.mock.calls.length).toBeLessThanOrEqual(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('clears project-scoped monitor data before loading a new project', async () => {
    apiMock.getFleetCapacity.mockResolvedValue({
      project_id: 'project-1',
      total_generators: 1,
      eligible_generators: 1,
      active_slots: 1,
      available_slots: 17,
      reserved_slots: 0,
      max_slots: 18,
      compatible_features: [],
      rejection_reasons: [],
    })
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" />)
    })
    await flushAsyncWork()
    expect(container.textContent).toContain('17 available')

    apiMock.getFleetCapacity.mockRejectedValue(new Error('project-2 capacity pending'))
    projectMockState.activeProjectId = 'project-2'
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" />)
    })
    await flushAsyncWork()

    expect(apiMock.getFleetCapacity).toHaveBeenCalledWith({ project_id: 'project-2' })
    expect(container.textContent).not.toContain('17 available')
  })

  it('shows saved clusters and pools on Fleet until they report generators', async () => {
    apiMock.getSystemInventory.mockResolvedValue({ execution_mode: 'local', generators: [], controller: { status: 'ok' } } as never)
    apiMock.getFleetAlerts.mockResolvedValue({ alerts: [], count: 0, unacknowledged_count: 0 })
    apiMock.listAuthAuditEvents.mockResolvedValue({ events: [], count: 0 })
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    fleetApiMock.getProvisioner.mockResolvedValue(null)
    fleetApiMock.listProvisionRequests.mockResolvedValue({ provisioning_requests: [], count: 0 })
    apiMock.getFleetCapacity.mockResolvedValue({ project_id: 'project-1', total_generators: 0, eligible_generators: 0, available_slots: 0, max_slots: 0, compatible_features: [], rejection_reasons: [] })
    clustersApiMock.listClusters.mockResolvedValue({ clusters: [{ id: 'c1', name: 'uxr cluster', kind: 'kubeconfig', last_check_status: '' }], total: 1 })
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [{ id: 'p1', name: 'uxr pool', cloud_provider: 'kubernetes', status: 'active' }], count: 1 })
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" canManageSystem />)
    })
    await flushAsyncWork()
    const configured = container.querySelector('[aria-label="Configured clusters and pools"]')
    expect(configured?.textContent).toContain('uxr cluster')
    expect(configured?.textContent).toContain('not tested yet')
    expect(configured?.textContent).toContain('uxr pool')
    expect(Array.from(container.querySelectorAll('a')).some(link => link.textContent === 'Configure distributed generator targets')).toBe(true)
  })

  it('filters and pages a large fleet alert feed, with a distinct name for every acknowledge button', async () => {
    const alerts = Array.from({ length: 30 }, (_, index) => ({ id: `a${index}`, type: 'generator_offline', target: `gen-${index}`, severity: index < 5 ? 'danger' : 'warning', summary: `Generator gen-${index} offline`, acknowledged: false }))
    apiMock.getSystemInventory.mockResolvedValue({ execution_mode: 'local', generators: [], controller: { status: 'ok' } } as never)
    apiMock.getFleetAlerts.mockResolvedValue({ alerts, count: alerts.length, unacknowledged_count: alerts.length } as never)
    apiMock.listAuthAuditEvents.mockResolvedValue({ events: [], count: 0 })
    capacityApiMock.listTimeslots.mockResolvedValue({ timeslots: [], count: 0 })
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [], count: 0 })
    fleetApiMock.listGeneratorMetadata.mockResolvedValue({ generators: [], count: 0 })
    fleetApiMock.getProvisioner.mockResolvedValue(null)
    fleetApiMock.listProvisionRequests.mockResolvedValue({ provisioning_requests: [], count: 0 })
    clustersApiMock.listClusters.mockResolvedValue({ clusters: [], total: 0 })
    apiMock.getFleetCapacity.mockResolvedValue({ project_id: 'project-1', total_generators: 0, eligible_generators: 0, available_slots: 0, max_slots: 0, compatible_features: [], rejection_reasons: [] })
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" canManageSystem />)
    })
    await flushAsyncWork()
    const ackButtons = () => Array.from(container.querySelectorAll('button')).filter(button => button.textContent === 'Ack')
    expect(ackButtons()).toHaveLength(25)
    expect(new Set(ackButtons().map(button => button.getAttribute('aria-label'))).size).toBe(25)
    expect(container.textContent).toContain('Showing 25 of 30 matching')
    const severity = container.querySelector('[aria-label="Filter fleet alerts"] select') as HTMLSelectElement
    await act(async () => { severity.value = 'danger'; severity.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(ackButtons()).toHaveLength(5)
  })

  it('does not report a refresh timestamp when every requested endpoint fails', async () => {
    for (const request of [
      apiMock.getHealth,
      apiMock.getSystemInventory,
      apiMock.getSystemReadiness,
      apiMock.getSystemServices,
      apiMock.getScheduleHealth,
      apiMock.getRunCalendar,
      apiMock.getAPMIntegrationProfiles,
      apiMock.getInfrastructureMonitorPacks,
      apiMock.getFleetAlerts,
      apiMock.getFleetCapacity,
      apiMock.getFleetProvisioner,
    ]) {
      request.mockRejectedValue(new Error('offline'))
    }

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" />)
    })
    await flushAsyncWork()

    expect(container.querySelector('.monitor-last-refreshed')).toBeNull()
  })

  it('always renders provider-specific infrastructure readiness rows', async () => {
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness).not.toBeNull()
    expect(readiness!.tagName).toBe('UL')
    expect(Array.from(readiness!.children).every(row => row.tagName === 'LI')).toBe(true)
    expect(readiness!.children).toHaveLength(3)
    expect(readiness!.textContent).toContain('Kubernetes API')
    expect(readiness!.textContent).toContain('Kubernetes API and pod-list permission are available · 4.2 ms')
    expect(readiness!.textContent).toContain('PostgreSQL Metadata Store')
    expect(readiness!.textContent).toContain('metadata store is reachable · 1.4 ms')
    expect(readiness!.textContent).toContain('Redis Cache')
    expect(readiness!.textContent).toContain('redis cache backend is reachable · 0.8 ms')
  })

  it('shows checking only while the first readiness request is unresolved', async () => {
    apiMock.getSystemReadiness.mockReturnValueOnce(new Promise(() => undefined))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.querySelectorAll('.monitor-infrastructure-status-checking')).toHaveLength(3)
    expect(readiness!.textContent).toContain('Waiting for readiness data.')
    expect(container.textContent).toContain('Waiting for /api/v1/system/readiness.')
    expect(container.textContent).toContain('Waiting for system data…')
    expect(container.textContent).not.toContain('Readiness data incomplete')
  })

  it('shows neutral and degraded Kubernetes readiness states without hiding data-platform health', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'degraded',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'degraded',
          message: 'Kubernetes API or pod-list permission is unavailable',
          details: { enabled: true, configured: false, reason: 'probe_failed' },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'sqlite' } },
        { name: 'cache_backend', status: 'ready', message: 'in-memory cache backend is ready', details: { cache_backend: 'memory' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.textContent).toContain('Kubernetes API')
    expect(readiness!.textContent).toContain('Degraded')
    expect(readiness!.textContent).toContain('Kubernetes API or pod-list permission is unavailable')
    expect(readiness!.textContent).toContain('SQLite Metadata Store')
    expect(readiness!.textContent).toContain('In-memory Cache')
  })

  it('shows checks omitted by an older controller as not reported after loading', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'sqlite' } },
        { name: 'cache_backend', status: 'ready', message: 'in-memory cache backend is ready', details: { cache_backend: 'memory' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.textContent).toContain('Kubernetes APINot reported by this controller.Not Reported')
    expect(readiness!.textContent).not.toContain('Waiting for readiness data.')
    const readinessCardBadge = container.querySelector('[data-card-id="production-readiness"] .monitor-header-badge')
    expect(readinessCardBadge?.classList.contains('unknown')).toBe(true)
    expect(readinessCardBadge?.textContent).toContain('1 unreported')
    expect(container.querySelector('[data-card-id="production-readiness"]')?.textContent).toContain('Ready Checks2/3')
    expect(container.textContent).not.toContain('All configured production readiness checks are passing.')
  })

  it('keeps a hard readiness result ahead of mixed-version coverage gaps', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'not_ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        { name: 'metadata_store', status: 'not_ready', message: 'PostgreSQL query failed', details: { store_type: 'postgres' } },
        { name: 'cache_backend', status: 'ready', message: 'redis cache backend is reachable', details: { cache_backend: 'redis' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readinessCard = container.querySelector<HTMLElement>('[data-card-id="production-readiness"]')
    const readinessCardBadge = readinessCard?.querySelector('.monitor-header-badge')
    expect(readinessCardBadge?.classList.contains('danger')).toBe(true)
    expect(readinessCardBadge?.textContent).toContain('Not Ready · 1 unreported')
    expect(readinessCard?.textContent).toContain('PostgreSQL query failed')
    expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Not Ready · 1 unreported.')
  })

  it('does not report an empty readiness response as all passing', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [],
    })

    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />
        </TopbarContributionProvider>,
      )
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.querySelectorAll('.monitor-infrastructure-status-not-reported')).toHaveLength(3)
    const readinessCardBadge = container.querySelector('[data-card-id="production-readiness"] .monitor-header-badge')
    expect(readinessCardBadge?.classList.contains('unknown')).toBe(true)
    expect(readinessCardBadge?.textContent).toContain('3 unreported')
    expect(container.querySelector('[data-card-id="production-readiness"]')?.textContent).toContain('Ready Checks0/3')
    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    expect(topbar?.textContent).toContain('Readiness data incomplete')
    expect(topbar?.textContent).not.toContain('All systems operational')
    expect(container.textContent).not.toContain('All configured production readiness checks are passing.')
  })

  it('shows an initial readiness request failure as unknown instead of checking forever', async () => {
    apiMock.getSystemReadiness.mockRejectedValueOnce(new Error('controller unavailable'))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })
    await flushAsyncWork()

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.querySelectorAll('.monitor-infrastructure-status-unknown')).toHaveLength(3)
    expect(readiness!.textContent).toContain('The readiness endpoint is unavailable.')
    expect(readiness!.textContent).not.toContain('Waiting for readiness data.')
    expect(container.textContent).toContain('Unable to load /api/v1/system/readiness. Retrying automatically.')
    expect(container.textContent).not.toContain('Waiting for /api/v1/system/readiness.')
    expect(container.textContent).toContain('Readiness unavailable')
  })

  it('releases the category sweep after a bounded readiness failure and retries on the next poll', async () => {
    vi.useFakeTimers()
    apiMock.getSystemReadiness
      .mockImplementationOnce(() => new Promise((_resolve, reject) => {
        setTimeout(() => reject(new Error('Request timed out after 4000ms')), 4000)
      }))
      .mockResolvedValue(readyReadinessFixture())

    try {
      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" telemetryDiagnostics={[]} />)
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(apiMock.getSystemReadiness).toHaveBeenCalledTimes(1)
      expect(apiMock.getFleetAlerts).not.toHaveBeenCalled()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(4000)
      })
      expect(apiMock.getFleetAlerts).toHaveBeenCalled()
      expect(apiMock.getAPMIntegrationProfiles).toHaveBeenCalled()
      expect(apiMock.getScheduleHealth).toHaveBeenCalled()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1000)
      })
      expect(apiMock.getSystemReadiness.mock.calls.length).toBeGreaterThanOrEqual(2)
      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Ready.')
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps retrying readiness outside Overview and refreshes it immediately on return', async () => {
    vi.useFakeTimers()
    apiMock.getSystemReadiness
      .mockRejectedValueOnce(new Error('controller unavailable'))
      .mockResolvedValue(readyReadinessFixture())

    try {
      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" activeCategoryId="fleet" telemetryDiagnostics={[]} />)
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(apiMock.getSystemReadiness).toHaveBeenCalledTimes(1)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })
      expect(apiMock.getSystemReadiness.mock.calls.length).toBeGreaterThanOrEqual(2)
      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Ready.')

      const degraded = readyReadinessFixture()
      degraded.status = 'degraded'
      degraded.checks[0] = {
        ...degraded.checks[0],
        status: 'degraded',
        message: 'Kubernetes API access is degraded',
      }
      apiMock.getSystemReadiness.mockResolvedValue(degraded)
      const callsBeforeReturn = apiMock.getSystemReadiness.mock.calls.length

      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" activeCategoryId="overview" telemetryDiagnostics={[]} />)
        await vi.advanceTimersByTimeAsync(0)
      })

      expect(apiMock.getSystemReadiness.mock.calls.length).toBeGreaterThan(callsBeforeReturn)
      expect(infrastructureReadinessRow('Kubernetes API')?.textContent).toContain('Kubernetes API access is degraded')
    } finally {
      vi.useRealTimers()
    }
  })

  it('marks last-known readiness stale on Overview and clears it after recovery', async () => {
    vi.useFakeTimers()
    apiMock.getSystemReadiness.mockResolvedValue(readyReadinessFixture())

    try {
      await act(async () => {
        root.render(
          <TopbarContributionProvider>
            <TopbarContributionHarness />
            <MonitoringPage connectionState="connected" activeCategoryId="overview" telemetryDiagnostics={[]} />
          </TopbarContributionProvider>,
        )
        await vi.advanceTimersByTimeAsync(0)
      })

      apiMock.getSystemReadiness.mockRejectedValue(new Error('readiness endpoint unavailable'))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      const readinessCard = container.querySelector<HTMLElement>('[data-card-id="production-readiness"]')
      const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
      const topbar = container.querySelector('[data-testid="topbar-contribution"]')
      expect(readinessCard?.classList.contains('stale')).toBe(true)
      expect(readiness?.textContent).toContain('Stale data; latest readiness refresh failed.')
      expect(readinessCard?.textContent).not.toContain('All configured production readiness checks are passing.')
      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Stale.')
      expect(topbar?.textContent).toContain('1 advisory warning')
      expect(topbar?.querySelector('.monitoring-topbar-advisory')?.getAttribute('title')).toContain('Readiness: stale data')

      apiMock.getSystemReadiness.mockResolvedValue(readyReadinessFixture())
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      expect(readinessCard?.classList.contains('stale')).toBe(false)
      expect(readiness?.textContent).not.toContain('Stale data')
      expect(readinessCard?.textContent).toContain('All configured production readiness checks are passing.')
      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Ready.')
      expect(topbar?.textContent).not.toContain('advisory warning')
    } finally {
      vi.useRealTimers()
    }
  })

  it('surfaces and recovers stale readiness while another Monitoring category is active', async () => {
    vi.useFakeTimers()
    apiMock.getSystemReadiness.mockResolvedValue(readyReadinessFixture())

    try {
      await act(async () => {
        root.render(
          <TopbarContributionProvider>
            <TopbarContributionHarness />
            <MonitoringPage connectionState="connected" activeCategoryId="fleet" telemetryDiagnostics={[]} />
          </TopbarContributionProvider>,
        )
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(container.querySelector('[aria-label="Infrastructure readiness"]')).toBeNull()

      apiMock.getSystemReadiness.mockRejectedValue(new Error('readiness endpoint unavailable'))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      const topbar = container.querySelector('[data-testid="topbar-contribution"]')
      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Stale.')
      expect(topbar?.textContent).toContain('1 advisory warning')
      expect(topbar?.querySelector('.monitoring-topbar-advisory')?.getAttribute('title')).toContain('Readiness: stale data')

      apiMock.getSystemReadiness.mockResolvedValue(readyReadinessFixture())
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Ready.')
      expect(topbar?.textContent).not.toContain('advisory warning')
    } finally {
      vi.useRealTimers()
    }
  })

  it('announces readiness state changes politely and does not rewrite an unchanged summary', async () => {
    vi.useFakeTimers()
    apiMock.getSystemReadiness.mockResolvedValue(readyReadinessFixture())

    try {
      await act(async () => {
        root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
        await vi.advanceTimersByTimeAsync(0)
      })

      const liveSummary = container.querySelector<HTMLElement>('[data-readiness-live-summary]')
      expect(liveSummary).not.toBeNull()
      expect(liveSummary?.getAttribute('role')).toBe('status')
      expect(liveSummary?.getAttribute('aria-live')).toBe('polite')
      expect(liveSummary?.getAttribute('aria-atomic')).toBe('true')
      expect(liveSummary?.textContent).toBe('Readiness Ready. Kubernetes API Ready. Metadata Store Ready. Cache Backend Ready.')
      const originalTextNode = liveSummary?.firstChild

      const sameStates = readyReadinessFixture()
      sameStates.generated_at = '2026-05-18T12:10:00Z'
      sameStates.checks[0] = {
        ...sameStates.checks[0],
        message: 'Kubernetes API is still available',
        details: { ...sameStates.checks[0].details, probe_latency_ms: 9.7 },
      }
      apiMock.getSystemReadiness.mockResolvedValue(sameStates)
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      expect(liveSummary?.firstChild).toBe(originalTextNode)
      expect(liveSummary?.textContent).toBe('Readiness Ready. Kubernetes API Ready. Metadata Store Ready. Cache Backend Ready.')

      const changedState = readyReadinessFixture()
      changedState.status = 'not_ready'
      changedState.checks[2] = { ...changedState.checks[2], status: 'not_ready', message: 'Redis PING failed' }
      apiMock.getSystemReadiness.mockResolvedValue(changedState)
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })

      expect(liveSummary?.textContent).toBe('Readiness Not Ready. Kubernetes API Ready. Metadata Store Ready. Cache Backend Not Ready.')
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps infrastructure failures out of the capped readiness warning list', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'not_ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        { name: 'kubernetes_api', status: 'degraded', message: 'Kubernetes API probe failed', details: { enabled: true } },
        { name: 'metadata_store', status: 'not_ready', message: 'PostgreSQL query failed', details: { store_type: 'postgres' } },
        { name: 'cache_backend', status: 'not_ready', message: 'Redis PING failed', details: { cache_backend: 'redis' } },
        { name: 'generator_rpc', status: 'degraded', message: 'generator RPC is degraded' },
        { name: 'auth_mode', status: 'degraded', message: 'authentication is degraded' },
        { name: 'scheduler_state', status: 'not_ready', message: 'scheduler is blocked' },
        { name: 'archive_writer', status: 'degraded', message: 'archive writer is degraded' },
        { name: 'telemetry_fanout', status: 'degraded', message: 'telemetry fan-out is degraded' },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const warningRows = Array.from(container.querySelectorAll<HTMLElement>('[data-card-id="production-readiness"] .monitor-warning-list > p'))
      .map(row => row.textContent)
    expect(warningRows).toEqual([
      'Generator RPC: generator RPC is degraded',
      'Auth Mode: authentication is degraded',
      'Scheduler State: scheduler is blocked',
      'Archive Writer: archive writer is degraded',
    ])
    expect(warningRows.join(' ')).not.toContain('Kubernetes API probe failed')
    expect(warningRows.join(' ')).not.toContain('PostgreSQL query failed')
    expect(warningRows.join(' ')).not.toContain('Redis PING failed')
  })

  it('preserves the message for a compatibility status it does not recognize', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'degraded',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'maintenance',
          message: 'Kubernetes API maintenance is in progress',
          details: { enabled: true, configured: true },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'postgres' } },
        { name: 'cache_backend', status: 'ready', message: 'in-memory cache backend is ready', details: { cache_backend: 'memory' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const kubernetesRow = infrastructureReadinessRow('Kubernetes API')
    expect(kubernetesRow).not.toBeUndefined()
    expect(kubernetesRow!.textContent).toContain('Kubernetes API maintenance is in progress')
    expect(kubernetesRow!.textContent).toContain('Unknown')
    expect(kubernetesRow!.textContent).not.toContain('Waiting for readiness data.')
    expect(kubernetesRow!.querySelector('.monitor-infrastructure-status-unknown')).not.toBeNull()
    const readinessCardBadge = container.querySelector('[data-card-id="production-readiness"] .monitor-header-badge')
    expect(readinessCardBadge?.classList.contains('warning')).toBe(true)
    expect(readinessCardBadge?.textContent).toContain('Degraded · 1 unknown')
    expect(container.querySelector('[data-readiness-live-summary]')?.textContent).toContain('Readiness Degraded · 1 unknown.')
  })

  it('shows the Valkey provider and a not-ready cache state', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'not_ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'ready',
          message: 'Kubernetes integration is not configured for this runtime',
          details: { enabled: false, configured: false },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'sqlite' } },
        { name: 'cache_backend', status: 'not_ready', message: 'cache backend probe failed', details: { cache_backend: 'valkey' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const valkeyRow = infrastructureReadinessRow('Valkey Cache')
    expect(valkeyRow).not.toBeUndefined()
    expect(valkeyRow!.textContent).toContain('cache backend probe failed')
    expect(valkeyRow!.textContent).toContain('Not Ready')
    expect(valkeyRow!.querySelector('.monitor-infrastructure-status-not-ready')).not.toBeNull()
  })

  it('shows Kubernetes as not configured outside an in-cluster runtime', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'ready',
          message: 'Kubernetes integration is not configured for this runtime',
          details: { enabled: false, configured: false },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'sqlite' } },
        { name: 'cache_backend', status: 'ready', message: 'in-memory cache backend is ready', details: { cache_backend: 'memory' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const readiness = container.querySelector('[aria-label="Infrastructure readiness"]')
    expect(readiness!.textContent).toContain('Kubernetes APIThis runtime does not use this optional integration.Not applicable')
  })

  it('honors an explicit not-configured status from a newer controller', async () => {
    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'ready',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'kubernetes_api',
          status: 'not_configured',
          message: 'Kubernetes integration is disabled',
          details: { enabled: false, configured: false },
        },
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable', details: { store_type: 'sqlite' } },
        { name: 'cache_backend', status: 'ready', message: 'in-memory cache backend is ready', details: { cache_backend: 'memory' } },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const kubernetesRow = infrastructureReadinessRow('Kubernetes API')
    expect(kubernetesRow!.textContent).toContain('This runtime does not use this optional integration.')
    expect(kubernetesRow!.textContent).toContain('Not applicable')
    expect(kubernetesRow!.querySelector('.monitor-infrastructure-status-not-configured')).not.toBeNull()
    expect(kubernetesRow!.querySelector('.monitor-infrastructure-status-unknown')).toBeNull()
    expect(container.textContent).not.toContain('Kubernetes API: Kubernetes integration is disabled')
    expect(container.textContent).toContain('All configured production readiness checks are passing.')
  })

  it('renders active APEX backend and frontend services', async () => {
    apiMock.getSystemServices.mockResolvedValue({
      generated_at: '2026-05-18T12:05:00Z',
      services: [
        {
          id: 'controller-api',
          name: 'Controller API',
          kind: 'backend',
          status: 'running',
          address: ':8080',
          url: 'http://127.0.0.1:8080',
          version: 'dev',
          source: 'controller',
        },
        {
          id: 'recorder-proxy',
          name: 'Recorder Proxy',
          kind: 'backend',
          status: 'idle',
          address: ':8081',
          url: 'http://127.0.0.1:8081',
          source: 'recorder',
        },
        {
          id: 'generator:apexload-generator-0',
          name: 'apexload-generator-0',
          kind: 'backend',
          status: 'connected',
          address: 'apexload-generator-0.apexload-generator:9090',
          version: '0.1.0',
          source: 'generator_inventory',
          metadata: { role: 'generator' },
        },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])
    expect(container.textContent).toContain('Readiness & Services')
    expect(container.textContent).toContain('4/4 active')
    expect(container.textContent).toContain('Backend')
    expect(container.textContent).toContain('Controller API')
    expect(container.textContent).toContain('http://127.0.0.1:8080')
    expect(container.textContent).toContain('Recorder Proxy')
    expect(container.textContent).toContain('http://127.0.0.1:8081')
    expect(container.textContent).toContain('apexload-generator-0')
    expect(container.textContent).toContain('apexload-generator-0.apexload-generator:9090')
    expect(container.textContent).toContain('Frontend')
    expect(container.textContent).toContain('Dashboard')
    expect(container.textContent).toContain(window.location.origin)
  })

  it('keeps the dashboard frontend visible when the service inventory endpoint is unavailable', async () => {
    apiMock.getSystemServices.mockRejectedValue(new Error('service inventory unavailable'))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(container.textContent).toContain('Readiness & Services')
    expect(container.textContent).toContain('Waiting for /api/v1/system/services.')
    expect(container.textContent).toContain('Dashboard')
    expect(container.textContent).toContain(window.location.origin)
  })

  it('cleans up legacy overview cards from saved card orders', async () => {
    window.localStorage.setItem('apexload.monitoring.cardOrder', JSON.stringify([
      'system-health',
      'active-services',
      'production-readiness',
      'controller',
      'capacity',
      'generators',
      'fleet-alerts',
      'telemetry-diagnostics',
      'apm-integrations',
      'monitor-packs',
      'run-calendar',
    ]))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(visibleMonitorCardIds()).toEqual(['production-readiness', 'system-health', 'threshold-alerts', 'parquet-analysis'])
    expect(container.textContent).not.toContain('Active Services')
    const storedOrder = JSON.parse(window.localStorage.getItem('apexload.monitoring.cardOrder') || '[]') as string[]
    expect(storedOrder).not.toContain('active-services')
    expect(storedOrder).not.toContain('controller')
  })

  it('renders C4 APM profiles and infrastructure monitor packs', async () => {
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Telemetry')

    expect(container.textContent).toContain('APM Profiles')
    expect(container.textContent).toContain('0 configured · 2 supported profiles')
    expect(container.querySelector('[data-card-id="apm-integrations"] .monitor-header-badge')?.classList.contains('danger')).toBe(false)
    expect(container.textContent).toContain('OpenTelemetry: Supported; setup required')
    expect(container.textContent).toContain('Dynatrace: Supported; setup required')
    expect(container.textContent).toContain('Monitor Packs')
    expect(container.textContent).toContain('1/2 native')
    expect(container.textContent).toContain('Kubernetes: Native support')
    expect(container.textContent).toContain('JVM: Integration contract')
  })

  it('flags an enabled APM integration that is unavailable without flagging optional profiles', async () => {
    const profiles = await apiMock.getAPMIntegrationProfiles()
    profiles.profiles[0].status = 'unavailable'
    apiMock.getAPMIntegrationProfiles.mockResolvedValue(profiles)
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })
    await clickMonitorTab('Telemetry')
    const badge = container.querySelector('[data-card-id="apm-integrations"] .monitor-header-badge')
    expect(badge?.textContent).toBe('1 unavailable · 0 configured · 2 supported profiles')
    expect(badge?.classList.contains('danger')).toBe(true)
    expect(container.textContent).toContain('Dynatrace: Supported; setup required')
  })

  it('renders JMS broker diagnostics with lag and throughput details', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          telemetryDiagnostics={[
            {
              test_id: 'test-2',
              test_name: 'JMS Quotes',
              timestamp: '2026-05-12T20:06:01Z',
              diagnostic: {
                severity: 'warning',
                summary: 'sampled 1 JMS broker pressure hotspots where consumer lag materially exceeds request-reply latency',
                conditions: ['jms_broker_pressure'],
                samples: [
                  {
                    issue: 'broker',
                    transaction: 'RequestReplyQuote',
                    dependency_target: 'quotes.rpc',
                    avg_message_latency_ms: 60,
                    avg_consumer_lag_ms: 280,
                    avg_messages_sent_per_sec: 30,
                    avg_messages_received_per_sec: 12,
                  },
                ],
              },
            },
          ]}
        />,
      )
    })

    await clickMonitorTab('Telemetry')

    const telemetryButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View details'))
    expect(telemetryButton).not.toBeUndefined()

    await act(async () => {
      telemetryButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('jms_broker_pressure')
    expect(container.textContent).toContain('broker')
    expect(container.textContent).toContain('lag 280 ms')
    expect(container.textContent).toContain('msg 60 ms')
    expect(container.textContent).toContain('30/12 msg/s')
    expect(container.textContent).toContain('quotes.rpc')
  })

  it('renders JMS dead-letter diagnostics with message counts', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          telemetryDiagnostics={[
            {
              test_id: 'test-3',
              test_name: 'JMS DLQ',
              timestamp: '2026-05-12T20:07:01Z',
              diagnostic: {
                severity: 'warning',
                summary: 'observed 1 JMS dead-letter destinations with live traffic',
                conditions: ['jms_dead_letter_activity'],
                samples: [
                  {
                    issue: 'dead_letter',
                    transaction: 'ConsumeOrderDLQ',
                    dependency_target: 'orders.dlq',
                    count: 7,
                    errors: 2,
                  },
                ],
              },
            },
          ]}
        />,
      )
    })

    await clickMonitorTab('Telemetry')

    const telemetryButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View details'))
    expect(telemetryButton).not.toBeUndefined()

    await act(async () => {
      telemetryButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('jms_dead_letter_activity')
    expect(container.textContent).toContain('dead_letter')
    expect(container.textContent).toContain('7 dead-letter msgs')
    expect(container.textContent).toContain('orders.dlq')
  })

  it('renders live generator inventory when the controller reports it', async () => {
    apiMock.getFleetProvisioner.mockResolvedValue({
      name: 'static-fleet',
      can_provision: false,
      supported_regions: ['us-east'],
      description: 'Uses the pre-configured generator fleet; no on-demand provisioning.',
    })
    apiMock.getFleetCapacity.mockResolvedValue({
      project_id: 'project-1',
      total_generators: 1,
      eligible_generators: 1,
      active_slots: 400,
      available_slots: 9600,
      project_active_slots: 250,
      project_pending_slots: 400,
      reserved_slots: 10000,
      max_slots: 10000,
      compatible_features: ['http', 'browser'],
      rejection_reasons: [],
      project_slots: [
        {
          project_id: 'project-1',
          active_slots: 250,
          pending_slots: 400,
          running_tests: 1,
          pending_tests: 1,
          reserved_generators: 1,
          reserved_slots: 10000,
          fair_share_tier: 'reserved',
        },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'apexload-controller-0',
        status: 'healthy',
        address: ':8080',
        version: 'dev',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'apexload-generator-0.apexload-generator:9090',
          generator_id: 'apexload-generator-0',
          status: 'connected',
          available_capacity: 9600,
          active_vusers: 400,
          cpu_percent: 22.5,
          memory_bytes: 96 * 1024 * 1024,
          goroutine_count: 812,
          cpu_limit_cores: 2,
          memory_limit_bytes: 2 * 1024 * 1024 * 1024,
          max_vusers: 10000,
        },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(container.textContent).toContain('apexload-controller-0')
    expect(container.textContent).toContain('healthy')
    expect(container.textContent).toContain('1/1 connected')

    await clickMonitorTab('Fleet')

    expect(container.textContent).toContain('Generators')
    expect(container.textContent).toContain('1/1 online')
    expect(container.textContent).toContain('all connected')
    expect(container.textContent).toContain('400 active VUsers across 1 generator')
    expect(container.textContent).toContain('9,600')
    expect(container.textContent).toContain('Eligible Generators')
    expect(container.textContent).toContain('1/1')
    expect(container.textContent).toContain('Reserved Slots')
    expect(container.textContent).toContain('10,000')
    expect(container.textContent).toContain('Project Active Slots')
    expect(container.textContent).toContain('Project Pending Slots')
    // Project usage is a neutral row (label + value), not a warning entry.
    const projectSlots = Array.from(container.querySelectorAll('.monitor-row')).find(row => row.querySelector('.monitor-label')?.textContent === 'project-1')
    expect(projectSlots?.querySelector('.monitor-value')?.textContent).toBe('250 active / 400 pending slots (reserved)')
    expect(Array.from(container.querySelectorAll('.monitor-warning-list')).some(list => list.textContent?.includes('250 active'))).toBe(false)
    expect(container.textContent).toContain('Live generator inventory (fleet placement)')
    expect(container.textContent).toContain('http, browser')

    expect(container.querySelector('[data-testid="fleet-provisioner"]')).toBeTruthy()
    expect(container.textContent).toContain('Generator Provisioning')
    expect(container.textContent).toContain('static fleet')

    const generatorButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('active VUsers across'))
    expect(generatorButton).not.toBeUndefined()

    await act(async () => {
      generatorButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Generator Inventory')
    expect(container.textContent).toContain('apexload-generator-0')
    expect(container.textContent).toContain('connected')
    expect(container.textContent).toContain('96 MB')
    expect(container.textContent).toContain('22.5%')
    expect(container.textContent).toContain('2 cores')
    expect(container.textContent).toContain('2 GB')
    expect(container.textContent).toContain('Close')
  })

  it('renders fleet alert cards from generator inventory health', async () => {
    apiMock.getHealth.mockResolvedValue({ status: 'ok', version: '0.1.0', ws_clients: 2, test_count: 1 })
    apiMock.getFleetAlerts.mockResolvedValue({
      count: 5,
      unacknowledged_count: 5,
      alerts: [
        { id: 'alert-drain', type: 'drain_state', target: 'gen-a', severity: 'warning', summary: 'gen-a is cordoned; new placements are excluded.' },
        { id: 'alert-unreachable', type: 'unreachable', target: 'gen-b', severity: 'danger', summary: 'gen-b is unreachable; placement cannot use this generator.' },
        { id: 'alert-saturation', type: 'saturation', target: 'gen-b', severity: 'danger', summary: 'gen-b is saturated with 2,000 active VUsers.' },
        { id: 'alert-compat', type: 'compatibility', target: 'gen-b', severity: 'danger', summary: 'gen-b is not placement-compatible: missing grpc runtime.' },
        { id: 'alert-version', type: 'version_mismatch', target: '1.0.0', severity: 'warning', summary: '1 generator report version 1.0.0, which differs from controller 0.1.0.' },
      ],
    })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'apexload-controller-0',
        status: 'healthy',
        address: ':8080',
        version: '0.1.0',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'gen-a:9090',
          generator_id: 'gen-a',
          status: 'connected',
          version: '0.1.0',
          drain_state: 'cordoned',
          capacity_state: 'available',
          compatibility_state: 'compatible',
          available_capacity: 1000,
          active_vusers: 100,
          cpu_percent: 20,
          memory_bytes: 1000,
          goroutine_count: 20,
          cpu_limit_cores: 2,
          memory_limit_bytes: 2000,
          max_vusers: 2000,
        },
        {
          target: 'gen-b:9090',
          generator_id: 'gen-b',
          status: 'unreachable',
          version: '1.0.0',
          drain_state: 'active',
          capacity_state: 'saturated',
          compatibility_state: 'protocol_mismatch',
          compatibility_issues: ['missing grpc runtime'],
          available_capacity: 0,
          active_vusers: 2000,
          cpu_percent: 95,
          memory_bytes: 1900,
          goroutine_count: 2000,
          cpu_limit_cores: 2,
          memory_limit_bytes: 2000,
          max_vusers: 2000,
        },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Fleet')

    expect(container.textContent).toContain('Fleet Alerts')
    expect(container.textContent).toContain('5 alerts')
    expect(container.textContent).toContain('gen-a is cordoned')
    expect(container.textContent).toContain('gen-b is unreachable')
    expect(container.textContent).toContain('gen-b is saturated')
    expect(container.textContent).toContain('missing grpc runtime')
    expect(container.textContent).toContain('version 1.0.0')
  })

  it('lets system admins acknowledge fleet alerts', async () => {
    const initialAlert = { id: 'alert-unreachable', type: 'unreachable', target: 'gen-b', severity: 'danger', summary: 'gen-b is unreachable; placement cannot use this generator.' }
    const acknowledgedAlert = { ...initialAlert, acknowledged: true, acknowledged_at: new Date().toISOString() }
    apiMock.getFleetAlerts
      .mockResolvedValueOnce({ count: 1, unacknowledged_count: 1, alerts: [initialAlert] })
      .mockResolvedValueOnce({ count: 1, unacknowledged_count: 0, alerts: [acknowledgedAlert] })
      .mockResolvedValue({ count: 1, unacknowledged_count: 0, alerts: [acknowledgedAlert] })
    apiMock.ackFleetAlert.mockResolvedValue(acknowledgedAlert)

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    await clickMonitorTab('Fleet')

    const ackButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Ack')
    expect(ackButton).toBeDefined()
    await act(async () => {
      ackButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.ackFleetAlert).toHaveBeenCalledWith('alert-unreachable', { acknowledged: true })
    expect(container.textContent).toContain('acknowledged')
  })

  it('renders recent fleet audit activity for system admins', async () => {
    apiMock.listAuthAuditEvents.mockResolvedValue({
      count: 2,
      events: [
        {
          id: 'audit-fleet-1',
          action: 'generator.drain_state.update',
          actor_type: 'user',
          actor_subject: 'admin@example.test',
          actor_display_name: 'Platform Admin',
          resource_type: 'generator',
          resource_id: 'apexload-generator-0',
          status: 'success',
          created_at: '2026-06-26T12:00:00Z',
        },
        {
          id: 'audit-other',
          action: 'auth.user.login',
          actor_type: 'user',
          actor_subject: 'user@example.test',
          resource_type: 'auth_user',
          resource_id: 'user-1',
          status: 'success',
          created_at: '2026-06-26T12:01:00Z',
        },
      ],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} canManageSystem />)
    })

    await clickMonitorTab('Fleet')

    expect(apiMock.listAuthAuditEvents).toHaveBeenCalledWith({ limit: 8, category: 'fleet' })
    expect(container.textContent).toContain('Fleet Audit')
    expect(container.textContent).toContain('generator.drain_state.update')
    expect(container.textContent).toContain('generator/apexload-generator-0')
    expect(container.textContent).toContain('Platform Admin')
    expect(container.textContent).not.toContain('auth.user.login')
  })

  it('refreshes fleet state when a fleet websocket event is observed', async () => {
    apiMock.getFleetAlerts
      .mockResolvedValueOnce({ count: 0, unacknowledged_count: 0, alerts: [] })
      .mockResolvedValueOnce({
        count: 1,
        unacknowledged_count: 1,
        alerts: [{ id: 'alert-drain', type: 'drain_state', target: 'gen-a', severity: 'warning', summary: 'gen-a is draining; new placements are excluded.' }],
      })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: { id: 'apexload-controller-0', status: 'healthy', address: ':8080', version: '0.1.0', execution_mode: 'distributed' },
      generators: [],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} fleetEventSignal={0} />)
    })
    expect(apiMock.getFleetAlerts).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} fleetEventSignal={1} />)
    })

    expect(apiMock.getFleetAlerts).toHaveBeenCalledTimes(2)
    await clickMonitorTab('Fleet')
    expect(container.textContent).toContain('gen-a is draining')
  })

  it('renders schedule health counts for operator triage', async () => {
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 6,
      enabled: 5,
      due: 2,
      blocked: 0,
      failed: 0,
      pending_approval: 3,
      queued: 2,
      materialized: 9,
      skipped: 4,
      completed: 8,
      is_leader: true,
      execution_plan_total: 2,
      execution_plan_enabled: 1,
      execution_plan_due: 1,
      execution_plan_blocked: 1,
      one_time_scheduled: 1,
      one_time_due: 1,
      status_counts: {
        queued: 2,
      },
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Schedules')

    const advisoryBadge = container.querySelector('.monitor-title-advisory')
    expect(advisoryBadge).toBeTruthy()
    expect(advisoryBadge?.className).toContain('danger')
    expect(advisoryBadge?.textContent).toContain('2 issues need attention')
    expect(advisoryBadge?.getAttribute('title')).toContain('1 execution-plan schedules blocked')
    expect(advisoryBadge?.getAttribute('title')).toContain('4 due across all schedule domains')
    expect(container.querySelector('.monitor-schedule-health-card')).toBeNull()
    expect(container.querySelector('[data-card-id="schedule-health"]')).toBeNull()
    expect(visibleMonitorCardIds()).toEqual(['run-calendar'])
    expect(container.textContent).toContain('Schedules')
    expect(container.textContent).not.toContain('Advisory Panel')
    expect(container.textContent).not.toContain('Enabled Schedules')
    expect(apiMock.getScheduleHealth).toHaveBeenCalled()
  })

  it('reports scheduler storage errors and stale domain sweeps', async () => {
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 1,
      enabled: 1,
      due: 0,
      blocked: 0,
      failed: 0,
      pending_approval: 0,
      is_leader: true,
      storage_error: 'schedule metadata unavailable',
      sweeps: {
        scenario: { last_sweep_at: '2020-01-01T00:00:00Z', last_successful_at: '2020-01-01T00:00:00Z' },
        execution_plan: { last_sweep_at: new Date().toISOString(), last_successful_at: new Date().toISOString() },
      },
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const advisoryBadge = container.querySelector('.monitor-title-advisory')
    expect(advisoryBadge?.className).toContain('danger')
    expect(advisoryBadge?.getAttribute('title')).toContain('Storage: schedule metadata unavailable')
    expect(advisoryBadge?.getAttribute('title')).toContain('Stale sweeps: scenario')
  })

  it('does not report configured schedules healthy before their first sweep', async () => {
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 1,
      enabled: 1,
      due: 0,
      blocked: 0,
      failed: 0,
      pending_approval: 0,
      is_leader: true,
      sweeps: {},
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    const advisoryBadge = container.querySelector('.monitor-title-advisory')
    expect(advisoryBadge?.className).toContain('warning')
    expect(advisoryBadge?.getAttribute('title')).toContain('Missing sweep evidence: scenario')
  })

  it('renders run calendar events and opens test details', async () => {
    const onSelectTest = vi.fn()
    const onScheduleForDate = vi.fn()
    const eventStart = new Date()
    eventStart.setHours(9, 0, 0, 0)
    const eventEnd = new Date(eventStart)
    eventEnd.setHours(eventEnd.getHours() + 30)
    const projectedStart = new Date(eventStart)
    projectedStart.setDate(projectedStart.getDate() + 1)
    apiMock.getRunCalendar.mockResolvedValue({
      generated_at: new Date().toISOString(),
      range: {
        from: new Date(eventStart.getFullYear(), eventStart.getMonth(), 1).toISOString(),
        to: new Date(eventStart.getFullYear(), eventStart.getMonth() + 1, 1).toISOString(),
        requested_from: new Date(eventStart.getFullYear(), eventStart.getMonth(), 1).toISOString(),
        requested_to: new Date(eventStart.getFullYear(), eventStart.getMonth() + 1, 1).toISOString(),
        future_ceiling: new Date(Date.now() + 366 * 24 * 60 * 60 * 1000).toISOString(),
      },
      events: [
        {
          id: 'test:test-adhoc',
          source: 'ad_hoc',
          run_type: 'ad_hoc',
          title: 'Checkout Spike Investigation',
          status: 'RUNNING',
          start_at: eventStart.toISOString(),
          end_at: eventEnd.toISOString(),
          test_id: 'test-adhoc',
          test_ref_key: 'RUN-42',
          scenario_name: 'Checkout Scenario',
          workload: {
            total_vusers: 750,
            duration: '30h',
            groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout', protocol: 'http', vusers: 750 }],
          },
          user: {
            display_name: 'Dana Runner',
            subject: 'dana@example.com',
            attribution: 'started_by',
          },
          topology: {
            source: 'live',
            controller: {
              id: 'controller-1',
              status: 'healthy',
              address: ':8080',
              version: 'dev',
              execution_mode: 'distributed',
            },
            generator_shards: [{ group: 'buyers', target: 'generator-a', vusers: 750, shard_strategy: 'capacity_weighted' }],
            generator_inventory: [{
              target: 'generator-a:9090',
              generator_id: 'generator-a',
              status: 'connected',
              available_capacity: 9250,
              active_vusers: 750,
              cpu_percent: 20,
              memory_bytes: 1024,
              goroutine_count: 120,
              cpu_limit_cores: 2,
              memory_limit_bytes: 2048,
              max_vusers: 10000,
            }],
          },
        },
        {
          id: 'projected:schedule-1',
          source: 'cron',
          run_type: 'scheduled',
          title: 'Nightly Checkout',
          status: 'scheduled',
          start_at: projectedStart.toISOString(),
          end_at: new Date(projectedStart.getTime() + 30 * 60 * 1000).toISOString(),
          projected: true,
          schedule_id: 'schedule-1',
          schedule_name: 'Nightly Checkout',
          cron_expr: '0 2 * * *',
          scenario_name: 'Checkout Scenario',
          workload: { total_vusers: 2500, duration: '30m' },
          user: { display_name: 'Scheduler Owner', attribution: 'schedule_owner' },
          topology: { source: 'planned', summary: 'Generator assignment will be resolved when the run materializes.' },
        },
      ],
      count: 2,
      status_counts: { RUNNING: 1, scheduled: 1 },
      source_counts: { ad_hoc: 1, cron: 1 },
    })

    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          telemetryDiagnostics={[]}
          onSelectTest={onSelectTest}
          onScheduleForDate={onScheduleForDate}
        />,
      )
    })

    await clickMonitorTab('Schedules')

    expect(container.textContent).toContain('Run Calendar')
    expect(container.querySelector('[data-card-id="timeslots"]')).toBeNull()
    const reservationsPanel = container.querySelector('.run-calendar-reservations-panel')
    expect(reservationsPanel).toBeTruthy()
    expect(container.querySelector('.run-calendar-top-row')?.contains(reservationsPanel)).toBe(true)
    expect(reservationsPanel?.textContent).toContain('Scheduled bookings')
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledWith({ project_id: 'project-1', limit: 100, offset: 0 })
    const periodLabel = container.querySelector('.run-calendar-period-label')
    expect(periodLabel?.textContent).toBe(new Date().toLocaleDateString([], { month: 'long', year: 'numeric' }))
    expect(container.querySelector('.run-calendar-nav')?.textContent).not.toBe('Today')
    expect(container.textContent).toContain('Checkout Spike Investigation')
    expect(container.textContent).toContain('Nightly Checkout')
    expect(container.textContent).toContain('Cron')
    expect(container.textContent).toContain('Ad-hoc')
    expect(container.textContent).toContain('Current')

    const localKey = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
    const continuationDate = new Date(eventStart)
    continuationDate.setDate(continuationDate.getDate() + 1)
    const startingDay = container.querySelector(`[data-calendar-date="${localKey(eventStart)}"]`)
    const continuationDay = container.querySelector(`[data-calendar-date="${localKey(continuationDate)}"]`)
    expect(startingDay?.querySelector('.run-calendar-event-time')?.textContent).not.toContain('Continues')
    expect(Array.from(continuationDay?.querySelectorAll('.run-calendar-event') || [])
      .find(button => button.textContent?.includes('Checkout Spike Investigation'))
      ?.querySelector('.run-calendar-event-time')?.textContent).toBe('↪ Continues')
    expect(startingDay?.querySelector('[aria-label="1 starting"]')).not.toBeNull()
    expect(continuationDay?.querySelector('[aria-label="1 continuing"]')).not.toBeNull()

    const schedulableDay = container.querySelector<HTMLElement>('.run-calendar-day.schedulable')
    expect(schedulableDay?.getAttribute('role')).toBeNull()
    expect(schedulableDay?.querySelector('button[aria-label^="Schedule a run on"]')).not.toBeNull()
    await act(async () => {
      schedulableDay?.querySelector<HTMLButtonElement>('button[aria-label^="Schedule a run on"]')?.click()
    })
    expect(onScheduleForDate).toHaveBeenCalledTimes(1)

    const runButton = Array.from(container.querySelectorAll<HTMLButtonElement>('.run-calendar-event'))
      .find(button => button.textContent?.includes('Checkout Spike Investigation'))
    expect(runButton).not.toBeUndefined()

    await act(async () => {
      runButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Controller & Hosts')
    expect(container.textContent).toContain('Dana Runner')
    expect(container.textContent).toContain('RUN-42')
    expect(container.textContent).toContain('generator-a')
    const detailPanel = container.querySelector('.run-calendar-detail-panel')
    expect(detailPanel).toBeTruthy()
    expect(detailPanel?.className).not.toContain('collapsed')

    const collapseButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.getAttribute('aria-label') === 'Collapse run details')
    expect(collapseButton).not.toBeUndefined()

    await act(async () => {
      collapseButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.run-calendar-detail-panel')?.className).toContain('collapsed')

    const expandButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.getAttribute('aria-label') === 'Expand run details')
    expect(expandButton).not.toBeUndefined()

    await act(async () => {
      expandButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.run-calendar-detail-panel')?.className).not.toContain('collapsed')

    const openButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Open Test')
    expect(openButton).not.toBeUndefined()

    await act(async () => {
      openButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onSelectTest).toHaveBeenCalledWith('test-adhoc')
  })

  it('buckets multi-day calendar events once while preserving order and continuation counts', () => {
    const event = (id: string, title: string, start: Date, end: Date): RunCalendarEvent => ({
      id,
      source: 'cron',
      run_type: 'scheduled',
      title,
      status: 'scheduled',
      start_at: start.toISOString(),
      end_at: end.toISOString(),
      workload: { duration: '1h' },
      user: {},
      topology: {},
    })
    const anchor = new Date(2026, 7, 5, 12)
    const start = new Date(2026, 7, 3, 22)
    const end = new Date(2026, 7, 6, 1)
    const earlier = event('earlier', 'Zulu', new Date(2026, 7, 3, 8), new Date(2026, 7, 3, 9))
    const sameTimeAlphabetical = event('alpha', 'Alpha', start, new Date(2026, 7, 3, 23))
    const spanning = event('spanning', 'Soak', start, end)

    const model = buildRunCalendarModel('week', anchor, [spanning, sameTimeAlphabetical, earlier, {
      ...earlier,
      id: 'invalid',
      start_at: 'not-a-date',
    }])
    const august3 = model.days.find(day => day.key === '2026-08-03')!
    const august4 = model.days.find(day => day.key === '2026-08-04')!
    const august5 = model.days.find(day => day.key === '2026-08-05')!
    const august6 = model.days.find(day => day.key === '2026-08-06')!

    expect(august3.events.map(item => item.id)).toEqual(['earlier', 'alpha', 'spanning'])
    expect(august3.startingEventCount).toBe(3)
    expect(august3.continuingEventCount).toBe(0)
    expect(august4.events.map(item => item.id)).toEqual(['spanning'])
    expect(august5.events.map(item => item.id)).toEqual(['spanning'])
    expect(august6.events.map(item => item.id)).toEqual(['spanning'])
    expect([august4, august5, august6].map(day => day.continuingEventCount)).toEqual([1, 1, 1])
  })

  it('spans day-scale and bare-number workload durations when the calendar omits end_at', () => {
    const durationEvent = (id: string, duration: string): RunCalendarEvent => ({
      id,
      source: 'cron',
      run_type: 'scheduled',
      title: id,
      status: 'scheduled',
      start_at: new Date(2026, 7, 3, 22).toISOString(),
      workload: { duration },
      user: {},
      topology: {},
    })
    const anchor = new Date(2026, 7, 5, 12)

    const dayUnit = buildRunCalendarModel('week', anchor, [durationEvent('soak-2d', '2d')])
    expect(dayUnit.days.find(day => day.key === '2026-08-03')!.startingEventCount).toBe(1)
    expect(dayUnit.days.find(day => day.key === '2026-08-04')!.continuingEventCount).toBe(1)
    expect(dayUnit.days.find(day => day.key === '2026-08-05')!.continuingEventCount).toBe(1)
    expect(dayUnit.days.find(day => day.key === '2026-08-06')!.events).toEqual([])

    // A bare number is minutes: 240 minutes from 22:00 crosses into the next day.
    const bareNumber = buildRunCalendarModel('week', anchor, [durationEvent('soak-bare', '240')])
    expect(bareNumber.days.find(day => day.key === '2026-08-03')!.startingEventCount).toBe(1)
    expect(bareNumber.days.find(day => day.key === '2026-08-04')!.continuingEventCount).toBe(1)

    // Unparseable durations still fall back to the one-minute floor.
    const unparseable = buildRunCalendarModel('week', anchor, [durationEvent('soak-unknown', 'until stopped')])
    expect(unparseable.days.find(day => day.key === '2026-08-03')!.startingEventCount).toBe(1)
    expect(unparseable.days.find(day => day.key === '2026-08-04')!.events).toEqual([])
  })

  it('does not treat a same-origin backend service as the dashboard frontend', () => {
    const backend: SystemServicesResponse['services'][number] = {
      id: 'controller-api',
      name: 'Controller API',
      kind: 'backend',
      status: 'running',
      address: ':8080',
      url: 'http://127.0.0.1:8080',
      source: 'controller',
    }
    const merged = mergeDashboardService([backend], 'http://127.0.0.1:8080')
    expect(merged.map(service => service.id)).toEqual(['controller-api', 'frontend:dashboard'])

    const deduped = mergeDashboardService([
      backend,
      { ...backend, id: 'custom-frontend', name: 'Dashboard mirror', kind: 'frontend' },
    ], 'http://127.0.0.1:8080')
    expect(deduped.map(service => service.id)).toEqual(['controller-api', 'custom-frontend'])
  })

  it('shows unconfigured SLA coverage without asserting that tests are clear', async () => {
    await act(async () => root.render(<MonitoringPage connectionState="connected" />))
    const card = container.querySelector('[data-card-id="threshold-alerts"]')
    expect(card?.textContent).toContain('Not configured')
    expect(card?.textContent).toContain('Shared SLA incident history')
    expect(card?.querySelector('details')).toBeNull()
    expect(card?.querySelector('#shared-sla-incident-history')).not.toBeNull()
    expect(card?.textContent).not.toContain('No global SLA threshold breaches')
    expect(request).toHaveBeenCalledWith(expect.stringContaining('project_id=project-1'), expect.anything())
  })
})
