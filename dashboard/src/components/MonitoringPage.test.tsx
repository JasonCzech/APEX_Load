// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringPage from './MonitoringPage'

const apiMock = {
  getHealth: vi.fn(),
  getParityStatus: vi.fn(),
  getFleetAlerts: vi.fn(),
  getFleetCapacity: vi.fn(),
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
    apiMock.getHealth.mockResolvedValue({ status: 'ok', version: 'dev', ws_clients: 2, test_count: 1 })
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
    apiMock.getParityStatus.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      overall: 'partial',
      areas: [
        {
          id: 'fleet',
          name: 'Fleet And Scale-Out',
          status: 'partial',
          summary: 'Fleet evidence is partially available.',
          evidence: ['/api/v1/fleet/alerts'],
          remaining: ['Complete production deployment autoscaling evidence.'],
          release_gate: true,
        },
        {
          id: 'monitoring',
          name: 'Real-Time Monitoring',
          status: 'complete',
          summary: 'Operator checklist is available.',
          evidence: ['/api/v1/system/parity-status'],
          release_gate: true,
        },
      ],
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

    expect(container.textContent).toContain('Telemetry Diagnostics')
    expect(container.textContent).toContain('Controller')
    expect(container.textContent).toContain('Production Readiness')
    expect(container.textContent).toContain('Ready Checks')
    expect(container.textContent).toContain('4/6')
    expect(container.textContent).toContain('Generator RPC: some generator RPC targets are not reachable')
    expect(container.textContent).toContain('Auth Mode: development auth bypass is enabled')
    expect(container.textContent).toContain('Parity Checklist')
    expect(container.textContent).toContain('Fleet And Scale-Out: partial')
    expect(container.textContent).toContain('Real-Time Monitoring: complete')
    expect(container.textContent).toContain('Release Gates')
    expect(container.textContent).toContain('Generator Inventory')
    expect(container.textContent).toContain('0/0 connected')
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
        id: 'stormrunner-controller-0',
        status: 'healthy',
        address: ':8080',
        version: 'dev',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'stormrunner-generator-0.stormrunner-generator:9090',
          generator_id: 'stormrunner-generator-0',
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

    expect(container.textContent).toContain('stormrunner-controller-0')
  expect(container.textContent).toContain('healthy')
  expect(container.textContent).toContain('1/1 connected')
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

    const generatorButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('View details'))
    expect(generatorButton).not.toBeUndefined()

    await act(async () => {
      generatorButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Generator Inventory')
    expect(container.textContent).toContain('stormrunner-generator-0')
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
        id: 'stormrunner-controller-0',
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
      status_counts: {
        materialized: 9,
        skipped: 4,
        completed: 8,
      },
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    expect(container.textContent).toContain('Schedule Health')
    expect(container.textContent).toContain('1 blocked')
    expect(container.textContent).toContain('Due')
    expect(container.textContent).toContain('Blocked')
    expect(container.textContent).toContain('Approval')
    expect(container.textContent).toContain('Enabled Schedules')
    expect(container.textContent).toContain('5/6')
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

    expect(container.textContent).toContain('Run Timeline')
    expect(container.textContent).toContain('Checkout Capacity Queue')
    expect(container.textContent).toContain('Queue #1')
    expect(container.textContent).toContain('waiting for project VUser capacity')
  })
})
