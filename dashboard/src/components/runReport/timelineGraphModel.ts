import type { MetricsHistoryPoint } from '../../types'
import { transactionChartSeriesKey, type TransactionChartMetric } from '../transactionChartSeries'

export const MAX_TIMELINE_GRAPHS = 6
export const MAX_TIMELINE_TRANSACTIONS = 12

interface TimelineSeries {
  key: string
  name: string
  transactionMetric?: TransactionChartMetric
  baselineKey?: string
}

export interface TimelineGraph {
  id: string
  title: string
  group: 'Performance' | 'Resources' | 'Diagnostics'
  unit: string
  series: TimelineSeries[]
}

export const TIMELINE_GRAPHS: TimelineGraph[] = [
  {
    id: 'vusers',
    title: 'VUsers',
    group: 'Performance',
    unit: 'VUsers',
    series: [{ key: 'vusers', name: 'Active VUsers', baselineKey: 'baselineVusers' }],
  },
  {
    id: 'throughput',
    title: 'Throughput',
    group: 'Performance',
    unit: 'TPS',
    series: [
      { key: 'tps', name: 'Actual TPS', transactionMetric: 'tps', baselineKey: 'baselineTps' },
      { key: 'targetTps', name: 'Target TPS' },
    ],
  },
  {
    id: 'latency',
    title: 'Response Time',
    group: 'Performance',
    unit: 'ms',
    series: [
      { key: 'avgMs', name: 'Average', transactionMetric: 'avgMs' },
      { key: 'p95', name: 'P95', transactionMetric: 'p95', baselineKey: 'baselineP95' },
    ],
  },
  {
    id: 'errors',
    title: 'Error Rate',
    group: 'Performance',
    unit: '%',
    series: [{ key: 'errorPct', name: 'Error rate', transactionMetric: 'errorPct', baselineKey: 'baselineErrorPct' }],
  },
  ...(['p50', 'p90', 'p95', 'p99'] as const).map((metric) => ({
    id: metric,
    title: `${metric.toUpperCase()} Response Time`,
    group: 'Performance' as const,
    unit: 'ms',
    series: [
      {
        key: metric,
        name: metric.toUpperCase(),
        transactionMetric: metric,
        ...(metric === 'p95' ? { baselineKey: 'baselineP95' } : {}),
      },
    ],
  })),
  {
    id: 'cpu',
    title: 'CPU Usage',
    group: 'Resources',
    unit: '%',
    series: [
      { key: 'cpuProcess', name: 'Process CPU' },
      { key: 'cpuFree', name: 'Free CPU' },
    ],
  },
  {
    id: 'memory',
    title: 'Memory',
    group: 'Resources',
    unit: 'MB',
    series: [
      { key: 'memProcess', name: 'Process memory' },
      { key: 'memFree', name: 'Free memory' },
    ],
  },
  {
    id: 'network',
    title: 'Network Throughput',
    group: 'Resources',
    unit: 'KB/s',
    series: [
      { key: 'netSent', name: 'Sent' },
      { key: 'netRecv', name: 'Received' },
    ],
  },
  {
    id: 'disk',
    title: 'Disk Throughput',
    group: 'Resources',
    unit: 'KB/s',
    series: [
      { key: 'diskRead', name: 'Read' },
      { key: 'diskWrite', name: 'Write' },
    ],
  },
  {
    id: 'cpuPressure',
    title: 'CPU Pressure',
    group: 'Resources',
    unit: '%',
    series: [
      { key: 'cpuIOWait', name: 'I/O wait' },
      { key: 'cpuSteal', name: 'Steal' },
    ],
  },
  {
    id: 'containerCPU',
    title: 'Container CPU',
    group: 'Resources',
    unit: '%',
    series: [{ key: 'containerCPUUsagePct', name: 'CPU usage' }],
  },
  {
    id: 'containerMemory',
    title: 'Container Memory',
    group: 'Resources',
    unit: '%',
    series: [{ key: 'containerMemoryUsagePct', name: 'Memory usage' }],
  },
  {
    id: 'diskBusy',
    title: 'Disk Utilization',
    group: 'Resources',
    unit: '%',
    series: [{ key: 'diskIOTimePct', name: 'I/O time' }],
  },
  { id: 'gc', title: 'GC Pause', group: 'Diagnostics', unit: 'ms', series: [{ key: 'gcPauseMs', name: 'GC pause' }] },
  {
    id: 'networkPhases',
    title: 'Network Timing',
    group: 'Diagnostics',
    unit: 'ms',
    series: [
      { key: 'txDnsMs', name: 'DNS' },
      { key: 'txConnectMs', name: 'Connect' },
      { key: 'txTlsMs', name: 'TLS' },
      { key: 'txTtfbMs', name: 'TTFB' },
      { key: 'txWaitMs', name: 'Wait' },
    ],
  },
  {
    id: 'networkHealth',
    title: 'Network Health',
    group: 'Diagnostics',
    unit: '/s',
    series: [
      { key: 'netDropsPerSec', name: 'Drops' },
      { key: 'netErrorsPerSec', name: 'Errors' },
      { key: 'tcpRetransmitsPerSec', name: 'TCP retransmits' },
    ],
  },
  {
    id: 'throttling',
    title: 'CPU Throttling',
    group: 'Diagnostics',
    unit: 's/s',
    series: [{ key: 'containerCPUThrottledSecondsRate', name: 'Throttled time' }],
  },
  {
    id: 'oom',
    title: 'OOM Kills',
    group: 'Diagnostics',
    unit: 'kills / interval',
    series: [{ key: 'containerOOMKillCountDelta', name: 'OOM kills' }],
  },
  {
    id: 'errorTypes',
    title: 'Error Breakdown',
    group: 'Diagnostics',
    unit: 'errors / interval',
    series: [
      { key: 'errTimeout', name: 'Timeout' },
      { key: 'errConnection', name: 'Connection' },
      { key: 'errHttp4xx', name: 'HTTP 4xx' },
      { key: 'errHttp5xx', name: 'HTTP 5xx' },
      { key: 'errAssertion', name: 'Assertion' },
      { key: 'errResourceLimit', name: 'Generator resource limit' },
      { key: 'errSessionLost', name: 'Observer session lost' },
      { key: 'errOther', name: 'Other' },
    ],
  },
  {
    id: 'messaging',
    title: 'JMS Consumer Lag',
    group: 'Diagnostics',
    unit: 'ms',
    series: [
      { key: 'jmsConsumerLagMs', name: 'Average lag' },
      { key: 'jmsPeakConsumerLagMs', name: 'Peak lag' },
    ],
  },
]

export const DEFAULT_TIMELINE_GRAPHS = ['vusers', 'throughput', 'latency', 'errors']

export function timelineMetricKeys(data: MetricsHistoryPoint[]): Set<string> {
  const keys = new Set<string>()
  data.forEach((point) =>
    Object.entries(point).forEach(([key, value]) => {
      if (typeof value === 'number' && Number.isFinite(value)) keys.add(key)
    }),
  )
  return keys
}

export function graphSupportsTransactions(graph: TimelineGraph): boolean {
  return graph.series.some((series) => series.transactionMetric !== undefined)
}

export function graphHasData(graph: TimelineGraph, keys: Set<string>, transactions?: string[]): boolean {
  return graph.series.some((series) =>
    transactions !== undefined && graphSupportsTransactions(graph)
      ? series.transactionMetric !== undefined &&
        transactions.some((name) => keys.has(transactionChartSeriesKey(name, series.transactionMetric!)))
      : keys.has(series.key),
  )
}
