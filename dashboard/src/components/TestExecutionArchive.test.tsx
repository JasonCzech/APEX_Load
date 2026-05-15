// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveReport, TestSummary, TransactionEvent } from '../types'
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
    manifest: {
      schema_version: '1',
      test_id: 'test-1',
      test_name: 'SOAP Fault Test',
      created_at: '2026-05-14T20:00:00Z',
      completed_at: '2026-05-14T20:01:00Z',
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
      peak_active_vusers: 1,
      browser: {
        samples: 1,
        avg_fcp_ms: 130,
        avg_lcp_ms: 240,
        avg_cls_score: 0.031,
        avg_inp_ms: 56,
        avg_long_task_count: 1,
        avg_blocking_resources: 2,
      },
    },
    by_action: {},
    by_soap_operation: {},
    by_soap_fault_code: {},
    by_soap_fault_category: {},
    soap_fault_timeline: [],
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
    resource_peaks: {
      cpu_process_pct: 0,
      mem_process_mb: 0,
      net_sent_kbps: 0,
      net_recv_kbps: 0,
      disk_read_kbps: 0,
      disk_write_kbps: 0,
    },
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
        blocking_resource_count: 2,
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
          onPreviousPage={vi.fn()}
          onNextPage={vi.fn()}
          archiveTransactions={transactions}
          expandedArchiveEvent="2026-05-14T20:00:30Z-0"
          onExpandedArchiveEventChange={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('SOAP Fault')
    expect(container.textContent).toContain('SOAP 1.2')
    expect(container.textContent).toContain('Code: env:Sender')
    expect(container.textContent).toContain('Reason: Request validation failed')
    expect(container.textContent).toContain('Browser Metrics')
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
    const detailValueTexts = Array.from(container.querySelectorAll('.scenario-script-detail-value')).map(node => node.textContent)
    expect(detailValueTexts).toContain('iphone_se · iOS · WebKit · low tier')
    expect(detailValueTexts).not.toContain('iphone_se · WebKit · low tier')
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
    const copyRequestButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy request'))
    const copyResponseButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy response'))
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
    expect(container.textContent).toContain('Show full text')
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
    expect(container.textContent).toContain('SOAP namespace conflict: undeclared prefixes: auth')
    expect(container.textContent).toContain('Redirects: 2 hops / 31.0 ms')
    expect(container.textContent).toContain('Message latency: 18.0 ms')
    expect(container.textContent).toContain('Messages: 1 sent / 1 received')
    expect(container.textContent).toContain('Message throughput: 6.4 sent/s / 5.8 received/s')
    expect(container.textContent).toContain('Consumer lag: 22.0 ms')
    expect(container.textContent).toContain('Connection: new')
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
    }

    await act(async () => {
      root.render(
        <TestExecutionArchive
          hasArchive={true}
          test={test}
          archiveError=""
          archiveLoading={false}
          archiveReport={operationReport}
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
    expect(container.textContent).toContain('95.0')
    expect(container.textContent).toContain('170.0')
  })
})