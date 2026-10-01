import type { WebSocketDiagnostics, WebSocketSummary } from '../types/protocolDiagnostics'
import { diagnosticMean } from './grpcDiagnostics'

const ms = (value?: number) => value !== undefined && Number.isFinite(value) ? `${value.toFixed(1)} ms` : 'unavailable'

export function websocketDiagnosticLines(d: WebSocketDiagnostics): string[] {
  const lines = [
    `WebSocket ${d.operation}${d.message_type ? ` (${d.message_type})` : ''}`,
    `Handshake: ${ms(d.handshake_ms)} · Reconnect: ${ms(d.reconnect_ms)}`,
    `Send: ${ms(d.send_ms)} · Write wait: ${ms(d.write_wait_ms)}`,
    `Receive wait: ${ms(d.receive_ms)}`,
    `Matched ping RTT: ${d.ping_rtt_ms?.length ? d.ping_rtt_ms.map(ms).join(', ') : 'unavailable'}`,
    `Messages: ${d.messages_sent} sent / ${d.messages_received} received`,
    `Payload: ${d.bytes_sent} bytes sent / ${d.bytes_received} bytes received`,
  ]
  if (d.disconnect_reason) lines.push(`Disconnect: ${d.disconnect_reason}${d.close_code !== undefined ? ` (${d.close_code})` : ''}`)
  return lines
}

export function websocketSummaryLines(s: WebSocketSummary): string[] {
  return [
    `${s.samples} diagnostic samples · ${s.messages_sent} messages sent / ${s.messages_received} received`,
    `Handshake: ${ms(diagnosticMean(s.handshake_ms))} mean (${s.handshake_ms.count} measured)`,
    `Reconnect: ${ms(diagnosticMean(s.reconnect_ms))} mean · ${s.reconnects} reconnects`,
    `Write wait: ${ms(diagnosticMean(s.write_wait_ms))} mean · ${s.max_pending_writes} peak pending writes`,
    `Receive wait: ${ms(diagnosticMean(s.receive_ms))} mean (${s.receive_ms.count} measured)`,
    `Matched ping RTT: ${ms(diagnosticMean(s.ping_rtt_ms))} mean (${s.ping_rtt_ms.count} measured)`,
    `Closes: ${s.normal_closes} normal / ${s.abnormal_closes} abnormal · ${s.timeouts} timeouts`,
  ]
}
