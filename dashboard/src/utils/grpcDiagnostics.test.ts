import { describe, expect, it } from 'vitest'
import { DiagnosticDistribution } from '../types'
import { grpcClientAnalysis, grpcDiagnosticLines, grpcSummaryLines } from './grpcDiagnostics'

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


describe('gRPC client-call analysis', () => {
  const empty: DiagnosticDistribution = { count: 0, sum: 0, min: 0, max: 0, buckets: [] }
  const summary = { samples: 2, call_ms: { ...empty, count: 2, sum: 1000 }, encode_ms: { ...empty, count: 2, sum: 20 }, decode_ms: { ...empty, count: 2, sum: 10 }, first_message_ms: empty, deadline_budget_ms: empty, status_counts: [2], unavailable_status: 0, receive_limit_reached: 0, messages_sent: 4, messages_received: 2, request_bytes: 200, response_bytes: 150 }
  it('uses weighted call seconds and message counts, matching the Go analysis', () => {
    expect(grpcClientAnalysis(summary)).toEqual({ sentMessagesPerCallSecond: 4, receivedMessagesPerCallSecond: 2, sentBytesPerCallSecond: 200, receivedBytesPerCallSecond: 150, meanSentPayloadBytes: 50, meanReceivedPayloadBytes: 75, codecWorkToCallWallPct: 3 })
    expect(grpcSummaryLines(summary)).toContain('Client-call message rate: 4.00 sent/s / 2.00 received/s')
  })
  it('keeps independent missing evidence and measured zero', () => {
    const zero = grpcClientAnalysis({ ...summary, messages_received: 0, response_bytes: 0, decode_ms: empty })
    expect(zero.receivedMessagesPerCallSecond).toBe(0)
    expect(zero.meanReceivedPayloadBytes).toBeUndefined()
    expect(zero.codecWorkToCallWallPct).toBeUndefined()
    const partial = grpcClientAnalysis({ ...summary, call_ms: { ...summary.call_ms, count: 1 } })
    expect(partial.sentMessagesPerCallSecond).toBeUndefined()
    expect(partial.meanSentPayloadBytes).toBe(50)
    expect(grpcClientAnalysis({ ...summary, call_ms: { ...summary.call_ms, sum: 0 } }).sentMessagesPerCallSecond).toBeUndefined()
  })
  it('does not clamp overlapping codec work or accept unsafe evidence', () => {
    expect(grpcClientAnalysis({ ...summary, encode_ms: { ...summary.encode_ms, sum: 800 }, decode_ms: { ...summary.decode_ms, sum: 800 } }).codecWorkToCallWallPct).toBe(160)
    for (const sum of [NaN, Infinity, -1]) expect(grpcClientAnalysis({ ...summary, call_ms: { ...summary.call_ms, sum } }).sentMessagesPerCallSecond).toBeUndefined()
    expect(grpcClientAnalysis({ ...summary, request_bytes: 2 ** 53 }).sentBytesPerCallSecond).toBeUndefined()
    expect(grpcClientAnalysis({ ...summary, samples: 0 }).receivedMessagesPerCallSecond).toBeUndefined()
  })
})
