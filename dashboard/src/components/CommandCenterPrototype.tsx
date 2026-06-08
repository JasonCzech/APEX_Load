import { useCallback, useEffect, useMemo, useState } from 'react'

import PageViewToggle from './PageViewToggle'
import StatusBadge from './StatusBadge'
import { useApi } from '../hooks/useApi'
import { parseDurationToSeconds } from '../utils/timeUtils'
import type { GroupLiveMetrics, LiveMetrics, MetricsHistoryPoint, TestGroup, TestStatus, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import TestExecutionCharts, { type GraphCursorState } from './TestExecutionCharts'
import type { ExecutionChartTimeRange } from './testExecutionChartZoomModel'
import { mapExecutionMetricToHistoryPoint } from './testExecutionMetricsModel'
import { useExecutionChartModel } from './useExecutionChartModel'
import { useExecutionMetricsStream } from './useExecutionMetricsStream'
import './TestExecution.css'
import './CommandCenterPrototype.css'

type VariantId = 'ops' | 'grafana'

interface CommandCenterPrototypeProps {
  testId?: string
  history?: MetricsHistoryPoint[]
  statusOverride?: string
  initialTest?: TestSummary
  onOpenExecution?: (testId: string) => void
}

interface CommandGroup {
  key: string
  name: string
  protocol: string
  current: number
  target: number
  tps: number
  art: number
  errorPct: number
  p99: number
  transactions: number
  errors: number
  color: string
}

interface ErrorPattern {
  id: string
  title: string
  group: string
  transaction: string
  count: number
  rate: number
  firstSeen: string
  severity: 'warning' | 'danger'
  sample: string
}

interface CommandCenterState {
  runStatus: TestStatus
  vuserTargets: Record<string, number>
  selectedErrorId: string
  feedback: string
}

const DEFAULT_PROTOTYPE_FEEDBACK = 'Prototype controls are local-only. No live run will be changed.'
const DEFAULT_LIVE_FEEDBACK = 'Command center is connected to live run data.'
const TEST_STATUSES: TestStatus[] = [
  'QUEUED',
  'SCHEDULED',
  'PENDING',
  'SHAKEOUT',
  'SHAKEOUT_REVIEW',
  'INITIALIZING',
  'RUNNING',
  'STOPPING',
  'COMPLETE',
  'FAILED',
  'ABORTED',
  'PAUSED',
]

const VARIANT_OPTIONS: Array<{ value: VariantId; label: string }> = [
  { value: 'ops', label: 'Ops Cockpit' },
  { value: 'grafana', label: 'Grafana Wall' },
]

const GROUPS: CommandGroup[] = [
  {
    key: 'checkout',
    name: 'Checkout API',
    protocol: 'http',
    current: 860,
    target: 1200,
    tps: 812.4,
    art: 188,
    errorPct: 0.92,
    p99: 621,
    transactions: 48210,
    errors: 442,
    color: 'var(--chart-1)',
  },
  {
    key: 'browse',
    name: 'Browse Web',
    protocol: 'browser',
    current: 320,
    target: 420,
    tps: 146.7,
    art: 842,
    errorPct: 1.84,
    p99: 2210,
    transactions: 9240,
    errors: 170,
    color: 'var(--chart-2)',
  },
  {
    key: 'search',
    name: 'Search Solace',
    protocol: 'jms-solace',
    current: 410,
    target: 520,
    tps: 332.9,
    art: 142,
    errorPct: 0.28,
    p99: 384,
    transactions: 23780,
    errors: 66,
    color: 'var(--chart-3)',
  },
]

const ERROR_PATTERNS: ErrorPattern[] = [
  {
    id: 'err-token',
    title: 'HTTP 401 token refresh race',
    group: 'Checkout API',
    transaction: 'POST /orders',
    count: 214,
    rate: 0.44,
    firstSeen: '12:14:22 PM',
    severity: 'danger',
    sample: 'jwt expired while replaying payment authorization',
  },
  {
    id: 'err-cart',
    title: 'Cart lookup p99 spike',
    group: 'Browse Web',
    transaction: 'GET /cart/{id}',
    count: 118,
    rate: 1.28,
    firstSeen: '12:17:09 PM',
    severity: 'warning',
    sample: 'upstream response exceeded 2s timeout budget',
  },
  {
    id: 'err-solace',
    title: 'Solace publish retry burst',
    group: 'Search Solace',
    transaction: 'publish inventory.search',
    count: 66,
    rate: 0.28,
    firstSeen: '12:21:44 PM',
    severity: 'warning',
    sample: 'producer reconnect after broker route failover',
  },
]

const MOCK_TEST_GROUPS: TestGroup[] = GROUPS.map(group => ({
  name: group.name,
  protocol: group.protocol,
  script_id: `script-${group.key}`,
  script_name: `${group.name} Flow`,
  vusers: group.target,
  ramp_up: '12m',
}))

const MOCK_GROUP_METRICS: Record<string, GroupLiveMetrics> = GROUPS.reduce((metrics, group) => {
  metrics[group.name] = {
    name: group.name,
    vusers: group.current,
    transactions: group.transactions,
    errors: group.errors,
    tps: group.tps,
    target_tps: group.tps * 1.12,
    throughput_gap_pct: 7.8,
    throughput_target_missed: group.key === 'browse',
    avg_response_ms: group.art,
    p50_ms: group.art * 0.72,
    p90_ms: group.art * 1.55,
    p99_ms: group.p99,
  }
  return metrics
}, {} as Record<string, GroupLiveMetrics>)

const MOCK_LIVE_METRICS: LiveMetrics = {
  timestamp: '2026-06-08T16:24:00Z',
  active_vusers: GROUPS.reduce((sum, group) => sum + group.current, 0),
  tps: GROUPS.reduce((sum, group) => sum + group.tps, 0),
  target_tps: 1500,
  throughput_gap_pct: 6.4,
  throughput_target_missed: true,
  avg_response_ms: 274,
  error_pct: 1.02,
  total_transactions: GROUPS.reduce((sum, group) => sum + group.transactions, 0),
  total_errors: GROUPS.reduce((sum, group) => sum + group.errors, 0),
  p50_ms: 174,
  p90_ms: 512,
  p95_ms: 714,
  p99_ms: 1604,
  groups: MOCK_GROUP_METRICS,
  error_patterns: ERROR_PATTERNS.map(pattern => ({
    signature: pattern.title,
    sample: pattern.sample,
    count: pattern.count,
    first_seen: pattern.firstSeen,
    latest_seen: '12:28:18 PM',
    actions: [pattern.transaction],
    affected_groups: [pattern.group],
    error_pct: pattern.rate,
  })),
  capacity_status: {
    allocated_vusers: GROUPS.reduce((sum, group) => sum + group.current, 0),
    available_generators: 12,
    recommended_generators: 10,
    saturated: false,
    queued_vusers: 0,
    rejected_vusers: 0,
    by_protocol: {
      http: { allocated_vusers: 860 },
      browser: { allocated_vusers: 320 },
      'jms-solace': { allocated_vusers: 410 },
    },
    by_region: {
      'east-us': { allocated_vusers: 760 },
      'central-us': { allocated_vusers: 590 },
      'west-us': { allocated_vusers: 240 },
    },
  },
  resources: {
    cpu_process_pct: 62.4,
    cpu_free_pct: 27.1,
    mem_process_mb: 3840,
    mem_free_mb: 9120,
    net_sent_kbps: 8200,
    net_recv_kbps: 6400,
    disk_read_kbps: 420,
    disk_write_kbps: 920,
    gc_pause_ms: 34,
    container_cpu_usage_pct: 78,
    container_cpu_throttled_seconds_rate: 0.08,
    container_memory_usage_pct: 64,
    net_drop_in_per_sec: 0.4,
    net_error_in_per_sec: 0.1,
    tcp_retransmits_per_sec: 1.2,
    disk_io_time_pct: 32,
  },
  generator_resources: {
    'gen-east-1': {
      generator_id: 'gen-east-1',
      target: 'checkout-east',
      region: 'east-us',
      active_vusers: 760,
      tps: 640.2,
      error_pct: 0.7,
      p95_ms: 690,
      p99_ms: 1420,
      saturation_state: 'warning',
      impact_reasons: ['CPU throttling'],
      timestamp: '2026-06-08T16:24:00Z',
      resources: {
        cpu_process_pct: 84,
        cpu_free_pct: 14,
        mem_process_mb: 4120,
        mem_free_mb: 6100,
        net_sent_kbps: 4300,
        net_recv_kbps: 3600,
        disk_read_kbps: 220,
        disk_write_kbps: 440,
        gc_pause_ms: 42,
        container_cpu_usage_pct: 92,
        container_cpu_throttled_seconds_rate: 0.22,
        container_memory_usage_pct: 71,
        tcp_retransmits_per_sec: 1.8,
        disk_io_time_pct: 38,
      },
    },
    'gen-central-2': {
      generator_id: 'gen-central-2',
      target: 'browse-central',
      region: 'central-us',
      active_vusers: 590,
      tps: 236.4,
      error_pct: 1.4,
      p95_ms: 1180,
      p99_ms: 2210,
      saturation_state: 'danger',
      impact_reasons: ['Memory near limit', 'Network loss/retransmits'],
      timestamp: '2026-06-08T16:24:00Z',
      resources: {
        cpu_process_pct: 72,
        cpu_free_pct: 18,
        mem_process_mb: 6240,
        mem_free_mb: 2800,
        net_sent_kbps: 3900,
        net_recv_kbps: 3100,
        disk_read_kbps: 180,
        disk_write_kbps: 370,
        gc_pause_ms: 126,
        container_cpu_usage_pct: 81,
        container_memory_usage_pct: 93,
        net_drop_in_per_sec: 1.6,
        net_error_in_per_sec: 0.7,
        tcp_retransmits_per_sec: 4.2,
        disk_io_time_pct: 41,
      },
    },
  },
  latency: {
    targets: [{
      name: 'edge',
      endpoint: 'checkout.api.internal:443',
      latency_ms: 41.5,
      probe_total_ms: 44.2,
      dns_ms: 3.2,
      connect_ms: 11.1,
      tls_ms: 5.4,
      ttfb_ms: 32.8,
      http_status: 204,
      probe_method: 'tcp',
      ip: '192.0.2.10',
      hops: 12,
      last_success: true,
      jitter_ms: 3.4,
      success_rate_pct: 97.8,
      sample_count: 120,
      route_status: 'stable',
      timestamp: '2026-06-08T16:24:00Z',
    }],
  },
}

const MOCK_TEST: TestSummary = {
  id: 'run-command-center-prototype',
  ref_key: 'RUN-20260608-442',
  name: 'Checkout Peak Soak - Command Center Prototype',
  status: 'RUNNING',
  application_id: 'app-commerce',
  release_id: 'rel-2026-06-08',
  scenario_id: 'scenario-checkout-peak',
  project_id: 'project-retail',
  groups: MOCK_TEST_GROUPS,
  total_vusers: GROUPS.reduce((sum, group) => sum + group.target, 0),
  duration: '1h',
  created_at: '2026-06-08T15:00:00Z',
  started_at: '2026-06-08T15:34:00Z',
  started_by_display_name: 'Performance Ops',
  live_metrics: MOCK_LIVE_METRICS,
  scenario_mode: 'vuser_driven',
}

const HISTORY: MetricsHistoryPoint[] = Array.from({ length: 36 }, (_, index) => {
  const wave = Math.sin(index / 4)
  const browseWave = Math.cos(index / 5)
  const tps = 1040 + index * 7 + wave * 64
  const avgMs = 246 + Math.max(0, index - 18) * 6 + browseWave * 20
  const errorPct = index > 22 ? 0.7 + (index - 22) * 0.08 + Math.max(0, wave) * 0.4 : 0.22 + Math.max(0, wave) * 0.18
  const vusers = 840 + index * 42
  const checkoutTps = Math.round((620 + index * 4.8 + wave * 36) * 10) / 10
  const browseTps = Math.round((122 + index * 1.4 + browseWave * 12) * 10) / 10
  const searchTps = Math.round((260 + index * 2.7 + Math.sin(index / 3) * 20) * 10) / 10
  const cpuProcess = 52 + index * 0.75 + Math.max(0, wave) * 8
  const memProcess = 2700 + index * 44 + Math.max(0, browseWave) * 220
  return {
    time: `-${35 - index}m`,
    relativeMs: (index - 35) * 60000,
    timestampMs: Date.parse('2026-06-08T15:34:00Z') + index * 60000,
    vusers,
    tps: Math.round(tps * 10) / 10,
    targetTps: 1500,
    avgMs: Math.round(avgMs * 10) / 10,
    errorPct: Math.round(errorPct * 100) / 100,
    p50: Math.round((avgMs * 0.7) * 10) / 10,
    p90: Math.round((avgMs * 1.55) * 10) / 10,
    p95: Math.round((avgMs * 1.95) * 10) / 10,
    p99: Math.round((avgMs * 3.4) * 10) / 10,
    cpuProcess: Math.round(cpuProcess * 10) / 10,
    cpuFree: Math.round((34 - index * 0.28 - Math.max(0, wave) * 2) * 10) / 10,
    memProcess: Math.round(memProcess * 10) / 10,
    memFree: Math.round((12000 - memProcess * 0.8) * 10) / 10,
    netSent: Math.round((7200 + index * 56 + Math.max(0, wave) * 820) * 10) / 10,
    netRecv: Math.round((5600 + index * 48 + Math.max(0, browseWave) * 680) * 10) / 10,
    diskRead: Math.round((360 + Math.max(0, browseWave) * 95) * 10) / 10,
    diskWrite: Math.round((760 + index * 8 + Math.max(0, wave) * 120) * 10) / 10,
    containerCPUUsagePct: Math.round((cpuProcess + 12 + Math.max(0, wave) * 6) * 10) / 10,
    containerCPUThrottledSecondsRate: Math.round(Math.max(0, cpuProcess - 78) * 0.018 * 100) / 100,
    containerMemoryUsagePct: Math.round(Math.min(96, 48 + index * 0.9 + Math.max(0, browseWave) * 8) * 10) / 10,
    gcPauseMs: Math.round((8 + Math.max(0, browseWave) * 26 + Math.max(0, index - 24) * 1.8) * 10) / 10,
    netDropsPerSec: index > 26 ? Math.round((index - 26) * 0.35 * 10) / 10 : 0,
    netErrorsPerSec: index > 29 ? Math.round((index - 29) * 0.18 * 10) / 10 : 0,
    tcpRetransmitsPerSec: index > 25 ? Math.round((index - 25) * 0.42 * 10) / 10 : 0,
    diskIOTimePct: Math.round((22 + Math.max(0, wave) * 11 + Math.max(0, index - 28) * 1.5) * 10) / 10,
    latency_edge: Math.round((32 + Math.max(0, wave) * 10 + Math.max(0, index - 28) * 1.2) * 10) / 10,
    latency_dns_edge: Math.round((3 + Math.max(0, browseWave) * 1.2) * 10) / 10,
    latency_connect_edge: Math.round((9 + Math.max(0, wave) * 2.8) * 10) / 10,
    latency_tls_edge: Math.round((5 + Math.max(0, wave) * 1.6) * 10) / 10,
    latency_ttfb_edge: Math.round((24 + Math.max(0, wave) * 7.5 + Math.max(0, index - 28) * 0.8) * 10) / 10,
    latency_jitter_edge: Math.round((1.8 + Math.max(0, wave) * 2.1) * 10) / 10,
    latency_success_rate_edge: Math.round((99.5 - Math.max(0, index - 30) * 0.7) * 10) / 10,
    checkoutTps,
    browseTps,
    searchTps,
    'Checkout API_tps': checkoutTps,
    'Browse Web_tps': browseTps,
    'Search Solace_tps': searchTps,
    'Checkout API_avgMs': Math.round((avgMs * 0.82) * 10) / 10,
    'Browse Web_avgMs': Math.round((avgMs * 2.4) * 10) / 10,
    'Search Solace_avgMs': Math.round((avgMs * 0.62) * 10) / 10,
  }
})

function formatNumber(value: number, digits = 0) {
  return Number.isFinite(value)
    ? value.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits })
    : '0'
}

function formatPercent(value: number) {
  return `${value.toFixed(2)}%`
}

function formatRemaining(seconds: number) {
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const remSeconds = seconds % 60
  return hours > 0
    ? `${hours}h ${minutes.toString().padStart(2, '0')}m`
    : `${minutes}m ${remSeconds.toString().padStart(2, '0')}s`
}

function clampTarget(value: number) {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(2400, Math.round(value)))
}

function metricTone(value: number, warning: number, danger: number) {
  if (value >= danger) return 'danger'
  if (value >= warning) return 'warning'
  return 'healthy'
}

function coerceTestStatus(status: string | undefined, fallback: TestStatus = 'RUNNING'): TestStatus {
  const normalized = String(status || fallback).toUpperCase()
  return TEST_STATUSES.includes(normalized as TestStatus) ? normalized as TestStatus : fallback
}

function formatTimeValue(value?: string) {
  if (!value) return 'n/a'
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? value : new Date(parsed).toLocaleTimeString()
}

function computeRemainingSeconds(test: TestSummary, status: TestStatus) {
  if (status === 'STOPPING' || status === 'ABORTED' || status === 'COMPLETE' || status === 'FAILED') {
    return 0
  }
  const durationSeconds = parseDurationToSeconds(test.duration)
  if (durationSeconds <= 0) return 0
  const startedAt = test.started_at || test.start_time
  if (!startedAt) return durationSeconds
  const startedMs = Date.parse(startedAt)
  if (Number.isNaN(startedMs)) return durationSeconds
  const elapsedSeconds = Math.floor((Date.now() - startedMs) / 1000)
  return Math.max(durationSeconds - elapsedSeconds, 0)
}

function groupErrorPercent(group: GroupLiveMetrics | undefined) {
  if (!group || group.transactions <= 0) return 0
  return group.errors / group.transactions * 100
}

function buildLiveGroups(test: TestSummary, targetDrafts: Record<string, number>): CommandGroup[] {
  return (test.groups || []).map((group, index) => {
    const metrics = test.live_metrics?.groups?.[group.name]
    const aggregateFallback = !metrics && test.groups.length === 1 ? test.live_metrics : undefined
    const target = targetDrafts[group.name] ?? test.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers
    return {
      key: group.name,
      name: group.name,
      protocol: group.protocol || 'unknown',
      current: metrics?.vusers ?? aggregateFallback?.active_vusers ?? group.vusers,
      target,
      tps: metrics?.tps ?? aggregateFallback?.tps ?? 0,
      art: metrics?.avg_response_ms ?? aggregateFallback?.avg_response_ms ?? 0,
      errorPct: metrics ? groupErrorPercent(metrics) : aggregateFallback?.error_pct ?? 0,
      p99: metrics?.p99_ms ?? aggregateFallback?.p99_ms ?? 0,
      transactions: metrics?.transactions ?? aggregateFallback?.total_transactions ?? 0,
      errors: metrics?.errors ?? aggregateFallback?.total_errors ?? 0,
      color: `var(--chart-${index % 5 + 1})`,
    }
  })
}

function buildLiveErrorPatterns(metrics?: LiveMetrics): ErrorPattern[] {
  return (metrics?.error_patterns || []).map((pattern, index) => {
    const rate = pattern.error_pct ?? (metrics?.total_transactions ? pattern.count / metrics.total_transactions * 100 : 0)
    const group = pattern.affected_groups?.join(', ') || 'Multiple groups'
    return {
      id: `live-${index}-${pattern.signature}`,
      title: pattern.signature || `Error pattern ${index + 1}`,
      group,
      transaction: pattern.actions?.[0] || pattern.snapshot_url || 'Transaction evidence',
      count: pattern.count,
      rate,
      firstSeen: formatTimeValue(pattern.first_seen),
      severity: rate >= 1 || pattern.status_code && pattern.status_code >= 500 ? 'danger' : 'warning',
      sample: pattern.sample || 'No sample captured yet.',
    }
  })
}

function buildInitialTargets() {
  return GROUPS.reduce((targets, group) => {
    targets[group.name] = group.target
    return targets
  }, {} as Record<string, number>)
}

function buildGroupsWithTargets(targets: Record<string, number>) {
  return GROUPS.map(group => ({ ...group, target: targets[group.name] ?? group.target }))
}

export default function CommandCenterPrototype({
  testId,
  history = [],
  statusOverride,
  initialTest,
  onOpenExecution,
}: CommandCenterPrototypeProps = {}) {
  const api = useApi()
  const [variant, setVariant] = useState<VariantId>('ops')
  const [liveTest, setLiveTest] = useState<TestSummary | null>(() => initialTest || null)
  const [loadError, setLoadError] = useState('')
  const [expandedCharts, setExpandedCharts] = useState<Record<VariantId, string | null>>({ ops: null, grafana: null })
  const [activeGraphCursor, setActiveGraphCursor] = useState<GraphCursorState | null>(null)
  const [state, setState] = useState<CommandCenterState>(() => ({
    runStatus: 'RUNNING',
    vuserTargets: buildInitialTargets(),
    selectedErrorId: ERROR_PATTERNS[0].id,
    feedback: DEFAULT_PROTOTYPE_FEEDBACK,
  }))

  useEffect(() => {
    if (!testId) {
      setLiveTest(null)
      setLoadError('')
      return undefined
    }
    if (initialTest && initialTest.id === testId) {
      setLiveTest(initialTest)
      setLoadError('')
      return undefined
    }

    let cancelled = false
    const load = async () => {
      try {
        const next = await api.getTest(testId)
        if (!cancelled) {
          setLiveTest(next)
          setLoadError('')
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setLoadError(getErrorMessage(error, 'Unable to load command-center run data'))
        }
      }
    }

    void load()
    const interval = window.setInterval(() => {
      void load()
    }, 2000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [api, initialTest, testId])

  const streamStatus = liveTest ? coerceTestStatus(statusOverride || liveTest.status, liveTest.status) : undefined
  const { localMetricsHistory, latestMetrics } = useExecutionMetricsStream(testId || '', streamStatus)
  const liveMetrics = latestMetrics || liveTest?.live_metrics
  const hasLiveRoute = Boolean(testId)
  const liveMode = Boolean(testId && liveTest)
  const loadingTest: TestSummary = {
    ...MOCK_TEST,
    id: testId || MOCK_TEST.id,
    ref_key: testId || MOCK_TEST.ref_key,
    name: 'Loading command-center run...',
    status: 'INITIALIZING',
    groups: [],
    total_vusers: 0,
    live_metrics: undefined,
  }
  const displayTest: TestSummary = liveTest
    ? {
        ...liveTest,
        status: streamStatus || liveTest.status,
        live_metrics: liveMetrics || liveTest.live_metrics,
      }
    : hasLiveRoute ? loadingTest : MOCK_TEST
  const runStatus = liveMode || hasLiveRoute ? displayTest.status : state.runStatus
  const groups = useMemo(
    () => liveMode ? buildLiveGroups(displayTest, state.vuserTargets) : hasLiveRoute ? [] : buildGroupsWithTargets(state.vuserTargets),
    [displayTest, hasLiveRoute, liveMode, state.vuserTargets],
  )
  const errorPatterns = useMemo(
    () => liveMode ? buildLiveErrorPatterns(displayTest.live_metrics) : hasLiveRoute ? [] : ERROR_PATTERNS,
    [displayTest.live_metrics, hasLiveRoute, liveMode],
  )
  const selectedError = errorPatterns.find(error => error.id === state.selectedErrorId) || errorPatterns[0]
  const chartFallbackHistory = useMemo(() => {
    if (!liveMode) return hasLiveRoute ? [] : HISTORY
    if (history.length > 0) return history
    return displayTest.live_metrics ? [mapExecutionMetricToHistoryPoint(displayTest.live_metrics)] : []
  }, [displayTest.live_metrics, hasLiveRoute, history, liveMode])
  const {
    filteredData: chartData,
    timeRange,
    refAreaLeft,
    refAreaEnd,
    showRefArea,
    onTimeRangeChange,
    onChartMouseDown,
    onChartMouseMove,
    onZoom,
    onResetZoom,
  } = useExecutionChartModel({
    test: liveMode ? displayTest : null,
    history: chartFallbackHistory,
    localMetricsHistory: liveMode ? localMetricsHistory : [],
  })
  const totalCurrent = groups.reduce((sum, group) => sum + group.current, 0)
  const totalTarget = groups.reduce((sum, group) => sum + group.target, 0)
  const remainingSeconds = liveMode ? computeRemainingSeconds(displayTest, runStatus) : runStatus === 'STOPPING' || runStatus === 'ABORTED' ? 0 : 37 * 60 + 18
  const feedback = loadError
    || (hasLiveRoute && !liveTest ? 'Loading live run data for the command center...' : '')
    || (liveMode && state.feedback === DEFAULT_PROTOTYPE_FEEDBACK ? DEFAULT_LIVE_FEEDBACK : state.feedback)

  const setRunStatus = (runStatus: TestStatus, feedback: string) => {
    setState(prev => ({ ...prev, runStatus, feedback }))
  }

  const updateLiveStatus = useCallback((status: TestStatus, feedbackMessage: string) => {
    setLiveTest(prev => prev ? { ...prev, status } : prev)
    setState(prev => ({ ...prev, feedback: feedbackMessage }))
  }, [])

  const togglePause = async () => {
    if (liveMode && testId) {
      try {
        if (runStatus === 'PAUSED') {
          await api.resumeTest(testId)
          updateLiveStatus('RUNNING', 'Run resumed. Soft pause gate released between actions.')
          return
        }
        await api.pauseTest(testId)
        updateLiveStatus('PAUSED', 'Run paused at the next action boundary.')
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to change pause state') }))
      }
      return
    }

    if (runStatus === 'PAUSED') {
      setRunStatus('RUNNING', 'Run resumed. Soft pause gate released between actions.')
      return
    }
    setRunStatus('PAUSED', 'Run paused at the next action boundary.')
  }

  const stopRun = async () => {
    if (liveMode && testId) {
      try {
        await api.stopTest(testId)
        updateLiveStatus('STOPPING', 'Graceful stop requested. VUsers will finish the current action.')
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to stop test') }))
      }
      return
    }
    setRunStatus('STOPPING', 'Graceful stop requested. VUsers will finish the current action.')
  }

  const abortRun = async () => {
    if (liveMode && testId) {
      try {
        await api.abortTest(testId)
        updateLiveStatus('ABORTED', 'Abort requested. Live run state moved to aborted.')
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to abort test') }))
      }
      return
    }
    setRunStatus('ABORTED', 'Abort requested. Prototype run state moved to aborted.')
  }

  const changeTarget = (groupName: string, value: number) => {
    const target = clampTarget(value)
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: `${groupName} target staged at ${target.toLocaleString()} VUsers.`,
    }))
  }

  const applyTarget = async (groupName: string, value: number) => {
    const target = clampTarget(value)
    if (liveMode && testId) {
      try {
        await api.scaleVUsers(testId, groupName, target, { action: 'run' })
        setState(prev => ({
          ...prev,
          vuserTargets: { ...prev.vuserTargets, [groupName]: target },
          feedback: `${groupName} VUser target applied at ${target.toLocaleString()}.`,
        }))
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to scale VUsers') }))
      }
      return
    }
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: `${groupName} target staged at ${target.toLocaleString()} VUsers.`,
    }))
  }

  const selectError = (errorId: string) => {
    const error = errorPatterns.find(pattern => pattern.id === errorId)
    setState(prev => ({
      ...prev,
      selectedErrorId: errorId,
      feedback: error ? `Drill-down selected: ${error.title}` : prev.feedback,
    }))
  }

  const setExpandedChartForVariant = (value: string | null) => {
    setExpandedCharts(prev => ({ ...prev, [variant]: value }))
    setState(prev => ({
      ...prev,
      feedback: value ? 'Runtime graph expanded.' : 'Runtime graph collapsed.',
    }))
  }

  return (
    <div className="page command-center-page">
      <section className="command-center-intro" aria-label="Command center view mode">
        <PageViewToggle
          value={variant}
          options={VARIANT_OPTIONS}
          ariaLabel="Command center mockup variants"
          className="command-center-variant-toggle"
          onChange={(nextVariant) => {
            setVariant(nextVariant)
            setActiveGraphCursor(null)
          }}
        />
      </section>

      <RunControlShelf
        test={displayTest}
        status={runStatus}
        remainingSeconds={remainingSeconds}
        totalCurrent={totalCurrent}
        totalTarget={totalTarget}
        errorPct={displayTest.live_metrics?.error_pct ?? MOCK_LIVE_METRICS.error_pct}
        feedback={feedback}
        onPauseResume={() => { void togglePause() }}
        onStop={() => { void stopRun() }}
        onAbort={() => { void abortRun() }}
        onOpenExecution={testId && onOpenExecution ? () => onOpenExecution(testId) : undefined}
      />

      {variant === 'ops' && (
        <OpsCockpitView
          groups={groups}
          metrics={displayTest.live_metrics}
          chartData={chartData}
          expandedChart={expandedCharts.ops}
          onExpandedChartChange={setExpandedChartForVariant}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          activeCursor={activeGraphCursor}
          onActiveCursorChange={setActiveGraphCursor}
          errorPatterns={errorPatterns}
          selectedError={selectedError}
          onTargetChange={changeTarget}
          onApplyTarget={applyTarget}
          onErrorSelect={selectError}
        />
      )}
      {variant === 'grafana' && (
        <GrafanaWallView
          groups={groups}
          metrics={displayTest.live_metrics}
          chartData={chartData}
          expandedChart={expandedCharts.grafana}
          onExpandedChartChange={setExpandedChartForVariant}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          activeCursor={activeGraphCursor}
          onActiveCursorChange={setActiveGraphCursor}
          errorPatterns={errorPatterns}
          selectedError={selectedError}
          onTargetChange={changeTarget}
          onApplyTarget={applyTarget}
          onErrorSelect={selectError}
        />
      )}
    </div>
  )
}

interface RunControlShelfProps {
  test: TestSummary
  status: TestStatus
  remainingSeconds: number
  totalCurrent: number
  totalTarget: number
  errorPct: number
  feedback: string
  onPauseResume: () => void
  onStop: () => void
  onAbort: () => void
  onOpenExecution?: () => void
}

function RunControlShelf({
  test,
  status,
  remainingSeconds,
  totalCurrent,
  totalTarget,
  errorPct,
  feedback,
  onPauseResume,
  onStop,
  onAbort,
  onOpenExecution,
}: RunControlShelfProps) {
  const terminal = status === 'ABORTED' || status === 'STOPPING' || status === 'COMPLETE' || status === 'FAILED'
  const canPauseResume = status === 'RUNNING' || status === 'PAUSED'
  return (
    <section className="command-control-shelf" aria-label="Command center run controls">
      <div className="command-run-identity">
        <StatusBadge status={status} />
        <div>
          <strong>{test.name}</strong>
          <span>{test.ref_key} / {test.duration} duration / {test.groups.length} groups</span>
        </div>
      </div>

      <div className="command-run-stats" aria-label="Run summary">
        <MetricPill label="Remaining" value={formatRemaining(remainingSeconds)} tone={remainingSeconds <= 60 ? 'warning' : 'default'} />
        <MetricPill label="Current Load" value={`${formatNumber(totalCurrent)} VU`} tone="info" />
        <MetricPill label="Target Load" value={`${formatNumber(totalTarget)} VU`} tone="accent" />
        <MetricPill label="Error Rate" value={formatPercent(errorPct)} tone="danger" />
      </div>

      <div className="command-run-actions">
        {onOpenExecution && (
          <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenExecution}>
            Details
          </button>
        )}
        <button type="button" className={`btn btn-sm ${status === 'PAUSED' ? 'btn-success' : 'btn-secondary'}`} onClick={onPauseResume} disabled={terminal || !canPauseResume}>
          {status === 'PAUSED' ? 'Resume' : 'Pause'}
        </button>
        <button type="button" className="btn btn-sm btn-secondary" onClick={onStop} disabled={terminal}>
          Stop
        </button>
        <button type="button" className="btn btn-sm btn-danger" onClick={onAbort} disabled={status === 'ABORTED'}>
          Abort
        </button>
      </div>

      <p className="command-control-feedback" role="status">{feedback}</p>
    </section>
  )
}

function MetricPill({ label, value, tone = 'default' }: { label: string; value: string; tone?: 'default' | 'accent' | 'info' | 'warning' | 'danger' }) {
  return (
    <div className={`command-metric-pill ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

interface CommonViewProps {
  groups: CommandGroup[]
  metrics?: LiveMetrics
  chartData: MetricsHistoryPoint[]
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  onChartMouseDown: (activeLabel?: string | number) => void
  onChartMouseMove: (activeLabel?: string | number) => void
  onZoom: () => void
  onResetZoom: () => void
  showRefArea: boolean
  refAreaLeft: string | number
  refAreaEnd: string | number
  timeRange: ExecutionChartTimeRange
  onTimeRangeChange: (value: ExecutionChartTimeRange) => void
  activeCursor: GraphCursorState | null
  onActiveCursorChange: (cursor: GraphCursorState | null) => void
  errorPatterns: ErrorPattern[]
  selectedError?: ErrorPattern
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onErrorSelect: (errorId: string) => void
}

function OpsCockpitView({
  groups,
  metrics,
  chartData,
  expandedChart,
  onExpandedChartChange,
  onChartMouseDown,
  onChartMouseMove,
  onZoom,
  onResetZoom,
  showRefArea,
  refAreaLeft,
  refAreaEnd,
  timeRange,
  onTimeRangeChange,
  activeCursor,
  onActiveCursorChange,
  errorPatterns,
  selectedError,
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
}: CommonViewProps) {
  return (
    <section className="command-layout command-layout-ops" aria-label="Ops cockpit mockup">
      <div className="command-primary-column">
        <HeroMetricStrip groups={groups} metrics={metrics} />
        <GroupLoadTable groups={groups} onTargetChange={onTargetChange} onApplyTarget={onApplyTarget} compact={false} />
        <CommandRuntimeGraphs
          variant="ops"
          groups={groups}
          metrics={metrics}
          chartData={chartData}
          expandedChart={expandedChart}
          onExpandedChartChange={onExpandedChartChange}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          activeCursor={activeCursor}
          onActiveCursorChange={onActiveCursorChange}
        />
      </div>
      <aside className="command-side-rail" aria-label="Error drill-down rail">
        <ErrorDrilldown patterns={errorPatterns} selectedError={selectedError} onErrorSelect={onErrorSelect} />
        <CapacitySnapshot groups={groups} metrics={metrics} />
      </aside>
    </section>
  )
}

function GrafanaWallView({
  groups,
  metrics,
  chartData,
  expandedChart,
  onExpandedChartChange,
  onChartMouseDown,
  onChartMouseMove,
  onZoom,
  onResetZoom,
  showRefArea,
  refAreaLeft,
  refAreaEnd,
  timeRange,
  onTimeRangeChange,
  activeCursor,
  onActiveCursorChange,
  errorPatterns,
  selectedError,
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
}: CommonViewProps) {
  return (
    <section className="command-layout command-layout-grafana" aria-label="Grafana wall mockup">
      <div className="command-wall-toolbar">
        <span className="command-wall-label">Graph-heavy view</span>
        <div className="command-wall-filters" aria-label="Group filters">
          {groups.map(group => (
            <button key={group.key} type="button" className="command-filter-chip active">
              <span style={{ background: group.color }} />
              {group.name}
            </button>
          ))}
        </div>
      </div>
      <div className="command-grafana-grid">
        <CommandRuntimeGraphs
          variant="grafana"
          groups={groups}
          metrics={metrics}
          chartData={chartData}
          expandedChart={expandedChart}
          onExpandedChartChange={onExpandedChartChange}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          activeCursor={activeCursor}
          onActiveCursorChange={onActiveCursorChange}
        />
        <div className="command-grafana-side">
          <GroupLoadTable groups={groups} onTargetChange={onTargetChange} onApplyTarget={onApplyTarget} compact />
          <ErrorDrilldown patterns={errorPatterns} selectedError={selectedError} onErrorSelect={onErrorSelect} compact />
        </div>
      </div>
    </section>
  )
}

interface CommandRuntimeGraphsProps {
  variant: VariantId
  groups: CommandGroup[]
  metrics?: LiveMetrics
  chartData: MetricsHistoryPoint[]
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  onChartMouseDown: (activeLabel?: string | number) => void
  onChartMouseMove: (activeLabel?: string | number) => void
  onZoom: () => void
  onResetZoom: () => void
  showRefArea: boolean
  refAreaLeft: string | number
  refAreaEnd: string | number
  timeRange: ExecutionChartTimeRange
  onTimeRangeChange: (value: ExecutionChartTimeRange) => void
  activeCursor: GraphCursorState | null
  onActiveCursorChange: (cursor: GraphCursorState | null) => void
}

function CommandRuntimeGraphs({
  variant,
  groups,
  metrics,
  chartData,
  expandedChart,
  onExpandedChartChange,
  onChartMouseDown,
  onChartMouseMove,
  onZoom,
  onResetZoom,
  showRefArea,
  refAreaLeft,
  refAreaEnd,
  timeRange,
  onTimeRangeChange,
  activeCursor,
  onActiveCursorChange,
}: CommandRuntimeGraphsProps) {
  const groupNames = groups.map(group => group.name)
  const groupColors = groups.map(group => group.color)
  return (
    <div className="command-runtime-graphs">
      <TestExecutionCharts
        key={variant}
        filteredData={chartData}
        expandedChart={expandedChart}
        onExpandedChartChange={onExpandedChartChange}
        onChartMouseDown={onChartMouseDown}
        onChartMouseMove={onChartMouseMove}
        onZoom={onZoom}
        onResetZoom={onResetZoom}
        showRefArea={showRefArea}
        refAreaLeft={refAreaLeft}
        refAreaEnd={refAreaEnd}
        timeRange={timeRange}
        onTimeRangeChange={onTimeRangeChange}
        isMultiGroup={groups.length > 1}
        groupNames={groupNames}
        groupColors={groupColors.length > 0 ? groupColors : ['var(--chart-1)']}
        showLoadImpact={false}
        loadImpactData={chartData}
        latestLatency={metrics?.latency}
        latestMetrics={metrics}
        surface="command-center"
        storageScope={`apexload.commandCenter.${variant}`}
        initialPresetId={variant === 'ops' ? 'run_operator' : 'performance_engineer'}
        defaultDensity="command_center"
        activeCursor={activeCursor}
        onActiveCursorChange={onActiveCursorChange}
      />
    </div>
  )
}

function HeroMetricStrip({ groups, metrics }: { groups: CommandGroup[]; metrics?: LiveMetrics }) {
  const totalTps = metrics?.tps ?? groups.reduce((sum, group) => sum + group.tps, 0)
  const totalTransactions = groups.reduce((sum, group) => sum + group.transactions, 0)
  const weightedArt = totalTransactions > 0 ? groups.reduce((sum, group) => sum + group.art * group.transactions, 0) / totalTransactions : 0
  const avgArt = metrics?.avg_response_ms ?? weightedArt
  const groupedErrorPct = totalTransactions > 0 ? groups.reduce((sum, group) => sum + group.errors, 0) / totalTransactions * 100 : 0
  const errorPct = metrics?.error_pct ?? groupedErrorPct
  const activeVUsers = metrics?.active_vusers ?? groups.reduce((sum, group) => sum + group.current, 0)
  return (
    <div className="command-hero-strip" aria-label="Top line live metrics">
      <HeroMetric label="TPS" value={formatNumber(totalTps, 1)} tone="accent" />
      <HeroMetric label="ART" value={`${formatNumber(avgArt)} ms`} tone="default" />
      <HeroMetric label="Error Rate" value={formatPercent(errorPct)} tone="danger" />
      <HeroMetric label="Active VUsers" value={formatNumber(activeVUsers)} tone="info" />
      <HeroMetric label="Target VUsers" value={formatNumber(groups.reduce((sum, group) => sum + group.target, 0))} tone="success" />
    </div>
  )
}

function HeroMetric({ label, value, tone }: { label: string; value: string; tone: 'default' | 'accent' | 'danger' | 'info' | 'success' }) {
  return (
    <div className={`command-hero-metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  )
}

function GroupLoadTable({
  groups,
  compact,
  onTargetChange,
  onApplyTarget,
}: {
  groups: CommandGroup[]
  compact: boolean
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
}) {
  return (
    <section className={`command-panel command-group-panel ${compact ? 'compact' : ''}`} aria-label="Group load and health">
      <div className="command-panel-header">
        <div>
          <h3>Group Load</h3>
          <p>Current load, staged target, TPS, ART, and error rate by group.</p>
        </div>
      </div>
      <div className="command-group-table" role="table">
        <div className="command-group-row header" role="row">
          <span role="columnheader">Group</span>
          <span role="columnheader">Current</span>
          <span role="columnheader">Target</span>
          <span role="columnheader">TPS</span>
          <span role="columnheader">ART</span>
          <span role="columnheader">Errors</span>
        </div>
        {groups.map(group => (
          <div key={group.key} className="command-group-row" role="row">
            <span role="cell" className="command-group-name">
              <i style={{ background: group.color }} />
              <span>
                <strong>{group.name}</strong>
                <small>{group.protocol}</small>
              </span>
            </span>
            <span role="cell">{formatNumber(group.current)}</span>
            <span role="cell" className="command-target-cell">
              <input
                type="number"
                min={0}
                max={2400}
                aria-label={`${group.name} target VUsers`}
                value={group.target}
                onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
              />
              <input
                type="range"
                min={0}
                max={1800}
                aria-label={`${group.name} target slider`}
                value={group.target}
                onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
              />
              <button type="button" className="btn btn-sm btn-accent" onClick={() => onApplyTarget(group.name, group.target)}>
                Apply
              </button>
            </span>
            <span role="cell">{formatNumber(group.tps, 1)}</span>
            <span role="cell">{formatNumber(group.art)} ms</span>
            <span role="cell" className={metricTone(group.errorPct, 0.75, 1.5)}>{formatPercent(group.errorPct)}</span>
          </div>
        ))}
      </div>
    </section>
  )
}

function ErrorDrilldown({
  patterns,
  selectedError,
  onErrorSelect,
  compact = false,
}: {
  patterns: ErrorPattern[]
  selectedError?: ErrorPattern
  onErrorSelect: (errorId: string) => void
  compact?: boolean
}) {
  return (
    <section className={`command-panel command-error-panel ${compact ? 'compact' : ''}`} aria-label="Error drill-down">
      <div className="command-panel-header">
        <div>
          <h3>Error Drill-down</h3>
          <p>Grouped failure signatures with transaction evidence.</p>
        </div>
      </div>
      <div className="command-error-list">
        {patterns.length === 0 && (
          <div className="command-error-empty">
            <strong>No active error patterns</strong>
            <span>Live error signatures will appear here as the controller reports them.</span>
          </div>
        )}
        {patterns.map(pattern => (
          <button
            key={pattern.id}
            type="button"
            className={`command-error-item ${pattern.severity} ${selectedError?.id === pattern.id ? 'active' : ''}`}
            aria-pressed={selectedError?.id === pattern.id}
            onClick={() => onErrorSelect(pattern.id)}
          >
            <span>{pattern.title}</span>
            <strong>{pattern.count}</strong>
            <small>{pattern.group} / {formatPercent(pattern.rate)}</small>
          </button>
        ))}
      </div>
      {selectedError && (
        <div className={`command-error-detail ${selectedError.severity}`}>
          <span>{selectedError.transaction}</span>
          <strong>{selectedError.sample}</strong>
          <dl>
            <div>
              <dt>Group</dt>
              <dd>{selectedError.group}</dd>
            </div>
            <div>
              <dt>First seen</dt>
              <dd>{selectedError.firstSeen}</dd>
            </div>
            <div>
              <dt>Error rate</dt>
              <dd>{formatPercent(selectedError.rate)}</dd>
            </div>
          </dl>
        </div>
      )}
    </section>
  )
}

function CapacitySnapshot({ groups, metrics }: { groups: CommandGroup[]; metrics?: LiveMetrics }) {
  const capacity = metrics?.capacity_status
  const allocated = capacity?.allocated_vusers ?? groups.reduce((sum, group) => sum + group.current, 0)
  const projected = groups.reduce((sum, group) => sum + group.target, 0)
  const availableGenerators = capacity?.available_generators ?? 12
  const recommendedGenerators = capacity?.recommended_generators ?? 10
  return (
    <section className="command-panel command-capacity-snapshot" aria-label="Capacity snapshot">
      <div className="command-panel-header">
        <div>
          <h3>Capacity</h3>
          <p>Generator demand and staged target pressure.</p>
        </div>
      </div>
      <div className="command-capacity-meter">
        <span style={{ width: `${Math.min(100, allocated / Math.max(projected, 1) * 100)}%` }} />
      </div>
      <div className="command-capacity-stats">
        <MetricPill label="Allocated" value={`${formatNumber(allocated)} VU`} tone="info" />
        <MetricPill label="Projected" value={`${formatNumber(projected)} VU`} tone="accent" />
        <MetricPill label="Generators" value={`${recommendedGenerators} / ${availableGenerators}`} tone={capacity?.saturated ? 'danger' : 'default'} />
      </div>
    </section>
  )
}
