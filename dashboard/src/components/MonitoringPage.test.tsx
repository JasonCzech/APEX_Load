// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringPage from './MonitoringPage'

const apiMock = {
  getAPMIntegrationProfiles: vi.fn(),
  getHealth: vi.fn(),
  getInfrastructureMonitorPacks: vi.fn(),
  getFleetAlerts: vi.fn(),
  getFleetCapacity: vi.fn(),
  getFleetProvisioner: vi.fn(),
  getScheduleHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getSystemReadiness: vi.fn(),
  listTests: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('MonitoringPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    apiMock.getHealth.mockResolvedValue({ status: 'ok', version: 'dev', ws_clients: 2, test_count: 1 })
    apiMock.getFleetProvisioner.mockResolvedValue(null)
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
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 0,
      enabled: 0,
      due: 0,
      blocked: 0,
      failed: 0,
      pending_approval: 0,
      status_counts: {},
    })
    apiMock.getFleetAlerts.mockResolvedValue({ alerts: [], count: 0 })
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
    expect(container.textContent).toContain('Controller')
    expect(container.textContent).toContain('Production Readiness')
    expect(container.textContent).toContain('Ready Checks')
    expect(container.textContent).toContain('4/6')
    expect(container.textContent).toContain('Generator RPC: some generator RPC targets are not reachable')
    expect(container.textContent).toContain('Auth Mode: development auth bypass is enabled')
    expect(container.textContent).toContain('Generator Inventory')
    expect(container.textContent).toContain('0/0 connected')
    expect(visibleMonitorCardIds()).toEqual(['system-health', 'production-readiness', 'controller'])

    await clickMonitorTab('Telemetry')

    expect(visibleMonitorCardIds()).toEqual(['telemetry-diagnostics', 'apm-integrations', 'monitor-packs'])
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
    expect(apiMock.listTests).toHaveBeenCalledWith({ project_id: 'project-1' })
    expect(apiMock.getFleetAlerts).toHaveBeenCalled()
    expect(apiMock.getFleetCapacity).toHaveBeenCalledWith({ project_id: 'project-1' })
    expect(apiMock.getSystemReadiness).toHaveBeenCalled()

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Close')
    await act(async () => {
      closeButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await clickMonitorTab('Fleet')

    expect(visibleMonitorCardIds()).toEqual(['capacity', 'generators', 'fleet-alerts'])
    expect(container.textContent).toContain('Generators')
    expect(container.textContent).toContain('Fleet Alerts')
    expect(container.textContent).toContain('No generator inventory was returned by the controller.')
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
      alerts: [
        { type: 'drain_state', target: 'gen-a', severity: 'warning', summary: 'gen-a is cordoned; new placements are excluded.' },
        { type: 'unreachable', target: 'gen-b', severity: 'danger', summary: 'gen-b is unreachable; placement cannot use this generator.' },
        { type: 'saturation', target: 'gen-b', severity: 'danger', summary: 'gen-b is saturated with 2,000 active VUsers.' },
        { type: 'compatibility', target: 'gen-b', severity: 'danger', summary: 'gen-b is not placement-compatible: missing grpc runtime.' },
        { type: 'version_mismatch', target: '1.0.0', severity: 'warning', summary: '1 generator report version 1.0.0, which differs from controller 0.1.0.' },
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

  it('renders schedule health counts for operator triage', async () => {
    apiMock.getScheduleHealth.mockResolvedValue({
      total: 6,
      enabled: 5,
      due: 2,
      blocked: 1,
      failed: 0,
      pending_approval: 3,
      queued: 2,
      materialized: 9,
      skipped: 4,
      completed: 8,
      status_counts: {
        queued: 2,
      },
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Schedules')

    expect(container.textContent).toContain('Schedule Health')
    expect(container.textContent).toContain('1 blocked')
    expect(container.textContent).toContain('Due')
    expect(container.textContent).toContain('Blocked')
    expect(container.textContent).toContain('Approval')
    expect(container.textContent).toContain('Enabled Schedules')
    expect(container.textContent).toContain('5/6')
    expect(container.textContent).toContain('Queued')
    expect(container.textContent).toContain('2')
    expect(container.textContent).toContain('Materialized')
    expect(container.textContent).toContain('9')
    expect(container.textContent).toContain('Skipped')
    expect(container.textContent).toContain('4')
    expect(container.textContent).toContain('Completed')
    expect(container.textContent).toContain('8')
    expect(apiMock.getScheduleHealth).toHaveBeenCalled()
  })

  it('shows pending queue details in the run timeline', async () => {
    apiMock.listTests.mockResolvedValue({
      tests: [{
        id: 'test-pending',
        name: 'Checkout Capacity Queue',
        status: 'PENDING',
        groups: [{ name: 'Checkout', script_id: 'script-1', script_name: 'Checkout', protocol: 'http', vusers: 2500 }],
        total_vusers: 2500,
        duration: '15m',
        created_at: '2026-05-12T20:00:00Z',
        pending_since: '2026-05-12T20:03:00Z',
        queue_position: 1,
        pending_reason: 'waiting for project VUser capacity (2000 max vusers)',
      }],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Schedules')

    expect(container.textContent).toContain('Run Timeline')
    expect(container.textContent).toContain('Checkout Capacity Queue')
    expect(container.textContent).toContain('Queue #1')
    expect(container.textContent).toContain('waiting for project VUser capacity')
  })
})
