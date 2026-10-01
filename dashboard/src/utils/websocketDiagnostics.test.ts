import { describe, expect, it } from 'vitest'
import { websocketDiagnosticLines, websocketSummaryLines } from './websocketDiagnostics'
import type { WebSocketDiagnostics, WebSocketSummary } from '../types/protocolDiagnostics'

describe('WebSocket diagnostics', () => {
  it('preserves unavailable timings and does not infer RTT from receiving a message', () => {
    const d: WebSocketDiagnostics = { operation: 'ws_receive', receive_ms: 0, messages_received: 1, messages_sent: 0, bytes_received: 2, bytes_sent: 0, pending_writes: 0, reconnects: 0 }
    const lines = websocketDiagnosticLines(d)
    expect(lines).toContain('Receive wait: 0.0 ms')
    expect(lines).toContain('Matched ping RTT: unavailable')
    expect(websocketDiagnosticLines({ ...d, ping_rtt_ms: [0, 2] })).toContain('Matched ping RTT: 0.0 ms, 2.0 ms')
  })
  it('uses the measurement denominator', () => {
    const empty = { count: 0, sum: 0, min: 0, max: 0, buckets: [] }
    const s: WebSocketSummary = { samples: 3, handshake_ms: { ...empty, count: 1, sum: 5, max: 5 }, reconnect_ms: empty, send_ms: empty, receive_ms: empty, write_wait_ms: empty, ping_rtt_ms: empty, text_receive_ms: empty, binary_receive_ms: empty, reconnects: 0, messages_sent: 0, messages_received: 0, bytes_sent: 0, bytes_received: 0, max_pending_writes: 0, normal_closes: 0, abnormal_closes: 0, timeouts: 0, cancellations: 0 }
    expect(websocketSummaryLines(s)).toContain('Handshake: 5.0 ms mean (1 measured)')
    expect(websocketSummaryLines(s)).toContain('Matched ping RTT: unavailable mean (0 measured)')
  })
})
