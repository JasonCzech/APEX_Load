// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import ElasticInspectionResult from './ElasticInspectionResult'
import type { ElasticInspection } from '../types/elastic'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('renders transformed nested preview results, approximation, and truncation', () => {
  const operation: ElasticInspection = { id: 'op', connection_id: 'connection', connection_revision: 7, kind: 'preview', status: 'complete', created_at: '', expires_at: '', result: { preview: { points: [{ timestamp_utc: '2026-10-05T00:00:00Z', metric_name: 'p99', value: 42, unit: 'ms', quality: 'valid', dimensions: { service: 'checkout' }, provider: { name: 'elasticsearch', metric_id: 'query', statistic: 'p99', bucket_start: '', bucket_end: '', resolution_seconds: 10, settled: true, aggregation_eligible: false, approximate: true } }], evidence: [{ kind: 'log', service: 'checkout', correlation: 'service_time', log: { message: '<img src=x onerror=alert(1)>', attributes: { value: { kind: 'number', number_value: 0 } } }, deep_link: 'https://captured.example.com/unapproved' }], coverage: { logs: { status: 'partial', truncated: true, reason: 'record_limit' } } } } }
  act(() => root.render(<ElasticInspectionResult operation={operation} />))
  expect(container.textContent).toContain('connection revision 7')
  expect(container.textContent).toContain('42 ms')
  expect(container.textContent).toContain('approximate')
  expect(container.textContent).toContain('record_limit')
  expect(container.textContent).toContain('<img src=x onerror=alert(1)>')
  expect(container.querySelector('img')).toBeNull()
  expect(container.querySelector('a')).toBeNull()
})
it('keeps distinct span records and visibly reports partial trace context', () => {
  const operation: ElasticInspection = { id: 'op', connection_id: 'connection', connection_revision: 1, kind: 'preview', status: 'complete', created_at: '', expires_at: '', result: { preview: { points: [], coverage: {}, evidence: ['one', 'two'].map((span, index) => ({ id: span, kind: 'span', timestamp_utc: `2026-10-05T00:00:0${index}Z`, service: 'checkout', span: { trace_id: 'trace', span_id: span, parent_span_id: 'missing', operation: 'purchase', duration_ms: 10 } })) } } }
  act(() => root.render(<ElasticInspectionResult operation={operation} />))
  expect(container.textContent).toContain('2 retained spans · partial trace context')
  expect(container.textContent).toContain('upstream sampling')
  expect(container.textContent).toContain('(not retained on this page)')
})
