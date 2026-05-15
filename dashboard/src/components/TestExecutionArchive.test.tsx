// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveReport, TestSummary, TransactionEvent } from '../types'
import { formatSnapshotRequestSection, formatSnapshotResponseSection } from '../utils/snapshotFormatUtils'
import { formatSOAPFaultSummary } from './soapFaultUtils'
import TestExecutionArchive from './TestExecutionArchive'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
          selectedArchiveAction=""
          onSelectedArchiveActionChange={vi.fn()}
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
    expect(container.textContent).toContain('Avg FCP')
    expect(container.textContent).toContain('iphone_se')
    expect(container.textContent).toContain('WebKit · low tier')
    expect(container.textContent).toContain('iOS · WebKit · low tier')
    expect(container.textContent).toContain('Client Metrics')
    expect(container.textContent).toContain('iphone_se · WebKit · low tier')
    expect(container.textContent).toContain('iphone_se · iOS · WebKit · low tier')
    expect(container.textContent).toContain('INP: 56.0 ms')
    expect(container.textContent).toContain('Response')
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
})