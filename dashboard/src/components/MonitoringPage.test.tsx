// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringPage from './MonitoringPage'

const apiMock = {
  getAPMIntegrationProfiles: vi.fn(),
  getHealth: vi.fn(),
  getInfrastructureMonitorPacks: vi.fn(),
  getParityStatus: vi.fn(),
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
    apiMock.getParityStatus.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      overall: 'partial',
      areas: [
        {
          id: 'architecture',
          name: 'Platform Architecture',
          status: 'partial',
          summary: 'Project ownership and readiness checks are in place.',
          evidence: ['/api/v1/system/readiness'],
          remaining: ['Complete external dependency evidence and production benchmark gates.'],
          release_gate: true,
        },
        {
          id: 'fleet',
          name: 'Fleet And Scale-Out',
          status: 'partial',
          summary: 'Fleet inventory and capacity APIs are available.',
          evidence: ['/api/v1/fleet/alerts', '/api/v1/fleet/capacity'],
          remaining: ['Complete production deployment autoscaling evidence.'],
          release_gate: true,
        },
        {
          id: 'authoring',
          name: 'Authoring UX',
          status: 'partial',
          summary: 'Imports, validation, replay evidence, and authoring health are implemented.',
          evidence: ['/api/v1/scripts/import/loadrunner'],
          remaining: ['Complete final authoring benchmark evidence.'],
          release_gate: true,
        },
        {
          id: 'ai_assistance',
          name: 'AI-Assisted Workflows',
          status: 'complete',
          summary: 'Governed deterministic AI assistance is implemented.',
          evidence: ['/api/v1/ai/authoring/contract', '/api/v1/ai/drafts'],
          release_gate: true,
        },
        {
          id: 'browser_testing',
          name: 'Browser Testing',
          status: 'partial',
          summary: 'Browser benchmark evidence is code-owned and gated.',
          evidence: ['/api/v1/system/browser-benchmarks'],
          remaining: ['Attach controlled raw benchmark output.'],
          release_gate: true,
        },
        {
          id: 'vugen_web2_migration',
          name: 'VuGen Web 2.0 Migration',
          status: 'partial',
          summary: 'TruClient 2.0 Web review-profile migration evidence is exposed.',
          evidence: ['/api/v1/scripts/import/loadrunner'],
          remaining: ['Keep production readiness gated on replay evidence.'],
          release_gate: true,
        },
        {
          id: 'load_modeling',
          name: 'Load Modeling',
          status: 'partial',
          summary: 'Load-model validation and report evidence have backend coverage.',
          evidence: ['/api/v1/scenarios/validate-load-model'],
          remaining: ['Complete final load-model benchmark evidence.'],
          release_gate: true,
        },
        {
          id: 'monitoring',
          name: 'Real-Time Monitoring',
          status: 'partial',
          summary: 'Operator checklist is available.',
          evidence: ['/api/v1/system/parity-status'],
          remaining: ['Complete final monitoring benchmark evidence.'],
          release_gate: true,
        },
        {
          id: 'reporting',
          name: 'Historical Reporting',
          status: 'partial',
          summary: 'Archives, CSV exports, usage snapshots, and retention apply are implemented.',
          evidence: ['/api/v1/system/usage/export'],
          remaining: ['Complete final historical reporting benchmark evidence.'],
          release_gate: true,
        },
        {
          id: 'scheduling',
          name: 'Scheduling',
          status: 'partial',
          summary: 'Schedules, occurrences, approvals, and health are implemented.',
          evidence: ['/api/v1/schedules/health'],
          remaining: ['Expand governance conflict evidence for all external environment calendars.'],
          release_gate: true,
        },
      ],
      competitive_parity: [
        {
          id: 'developer_experience',
          name: 'Developer Script-As-Code Experience',
          status: 'partial',
          competitors: ['k6'],
          summary: 'CLI and DSL workflows exist, and k6 result import is supported, but k6 conversion/export is incomplete.',
          evidence: ['README.md#build--run', '/api/v1/results/import'],
          remaining: ['Add apexctl script validation/explain commands and constrained k6 HTTP conversion/export.'],
          decision: 'differentiate',
          evidence_level: 'docs+code',
        },
        {
          id: 'managed_scale_load_zones',
          name: 'Managed Scale And Load Zones',
          status: 'partial',
          competitors: ['k6', 'NeoLoad'],
          summary: 'Regional inventory and capacity APIs exist; load-zone packaging remains.',
          evidence: ['/api/v1/fleet/capacity'],
          remaining: ['Promote regions to named load zones with cost/capacity metadata.'],
          decision: 'match_with_packaging',
          evidence_level: 'api+docs+tests',
        },
        {
          id: 'authoring_recording',
          name: 'Authoring, Recording, And Guided Repair',
          status: 'supported',
          competitors: ['k6', 'NeoLoad'],
          summary: 'Guided authoring, review approval, correlation repair, provenance review, and reusable components are supported for the current C5 scope.',
          evidence: ['/api/v1/scripts/import/loadrunner', '/api/v1/scripts/authoring/sessions', '/api/v1/scripts/authoring/components'],
          remaining: ['Expand public demo packs, competitive fixtures, and richer recorder examples.'],
          decision: 'supported_for_current_scope',
          evidence_level: 'api+docs+dashboard+tests',
        },
        {
          id: 'browser_testing',
          name: 'Browser Testing And Client UX Evidence',
          status: 'partial',
          competitors: ['k6', 'NeoLoad'],
          summary: 'Browser canaries are supported with density claims gated on benchmarks.',
          evidence: ['docs/MOBILE_BROWSER_EMULATION_PLAN.md'],
          remaining: ['Publish browser benchmark artifacts.'],
          decision: 'bounded_canary_support',
          evidence_level: 'api+docs+external_evidence_required',
        },
        {
          id: 'analysis_integrations',
          name: 'Analysis, APM, And Root-Cause Integrations',
          status: 'supported',
          competitors: ['k6', 'NeoLoad'],
          summary: 'APM profiles, monitor packs, root-cause summaries, SLA governance, report profiles, and external result ingestion are supported for C4 scope.',
          evidence: ['/api/v1/integrations/apm/profiles', '/api/v1/monitoring/packs', '/api/v1/tests/{id}/archive/root-cause', '/api/v1/results/import'],
          remaining: ['Deepen provider-specific APM fixtures, causal scoring, and packaged setup guides.'],
          decision: 'supported_for_current_scope',
          evidence_level: 'api+docs+dashboard+tests',
        },
        {
          id: 'specialized_enterprise_protocols',
          name: 'Specialized Enterprise Protocol Breadth',
          status: 'gap',
          competitors: ['NeoLoad'],
          summary: 'Native SAP GUI and Citrix protocols remain demand-gated.',
          evidence: ['docs/COMPETITIVE_COMPARISON.md#specialized-enterprise-protocols'],
          remaining: ['Publish explicit non-goal or coexistence guidance.'],
          decision: 'demand_gated_non_goal',
          evidence_level: 'docs',
        },
        {
          id: 'packaging_samples_trust',
          name: 'Packaging, Samples, And Trust',
          status: 'partial',
          competitors: ['k6', 'NeoLoad'],
          summary: 'Docs, support matrix, CI templates, dashboard workflows, and samples exist; release packaging remains.',
          evidence: ['docs/API_USER_GUIDE.md'],
          remaining: ['Publish canonical sample projects, one-command demo seeding, and release notes with benchmark deltas.'],
          decision: 'evidence_gate',
          evidence_level: 'docs',
        },
      ],
      browser_benchmarks: [
        {
          id: 'desktop_chromium_1080p',
          name: 'Desktop Chromium 1080p',
          browser_engine: 'chromium',
          device_profile: 'desktop_1080p',
          device_class: 'desktop',
          platform: 'desktop',
          status: 'baseline_artifact_published',
          evidence_level: 'code+docs+release_artifact_required',
          artifact_path: 'docs/BROWSER_BENCHMARK_ARTIFACTS.md#desktop-chromium-1080p',
          acceptance: {
            launch_p50_target_ms: 3000,
            action_overhead_target_ms: 10,
            rss_per_vuser_target_mb: 250,
            rss_attribution: 'chromium_cdp_process_snapshot',
            required_raw_run_count: 5,
            required_benchmark_command: 'benchmark command',
          },
          density_envelope: {
            recommended_canary_vusers_per_generator: 2,
            maximum_claimed_vusers_per_generator: 5,
            pairing_model: 'Run beside protocol load.',
            claim_rule: 'Do not claim high-density browser load.',
          },
          benchmark_commands: ['go test ./pkg/protocol/browser'],
          evidence: ['/api/v1/system/browser-benchmarks'],
          release_gate: true,
          high_density_claim_allowed: false,
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
    expect(container.textContent).toContain('Parity')
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
    expect(container.textContent).toContain('Parity')
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

    await clickMonitorTab('Parity')

    expect(visibleMonitorCardIds()).toEqual(['competitive-parity', 'parity-checklist'])
    expect(container.textContent).toContain('Parity Checklist')
    expect(container.textContent).toContain('Tracked Areas')
    expect(container.textContent).toContain('10/10')
    expect(container.textContent).toMatch(/Fleet And Scale-Out\s*Partial/i)
    expect(container.textContent).toMatch(/Browser Testing\s*Partial/i)
    expect(container.textContent).toMatch(/AI-Assisted Workflows\s*Complete/i)
    expect(container.textContent).toMatch(/VuGen Web 2\.0 Migration\s*Partial/i)
    expect(container.textContent).toContain('Browser Benchmark Evidence')
    expect(container.textContent).toContain('Desktop Chromium 1080p')
    expect(container.textContent).toContain('2-5 VUsers/generator')
    expect(container.textContent).toContain('High-density browser load claims are gated separately')
    expect(container.textContent).toMatch(/Real-Time Monitoring\s*Partial/i)
    expect(container.textContent).toContain('Release Gates')
    expect(container.textContent).toContain('Competitive Parity')
    expect(container.textContent).toContain('7/7 known limitations tracked across k6, NeoLoad')

    await clickMonitorTab('Fleet')

    expect(visibleMonitorCardIds()).toEqual(['capacity', 'generators', 'fleet-alerts'])
    expect(container.textContent).toContain('Generators')
    expect(container.textContent).toContain('Fleet Alerts')
    expect(container.textContent).toContain('No generator inventory was returned by the controller.')
  })

  it('keeps the parity checklist as the last monitoring card', async () => {
    window.localStorage.setItem('apexload.monitoring.cardOrder', JSON.stringify([
      'system-health',
      'production-readiness',
      'controller',
      'telemetry-diagnostics',
      'apm-integrations',
      'monitor-packs',
      'capacity',
      'parity-checklist',
      'competitive-parity',
      'schedule-health',
      'generators',
      'fleet-alerts',
      'run-timeline',
    ]))

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Parity')

    const cardIds = visibleMonitorCardIds()
    expect(cardIds).toContain('parity-checklist')
    expect(cardIds).toContain('competitive-parity')
    expect(cardIds[cardIds.length - 1]).toBe('parity-checklist')
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

  it('warns when the parity status endpoint is missing current release gates', async () => {
    apiMock.getParityStatus.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      overall: 'partial',
      areas: [
        { id: 'architecture', name: 'Platform Architecture', status: 'partial', summary: 'Architecture evidence.', evidence: ['/api/v1/system/readiness'], remaining: ['Benchmark evidence.'], release_gate: true },
        { id: 'fleet', name: 'Fleet And Scale-Out', status: 'partial', summary: 'Fleet evidence.', evidence: ['/api/v1/fleet/alerts'], remaining: ['Autoscaling evidence.'], release_gate: true },
        { id: 'authoring', name: 'Authoring UX', status: 'partial', summary: 'Authoring evidence.', evidence: ['/api/v1/scripts'], remaining: ['Authoring benchmark evidence.'], release_gate: true },
        { id: 'load_modeling', name: 'Load Modeling', status: 'partial', summary: 'Load model evidence.', evidence: ['/api/v1/scenarios/validate-load-model'], remaining: ['Load-model benchmark evidence.'], release_gate: true },
        { id: 'monitoring', name: 'Real-Time Monitoring', status: 'partial', summary: 'Monitoring evidence.', evidence: ['/api/v1/system/parity-status'], remaining: ['Monitoring benchmark evidence.'], release_gate: true },
        { id: 'reporting', name: 'Historical Reporting', status: 'partial', summary: 'Reporting evidence.', evidence: ['/api/v1/system/usage/export'], remaining: ['Reporting benchmark evidence.'], release_gate: true },
        { id: 'scheduling', name: 'Scheduling', status: 'partial', summary: 'Scheduling evidence.', evidence: ['/api/v1/schedules/health'], remaining: ['Calendar evidence.'], release_gate: true },
      ],
      competitive_parity: [],
      browser_benchmarks: [],
    })

    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Parity')

    expect(container.textContent).toContain('7/10 tracked')
    expect(container.textContent).toContain('Tracked Areas')
    expect(container.textContent).toContain('7/10')
    expect(container.textContent).toContain('Parity status is missing current release gates')
    expect(container.textContent).toContain('AI-Assisted Workflows')
    expect(container.textContent).toContain('Browser Testing')
    expect(container.textContent).toContain('VuGen Web 2.0 Migration')
    expect(container.textContent).toContain('0/7 tracked')
    expect(container.textContent).toContain('Competitive parity status is missing current gates')
  })

  it('opens competitive parity details with evidence levels and known limitations', async () => {
    await act(async () => {
      root.render(<MonitoringPage connectionState="connected" telemetryDiagnostics={[]} />)
    })

    await clickMonitorTab('Parity')

    const competitiveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('known limitations tracked'))
    expect(competitiveButton).not.toBeUndefined()

    await act(async () => {
      competitiveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('k6 and NeoLoad support status')
    expect(container.textContent).toContain('Developer Script-As-Code Experience')
    expect(container.textContent).toContain('Evidence Level')
    expect(container.textContent).toContain('docs + code')
    expect(container.textContent).toContain('Add apexctl script validation/explain commands and constrained k6 HTTP conversion/export.')
    expect(container.textContent).toMatch(/Authoring, Recording, And Guided Repair\s*Supported/i)
    expect(container.textContent).toContain('api + docs + dashboard + tests')
    expect(container.textContent).toContain('supported for current scope')
    expect(container.textContent).toContain('Browser Testing And Client UX Evidence')
    expect(container.textContent).toContain('api + docs + external evidence required')
    expect(container.textContent).toMatch(/Analysis, APM, And Root-Cause Integrations\s*Supported/i)
    expect(container.textContent).toContain('Deepen provider-specific APM fixtures, causal scoring, and packaged setup guides.')
    expect(container.textContent).toContain('Specialized Enterprise Protocol Breadth')
    expect(container.textContent).toContain('demand gated non goal')
    expect(container.textContent).toContain('Publish explicit non-goal or coexistence guidance.')
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
