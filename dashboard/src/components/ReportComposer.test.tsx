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
    await act(async () => {
      root.render(<ReportComposer testId="test-1" testName="Checkout Run" profile="executive" onClose={() => {}} />)
    })
    await flush()

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
    resource_peaks: {},
  }
}
