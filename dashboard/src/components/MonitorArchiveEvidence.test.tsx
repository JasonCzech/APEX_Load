// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import MonitorArchiveEvidence from './MonitorArchiveEvidence'
import { safeMonitorEvidenceLink, readBoundedMonitorArchive } from './monitorArchiveModel'
import type { RunArchiveReport } from '../types'
const apiFetch = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), apiFetch }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('displays frozen coverage, source attribution and safe links on archived evidence', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const report = { manifest: { monitoring_status: 'incomplete', monitor_diagnostics_file: 'monitor_diagnostics.v1.json', monitor_evidence_file: 'monitor_evidence.v1.jsonl', monitor_coverage: [{ job_id: 'run:apm', source_id: 'apm', source_revision: 3, required: true, state: 'incomplete', executor_mode: 'direct', appdynamics: {}, provider_coverage: { metrics: { status: 'partial', truncated: true } } }] }, monitor_diagnostics: [{ source_id: 'apm', diagnostic: { id: '9', kind: 'snapshot', summary: 'Checkout', status: 'resolved', deep_link: 'https://controller.example.com/controller/#/snapshot/9', properties: { correlation: 'run_header', requestguid: 'appd-guid' } } }, { source_id: 'apm', diagnostic: { id: '10', kind: 'event', deep_link: 'javascript:alert(1)' } }] } as unknown as RunArchiveReport
  try {
    await act(async () => root.render(<MonitorArchiveEvidence report={report} testId="run" />))
    expect(container.textContent).toContain('Monitoring: incomplete'); expect(container.textContent).toContain('metrics: partial · truncated')
    expect(container.textContent).toContain('run_header'); expect(container.textContent).toContain('Request GUID: appd-guid')
    expect(container.querySelectorAll('a')).toHaveLength(3);
    expect(container.querySelector('a[href$="/archive/files/monitor_diagnostics.v1.json"]')?.textContent).toContain('Download retained monitoring diagnostics');
    expect(container.querySelector('a[href$="/archive/files/monitor_evidence.v1.jsonl"]')?.textContent).toContain('Download retained monitoring metrics'); expect(container.querySelector('a')?.getAttribute('rel')).toContain('noopener')
  } finally { act(() => root.unmount()); container.remove() }
})
it('rejects insecure links and bounded artifact previews', async () => {
  expect(safeMonitorEvidenceLink('http://controller.example.com')).toBeUndefined()
  expect(safeMonitorEvidenceLink('https://user:password@controller.example.com')).toBeUndefined()
  await expect(readBoundedMonitorArchive(new Response('', { headers: { 'content-length': String(8 * 1024 * 1024 + 1) } }))).rejects.toThrow('8 MiB')
  const point = { source: 'apm', timestamp_utc: '2026-10-03T12:00:00Z', provider: { name: 'appdynamics' } }
  expect(await readBoundedMonitorArchive(new Response(`${JSON.stringify(point)}\n${JSON.stringify({ source: 'legacy' })}\n`))).toEqual([point])
  const dynatrace = { ...point, source: 'dt', provider: { name: 'dynatrace' } }
  expect(await readBoundedMonitorArchive(new Response(`${JSON.stringify(dynatrace)}\n${JSON.stringify(point)}\n`))).toEqual([dynatrace, point])
})

it('renders archive diagnostics in bounded pages', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const report = { manifest: {}, monitor_diagnostics: Array.from({ length: 101 }, (_, index) => ({ source_id: 'apm', diagnostic: { id: String(index), kind: 'event', summary: `Diagnostic ${index}` } })) } as unknown as RunArchiveReport
  try {
    await act(async () => root.render(<MonitorArchiveEvidence report={report} testId="run" />))
    expect(container.querySelectorAll('tbody tr')).toHaveLength(100)
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Next archived evidence page')!.click())
    expect(container.querySelectorAll('tbody tr')).toHaveLength(1); expect(container.textContent).toContain('Diagnostic 100')
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Previous archived evidence page')!.click())
    expect(container.querySelectorAll('tbody tr')).toHaveLength(100)
  } finally { act(() => root.unmount()); container.remove() }
})
