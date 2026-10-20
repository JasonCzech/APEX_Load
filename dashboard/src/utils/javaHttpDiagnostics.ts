import { METRIC_LABELS } from '../metricLabels'
import type { JavaHTTPActions, JavaHTTPByteMoments, JavaHTTPBytePair, JavaHTTPSummary } from '../types/javaHttp'

const count = (n: number) => Number.isSafeInteger(n) && n >= 0
const finite = (n: number) => Number.isFinite(n) && n >= 0
const number = (n: number) => n.toLocaleString('en-US', { maximumFractionDigits: 3 })
const unavailable = 'Unavailable'

export function javaHTTPMeanBytes(m?: JavaHTTPByteMoments): number | undefined {
  if (!m || m.overflow || !count(m.count) || m.count === 0 || !count(m.sum_bytes) || !count(m.min_bytes) || !count(m.max_bytes) || m.min_bytes > m.max_bytes) return
  const mean = m.sum_bytes / m.count
  return mean >= m.min_bytes && mean <= m.max_bytes ? mean : undefined
}
export function javaHTTPPairedRatio(p?: JavaHTTPBytePair): number | undefined {
  if (!p || p.overflow || !count(p.samples) || p.samples === 0 || !count(p.numerator_bytes) || !count(p.denominator_bytes) || p.denominator_bytes === 0) return
  return p.numerator_bytes / p.denominator_bytes
}
export function javaHTTPBase64WorkShare(s: JavaHTTPSummary): number | undefined {
  const p = s.base64_work
  if (!p || !count(p.samples) || p.samples === 0 || p.samples > s.samples || !finite(p.base64_ms) || !finite(p.enclosing_ms) || p.enclosing_ms <= 0 || p.base64_ms > p.enclosing_ms * (1 + 1e-12)) return
  return Math.min(1, p.base64_ms / p.enclosing_ms)
}

export function javaHTTPSummaryLines(s: JavaHTTPSummary, population: number): string[] {
  if (!s || s.version !== 1 || s.mode !== 'embedded_opaque_bytes' || s.object_footprint !== 'unavailable_embedded_opaque_bytes' || !count(population) || !count(s.samples) || s.samples > population) return ['Diagnostics unavailable: unsupported or inconsistent evidence.']
  const lines = [`Observed diagnostics: ${number(s.samples)} of ${number(population)} action executions.`]
  lines.push(`Prepared request compression: none ${count(s.request_compression_none) ? number(s.request_compression_none) : unavailable}; gzip ${count(s.request_compression_gzip) ? number(s.request_compression_gzip) : unavailable}; deflate ${count(s.request_compression_deflate) ? number(s.request_compression_deflate) : unavailable} observed actions. Mixed compression is not a single comparison cohort.`)
  if (s.incomplete || s.sample_limit_reached || s.samples < population || s.invalid_samples > 0) lines.push('Coverage incomplete; retained measurements describe only the observed actions.')
  for (const [key, m] of [
    ['javaAuthoredBase64', s.request_encoded], ['javaDecodedRequest', s.request_decoded], ['javaPreparedRequest', s.prepared_request_body],
    ['javaRetainedResponse', s.response_retained], ['javaBase64Response', s.response_base64],
  ] as const) {
    const metric = METRIC_LABELS[key], mean = javaHTTPMeanBytes(m)
    lines.push(`${metric.label}: ${mean === undefined ? unavailable : `${number(mean)} ${metric.unit} mean; ${number(m.max_bytes)} ${metric.unit} max; ${number(m.count)} observed`}${m?.overflow ? ' (exact sum limit reached)' : ''}`)
  }
  for (const [label, pair] of [['Decoded / base64 input', s.decoded_per_encoded], ['Prepared / decoded request', s.prepared_per_decoded], ['Base64 / retained response', s.base64_per_retained]] as const) {
    const ratio = javaHTTPPairedRatio(pair)
    lines.push(`${label}: ${ratio === undefined ? unavailable : `${number(ratio)} (${number(pair.samples)} paired observations)`}`)
  }
  const share = javaHTTPBase64WorkShare(s)
  lines.push(`${METRIC_LABELS.javaBase64Work.label}: ${share === undefined ? unavailable : `${number(share * 100)} ${METRIC_LABELS.javaBase64Work.unit} (${number(s.base64_work.samples)} paired observations)`}`)
  for (const [label, d] of [['Base64 decoding', s.base64_decode_ms], ['Base64 encoding', s.base64_encode_ms], [METRIC_LABELS.javaEnclosing.label, s.enclosing_ms]] as const) {
    const valid = d && count(d.count) && d.count > 0 && d.count <= s.samples && finite(d.sum)
    lines.push(`${label}: ${valid ? `${number(d.sum / d.count)} ${METRIC_LABELS.javaEnclosing.unit} mean; ${number(d.count)} observed` : unavailable}`)
  }
  lines.push(`Response availability: ${count(s.response_unavailable) ? number(s.response_unavailable) : unavailable} unavailable; ${count(s.response_window) ? number(s.response_window) : unavailable} limited to a collection window; ${count(s.response_discarded) ? number(s.response_discarded) : unavailable} explicitly discarded.`)
  return lines
}

// Historical observations retain their own java_http action denominator, which
// excludes wrappers and other protocols' same-named actions; absent older fields
// are not reconstructed from generic transaction counters.
export function javaHTTPActionsFromArchive(actions?: Record<string, { transactions: number; java_http?: JavaHTTPSummary }>): JavaHTTPActions | undefined {
  const measured = Object.entries(actions ?? {}).flatMap(([name, action]) => action.java_http ? [[name, action.java_http] as const] : [])
  if (!measured.length) return
  if (measured.some(([, summary]) => summary.population === undefined || !count(summary.population))) return { version: 1, incomplete: true, unavailable_reason: 'malformed' }
  return { version: 1, actions: Object.fromEntries(measured.map(([name, summary]) => [name, { population: summary.population!, diagnostics: summary }])) }
}
