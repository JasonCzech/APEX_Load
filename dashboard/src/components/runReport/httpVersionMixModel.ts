import { formatPercentValue } from '../../utils/numberFormat'
import type { HTTPVersionMix, HTTPVersionRatio, RunArchiveHTTPVersionMix, RunArchiveNetworkPhases, RunArchiveNetworkPhaseSummary } from '../../types'

/** Mix buckets in display order; the report exports use the same labels. */
export const HTTP_VERSION_BUCKETS: ReadonlyArray<{ key: string; label: string; short: string }> = [
  { key: '1.1', label: 'HTTP/1.1', short: 'HTTP/1.1' },
  { key: '2', label: 'HTTP/2', short: 'HTTP/2' },
  { key: '3', label: 'HTTP/3', short: 'HTTP/3' },
  { key: 'unknown', label: 'No response (version unknown)', short: 'no response' },
  { key: 'cache', label: 'Served from cache', short: 'cache' },
]

// Reader labels for the bounded routing, fallback and QUIC error tokens.
const REASON_LABELS: Record<string, string> = {
  upgraded: 'Upgraded to HTTP/3',
  not_advertised: 'TCP: HTTP/3 not yet advertised',
  broken_cache: 'TCP: HTTP/3 failed recently',
  tls_policy: 'TCP: TLS maximum below 1.3',
  proxy_route: 'Proxy route',
  integrated_auth: 'Integrated authentication',
  exact_policy: 'Exact HTTP/3 policy',
  quic_connect_failed: 'QUIC could not connect',
  h3_version_fallback: 'Server asked for an earlier HTTP version',
  misdirected_request: '421 Misdirected Request',
  timeout: 'QUIC timeout',
  handshake: 'QUIC handshake failed',
  version_negotiation: 'QUIC version negotiation failed',
  connection_closed: 'QUIC connection closed',
  request_rejected: 'Request rejected unprocessed',
  possibly_sent: 'Possibly sent before failing',
  version_fallback: 'HTTP/3 version fallback',
}

export function reasonLabel(token: string): string {
  if (Object.prototype.hasOwnProperty.call(REASON_LABELS, token)) return REASON_LABELS[token]
  const words = token.replace(/_/g, ' ').trim()
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : 'Not available'
}

const count = (value: unknown): number => typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0

export function usedHTTP3(mix: HTTPVersionMix | undefined): boolean {
  return !!mix && (count(mix.http3_policies) > 0 || count(mix.by_version?.['3']) > 0 || count(mix.quic_attempted) > 0)
}

export type VersionMixRow = { key: string; label: string; short: string; count: number; sharePct: number }

export function versionMixRows(mix: HTTPVersionMix | undefined): VersionMixRow[] {
  const total = count(mix?.actions)
  if (!mix || total === 0) return []
  return HTTP_VERSION_BUCKETS.flatMap(bucket => {
    const value = count(mix.by_version?.[bucket.key])
    return value > 0 ? [{ ...bucket, count: value, sharePct: value * 100 / total }] : []
  })
}

// A share as the app states percentages; one that rounds to zero but is not
// zero reads "<0.01%".
function ratioPercent(value: number): string {
  return formatPercentValue(value * 100)
}

/** A ratio with its denominator, or why it has none. */
export function ratioText(ratio: HTTPVersionRatio | undefined, noun: string): string {
  if (!ratio || count(ratio.denominator) === 0) return `n/a (no ${noun})`
  return `${ratioPercent(ratio.value)} (${ratio.numerator.toLocaleString()} of ${ratio.denominator.toLocaleString()} ${noun})`
}

/** The same ratio split for a metric tile: the share, and its counts. */
export function ratioParts(ratio: HTTPVersionRatio | undefined, noun: string): { value: string; detail: string } {
  if (!ratio || count(ratio.denominator) === 0) return { value: 'n/a', detail: `no ${noun}` }
  return { value: ratioPercent(ratio.value), detail: `${ratio.numerator.toLocaleString()} of ${ratio.denominator.toLocaleString()} ${noun}` }
}

export function countText(counts: Record<string, number> | undefined): string {
  return Object.entries(counts ?? {})
    .filter(([, value]) => count(value) > 0)
    .sort(([a, left], [b, right]) => right - left || a.localeCompare(b))
    .map(([key, value]) => `${reasonLabel(key)} (${value})`)
    .join(', ')
}

/** One line for the answer-first header, e.g. "HTTP/3 75% · HTTP/2 25%". */
export function versionMixHeadline(mix: HTTPVersionMix | undefined): string {
  return versionMixRows(mix).map(row => `${row.short} ${formatShare(row.sharePct)}`).join(' · ')
}

function formatShare(pct: number): string {
  if (pct > 0 && pct < 1) return '<1%'
  return `${Math.round(pct)}%`
}

export type VersionPhaseRow = { key: string; label: string; phases: RunArchiveNetworkPhases }

export function versionPhaseRows(summary: RunArchiveNetworkPhaseSummary | undefined): VersionPhaseRow[] {
  return HTTP_VERSION_BUCKETS.flatMap(bucket => {
    const phases = summary?.by_http_version?.[bucket.key]
    return phases && count(phases.count) > 0 ? [{ key: bucket.key, label: bucket.label, phases }] : []
  })
}

export type VersionMixView = {
  mix: RunArchiveHTTPVersionMix
  rows: VersionMixRow[]
  usedHTTP3: boolean
  fallback: string
  quicFailure: string
  routing: string
}

export function versionMixView(summary: RunArchiveNetworkPhaseSummary | undefined): VersionMixView | null {
  const mix = summary?.version_mix
  if (!mix || count(mix.actions) === 0) return null
  return {
    mix,
    rows: versionMixRows(mix),
    usedHTTP3: usedHTTP3(mix),
    fallback: ratioText(mix.fallback_ratio, 'upgrade-eligible auto+h3 actions'),
    quicFailure: ratioText(mix.quic_failure_ratio, 'QUIC attempts'),
    routing: countText(mix.by_selection),
  }
}
