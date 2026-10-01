export interface DiagnosticDistribution {
  count: number
  sum: number
  min: number
  max: number
  buckets: number[]
}


export interface ProtocolDiagnosticFinding {
  rule: string
  protocol: string
  measurement: string
  message: string
  observed: number
  samples: number
  advisory: boolean
}

export interface WebSocketDiagnostics {
  operation: string
  message_type?: string
  handshake_ms?: number
  reconnect_ms?: number
  send_ms?: number
  receive_ms?: number
  write_wait_ms?: number
  pending_writes: number
  reconnects: number
  messages_sent: number
  messages_received: number
  bytes_sent: number
  bytes_received: number
  ping_rtt_ms?: number[]
  close_code?: number
  disconnect_reason?: string
  callback_mode?: boolean
}

export interface WebSocketSummary {
  samples: number
  handshake_ms: DiagnosticDistribution
  reconnect_ms: DiagnosticDistribution
  send_ms: DiagnosticDistribution
  receive_ms: DiagnosticDistribution
  write_wait_ms: DiagnosticDistribution
  ping_rtt_ms: DiagnosticDistribution
  text_receive_ms: DiagnosticDistribution
  binary_receive_ms: DiagnosticDistribution
  reconnects: number
  messages_sent: number
  messages_received: number
  bytes_sent: number
  bytes_received: number
  max_pending_writes: number
  normal_closes: number
  abnormal_closes: number
  timeouts: number
  cancellations: number
}
