import type { BrowserReplayEvent, BrowserReplayNetworkEntry, ReplayHARPhase } from '../../types/replay'
import type { BrowserResourcePhase } from '../../types/browserResourceEvidence'
import { eventLocation } from '../debuggerExperience'
import { appliedReplayExtractions } from '../replayExtractionValues'

import {
  WATERFALL_LEGEND_PHASES,
  WATERFALL_PHASE_BY_KEY,
  WATERFALL_PHASE_ORDER,
  WATERFALL_PHASES,
  formatWaterfallMs,
  type WaterfallPhaseDescriptor,
  type WaterfallPhaseKey,
} from './phases'

export type DebugTimelineScaleMode = 'waterfall' | 'stacked'
export type DebugTimelinePhaseKey = WaterfallPhaseKey
export type DebugTimelinePhaseDescriptor = WaterfallPhaseDescriptor
export const DEBUG_TIMELINE_PHASES = WATERFALL_PHASES
export const DEBUG_TIMELINE_LEGEND_PHASES = WATERFALL_LEGEND_PHASES
const PHASE_BY_KEY = WATERFALL_PHASE_BY_KEY

export type DebugTimelineTimingSource = 'har' | 'browser' | 'protocol_evidence' | 'duration'

export interface DebugTimelineSegment {
  key: DebugTimelinePhaseKey
  label: string
  shortLabel: string
  startMs: number
  endMs: number
  durationMs: number
  /** Width percentage within the row's bar (0..100). */
  widthPct: number
  /** Offset percentage within the row's bar (0..100) for inspector mini-bars. */
  offsetPct: number
}

export interface DebugTimelineBreakdownItem {
  key: DebugTimelinePhaseKey
  label: string
  shortLabel: string
  category: 'connection' | 'request' | 'execution'
  durationMs: number
  startPct: number
  widthPct: number
  unavailableReason?: string
}

export interface DebugTimelineHopModel {
  hopIndex: number
  label: string
  method: string
  url: string
  statusCode?: number
  protocolLabel: string
  connectionReused: boolean
  startOffsetMs: number
  durationMs: number
  trackOffsetPct: number
  trackWidthPct: number
  segments: DebugTimelineSegment[]
}

export interface DebugTimelineRowModel {
  index: number
  id: string
  event: BrowserReplayEvent
  location: string
  actionName: string
  methodOrType: string
  target: string
  statusCode?: number
  protocolLabel: string
  connectionReused: boolean
  success: boolean
  errorMessage?: string
  extractedVariables: string[]
  startOffsetMs: number
  startedOffsetMs: number
  durationMs: number
  trackOffsetPct: number
  trackWidthPct: number
  timingSource: DebugTimelineTimingSource
  segments: DebugTimelineSegment[]
  breakdown: DebugTimelineBreakdownItem[]
  hops: DebugTimelineHopModel[]
}

export interface DebugTimelineModel {
  mode: DebugTimelineScaleMode
  sessionSpanMs: number
  /** Epoch ms that offsets are relative to; null when start times were unusable. */
  originMs: number | null
  rulerTicks: string[]
  rows: DebugTimelineRowModel[]
}

const AVAILABILITY_REASON_LABELS: Record<string, string> = {
  ip_literal: 'IP literal (no DNS)',
  in_transport_handshake: 'In QUIC transport handshake',
  cached: 'Served from cache',
  cache: 'Served from cache',
  service_worker: 'Served by service worker',
  not_exposed: 'Not exposed by runtime',
}

const PHASE_ALIAS: Record<string, DebugTimelinePhaseKey | undefined> = {
  blocked: 'queueing',
  stalled: 'queueing',
  ssl: 'tls',
  request_wait: 'wait',
  response: 'receive',
}
const mapPhase = (n?: string): DebugTimelinePhaseKey | undefined =>
  (n && (PHASE_ALIAS[n] || (n !== 'total' && n in PHASE_BY_KEY ? (n as DebugTimelinePhaseKey) : undefined))) || undefined

const PHASE_ORDER = WATERFALL_PHASE_ORDER

const PROTO_LABELS: Record<string, string | undefined> = {
  '3': 'HTTP/3 (QUIC)',
  '2': 'HTTP/2',
  '1.1': 'HTTP/1.1',
  '1': 'HTTP/1.1',
}

function resolveReason(code: string | undefined): string | undefined {
  const c = typeof code === 'string' ? code.trim() : ''
  if (!c || c === 'available') return undefined
  return AVAILABILITY_REASON_LABELS[c] || c.replace(/_/g, ' ').replace(/^./, ch => ch.toUpperCase())
}

const clamp = (v: number, min: number, max: number) => Math.min(max, Math.max(min, v))
const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const posNum = (v: unknown) => (isNum(v) && v > 0 ? v : 0)

export function parseTimestampMs(raw: string | undefined): number | null {
  if (!raw || typeof raw !== 'string' || /^0001-01-01T00:00:00/.test(raw)) return null
  const ms = Date.parse(raw)
  if (!Number.isFinite(ms)) return null
  const subMs = /\.(\d{3})(\d+)/.exec(raw)
  return subMs ? ms + Number(`0.${subMs[2]}`) : ms
}

export const formatTimelineDurationMs = formatWaterfallMs

function formatRulerTickMs(ms: number): string {
  const safe = posNum(ms)
  return safe === 0 ? '0 ms' : safe >= 10 && safe < 1000 ? `${Math.round(safe)} ms` : formatTimelineDurationMs(safe)
}

// 304 Not Modified (cache revalidation) and other 3xx answers are not followed.
const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308])

function isRedirectFollowUp(prev: BrowserReplayNetworkEntry, curr: BrowserReplayNetworkEntry): boolean {
  const prevEv = prev.browser_evidence
  const currEv = curr.browser_evidence
  if (prevEv && currEv) {
    // Browser evidence links hops explicitly: curr must continue prev's own
    // chain, so a redirected sub-resource whose earlier hop was not retained
    // does not join the document's chain.
    if ((currEv.redirect_hop ?? 0) !== (prevEv.redirect_hop ?? 0) + 1) return false
    return !currEv.redirect_from_request_id || !prevEv.request_id || currEv.redirect_from_request_id === prevEv.request_id
  }
  return REDIRECT_STATUSES.has(prev.status_code ?? 0)
}

function selectPrimaryNetworkChain(entries: readonly BrowserReplayNetworkEntry[], event: BrowserReplayEvent): BrowserReplayNetworkEntry[] {
  if (entries.length <= 1 || entries.every(e => !e.resource_type)) return entries.slice()
  const startIdx = Math.max(
    0,
    entries.findIndex(e => {
      const rt = e.resource_type?.toLowerCase()
      return !rt || rt === 'document' || rt === 'navigation' || Boolean(event.target && e.url === event.target)
    }),
  )
  const chain = [entries[startIdx]]
  for (let i = startIdx + 1; i < entries.length; i++) {
    if (!entries[i].resource_type || isRedirectFollowUp(chain[chain.length - 1], entries[i])) chain.push(entries[i])
    else break
  }
  return chain
}

interface RawSpan {
  key: DebugTimelinePhaseKey
  startMs: number
  endMs: number
}

const sumSpans = (spans: RawSpan[]) => spans.reduce((sum, s) => sum + s.endMs - s.startMs, 0)
const isEvReused = (ev?: BrowserReplayEvent['protocol_evidence']) =>
  ev?.quic_connection === 'reused' || ev?.timing_availability?.connect === 'reused_connection' || ev?.timing_availability?.dns === 'reused_connection'

function extractPhaseSpans(
  phases: ReadonlyArray<ReplayHARPhase | BrowserResourcePhase> | null | undefined,
): { spans: RawSpan[]; reasons: Partial<Record<DebugTimelinePhaseKey, string>> } {
  const reasons: Partial<Record<DebugTimelinePhaseKey, string>> = {}
  if (!phases?.length) return { spans: [], reasons }

  let rawSpans: RawSpan[] = []
  for (const p of phases) {
    const key = p && mapPhase(p.name)
    if (!key) continue
    const r = resolveReason(p.unavailable_reason)
    if (r) reasons[key] = r
    if (isNum(p.start_ms) && isNum(p.end_ms)) {
      const startMs = Math.max(0, p.start_ms)
      if (p.end_ms > startMs) rawSpans.push({ key, startMs, endMs: p.end_ms })
    }
  }
  const tls = rawSpans.find(s => s.key === 'tls')
  rawSpans = rawSpans.filter(s => {
    if (s.key === 'connect' && tls && s.endMs > tls.startMs && tls.startMs >= s.startMs) s.endMs = tls.startMs
    return s.endMs > s.startMs
  })
  if (!rawSpans.length) return { spans: [], reasons }

  rawSpans.sort((a, b) => a.startMs - b.startMs || PHASE_ORDER[a.key] - PHASE_ORDER[b.key])
  if (rawSpans[0].startMs >= 0.05 && !rawSpans.some(s => s.key === 'queueing')) {
    rawSpans.unshift({ key: 'queueing', startMs: 0, endMs: rawSpans[0].startMs })
  }
  return { spans: rawSpans, reasons }
}

function finalizeSegments(spans: RawSpan[], fallbackDurationMs: number): DebugTimelineSegment[] {
  if (!spans.length) {
    const dur = posNum(fallbackDurationMs)
    return [{ ...PHASE_BY_KEY.total, startMs: 0, endMs: dur, durationMs: dur, widthPct: 100, offsetPct: 0 }]
  }
  const total = sumSpans(spans)
  let cursor = 0
  return spans.map(s => {
    const durationMs = s.endMs - s.startMs
    const widthPct = total > 0 ? (durationMs / total) * 100 : 0
    const offsetPct = total > 0 ? (cursor / total) * 100 : 0
    cursor += durationMs
    return { ...PHASE_BY_KEY[s.key], startMs: s.startMs, endMs: s.endMs, durationMs, widthPct, offsetPct }
  })
}

function inferProtocolLabel(event: BrowserReplayEvent, entry?: BrowserReplayNetworkEntry): string {
  const ev = event.protocol_evidence
  const proto = ev?.http_version_negotiated || entry?.browser_evidence?.protocol
  const mapped = proto && PROTO_LABELS[proto.replace(/^h/, '')]
  if (mapped) return mapped
  if (entry?.trace?.http_protocol) return entry.trace.http_protocol
  if (typeof ev?.protocol === 'string' && ev.protocol.trim()) return ev.protocol.trim().toUpperCase()
  return /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)$/i.test(entry?.method || event.method || '') ? 'HTTP' : event.action_type || 'action'
}

function inferConnectionReused(event: BrowserReplayEvent, entry: BrowserReplayNetworkEntry | undefined, spans: RawSpan[]): boolean {
  if (spans.some(s => s.key === 'connect' || s.key === 'tls')) return false
  // true: pooled socket; false: fresh socket; absent: no connection observed.
  if (typeof entry?.trace?.connection_reused === 'boolean') return entry.trace.connection_reused
  if (isEvReused(event.protocol_evidence)) return true
  const phases = entry?.har?.phases
  return Boolean(phases?.length) && spans.some(s => s.key === 'send' || s.key === 'wait' || s.key === 'receive') && !phases!.some(p => p && ['dns', 'connect', 'tls'].includes(mapPhase(p.name) || ''))
}

function buildBreakdownItems(
  segments: DebugTimelineSegment[],
  timingSource: 'har' | 'browser' | 'protocol_evidence' | 'duration',
  event: BrowserReplayEvent,
  entry: BrowserReplayNetworkEntry | undefined,
  phaseReasons: Partial<Record<DebugTimelinePhaseKey, string>>,
  connectionReused: boolean,
): DebugTimelineBreakdownItem[] {
  if (timingSource === 'duration' && segments.every(s => s.key === 'total')) {
    const durationMs = segments.reduce((sum, s) => sum + s.durationMs, 0)
    return [{ ...PHASE_BY_KEY.total, durationMs, startPct: 0, widthPct: durationMs > 0 ? 100 : 0 }]
  }

  const byPhase = new Map<DebugTimelinePhaseKey, number>()
  for (const s of segments) byPhase.set(s.key, (byPhase.get(s.key) ?? 0) + s.durationMs)

  const ev = event.protocol_evidence
  const avail = ev?.timing_availability ?? {}
  const reasons: Partial<Record<DebugTimelinePhaseKey, string>> = { ...phaseReasons }
  for (const [k, raw] of [['dns', avail.dns], ['connect', avail.connect], ['tls', avail.tls], ['wait', avail.ttfb]] as const) {
    const r = resolveReason(raw)
    if (r) reasons[k] = r
  }
  const wireKeys: DebugTimelinePhaseKey[] = ['dns', 'connect', 'tls', 'send', 'wait']
  const fill = (ks: DebugTimelinePhaseKey[], label: string) => {
    for (const k of ks) reasons[k] = reasons[k] || label
  }
  if (ev?.tls_in_transport_handshake === true) fill(['tls'], AVAILABILITY_REASON_LABELS.in_transport_handshake)
  if (entry?.browser_evidence?.service_worker === true) fill(wireKeys, AVAILABILITY_REASON_LABELS.service_worker)
  if (ev?.http_version_source === 'cache' || entry?.browser_evidence?.cache === true) fill(wireKeys, AVAILABILITY_REASON_LABELS.cached)
  if (connectionReused) fill(['dns', 'connect', 'tls'], 'Reused connection')
  if (/^http:\/\//i.test(String(entry?.url || event.snapshot?.request_url || event.target || '').trim())) fill(['tls'], 'Cleartext HTTP')

  const keys: DebugTimelinePhaseKey[] = ['queueing', ...wireKeys, 'receive']
  const totalPos = keys.reduce((sum, k) => sum + (byPhase.get(k) ?? 0), 0)
  let cursor = 0
  return keys.map(key => {
    const durationMs = byPhase.get(key) ?? 0
    const startPct = clamp(totalPos > 0 ? (cursor / totalPos) * 100 : 0, 0, 98)
    const widthPct = durationMs > 0 && totalPos > 0 ? clamp((durationMs / totalPos) * 100, 2, 100 - startPct) : 0
    if (durationMs > 0) cursor += durationMs
    return { ...PHASE_BY_KEY[key], durationMs, startPct, widthPct, unavailableReason: durationMs === 0 ? reasons[key] : undefined }
  })
}

function extractTimingsFromProtocolEvidence(event: BrowserReplayEvent, redirectedChain: boolean): { spans: RawSpan[]; hasEvidence: boolean } {
  const ev = event.protocol_evidence
  const t = ev?.timings
  const hasMeta = ev?.http_version_source === 'cache' || Boolean(ev?.timing_availability && Object.keys(ev.timing_availability).length)
  if (!t || typeof t !== 'object') return { spans: [], hasEvidence: hasMeta }
  // One trace spans the whole redirect chain: TTFB is the first hop's first
  // byte while DNS/connect/TLS come from the last hop that dialed, so the
  // action total cannot be split into phases. Report the total only.
  if (redirectedChain || posNum(t.redirect_count) > 0) return { spans: [], hasEvidence: false }
  const dnsMs = posNum(t.dns_ms)
  const connectMs = posNum(t.connect_ms)
  const tlsMs = posNum(t.tls_ms)
  const rawTtfbMs = posNum(t.ttfb_ms)
  const durationMs = Math.max(posNum(t.duration_ms), posNum(event.duration_ms))
  const dialMs = dnsMs + connectMs + tlsMs
  const waitMs =
    rawTtfbMs > 0
      ? rawTtfbMs >= dialMs
        ? rawTtfbMs - dialMs
        : rawTtfbMs
      : (dialMs > 0 || isEvReused(ev)) && durationMs > dialMs
        ? durationMs - dialMs
        : 0
  if (!(dnsMs > 0 || connectMs > 0 || tlsMs > 0 || waitMs > 0)) return { spans: [], hasEvidence: hasMeta }
  const receiveMs = rawTtfbMs > 0 ? Math.max(0, durationMs - (dialMs + waitMs)) : 0
  const spans: RawSpan[] = []
  let cursor = 0
  for (const [key, ms] of [['dns', dnsMs], ['connect', connectMs], ['tls', tlsMs], ['wait', waitMs], ['receive', receiveMs]] as const) {
    if (ms > 0) {
      spans.push({ key, startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  return { spans, hasEvidence: true }
}

function computeTrackBounds(startOffsetMs: number, durationMs: number, sessionSpanMs: number, mode: DebugTimelineScaleMode) {
  if (mode !== 'waterfall') return { trackOffsetPct: 0, trackWidthPct: 100 }
  const trackOffsetPct = clamp((startOffsetMs / sessionSpanMs) * 100, 0, 98)
  const trackWidthPct = durationMs > 0 ? clamp((durationMs / sessionSpanMs) * 100, 2, 100 - trackOffsetPct) : 1.5
  return { trackOffsetPct, trackWidthPct }
}

export function buildDebugTimelineModel(
  events: readonly BrowserReplayEvent[],
  mode: DebugTimelineScaleMode = 'waterfall',
): DebugTimelineModel {
  const list = Array.isArray(events) ? events : []
  const parsedStarts = list.map(ev => parseTimestampMs(ev.started_at))
  const validStarts = parsedStarts.filter((t): t is number => t !== null)
  const minStartMs = validStarts.length > 0 ? Math.min(...validStarts) : null
  const allSameStart = list.length > 1 && new Set(validStarts).size <= 1

  let cumulativeCursorMs = 0
  const rawOffsetsMs: number[] = list.map((ev, idx) => {
    const dur = posNum(ev.duration_ms)
    if (!allSameStart && minStartMs !== null && parsedStarts[idx] !== null) {
      const start = Math.max(0, parsedStarts[idx]! - minStartMs)
      cumulativeCursorMs = Math.max(cumulativeCursorMs, start + dur)
      return start
    }
    const start = cumulativeCursorMs
    cumulativeCursorMs += dur
    return start
  })

  const preliminaryRows = list.map((ev, index) => {
    const entries: BrowserReplayNetworkEntry[] = ev.network_entries?.length ? ev.network_entries : ev.snapshot?.network_entries ?? []
    const primaryChain = selectPrimaryNetworkChain(entries, ev)
    const primaryEntry = primaryChain[primaryChain.length - 1] ?? entries[0]
    const startOffsetMs = rawOffsetsMs[index]

    let hopCursorMs = startOffsetMs
    const hops = entries.map((entry, hopIdx) => {
      const harEx = extractPhaseSpans(entry.har?.phases)
      const hopSpans = harEx.spans.length > 0 ? harEx.spans : extractPhaseSpans(entry.browser_evidence?.phases).spans
      const hopDurationMs = Math.max(posNum(entry.duration_ms), sumSpans(hopSpans))
      const hopEntryStartMs = parseTimestampMs(entry.har?.started_at)
      const docOffset = entry.browser_evidence?.document_offset_ms
      const epochStartMs = entry.browser_evidence?.epoch_start_ms
      // Sequential hops (untyped entries, redirect follow-ups) start after the
      // previous hop; typed sub-resources load concurrently from the event start.
      const minHopOffsetMs = hopIdx > 0 && (!entry.resource_type || isRedirectFollowUp(entries[hopIdx - 1], entry)) ? hopCursorMs : startOffsetMs
      const canUseAbsoluteStart = !allSameStart && minStartMs !== null
      const hopStartOffsetMs =
        canUseAbsoluteStart && hopEntryStartMs !== null && hopEntryStartMs >= minStartMs
          ? Math.max(minHopOffsetMs, hopEntryStartMs - minStartMs)
          : isNum(docOffset) && docOffset >= 0
            ? Math.max(minHopOffsetMs, startOffsetMs + docOffset)
            : canUseAbsoluteStart && isNum(epochStartMs) && epochStartMs >= minStartMs
              ? Math.max(minHopOffsetMs, epochStartMs - minStartMs)
              : minHopOffsetMs
      hopCursorMs = Math.max(hopCursorMs, hopStartOffsetMs + hopDurationMs)
      const method = entry.method || ev.method || ev.action_type || 'GET'
      const url = entry.url || ev.target
      return {
        hopIndex: hopIdx,
        label: `Hop ${hopIdx + 1} · ${entry.status_code ?? '—'} ${method} ${url}`,
        method,
        url,
        statusCode: entry.status_code,
        protocolLabel: inferProtocolLabel(ev, entry),
        connectionReused: inferConnectionReused(ev, entry, hopSpans),
        startOffsetMs: hopStartOffsetMs,
        durationMs: hopDurationMs,
        segments: finalizeSegments(hopSpans, hopDurationMs),
      }
    })

    let rowSpans: RawSpan[] = []
    let phaseReasons: Partial<Record<DebugTimelinePhaseKey, string>> = {}
    let timingSource: 'har' | 'browser' | 'protocol_evidence' | 'duration' = 'duration'
    const hasHar = primaryChain.some(e => e.har?.phases?.length)
    const hasBrowser = !hasHar && primaryChain.some(e => e.browser_evidence?.phases?.length)

    if (hasHar || hasBrowser) {
      timingSource = hasHar ? 'har' : 'browser'
      let spanOffset = 0
      for (const entry of primaryChain) {
        const ex = extractPhaseSpans(hasHar ? entry.har?.phases : entry.browser_evidence?.phases)
        phaseReasons = { ...phaseReasons, ...ex.reasons }
        let maxEnd = 0
        for (const s of ex.spans) {
          rowSpans.push({ key: s.key, startMs: spanOffset + s.startMs, endMs: spanOffset + s.endMs })
          if (s.endMs > maxEnd) maxEnd = s.endMs
        }
        spanOffset += Math.max(maxEnd, posNum(entry.duration_ms))
      }
    }

    if (!rowSpans.length) {
      const fromEv = extractTimingsFromProtocolEvidence(ev, primaryChain.length > 1)
      if (fromEv.hasEvidence) {
        rowSpans = fromEv.spans
        timingSource = 'protocol_evidence'
      }
    }

    const durationMs = Math.max(posNum(ev.duration_ms), sumSpans(rowSpans))
    const segments = finalizeSegments(rowSpans, durationMs)
    const initialQueueingMs = segments[0]?.key === 'queueing' ? segments[0].durationMs : 0
    const connectionReused = inferConnectionReused(ev, primaryEntry, rowSpans)
    const protocolLabel = inferProtocolLabel(ev, primaryEntry)

    return {
      index,
      id: ev.event_id || `ev-${index}`,
      event: ev,
      location: eventLocation(ev),
      actionName: ev.action_name,
      methodOrType: (ev.method || primaryEntry?.method || ev.action_type || 'action').toUpperCase(),
      target: ev.target || primaryEntry?.url || ev.action_name,
      statusCode: ev.status_code ?? primaryEntry?.status_code,
      protocolLabel,
      connectionReused,
      success: ev.success,
      errorMessage: ev.error_message,
      extractedVariables: Object.keys(appliedReplayExtractions(ev)),
      startOffsetMs,
      startedOffsetMs: startOffsetMs + initialQueueingMs,
      durationMs,
      timingSource,
      segments,
      breakdown: buildBreakdownItems(segments, timingSource, ev, primaryEntry, phaseReasons, connectionReused),
      hops,
    }
  })

  const sessionSpanMs = list.length
    ? Math.max(1, ...preliminaryRows.flatMap(r => [r.startOffsetMs + r.durationMs, ...r.hops.map(h => h.startOffsetMs + h.durationMs)]))
    : 0
  const rows: DebugTimelineRowModel[] = preliminaryRows.map(row => ({
    ...row,
    ...computeTrackBounds(row.startOffsetMs, row.durationMs, sessionSpanMs, mode),
    hops: row.hops.map(hop => ({
      ...hop,
      ...computeTrackBounds(hop.startOffsetMs, hop.durationMs, sessionSpanMs, mode),
    })),
  }))

  const tickSpan = sessionSpanMs || 100
  const rulerTicks = [0, 0.25, 0.5, 0.75, 1].map(f => (mode === 'stacked' ? `${f * 100}%` : formatRulerTickMs(tickSpan * f)))

  return { mode, sessionSpanMs, originMs: allSameStart ? null : minStartMs, rulerTicks, rows }
}
