// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import ElasticArchiveEvidence from './ElasticArchiveEvidence'
import { elasticEvidenceTimeError, readBoundedElasticArchive } from './elasticEvidenceModel'
import type { RunArchiveReport } from '../types'
const apiFetch = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', async importOriginal => ({ ...(await importOriginal<typeof import('../hooks/apiClient')>()), apiFetch }))
it('reads retained provider projections while preserving document identity and source', async () => {
  const evidence = { id: 'index:doc', kind: 'log', provider: 'opensearch', index: 'logs-1', document_id: 'doc', log: { message: 'Retained' } }
  const body = `${JSON.stringify({ source_id: 'source', diagnostic: evidence })}\n${JSON.stringify({ source_id: 'legacy', diagnostic: { provider: 'appdynamics' } })}\n`
  expect(await readBoundedElasticArchive(new Response(body))).toEqual([{ ...evidence, source_id: 'source' }])
  await expect(readBoundedElasticArchive(new Response('', { status: 404 }))).rejects.toThrow('404')
})
it('renders archived coverage and paginated scrubbed records with filters', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const report = { manifest: { monitoring_status: 'incomplete', monitor_coverage: [{ job_id: 'run:source', source_id: 'source', source_revision: 2, connection_revision: 4, state: 'incomplete', elastic: { indices: ['logs-*'] }, provider_coverage: { logs: { status: 'truncated', truncated: true } } }] }, monitor_diagnostics: Array.from({ length: 101 }, (_, index) => ({ source_id: 'source', diagnostic: { id: `doc-${index}`, provider: 'elasticsearch', kind: 'log', service: 'checkout', log: { message: `Retained ${index}` } } })) } as unknown as RunArchiveReport
  try {
    await act(async () => root.render(<ElasticArchiveEvidence report={report} testId="run" />))
    expect(container.textContent).toContain('connection revision 4')
    expect(container.textContent).toContain('logs: truncated · truncated')
    expect(container.querySelectorAll('tbody tr')).toHaveLength(100)
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Next archived Elastic page')!.click())
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1)
    expect(container.textContent).toContain('Retained 100')
  } finally { act(() => root.unmount()); container.remove() }
})

it.each(['projections', 'timeline'])('retries the same archived %s load after a transient failure', async kind => {
  apiFetch.mockReset(); apiFetch.mockRejectedValueOnce(new Error('temporary outage')).mockResolvedValueOnce(new Response(''))
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const report = { manifest: { monitor_diagnostics_file: 'records.ndjson', monitor_evidence_file: 'points.ndjson', monitor_coverage: [{ job_id: 'job', source_id: 'source', elastic: {} }] } } as unknown as RunArchiveReport
  try {
    await act(async () => root.render(<ElasticArchiveEvidence report={report} testId="run" />))
    const button = Array.from(container.querySelectorAll('button')).find(button => button.textContent === `Load archived Elastic ${kind}`)!
    await act(async () => button.click())
    expect(container.textContent).toContain('temporary outage')
    await act(async () => button.click())
    expect(apiFetch).toHaveBeenCalledTimes(2)
    expect(container.textContent).not.toContain('temporary outage')
  } finally { act(() => root.unmount()); container.remove() }
})

it('reports invalid UTC bounds without presenting an empty successful filter result', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const report = { manifest: { monitor_coverage: [{ job_id: 'job', source_id: 'source', elastic: {} }] }, monitor_diagnostics: [{ source_id: 'source', diagnostic: { id: 'doc', kind: 'log', provider: 'elasticsearch', timestamp_utc: '2026-10-06T10:00:00.000000001Z', log: { message: 'Retained' } } }] } as unknown as RunArchiveReport
  try {
    await act(async () => root.render(<ElasticArchiveEvidence report={report} testId="run" />))
    const from = Array.from(container.querySelectorAll('label')).find(label => label.textContent === 'Archived Elastic from')!.querySelector('input')!
    const until = Array.from(container.querySelectorAll('label')).find(label => label.textContent === 'Archived Elastic until')!.querySelector('input')!
    const fill = async (input: HTMLInputElement, value: string) => act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
    await fill(from, '2026-02-30T10:00:00Z')
    expect(container.textContent).toContain('valid UTC RFC3339')
    expect(container.textContent).not.toContain('No retained matching')
    await fill(from, '2026-10-06T10:00:00.000000001Z')
    await fill(until, '2026-10-06T10:00:00.000000002Z')
    expect(container.textContent).toContain('Retained')
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1)
    await fill(until, '2026-10-06T10:00:00.000000001Z')
    expect(container.textContent).toContain('until must be after from')
  } finally { act(() => root.unmount()); container.remove() }
})

it.each(['invalid', '2026-10-06T10:00:00+00:00', '2026-02-30T10:00:00Z', '0000-01-01T00:00:00Z'])('rejects invalid archive timestamps: %s', value => {
  expect(elasticEvidenceTimeError(value, '')).not.toBe('')
})
