// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import MonitoringPage from './MonitoringPage'

const apiMock = {
  getHealth: vi.fn(),
  listTests: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('MonitoringPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    apiMock.getHealth.mockResolvedValue({ status: 'ok', version: 'dev', ws_clients: 2, test_count: 1 })
    apiMock.listTests.mockResolvedValue({ tests: [] })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders sampled telemetry diagnostics in the monitoring grid', async () => {
    await act(async () => {
      root.render(
        <MonitoringPage
          connectionState="connected"
          telemetryDiagnostics={[
            {
              test_id: 'test-1',
              test_name: 'Checkout',
              timestamp: '2026-05-12T20:05:01Z',
              diagnostic: {
                severity: 'danger',
                summary: 'collector dropped 4 metric samples',
                conditions: ['collector_drops'],
                samples: [
                  {
                    issue: 'error',
                    transaction: 'Checkout',
                    dependency_target: 'payments.example.test',
                    errors: 4,
                    count: 10,
                    avg_ms: 640,
                    p99_ms: 920,
                  },
                ],
              },
            },
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('Telemetry Diagnostics')
    expect(container.textContent).toContain('Checkout: collector dropped 4 metric samples')
    expect(container.textContent).toContain('collector_drops')
    expect(container.textContent).toContain('danger')
    expect(container.textContent).toContain('error')
    expect(container.textContent).toContain('4/10 errors')
    expect(container.textContent).toContain('payments.example.test')
  })
})