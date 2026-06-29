// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ReportComposer from './ReportComposer'

const apiMock = vi.hoisted(() => ({
  getTestArchiveReport: vi.fn(),
  getTestReportAnnotations: vi.fn(),
  listTestNotes: vi.fn(),
  saveTestReportAnnotations: vi.fn(),
  downloadTestReport: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./ReportCharts', () => ({
  default: () => <div>Report charts</div>,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ReportComposer test notes source panel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getTestArchiveReport.mockResolvedValue(sampleReport())
    apiMock.getTestReportAnnotations.mockResolvedValue({
      sections: [{ id: 'overview', label: 'Test Overview' }],
      annotations: {
        test_id: 'test-1',
        profile: 'executive',
        title: 'Initial report',
        prepared_by: '',
        executive_narrative: 'Existing summary',
        section_comments: { overview: 'Existing overview' },
        verdict_override: '',
        verdict_explanation: '',
        findings: [],
        updated_at: '2026-06-24T12:00:00Z',
        updated_by: 'Ava',
      },
    })
    apiMock.listTestNotes.mockResolvedValue({
      test_id: 'test-1',
      notes: [{
        id: 'note-1',
        test_id: 'test-1',
        body: '# Analyst note\n\nObserved **spike** during checkout.',
        created_at: '2026-06-24T12:00:00Z',
        updated_at: '2026-06-24T12:05:00Z',
      }],
    })
    apiMock.saveTestReportAnnotations.mockImplementation(async (_testId: string, _profile: string, data: any) => ({
      sections: [{ id: 'overview', label: 'Test Overview' }],
      annotations: {
        test_id: 'test-1',
        profile: 'executive',
        updated_at: '2026-06-24T12:10:00Z',
        updated_by: 'Ava',
        ...data,
      },
    }))
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('inserts selected notes into summary, commentary, and info findings', async () => {
    await renderComposer()

    expect(container.textContent).toContain('Analyst note')
    expect(container.querySelector('.report-note-source-markdown strong')?.textContent).toBe('spike')

    await click(button('Append to Executive Summary'))
    expect((container.querySelector('.report-narrative') as HTMLTextAreaElement).value).toContain('Observed **spike**')

    await click(button('Append to Section'))
    expect(Array.from(container.querySelectorAll('textarea')).some((textarea) => textarea.value.includes('Existing overview') && textarea.value.includes('Observed **spike**'))).toBe(true)

    await click(button('Convert to Info Finding'))
    expect((container.querySelector('.report-finding-title') as HTMLInputElement).value).toBe('Analyst note')

    await click(button('Save'))
    await flush()

    expect(apiMock.saveTestReportAnnotations).toHaveBeenCalledTimes(1)
    const payload = apiMock.saveTestReportAnnotations.mock.calls[0][2]
    expect(payload.executive_narrative).toContain('Observed **spike**')
    expect(payload.section_comments.overview).toContain('Observed **spike**')
    expect(payload.findings[0]).toMatchObject({
      title: 'Analyst note',
      severity: 'info',
      observed: '# Analyst note\n\nObserved **spike** during checkout.',
    })
  })

  it('shows an empty notes source state', async () => {
    apiMock.listTestNotes.mockResolvedValue({ test_id: 'test-1', notes: [] })

    await renderComposer()

    expect(container.textContent).toContain('No test notes are available for this run.')
  })

  it('shows a notes source load failure without failing the report load', async () => {
    apiMock.listTestNotes.mockRejectedValue(new Error('notes unavailable'))

    await renderComposer()

    expect(container.textContent).toContain('Report charts')
    expect(container.textContent).toContain('notes unavailable')
  })

  it('renders audit evidence without engineering drilldowns', async () => {
    apiMock.getTestReportAnnotations.mockResolvedValueOnce({
      sections: [{ id: 'audit_scope', label: 'Audit Scope' }],
      annotations: {
        test_id: 'test-1',
        profile: 'audit',
        title: 'Audit report',
        prepared_by: '',
        executive_narrative: 'Audit evidence narrative',
        section_comments: { audit_scope: 'Review scope and evidence.' },
        verdict_override: '',
        verdict_explanation: '',
        findings: [],
        updated_at: '2026-06-24T12:00:00Z',
        updated_by: 'Ava',
      },
    })

    await renderComposer('audit')

    expect(apiMock.getTestReportAnnotations).toHaveBeenCalledWith('test-1', 'audit')
    expect(container.textContent).toContain('Audit Evidence')
    expect(container.textContent).toContain('Audit Summary')
    expect(container.textContent).toContain('Creator User (user:creator)')
    expect(container.textContent).toContain('Stopper User (user:stopper)')
    expect(container.textContent).toContain('approved · user:approver · release gate approved')
    expect(container.textContent).toContain('Artifact Integrity')
    expect(container.textContent).toContain('transactions.jsonl')
    expect(container.textContent).toContain('sha-transaction')
    expect(container.textContent).not.toContain('Report charts')
    expect(container.textContent).not.toContain('Action Summary')
    expect(container.textContent).not.toContain('Network Phase Breakdown')
  })

  async function renderComposer(profile: 'executive' | 'audit' = 'executive') {
    await act(async () => {
      root.render(<ReportComposer testId="test-1" testName="Checkout Run" profile={profile} onClose={() => {}} />)
    })
    await flush()
  }

  function button(label: string) {
    const match = Array.from(container.querySelectorAll('button')).find((item) => item.textContent?.trim() === label)
    if (!match) throw new Error(`button not found: ${label}`)
    return match
  }
})

async function click(element: Element) {
  await act(async () => {
    element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function flush() {
  await act(async () => {
    await Promise.resolve()
  })
}

function sampleReport() {
  return {
    schema_version: 'apexload.archive_report.v1',
    manifest: {
      schema_version: 'apexload.archive.v1',
      test_id: 'test-1',
      test_name: 'Checkout Run',
      created_at: '2026-06-24T12:00:00Z',
      completed_at: '2026-06-24T12:05:00Z',
      status: 'COMPLETE',
      transactions_file: 'transactions.jsonl',
      resource_samples_file: 'resource_samples.jsonl',
      interval_summaries_file: 'interval_summaries.jsonl',
      artifacts: [{
        name: 'transactions.jsonl',
        kind: 'evidence',
        format: 'jsonl',
        record_type: 'transactions',
        count: 10,
        size_bytes: 2048,
        sha256: 'sha-transaction',
      }],
      provenance: {
        created_by_subject: 'user:creator',
        created_by_display_name: 'Creator User',
        started_by_subject: 'user:starter',
        started_by_display_name: 'Starter User',
        stopped_by_subject: 'user:stopper',
        stopped_by_display_name: 'Stopper User',
        governance: {
          execution_plan_approval: {
            id: 'approval-1',
            plan_id: 'plan-1',
            policy: 'quota_burst',
            status: 'approved',
            requested_by: 'user:requester',
            resolved_by: 'user:approver',
            decision_reason: 'release gate approved',
            required_role: 'project-admin',
          },
        },
      },
      transaction_count: 10,
      resource_sample_count: 2,
      interval_summary_count: 2,
      sla_breached: false,
    },
    overview: {
      transactions: 10,
      errors: 0,
      error_pct: 0,
      avg_response_ms: 120,
      peak_tps: 3,
      peak_active_vusers: 5,
    },
    by_action: {
      Checkout: {
        transactions: 10,
        errors: 0,
        error_pct: 0,
        avg_response_ms: 120,
        max_response_ms: 220,
      },
    },
    network_phases: {
      samples: 10,
      overall: {
        count: 10,
        avg_dns_ms: 1,
        avg_connect_ms: 2,
        avg_tls_ms: 3,
        avg_ttfb_ms: 40,
      },
    },
    resource_peaks: {},
    compatibility: {
      status: 'compatible',
      report_schema_version: 'apexload.archive_report.v1',
      manifest_schema_version: 'apexload.archive.v2',
      supported_manifest_schemas: ['apexload.archive.v1', 'apexload.archive.v2'],
      outputs: [],
    },
    completeness: {
      score_pct: 100,
      status: 'complete',
      findings: [],
    },
    change_attribution: {
      runtime_overrides: true,
      runtime_override_fields: ['log_level'],
    },
    artifact_inventory: [{
      name: 'transactions.jsonl',
      kind: 'evidence',
      format: 'jsonl',
      record_type: 'transactions',
      count: 10,
      size_bytes: 2048,
      sha256: 'sha-transaction',
    }],
  }
}
