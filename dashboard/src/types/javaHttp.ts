import type { DiagnosticDistribution } from './protocolDiagnostics'

export interface JavaHTTPByteMoments { count: number; sum_bytes: number; min_bytes: number; max_bytes: number; overflow?: boolean }
export interface JavaHTTPBytePair { samples: number; numerator_bytes: number; denominator_bytes: number; overflow?: boolean }
export interface JavaHTTPSummary {
  version: number
  mode: string
  samples: number
  invalid_samples: number
  request_compression_none: number
  request_compression_gzip: number
  request_compression_deflate: number
  incomplete?: boolean
  sample_limit_reached?: boolean
  request_encoded: JavaHTTPByteMoments
  request_decoded: JavaHTTPByteMoments
  prepared_request_body: JavaHTTPByteMoments
  response_retained: JavaHTTPByteMoments
  response_base64: JavaHTTPByteMoments
  decoded_per_encoded: JavaHTTPBytePair
  prepared_per_decoded: JavaHTTPBytePair
  base64_per_retained: JavaHTTPBytePair
  base64_decode_ms: DiagnosticDistribution
  base64_encode_ms: DiagnosticDistribution
  enclosing_ms: DiagnosticDistribution
  base64_work: { samples: number; base64_ms: number; enclosing_ms: number }
  response_unavailable: number
  response_window: number
  response_discarded: number
  object_footprint: string
}
export interface JavaHTTPActions {
  version: number
  incomplete?: boolean
  unavailable_reason?: string
  actions?: Record<string, { population: number; diagnostics: JavaHTTPSummary }>
}
