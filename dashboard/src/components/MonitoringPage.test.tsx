// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import MonitoringPage from './MonitoringPage'

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
        { name: 'metadata_store', status: 'ready', message: 'metadata store is reachable' },
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
    expect(controlPlaneCard!.textContent).toContain('Active Tests')
    expect(controlPlaneCard!.textContent).toContain('WS Clients')
    expect(controlPlaneCard!.textContent).toContain('Generator Inventory')
    expect(controlPlaneCard!.textContent).toContain('0/0 connected')
    expect(controlPlaneCard!.textContent).toContain('Execution')
    expect(controlPlaneCard!.textContent).toContain('distributed')
    expect(controlPlaneCard!.textContent).toContain('Instance')
    expect(controlPlaneCard!.textContent).toContain('controller-1')
    expect(controlPlaneCard!.textContent).toContain('Address')
    expect(controlPlaneCard!.textContent).toContain(':8080')
    expect(controlPlaneCard!.textContent).toContain('Restart Controller')
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
    expect(container.textContent).toContain('4/6')
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
    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness'])

    await clickMonitorTab('Telemetry')

    expect(visibleMonitorCardIds()).toEqual(['telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors'])
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

    expect(visibleMonitorCardIds()).toEqual(['capacity', 'generators', 'fleet-alerts', 'fleet-audit', 'fleet-pools', 'generator-metadata'])
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

    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness'])
    await clickMonitorTab('Telemetry')
    expect(onCategoryChange).toHaveBeenCalledWith('telemetry')
    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness'])

    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          activeCategoryId="telemetry"
          onCategoryChange={onCategoryChange}
        />,
      )
    })
    expect(visibleMonitorCardIds()).toEqual(['telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors'])
    expect(container.querySelector('.monitor-page-title')?.textContent).toContain('Telemetry')
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

    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness'])
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

    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness'])
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
    expect(container.textContent).toContain('1/2 ready')
    expect(container.textContent).toContain('OpenTelemetry: supported')
    expect(container.textContent).toContain('Dynatrace: available')
    expect(container.textContent).toContain('Monitor Packs')
    expect(container.textContent).toContain('1/2 native')
    expect(container.textContent).toContain('Kubernetes: pod saturation, active slots')
    expect(container.textContent).toContain('JVM: heap used, gc pause')
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
    expect(container.textContent).toContain('project-1: 250 active / 400 pending slots (reserved)')
    expect(container.textContent).toContain('fleet placement API')
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

    expect(apiMock.listAuthAuditEvents).toHaveBeenCalledWith({ limit: 20 })
    expect(container.textContent).toContain('Fleet Audit')
    expect(container.textContent).toContain('generator.drain_state.update')
    expect(container.textContent).toContain('apexload-generator-0 by Platform Admin')
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
    expect(container.textContent).toContain('Schedules1/1')
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
    const eventStart = new Date()
    eventStart.setHours(9, 0, 0, 0)
    const eventEnd = new Date(eventStart)
    eventEnd.setMinutes(eventEnd.getMinutes() + 30)
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
            duration: '30m',
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
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} onSelectTest={onSelectTest} />)
    })

    await clickMonitorTab('Schedules')

    expect(container.textContent).toContain('Run Calendar')
    expect(container.querySelector('[data-card-id="timeslots"]')).toBeNull()
    const reservationsPanel = container.querySelector('.run-calendar-reservations-panel')
    expect(reservationsPanel).toBeTruthy()
    expect(container.querySelector('.run-calendar-top-row')?.contains(reservationsPanel)).toBe(true)
    expect(reservationsPanel?.textContent).toContain('Capacity Reservations')
    expect(capacityApiMock.listTimeslots).toHaveBeenCalledWith({ project_id: 'project-1' })
    const periodLabel = container.querySelector('.run-calendar-period-label')
    expect(periodLabel?.textContent).toBe(new Date().toLocaleDateString([], { month: 'long', year: 'numeric' }))
    expect(container.querySelector('.run-calendar-nav')?.textContent).not.toBe('Today')
    expect(container.textContent).toContain('Checkout Spike Investigation')
    expect(container.textContent).toContain('Nightly Checkout')
    expect(container.textContent).toContain('Cron')
    expect(container.textContent).toContain('Ad-hoc')
    expect(container.textContent).toContain('Current')

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
})
