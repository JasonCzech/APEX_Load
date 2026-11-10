/**
 * HAR 1.2 export of a representative instance rebuilt from the run archive,
 * so its requests open in browser devtools or a HAR viewer.
 *
 * It is built only from the drill-down rows this viewer already received:
 * the archive keeps each request's action name, target host, status, timings
 * and (when sent) trace id, already redacted, but no request URL, method,
 * headers, cookies or bodies. Nothing is invented to fill those in: the
 * method is the action name's leading HTTP verb when it reads as a request
 * line ("GET /cart"; else empty), and the URL is the archived target when the
 * archive kept it as an http(s) URL (already scrubbed), else an `apexload:`
 * reference to the target and action, never a guessed http(s) address.
 */
import type { ArchiveTransactionWaterfall, ArchiveWaterfallRow } from '../../types/timeAnatomy'
import { harTimings } from '../waterfall/debugRequestExport'

export const REPRESENTATIVE_HAR_COMMENT = 'Rebuilt from the APEX Load run archive. The archive keeps each request\'s action name, target host, status and phase timings, not its URL, method, headers, cookies or bodies, so a request URL is the archived target only where the archive kept one as a URL, and otherwise an apexload: reference to the target and action.'

// An uppercase verb then a path or URL: "Get account" or "Options page" names no method.
const HTTP_METHOD = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE)\s+(?=\/|https?:\/\/)/
const HOST = /^[A-Za-z0-9.-]+(?::\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](?::\d{1,5})?$/
const HTTP_VERSION = /^(1\.0|1\.1|2|3)$/

const pos = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : 0)

export interface RepresentativeHarInstance {
  /** The page title in the HAR: "P95 Checkout". */
  title: string
  data: ArchiveTransactionWaterfall
}

/** The request's method: the action name's leading HTTP verb before a path or URL, or empty. */
export function harMethod(row: Pick<ArchiveWaterfallRow, 'action_name'>): string {
  return HTTP_METHOD.exec((row.action_name || '').trim())?.[1] ?? ''
}

/**
 * The request's URL: the archived target when it is itself an http(s) URL
 * (the archive scrubbed it), else an `apexload:` reference naming the target
 * host and the action.
 */
export function harUrl(row: Pick<ArchiveWaterfallRow, 'action_name' | 'dependency_target' | 'event_id'>): string {
  const target = (row.dependency_target || '').trim()
  if (/^https?:\/\//i.test(target)) {
    try {
      return new URL(target).toString()
    } catch {
      // Not a URL after all: fall through to a reference.
    }
  }
  const host = HOST.test(target) ? target.toLowerCase() : 'unknown-target'
  return `apexload://${host}/${encodeURIComponent((row.action_name || '').trim() || row.event_id || 'request')}`
}

function harQuery(url: string): { name: string; value: string }[] {
  if (!/^https?:/i.test(url)) return []
  try {
    return [...new URL(url).searchParams].map(([name, value]) => ({ name, value }))
  } catch {
    return []
  }
}

function harEntry(row: ArchiveWaterfallRow, pageref: string) {
  const time = pos(row.duration_ms)
  const p = row.phases
  const segments = [
    { key: 'dns', durationMs: pos(p?.dns_ms) },
    { key: 'connect', durationMs: pos(p?.connect_ms) },
    { key: 'tls', durationMs: pos(p?.tls_ms) },
    { key: 'wait', durationMs: pos(p?.server_ms) },
    { key: 'receive', durationMs: pos(p?.download_ms) },
    { key: 'total', durationMs: pos(p?.unsplit_ms) },
  ]
  const { timings, measured } = harTimings(segments, time, 'wait')
  const httpVersion = HTTP_VERSION.test(row.http_version || '') ? `HTTP/${row.http_version}` : ''
  const url = harUrl(row)
  const notes = [
    measured ? (pos(p?.unsplit_ms) > 0 ? 'No time to first byte was recorded: everything after connection setup is in wait.' : '') : 'Only the total time was recorded: it is all in wait.',
    row.clamped ? 'Phase timings were clamped to fit the request duration.' : '',
    row.retried ? 'The request was retried.' : '',
    row.interrupted ? 'The request was interrupted.' : '',
  ].filter(Boolean)
  return {
    pageref,
    startedDateTime: row.timestamp,
    time,
    request: {
      method: harMethod(row),
      url,
      httpVersion,
      cookies: [],
      headers: [],
      queryString: harQuery(url),
      headersSize: -1,
      bodySize: -1,
    },
    response: {
      status: row.status_code && row.status_code > 0 ? row.status_code : 0,
      statusText: '',
      httpVersion,
      cookies: [],
      headers: [],
      content: { size: 0, mimeType: '' },
      redirectURL: '',
      headersSize: -1,
      bodySize: -1,
      ...(row.success ? {} : { _error: row.error_message || row.error_class || 'failed' }),
    },
    cache: {},
    timings,
    ...(notes.length ? { comment: notes.join(' ') } : {}),
    _action_name: row.action_name,
    ...(row.dependency_type ? { _dependency_type: row.dependency_type } : {}),
    ...(row.connection ? { _connection: row.connection } : {}),
    ...(row.trace_id ? { _trace_id: row.trace_id } : {}),
  }
}

/** The rows a HAR lists for one instance: its requests, or the root itself when it is a request. */
function harRows(data: ArchiveTransactionWaterfall): ArchiveWaterfallRow[] {
  const requests = [...(data.requests || [])].sort((a, b) => pos(a.offset_ms) - pos(b.offset_ms) || a.ordinal - b.ordinal)
  if (requests.length) return requests
  return data.root.population === 'action' ? [data.root] : []
}

/** A HAR 1.2 log with one page per instance and its requests as entries. */
export function representativeHarLog(instances: readonly RepresentativeHarInstance[]) {
  const pages = instances.map(({ title, data }, index) => {
    const caveats = [
      data.truncated ? `Only the first ${data.requests.length} of ${data.requests_total} requests are included.` : '',
      data.partial ? 'The report did not retain every request of this transaction, so some may be missing.' : '',
      data.identity_quality === 'legacy' ? 'Legacy archive: requests from another VUser may appear.' : '',
    ].filter(Boolean)
    return {
      startedDateTime: data.root.timestamp,
      id: `page_${index + 1}`,
      title,
      pageTimings: { onContentLoad: -1, onLoad: pos(Math.max(pos(data.root.duration_ms), pos(data.root.elapsed_ms))) },
      ...(caveats.length ? { comment: caveats.join(' ') } : {}),
    }
  })
  return {
    log: {
      version: '1.2',
      creator: { name: 'APEX Load run report', version: '1' },
      pages,
      entries: instances.flatMap(({ data }, index) => harRows(data).map(row => harEntry(row, `page_${index + 1}`))),
      comment: REPRESENTATIVE_HAR_COMMENT,
    },
  }
}

/** A file name for the export: the instance label and transaction, safe on every file system. */
export function representativeHarFileName(title: string, eventId: string): string {
  const base = `${title}-${eventId.slice(-8)}`.replace(/[^A-Za-z0-9._-]+/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '').toLowerCase()
  return `${base || 'representative'}.har`
}
