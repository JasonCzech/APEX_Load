// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'

import TestExecutionInfrastructure from './TestExecutionInfrastructure'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('renders live Elastic capture health, values, and scrubbed samples', async () => {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  await act(async () => root.render(<TestExecutionInfrastructure manifest={null} manifestError="" manifestLoading={false} test={{ id: 'run', name: 'Run', status: 'RUNNING', total_vusers: 1, duration: '1m', created_at: '', live_metrics: { elk: { connection_id: 'elk-a', connection_name: 'Production logs', status: 'connected', metrics: { logs_total: 12, errors_total: 2 }, samples: [{ id: 'one', level: 'error', message: 'redacted sample' }] } } } as never} />))
  expect(container.textContent).toContain('Elastic capture · Production logs')
  expect(container.textContent).toContain('Log count')
  expect(container.textContent).toContain('redacted sample')
  act(() => root.unmount())
  container.remove()
})

it('renders live Dynatrace capability health, values, problems, and logs', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  await act(async () => root.render(<TestExecutionInfrastructure manifest={null} manifestError="" manifestLoading={false} test={{ id: 'run', name: 'Run', status: 'RUNNING', total_vusers: 1, duration: '1m', created_at: '', live_metrics: { dynatrace: { connection_id: 'dt-a', connection_name: 'Production Dynatrace', status: 'partial', capabilities: { metrics: { status: 'connected' }, logs: { status: 'error' } }, metrics: { service_latency: 42 }, problems: [{ id: 'p-1', title: 'Checkout failure', severity: 'ERROR' }], logs: [{ status: 'ERROR', message: 'redacted Grail log' }] } } } as never} />))
  expect(container.textContent).toContain('Dynatrace capture · Production Dynatrace')
  expect(container.textContent).toContain('service latency')
  expect(container.textContent).toContain('Checkout failure')
  expect(container.textContent).toContain('redacted Grail log')
  act(() => root.unmount()); container.remove()
})

it('shows controller-failover continuity and the failovers a run survived', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const test = { id: 'run', name: 'Run', status: 'COMPLETE', total_vusers: 10, duration: '150s', created_at: '', continuity: { policy: 'continue', armed: false, execution_generation: 3, failovers: [
    { prior_generation: 1, generation: 2, adopted_at: '2026-09-25T22:47:22Z', gap_seconds: 36.4, adopted_generators: 2 },
    { prior_generation: 2, generation: 3, adopted_at: '2026-09-25T22:49:30Z', adopted_generators: 1, completed_generators: 1 },
  ] } }
  await act(async () => root.render(<TestExecutionInfrastructure manifest={null} manifestError="" manifestLoading={false} test={test as never} />))
  expect(container.textContent).toContain('survived 2 failovers')
  expect(container.textContent).toContain('1 → 2')
  expect(container.textContent).toContain('36.4 s')
  expect(container.textContent).toContain('1 finished during the gap')
  act(() => root.unmount()); container.remove()
})

it('reports fail-on-failover for runs without continuity', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  await act(async () => root.render(<TestExecutionInfrastructure manifest={null} manifestError="" manifestLoading={false} test={{ id: 'run', name: 'Run', status: 'RUNNING', total_vusers: 1, duration: '1m', created_at: '' } as never} />))
  expect(container.textContent).toContain('Fail on controller failover')
  expect(container.querySelector('[aria-label="Controller failovers"]')).toBeNull()
  act(() => root.unmount()); container.remove()
})
