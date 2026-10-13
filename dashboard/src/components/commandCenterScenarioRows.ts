import type { LiveMetrics, TxMetrics, VUserRampStatus } from '../types'
import { groupTransactionKey } from './testExecutionMetricsModel'

export interface CommandGroup {
  key: string
  name: string
  protocol: string
  role?: 'load' | 'observer'
  current: number
  target: number
  targetVusers: number
  targetMode: 'vusers' | 'tps'
  /** The group's TPS target; in a goal run it includes a staged, unapplied draft. */
  targetTps?: number
  /** A goal run's applied TPS target. */
  committedTargetTps?: number
  /** The targets the engine held a goal group to over its goal window, which its gap is measured against. */
  goalWindowTargetTps?: number
  throughputGapPct?: number
  throughputTargetMissed?: boolean
  suggestedVusers?: number
  /** A traffic-profile group's place in its profile. */
  profile?: import('./goalThroughputModel').ProfilePosition
  minVusers?: number
  maxVusers?: number
  activeVuserRamp?: VUserRampStatus
  /** Transactions per second in the last sample window; goal targets are in TPS. */
  tps: number
  /** The throughput the page shows: the page's population (requests/s or TPS) over its window. */
  rate: number
  /** Requests per second over the page's window; a browser group's are the requests its pages make. */
  requestRate?: number
  /** Completed iterations per minute over the last minute; unset when not reported. */
  journeysPerMin?: number
  art: number
  /** Failed over attempted in the page's error population, run to date where reported. */
  errorPct: number
  p99: number
  /** The attempts and failures behind errorPct. */
  attempts: number
  errors: number
  color: string
}

export interface GroupRunRow {
  id: string
  name: string
  protocol: string
  currentVusers?: number
  targetVusers?: number
  rate: number
  /** A browser group's completed iterations per minute; unset for other groups. */
  journeysPerMin?: number
  targetTps?: number
  throughputGap?: number
  attempts: number
  errors: number
  /** Undefined before the group's first attempt. */
  errorPct?: number
  art: number
  p50: number
  p90: number
  p95?: number
  p99: number
}

export interface TransactionRunRow {
  id: string
  name: string
  /** The group whose share the row is; absent when the source does not say. */
  group?: string
  protocol: string
  tps: number
  attempts: number
  errors: number
  errorPct: number
  art: number
  p50: number
  p90: number
  p95?: number
  p99: number
}

/** A group whose VUsers drive a real browser: its iterations are user journeys. */
export function isBrowserGroup(group: { protocol?: string }): boolean {
  return group.protocol === 'browser'
}

// A transaction's `count` already includes its errors.
function errorPctFromAttempts(errors: number, attempts: number) {
  if (attempts <= 0) return errors > 0 ? 100 : 0
  return Math.min(100, errors / attempts * 100)
}

function calculateThroughputGap(tps: number, targetTps?: number) {
  // No configured/reported target means there is no gap to report: returning 0 here
  // would render a fabricated "0.00%" next to a "--" target on every ordinary run.
  if (!targetTps || targetTps <= 0) return undefined
  return (targetTps - tps) / targetTps * 100
}

// Group rows show the same figures as Group Load: the group's rate over the
// page's window, and its attempts, failures and error rate run to date.
// Throughput targets and gaps are in TPS: a goal run's gap is the one Group
// Load shows, and another run's is the engine's, from its last sample.
export function buildGroupRunRows(groups: CommandGroup[], metrics?: LiveMetrics): GroupRunRow[] {
  return groups.map(group => {
    const groupMetrics = metrics?.groups?.[group.name]
    // A goal run shows the applied target its gap is measured against; a
    // staged draft lives in Group Load's input until it is applied.
    // (Measured over the engine's goal window, that is the window's target.)
    const targetTps = group.targetMode === 'tps'
      ? group.goalWindowTargetTps ?? group.committedTargetTps ?? group.targetTps ?? group.target
      : groupMetrics?.target_tps ?? group.targetTps
    return {
      id: `group-${group.key}`,
      name: group.name,
      protocol: group.protocol,
      currentVusers: groupMetrics?.vusers ?? group.current,
      targetVusers: group.targetVusers,
      rate: group.rate,
      journeysPerMin: isBrowserGroup(group) ? group.journeysPerMin : undefined,
      targetTps,
      // The gap Group Load shows: a goal run's is measured on its averaged
      // rate against its committed target.
      throughputGap: group.targetMode === 'tps'
        ? group.throughputGapPct
        : groupMetrics?.throughput_gap_pct ?? calculateThroughputGap(group.tps, targetTps),
      attempts: group.attempts,
      errors: group.errors,
      errorPct: group.attempts > 0 ? group.errorPct : undefined,
      art: groupMetrics?.avg_response_ms ?? group.art,
      p50: groupMetrics?.p50_ms ?? group.art * 0.72,
      p90: groupMetrics?.p90_ms ?? group.art * 1.55,
      p95: groupMetrics?.p95_ms ?? group.art * 1.95,
      p99: groupMetrics?.p99_ms ?? group.p99,
    }
  })
}

/**
 * The groups a transaction has rows for in the Transactions table: each group
 * whose share it carries, or else the one group that ran all of it (the run's
 * only group, soleGroup, when the source does not say). undefined is a row
 * with no group.
 */
export function transactionRowGroups(tx: TxMetrics, soleGroup?: string): Array<string | undefined> {
  const shares = Object.keys(tx.by_group ?? {}).filter(group => tx.by_group?.[group])
  if (shares.length > 1) return shares.sort((left, right) => left.localeCompare(right))
  return [tx.groups?.length === 1 ? tx.groups[0] : tx.groups?.length ? undefined : soleGroup]
}

// Transaction rows, one for each group that ran the transaction: TPS over
// the page's window where the history has it (recentTps by transaction,
// recentGroupTps by groupTransactionKey), else the last sample's; attempts,
// failures and response times are the last sample window's, as live
// transaction counts are only kept per window. A transaction the source does
// not attribute belongs to soleGroup, the run's only group, if it has one.
export function buildTransactionRunRows(
  metrics?: LiveMetrics,
  transactionSnapshot?: Record<string, TxMetrics>,
  recentTps?: Record<string, number>,
  recentGroupTps?: Record<string, number>,
  soleGroup?: string,
): TransactionRunRow[] {
  const transactions = (metrics?.transactions && Object.keys(metrics.transactions).length > 0)
    ? metrics.transactions
    : transactionSnapshot
  const row = (key: string, name: string, group: string | undefined, protocol: string, tps: number, figures: Pick<TxMetrics, 'count' | 'errors' | 'avg_ms' | 'p50_ms' | 'p90_ms' | 'p95_ms' | 'p99_ms'>): TransactionRunRow => ({
    id: `transaction-${encodeURIComponent(group ?? '')}:${encodeURIComponent(key)}`,
    name: key,
    group,
    protocol,
    tps: (group === undefined ? undefined : recentGroupTps?.[groupTransactionKey(group, name)]) ?? tps,
    attempts: figures.count,
    errors: figures.errors,
    errorPct: errorPctFromAttempts(figures.errors, figures.count),
    art: figures.avg_ms,
    p50: figures.p50_ms,
    p90: figures.p90_ms,
    p95: figures.p95_ms,
    p99: figures.p99_ms,
  })
  return Object.entries(transactions || {}).flatMap(([key, tx]) => {
    if (!tx) return []
    const name = tx.name?.trim() || key
    const protocol = tx.dependency_type || 'transaction'
    return transactionRowGroups(tx, soleGroup).map(group => {
      const share = group === undefined ? undefined : tx.by_group?.[group]
      return share
        ? row(key, name, group, protocol, share.tps ?? 0, share)
        : row(key, name, group, protocol, recentTps?.[name] ?? tx.tps ?? 0, tx)
    })
  })
}
