// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TestExecutionSummary from './TestExecutionSummary'
import { LiveMetrics, TxMetrics } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecutionSummary', () => {
	let container: HTMLDivElement
	let root: Root

	beforeEach(() => {
		container = document.createElement('div')
		document.body.appendChild(container)
		root = createRoot(container)
	})

	afterEach(async () => {
		await act(async () => {
			root.unmount()
		})
		container.remove()
	})

	it('renders live server telemetry and prior-sample deltas', async () => {
		const current: TxMetrics = {
			name: 'POST /checkout',
			count: 12,
			errors: 1,
			avg_ms: 180,
			p50_ms: 150,
			p90_ms: 210,
			p99_ms: 260,
			server_telemetry: {
				samples: 12,
				avg_server_processing_ms: 48,
				avg_transfer_ms: 14,
				avg_request_payload_kb: 1.25,
				avg_response_payload_kb: 4,
				connection_reused_pct: 75,
			},
		}
		const previous: LiveMetrics = {
			timestamp: '2026-05-15T18:59:59Z',
			active_vusers: 6,
			tps: 8,
			avg_response_ms: 170,
			error_pct: 0,
			total_transactions: 10,
			total_errors: 0,
			p50_ms: 145,
			p90_ms: 205,
			p95_ms: 220,
			p99_ms: 240,
			transactions: {
				'POST /checkout': {
					...current,
					server_telemetry: {
						samples: 10,
						avg_server_processing_ms: 42,
						avg_transfer_ms: 18,
						avg_request_payload_kb: 1.2,
						avg_response_payload_kb: 4,
						connection_reused_pct: 50,
					},
				},
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[current]}
					metrics={{ p50_ms: 150, p90_ms: 210, p95_ms: 230, p99_ms: 260 }}
					history={[previous, { ...previous, timestamp: '2026-05-15T19:00:00Z', transactions: { 'POST /checkout': current } }]}
				/>,
			)
		})

		expect(container.textContent).toContain('Server Telemetry')
		expect(container.textContent).toContain('Server 48.0 ms (+6.0 ms vs prev)')
		expect(container.textContent).toContain('Transfer 14.0 ms (-4.0 ms vs prev)')
		expect(container.textContent).toContain('Payload 1.25 KB / 4.00 KB')
		expect(container.textContent).toContain('Reuse 75% (+25% vs prev)')
	})
})