/** Copy-as-cURL and single-request HAR export for a debug event. */
import type { BrowserReplayEvent } from '../../types/replay'
import type { DebugTimelineRowModel } from './debugEventModel'

export interface DebugRequestView {
  method: string
  url: string
  headers: Record<string, string>
  body?: string
  status?: number
  responseHeaders: Record<string, string>
  responseBody?: string
}

const HTTP_METHODS = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE)$/i

export function debugRequestView(event: BrowserReplayEvent): DebugRequestView | null {
  const s = event.snapshot
  const method = (s?.request_method || event.method || '').toUpperCase()
  const url = s?.request_url || event.target || ''
  if (!HTTP_METHODS.test(method) || !/^https?:\/\//i.test(url)) return null
  return {
    method,
    url,
    headers: { ...(s ? s.request_headers : event.headers) },
    body: s ? s.request_body : event.body,
    status: s?.response_status ?? event.status_code,
    responseHeaders: { ...(s?.response_headers || {}) },
    responseBody: s?.response_body,
  }
}

const shellQuote = (v: string) => `'${v.replace(/'/g, `'\\''`)}'`

export function toCurl(view: DebugRequestView): string {
  const parts = ['curl']
  // `-X HEAD` makes curl wait for a body that never comes; --head sends HEAD properly.
  if (view.method === 'HEAD') parts.push('--head')
  else if (view.method !== 'GET' || view.body) parts.push(`-X ${view.method}`)
  parts.push(shellQuote(view.url))
  for (const [k, v] of Object.entries(view.headers)) {
    // curl computes these itself; sending stale values breaks the request.
    // Accept-Encoding is replaced by --compressed so curl only asks for encodings it can decode.
    if (/^(content-length|host|accept-encoding)$/i.test(k)) continue
    // `Name:` with no value tells curl to remove the header; `Name;` sends it empty.
    parts.push(`-H ${shellQuote(String(v).trim() ? `${k}: ${v}` : `${k};`)}`)
  }
  if (view.body) parts.push(`--data-raw ${shellQuote(view.body)}`)
  if (Object.keys(view.headers).some(k => /^accept-encoding$/i.test(k))) parts.push('--compressed')
  return parts.join(' \\\n  ')
}

/** HAR wants a version token (HTTP/1.1, HTTP/2, HTTP/3), not a display label. */
export function harHttpVersion(label: string): string {
  const m = /^HTTP\/(1\.0|1\.1|2|3)\b/i.exec(label.trim())
  return m ? `HTTP/${m[1]}` : 'HTTP/1.1'
}

/** The event start shifted to a hop's start, as HAR's ISO timestamp. */
function hopStartedAt(eventStart: string, offsetMs: number): string {
  const ms = Date.parse(eventStart)
  return offsetMs > 0 && Number.isFinite(ms) ? new Date(ms + offsetMs).toISOString() : eventStart
}

const headerList = (h: Record<string, string>) => Object.entries(h).map(([name, value]) => ({ name, value }))

/** A HAR 1.2 log holding this one request, for browser devtools or HAR viewers. */
export function toHarLog(view: DebugRequestView, row: DebugTimelineRowModel) {
  // One entry for the final response: a redirect chain's earlier hops are not
  // folded into its timings.
  // The last hop of the document's own chain: typed sub-resources (images,
  // scripts) a navigation loaded alongside it are not the final response.
  const entries = row.event.network_entries?.length ? row.event.network_entries : row.event.snapshot?.network_entries ?? []
  const primary = row.hops.filter(h => {
    const type = entries[h.hopIndex]?.resource_type?.toLowerCase()
    return !type || type === 'document' || type === 'navigation'
  })
  const lastHop = row.hops.length > 1 && primary.length ? primary[primary.length - 1] : null
  const segments = lastHop ? lastHop.segments : row.segments
  const time = lastHop ? lastHop.durationMs : row.durationMs
  const sum = (key: string) => segments.filter(s => s.key === key).reduce((a, s) => a + s.durationMs, 0)
  // Measured when any real phase exists, whatever the reported source.
  const measured = segments.some(s => s.key !== 'total' && s.durationMs > 0)
  let timings: { blocked: number; dns: number; connect: number; ssl: number; send: number; wait: number; receive: number }
  if (measured) {
    timings = { blocked: sum('queueing'), dns: sum('dns') || -1, connect: sum('connect') + sum('tls') || -1, ssl: sum('tls') || -1, send: sum('send'), wait: sum('wait'), receive: sum('receive') }
    // HAR requires time = blocked + dns + connect + send + wait + receive (ssl is inside connect).
    const explained = Math.max(0, timings.blocked) + Math.max(0, timings.dns) + Math.max(0, timings.connect) + timings.send + timings.wait + timings.receive
    const rest = time - explained
    if (rest > 0) timings.blocked = Math.max(0, timings.blocked) + rest
  } else timings = { blocked: -1, dns: -1, connect: -1, ssl: -1, send: 0, wait: time, receive: 0 }
  let query: Array<{ name: string; value: string }> = []
  try {
    query = [...new URL(view.url).searchParams].map(([name, value]) => ({ name, value }))
  } catch {
    query = []
  }
  const mime = Object.entries(view.responseHeaders).find(([k]) => /^content-type$/i.test(k))?.[1] || ''
  const reqMime = Object.entries(view.headers).find(([k]) => /^content-type$/i.test(k))?.[1] || ''
  return {
    log: {
      version: '1.2',
      creator: { name: 'APEX Load debugger', version: '1' },
      entries: [
        {
          startedDateTime: hopStartedAt(row.event.started_at, lastHop ? lastHop.startOffsetMs - row.startOffsetMs : 0),
          time,
          request: {
            method: view.method,
            url: view.url,
            httpVersion: harHttpVersion(row.protocolLabel),
            headers: headerList(view.headers),
            queryString: query,
            cookies: [],
            headersSize: -1,
            bodySize: view.body ? view.body.length : 0,
            ...(view.body ? { postData: { mimeType: reqMime, text: view.body } } : {}),
          },
          response: {
            status: view.status ?? 0,
            statusText: '',
            httpVersion: harHttpVersion(row.protocolLabel),
            headers: headerList(view.responseHeaders),
            cookies: [],
            content: { size: view.responseBody?.length ?? 0, mimeType: mime, ...(view.responseBody ? { text: view.responseBody } : {}) },
            redirectURL: '',
            headersSize: -1,
            bodySize: view.responseBody?.length ?? -1,
          },
          cache: {},
          timings,
          comment: measured ? `Timing source: ${row.timingSource}` : 'Only the total time was retained.',
        },
      ],
    },
  }
}
