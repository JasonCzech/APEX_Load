import { GoalStatus, GroupLiveMetrics, TestSummary, TxMetrics } from '../types'

interface ExecutionStatusModel {
  metrics: TestSummary['live_metrics']
  goalStatus: GoalStatus | undefined
  transactions: TxMetrics[]
  groupMetrics: GroupLiveMetrics[]
  isMultiGroup: boolean
  groupNames: string[]
  isGoalOriented: boolean
  shakeoutFailed: boolean
}

function formatGoalScaleReason(reason?: string): string {
  if (!reason) return 'Tracking target'
  return reason.replace(/_/g, ' ')
}

export function describeGoalController(status?: GoalStatus): string {
  if (!status) return 'Waiting for live metrics'
  if (status.stop_triggered) return `Stopping: ${status.stop_reason || 'SLA breached'}`
  if (status.breach_active) return `SLA breach active for ${status.breach_duration_sec || 0}s`
  if (status.last_scale_direction) {
    return `Last scale ${status.last_scale_direction} due to ${formatGoalScaleReason(status.last_scale_reason)}`
  }
  return 'Tracking target'
}

export function buildExecutionStatusModel(test: TestSummary): ExecutionStatusModel {
  const metrics = test.live_metrics
  const goalStatus = metrics?.goal_status
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []

  return {
    metrics,
    goalStatus,
    transactions,
    groupMetrics,
    isMultiGroup: (test.groups?.length || 0) > 1,
    groupNames: test.groups?.map((group) => group.name) || [],
    isGoalOriented: test.scenario_mode === 'goal_oriented',
    shakeoutFailed: test.status === 'FAILED' && Boolean(test.script_shakeout) && Boolean(test.error?.toLowerCase().includes('shakeout')),
  }
}