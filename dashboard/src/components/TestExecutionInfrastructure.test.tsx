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
  expect(container.textContent).toContain('logs_total')
  expect(container.textContent).toContain('redacted sample')
  act(() => root.unmount())
  container.remove()
})

it('renders live Dynatrace capability health, values, problems, and logs', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  await act(async () => root.render(<TestExecutionInfrastructure manifest={null} manifestError="" manifestLoading={false} test={{ id: 'run', name: 'Run', status: 'RUNNING', total_vusers: 1, duration: '1m', created_at: '', live_metrics: { dynatrace: { connection_id: 'dt-a', connection_name: 'Production Dynatrace', status: 'partial', capabilities: { metrics: { status: 'connected' }, logs: { status: 'error' } }, metrics: { service_latency: 42 }, problems: [{ id: 'p-1', title: 'Checkout failure', severity: 'ERROR' }], logs: [{ status: 'ERROR', message: 'redacted Grail log' }] } } } as never} />))
  expect(container.textContent).toContain('Dynatrace capture · Production Dynatrace')
  expect(container.textContent).toContain('service_latency')
  expect(container.textContent).toContain('Checkout failure')
  expect(container.textContent).toContain('redacted Grail log')
  act(() => root.unmount()); container.remove()
})
