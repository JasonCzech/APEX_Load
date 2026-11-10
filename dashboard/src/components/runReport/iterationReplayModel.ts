import type { BusinessKeyRequest } from '../../types/businessKeys'
import type {
  ArchiveIterationWaterfall,
  ArchiveIterationWaterfallParams,
  IterationReplayTarget,
  IterationRepresentativeReason,
  IterationWaterfallPartialReason,
  RunArchiveIterationRef,
} from '../../types/iterationReplay'
import type { ArchiveWaterfallRow, RunArchivePhaseMeans, RunArchiveRepresentativeSample } from '../../types/timeAnatomy'
import { getErrorMessage } from '../../utils/errorUtils'
import { AGGREGATE_PHASE_KEYS, type WaterfallPhaseKey } from '../waterfall/phases'
import type { WaterfallNode, WaterfallPhaseSpan, WaterfallStatus } from '../waterfall/types'
import { formatAnatomyMs, formatCount, isMaterialGap } from './timeAnatomyModel'

// R4 iteration replay: one VUser iteration rebuilt from its archive rows as a
// shared <Waterfall> tree.

export interface IterationNodePayload {
  row?: ArchiveWaterfallRow
  /** Think time the engine subtracted from a transaction (elapsed − duration). */
  thinkInsideMs?: number
}

export type IterationWaterfallNode = WaterfallNode<IterationNodePayload>

export const ITERATION_REASON_LABELS: Record<IterationRepresentativeReason, string> = {
  slowest: 'Slowest',
  p50: 'Median',
  p95: 'P95',
  failed: 'Failed',
}

export const THINK_GAP_LABEL = 'Think time or pacing (not recorded)'

/** The clock slack the backend join allows on window edges (2 ms). */
const EDGE_EPSILON_MS = 2

const pos = (value: number | undefined) => (typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0)

// Backend phase fields onto the shared keys (server → wait, download → receive, unsplit → total).
const ROW_PHASES: readonly [keyof RunArchivePhaseMeans, WaterfallPhaseKey][] = [
  ['dns_ms', AGGREGATE_PHASE_KEYS.dns],
  ['connect_ms', AGGREGATE_PHASE_KEYS.connect],
  ['tls_ms', AGGREGATE_PHASE_KEYS.tls],
  ['server_ms', AGGREGATE_PHASE_KEYS.server],
  ['download_ms', AGGREGATE_PHASE_KEYS.download],
  ['unsplit_ms', 'total'],
]

/** A row's phases laid one after another from the node's start. */
export function iterationRowPhases(phases: RunArchivePhaseMeans | undefined): WaterfallPhaseSpan[] {
  const spans: WaterfallPhaseSpan[] = []
  let cursor = 0
  for (const [field, key] of ROW_PHASES) {
    const ms = pos(phases?.[field])
    if (ms > 0) {
      spans.push({ key, startMs: cursor, endMs: cursor + ms })
      cursor += ms
    }
  }
  return spans
}

function rowStatus(row: ArchiveWaterfallRow): WaterfallStatus {
  if (row.interrupted) return 'incomplete'
  return row.success ? 'passed' : 'failed'
}

/** A row without a timestamp crosses JSON as Go's zero time. */
function rowIsTimed(row: ArchiveWaterfallRow): boolean {
  const at = Date.parse(row.timestamp)
  return Number.isFinite(at) && !row.timestamp.startsWith('0001-')
}

/** The row's wall window: the larger of duration and elapsed, as the backend's. */
function rowSpanMs(row: ArchiveWaterfallRow): number {
  return Math.max(pos(row.duration_ms), pos(row.elapsed_ms))
}

function requestNode(row: ArchiveWaterfallRow): IterationWaterfallNode {
  const phases = iterationRowPhases(row.phases)
  const split = phases.some(span => span.key !== 'total')
  const detail = [row.dependency_target, row.http_version ? `HTTP/${row.http_version}` : '', row.connection ? `${row.connection} connection` : ''].filter(Boolean).join(' · ')
  return {
    id: `row-${row.ordinal}`,
    kind: 'request',
    label: row.action_name || 'Request',
    detail: detail || undefined,
    startMs: rowIsTimed(row) ? pos(row.offset_ms) : null,
    durationMs: rowIsTimed(row) ? pos(row.duration_ms) : null,
    status: rowStatus(row),
    phases,
    timingSource: split ? 'protocol_evidence' : 'duration',
    statusCode: row.status_code,
    protocolLabel: row.http_version ? `HTTP/${row.http_version}` : undefined,
    connectionReused: row.connection === 'reused' ? true : row.connection === 'new' ? false : undefined,
    errorMessage: row.error_message,
    children: [],
    payload: { row },
  }
}

function transactionNode(row: ArchiveWaterfallRow): IterationWaterfallNode {
  const thinkInsideMs = Math.max(pos(row.elapsed_ms) - pos(row.duration_ms), 0)
  const detail = [
    row.source_transaction && row.source_transaction !== row.action_name ? row.source_transaction : '',
    thinkInsideMs > 0 ? `includes ${formatAnatomyMs(thinkInsideMs)} think time (duration ${formatAnatomyMs(row.duration_ms)})` : '',
  ].filter(Boolean).join(' · ')
  const timed = rowIsTimed(row)
  return {
    id: `row-${row.ordinal}`,
    kind: 'transaction',
    label: row.action_name || 'Transaction',
    detail: detail || undefined,
    startMs: timed ? pos(row.offset_ms) : null,
    durationMs: timed ? rowSpanMs(row) : null,
    status: rowStatus(row),
    phases: [],
    timingSource: 'duration',
    statusCode: row.status_code,
    errorMessage: row.error_message,
    children: [],
    payload: { row, thinkInsideMs },
  }
}

const startOf = (node: IterationWaterfallNode) => node.startMs ?? Number.POSITIVE_INFINITY
const endOf = (node: IterationWaterfallNode) => (node.startMs ?? 0) + (node.durationMs ?? 0)

/** The marker the archive's redaction writes in place of a scrubbed value. */
const REDACTED_MARKER = '[REDACTED]'

/** A name the join can compare: set and not scrubbed (the backend's timeAnatomyHintUsable). */
function nameUsable(value: string | undefined): value is string {
  const trimmed = (value ?? '').trim()
  return trimmed !== '' && !trimmed.includes(REDACTED_MARKER)
}

/**
 * Whether a transaction window claims a request, by the backend join's rule
 * (timeAnatomyClaimsRequest): a request starting inside [S, E+2ms] is held
 * whatever its source_transaction. One starting in the 2 ms slack before S
 * is held only when both names are usable and match (trimmed, any case), or,
 * without usable names, when it still runs at S.
 */
function windowClaims(tx: IterationWaterfallNode, request: IterationWaterfallNode): boolean {
  const start = request.startMs
  if (tx.startMs == null || start == null) return false
  if (start < tx.startMs - EDGE_EPSILON_MS || start > endOf(tx) + EDGE_EPSILON_MS) return false
  if (start >= tx.startMs) return true
  const source = request.payload?.row?.source_transaction
  const name = tx.payload?.row?.action_name
  if (nameUsable(source) && nameUsable(name)) return source.trim().toLowerCase() === name.trim().toLowerCase()
  return start + (request.durationMs ?? 0) > tx.startMs
}

function ordinalOf(node: IterationWaterfallNode): number {
  return node.payload?.row?.ordinal ?? 0
}

function sortByStart(nodes: IterationWaterfallNode[]) {
  nodes.sort((a, b) => startOf(a) - startOf(b) || ordinalOf(a) - ordinalOf(b))
}

export interface IterationTree {
  roots: IterationWaterfallNode[]
  spanMs: number
  /** Between-transaction gaps drawn as think rows. */
  thinkGapMs: number
}

/**
 * The iteration as a tree. Rows arrive in start order from the backend, but
 * are sorted again by offset (the archive itself is in completion order).
 *
 * - Transactions nest by window containment: a transaction inside another
 *   one's window (start and end, with 2 ms slack) is its child.
 * - A request goes under the innermost transaction whose window claims it
 *   by the report's join rule (windowClaims): source_transaction only settles
 *   the 2 ms slack before a window's start. Otherwise it is top level.
 * - Material gaps between consecutive top-level items become think rows: the
 *   archive records no think time or pacing between transactions, only the
 *   gap. When rows were cut or may be missing, no gap is drawn.
 */
export function buildIterationTree(resp: ArchiveIterationWaterfall): IterationTree {
  const rows = [...(resp.rows || [])]
  rows.sort((a, b) => {
    const timedA = rowIsTimed(a), timedB = rowIsTimed(b)
    if (timedA !== timedB) return timedA ? -1 : 1
    return pos(a.offset_ms) - pos(b.offset_ms) || a.ordinal - b.ordinal
  })
  const transactions: IterationWaterfallNode[] = []
  const requests: IterationWaterfallNode[] = []
  for (const row of rows) {
    if (row.population === 'transaction') transactions.push(transactionNode(row))
    else requests.push(requestNode(row))
  }
  // Longest first at one start, so an enclosing transaction precedes its children.
  transactions.sort((a, b) => startOf(a) - startOf(b) || (b.durationMs ?? 0) - (a.durationMs ?? 0) || ordinalOf(a) - ordinalOf(b))
  const topLevel: IterationWaterfallNode[] = []
  const stack: IterationWaterfallNode[] = []
  for (const tx of transactions) {
    if (tx.startMs == null) {
      topLevel.push(tx)
      continue
    }
    while (stack.length && endOf(stack[stack.length - 1]) + EDGE_EPSILON_MS < endOf(tx)) stack.pop()
    const parent = stack[stack.length - 1]
    if (parent) parent.children.push(tx)
    else topLevel.push(tx)
    stack.push(tx)
  }
  for (const request of requests) {
    let parent: IterationWaterfallNode | undefined
    if (request.startMs != null) {
      for (const tx of transactions) {
        if (!windowClaims(tx, request)) continue
        // Innermost: the shortest window, then the later start.
        if (!parent || (tx.durationMs ?? 0) < (parent.durationMs ?? 0) || ((tx.durationMs ?? 0) === (parent.durationMs ?? 0) && startOf(tx) > startOf(parent))) parent = tx
      }
    }
    if (parent) parent.children.push(request)
    else topLevel.push(request)
  }
  for (const tx of transactions) {
    sortByStart(tx.children)
    // A transaction with no rows inside draws its own duration.
    if (!tx.children.length && tx.durationMs != null) tx.phases = [{ key: 'total', startMs: 0, endMs: pos(tx.payload?.row?.duration_ms) }]
  }
  sortByStart(topLevel)

  let spanMs = pos(resp.span_ms)
  for (const node of topLevel) if (node.startMs != null) spanMs = Math.max(spanMs, endOf(node))

  const children: IterationWaterfallNode[] = []
  let thinkGapMs = 0
  const gapsKnown = !resp.truncated && !resp.partial
  let coveredEnd: number | null = null
  let gapIndex = 0
  for (const node of topLevel) {
    if (gapsKnown && node.startMs != null && coveredEnd != null && isMaterialGap(node.startMs - coveredEnd, spanMs)) {
      gapIndex += 1
      thinkGapMs += node.startMs - coveredEnd
      children.push({
        id: `gap-${gapIndex}`,
        kind: 'think',
        label: THINK_GAP_LABEL,
        detail: 'No archived row covers this time',
        startMs: coveredEnd,
        durationMs: node.startMs - coveredEnd,
        status: 'passed',
        phases: [],
        derived: true,
        children: [],
        payload: {},
      })
    }
    children.push(node)
    if (node.startMs != null) coveredEnd = Math.max(coveredEnd ?? 0, endOf(node))
  }

  const anyFailed = rows.some(row => !row.success && !row.interrupted)
  const anyInterrupted = rows.some(row => row.interrupted)
  const root: IterationWaterfallNode = {
    id: 'iteration',
    kind: 'iteration',
    label: `Iteration ${resp.iteration}`,
    detail: iterationIdentityLabel(resp),
    startMs: 0,
    durationMs: spanMs,
    status: anyFailed ? 'failed' : anyInterrupted || resp.partial ? 'incomplete' : 'passed',
    phases: [],
    timingSource: 'duration',
    children,
    payload: {},
  }
  return { roots: [root], spanMs, thinkGapMs }
}

/** "VUser 3 · group" for a heading. */
export function iterationIdentityLabel(value: { group?: string; vuser_id?: number; iteration?: number }, withIteration = false): string {
  return [
    `VUser ${value.vuser_id ?? 0}`,
    withIteration && value.iteration ? `iteration ${value.iteration}` : '',
    value.group || '',
  ].filter(Boolean).join(' · ')
}

/**
 * Why a representative is not a whole iteration: a slowest pick can be cut
 * short by the test's stop or split, and then its span can exceed the
 * index's whole-iteration max.
 */
export function iterationRefQualifier(ref: Pick<RunArchiveIterationRef, 'interrupted' | 'split'>): string {
  if (ref.interrupted) return 'cut short'
  if (ref.split) return 'split'
  return ''
}

export function iterationChipLabel(ref: RunArchiveIterationRef): string {
  const qualifier = iterationRefQualifier(ref)
  return `${ITERATION_REASON_LABELS[ref.reason] ?? ref.reason}${qualifier ? ` (${qualifier})` : ''} · ${formatAnatomyMs(ref.span_ms)} · VUser ${ref.vuser_id ?? 0} #${ref.iteration}`
}

/** The live-region line once an iteration is rebuilt. */
export function iterationReadyAnnouncement(data: Pick<ArchiveIterationWaterfall, 'iteration' | 'vuser_id' | 'rows_total' | 'span_ms' | 'partial' | 'truncated' | 'identity_quality'>): string {
  return [
    `Iteration ${data.iteration} of VUser ${data.vuser_id ?? 0} rebuilt: ${formatCount(data.rows_total)} rows, ${formatAnatomyMs(data.span_ms)}.`,
    data.partial ? 'Rows may be missing.' : '',
    data.truncated ? 'Only the first rows are shown.' : '',
    data.identity_quality === 'legacy' ? 'Legacy archive: a reused VUser ID can mix in another VUser\'s rows.' : '',
  ].filter(Boolean).join(' ')
}

export function targetFromIterationRef(ref: RunArchiveIterationRef): IterationReplayTarget {
  return {
    group: ref.group,
    vuser_id: ref.vuser_id ?? 0,
    vuser_lifetime: ref.vuser_lifetime,
    iteration: ref.iteration,
    // A split iteration's rows may lie outside its ordinals: scan in full.
    ...(ref.split ? {} : { first_ordinal: ref.first_ordinal, last_ordinal: ref.last_ordinal }),
    source: ITERATION_REASON_LABELS[ref.reason],
  }
}

/**
 * The target a filled-in form asks for: the incoming target itself (its
 * lifetime and ordinal anchors included) while the fields still show it,
 * otherwise a VUser-ID-only lookup. VUser 0 is a real VUser, but by ID alone
 * the endpoint needs a non-zero ID, so it is reachable only with a lifetime.
 */
export function formTarget(fields: { group: string; vuser: string; iteration: string }, current: IterationReplayTarget | null): IterationReplayTarget | null {
  const vuserID = fields.vuser.trim() === '' ? NaN : Number(fields.vuser)
  const iteration = fields.iteration.trim() === '' ? NaN : Number(fields.iteration)
  if (!Number.isInteger(vuserID) || vuserID < 0 || !Number.isInteger(iteration) || iteration < 1) return null
  const group = fields.group.trim()
  if (current && group === (current.group ?? '') && vuserID === (current.vuser_id ?? 0) && iteration === current.iteration) {
    return canReplay(current) ? current : null
  }
  const manual: IterationReplayTarget = { group: group || undefined, vuser_id: vuserID, iteration, source: 'Manual' }
  return canReplay(manual) ? manual : null
}

/** A target can be asked for: an iteration and a VUser lifetime or a non-zero VUser ID. */
export function canReplay(value: { vuser_id?: number; vuser_lifetime?: string; iteration?: number }): boolean {
  return (value.iteration ?? 0) > 0 && (Boolean(value.vuser_lifetime) || (value.vuser_id ?? 0) > 0)
}

/** The iteration a representative transaction ran in; its row anchors legacy IDs. */
export function targetFromRepresentative(sample: RunArchiveRepresentativeSample, transactionName: string): IterationReplayTarget | null {
  if (!canReplay(sample)) return null
  return {
    group: sample.group,
    vuser_id: sample.vuser_id ?? 0,
    vuser_lifetime: sample.vuser_lifetime,
    iteration: sample.iteration ?? 0,
    ordinal: Number.isInteger(sample.ordinal) && sample.ordinal >= 0 ? sample.ordinal : undefined,
    source: transactionName,
  }
}

/** The iteration a business-key match ran in. */
export function targetFromBusinessKeyMatch(match: BusinessKeyRequest): IterationReplayTarget | null {
  if (!canReplay(match)) return null
  return {
    group: match.group,
    vuser_id: match.vuser_id ?? 0,
    vuser_lifetime: match.vuser_lifetime,
    iteration: match.iteration ?? 0,
    ordinal: Number.isInteger(match.ordinal) && (match.ordinal ?? -1) >= 0 ? match.ordinal : undefined,
    source: 'Business key',
  }
}

/** The endpoint's query for a target (without where it came from). */
export function iterationParams(target: IterationReplayTarget): ArchiveIterationWaterfallParams {
  return {
    group: target.group, vuser_id: target.vuser_id, vuser_lifetime: target.vuser_lifetime, iteration: target.iteration,
    first_ordinal: target.first_ordinal, last_ordinal: target.last_ordinal, ordinal: target.ordinal, max_rows: target.max_rows,
  }
}

/** Same iteration request (ignores where it came from). */
export function sameTarget(left: IterationReplayTarget | null, right: IterationReplayTarget | null): boolean {
  if (!left || !right) return left === right
  const key = (t: IterationReplayTarget) => [t.group || '', t.vuser_id ?? 0, t.vuser_lifetime || '', t.iteration, t.first_ordinal ?? '', t.last_ordinal ?? '', t.ordinal ?? ''].join('|')
  return key(left) === key(right)
}

const PARTIAL_COPY: Record<IterationWaterfallPartialReason, string> = {
  scan_budget: 'The archive is too large to read in full, so rows of this iteration may be missing.',
  ambiguous_vuser_id: 'Several VUsers used this VUser ID (another generator or VUser lifetime); this is the iteration holding the linked row, or else the first matching one.',
}

export function iterationPartialCopy(reasons: readonly string[] | undefined): string {
  const lines = (reasons || []).map(reason => PARTIAL_COPY[reason as IterationWaterfallPartialReason]).filter(Boolean)
  return lines.length ? lines.join(' ') : 'Rows of this iteration may be missing.'
}

function errorStatus(error: unknown): unknown {
  return error && typeof error === 'object' ? (error as { status?: unknown }).status : undefined
}

export function iterationErrorIsBusy(error: unknown): boolean {
  const status = errorStatus(error)
  return status === 429 || status === 503
}

export function iterationErrorCopy(error: unknown): string {
  const status = errorStatus(error)
  if (iterationErrorIsBusy(error)) return 'The archive is busy rebuilding other drill-downs. Try again in a moment.'
  if (status === 404) return 'No archived rows for this VUser and iteration.'
  if (status === 400) return getErrorMessage(error, 'Check the VUser and iteration.')
  if (status === 413) return 'Archive too large to rebuild this iteration.'
  return getErrorMessage(error, 'Unable to rebuild this iteration.')
}
