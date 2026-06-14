// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TestExecutionSummary from './TestExecutionSummary'
import { RunComparisonBaseline, TxMetrics } from '../types'

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

	it('renders live server telemetry and previous-run deltas', async () => {
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
				avg_first_event_ms: 125,
				max_first_event_ms: 180,
				event_count: 7,
				avg_request_payload_kb: 1.25,
				avg_response_payload_kb: 4,
				connection_reused_pct: 75,
			},
		}
		const baseline: RunComparisonBaseline = {
			source_test_id: 'test-0',
			source_test_name: 'Checkout Run - Previous',
			policy: 'latest_successful',
			completed_at: '2026-05-15T18:59:59Z',
			transactions: {
				'POST /checkout': {
					...current,
					server_telemetry: {
						samples: 10,
						avg_server_processing_ms: 42,
						avg_transfer_ms: 18,
						avg_first_event_ms: 150,
						max_first_event_ms: 210,
						event_count: 5,
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
					baseline={baseline}
				/>,
			)
		})

		expect(container.textContent).toContain('Live Server Telemetry')
		expect(container.textContent).toContain('Baseline: Checkout Run - Previous (latest successful)')
		expect(container.textContent).toContain('Server Telemetry')
		expect(container.textContent).toContain('Server 48.0 ms (+6.0 ms vs prev)')
		expect(container.textContent).toContain('Transfer 14.0 ms (-4.0 ms vs prev)')
		expect(container.textContent).toContain('First event 125.0 ms / max 180.0 ms (-25.0 ms vs prev)')
		expect(container.textContent).toContain('Events 7')
		expect(container.textContent).toContain('Payload 1.25 KB / 4.00 KB')
		expect(container.textContent).toContain('Reuse 75% (+25% vs prev)')
	})

	it('keeps long telemetry names and targets constrained inside cards', async () => {
		// @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
		const { readFileSync } = await import('node:fs')
		const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
		const executionCss = readFileSync(`${cwd}/src/components/TestExecution.css`, 'utf8') as string
		const longTransaction: TxMetrics = {
			name: 'POST /Services/com/cingular/csi/edp/InquireSubscriberBillingAgreementWithExtremelyLongOperationNameWithoutNaturalBreaks.jws',
			dependency_type: 'http',
			dependency_target: 'https://csi-gw-ext.att.com:443/Services/com/cingular/csi/edp/InquireSubscriberBillingAgreementWithExtremelyLongDependencyTargetWithoutNaturalBreaks',
			count: 12,
			errors: 0,
			avg_ms: 180,
			p50_ms: 150,
			p90_ms: 210,
			p99_ms: 260,
			avg_dns_ms: 8,
			avg_connect_ms: 14,
			avg_tls_ms: 20,
			avg_ttfb_ms: 84,
			server_telemetry: {
				samples: 12,
				avg_server_processing_ms: 48,
				avg_transfer_ms: 14,
				avg_request_payload_kb: 1.25,
				avg_response_payload_kb: 4,
				connection_reused_pct: 75,
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[longTransaction]}
					metrics={{ p50_ms: 150, p90_ms: 210, p95_ms: 230, p99_ms: 260 }}
				/>,
			)
		})

		const card = container.querySelector('.server-telemetry-card')
		const name = container.querySelector('.server-telemetry-name')
		const target = container.querySelector('.server-telemetry-target')

		expect(container.textContent).toContain('Live Server Telemetry')
		expect(container.textContent).toContain('Live HTTP Response Decomposition')
		expect(card).not.toBeNull()
		expect(name).not.toBeNull()
		expect(target).not.toBeNull()
		expect(cssRule(executionCss, '.server-telemetry-card')).toContain('min-width: 0;')
		expect(cssRule(executionCss, '.server-telemetry-card')).toContain('overflow: hidden;')
		expect(cssRule(executionCss, '.server-telemetry-name')).toContain('overflow-wrap: anywhere;')
		expect(cssRule(executionCss, '.server-telemetry-target')).toContain('overflow-wrap: anywhere;')
		expect(cssRule(executionCss, '.server-telemetry-value')).toContain('word-break: break-word;')
	})

	it('renders live SOAP operation hotspots when available', async () => {
		const createOrder: TxMetrics = {
			name: 'CreateOrder',
			dependency_type: 'soap',
			dependency_target: 'checkout.example.test#CreateOrder',
			count: 6,
			errors: 1,
			avg_ms: 140,
			p50_ms: 120,
			p90_ms: 180,
			p99_ms: 240,
			server_telemetry: {
				samples: 6,
				avg_server_processing_ms: 52,
				avg_transfer_ms: 18,
			},
		}
		const cancelOrder: TxMetrics = {
			name: 'CancelOrder',
			dependency_type: 'soap',
			dependency_target: 'checkout.example.test#CancelOrder',
			count: 10,
			errors: 0,
			avg_ms: 90,
			p50_ms: 82,
			p90_ms: 120,
			p99_ms: 150,
			server_telemetry: {
				samples: 10,
				avg_server_processing_ms: 28,
				avg_transfer_ms: 12,
			},
		}
		const getStatus: TxMetrics = {
			name: 'GetOrderStatus',
			dependency_type: 'soap',
			dependency_target: 'checkout.example.test#GetOrderStatus',
			count: 4,
			errors: 2,
			avg_ms: 110,
			p50_ms: 108,
			p90_ms: 132,
			p99_ms: 160,
			server_telemetry: {
				samples: 4,
				avg_server_processing_ms: 24,
				avg_transfer_ms: 16,
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[]}
					metrics={{
						p50_ms: 150,
						p90_ms: 210,
						p95_ms: 230,
						p99_ms: 260,
						soap_operations: {
							CreateOrder: createOrder,
							CancelOrder: cancelOrder,
							GetOrderStatus: getStatus,
						},
					}}
				/>,
			)
		})

		expect(container.textContent).toContain('Live SOAP Operations')
		expect(container.textContent).toContain('Busiest Operation')
		expect(container.textContent).toContain('CancelOrder')
		expect(container.textContent).toContain('10 calls')
		expect(container.textContent).toContain('Slowest Avg Operation')
		expect(container.textContent).toContain('CreateOrder')
		expect(container.textContent).toContain('140.0 ms')
		expect(container.textContent).toContain('Highest Error Rate')
		expect(container.textContent).toContain('GetOrderStatus')
		expect(container.textContent).toContain('50.0%')
		expect(container.textContent).toContain('CreateOrder')
		expect(container.textContent).toContain('140.0')
		expect(container.textContent).toContain('52.0 ms')
		expect(container.textContent).toContain('18.0 ms')
		expect(container.textContent).toContain('checkout.example.test#CreateOrder')
	})

	it('renders live HTTP response decomposition when phase timing is available', async () => {
		const home: TxMetrics = {
			name: 'GET /',
			dependency_type: 'http',
			dependency_target: 'storefront.example.test',
			count: 12,
			errors: 0,
			avg_ms: 118,
			p50_ms: 102,
			p90_ms: 144,
			p95_ms: 168,
			p99_ms: 188,
			avg_dns_ms: 8,
			avg_connect_ms: 14,
			avg_tls_ms: 20,
			avg_ttfb_ms: 84,
			server_telemetry: {
				samples: 12,
				avg_server_processing_ms: 42,
				avg_transfer_ms: 18,
			},
		}
		const checkout: TxMetrics = {
			name: 'POST /checkout',
			dependency_type: 'http',
			dependency_target: 'api.example.test',
			count: 8,
			errors: 1,
			avg_ms: 96,
			p50_ms: 90,
			p90_ms: 118,
			p95_ms: 134,
			p99_ms: 140,
			avg_dns_ms: 3,
			avg_connect_ms: 9,
			avg_tls_ms: 0,
			avg_ttfb_ms: 58,
			server_telemetry: {
				samples: 8,
				avg_server_processing_ms: 39,
				avg_transfer_ms: 12,
			},
		}

		const baseline: RunComparisonBaseline = {
			source_test_id: 'test-0',
			source_test_name: 'Checkout Run - Previous',
			completed_at: '2026-05-15T18:59:59Z',
			transactions: {
				'GET /': {
					...home,
					avg_ms: 104,
					p95_ms: 140,
					p99_ms: 170,
					server_telemetry: {
						samples: 10,
						avg_server_processing_ms: 36,
						avg_transfer_ms: 16,
					},
				},
				'POST /checkout': {
					...checkout,
					avg_ms: 88,
					p95_ms: 120,
					p99_ms: 132,
					server_telemetry: {
						samples: 8,
						avg_server_processing_ms: 34,
						avg_transfer_ms: 10,
					},
				},
			},
			dependency_health: {
				'storefront.example.test': {
					name: 'storefront.example.test',
					dependency_type: 'http',
					dependency_target: 'storefront.example.test',
					count: 10,
					errors: 1,
					avg_ms: 104,
					p50_ms: 96,
					p90_ms: 132,
					p95_ms: 150,
					p99_ms: 170,
					server_telemetry: {
						samples: 10,
						avg_server_processing_ms: 36,
						avg_transfer_ms: 16,
					},
				},
				'api.example.test': {
					name: 'api.example.test',
					dependency_type: 'http',
					dependency_target: 'api.example.test',
					count: 8,
					errors: 0,
					avg_ms: 88,
					p50_ms: 84,
					p90_ms: 110,
					p95_ms: 120,
					p99_ms: 132,
					server_telemetry: {
						samples: 8,
						avg_server_processing_ms: 34,
						avg_transfer_ms: 10,
					},
				},
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[home, checkout]}
					metrics={{
						p50_ms: 95,
						p90_ms: 132,
						p95_ms: 150,
						p99_ms: 188,
						dependency_health: {
							'storefront.example.test': {
								name: 'storefront.example.test',
								dependency_type: 'http',
								dependency_target: 'storefront.example.test',
								count: 12,
								errors: 0,
								avg_ms: 118,
								p50_ms: 102,
								p90_ms: 144,
								p95_ms: 172,
								p99_ms: 188,
								server_telemetry: {
									samples: 12,
									avg_server_processing_ms: 42,
									avg_transfer_ms: 18,
								},
							},
							'api.example.test': {
								name: 'api.example.test',
								dependency_type: 'http',
								dependency_target: 'api.example.test',
								count: 8,
								errors: 1,
								avg_ms: 96,
								p50_ms: 90,
								p90_ms: 118,
								p95_ms: 134,
								p99_ms: 140,
								server_telemetry: {
									samples: 8,
									avg_server_processing_ms: 39,
									avg_transfer_ms: 12,
								},
							},
						},
						dependency_map: {
							nodes: [
								{ id: 'apexload', label: 'APEX Load VUsers', kind: 'source', status: 'healthy' },
								{
									id: 'dependency:storefront.example.test',
									label: 'storefront.example.test',
									kind: 'dependency',
									dependency_type: 'http',
									status: 'warning',
									transactions: 12,
									errors: 0,
									error_pct: 0,
									p95_ms: 172,
								},
								{
									id: 'dependency:api.example.test',
									label: 'api.example.test',
									kind: 'dependency',
									dependency_type: 'http',
									status: 'danger',
									transactions: 8,
									errors: 1,
									error_pct: 12.5,
									p95_ms: 134,
								},
							],
							edges: [
								{ source: 'apexload', target: 'dependency:storefront.example.test', dependency_type: 'http', transactions: 12, errors: 0, error_pct: 0, p95_ms: 172 },
								{ source: 'apexload', target: 'dependency:api.example.test', dependency_type: 'http', transactions: 8, errors: 1, error_pct: 12.5, p95_ms: 134 },
							],
						},
						error_patterns: [
							{
								signature: 'http ___ backend timeout',
								sample: 'HTTP 503 backend timeout',
								count: 3,
								first_seen: '2026-05-18T10:00:00Z',
								latest_seen: '2026-05-18T10:00:30Z',
								actions: ['GET /', 'POST /checkout'],
								affected_groups: ['buyers'],
								status_code: 503,
								error_pct: 12.5,
								snapshot_url: '/api/v1/tests/test-1/archive/transactions?success=false',
							},
						],
					}}
					baseline={baseline}
					goalConfig={{
						p95_latency_ms: 160,
						max_error_pct: 10,
						max_avg_server_processing_ms: 40,
					}}
				/>,
			)
		})

		expect(container.textContent).toContain('Live HTTP Dependency Health')
		expect(container.textContent).toContain('Live Error Patterns')
		expect(container.textContent).toContain('HTTP 503 backend timeout')
		expect(container.textContent).toContain('3 hits')
		expect(container.textContent).toContain('POST /checkout')
		expect(container.textContent).toContain('buyers')
		expect(container.textContent).toContain('Snapshot query: /api/v1/tests/test-1/archive/transactions?success=false')
		expect(container.textContent).toContain('Live Dependency Map')
		expect(container.textContent).toContain('APEX Load VUsers')
		expect(container.textContent).toContain('2 downstream links')
		expect(container.textContent).toContain('Warning')
		expect(container.textContent).toContain('12 tx')
		expect(container.textContent).toContain('0.0% errors')
		expect(container.textContent).toContain('134.0 ms P95')
		expect(container.textContent).toContain('storefront.example.test')
		expect(container.textContent).toContain('172.0 ms (+22.0 ms vs prev)')
		expect(container.textContent).toContain('12.5%')
		expect(container.textContent).toContain('0.0% (-10.0% vs prev)')
		expect(container.textContent).toContain('Breach: P95, Server')
		expect(container.textContent).toContain('Breach: Errors')
		expect(container.textContent).toContain('Live HTTP Response Decomposition')
		expect(container.textContent).toContain('Stacked Phase View')
		expect(container.textContent).toContain('Transaction Breakdown')
		expect(container.textContent).toContain('118.0 ms (+14.0 ms vs prev)')
		expect(container.textContent).toContain('168.0 ms (+28.0 ms vs prev)')
		expect(container.textContent).toContain('GET /')
		expect(container.textContent).toContain('storefront.example.test')
		expect(container.textContent).toContain('DNS 8.0 ms')
		expect(container.textContent).toContain('TCP 14.0 ms')
		expect(container.textContent).toContain('TLS 20.0 ms')
		expect(container.textContent).toContain('Server 42.0 ms')
		expect(container.textContent).toContain('Transfer 18.0 ms')
		expect(container.textContent).toContain('8.0 ms')
		expect(container.textContent).toContain('14.0 ms')
		expect(container.textContent).toContain('20.0 ms')
		expect(container.textContent).toContain('42.0 ms')
		expect(container.textContent).toContain('18.0 ms')
		expect(container.textContent).toContain('84.0 ms')
		expect(container.textContent).toContain('102.0 ms')
	})

	it('renders live JMS payload hotspots when JMS payload telemetry is available', async () => {
		const publish: TxMetrics = {
			name: 'PublishOrder',
			dependency_type: 'jms_queue',
			dependency_target: 'orders.in',
			count: 12,
			errors: 0,
			avg_ms: 95,
			p50_ms: 80,
			p90_ms: 120,
			p99_ms: 150,
			server_telemetry: {
				samples: 12,
				avg_transfer_ms: 18,
				avg_request_payload_kb: 6.5,
				avg_response_payload_kb: 0.5,
				request_payload_histogram: { '4-16 KB': 12 },
				response_payload_histogram: { '0-1 KB': 12 },
				messages_sent: 12,
				avg_messages_sent_per_sec: 18,
				request_size_classes: { medium: 12 },
				response_size_classes: { small: 12 },
			},
		}
		const requestReply: TxMetrics = {
			name: 'RequestReplyQuote',
			dependency_type: 'jms_queue',
			dependency_target: 'quotes.rpc',
			count: 5,
			errors: 1,
			avg_ms: 140,
			p50_ms: 132,
			p90_ms: 170,
			p99_ms: 210,
			server_telemetry: {
				samples: 5,
				avg_transfer_ms: 42,
				avg_request_payload_kb: 2.25,
				avg_response_payload_kb: 12.5,
				request_payload_histogram: { '1-4 KB': 5 },
				response_payload_histogram: { '4-16 KB': 5 },
				avg_message_latency_ms: 95,
				max_message_latency_ms: 110,
				messages_sent: 5,
				messages_received: 5,
				avg_messages_sent_per_sec: 7.5,
				avg_messages_received_per_sec: 7.5,
				avg_consumer_lag_ms: 35,
				max_consumer_lag_ms: 52,
				request_size_classes: { small: 5 },
				response_size_classes: { medium: 5 },
			},
		}
		const consume: TxMetrics = {
			name: 'ConsumeAudit',
			dependency_type: 'jms_topic',
			dependency_target: 'audit.events',
			count: 20,
			errors: 0,
			avg_ms: 60,
			p50_ms: 54,
			p90_ms: 74,
			p99_ms: 88,
			server_telemetry: {
				samples: 20,
				avg_transfer_ms: 12,
				avg_request_payload_kb: 0,
				avg_response_payload_kb: 3.4,
				response_payload_histogram: { '1-4 KB': 20 },
				messages_received: 20,
				avg_messages_received_per_sec: 24,
				avg_consumer_lag_ms: 18,
				max_consumer_lag_ms: 24,
				response_size_classes: { small: 20 },
			},
		}
		const publishTopic: TxMetrics = {
			name: 'BroadcastAudit',
			dependency_type: 'jms_topic',
			dependency_target: 'audit.events',
			count: 8,
			errors: 0,
			avg_ms: 40,
			p50_ms: 35,
			p90_ms: 50,
			p99_ms: 64,
			server_telemetry: {
				samples: 8,
				messages_sent: 8,
				avg_messages_sent_per_sec: 14,
				avg_request_payload_kb: 1.5,
				request_payload_histogram: { '1-4 KB': 8 },
				request_size_classes: { small: 8 },
			},
		}
		const consumeReplica: TxMetrics = {
			name: 'ConsumeAuditReplica',
			dependency_type: 'jms_topic',
			dependency_target: 'audit.events',
			count: 4,
			errors: 0,
			avg_ms: 58,
			p50_ms: 55,
			p90_ms: 68,
			p99_ms: 80,
			server_telemetry: {
				samples: 4,
				messages_received: 4,
				avg_messages_received_per_sec: 6,
				avg_consumer_lag_ms: 16,
				response_payload_histogram: { '1-4 KB': 4 },
				response_size_classes: { small: 4 },
			},
		}
		const consumeOrderDLQ: TxMetrics = {
			name: 'ConsumeOrderDLQ',
			dependency_type: 'jms_dead_letter_queue',
			dependency_target: 'orders.dlq',
			count: 7,
			errors: 2,
			avg_ms: 185,
			p50_ms: 170,
			p90_ms: 220,
			p99_ms: 300,
			server_telemetry: {
				samples: 7,
				messages_received: 7,
				avg_messages_received_per_sec: 5,
				avg_response_payload_kb: 2.2,
				response_payload_histogram: { '1-4 KB': 7 },
				response_size_classes: { small: 7 },
			},
		}
		const consumeAuditDLQ: TxMetrics = {
			name: 'ConsumeAuditDLQ',
			dependency_type: 'jms_dead_letter_queue',
			dependency_target: 'audit.dlq',
			count: 3,
			errors: 0,
			avg_ms: 90,
			p50_ms: 84,
			p90_ms: 110,
			p99_ms: 125,
			server_telemetry: {
				samples: 3,
				messages_received: 3,
				avg_messages_received_per_sec: 4,
				avg_response_payload_kb: 1.1,
				response_payload_histogram: { '1-4 KB': 3 },
				response_size_classes: { small: 3 },
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[publish, requestReply, consume, publishTopic, consumeReplica, consumeOrderDLQ, consumeAuditDLQ]}
					metrics={{ p50_ms: 80, p90_ms: 120, p95_ms: 150, p99_ms: 210 }}
					history={[
						{ time: '10:00:00', vusers: 5, tps: 12, avgMs: 90, errorPct: 0, p50: 80, p90: 110, p99: 140, jmsConsumerLagMs: 18, jmsPeakConsumerLagMs: 24 },
						{ time: '10:00:30', vusers: 5, tps: 11, avgMs: 92, errorPct: 0, p50: 82, p90: 112, p99: 145, jmsConsumerLagMs: 32, jmsPeakConsumerLagMs: 40 },
						{ time: '10:01:00', vusers: 5, tps: 10, avgMs: 95, errorPct: 0, p50: 85, p90: 120, p99: 150, jmsConsumerLagMs: 45, jmsPeakConsumerLagMs: 52 },
					]}
				/>,
			)
		})

		expect(container.textContent).toContain('Live JMS Payload Hotspots')
		expect(container.textContent).toContain('Largest Request Payload')
		expect(container.textContent).toContain('PublishOrder')
		expect(container.textContent).toContain('6.50 KB')
		expect(container.textContent).toContain('Largest Response Payload')
		expect(container.textContent).toContain('RequestReplyQuote')
		expect(container.textContent).toContain('12.50 KB')
		expect(container.textContent).toContain('Heaviest Round Trip')
		expect(container.textContent).toContain('14.75 KB')
		expect(container.textContent).toContain('Slowest Transfer Stage')
		expect(container.textContent).toContain('42.0 ms')
		expect(container.textContent).toContain('orders.in')
		expect(container.textContent).toContain('quotes.rpc')
		expect(container.textContent).toContain('Live JMS Payload Distribution')
		expect(container.textContent).toContain('small')
		expect(container.textContent).toContain('medium')
		expect(container.textContent).toContain('17')
		expect(container.textContent).toContain('32')
		expect(container.textContent).toContain('Live JMS Payload Histogram')
		expect(container.textContent).toContain('0-1 KB')
		expect(container.textContent).toContain('1-4 KB')
		expect(container.textContent).toContain('4-16 KB')
		expect(container.textContent).toContain('13')
		expect(container.textContent).toContain('34')
		expect(container.textContent).toContain('Live JMS Messaging')
		expect(container.textContent).toContain('Highest Message Latency')
		expect(container.textContent).toContain('95.0 ms')
		expect(container.textContent).toContain('Fastest Producer Rate')
		expect(container.textContent).toContain('18.0 msg/s')
		expect(container.textContent).toContain('Fastest Consumer Rate')
		expect(container.textContent).toContain('24.0 msg/s')
		expect(container.textContent).toContain('Largest Message Volume')
		expect(container.textContent).toContain('20 msgs')
		expect(container.textContent).toContain('Live JMS Topic Fan-Out')
		expect(container.textContent).toContain('Widest Observed Fan-Out')
		expect(container.textContent).toContain('3.00x')
		expect(container.textContent).toContain('Highest Delivery Volume')
		expect(container.textContent).toContain('24 copies')
		expect(container.textContent).toContain('audit.events')
		expect(container.textContent).toContain('Live JMS Dead-Letter Activity')
		expect(container.textContent).toContain('Busiest Dead-Letter Destination')
		expect(container.textContent).toContain('7 msgs')
		expect(container.textContent).toContain('Highest Dead-Letter Error Rate')
		expect(container.textContent).toContain('28.6%')
		expect(container.textContent).toContain('Most Distinct DLQ Actions')
		expect(container.textContent).toContain('orders.dlq')
		expect(container.textContent).toContain('JMS Consumer Lag Trend')
		expect(container.textContent).toContain('Current lag is at the visible-window peak.')
		expect(container.textContent).toContain('Latest Avg Lag')
		expect(container.textContent).toContain('45.0 ms')
		expect(container.textContent).toContain('Window Peak')
	})

	it('renders WebSocket and gRPC stream telemetry when protocol message metrics are available', async () => {
		const websocketSend: TxMetrics = {
			name: 'WS_Send_Order',
			dependency_type: 'websocket',
			dependency_target: 'stream.example.test/ws',
			count: 24,
			errors: 0,
			avg_ms: 18,
			p50_ms: 14,
			p90_ms: 24,
			p99_ms: 36,
			server_telemetry: {
				samples: 24,
				messages_sent: 24,
				avg_messages_sent_per_sec: 48,
				avg_message_latency_ms: 18,
			},
		}
		const grpcStream: TxMetrics = {
			name: 'OrderService/Watch',
			dependency_type: 'grpc',
			dependency_target: 'grpc.example.test:443',
			count: 8,
			errors: 1,
			avg_ms: 42,
			p50_ms: 40,
			p90_ms: 56,
			p99_ms: 80,
			server_telemetry: {
				samples: 8,
				messages_sent: 8,
				messages_received: 32,
				avg_messages_sent_per_sec: 7.5,
				avg_messages_received_per_sec: 30,
				avg_message_latency_ms: 42,
			},
		}

		await act(async () => {
			root.render(
				<TestExecutionSummary
					transactions={[websocketSend, grpcStream]}
					metrics={{ p50_ms: 40, p90_ms: 56, p95_ms: 70, p99_ms: 80 }}
				/>,
			)
		})

		expect(container.textContent).toContain('Live WebSocket and gRPC Streams')
		expect(container.textContent).toContain('Message throughput, call latency, and protocol status')
		expect(container.textContent).toContain('Highest Message Latency')
		expect(container.textContent).toContain('OrderService/Watch')
		expect(container.textContent).toContain('42.0 ms')
		expect(container.textContent).toContain('Fastest Send Rate')
		expect(container.textContent).toContain('48.0 msg/s')
		expect(container.textContent).toContain('Fastest Receive Rate')
		expect(container.textContent).toContain('30.0 msg/s')
		expect(container.textContent).toContain('Largest Message Volume')
		expect(container.textContent).toContain('40 msgs')
		expect(container.textContent).toContain('WebSocket')
		expect(container.textContent).toContain('gRPC')
		expect(container.textContent).toContain('24')
		expect(container.textContent).toContain('32')
		expect(container.textContent).toContain('12.5% errors')
		expect(container.textContent).toContain('stream.example.test/ws')
		expect(container.textContent).toContain('grpc.example.test:443')
	})
})

function cssRule(css: string, selector: string) {
	const selectorIndex = css.indexOf(`${selector} {`)
	if (selectorIndex === -1) return ''
	const bodyStart = css.indexOf('{', selectorIndex) + 1
	const bodyEnd = css.indexOf('}', bodyStart)
	return css.slice(bodyStart, bodyEnd)
}
