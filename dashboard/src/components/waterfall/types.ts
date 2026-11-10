import type { WaterfallPhaseKey, WaterfallTimingSource } from './phases'

/**
 * Tree levels: iteration → transaction (or control-flow container) → page/step
 * → request → redirect hop. `think` marks think-time actions drawn as hatched
 * gaps; `marker` marks transaction start/end/close bookkeeping, which is not work.
 */
export type WaterfallNodeKind = 'iteration' | 'transaction' | 'container' | 'step' | 'request' | 'hop' | 'think' | 'marker'

export type WaterfallStatus = 'passed' | 'failed' | 'incomplete' | 'not_run'

/** One phase span, relative to its node's start. */
export interface WaterfallPhaseSpan {
  key: WaterfallPhaseKey
  startMs: number
  endMs: number
}

/**
 * Input-agnostic waterfall row. Adapters build these from debug events today;
 * archive `TransactionEvent` rows and aggregate phase vectors use the same
 * shape (see aggregate.ts), so `<Waterfall>` never sees the source format.
 */
export interface WaterfallNode<T = unknown> {
  id: string
  kind: WaterfallNodeKind
  label: string
  /** Secondary text: target, location or error. */
  detail?: string
  /** Offset from the waterfall origin; null when the node was not timed. */
  startMs: number | null
  durationMs: number | null
  status: WaterfallStatus
  /** Phase spans for leaves; parents draw their children instead. */
  phases: WaterfallPhaseSpan[]
  timingSource?: WaterfallTimingSource
  statusCode?: number
  method?: string
  protocolLabel?: string
  connectionReused?: boolean
  /** Redirects folded into this request when per-hop evidence is missing. */
  redirect?: { count: number; ms: number }
  /** Synthetic row built from counts rather than per-hop evidence. */
  derived?: boolean
  errorMessage?: string
  children: WaterfallNode<T>[]
  /**
   * Set on a copy a filter trimmed: the full child list, so timing analysis
   * (self time, gaps, phase totals, the parent track) still sees every child.
   */
  sourceChildren?: WaterfallNode<T>[]
  /**
   * Comparison overlay: the baseline's bar for the same action, drawn from this
   * node's start so the two lengths line up.
   */
  ghost?: { durationMs: number; phases: WaterfallPhaseSpan[] }
  /** Change against a baseline (current − baseline) and a one-line explanation. */
  delta?: { ms: number; label: string; outcome?: 'same' | 'now_failing' | 'now_passing' | 'added' | 'removed' }
  payload?: T
}

export type WaterfallSortMode = 'chronological' | 'slowest' | 'dominant' | 'change'
export type WaterfallScaleMode = 'waterfall' | 'stacked'

export interface WaterfallFlatRow<T = unknown> {
  node: WaterfallNode<T>
  depth: number
  parentId: string | null
  hasChildren: boolean
  expanded: boolean
  /** 1-based position among visible siblings, for aria-posinset. */
  posInSet: number
  setSize: number
}
