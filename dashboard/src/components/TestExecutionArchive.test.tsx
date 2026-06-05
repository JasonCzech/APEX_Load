// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveAnalysisResponse, RunArchiveReport, TestSummary, TransactionEvent } from '../types'
import { formatSnapshotRequestSection, formatSnapshotResponseSection } from '../utils/snapshotFormatUtils'
import { formatSOAPFaultSummary } from './soapFaultUtils'
import TestExecutionArchive from './TestExecutionArchive'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
;(globalThis as typeof globalThis & { ResizeObserver?: typeof ResizeObserver }).ResizeObserver = class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as typeof ResizeObserver

describe('TestExecutionArchive', () => {
  let container: HTMLDivElement
  let root: Root
  const writeText = vi.fn()

  const test: TestSummary = {
    id: 'test-1',
    name: 'SOAP Fault Test',
    status: 'FAILED',
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: '2026-05-14T20:00:00Z',
    archive: {
      test_id: 'test-1',
      schema_version: '1',
      archive_path: '/tmp/archive.jsonl',
      manifest_path: '/tmp/manifest.json',
      transaction_count: 1,
      resource_sample_count: 0,
      interval_summary_count: 0,
      created_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:01:00Z',
    },
  }

  const archiveReport: RunArchiveReport = {
    schema_version: 'apexload.archive_report.v1',
    compatibility: {
      status: 'compatible',
      report_schema_version: 'apexload.archive_report.v1',
      manifest_schema_version: 'apexload.archive.v1',
      supported_manifest_schemas: ['apexload.archive.v1'],
      outputs: [
        { format: 'json', artifact: 'archive/report', schema_version: 'apexload.archive_report.v1', status: 'compatible' },
        { format: 'standalone_html', artifact: 'report', schema_version: 'apexload.archive_report.v1', status: 'compatible' },
        { format: 'transactions_parquet', artifact: 'transactions.parquet', schema_version: 'apexload.archive.v1', status: 'missing' },
        { format: 'pdf', artifact: 'archive/report?format=pdf', schema_version: 'apexload.archive_report.v1', status: 'compatible' },
        { format: 'csv', status: 'unsupported', reason: 'CSV archive exports are not registered for this report schema yet.' },
      ],
    },
    completeness: {
      score_pct: 92,
      status: 'needs_review',
      findings: [
        { dimension: 'telemetry_gaps', severity: 'warning', penalty: 8, message: 'Telemetry was degraded for one or more archived intervals.' },
      ],
    },
    change_attribution: {
      script_revisions: ['script-a', 'script-b'],
      scenario_revision: 'scenario-9',
      environment: 'perf-lab',
      generator_versions: ['0.2.0'],
      data_revision: 'script-a:data,script-b:data',
      runtime_overrides: true,
      runtime_override_fields: ['browser', 'log_level'],
    },
    report_profiles: [
      { id: 'executive', label: 'Executive', description: 'Decision-focused summary.', export_formats: ['html', 'csv', 'pdf'], included_sections: ['overview'] },
      { id: 'engineer', label: 'Engineer', description: 'Engineering drilldown.', export_formats: ['html', 'pdf', 'jsonl'], included_sections: ['actions'] },
      { id: 'audit', label: 'Audit', description: 'Governance package.', export_formats: ['html', 'json'], included_sections: ['evidence'] },
    ],
    manifest: {
      schema_version: '1',
      test_id: 'test-1',
      test_name: 'SOAP Fault Test',
      created_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:01:00Z',
      provenance: {
        project_id: 'project-1',
        release_id: 'release-7',
        scenario_id: 'scenario-9',
        scenario_mode: 'goal_oriented',
        resource_baseline: true,
        script_shakeout: true,
        start_delay: '3s',
        total_vusers: 1,
        duration: '1m',
        baseline_pinned: true,
        runtime_settings: {
          browser: {
            device_profile: 'iphone_se',
            orientation: 'portrait',
            touch_mode: true,
            headless: false,
          },
        },
        execution_settings: {
          general: {
            api_endpoint: ':8080',
            max_concurrent_tests: 10,
            metrics_interval_seconds: 5,
          },
          thresholds: {
            max_error_rate_pct: 2,
          },
          capacity_planning: {
            headroom_pct: 20,
            generator: {
              cpu_cores: 4,
              memory_mb: 8192,
              network_mbps: 1000,
              max_vusers: 10000,
            },
            availability: {
              source: 'configured',
              configured_available_generators: 1,
            },
          },
        },
        groups: [{ name: 'buyers', script_id: 'script-1', script_name: 'Checkout', protocol: 'soap', vusers: 1, initialization: 'at_once' }],
        controller_env: {
          APEXLOAD_SAMPLE_FLAG: 'enabled',
        },
        generator_inventory: [{ target: 'generator-1', status: 'healthy', available_capacity: 50 }],
      },
      transactions_file: 'transactions.jsonl',
      resource_samples_file: 'resources.jsonl',
      interval_summaries_file: 'intervals.jsonl',
      transaction_count: 1,
      resource_sample_count: 0,
      interval_summary_count: 0,
    },
    overview: {
      transactions: 1,
      errors: 1,
      error_pct: 100,
      avg_response_ms: 120,
      min_response_ms: 120,
      max_response_ms: 120,
      resource_samples: 0,
      interval_summaries: 0,
      peak_tps: 1,
      peak_target_tps: 2,
      max_throughput_gap_pct: 50,
      throughput_missed_intervals: 3,
      peak_active_vusers: 1,
      load_model_goals: {
        configured_global_iterations: 5,
        configured_max_transactions: 10,
        target_tps: 2,
        staged_groups: 1,
        stage_count: 2,
        once_only_groups: 1,
      },
      goal_completion: {
        achieved_transactions: 1,
        configured_max_transactions: 10,
        transaction_completion_pct: 10,
        configured_global_iterations: 5,
        throughput_target_tps: 2,
        throughput_missed_intervals: 3,
      },
      browser: {
        samples: 1,
        avg_fcp_ms: 130,
        avg_lcp_ms: 240,
        avg_cls_score: 0.031,
        avg_inp_ms: 56,
        avg_long_task_count: 1,
        avg_blocking_resources: 2,
      },
      telemetry: {
        status: 'degraded',
        samples: 3,
        healthy_intervals: 1,
        degraded_intervals: 2,
        breached_intervals: 0,
        max_sample_age_ms: 850,
        max_generator_processing_lag_ms: 40,
        max_transport_lag_ms: 60,
      },
    },
    evidence_tiers: [
      { tier: 'summary', label: 'Summary', status: 'present', count: 3, description: 'Aggregated run totals.' },
      { tier: 'external_metrics', label: 'External Metrics', status: 'missing', count: 0, reason: 'No external metrics were archived.' },
    ],
    by_action: {},
    by_soap_operation: {},
    by_region: {
      'us-east': { transactions: 80, errors: 2, error_pct: 2.5, avg_response_ms: 120, min_response_ms: 40, max_response_ms: 300 },
      'eu-west': { transactions: 20, errors: 0, error_pct: 0, avg_response_ms: 210, min_response_ms: 90, max_response_ms: 420 },
    },
    by_network_profile: {
      '4g': { transactions: 70, errors: 1, error_pct: 1.4, avg_response_ms: 95, min_response_ms: 30, max_response_ms: 280 },
      '3g': { transactions: 30, errors: 3, error_pct: 10, avg_response_ms: 320, min_response_ms: 150, max_response_ms: 800 },
    },
    by_soap_fault_code: {},
    by_soap_fault_category: {},
    summary_timeline: [{
      timestamp: '2026-05-14T20:00:00Z',
      active_vusers: 1,
      tps: 1,
      error_pct: 100,
      p95_ms: 120,
      total_transactions: 1,
      total_errors: 1,
    }],
    error_patterns: [{
      signature: 'soap fault: request validation failed for order #',
      sample: 'soap fault: Request validation failed for order 1001',
      count: 2,
      first_seen: '2026-05-14T20:00:10Z',
      latest_seen: '2026-05-14T20:00:30Z',
      actions: ['CreateOrder'],
      snapshot: { request_url: 'https://soap.example.test/orders' },
    }],
    soap_fault_timeline: [],
    soap_payload_timeline: [],
    browser_by_engine: {
      webkit: {
        browser_engine: 'webkit',
        samples: 1,
        avg_fcp_ms: 130,
        avg_lcp_ms: 240,
        avg_cls_score: 0.031,
        avg_inp_ms: 56,
      },
    },
    browser_by_platform: {
      ios: {
        device_platform: 'ios',
        browser_engine: 'webkit',
        samples: 1,
        avg_fcp_ms: 130,
        avg_lcp_ms: 240,
        avg_cls_score: 0.031,
        avg_inp_ms: 56,
      },
    },
    browser_by_device: {
      iphone_se: {
        device_profile: 'iphone_se',
        device_platform: 'ios',
        device_tier: 'low',
        browser_engine: 'webkit',
        samples: 1,
        avg_fcp_ms: 130,
        avg_lcp_ms: 240,
        avg_cls_score: 0.031,
        avg_inp_ms: 56,
      },
    },
    browser_resources: [{
      timestamp: '2026-05-14T20:00:30Z',
      action_name: 'browser_navigate',
      page_url: 'https://example.test/home',
      url: 'https://example.test/assets/app.js',
      method: 'GET',
      status_code: 200,
      resource_type: 'script',
      duration_ms: 45,
      bytes_received: 2048,
      device_profile: 'iphone_se',
      device_platform: 'ios',
      browser_engine: 'webkit',
    }],
    resource_peaks: {
      cpu_process_pct: 0,
      mem_process_mb: 0,
      net_sent_kbps: 0,
      net_recv_kbps: 0,
      disk_read_kbps: 0,
      disk_write_kbps: 0,
    },
  }

  const archiveAnalysis: RunArchiveAnalysisResponse = {
    schema_version: 'apexload.archive_analysis.v1',
    test_id: 'test-1',
    baseline_id: 'baseline-1',
    graph: {
      nodes: [
        {
          id: 'action:CreateOrder',
          kind: 'action',
          label: 'CreateOrder',
          runs: [
            { run_id: 'baseline-1', run_name: 'Baseline', transactions: 2, errors: 0, error_pct: 0, avg_response_ms: 90 },
            { run_id: 'test-1', run_name: 'SOAP Fault Test', transactions: 4, errors: 1, error_pct: 25, avg_response_ms: 140 },
          ],
        },
        {
          id: 'dependency:soap:orders',
          kind: 'dependency',
          label: 'soap:orders',
          runs: [
            { run_id: 'baseline-1', run_name: 'Baseline', transactions: 2, errors: 0, error_pct: 0, avg_response_ms: 90 },
            { run_id: 'test-1', run_name: 'SOAP Fault Test', transactions: 4, errors: 1, error_pct: 25, avg_response_ms: 140 },
          ],
        },
      ],
      edges: [
        {
          id: 'action_dependency:action:CreateOrder->dependency:soap:orders',
          kind: 'action_dependency',
          from: 'action:CreateOrder',
          to: 'dependency:soap:orders',
          runs: [
            { run_id: 'baseline-1', run_name: 'Baseline', transactions: 2, errors: 0, error_pct: 0, avg_response_ms: 90 },
            { run_id: 'test-1', run_name: 'SOAP Fault Test', transactions: 4, errors: 1, error_pct: 25, avg_response_ms: 140 },
          ],
        },
      ],
    },
    transactions: [
      {
        index: 0,
        timestamp: '2026-05-14T20:00:30Z',
        run_id: 'test-1',
        group: 'buyers',
        region: 'us-east',
        action_name: 'CreateOrder',
        dependency_type: 'soap',
        dependency_target: 'orders',
        duration_ms: 140,
        status_code: 500,
        success: false,
        error_message: 'soap fault',
        bytes_received: 512,
        trace_id: 'trace-1',
        correlation_id: 'corr-1',
        source_transaction: 'Checkout Flow',
        source_action: 'Create Order',
        path: ['us-east', 'buyers', 'Checkout Flow', 'CreateOrder', 'soap:orders'],
      },
    ],
    count: 1,
    total: 1,
    limit: 10,
    offset: 0,
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    writeText.mockReset()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('surfaces parsed SOAP fault details from archived snapshots', async () => {
    const onDownloadBrowserDiagnostics = vi.fn()
    const onDownloadArchiveArtifact = vi.fn()

    const transactions: TransactionEvent[] = [{
      timestamp: '2026-05-14T20:00:30Z',
      test_id: 'test-1',
      test_name: 'SOAP Fault Test',
      group: 'CreateOrder',
      action_name: 'CreateOrder',
      duration_ms: 120,
      status_code: 200,
      success: false,
      error_message: 'soap fault: env:Sender: Request validation failed',
      bytes_received: 512,
      client_metrics: {
        device_profile: 'iphone_se',
        device_platform: 'ios',
        device_tier: 'low',
        browser_engine: 'webkit',
        fcp_ms: 130,
        lcp_ms: 240,
        cls_score: 0.031,
        inp_ms: 56,
        dom_content_loaded_ms: 180,
        dom_complete_ms: 320,
        load_event_ms: 340,
        blocking_resource_count: 2,
        total_resource_count: 14,
        resource_transfer_kb: 128.5,
        long_task_count: 1,
      },
      snapshot: {
        request_method: 'POST',
        request_url: 'https://soap.example.test/orders',
        request_headers: {
          'Content-Type': 'application/soap+xml',
          ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Debug-${index}`, `value-${index}`])),
        },
        request_body: '<CreateOrder />',
        response_status: 200,
        response_headers: {
          'Content-Type': 'application/soap+xml',
          ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Trace-${index}`, `trace-${index}`])),
        },
        response_body: '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>',
      },
    }]

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          archiveAnalysis={archiveAnalysis}
          archiveAnalysisLoading={false}
          archiveAnalysisError=""
          onDownloadBrowserDiagnostics={onDownloadBrowserDiagnostics}
          onDownloadArchiveArtifact={onDownloadArchiveArtifact}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={1}
          archivePageStart={1}
          archivePageEnd={1}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={transactions}
          expandedArchiveEvent="2026-05-14T20:00:30Z-0"
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.querySelector('[data-testid="archive-by-region"]')).toBeTruthy()
    expect(container.textContent).toContain('Region Summary')
    expect(container.textContent).toContain('us-east')
    expect(container.querySelector('[data-testid="archive-by-network-profile"]')).toBeTruthy()
    expect(container.textContent).toContain('Network Profile Summary')

    expect(container.textContent).toContain('SOAP Fault')
    expect(container.textContent).toContain('SOAP 1.2')
    expect(container.textContent).toContain('Code: env:Sender')
    expect(container.textContent).toContain('Reason: Request validation failed')
    expect(container.textContent).toContain('Summary Timeline')
    expect(container.textContent).toContain('2026-05-14 20:00Z')
    expect(container.textContent).toContain('120.0 ms')
    expect(container.querySelector('[data-testid="archive-analysis-section"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="archive-analysis-nodes"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="archive-analysis-edges"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="archive-analysis-drilldown"]')).toBeTruthy()
    expect(container.textContent).toContain('Archive Analysis')
    expect(container.textContent).toContain('2 nodes')
    expect(container.textContent).toContain('1 link')
    expect(container.textContent).toContain('Baseline baseline-1')
    expect(container.textContent).toContain('Action to Dependency')
    expect(container.textContent).toContain('CreateOrder')
    expect(container.textContent).toContain('soap:orders')
    expect(container.textContent).toContain('4 tx · 25.00% err · avg 140.0 ms')
    expect(container.textContent).toContain('2 tx · 0.00% err · avg 90.0 ms')
    expect(container.textContent).toContain('us-east / buyers / Checkout Flow / CreateOrder / soap:orders')
    expect(container.textContent).toContain('soap: orders')
    expect(container.textContent).toContain('Run Provenance')
    expect(container.textContent).toContain('Target 2.0 TPS')
    expect(container.textContent).toContain('Throughput Gap')
    expect(container.textContent).toContain('3 missed target intervals')
    expect(container.textContent).toContain('Goal Completion')
    expect(container.textContent).toContain('1 / 10 transactions')
    expect(container.textContent).toContain('Staged Load')
    expect(container.textContent).toContain('1 group · 1 once-only')
    expect(container.textContent).toContain('buyers · 1 VUsers · init once')
    expect(container.textContent).toContain('Completeness Score')
    expect(container.textContent).toContain('92% · needs review')
    expect(container.textContent).toContain('telemetry gaps')
    const metadataBoxes = Array.from(container.querySelectorAll('.archive-metadata-box'))
    expect(metadataBoxes.map(box => box.querySelector('h3')?.textContent)).toEqual([
      'Telemetry Health',
      'Schema Compatibility',
      'Evidence Tiers',
      'Run Provenance',
      'Change Attribution',
    ])
    expect(container.textContent).toContain('1 healthy · 2 degraded · 0 breached')
    expect(container.textContent).toContain('Max Sample Age')
    expect(container.textContent).toContain('850ms')
    expect(container.textContent).toContain('Schema Compatibility')
    expect(container.textContent).toContain('apexload.archive_report.v1')
    expect(container.textContent).toContain('standalone html')
    expect(container.textContent).toContain('Evidence Tiers')
    expect(container.textContent).toContain('Summary')
    expect(container.textContent).toContain('present · 3')
    expect(container.textContent).toContain('External Metrics')
    expect(container.textContent).toContain('missing · 0')
    expect(container.textContent).toContain('Executive')
    expect(container.textContent).toContain('Engineer')
    expect(container.textContent).toContain('Audit')
    expect(container.querySelector('a[href="/api/v1/tests/test-1/report?profile=executive"]')).toBeTruthy()
    expect(container.querySelector('a[href="/api/v1/tests/test-1/archive/report"]')).toBeTruthy()
    const profileCsvLink = Array.from(container.querySelectorAll('a')).find(anchor => anchor.getAttribute('href') === '/api/v1/tests/test-1/archive/report?format=csv&profile=executive')
    expect(profileCsvLink).toBeTruthy()
    const profilePdfLinks = Array.from(container.querySelectorAll('a')).map(anchor => anchor.getAttribute('href'))
    expect(profilePdfLinks).toContain('/api/v1/tests/test-1/archive/report?format=pdf&profile=executive')
    expect(profilePdfLinks).toContain('/api/v1/tests/test-1/archive/report?format=pdf&profile=engineer')
    expect(profilePdfLinks).not.toContain('/api/v1/tests/test-1/archive/report?format=pdf&profile=audit')
    expect(container.textContent).toContain('Executive HTML')
    expect(container.textContent).toContain('Executive CSV')
    expect(container.textContent).toContain('Executive PDF')
    expect(container.textContent).toContain('Engineer PDF')
    expect(container.textContent).toContain('Engineer JSONL')
    expect(container.textContent).toContain('overview')
    expect(container.textContent).toContain('Error Patterns')
    expect(container.textContent).toContain('soap fault: Request validation failed for order 1001')
    expect(container.textContent).toContain('CreateOrder')
    expect(container.textContent).toContain('retained')
    expect(container.textContent).toContain('goal_oriented · 1 VUsers · 1m')
    expect(container.textContent).toContain('project-1 / release-7 / scenario-9')
    expect(container.textContent).toContain('Shakeout Enabled · Baseline Enabled')
    expect(container.textContent).toContain('Change Attribution')
    expect(container.textContent).toContain('script-a, script-b')
    expect(container.textContent).toContain('perf-lab')
    expect(container.textContent).toContain('Browser Runtime')
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')
    expect(container.textContent).toContain('Pinned')
    expect(container.textContent).toContain('APEXLOAD_SAMPLE_FLAG=enabled')
    expect(container.textContent).toContain('generator-1 · healthy · 50 available')
    expect(container.textContent).toContain('Browser Metrics')
    expect(container.textContent).toContain('UX Intelligence')
    expect(container.textContent).toContain('Healthy · 100/100')
    expect(container.textContent).toContain('Browser UX signals are within the current canary thresholds; weakest device is iphone_se.')
    expect(container.textContent).toContain('Weakest: iphone_se')
    expect(container.textContent).toContain('Largest Contentful Paint reflects perceived page-load speed.')
    const reportLink = container.querySelector('a[href="/api/v1/tests/test-1/report"]') as HTMLAnchorElement | null
    expect(reportLink).toBeTruthy()
    expect(reportLink?.textContent).toContain('Download Report')
    const browserDiagnosticsButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Download Browser Diagnostics ZIP'))
    expect(browserDiagnosticsButton).toBeTruthy()
    await act(async () => {
      browserDiagnosticsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onDownloadBrowserDiagnostics).toHaveBeenCalledTimes(1)
    const engineerJSONLButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Engineer JSONL'))
    expect(engineerJSONLButton).toBeTruthy()
    await act(async () => {
      engineerJSONLButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onDownloadArchiveArtifact).toHaveBeenCalledWith('transactions.jsonl')
    expect(container.textContent).toContain('Browser Device Breakdown (LCP / INP / CLS)')
    expect(container.textContent).toContain('Avg FCP')
    expect(container.textContent).toContain('Engine Rollup')
    expect(container.textContent).toContain('Platform Rollup')
    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('WebKit')
    expect(container.textContent).toContain('1 samples')
    expect(container.textContent).toContain('iOS')
    expect(container.textContent).toContain('iphone_se')
    expect(container.textContent).toContain('iOS · WebKit · low tier')
    const archiveDeviceTierTexts = Array.from(container.querySelectorAll('.archive-browser-section .browser-device-tier')).map(node => node.textContent)
    expect(archiveDeviceTierTexts).toEqual(['1 samples', 'WebKit · 1 samples', 'iOS · WebKit · low tier'])
    expect(container.textContent).toContain('Client Metrics')
    expect(container.textContent).toContain('iphone_se · iOS · WebKit · low tier')
    expect(container.textContent).toContain('Navigation Waterfall')
    expect(container.textContent).toContain('DOM content loaded: 180.0 ms')
    expect(container.textContent).toContain('DOM complete: 320.0 ms')
    expect(container.textContent).toContain('Load event: 340.0 ms')
    expect(container.textContent).toContain('Resources: 14 total / 2 blocking')
    expect(container.textContent).toContain('Transfer: 128.50 KB')
    expect(container.textContent).toContain('DOM completion gap: 140.0 ms')
    expect(container.textContent).toContain('Load-event gap: 20.0 ms')
    expect(container.textContent).toContain('Blocking-resource pressure: 14.3%')
    expect(container.textContent).toContain('Browser Resource Waterfall')
    expect(container.textContent).toContain('Slowest browser sub-resources captured from archived network entries.')
    expect(container.textContent).toContain('example.test/assets/app.js')
    expect(container.textContent).toContain('script')
    expect(container.textContent).toContain('45.0 ms')
    expect(container.textContent).toContain('2.00 KB')
    const detailValueTexts = Array.from(container.querySelectorAll('.scenario-script-detail-value')).map(node => node.textContent)
    expect(detailValueTexts).toContain('iphone_se · iOS · WebKit · low tier')
    expect(detailValueTexts).not.toContain('iphone_se · WebKit · low tier')
    expect(detailValueTexts).not.toContain('POST https://soap.example.test/orders')
    expect(detailValueTexts).not.toContain('200')
    expect(container.textContent).toContain('Request line and headers')
    expect(container.textContent).toContain('Request body')
    expect(container.textContent).toContain('Response status and headers')
    expect(container.textContent).toContain('Response body')
    const requestPreview = Array.from(container.querySelectorAll('pre.archive-event-detail-pre')).find(node =>
      node.textContent?.includes('POST https://soap.example.test/orders') &&
      node.textContent?.includes('Content-Type: application/soap+xml'),
    )
    expect(requestPreview).toBeDefined()
    const responsePreview = Array.from(container.querySelectorAll('pre.archive-event-detail-pre')).find(node =>
      node.textContent?.includes('Status: 200') &&
      node.textContent?.includes('Content-Type: application/soap+xml'),
    )
    expect(responsePreview).toBeDefined()
    expect(container.textContent).toContain('INP: 56.0 ms')
    expect(container.textContent).toContain('Response')
    const overallBreakdownButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Overall')
    const platformBreakdownButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Platform')
    const engineBreakdownButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Engine')
    expect(overallBreakdownButton).toBeDefined()
    expect(platformBreakdownButton).toBeDefined()
    expect(engineBreakdownButton).toBeDefined()
    if (!overallBreakdownButton || !platformBreakdownButton || !engineBreakdownButton) {
      return
    }

    await act(async () => {
      overallBreakdownButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Overview Breakdown (LCP / INP / CLS)')

    await act(async () => {
      engineBreakdownButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Engine Breakdown (LCP / INP / CLS)')

    await act(async () => {
      platformBreakdownButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Browser Platform Breakdown (LCP / INP / CLS)')
    expect(container.textContent).toContain('Series: LCP and INP on the left axis, CLS on the right axis.')
    const copyFaultButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy fault'))
    const copyRequestButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy full request'))
    const copyResponseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy full response'))
    expect(copyFaultButton).toBeDefined()
    expect(copyRequestButton).toBeDefined()
    expect(copyResponseButton).toBeDefined()
    if (!copyFaultButton || !copyRequestButton || !copyResponseButton) {
      return
    }

    await act(async () => {
      copyFaultButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(formatSOAPFaultSummary({
      version: '1.2',
      code: 'env:Sender',
      reason: 'Request validation failed',
    }))

    await act(async () => {
      copyRequestButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(formatSnapshotRequestSection({
      request_method: 'POST',
      request_url: 'https://soap.example.test/orders',
      request_headers: {
        'Content-Type': 'application/soap+xml',
        ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Debug-${index}`, `value-${index}`])),
      },
      request_body: '<CreateOrder />',
    }))

    await act(async () => {
      copyResponseButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenLastCalledWith(formatSnapshotResponseSection({
      response_status: 200,
      response_headers: {
        'Content-Type': 'application/soap+xml',
        ...Object.fromEntries(Array.from({ length: 12 }, (_, index) => [`X-Trace-${index}`, `trace-${index}`])),
      },
      response_body: '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>',
    }, 200))
    expect(container.textContent).toContain('Expand snapshots')
    expect(container.textContent).toContain('Open full text')
    expect(container.textContent).not.toContain('X-Debug-11')
    expect(container.textContent).not.toContain('X-Trace-11')
    expect(container.querySelectorAll('.snapshot-code-token-tag').length).toBeGreaterThan(0)
    expect(container.textContent).toContain('env:Body')
    expect(container.textContent).toContain('env:Fault')

    const expandSnapshotsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Expand snapshots'))
    expect(expandSnapshotsButton).toBeDefined()
    if (!expandSnapshotsButton) {
      return
    }

    await act(async () => {
      expandSnapshotsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Collapse snapshots')
    expect(container.textContent).toContain('X-Debug-11')
    expect(container.textContent).toContain('X-Trace-11')
  })

  it('renders transaction drill-down column sort and filter controls', async () => {
    const onSort = vi.fn()
    const onFilterChange = vi.fn()
    const onFiltersReset = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionSort={{ key: 'duration', direction: 'desc' }}
          onArchiveTransactionSortChange={onSort}
          archiveTransactionFilters={{
            timeFrom: '',
            timeTo: '',
            status: '500',
            minDurationMs: '100',
            maxDurationMs: '',
            minBytes: '',
            maxBytes: '',
            errorContains: 'timeout',
          }}
          archiveTransactionFiltersActive={true}
          onArchiveTransactionFilterChange={onFilterChange}
          onArchiveTransactionFiltersReset={onFiltersReset}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={1}
          archivePageStart={1}
          archivePageEnd={1}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[{
            timestamp: '2026-05-14T20:00:30Z',
            test_id: 'test-1',
            test_name: 'SOAP Fault Test',
            action_name: 'CreateOrder',
            duration_ms: 120,
            bytes_received: 256,
            status_code: 500,
            success: false,
            error_message: 'upstream timeout',
          }]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    const statusFilter = container.querySelector('input[aria-label="Filter transactions by status"]') as HTMLInputElement | null
    expect(statusFilter?.value).toBe('500')

    const durationSort = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Duration'))
    expect(durationSort).toBeDefined()
    if (!durationSort || !statusFilter) return

    await act(async () => {
      durationSort.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(statusFilter, '404')
      statusFilter.dispatchEvent(new Event('input', { bubbles: true }))
      const clearButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Clear')
      clearButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(onSort).toHaveBeenCalledWith('duration')
    expect(onFilterChange).toHaveBeenCalledWith('status', '404')
    expect(onFiltersReset).toHaveBeenCalled()
  })

  it('lets standalone action summary use the full archive card width', async () => {
    await act(async () => {
      root.render(
        <TestExecutionArchive
          view="action-summary"
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[
            ['CreateOrder', { transactions: 20, errors: 1, error_pct: 5, avg_response_ms: 140, min_response_ms: 40, max_response_ms: 340 }],
          ]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={0}
          archivePageStart={0}
          archivePageEnd={0}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    const layout = container.querySelector('.archive-report-layout')
    expect(layout?.classList.contains('single-view')).toBe(true)
    expect(container.querySelector('.archive-action-section .tx-table')).toBeTruthy()
    expect(container.textContent).toContain('CreateOrder')
  })

  it('renders mixed engine and platform browser rollups from the archive report', async () => {
    const mixedReport: RunArchiveReport = {
      ...archiveReport,
      overview: {
        ...archiveReport.overview,
        browser: {
          samples: 3,
          avg_fcp_ms: 122,
          avg_lcp_ms: 230,
          avg_cls_score: 0.025,
          avg_inp_ms: 52,
          avg_long_task_count: 1,
          avg_blocking_resources: 2,
        },
      },
      browser_by_engine: {
        chromium: {
          browser_engine: 'chromium',
          samples: 1,
          avg_fcp_ms: 105,
          avg_lcp_ms: 180,
          avg_cls_score: 0.014,
          avg_inp_ms: 41,
        },
        webkit: {
          browser_engine: 'webkit',
          samples: 2,
          avg_fcp_ms: 130,
          avg_lcp_ms: 255,
          avg_cls_score: 0.031,
          avg_inp_ms: 56,
        },
      },
      browser_by_platform: {
        android: {
          device_platform: 'android',
          browser_engine: 'chromium',
          samples: 1,
          avg_fcp_ms: 105,
          avg_lcp_ms: 180,
          avg_cls_score: 0.014,
          avg_inp_ms: 41,
        },
        ios: {
          device_platform: 'ios',
          browser_engine: 'webkit',
          samples: 2,
          avg_fcp_ms: 130,
          avg_lcp_ms: 255,
          avg_cls_score: 0.031,
          avg_inp_ms: 56,
        },
      },
      browser_by_device: {
        iphone_se: archiveReport.browser_by_device!.iphone_se,
        pixel_8: {
          device_profile: 'pixel_8',
          device_platform: 'android',
          device_tier: 'high',
          browser_engine: 'chromium',
          samples: 1,
          avg_fcp_ms: 105,
          avg_lcp_ms: 180,
          avg_cls_score: 0.014,
          avg_inp_ms: 41,
        },
      },
    }

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={mixedReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={0}
          archivePageStart={0}
          archivePageEnd={0}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('Engine Rollup')
    expect(container.textContent).toContain('Platform Rollup')
    expect(container.textContent).toContain('Devices')
    expect(container.textContent).toContain('Browser Device Breakdown (LCP / INP / CLS)')
    expect(container.textContent).toContain('Series: LCP and INP on the left axis, CLS on the right axis.')

    const browserNames = Array.from(container.querySelectorAll('.archive-browser-section .browser-device-name')).map(node => node.textContent)
    expect(browserNames).toEqual(['Chromium', 'WebKit', 'Android', 'iOS', 'iphone_se', 'pixel_8'])

    const browserTiers = Array.from(container.querySelectorAll('.archive-browser-section .browser-device-tier')).map(node => node.textContent)
    expect(browserTiers).toEqual([
      '1 samples',
      '2 samples',
      'Chromium · 1 samples',
      'WebKit · 2 samples',
      'iOS · WebKit · low tier',
      'Android · Chromium · high tier',
    ])

    expect(container.textContent).toContain('180.0 ms')
    expect(container.textContent).toContain('255.0 ms')
  })

  it('surfaces degraded browser UX signals from archive metrics', async () => {
    const degradedReport: RunArchiveReport = {
      ...archiveReport,
      overview: {
        ...archiveReport.overview,
        browser: {
          samples: 4,
          avg_fcp_ms: 2800,
          avg_lcp_ms: 4300,
          avg_cls_score: 0.18,
          avg_inp_ms: 320,
          avg_long_task_count: 4,
          avg_blocking_resources: 12,
          avg_browser_process_cpu_pct: 74,
          avg_browser_process_mem_mb: 264,
        },
      },
      browser_by_device: {
        iphone_se: {
          device_profile: 'iphone_se',
          device_platform: 'ios',
          device_tier: 'low',
          browser_engine: 'webkit',
          samples: 2,
          avg_lcp_ms: 4400,
          avg_cls_score: 0.2,
          avg_inp_ms: 520,
        },
        pixel_8: {
          device_profile: 'pixel_8',
          device_platform: 'android',
          device_tier: 'high',
          browser_engine: 'chromium',
          samples: 2,
          avg_lcp_ms: 2100,
          avg_cls_score: 0.02,
          avg_inp_ms: 120,
        },
      },
    }

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={degradedReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={0}
          archivePageStart={0}
          archivePageEnd={0}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('UX Intelligence')
    expect(container.textContent).toContain('Poor ·')
    expect(container.textContent).toContain('UX signals need attention; weakest device is iphone_se.')
    expect(container.textContent).toContain('LCP')
    expect(container.textContent).toContain('4300.0 ms')
    expect(container.textContent).toContain('Blocking resources')
    expect(container.textContent).toContain('12.0')
    expect(container.textContent).toContain('Browser RSS')
    expect(container.textContent).toContain('264.0 MB')
  })

  it('shows server telemetry for archived events without requiring a snapshot', async () => {
    const transactions: TransactionEvent[] = [{
      timestamp: '2026-05-14T20:00:40Z',
      test_id: 'test-1',
      test_name: 'SOAP Fault Test',
      group: 'Checkout',
      action_name: 'POST /checkout',
      duration_ms: 180,
      status_code: 200,
      success: true,
      bytes_received: 4096,
      server_telemetry: {
        server_processing_ms: 48,
        transfer_ms: 14,
        serialization_ms: 3,
        deserialization_ms: 2,
        request_payload_kb: 1.25,
        request_size_class: 'small',
        response_payload_kb: 4,
        response_size_class: 'small',
        envelope_overhead_kb: 0.75,
        soap_fault_code: 'soapenv:Client',
        soap_fault_category: 'client',
        soap_namespace_conflict: 'undeclared prefixes: auth',
        redirect_count: 2,
        redirect_ms: 31,
        message_latency_ms: 18,
        messages_sent: 1,
        messages_received: 1,
        messages_sent_per_sec: 6.4,
        messages_received_per_sec: 5.8,
        consumer_lag_ms: 22,
        connection_reused: false,
      },
    }]

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={1}
          archivePageStart={1}
          archivePageEnd={1}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={transactions}
          expandedArchiveEvent="2026-05-14T20:00:40Z-0"
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('Server Telemetry')
    expect(container.textContent).toContain('Server processing: 48.0 ms')
    expect(container.textContent).toContain('Transfer: 14.0 ms')
    expect(container.textContent).toContain('Serialization: 3.0 ms')
    expect(container.textContent).toContain('Deserialization: 2.0 ms')
    expect(container.textContent).toContain('Request payload: 1.25 KB')
    expect(container.textContent).toContain('Request size class: small')
    expect(container.textContent).toContain('Response payload: 4.00 KB')
    expect(container.textContent).toContain('Response size class: small')
    expect(container.textContent).toContain('Envelope overhead: 0.75 KB')
    expect(container.textContent).toContain('SOAP fault: soapenv:Client / client')

    const serverTelemetryCard = Array.from(container.querySelectorAll('.archive-event-detail-card')).find(card => card.textContent?.includes('Server Telemetry'))
    expect(serverTelemetryCard).toBeDefined()
    const copyTelemetryButton = Array.from(serverTelemetryCard?.querySelectorAll('button') ?? []).find(button => button.textContent?.includes('Copy'))
    const openTelemetryButton = Array.from(serverTelemetryCard?.querySelectorAll('button') ?? []).find(button => button.textContent?.includes('Open full text'))
    expect(copyTelemetryButton).toBeDefined()
    expect(openTelemetryButton).toBeDefined()
    if (!copyTelemetryButton || !openTelemetryButton) return

    await act(async () => {
      copyTelemetryButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('Server processing: 48.0 ms'))

    await act(async () => {
      openTelemetryButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    const dialog = document.body.querySelector('[role="dialog"][aria-label="Full text"]')
    expect(dialog?.textContent).toContain('SOAP namespace conflict: undeclared prefixes: auth')
    expect(dialog?.textContent).toContain('Redirects: 2 hops / 31.0 ms')
    expect(dialog?.textContent).toContain('Message latency: 18.0 ms')
    expect(dialog?.textContent).toContain('Messages: 1 sent / 1 received')
    expect(dialog?.textContent).toContain('Message throughput: 6.4 sent/s / 5.8 received/s')
    expect(dialog?.textContent).toContain('Consumer lag: 22.0 ms')
    expect(dialog?.textContent).toContain('Connection: new')
  })

  it('shows native protocol evidence for archived events without requiring HTTP snapshots', async () => {
    const transactions: TransactionEvent[] = [{
      timestamp: '2026-05-14T20:00:45Z',
      test_id: 'test-1',
      test_name: 'Native Evidence Test',
      group: 'Database',
      action_name: 'Lookup User',
      dependency_type: 'database',
      dependency_target: 'sqlite',
      duration_ms: 32,
      status_code: 500,
      success: false,
      error_message: 'row_count assertion failed',
      bytes_received: 64,
      protocol_evidence: {
        protocol: 'database',
        operation: 'db_query',
        target: 'sqlite',
        status: 'failed',
        rows: {
          row_count: 0,
          rows: [],
        },
        metadata: {
          error: 'row_count assertion failed',
        },
      },
    }]

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={[]}
          archiveSOAPOperationEntries={[]}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={1}
          archivePageStart={1}
          archivePageEnd={1}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={transactions}
          expandedArchiveEvent="2026-05-14T20:00:45Z-0"
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('Protocol Evidence')
    expect(container.textContent).toContain('database / db_query / failed / sqlite')
    expect(container.textContent).toContain('row_count')
    expect(container.textContent).toContain('row_count assertion failed')
  })

  it('renders SOAP operation archive summaries when present', async () => {
    const operationReport: RunArchiveReport = {
      ...archiveReport,
      by_action: {
        SOAPRequest: {
          transactions: 3,
          errors: 1,
          error_pct: 33.33,
          avg_response_ms: 120,
          min_response_ms: 80,
          max_response_ms: 180,
        },
      },
      by_soap_operation: {
        CreateOrder: {
          transactions: 2,
          errors: 0,
          error_pct: 0,
          avg_response_ms: 95,
          min_response_ms: 80,
          max_response_ms: 110,
        },
        CancelOrder: {
          transactions: 1,
          errors: 1,
          error_pct: 100,
          avg_response_ms: 170,
          min_response_ms: 170,
          max_response_ms: 170,
        },
      },
      by_soap_fault_code: {
        'env:Sender': 2,
        'env:Receiver': 1,
      },
      by_soap_fault_category: {
        client: 2,
        server: 1,
      },
      soap_fault_timeline: [
        {
          timestamp: '2026-05-14T20:00:00Z',
          total: 2,
          by_code: {
            'env:Sender': 2,
          },
          by_category: {
            client: 2,
          },
        },
        {
          timestamp: '2026-05-14T20:01:00Z',
          total: 1,
          by_code: {
            'env:Receiver': 1,
          },
          by_category: {
            server: 1,
          },
        },
      ],
      soap_payload_timeline: [
        {
          timestamp: '2026-05-14T20:00:00Z',
          samples: 2,
          avg_request_payload_kb: 1.5,
          avg_response_payload_kb: 5,
          avg_envelope_overhead_kb: 0.8,
          max_envelope_overhead_kb: 0.9,
        },
        {
          timestamp: '2026-05-14T20:01:00Z',
          samples: 1,
          avg_request_payload_kb: 2,
          avg_response_payload_kb: 8,
          avg_envelope_overhead_kb: 1.5,
          max_envelope_overhead_kb: 1.5,
        },
      ],
    }

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={operationReport}
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={Object.entries(operationReport.by_action)}
          archiveSOAPOperationEntries={Object.entries(operationReport.by_soap_operation || {})}
          selectedArchiveAction="SOAPRequest"
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation="CreateOrder"
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={0}
          archivePageStart={0}
          archivePageEnd={0}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('SOAP Operation Summary')
    expect(container.textContent).toContain('SOAP Fault Summary')
    expect(container.textContent).toContain('SOAP Fault Trend')
    expect(container.textContent).toContain('SOAP Payload Trend')
    expect(container.textContent).toContain('CreateOrder')
    expect(container.textContent).toContain('CancelOrder')
    expect(container.textContent).toContain('All SOAP operations')
    expect(container.textContent).toContain('env:Sender')
    expect(container.textContent).toContain('env:Receiver')
    expect(container.textContent).toContain('client')
    expect(container.textContent).toContain('server')
    expect(container.textContent).toContain('2026-05-14 20:00Z')
    expect(container.textContent).toContain('2026-05-14 20:01Z')
    expect(container.textContent).toContain('env:Sender (2)')
    expect(container.textContent).toContain('env:Receiver (1)')
    expect(container.textContent).toContain('1.50 KB')
    expect(container.textContent).toContain('5.00 KB')
    expect(container.textContent).toContain('0.80 KB')
    expect(container.textContent).toContain('1.50 KB')
    expect(container.textContent).toContain('95.0')
    expect(container.textContent).toContain('170.0')
  })

  it('renders archive report sections as standalone views', async () => {
    await renderArchiveView('report-overview')

    expect(container.textContent).toContain('Peak TPS')
    expect(container.textContent).toContain('Goal Completion')
    expect(container.textContent).not.toContain('Error Patterns')

    await renderArchiveView('report-error-patterns')

    expect(container.textContent).toContain('Error Patterns')
    expect(container.textContent).toContain('soap fault: Request validation failed for order 1001')
    expect(container.textContent).not.toContain('Peak TPS')

    await renderArchiveView('report-assets')

    expect(container.textContent).toContain('Download Report')
    expect(container.textContent).toContain('Download Transactions JSONL')
    expect(container.textContent).not.toContain('Error Patterns')
  })

  async function renderArchiveView(view: Parameters<typeof TestExecutionArchive>[0]['view']) {
    await act(async () => {
      root.render(
        <TestExecutionArchive
          view={view}
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={archiveReport}
          archiveAnalysis={archiveAnalysis}
          archiveAnalysisLoading={false}
          archiveAnalysisError=""
          onDownloadBrowserDiagnostics={vi.fn()}
          onDownloadArchiveArtifact={vi.fn()}
          archiveActionEntries={Object.entries(archiveReport.by_action)}
          archiveSOAPOperationEntries={Object.entries(archiveReport.by_soap_operation || {})}
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
          selectedArchiveSOAPOperation=""
          onSelectedArchiveSOAPOperationChange={vi.fn()}
          archiveGroups={[]}
          selectedArchiveGroup=""
          onSelectedArchiveGroupChange={vi.fn()}
          archiveErrorsOnly={false}
          onArchiveErrorsOnlyChange={vi.fn()}
          archiveTransactionsLoading={false}
          archiveTransactionsTotal={0}
          archivePageStart={0}
          archivePageEnd={0}
          archiveTransactionsOffset={0}
          archivePageSize={50}
          onPageSizeChange={vi.fn()}
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={[]}
          expandedArchiveEvent={null}
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })
  }
})
