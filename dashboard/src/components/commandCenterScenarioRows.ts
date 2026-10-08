import type { LiveMetrics, TxMetrics, VUserRampStatus } from '../types'

export interface CommandGroup {
  key: string
  name: string
  protocol: string
  role?: 'load' | 'observer'
  current: number
  target: number
  targetVusers: number
  targetMode: 'vusers' | 'tps'
  targetTps?: number
  throughputGapPct?: number
  throughputTargetMissed?: boolean
  suggestedVusers?: number
  minVusers?: number
  maxVusers?: number
  activeVuserRamp?: VUserRampStatus
  tps: number
  art: number
  errorPct: number
  p99: number
  transactions: number
  errors: number
  color: string
}

export interface ScenarioRunRow {
  id: string
  type: 'Group' | 'Transaction'
  name: string
  group: string
  protocol: string
  currentVusers?: number
  targetVusers?: number
  tps: number
  targetTps?: number
  throughputGap?: number
  transactions: number
  errors: number
  errorPct: number
  art: number
  p50: number
  p90: number
  p95?: number
  p99: number
}

// Every row reports the last sample window with one definition per column:
// TPS is the reported rate, `transactions` is attempts (successes + errors)
// and Error % is errors / attempts. Group metrics count successes and errors
// separately; transaction metrics' `count` already includes errors.
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

export function buildScenarioRunRows(
  groups: CommandGroup[],
  metrics?: LiveMetrics,
  transactionSnapshot?: Record<string, TxMetrics>,
): ScenarioRunRow[] {
  const rows: ScenarioRunRow[] = groups.map(group => {
    const groupMetrics = metrics?.groups?.[group.name]
    // A single-group run may report only whole-test window counters.
    const windowFallback = !groupMetrics && groups.length === 1 ? metrics : undefined
    const successes = groupMetrics?.transactions ?? windowFallback?.total_transactions ?? 0
    const errors = groupMetrics?.errors ?? windowFallback?.total_errors ?? 0
    const attempts = successes + errors
    const tps = groupMetrics?.tps ?? group.tps
    const targetTps = group.targetMode === 'tps'
      ? group.targetTps ?? group.target
      : groupMetrics?.target_tps ?? group.targetTps
    return {
      id: `group-${group.key}`,
      type: 'Group',
      name: group.name,
      group: group.name,
      protocol: group.protocol,
      currentVusers: groupMetrics?.vusers ?? group.current,
      targetVusers: group.targetVusers,
      tps,
      targetTps,
      throughputGap: group.targetMode === 'tps'
        ? calculateThroughputGap(tps, targetTps)
        : groupMetrics?.throughput_gap_pct ?? calculateThroughputGap(tps, targetTps),
      transactions: attempts,
      errors,
      errorPct: errorPctFromAttempts(errors, attempts),
      art: groupMetrics?.avg_response_ms ?? group.art,
      p50: groupMetrics?.p50_ms ?? group.art * 0.72,
      p90: groupMetrics?.p90_ms ?? group.art * 1.55,
      p95: groupMetrics?.p95_ms ?? group.art * 1.95,
      p99: groupMetrics?.p99_ms ?? group.p99,
    }
  })

  const transactions = (metrics?.transactions && Object.keys(metrics.transactions).length > 0)
    ? metrics.transactions
    : transactionSnapshot
  Object.entries(transactions || {}).forEach(([name, tx]) => {
    if (!tx) return
    rows.push({
      id: `transaction-${name}`,
      type: 'Transaction',
      name,
      group: 'All groups',
      protocol: tx.dependency_type || 'transaction',
      tps: tx.tps ?? 0,
      transactions: tx.count,
      errors: tx.errors,
      errorPct: errorPctFromAttempts(tx.errors, tx.count),
      art: tx.avg_ms,
      p50: tx.p50_ms,
      p90: tx.p90_ms,
      p95: tx.p95_ms,
      p99: tx.p99_ms,
    })
  })

  return rows
}
