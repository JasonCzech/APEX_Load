// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ELKConnectionsCard from './ELKConnectionsCard'
import DynatraceConnectionsCard from './DynatraceConnectionsCard'
import CaptureSelectionHelp from './CaptureSelectionHelp'
import CaptureArchiveSummary from './CaptureArchiveSummary'
import { CaptureConnectionHealth } from './CaptureConnectionShared'
import { CaptureStatusSummary, CaptureEmptyEvidence, CaptureMetricValues } from './CaptureStatusSummary'
import { settingsHref } from './settingsNavigation'
import { parseAppViewFromHash } from '../appRouting'
import { captureSelectionNeedsReview, dynatraceCapabilities } from './captureConnectionModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const elkApi = { listELKConnections: vi.fn(), createELKConnection: vi.fn(), updateELKConnection: vi.fn(), deleteELKConnection: vi.fn(), testELKConnection: vi.fn() }
const dtApi = { listDynatraceConnections: vi.fn(), createDynatraceConnection: vi.fn(), updateDynatraceConnection: vi.fn(), deleteDynatraceConnection: vi.fn(), testDynatraceConnection: vi.fn() }
vi.mock('../hooks/api/useELKConnectionsApi', () => ({ useELKConnectionsApi: () => elkApi }))
vi.mock('../hooks/api/useDynatraceConnectionsApi', () => ({ useDynatraceConnectionsApi: () => dtApi }))
let container: HTMLDivElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); elkApi.listELKConnections.mockResolvedValue({ connections: [] }); dtApi.listDynatraceConnections.mockResolvedValue({ connections: [] }) })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.resetAllMocks() })
const click = async (name: string) => { await act(async () => { const button = Array.from(container.querySelectorAll('button')).find(b => b.textContent === name); expect(button).toBeTruthy(); button!.click() }) }
const field = (name: string) => Array.from(container.querySelectorAll('label')).find(l => l.textContent === name)!.querySelector('input')!
const fill = async (input: HTMLInputElement, value: string) => { await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }) }
const savedELK = { id: 'elk', project_id: 'p', name: 'Saved Elastic', enabled: false, status: 'disabled', endpoint: 'https://elastic.example.com', index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message', auth_type: 'api_key', has_api_key: true, metric_aggregations: [] }

it('requires explicit review of stale capture selections, but leaves opt-out unaffected', () => {
  expect(captureSelectionNeedsReview('chosen', [], false, true)).toBe(true)
  expect(captureSelectionNeedsReview('chosen', [], false, false)).toBe(true)
  expect(captureSelectionNeedsReview('chosen', [{ id: 'chosen' }], true, false)).toBe(true)
  expect(captureSelectionNeedsReview('chosen', [{ id: 'chosen' }], false, false)).toBe(false)
  expect(captureSelectionNeedsReview('', [], true, true)).toBe(false)
  expect(dynatraceCapabilities({ metrics_enabled: true, problems_enabled: false, logs_enabled: true })).toBe('Metrics, Grail logs')
})

it('saves a name-only disabled draft and resets it on a project switch', async () => {
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Add ELK connection'); await fill(field('Name (required)'), 'Prepared later')
  await click('Save draft')
  expect(elkApi.createELKConnection).toHaveBeenCalledWith('p', expect.objectContaining({ name: 'Prepared later', enabled: false }))
  await click('Add ELK connection'); await fill(field('Name (required)'), 'Private draft')
  await act(async () => root.render(<ELKConnectionsCard projectId="other" canManage />))
  expect(container.querySelector('input')).toBeNull()
  expect(container.textContent).not.toContain('Private draft')
})

it('preserves write-only credentials, tests disabled configurations, and confirms deletion', async () => {
  elkApi.listELKConnections.mockResolvedValue({ connections: [savedELK] })
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Test saved connection')
  expect(elkApi.testELKConnection).toHaveBeenCalledWith('p', 'elk')
  await click('Edit')
  expect(container.textContent).toContain('Configured credentials: API key')
  await fill(field('API key project-secret reference'), 'replacement')
  await fill(field('API key project-secret reference'), '')
  await click('Save draft')
  expect(elkApi.updateELKConnection).toHaveBeenCalledWith('p', 'elk', expect.objectContaining({ api_key_secret_name: undefined }))
  await click('Delete'); expect(elkApi.deleteELKConnection).not.toHaveBeenCalled()
  await click('Keep connection'); expect(elkApi.deleteELKConnection).not.toHaveBeenCalled()
  await click('Delete'); await click('Confirm delete')
  expect(elkApi.deleteELKConnection).toHaveBeenCalledWith('p', 'elk')
})

it('keeps metric-name focus stable across keystrokes', async () => {
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage />))
  await click('Add ELK connection'); await click('Add aggregation')
  const input = container.querySelector<HTMLInputElement>('[aria-label="Metric 1 name"]')!
  input.focus(); await fill(input, 'latency')
  expect(document.activeElement).toBe(input)
})

it('hides capability-specific fields and labels the Dynatrace aggregation control', async () => {
  await act(async () => root.render(<DynatraceConnectionsCard projectId="p" canManage />))
  await click('Add Dynatrace connection')
  expect(container.textContent).not.toContain('Log timestamp field')
  expect(container.textContent).not.toContain('Metric selectors')
  await act(async () => field('Metrics API').click())
  await click('Add metric')
  expect(container.querySelector('[aria-label="Dynatrace metric 1 aggregation"]')).not.toBeNull()
  expect(container.textContent).toContain('Environment URL')
  expect(container.textContent).not.toContain('Log timestamp field')
})

it('keeps Monitoring read-only and links to project-scoped Settings', async () => {
  window.location.hash = '#/monitoring?category=telemetry&projectId=p'
  await act(async () => root.render(<ELKConnectionsCard projectId="p" canManage summaryOnly />))
  expect(container.querySelector('input')).toBeNull()
  expect(container.querySelector('a')?.getAttribute('href')).toContain('setting=elk-capture')
  expect(container.querySelector('a')?.getAttribute('href')).toContain('projectId=p')
  expect(parseAppViewFromHash(settingsHref('elk-capture', '#/new-test?projectId=p'))).toMatchObject({ returnTo: '#/new-test?projectId=p' })
})

it('distinguishes loading errors from empty connections and exposes retry', async () => {
  const retry = vi.fn()
  await act(async () => root.render(<CaptureSelectionHelp provider="elk" count={0} error loading={false} retry={retry} />))
  expect(container.textContent).not.toContain('No ready connections')
  await click('Retry connections'); expect(retry).toHaveBeenCalledOnce()
  await act(async () => root.render(<CaptureSelectionHelp provider="elk" count={0} error={false} loading={false} retry={retry} canManage />))
  expect(container.textContent).toContain('No ready connections')
  expect(container.textContent).toContain('Configure connection')
})

it('explains partial capture, last successful collection, and capability failures', async () => {
  await act(async () => root.render(<><CaptureStatusSummary capture={{ connection_id: 'dt', status: 'partial', last_polled_at: '2026-09-21T10:10:00Z', last_successful_at: '2026-09-21T10:00:00Z', capabilities: { logs: { status: 'error', message: 'Permission denied' } } }} /><CaptureEmptyEvidence status="connected" /></>))
  expect(container.textContent).toContain('Last fully successful collection')
  expect(container.textContent).toContain('earlier successful collection')
  expect(container.textContent).toContain('Permission denied')
  expect(container.textContent).toContain('No matching evidence returned yet')
})

it('does not round small nonzero source metrics to zero', async () => {
  await act(async () => root.render(<CaptureMetricValues metrics={{ latency_seconds: 0.00001234, logs_total: 123456789 }} />))
  expect(container.textContent).toContain('0.00001234')
  expect(container.textContent).toContain('123,456,789')
})

it('does not present a check of old configuration as current verification', async () => {
  await act(async () => root.render(<CaptureConnectionHealth enabled missing={[]} status="connected" checkedAt="2026-09-21T10:00:00Z" updatedAt="2026-09-21T10:10:00Z" />))
  expect(container.textContent).toContain('Configuration changed since this check')
})

it('distinguishes archived selection, missing evidence, and unrecorded capture', async () => {
  const report = { manifest: { provenance: { elk_capture: { connection_id: 'elk' } } }, elk_samples: [] }
  await act(async () => root.render(<CaptureArchiveSummary report={report as never} />))
  expect(container.textContent).toContain('Capture selected; no evidence records')
  expect(container.textContent).toContain('Capture not recorded for this run')
  await act(async () => root.render(<CaptureArchiveSummary report={{ ...report, integrity: { artifacts: [{ name: 'elk-evidence.jsonl', status: 'missing' }] } } as never} />))
  expect(container.textContent).toContain('Evidence unavailable or integrity check failed')
})
