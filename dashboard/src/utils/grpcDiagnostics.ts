import { DiagnosticDistribution, GRPCDiagnostics, GRPCSummary } from '../types'

const statusNames = ['OK', 'Canceled', 'Unknown', 'Invalid argument', 'Deadline exceeded', 'Not found', 'Already exists', 'Permission denied', 'Resource exhausted', 'Failed precondition', 'Aborted', 'Out of range', 'Unimplemented', 'Internal', 'Unavailable', 'Data loss', 'Unauthenticated']

export function diagnosticMean(distribution: DiagnosticDistribution): number | undefined {
  return distribution.count > 0 ? distribution.sum / distribution.count : undefined
}

function ms(value: number | undefined): string {
  return value !== undefined && Number.isFinite(value) ? `${value.toFixed(1)} ms` : 'unavailable'
}

export function grpcSummaryLines(summary: GRPCSummary): string[] {
  const statuses = summary.status_counts.flatMap((count, code) => count > 0 ? [`${statusNames[code] ?? code}: ${count}`] : [])
  if (summary.unavailable_status > 0) statuses.push(`unavailable: ${summary.unavailable_status}`)
  const lines = [
    `${summary.samples} diagnostic sample${summary.samples === 1 ? '' : 's'} · ${summary.messages_sent} message${summary.messages_sent === 1 ? '' : 's'} sent / ${summary.messages_received} received`,
    `Client call: ${ms(diagnosticMean(summary.call_ms))} mean / ${ms(summary.call_ms.count ? summary.call_ms.max : undefined)} max`,
    `First response: ${ms(diagnosticMean(summary.first_message_ms))} (${summary.first_message_ms.count} measured)`,
    `Status · ${statuses.join(' / ') || 'unavailable'}`,
  ]
  if (summary.encode_ms || summary.decode_ms) lines.push(`Client protobuf codec per call: ${ms(summary.encode_ms ? diagnosticMean(summary.encode_ms) : undefined)} encode / ${ms(summary.decode_ms ? diagnosticMean(summary.decode_ms) : undefined)} decode`)
  if (summary.receive_limit_reached > 0) lines.push(`Receive limit reached: ${summary.receive_limit_reached}`)
  return lines
}

export function grpcDiagnosticLines(d: GRPCDiagnostics): string[] {
  const lines = [
    `gRPC ${d.method} (${d.mode})`,
    `Client call: ${ms(d.call_ms)}`,
    `First response: ${ms(d.first_message_ms)}`,
    `Client protobuf codec: ${ms(d.encode_ms)} encode / ${ms(d.decode_ms)} decode`,
    `Deadline budget at call start: ${ms(d.deadline_budget_ms)}`,
    `Final RPC status: ${d.status_code === undefined ? 'unavailable' : (statusNames[d.status_code] ?? d.status_code)}`,
    `Messages: ${d.messages_sent} sent / ${d.messages_received} received`,
    `Protobuf payload: ${d.request_bytes} bytes sent / ${d.response_bytes} bytes received`,
  ]
  if (d.receive_limit_reached) lines.push('Receive limit reached; remote completion was not observed.')
  return lines
}
