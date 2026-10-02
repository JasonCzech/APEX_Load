import { DiagnosticDistribution, GRPCDiagnostics, GRPCSummary } from '../types'

const statusNames = ['OK', 'Canceled', 'Unknown', 'Invalid argument', 'Deadline exceeded', 'Not found', 'Already exists', 'Permission denied', 'Resource exhausted', 'Failed precondition', 'Aborted', 'Out of range', 'Unimplemented', 'Internal', 'Unavailable', 'Data loss', 'Unauthenticated']

export function diagnosticMean(distribution: DiagnosticDistribution): number | undefined {
  return distribution.count > 0 ? distribution.sum / distribution.count : undefined
}

function ms(value: number | undefined): string {
  return value !== undefined && Number.isFinite(value) ? `${value.toFixed(1)} ms` : 'unavailable'
}

export interface GRPCClientAnalysis {
  sentMessagesPerCallSecond?: number
  receivedMessagesPerCallSecond?: number
  sentBytesPerCallSecond?: number
  receivedBytesPerCallSecond?: number
  meanSentPayloadBytes?: number
  meanReceivedPayloadBytes?: number
  codecWorkToCallWallPct?: number
}

export function grpcClientAnalysis(summary: GRPCSummary): GRPCClientAnalysis {
  const result: GRPCClientAnalysis = {}
  const countOK = (v: number) => Number.isSafeInteger(v) && v >= 0
  const sumOK = (v: number) => Number.isFinite(v) && v >= 0
  const ratio = (n: number, d: number): number | undefined => {
    const v = n / d
    return sumOK(n) && sumOK(d) && d > 0 && sumOK(v) ? v : undefined
  }
  if (!countOK(summary.samples) || summary.samples === 0) return result
  const sentOK = countOK(summary.messages_sent) && countOK(summary.request_bytes)
  const receivedOK = countOK(summary.messages_received) && countOK(summary.response_bytes)
  if (sentOK) result.meanSentPayloadBytes = ratio(summary.request_bytes, summary.messages_sent)
  if (receivedOK) result.meanReceivedPayloadBytes = ratio(summary.response_bytes, summary.messages_received)
  if (summary.call_ms.count !== summary.samples || !sumOK(summary.call_ms.sum) || summary.call_ms.sum <= 0) return result
  const seconds = summary.call_ms.sum / 1000
  if (sentOK) {
    result.sentMessagesPerCallSecond = ratio(summary.messages_sent, seconds)
    result.sentBytesPerCallSecond = ratio(summary.request_bytes, seconds)
  }
  if (receivedOK) {
    result.receivedMessagesPerCallSecond = ratio(summary.messages_received, seconds)
    result.receivedBytesPerCallSecond = ratio(summary.response_bytes, seconds)
  }
  if (summary.encode_ms?.count === summary.samples && summary.decode_ms?.count === summary.samples && sumOK(summary.encode_ms.sum) && sumOK(summary.decode_ms.sum)) {
    result.codecWorkToCallWallPct = ratio(100 * (summary.encode_ms.sum + summary.decode_ms.sum), summary.call_ms.sum)
  }
  return result
}

function measured(value: number | undefined, unit: string): string {
  return value === undefined ? 'unavailable' : `${value.toFixed(2)} ${unit}`
}

function clientAnalysisLines(summary: GRPCSummary): string[] {
  const a = grpcClientAnalysis(summary)
  return [
    `Client-call message rate: ${measured(a.sentMessagesPerCallSecond, 'sent/s')} / ${measured(a.receivedMessagesPerCallSecond, 'received/s')}`,
    `Client-call protobuf byte rate: ${measured(a.sentBytesPerCallSecond, 'sent bytes/s')} / ${measured(a.receivedBytesPerCallSecond, 'received bytes/s')}`,
    `Mean protobuf message payload: ${measured(a.meanSentPayloadBytes, 'sent bytes')} / ${measured(a.meanReceivedPayloadBytes, 'received bytes')}`,
    `Codec elapsed work / call wall time: ${measured(a.codecWorkToCallWallPct, '%')} (codec timings can overlap)`,
  ]
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
  lines.push(...clientAnalysisLines(summary))
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
