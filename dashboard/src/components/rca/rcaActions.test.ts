import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ReportAnnotations, ReportFinding } from '../../types'
import { appendReportFinding } from './rcaActions'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../hooks/apiClient', () => ({ request: mocks.request }))

const finding: ReportFinding = { id: '', title: 'Resource pressure', severity: 'minor', observed: 'Resource pressure explains Checkout p95 latency.', root_cause: 'Generated hypothesis #1 of root-cause analysis rca-1 (revision 2).' }
const annotations = (findings: ReportFinding[], updatedAt = '2026-10-09T12:00:00Z'): { annotations: ReportAnnotations; sections: [] } => ({
  annotations: { test_id: 'run-1', profile: 'engineer', title: '', prepared_by: '', executive_narrative: '', section_comments: {}, findings, updated_at: updatedAt, updated_by: 'pat' },
  sections: [],
})
const conflict = () => Object.assign(new Error('conflict'), { status: 409 })

describe('appendReportFinding', () => {
  beforeEach(() => mocks.request.mockReset())

  it('appends the finding to what is saved and retries a concurrent save', async () => {
    const other: ReportFinding = { id: 'f-1', title: 'Slow login', severity: 'major', observed: 'Login p95 rose.' }
    mocks.request
      .mockResolvedValueOnce(annotations([]))
      .mockRejectedValueOnce(conflict())
      .mockResolvedValueOnce(annotations([other], '2026-10-09T12:01:00Z'))
      .mockResolvedValueOnce(annotations([other, finding], '2026-10-09T12:02:00Z'))
    const saved = await appendReportFinding('run-1', 'engineer', finding)
    expect(saved.findings).toEqual([other, finding])
    const last = JSON.parse(mocks.request.mock.calls[3][1].body)
    expect(last.findings).toEqual([other, finding])
    expect(last.expected_updated_at).toBe('2026-10-09T12:01:00Z')
  })

  it('does not add the finding twice when a refused save had kept it', async () => {
    // The commentary was saved, then the verdict check refused the request.
    mocks.request
      .mockResolvedValueOnce(annotations([]))
      .mockRejectedValueOnce(conflict())
      .mockResolvedValueOnce(annotations([finding], '2026-10-09T12:01:00Z'))
    const saved = await appendReportFinding('run-1', 'engineer', finding)
    expect(saved.findings).toEqual([finding])
    expect(mocks.request).toHaveBeenCalledTimes(3)
  })
})
