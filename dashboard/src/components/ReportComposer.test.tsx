// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ReportComposer from './ReportComposer'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

const apiMock = vi.hoisted(() => ({
  getTestArchiveReport: vi.fn(),
  getTestReportAnnotations: vi.fn(),
  listTestNotes: vi.fn(),
  saveTestReportAnnotations: vi.fn(),
  downloadTestReport: vi.fn(),
  verifyTestArchive: vi.fn(),
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
    apiMock.verifyTestArchive.mockResolvedValue(null)
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

  it('announces loading and load failures to assistive technology', async () => {
    apiMock.getTestArchiveReport.mockReturnValueOnce(new Promise(() => {}))
    await act(async () => {
      root.render(<ReportComposer testId="test-1" testName="Checkout Run" profile="executive" onClose={() => {}} />)
    })

    const loadingStatus = container.querySelector('[role="status"]')
    expect(loadingStatus?.getAttribute('aria-live')).toBe('polite')
    expect(loadingStatus?.textContent).toContain('Loading report')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    apiMock.getTestArchiveReport.mockRejectedValueOnce(new Error('Archive report unavailable'))
    await renderComposer()

    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Archive report unavailable')
  })

  it('focuses the composer and confirms before discarding unsaved report edits', async () => {
    const onClose = vi.fn()
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <ReportComposer testId="test-1" testName="Checkout Run" profile="executive" onClose={onClose} />
        </NavigationGuardProvider>,
      )
    })
    await flush()
    await flushFocus()

    const composer = container.querySelector<HTMLElement>('.report-composer-overlay[role="dialog"]')
    const closeButton = button('Close')
    expect(composer?.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(closeButton)

    const titleInput = container.querySelector<HTMLInputElement>('[aria-label="Report title"]')
    expect(titleInput).not.toBeNull()
    await act(async () => {
      setInputValue(titleInput!, 'Changed report title')
    })
    await click(closeButton)
    await flushFocus()

    expect(onClose).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Unsaved Changes')
    expect(document.activeElement?.textContent).toContain('Keep editing')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.textContent).not.toContain('Unsaved Changes')
    expect(document.activeElement).toBe(closeButton)

    await click(closeButton)
    await click(button('Discard changes'))
    expect(onClose).toHaveBeenCalledOnce()
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

  it('renders a passing integrity banner when the archive verifies', async () => {
    apiMock.getTestReportAnnotations.mockResolvedValueOnce(auditAnnotations())
    apiMock.verifyTestArchive.mockResolvedValueOnce({
      schema_version: 'apexload.archive_integrity.v1',
      test_id: 'test-1',
      status: 'verified',
      verified: true,
      verified_at: '2026-06-24T12:06:00Z',
      summary: 'All 1 artifacts verified against their recorded SHA-256.',
      total: 1,
      matched: 1,
      mismatched: 0,
      missing: 0,
      unverifiable: 0,
      artifacts: [{ name: 'transactions.jsonl', status: 'match', expected_sha256: 'sha-transaction', actual_sha256: 'sha-transaction', size_bytes: 2048 }],
    })

    await renderComposer('audit')

    expect(apiMock.verifyTestArchive).toHaveBeenCalledWith('test-1')
    const banner = container.querySelector('.report-integrity-banner')
    expect(banner).not.toBeNull()
    expect(banner?.className).toContain('pass')
    expect(banner?.textContent).toContain('Integrity Verified')
    expect(banner?.textContent).toContain('All 1 artifacts verified')
    // The inventory row reflects the per-artifact verdict.
    expect(container.textContent).toContain('match')
  })

  it('renders a failing integrity banner when an artifact was tampered', async () => {
    apiMock.getTestReportAnnotations.mockResolvedValueOnce(auditAnnotations())
    apiMock.verifyTestArchive.mockResolvedValueOnce({
      schema_version: 'apexload.archive_integrity.v1',
      test_id: 'test-1',
      status: 'tampered',
      verified: false,
      verified_at: '2026-06-24T12:06:00Z',
      summary: '1 of 1 artifacts failed integrity verification — content no longer matches the recorded SHA-256 (tampered).',
      total: 1,
      matched: 0,
      mismatched: 1,
      missing: 0,
      unverifiable: 0,
      artifacts: [{ name: 'transactions.jsonl', status: 'mismatch', expected_sha256: 'sha-transaction', actual_sha256: 'sha-different', detail: 'recomputed SHA-256 does not match the manifest (artifact modified)' }],
    })

    await renderComposer('audit')

    const banner = container.querySelector('.report-integrity-banner')
    expect(banner).not.toBeNull()
    expect(banner?.className).toContain('fail')
    expect(banner?.textContent).toContain('Integrity tampered')
    expect(banner?.textContent).toContain('failed integrity verification')
    expect(container.textContent).toContain('mismatch')
  })

  it('omits the integrity banner for non-audit profiles', async () => {
    await renderComposer('executive')

    expect(apiMock.verifyTestArchive).not.toHaveBeenCalled()
    expect(container.querySelector('.report-integrity-banner')).toBeNull()
  })

  it('gates engineer sections on the profile included_sections', async () => {
    // Engineer report whose matching profile drops network_phases: the Action
    // Summary stays (still declared) while the Network Phase Breakdown disappears,
    // proving HTML visibility is driven by included_sections, not the id booleans.
    apiMock.getTestArchiveReport.mockResolvedValueOnce({
      ...sampleReport(),
      network_phases: { samples: 4, overall: { count: 4, avg_dns_ms: 2, avg_connect_ms: 3, avg_tls_ms: 4, avg_ttfb_ms: 5 } },
      report_profiles: [{
        id: 'engineer',
        label: 'Detailed Technical',
        description: '',
        export_formats: ['html'],
        included_sections: ['verdict', 'technical_overview', 'graphs', 'actions'],
        section_order: ['verdict', 'technical_overview', 'graphs', 'actions'],
      }],
    })
    apiMock.getTestReportAnnotations.mockResolvedValueOnce({
      sections: [{ id: 'overview', label: 'Overview' }],
      annotations: {
        test_id: 'test-1', profile: 'engineer', title: 'Eng', prepared_by: '',
        executive_narrative: '', section_comments: {}, verdict_override: '',
        verdict_explanation: '', findings: [], updated_at: '2026-06-24T12:00:00Z', updated_by: 'Ava',
      },
    })

    await renderComposer('engineer')

    expect(container.textContent).toContain('Action Summary')
    expect(container.textContent).not.toContain('Network Phase Breakdown')
  })

  it('renders the executive release trend comparison when a baseline exists', async () => {
    apiMock.getTestArchiveReport.mockResolvedValueOnce({
      ...sampleReport(),
      release_trend: releaseTrendWithBaseline(),
    })

    await renderComposer('executive')

    expect(container.textContent).toContain('Release Trend')
    expect(container.textContent).toContain('Prior Build') // baseline run name
    expect(container.textContent).toContain('+2.0 pp') // signed error-rate delta
    expect(container.textContent).toContain('Regressions vs prior run')
  })

  it('degrades the executive release trend to a note when there is no baseline', async () => {
    apiMock.getTestArchiveReport.mockResolvedValueOnce({
      ...sampleReport(),
      release_trend: {
        release_id: 'rel-9',
        run_count: 1,
        has_baseline: false,
        regressed: false,
        current: releaseTrendWithBaseline().current,
        trajectory: { latest_regressed: false, avg_response_trend_pct: 0, error_pct_trend: 0 },
        note: 'First archived run for this release; no prior run to compare against.',
      },
    })

    await renderComposer('executive')

    expect(container.textContent).toContain('Release Trend')
    expect(container.textContent).toContain('First archived run for this release')
    // No baseline => no comparison table header.
    expect(container.textContent).not.toContain('Prior run')
  })

  async function renderComposer(profile: 'executive' | 'engineer' | 'audit' = 'executive') {
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

async function flushFocus() {
  await act(async () => {
    await new Promise(resolve => window.setTimeout(resolve, 0))
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function auditAnnotations() {
  return {
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
  }
}

function releaseTrendWithBaseline() {
  return {
    release_id: 'rel-9',
    run_count: 2,
    has_baseline: true,
    regressed: true,
    regressions: ['error rate regressed'],
    current: {
      run_id: 'test-1', run_name: 'Checkout Run', status: 'COMPLETE', completed_at: '2026-06-24T12:05:00Z',
      transactions: 10, error_pct: 3, avg_response_ms: 130, peak_tps: 50, peak_active_vusers: 5,
      completeness_pct: 80, sla_breached: false,
    },
    baseline: {
      run_id: 'run-prev', run_name: 'Prior Build', status: 'COMPLETE', completed_at: '2026-06-23T12:05:00Z',
      transactions: 10, error_pct: 1, avg_response_ms: 100, peak_tps: 50, peak_active_vusers: 5,
      completeness_pct: 100, sla_breached: false,
    },
    deltas: {
      error_pct_delta: 2, avg_response_ms_delta: 30, avg_response_pct_delta: 30,
      peak_tps_delta: 0, completeness_delta: -20,
    },
    trajectory: { latest_regressed: true, latest_regressions: ['error rate regressed'], avg_response_trend_pct: 30, error_pct_trend: 2 },
  }
}

function sampleReport() {
  return {
    schema_version: 'apexload.archive_report.v1',
    // The real API always returns report_profiles; the composer gates preview
    // sections off the active profile's included_sections (single source of truth).
    report_profiles: [
      {
        id: 'executive',
        label: 'Executive Summary',
        included_sections: ['verdict', 'executive_overview', 'graphs', 'sla_rollup', 'release_trend', 'risk_summary', 'recommendations', 'signoff', 'completeness', 'change_attribution'],
      },
      {
        id: 'engineer',
        label: 'Detailed Technical',
        included_sections: ['verdict', 'technical_overview', 'sla_detail', 'graphs', 'root_cause', 'recommendations', 'test_setup', 'raw_exports', 'generator_manifest', 'completeness', 'schema_compatibility', 'change_attribution', 'summary_timeline', 'errors', 'resources', 'actions', 'browser', 'soap', 'network_phases', 'web2_migration'],
      },
      {
        id: 'audit',
        label: 'Audit',
        included_sections: ['official_outcome', 'audit_identity', 'authorization', 'governance', 'provenance', 'schema_compatibility', 'evidence_tiers', 'completeness', 'change_attribution', 'artifact_integrity', 'report_authorship'],
      },
    ],
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
