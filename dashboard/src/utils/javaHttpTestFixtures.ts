import type { JavaHTTPSummary } from '../types/javaHttp'

export function javaHTTPFixture(): JavaHTTPSummary {
  const bytes = (n: number) => ({ count: 1, sum_bytes: n, min_bytes: n, max_bytes: n })
  const duration = (n: number) => ({ count: 1, sum: n, min: n, max: n, buckets: [1, ...Array(143).fill(0)] })
  return { version: 1, mode: 'embedded_opaque_bytes', samples: 1, invalid_samples: 0, request_compression_none: 1, request_compression_gzip: 0, request_compression_deflate: 0,
    request_encoded: bytes(8), request_decoded: bytes(4), prepared_request_body: bytes(4), response_retained: bytes(0), response_base64: bytes(0),
    decoded_per_encoded: { samples: 1, numerator_bytes: 4, denominator_bytes: 8 }, prepared_per_decoded: { samples: 1, numerator_bytes: 4, denominator_bytes: 4 }, base64_per_retained: { samples: 1, numerator_bytes: 0, denominator_bytes: 0 },
    base64_decode_ms: duration(0), base64_encode_ms: duration(0), enclosing_ms: duration(1), base64_work: { samples: 1, base64_ms: 0, enclosing_ms: 1 },
    response_unavailable: 0, response_window: 0, response_discarded: 0, object_footprint: 'unavailable_embedded_opaque_bytes' }
}
