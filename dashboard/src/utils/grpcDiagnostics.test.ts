import { describe, expect, it } from 'vitest'
import { DiagnosticDistribution } from '../types'
import { grpcDiagnosticLines, grpcSummaryLines } from './grpcDiagnostics'

describe('gRPC diagnostic display', () => {
  it('distinguishes measured zero from unavailable timings and status', () => {
    const d = { method: '/example.Service/Call', mode: 'unary', call_ms: 1, messages_sent: 1, messages_received: 0, request_bytes: 0, response_bytes: 0 }
    expect(grpcDiagnosticLines(d)).toContain('First response: unavailable')
    expect(grpcDiagnosticLines(d)).toContain('Final RPC status: unavailable')
    expect(grpcDiagnosticLines({ ...d, first_message_ms: 0, status_code: 0 })).toContain('First response: 0.0 ms')
    expect(grpcDiagnosticLines({ ...d, status_code: 0 })).toContain('Final RPC status: OK')
    expect(grpcDiagnosticLines({ ...d, receive_limit_reached: true }).join(' ')).toContain('remote completion was not observed')
  })

  it('uses each timing measurement count instead of total calls', () => {
    const empty: DiagnosticDistribution = { count: 0, sum: 0, min: 0, max: 0, buckets: [] }
    const lines = grpcSummaryLines({ samples: 3, call_ms: { ...empty, count: 3, sum: 60, max: 30 }, first_message_ms: { ...empty, count: 1, sum: 5, max: 5 }, deadline_budget_ms: empty, status_counts: [1, 0, 0, 0, 1], unavailable_status: 1, receive_limit_reached: 1, messages_sent: 3, messages_received: 1, request_bytes: 3, response_bytes: 1 })
    expect(lines).toContain('Client call: 20.0 ms mean / 30.0 ms max')
    expect(lines).toContain('First response: 5.0 ms (1 measured)')
    expect(lines).toContain('Status · OK: 1 / Deadline exceeded: 1 / unavailable: 1')
  })
})
