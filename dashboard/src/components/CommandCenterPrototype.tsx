import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import PageViewToggle from './PageViewToggle'
import StatusBadge from './StatusBadge'
import { useApi } from '../hooks/useApi'
import { parseDurationToSeconds } from '../utils/timeUtils'
import type { BrowserReplaySnapshot, GroupLiveMetrics, IntervalSummaryRecord, LiveErrorEvidence, LiveMetrics, MetricsHistoryPoint, TestGroup, TestStatus, TestSummary, TransactionEvent } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import TestExecutionCharts, { type GraphCursorState, type GraphGroupTabId, type UnderLoadDisplayMode } from './TestExecutionCharts'
import type { ExecutionChartTimeRange } from './testExecutionChartZoomModel'
import { mapArchiveSummarySamples, mapExecutionHistory, mapExecutionMetricToHistoryPoint } from './testExecutionMetricsModel'
import { useExecutionChartModel } from './useExecutionChartModel'
import { useExecutionMetricsStream } from './useExecutionMetricsStream'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import {
  formatSnapshotRequestLineAndHeaders,
  formatSnapshotResponseStatusAndHeaders,
} from '../utils/snapshotFormatUtils'
import './TestExecution.css'
import './CommandCenterPrototype.css'

type VariantId = 'ops' | 'grafana'
type CommandTableTab = 'scenario' | 'errors' | 'infrastructure'
type SortDirection = 'asc' | 'desc'

interface CommandCenterPrototypeProps {
  testId?: string
  history?: MetricsHistoryPoint[]
  statusOverride?: string
  initialTest?: TestSummary
  onOpenExecution?: (testId: string) => void
  onOpenResults?: () => void
  prototypeMode?: boolean
}

export interface CommandGroup {
  key: string
  name: string
  protocol: string
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
  signature: string
  title: string
  group: string
  transaction: string
  count: number
  rate: number
  firstSeen: string
  latestSeen: string
  severity: 'warning' | 'danger'
  sample: string
  statusCode?: number
  actions: string[]
  affectedGroups: string[]
  evidenceUrl?: string
  evidenceCount?: number
  latestEvidenceId?: string
}

interface CommandCenterState {
  runStatus: TestStatus
  vuserTargets: Record<string, number>
  selectedErrorId: string
  feedback: string
}

const DEFAULT_PROTOTYPE_FEEDBACK = 'Prototype controls are local-only. No live run will be changed.'
const DEFAULT_LIVE_FEEDBACK = 'Command center is connected to live run data.'
const DEFAULT_HISTORICAL_FEEDBACK = 'Historical snapshot. Live controls are unavailable for completed, failed, or aborted runs.'
const TEST_STATUSES: TestStatus[] = [
  'QUEUED',
  'RESERVED',
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
  { value: 'ops', label: 'OPS View' },
  { value: 'grafana', label: 'Graph Wall' },
]

const UNDER_LOAD_DISPLAY_OPTIONS: Array<{ value: UnderLoadDisplayMode; label: string }> = [
  { value: 'graphs', label: 'Graphs' },
  { value: 'heatmap', label: 'Heatmap' },
]

const GROUPS: CommandGroup[] = [
  {
    key: 'checkout',
    name: 'Checkout API',
    protocol: 'http',
    current: 860,
    target: 1200,
    targetVusers: 1200,
    targetMode: 'vusers',
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
    targetVusers: 420,
    targetMode: 'vusers',
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
    targetVusers: 520,
    targetMode: 'vusers',
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
    signature: 'HTTP 401 token refresh race',
    title: 'HTTP 401 token refresh race',
    group: 'Checkout API',
    transaction: 'POST /orders',
    count: 214,
    rate: 0.44,
    firstSeen: '12:14:22 PM',
    latestSeen: '12:28:18 PM',
    severity: 'danger',
    sample: 'jwt expired while replaying payment authorization',
    statusCode: 401,
    actions: ['POST /orders', 'POST /payments/authorize'],
    affectedGroups: ['Checkout API'],
    evidenceUrl: '/api/v1/tests/run-command-center-prototype/errors/evidence?signature=HTTP+401+token+refresh+race',
    evidenceCount: 3,
    latestEvidenceId: 'mock-evidence-token-2',
  },
  {
    id: 'err-cart',
    signature: 'Cart lookup p99 spike',
    title: 'Cart lookup p99 spike',
    group: 'Browse Web',
    transaction: 'GET /cart/{id}',
    count: 118,
    rate: 1.28,
    firstSeen: '12:17:09 PM',
    latestSeen: '12:29:02 PM',
    severity: 'warning',
    sample: 'upstream response exceeded 2s timeout budget',
    statusCode: 504,
    actions: ['GET /cart/{id}'],
    affectedGroups: ['Browse Web'],
    evidenceUrl: '/api/v1/tests/run-command-center-prototype/errors/evidence?signature=Cart+lookup+p99+spike',
    evidenceCount: 2,
    latestEvidenceId: 'mock-evidence-cart-1',
  },
  {
    id: 'err-solace',
    signature: 'Solace publish retry burst',
    title: 'Solace publish retry burst',
    group: 'Search Solace',
    transaction: 'publish inventory.search',
    count: 66,
    rate: 0.28,
    firstSeen: '12:21:44 PM',
    latestSeen: '12:30:11 PM',
    severity: 'warning',
    sample: 'producer reconnect after broker route failover',
    statusCode: 503,
    actions: ['publish inventory.search'],
    affectedGroups: ['Search Solace'],
    evidenceUrl: '/api/v1/tests/run-command-center-prototype/errors/evidence?signature=Solace+publish+retry+burst',
    evidenceCount: 1,
    latestEvidenceId: 'mock-evidence-solace-0',
  },
]

const MOCK_ERROR_EVIDENCE: Record<string, LiveErrorEvidence[]> = {
  'err-token': [
    {
      id: 'mock-evidence-token-2',
      signature: 'HTTP 401 token refresh race',
      action: 'POST /orders',
      group: 'Checkout API',
      timestamp: '2026-06-08T16:28:18Z',
      duration_ms: 621,
      status: 'failed',
      status_code: 401,
      error_message: 'jwt expired while replaying payment authorization',
      snapshot: {
        request_method: 'POST',
        request_url: 'https://checkout.internal/orders',
        request_headers: {
          'Content-Type': 'application/json',
          Authorization: 'Bearer [REDACTED]',
          'X-Correlation-ID': 'cmd-cc-8471',
        },
        request_body: JSON.stringify({
          cartId: 'cart-[REDACTED]',
          paymentToken: '[REDACTED]',
          items: [{ sku: 'A-100', qty: 2 }],
        }, null, 2),
        response_status: 401,
        response_headers: {
          'Content-Type': 'application/json',
          'X-Request-ID': 'edge-9081',
        },
        response_body: JSON.stringify({
          error: 'invalid_token',
          message: 'token expired',
          refreshWindowMs: 250,
        }, null, 2),
      },
    },
    {
      id: 'mock-evidence-token-1',
      signature: 'HTTP 401 token refresh race',
      action: 'POST /payments/authorize',
      group: 'Checkout API',
      timestamp: '2026-06-08T16:27:42Z',
      duration_ms: 688,
      status: 'failed',
      status_code: 401,
      error_message: 'jwt expired while replaying payment authorization',
      snapshot: {
        request_method: 'POST',
        request_url: 'https://payments.internal/authorize',
        request_headers: {
          'Content-Type': 'application/json',
          Authorization: 'Bearer [REDACTED]',
        },
        request_body: '{"amount":129.98,"currency":"USD","card":"[REDACTED]"}',
        response_status: 401,
        response_headers: { 'Content-Type': 'application/json' },
        response_body: '{"error":"auth_expired","retryAfterMs":1000}',
      },
    },
  ],
  'err-cart': [
    {
      id: 'mock-evidence-cart-1',
      signature: 'Cart lookup p99 spike',
      action: 'GET /cart/{id}',
      group: 'Browse Web',
      timestamp: '2026-06-08T16:29:02Z',
      duration_ms: 2210,
      status: 'failed',
      status_code: 504,
      error_message: 'upstream response exceeded 2s timeout budget',
      snapshot: {
        request_method: 'GET',
        request_url: 'https://browse.internal/cart/cart-[REDACTED]',
        request_headers: {
          Accept: 'application/json',
          Cookie: '[REDACTED]',
        },
        response_status: 504,
        response_headers: { 'Content-Type': 'text/plain' },
        response_body: 'gateway timeout waiting for cart-cache-primary',
      },
    },
  ],
  'err-solace': [
    {
      id: 'mock-evidence-solace-0',
      signature: 'Solace publish retry burst',
      action: 'publish inventory.search',
      group: 'Search Solace',
      timestamp: '2026-06-08T16:30:11Z',
      duration_ms: 384,
      status: 'failed',
      status_code: 503,
      error_message: 'producer reconnect after broker route failover',
      snapshot: {
        request_method: 'JMS',
        request_url: 'solace://inventory.search',
        request_headers: {
          destination: 'topic/inventory/search',
          correlationId: 'cmd-cc-jms-42',
        },
        request_body: '<search><term>wireless headphones</term><region>east-us</region></search>',
        response_status: 503,
        response_headers: { broker: 'solace-a' },
        response_body: '<fault><code>route_failover</code><retry>true</retry></fault>',
      },
    },
  ],
}

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
    p95_ms: group.art * 1.95,
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
  transactions: {
    'POST /orders': {
      name: 'POST /orders',
      dependency_type: 'http',
      dependency_target: 'checkout.internal',
      last_error_message: 'jwt expired while replaying payment authorization',
      last_status_code: 401,
      last_error_at: '2026-06-08T16:28:18Z',
      correlation_ids: ['cmd-cc-8471'],
      correlation_count: 12,
      count: 28240,
      errors: 214,
      avg_ms: 188,
      p50_ms: 134,
      p90_ms: 291,
      p95_ms: 366,
      p99_ms: 621,
    },
    'GET /cart/{id}': {
      name: 'GET /cart/{id}',
      dependency_type: 'http',
      dependency_target: 'browse.internal',
      last_error_message: 'upstream response exceeded 2s timeout budget',
      last_status_code: 504,
      last_error_at: '2026-06-08T16:29:02Z',
      count: 9240,
      errors: 118,
      avg_ms: 842,
      p50_ms: 612,
      p90_ms: 1480,
      p95_ms: 1760,
      p99_ms: 2210,
    },
    'publish inventory.search': {
      name: 'publish inventory.search',
      dependency_type: 'jms_solace',
      dependency_target: 'inventory.search',
      last_error_message: 'producer reconnect after broker route failover',
      last_status_code: 503,
      last_error_at: '2026-06-08T16:30:11Z',
      count: 23780,
      errors: 66,
      avg_ms: 142,
      p50_ms: 96,
      p90_ms: 220,
      p95_ms: 277,
      p99_ms: 384,
    },
  },
  error_patterns: ERROR_PATTERNS.map(pattern => ({
    signature: pattern.title,
    sample: pattern.sample,
    count: pattern.count,
    first_seen: pattern.firstSeen,
    latest_seen: pattern.latestSeen,
    actions: pattern.actions,
    affected_groups: pattern.affectedGroups,
    status_code: pattern.statusCode,
    error_pct: pattern.rate,
    evidence_url: pattern.evidenceUrl,
    evidence_count: pattern.evidenceCount,
    latest_evidence_id: pattern.latestEvidenceId,
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

const EMPTY_COMMAND_CENTER_TEST: TestSummary = {
  id: 'command-center-empty',
  ref_key: 'No run selected',
  name: 'No active run selected',
  status: 'INITIALIZING',
  groups: [],
  total_vusers: 0,
  duration: '0m',
  created_at: '',
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
    'Checkout API_vusers': Math.round(vusers * 0.54),
    'Browse Web_vusers': Math.round(vusers * 0.2),
    'Search Solace_vusers': Math.max(0, vusers - Math.round(vusers * 0.54) - Math.round(vusers * 0.2)),
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

function clampTpsTarget(value: number) {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100000, Math.round(value * 10) / 10))
}

function formatTargetValue(value: number, mode: CommandGroup['targetMode']) {
  return mode === 'tps'
    ? `${formatNumber(value, 1)} TPS`
    : `${formatNumber(value)} VUsers`
}

function metricTone(value: number, warning: number, danger: number) {
  if (value >= danger) return 'danger'
  if (value >= warning) return 'warning'
  return 'healthy'
}

function clampPercent(value: number) {
  if (!Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}

function loadProgressPercent(durationSeconds: number, remainingSeconds: number) {
  if (durationSeconds <= 0) return 0
  const elapsedSeconds = Math.max(0, Math.min(durationSeconds, durationSeconds - remainingSeconds))
  return clampPercent(elapsedSeconds / durationSeconds * 100)
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

function isHistoricalRunStatus(status: TestStatus | string | undefined) {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

function groupErrorPercent(group: GroupLiveMetrics | undefined) {
  if (!group || group.transactions <= 0) return 0
  return group.errors / group.transactions * 100
}

function isGoalOrientedRun(test: TestSummary) {
  return test.scenario_mode === 'goal_oriented'
}

function finitePositive(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}

function stagedTargetTps(group: TestGroup): number {
  return (group.stages || []).reduce((maxTarget, stage) => Math.max(maxTarget, finitePositive(stage.target_tps)), 0)
}

function defaultGoalTargets(test: TestSummary): Record<string, number> {
  const groups = test.groups || []
  const aggregateTarget = finitePositive(test.goal_config?.target_average_tps)
  if (groups.length === 0 || aggregateTarget <= 0) return {}

  const totalVusers = groups.reduce((sum, group) => sum + Math.max(0, group.vusers || 0), 0)
  return groups.reduce((targets, group) => {
    targets[group.name] = totalVusers > 0
      ? aggregateTarget * Math.max(0, group.vusers || 0) / totalVusers
      : aggregateTarget / groups.length
    return targets
  }, {} as Record<string, number>)
}

function buildGoalTargetMap(test: TestSummary): Record<string, number> {
  const defaults = defaultGoalTargets(test)
  return (test.groups || []).reduce((targets, group) => {
    const stored = finitePositive(test.goal_targets?.[group.name]?.target_tps)
    const staged = stagedTargetTps(group)
    const live = finitePositive(test.live_metrics?.groups?.[group.name]?.target_tps)
    targets[group.name] = stored || staged || live || defaults[group.name] || 0
    return targets
  }, {} as Record<string, number>)
}

function buildCanonicalTargets(test: TestSummary): Record<string, number> {
  if (isGoalOrientedRun(test)) {
    return buildGoalTargetMap(test)
  }
  return (test.groups || []).reduce((targets, group) => {
    targets[group.name] = test.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers
    return targets
  }, {} as Record<string, number>)
}

function suggestedGroupVusers(test: TestSummary, groupName: string, currentVusers: number): number | undefined {
  const status = test.live_metrics?.goal_status
  const suggestedTotal = finitePositive(status?.suggested_total_vusers)
  if (!suggestedTotal) return undefined
  const currentTotal = finitePositive(status?.current_total_vusers) || (test.groups || []).reduce((sum, group) => {
    const metrics = test.live_metrics?.groups?.[group.name]
    return sum + (metrics?.vusers ?? group.vusers ?? 0)
  }, 0)
  if (currentTotal <= 0) return undefined
  return Math.round(suggestedTotal * currentVusers / currentTotal)
}

function buildLiveGroups(test: TestSummary, targetDrafts: Record<string, number>): CommandGroup[] {
  const targetMode = isGoalOrientedRun(test) ? 'tps' : 'vusers'
  const goalTargets = targetMode === 'tps' ? buildGoalTargetMap(test) : {}
  return (test.groups || []).map((group, index) => {
    const metrics = test.live_metrics?.groups?.[group.name]
    const aggregateFallback = !metrics && test.groups.length === 1 ? test.live_metrics : undefined
    const liveTargetVusers = test.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers
    const liveTarget = targetMode === 'tps' ? goalTargets[group.name] : liveTargetVusers
    const target = targetDrafts[group.name] ?? liveTarget
    const current = metrics?.vusers ?? aggregateFallback?.active_vusers ?? group.vusers
    const targetTps = targetMode === 'tps' ? target : metrics?.target_tps ?? aggregateFallback?.target_tps
    return {
      key: group.name,
      name: group.name,
      protocol: group.protocol || 'unknown',
      current,
      target,
      targetVusers: liveTargetVusers,
      targetMode,
      targetTps,
      throughputGapPct: metrics?.throughput_gap_pct ?? aggregateFallback?.throughput_gap_pct,
      throughputTargetMissed: metrics?.throughput_target_missed ?? aggregateFallback?.throughput_target_missed,
      suggestedVusers: targetMode === 'tps' ? suggestedGroupVusers(test, group.name, current) : undefined,
      minVusers: test.live_metrics?.goal_status?.min_vusers ?? test.goal_config?.min_vusers,
      maxVusers: test.live_metrics?.goal_status?.max_vusers ?? test.goal_config?.max_vusers,
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
      signature: pattern.signature,
      title: pattern.signature || `Error pattern ${index + 1}`,
      group,
      transaction: pattern.actions?.[0] || pattern.snapshot_url || 'Transaction evidence',
      count: pattern.count,
      rate,
      firstSeen: formatTimeValue(pattern.first_seen),
      latestSeen: formatTimeValue(pattern.latest_seen),
      severity: rate >= 1 || pattern.status_code && pattern.status_code >= 500 ? 'danger' : 'warning',
      sample: pattern.sample || 'No sample captured yet.',
      statusCode: pattern.status_code,
      actions: pattern.actions || [],
      affectedGroups: pattern.affected_groups || [],
      evidenceUrl: pattern.evidence_url,
      evidenceCount: pattern.evidence_count,
      latestEvidenceId: pattern.latest_evidence_id,
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
  return GROUPS.map(group => {
    const target = targets[group.name] ?? group.target
    return { ...group, target, targetVusers: target }
  })
}

export default function CommandCenterPrototype({
  testId,
  history = [],
  statusOverride,
  initialTest,
  onOpenExecution,
  onOpenResults,
  prototypeMode = false,
}: CommandCenterPrototypeProps = {}) {
  const api = useApi()
  const [variant, setVariant] = useState<VariantId>('ops')
  const [liveTest, setLiveTest] = useState<TestSummary | null>(() => initialTest || null)
  const [loadError, setLoadError] = useState('')
  const [expandedCharts, setExpandedCharts] = useState<Record<VariantId, string | null>>({ ops: null, grafana: null })
  const [activeGraphCursor, setActiveGraphCursor] = useState<GraphCursorState | null>(null)
  const [graphWallGroupVisibility, setGraphWallGroupVisibility] = useState<Record<string, boolean>>({})
  const [activeTableTab, setActiveTableTab] = useState<CommandTableTab>('scenario')
  const [selectedEvidenceId, setSelectedEvidenceId] = useState('')
  const [liveEvidenceBySignature, setLiveEvidenceBySignature] = useState<Record<string, LiveErrorEvidence[]>>({})
  const [evidenceLoadingSignature, setEvidenceLoadingSignature] = useState('')
  const [evidenceError, setEvidenceError] = useState('')
  const [backendScopedChartData, setBackendScopedChartData] = useState<MetricsHistoryPoint[] | null>(null)
  const [backendScopedMetrics, setBackendScopedMetrics] = useState<LiveMetrics | undefined>()
  const syncedTargetsRef = useRef<Record<string, number>>({})
  const syncedTargetTestIdRef = useRef<string | null>(null)
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

  const streamStatus = liveTest
    ? coerceTestStatus(isHistoricalRunStatus(liveTest.status) ? liveTest.status : statusOverride || liveTest.status, liveTest.status)
    : undefined
  const { localMetricsHistory, latestMetrics } = useExecutionMetricsStream(testId || '', streamStatus)
  const liveMetrics = latestMetrics || liveTest?.live_metrics
  const hasLiveRoute = Boolean(testId)
  const liveMode = Boolean(testId && liveTest)
  const emptyMode = !prototypeMode && !hasLiveRoute && !liveTest
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
    : hasLiveRoute ? loadingTest : emptyMode ? EMPTY_COMMAND_CENTER_TEST : MOCK_TEST

  useEffect(() => {
    if (!liveMode) return
    const canonicalTargets = buildCanonicalTargets(displayTest)
    const isNewTest = syncedTargetTestIdRef.current !== displayTest.id
    setState(prev => {
      const nextTargets = Object.entries(canonicalTargets).reduce((targets, [groupName, target]) => {
        const previousValue = isNewTest ? undefined : prev.vuserTargets[groupName]
        const previousSyncedValue = isNewTest ? undefined : syncedTargetsRef.current[groupName]
        targets[groupName] = previousValue === undefined || previousValue === previousSyncedValue
          ? target
          : previousValue
        return targets
      }, {} as Record<string, number>)
      return { ...prev, vuserTargets: nextTargets }
    })
    syncedTargetsRef.current = canonicalTargets
    syncedTargetTestIdRef.current = displayTest.id
  }, [
    displayTest.goal_config?.target_average_tps,
    displayTest.goal_targets,
    displayTest.groups,
    displayTest.id,
    displayTest.live_metrics?.groups,
    displayTest.scenario_mode,
    displayTest.vuser_ramps,
    liveMode,
  ])

  const runStatus = liveMode || hasLiveRoute || emptyMode ? displayTest.status : state.runStatus
  const groups = useMemo(
    () => liveMode ? buildLiveGroups(displayTest, state.vuserTargets) : hasLiveRoute || emptyMode ? [] : buildGroupsWithTargets(state.vuserTargets),
    [displayTest, emptyMode, hasLiveRoute, liveMode, state.vuserTargets],
  )
  const graphWallGroups = useMemo(
    () => groups.filter(group => graphWallGroupVisibility[group.key] !== false),
    [graphWallGroupVisibility, groups],
  )
  const graphWallGroupNames = useMemo(() => graphWallGroups.map(group => group.name), [graphWallGroups])
  const graphWallGroupSignature = graphWallGroupNames.join('\u001f')
  const errorPatterns = useMemo(
    () => liveMode ? buildLiveErrorPatterns(displayTest.live_metrics) : hasLiveRoute || emptyMode ? [] : ERROR_PATTERNS,
    [displayTest.live_metrics, emptyMode, hasLiveRoute, liveMode],
  )
  const selectedError = errorPatterns.find(error => error.id === state.selectedErrorId) || errorPatterns[0]
  const selectedErrorEvidence = useMemo(() => {
    if (!selectedError) return []
    if (liveMode) {
      return liveEvidenceBySignature[selectedError.signature] || []
    }
    return MOCK_ERROR_EVIDENCE[selectedError.id] || []
  }, [liveEvidenceBySignature, liveMode, selectedError])

  useEffect(() => {
    if (!liveMode || !testId || activeTableTab !== 'errors' || !selectedError?.signature) {
      setEvidenceLoadingSignature('')
      setEvidenceError('')
      return undefined
    }
    const signature = selectedError.signature
    let cancelled = false
    setEvidenceLoadingSignature(signature)
    setEvidenceError('')
    void api.getTestErrorEvidence(testId, { signature, limit: 20 })
      .then(response => {
        if (cancelled) return
        setLiveEvidenceBySignature(prev => ({
          ...prev,
          [signature]: response.evidence || [],
        }))
        setEvidenceLoadingSignature('')
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setEvidenceError(getErrorMessage(error, 'Unable to load live error evidence'))
        setEvidenceLoadingSignature('')
      })
    return () => {
      cancelled = true
    }
  }, [activeTableTab, api, liveMode, selectedError?.signature, testId])

  const chartFallbackHistory = useMemo(() => {
    if (!liveMode) return hasLiveRoute || emptyMode ? [] : HISTORY
    if (history.length > 0) return history
    return displayTest.live_metrics ? [mapExecutionMetricToHistoryPoint(displayTest.live_metrics)] : []
  }, [displayTest.live_metrics, emptyMode, hasLiveRoute, history, liveMode])
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
  const targetMode = groups[0]?.targetMode ?? 'vusers'
  const totalDurationSeconds = parseDurationToSeconds(displayTest.duration)
  const remainingSeconds = liveMode ? computeRemainingSeconds(displayTest, runStatus) : emptyMode || runStatus === 'STOPPING' || runStatus === 'ABORTED' ? 0 : 37 * 60 + 18
  const historicalRun = liveMode && isHistoricalRunStatus(runStatus)
  const allGraphWallGroupsVisible = groups.length === 0 || graphWallGroups.length === groups.length
  const noGraphWallGroupsVisible = groups.length > 0 && graphWallGroups.length === 0
  const clientScopedChartData = useMemo(() => {
    if (variant !== 'grafana' || groups.length === 0 || allGraphWallGroupsVisible) return chartData
    if (noGraphWallGroupsVisible) return []
    return selectedGroupLoadImpactData(chartData, graphWallGroups)
  }, [allGraphWallGroupsVisible, chartData, graphWallGroups, groups.length, noGraphWallGroupsVisible, variant])
  const alignedBackendScopedChartData = useMemo(
    () => backendScopedChartData ? alignScopedChartDataToReference(backendScopedChartData, chartData) : null,
    [backendScopedChartData, chartData],
  )
  const graphScopedChartData = noGraphWallGroupsVisible
    ? []
    : allGraphWallGroupsVisible
      ? chartData
      : alignedBackendScopedChartData ?? clientScopedChartData
  const graphScopedMetrics = allGraphWallGroupsVisible || noGraphWallGroupsVisible
    ? displayTest.live_metrics
    : backendScopedMetrics ?? displayTest.live_metrics

  useEffect(() => {
    if (!liveMode || !testId || variant !== 'grafana' || allGraphWallGroupsVisible || noGraphWallGroupsVisible) {
      setBackendScopedChartData(null)
      setBackendScopedMetrics(undefined)
      return undefined
    }

    let cancelled = false
    const selectedGroupNames = graphWallGroupSignature ? graphWallGroupSignature.split('\u001f') : []
    const selectedGroupSet = new Set(selectedGroupNames)
    const selectedGroupScopes = selectedGroupNames.map(name => ({ name }) as CommandGroup)
    const responseHonorsGroupFilter = (metrics?: LiveMetrics) => (
      Object.keys(metrics?.groups || {}).every(name => selectedGroupSet.has(name))
    )
    const loadArchiveTransactionsForSelectedGroups = async () => {
      const transactions: TransactionEvent[] = []
      for (const groupName of selectedGroupNames) {
        let offset = 0
        let total = Number.POSITIVE_INFINITY
        while (!cancelled && offset < total) {
          const response = await api.getTestArchiveTransactions(testId, {
            group: groupName,
            limit: 1000,
            offset,
          })
          const page = response.transactions || []
          transactions.push(...page)
          total = typeof response.total === 'number' ? response.total : offset + page.length
          if (page.length === 0) break
          offset += page.length
        }
      }
      return transactions
    }
    const loadScopedHistory = async () => {
      try {
        if (historicalRun && displayTest.archive?.interval_summary_count) {
          const loaded: IntervalSummaryRecord[] = []
          let archiveFilterHonored = false
          let offset = 0
          let total = displayTest.archive.interval_summary_count
          while (!cancelled && loaded.length < total) {
            const response = await api.getTestArchiveSummaries(testId, {
              limit: 1000,
              offset,
              groups: selectedGroupNames,
            })
            const page = response.summaries || []
            const responseGroups = Array.isArray(response.filters?.groups) ? response.filters.groups : []
            archiveFilterHonored = archiveFilterHonored || selectedGroupNames.every(name => responseGroups.includes(name))
            loaded.push(...page)
            total = response.total || loaded.length
            if (page.length === 0) break
            offset += page.length
          }
          if (!cancelled) {
            const mappedHistory = mapArchiveSummarySamples(loaded)
            if (archiveFilterHonored) {
              setBackendScopedChartData(mappedHistory)
            } else if (selectedGroupHistoryHasFields(mappedHistory, selectedGroupNames)) {
              setBackendScopedChartData(selectedGroupLoadImpactData(mappedHistory, selectedGroupScopes))
            } else {
              try {
                const transactions = await loadArchiveTransactionsForSelectedGroups()
                if (cancelled) return
                const transactionHistory = buildSelectedGroupChartHistoryFromTransactions(mappedHistory, transactions, graphWallGroups, groups)
                setBackendScopedChartData(transactionHistory.length > 0 ? transactionHistory : mappedHistory)
              } catch {
                if (!cancelled) {
                  setBackendScopedChartData(mappedHistory)
                }
              }
            }
            setBackendScopedMetrics(undefined)
          }
          return
        }

        const response = await api.getMetrics(testId, { groups: selectedGroupNames })
        if (cancelled) return
        const mappedHistory = mapExecutionHistory((response.history || []) as LiveMetrics[])
        setBackendScopedChartData(responseHonorsGroupFilter(response.metrics)
          ? mappedHistory
          : selectedGroupLoadImpactData(mappedHistory, selectedGroupScopes))
        setBackendScopedMetrics(responseHonorsGroupFilter(response.metrics)
          ? response.metrics
          : undefined)
      } catch {
        if (!cancelled) {
          setBackendScopedChartData(null)
          setBackendScopedMetrics(undefined)
        }
      }
    }

    void loadScopedHistory()
    return () => {
      cancelled = true
    }
  }, [
    allGraphWallGroupsVisible,
    api,
    displayTest.archive?.interval_summary_count,
    graphWallGroupSignature,
    graphWallGroups,
    groups,
    historicalRun,
    liveMetrics?.timestamp,
    liveMode,
    noGraphWallGroupsVisible,
    testId,
    variant,
  ])
  const feedback = loadError
    || (hasLiveRoute && !liveTest ? 'Loading live run data for the command center...' : '')
    || (emptyMode ? 'No active run is selected for the command center.' : '')
    || (historicalRun ? DEFAULT_HISTORICAL_FEEDBACK : '')
    || (liveMode && state.feedback === DEFAULT_PROTOTYPE_FEEDBACK ? DEFAULT_LIVE_FEEDBACK : state.feedback)

  const setRunStatus = (runStatus: TestStatus, feedback: string) => {
    setState(prev => ({ ...prev, runStatus, feedback }))
  }

  const updateLiveStatus = useCallback((status: TestStatus, feedbackMessage: string) => {
    setLiveTest(prev => prev ? { ...prev, status } : prev)
    setState(prev => ({ ...prev, feedback: feedbackMessage }))
  }, [])

  const startRun = async () => {
    if (liveMode && testId) {
      try {
        const response = await api.startTest(testId)
        if (response.status === 'pending') {
          setLiveTest(prev => prev ? {
            ...prev,
            status: 'PENDING',
            pending_reason: response.message || prev.pending_reason,
            pending_since: prev.pending_since || new Date().toISOString(),
          } : prev)
          setState(prev => ({ ...prev, feedback: response.message || 'Run is waiting for project capacity.' }))
          return
        }
        setLiveTest(prev => response.test || (prev ? { ...prev, status: 'INITIALIZING' } : prev))
        setState(prev => ({ ...prev, feedback: 'Start requested. Run is initializing.' }))
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to start test') }))
      }
      return
    }
    setRunStatus('RUNNING', 'Run started from the command center.')
  }

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
    const targetMode = isGoalOrientedRun(displayTest) ? 'tps' : 'vusers'
    const target = targetMode === 'tps' ? clampTpsTarget(value) : clampTarget(value)
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: `${groupName} target staged at ${formatTargetValue(target, targetMode)}.`,
    }))
  }

  const applyTarget = async (groupName: string, value: number) => {
    const targetMode = isGoalOrientedRun(displayTest) ? 'tps' : 'vusers'
    const target = targetMode === 'tps' ? clampTpsTarget(value) : clampTarget(value)
    if (targetMode === 'tps' && target <= 0) {
      setState(prev => ({ ...prev, feedback: `${groupName} target TPS must be greater than zero.` }))
      return
    }
    if (liveMode && testId) {
      try {
        if (targetMode === 'tps') {
          const update = await api.updateGoalTarget(testId, groupName, target)
          setLiveTest(prev => prev ? {
            ...prev,
            goal_targets: update.goal_targets,
            goal_config: {
              ...(prev.goal_config || {}),
              target_average_tps: update.target_average_tps,
            },
          } : prev)
          setState(prev => ({
            ...prev,
            vuserTargets: { ...prev.vuserTargets, [groupName]: target },
            feedback: `${groupName} TPS target applied at ${formatNumber(target, 1)}. Aggregate goal is ${formatNumber(update.target_average_tps, 1)} TPS.`,
          }))
          return
        }
        await api.scaleVUsers(testId, groupName, target, { action: 'run' })
        setState(prev => ({
          ...prev,
          vuserTargets: { ...prev.vuserTargets, [groupName]: target },
          feedback: `${groupName} VUser target applied at ${target.toLocaleString()}.`,
        }))
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, targetMode === 'tps' ? 'Unable to update goal target' : 'Unable to scale VUsers') }))
      }
      return
    }
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: `${groupName} target staged at ${formatTargetValue(target, targetMode)}.`,
    }))
  }

  const selectError = (errorId: string) => {
    const error = errorPatterns.find(pattern => pattern.id === errorId)
    setActiveTableTab('errors')
    setSelectedEvidenceId('')
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

  const changeVariant = (nextVariant: VariantId) => {
    setVariant(nextVariant)
    setActiveGraphCursor(null)
  }

  const toggleGraphWallGroup = (groupKey: string) => {
    setGraphWallGroupVisibility(prev => ({
      ...prev,
      [groupKey]: prev[groupKey] === false,
    }))
  }

  return (
    <div className="page command-center-page">
      {emptyMode ? (
        <CommandCenterEmptyState onOpenResults={onOpenResults} />
      ) : (
        <>
      <RunControlShelf
        test={displayTest}
        status={runStatus}
        remainingSeconds={remainingSeconds}
        totalCurrent={totalCurrent}
        totalTarget={totalTarget}
        targetMode={targetMode}
        errorPct={displayTest.live_metrics?.error_pct ?? MOCK_LIVE_METRICS.error_pct}
        feedback={feedback}
        historicalRun={historicalRun}
        onStart={() => { void startRun() }}
        onPauseResume={() => { void togglePause() }}
        onStop={() => { void stopRun() }}
        onAbort={() => { void abortRun() }}
        onOpenExecution={testId && onOpenExecution ? () => onOpenExecution(testId) : undefined}
      />
      {historicalRun && (
        <CommandCenterHistoricalBanner
          status={runStatus}
          archiveReady={Boolean(displayTest.archive)}
          onOpenArchive={testId && onOpenExecution ? () => onOpenExecution(testId) : undefined}
          onOpenResults={onOpenResults}
        />
      )}

      {variant === 'ops' && (
        <OpsCockpitView
          variant={variant}
          onVariantChange={changeVariant}
          groups={groups}
          metrics={displayTest.live_metrics}
          errorPatterns={errorPatterns}
          selectedError={selectedError}
          selectedErrorEvidence={selectedErrorEvidence}
          selectedEvidenceId={selectedEvidenceId}
          onEvidenceSelect={setSelectedEvidenceId}
          activeTableTab={activeTableTab}
          onTableTabChange={setActiveTableTab}
          evidenceLoading={Boolean(selectedError && evidenceLoadingSignature === selectedError.signature)}
          evidenceError={evidenceError}
          runStatus={runStatus}
          totalDurationSeconds={totalDurationSeconds}
          remainingSeconds={remainingSeconds}
          readOnly={historicalRun}
          onTargetChange={changeTarget}
          onApplyTarget={applyTarget}
          onErrorSelect={selectError}
        />
      )}
      {variant === 'grafana' && (
        <GrafanaWallView
          variant={variant}
          onVariantChange={changeVariant}
          groups={groups}
          chartGroups={graphWallGroups}
          groupVisibility={graphWallGroupVisibility}
          onGroupVisibilityToggle={toggleGraphWallGroup}
          metrics={displayTest.live_metrics}
          chartData={chartData}
          groupScopedChartData={graphScopedChartData}
          groupScopedMetrics={graphScopedMetrics}
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
          runStatus={runStatus}
          totalDurationSeconds={totalDurationSeconds}
          remainingSeconds={remainingSeconds}
          onTargetChange={changeTarget}
          onApplyTarget={applyTarget}
          onErrorSelect={selectError}
        />
      )}
        </>
      )}
    </div>
  )
}

function CommandCenterEmptyState({ onOpenResults }: { onOpenResults?: () => void }) {
  return (
    <section className="command-empty-state" aria-label="No active command center run">
      <div>
        <h2>No active run selected</h2>
        <p>
          Start a test or open the Command Center from an active run to see live controls and metrics.
          Historical results can be opened from the Results folder.
        </p>
        {onOpenResults && (
          <div className="command-empty-actions">
            <button type="button" className="btn btn-sm btn-secondary" onClick={onOpenResults}>
              Open Results
            </button>
          </div>
        )}
      </div>
    </section>
  )
}

interface RunControlShelfProps {
  test: TestSummary
  status: TestStatus
  remainingSeconds: number
  totalCurrent: number
  totalTarget: number
  targetMode: CommandGroup['targetMode']
  errorPct: number
  feedback: string
  historicalRun?: boolean
  onStart: () => void
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
  targetMode,
  errorPct,
  feedback,
  historicalRun = false,
  onStart,
  onPauseResume,
  onStop,
  onAbort,
  onOpenExecution,
}: RunControlShelfProps) {
  const terminal = status === 'ABORTED' || status === 'STOPPING' || status === 'COMPLETE' || status === 'FAILED'
  const canStart = status === 'QUEUED' || status === 'RESERVED'
  const canPauseResume = status === 'RUNNING' || status === 'PAUSED'
  const canStop = status === 'RUNNING' || status === 'INITIALIZING' || status === 'PAUSED'
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
        <MetricPill
          label={targetMode === 'tps' ? 'Target TPS' : 'Target Load'}
          value={targetMode === 'tps' ? formatNumber(totalTarget, 1) : `${formatNumber(totalTarget)} VU`}
          tone="accent"
        />
        <MetricPill label="Error Rate" value={formatPercent(errorPct)} tone="danger" />
      </div>

      <div className="command-run-actions">
        {onOpenExecution && !historicalRun && (
          <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenExecution}>
            Details
          </button>
        )}
        {!historicalRun && (
          <>
            {canStart && (
              <button type="button" className="btn btn-sm btn-success" onClick={onStart}>
                Start Test
              </button>
            )}
            {canPauseResume && (
              <button type="button" className={`btn btn-sm ${status === 'PAUSED' ? 'btn-success' : 'btn-secondary'}`} onClick={onPauseResume} disabled={terminal}>
                {status === 'PAUSED' ? 'Resume' : 'Pause'}
              </button>
            )}
            {canStop && (
              <button type="button" className="btn btn-sm btn-secondary" onClick={onStop} disabled={terminal}>
                Stop
              </button>
            )}
            {!terminal && (
              <button type="button" className="btn btn-sm btn-danger" onClick={onAbort}>
                Abort
              </button>
            )}
          </>
        )}
      </div>

      <p className="command-control-feedback" role="status">{feedback}</p>
    </section>
  )
}

function CommandCenterHistoricalBanner({
  status,
  archiveReady,
  onOpenArchive,
  onOpenResults,
}: {
  status: TestStatus
  archiveReady: boolean
  onOpenArchive?: () => void
  onOpenResults?: () => void
}) {
  return (
    <section className="command-history-banner" aria-label="Historical run snapshot">
      <div>
        <strong>Historical run snapshot</strong>
        <p>
          This run is {status.toLowerCase()}. Live pause, stop, abort, and scaling controls are unavailable.
          {archiveReady
            ? ' Open the results archive for saved evidence and reports.'
            : ' Historical results can be opened from the Results folder when no Command Center snapshot is available.'}
        </p>
      </div>
      <div className="command-history-actions">
        {archiveReady && onOpenArchive && (
          <button type="button" className="btn btn-sm btn-secondary" onClick={onOpenArchive}>
            Open Results Archive
          </button>
        )}
        {!archiveReady && onOpenResults && (
          <button type="button" className="btn btn-sm btn-secondary" onClick={onOpenResults}>
            Open Results
          </button>
        )}
      </div>
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
  variant: VariantId
  onVariantChange: (variant: VariantId) => void
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
  runStatus: TestStatus
  totalDurationSeconds: number
  remainingSeconds: number
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onErrorSelect: (errorId: string) => void
}

interface OpsCockpitViewProps {
  variant: VariantId
  onVariantChange: (variant: VariantId) => void
  groups: CommandGroup[]
  metrics?: LiveMetrics
  errorPatterns: ErrorPattern[]
  selectedError?: ErrorPattern
  selectedErrorEvidence: LiveErrorEvidence[]
  selectedEvidenceId: string
  onEvidenceSelect: (evidenceId: string) => void
  activeTableTab: CommandTableTab
  onTableTabChange: (tab: CommandTableTab) => void
  evidenceLoading: boolean
  evidenceError: string
  runStatus: TestStatus
  totalDurationSeconds: number
  remainingSeconds: number
  readOnly?: boolean
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onErrorSelect: (errorId: string) => void
}

interface GrafanaWallViewProps extends CommonViewProps {
  chartGroups: CommandGroup[]
  groupScopedChartData: MetricsHistoryPoint[]
  groupScopedMetrics?: LiveMetrics
  groupVisibility: Record<string, boolean>
  onGroupVisibilityToggle: (groupKey: string) => void
}

function OpsCockpitView({
  variant,
  onVariantChange,
  groups,
  metrics,
  errorPatterns,
  selectedError,
  selectedErrorEvidence,
  selectedEvidenceId,
  onEvidenceSelect,
  activeTableTab,
  onTableTabChange,
  evidenceLoading,
  evidenceError,
  runStatus,
  totalDurationSeconds,
  remainingSeconds,
  readOnly = false,
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
}: OpsCockpitViewProps) {
  return (
    <section className="command-layout command-layout-ops" aria-label="Ops cockpit mockup">
      <div className="command-primary-column">
        <HeroMetricStrip groups={groups} metrics={metrics} />
        <div className="command-load-model-row">
          <GroupLoadTable groups={groups} onTargetChange={onTargetChange} onApplyTarget={onApplyTarget} compact={false} paired readOnly={readOnly} />
          <LoadModelPanel
            groups={groups}
            runStatus={runStatus}
            totalDurationSeconds={totalDurationSeconds}
            remainingSeconds={remainingSeconds}
          />
        </div>
        <CommandRunTables
          groups={groups}
          metrics={metrics}
          patterns={errorPatterns}
          selectedError={selectedError}
          selectedEvidence={selectedErrorEvidence}
          selectedEvidenceId={selectedEvidenceId}
          onEvidenceSelect={onEvidenceSelect}
          activeTab={activeTableTab}
          onTabChange={onTableTabChange}
          onErrorSelect={onErrorSelect}
          evidenceLoading={evidenceLoading}
          evidenceError={evidenceError}
        />
      </div>
      <aside className="command-side-rail" aria-label="Command center sidebar">
        <CommandViewSwitchPanel variant={variant} onVariantChange={onVariantChange} />
        <ErrorDrilldown patterns={errorPatterns} selectedError={selectedError} onErrorSelect={onErrorSelect} />
        <CapacitySnapshot groups={groups} metrics={metrics} />
      </aside>
    </section>
  )
}

function GrafanaWallView({
  variant,
  onVariantChange,
  groups,
  chartGroups,
  groupScopedChartData,
  groupScopedMetrics,
  groupVisibility,
  onGroupVisibilityToggle,
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
  onErrorSelect,
}: GrafanaWallViewProps) {
  const [graphOptionsTarget, setGraphOptionsTarget] = useState<HTMLDivElement | null>(null)
  const [activeGraphGroupTab, setActiveGraphGroupTab] = useState<GraphGroupTabId>('test_results')
  const [underLoadDisplayMode, setUnderLoadDisplayMode] = useState<UnderLoadDisplayMode>('graphs')
  const bindGraphOptionsTarget = useCallback((node: HTMLDivElement | null) => {
    setGraphOptionsTarget(node)
  }, [])
  const showUnderLoadDisplaySwitch = activeGraphGroupTab === 'under_load'

  return (
    <section className="command-layout command-layout-grafana" aria-label="Grafana wall mockup">
      <div className="command-grafana-grid">
        <CommandRuntimeGraphs
          variant="grafana"
          groups={chartGroups}
          metrics={metrics}
          chartData={chartData}
          groupScopedChartData={groupScopedChartData}
          groupScopedMetrics={groupScopedMetrics}
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
          activeGraphGroupTab={activeGraphGroupTab}
          onActiveGraphGroupTabChange={setActiveGraphGroupTab}
          underLoadDisplayMode={underLoadDisplayMode}
          externalControlsTarget={graphOptionsTarget}
        />
        <div className="command-grafana-side">
          <CommandViewSwitchPanel variant={variant} onVariantChange={onVariantChange} />
          {showUnderLoadDisplaySwitch && (
            <CommandUnderLoadDisplaySwitchPanel
              value={underLoadDisplayMode}
              onChange={setUnderLoadDisplayMode}
            />
          )}
          <GroupSelectionPanel groups={groups} groupVisibility={groupVisibility} onToggle={onGroupVisibilityToggle} />
          <ErrorDrilldown patterns={errorPatterns} selectedError={selectedError} onErrorSelect={onErrorSelect} compact />
          <div className="command-graph-options-slot" ref={bindGraphOptionsTarget} />
        </div>
      </div>
    </section>
  )
}

function CommandViewSwitchPanel({ variant, onVariantChange }: { variant: VariantId; onVariantChange: (variant: VariantId) => void }) {
  return (
    <section className="command-panel command-view-switch-panel" aria-label="Command center view mode">
      <PageViewToggle
        value={variant}
        options={VARIANT_OPTIONS}
        ariaLabel="Command center views"
        className="command-center-variant-toggle"
        onChange={onVariantChange}
      />
    </section>
  )
}

function CommandUnderLoadDisplaySwitchPanel({ value, onChange }: { value: UnderLoadDisplayMode; onChange: (value: UnderLoadDisplayMode) => void }) {
  return (
    <section className="command-panel command-under-load-display-panel" aria-label="Under Load display mode">
      <PageViewToggle
        value={value}
        options={UNDER_LOAD_DISPLAY_OPTIONS}
        ariaLabel="Under Load display mode"
        className="command-under-load-display-toggle"
        onChange={onChange}
      />
    </section>
  )
}

function GroupSelectionPanel({
  groups,
  groupVisibility,
  onToggle,
}: {
  groups: CommandGroup[]
  groupVisibility: Record<string, boolean>
  onToggle: (groupKey: string) => void
}) {
  const visibleCount = groups.filter(group => groupVisibility[group.key] !== false).length

  return (
    <section className="command-panel command-group-selection-panel" aria-label="Group selection">
      <div className="command-panel-header">
        <div>
          <h3>Group Selection</h3>
          <p>{visibleCount} / {groups.length} groups visible</p>
        </div>
      </div>
      <div className="command-group-selection-list">
        {groups.map(group => {
          const visible = groupVisibility[group.key] !== false
          return (
            <button
              key={group.key}
              type="button"
              className={`command-group-select-toggle ${visible ? 'active' : ''}`}
              data-state={visible ? 'selected' : 'hidden'}
              aria-pressed={visible}
              aria-label={`${group.name} graph visibility`}
              style={{ '--command-group-color': group.color } as CSSProperties}
              onClick={() => onToggle(group.key)}
            >
              <span className="command-group-select-swatch" />
              <strong>{group.name}</strong>
              <small className="command-group-select-status">
                <span className="command-group-select-status-dot" aria-hidden="true" />
                {visible ? 'Selected' : 'Hidden'}
              </small>
            </button>
          )
        })}
      </div>
    </section>
  )
}

const COMMAND_TABLE_TABS: Array<{ id: CommandTableTab; label: string }> = [
  { id: 'scenario', label: 'Scenario Run' },
  { id: 'errors', label: 'Error Details' },
  { id: 'infrastructure', label: 'Infrastructure' },
]

type CommandSortState = {
  key: string
  direction: SortDirection
}

interface ScenarioRunRow {
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

interface InfrastructureRow {
  id: string
  type: 'Generator' | 'Capacity' | 'Route'
  generatorId: string
  target: string
  region: string
  activeVusers?: number
  tps?: number
  errorPct?: number
  p95?: number
  p99?: number
  saturation: string
  cpu?: number
  memory?: number
  network?: number
  disk?: number
  gc?: number
  impact: string
}

interface CommandRunTablesProps {
  groups: CommandGroup[]
  metrics?: LiveMetrics
  patterns: ErrorPattern[]
  selectedError?: ErrorPattern
  selectedEvidence: LiveErrorEvidence[]
  selectedEvidenceId: string
  onEvidenceSelect: (evidenceId: string) => void
  activeTab: CommandTableTab
  onTabChange: (tab: CommandTableTab) => void
  onErrorSelect: (errorId: string) => void
  evidenceLoading: boolean
  evidenceError: string
}

function CommandRunTables({
  groups,
  metrics,
  patterns,
  selectedError,
  selectedEvidence,
  selectedEvidenceId,
  onEvidenceSelect,
  activeTab,
  onTabChange,
  onErrorSelect,
  evidenceLoading,
  evidenceError,
}: CommandRunTablesProps) {
  const [filter, setFilter] = useState('')
  const [sorts, setSorts] = useState<Record<CommandTableTab, CommandSortState>>({
    scenario: { key: 'type', direction: 'asc' },
    errors: { key: 'count', direction: 'desc' },
    infrastructure: { key: 'type', direction: 'asc' },
  })
  const activeSort = sorts[activeTab]
  const scenarioRows = useMemo(() => buildScenarioRunRows(groups, metrics), [groups, metrics])
  const infrastructureRows = useMemo(() => buildInfrastructureRows(groups, metrics), [groups, metrics])
  const activeEvidence = selectedEvidence.find(item => item.id === selectedEvidenceId) || selectedEvidence[0]

  useEffect(() => {
    setFilter('')
  }, [activeTab])

  const updateSort = (key: string) => {
    setSorts(prev => {
      const current = prev[activeTab]
      const direction: SortDirection = current.key === key && current.direction === 'asc' ? 'desc' : 'asc'
      return { ...prev, [activeTab]: { key, direction } }
    })
  }

  const filteredScenarioRows = useMemo(
    () => filterAndSortRows(scenarioRows, filter, activeSort),
    [activeSort, filter, scenarioRows],
  )
  const filteredErrorRows = useMemo(
    () => filterAndSortRows(patterns, filter, activeSort),
    [activeSort, filter, patterns],
  )
  const filteredInfrastructureRows = useMemo(
    () => filterAndSortRows(infrastructureRows, filter, activeSort),
    [activeSort, filter, infrastructureRows],
  )

  return (
    <section className="command-run-tables" aria-label="Command center run tables">
      <div className="command-table-tabs" role="tablist" aria-label="OPS data tables">
        <div className="command-table-tab-buttons">
          {COMMAND_TABLE_TABS.map(tab => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              className={activeTab === tab.id ? 'active' : ''}
              aria-selected={activeTab === tab.id}
              onClick={() => onTabChange(tab.id)}
            >
              {tab.label}
            </button>
          ))}
        </div>
        <label className="command-table-filter">
          <span>Filter</span>
          <input
            type="search"
            value={filter}
            aria-label="Filter command center table rows"
            placeholder="Filter rows"
            onChange={(event) => setFilter(event.target.value)}
          />
        </label>
      </div>

      <div className="command-table-surface">
        {activeTab === 'scenario' && (
          <ScenarioRunTable rows={filteredScenarioRows} sort={activeSort} onSort={updateSort} />
        )}
        {activeTab === 'errors' && (
          <ErrorDetailsTable
            rows={filteredErrorRows}
            sort={activeSort}
            selectedError={selectedError}
            selectedEvidence={selectedEvidence}
            activeEvidence={activeEvidence}
            evidenceLoading={evidenceLoading}
            evidenceError={evidenceError}
            onSort={updateSort}
            onErrorSelect={onErrorSelect}
            onEvidenceSelect={onEvidenceSelect}
          />
        )}
        {activeTab === 'infrastructure' && (
          <InfrastructureTable rows={filteredInfrastructureRows} sort={activeSort} onSort={updateSort} />
        )}
      </div>
    </section>
  )
}

function ScenarioRunTable({
  rows,
  sort,
  onSort,
}: {
  rows: ScenarioRunRow[]
  sort: CommandSortState
  onSort: (key: string) => void
}) {
  return (
    <div className="command-data-table-wrap" role="tabpanel" aria-label="Scenario run metrics">
      <table className="command-data-table command-scenario-table">
        <thead>
          <tr>
            <SortableHeader label="Type" sortKey="type" sort={sort} onSort={onSort} />
            <SortableHeader label="Name" sortKey="name" sort={sort} onSort={onSort} />
            <SortableHeader label="Group" sortKey="group" sort={sort} onSort={onSort} />
            <SortableHeader label="Protocol" sortKey="protocol" sort={sort} onSort={onSort} />
            <SortableHeader label="Current VU" sortKey="currentVusers" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Target VU" sortKey="targetVusers" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="TPS" sortKey="tps" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Target TPS" sortKey="targetTps" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Gap" sortKey="throughputGap" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Tx" sortKey="transactions" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Errors" sortKey="errors" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Error %" sortKey="errorPct" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="ART" sortKey="art" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p50" sortKey="p50" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p90" sortKey="p90" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p95" sortKey="p95" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p99" sortKey="p99" sort={sort} onSort={onSort} align="right" />
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.id} className={row.type === 'Group' ? 'command-table-group-row' : ''}>
              <td>{row.type}</td>
              <td><strong>{row.name}</strong></td>
              <td>{row.group}</td>
              <td>{row.protocol}</td>
              <td className="numeric">{formatOptionalNumber(row.currentVusers)}</td>
              <td className="numeric">{formatOptionalNumber(row.targetVusers)}</td>
              <td className="numeric">{formatOptionalNumber(row.tps, 1)}</td>
              <td className="numeric">{formatOptionalNumber(row.targetTps, 1)}</td>
              <td className={`numeric ${metricTone(Math.abs(row.throughputGap || 0), 5, 15)}`}>{formatOptionalPercent(row.throughputGap)}</td>
              <td className="numeric">{formatOptionalNumber(row.transactions)}</td>
              <td className="numeric">{formatOptionalNumber(row.errors)}</td>
              <td className={`numeric ${metricTone(row.errorPct, 0.75, 1.5)}`}>{formatPercent(row.errorPct)}</td>
              <td className="numeric">{formatOptionalMs(row.art)}</td>
              <td className="numeric">{formatOptionalMs(row.p50)}</td>
              <td className="numeric">{formatOptionalMs(row.p90)}</td>
              <td className="numeric">{formatOptionalMs(row.p95)}</td>
              <td className="numeric">{formatOptionalMs(row.p99)}</td>
            </tr>
          ))}
          {rows.length === 0 && <EmptyTableRow colSpan={17} message="No scenario metrics match the current filter." />}
        </tbody>
      </table>
    </div>
  )
}

function ErrorDetailsTable({
  rows,
  sort,
  selectedError,
  selectedEvidence,
  activeEvidence,
  evidenceLoading,
  evidenceError,
  onSort,
  onErrorSelect,
  onEvidenceSelect,
}: {
  rows: ErrorPattern[]
  sort: CommandSortState
  selectedError?: ErrorPattern
  selectedEvidence: LiveErrorEvidence[]
  activeEvidence?: LiveErrorEvidence
  evidenceLoading: boolean
  evidenceError: string
  onSort: (key: string) => void
  onErrorSelect: (errorId: string) => void
  onEvidenceSelect: (evidenceId: string) => void
}) {
  return (
    <div className="command-error-table-layout" role="tabpanel" aria-label="Error details">
      <div className="command-data-table-wrap command-error-pattern-table-wrap">
        <table className="command-data-table command-error-table">
          <thead>
            <tr>
              <SortableHeader label="Signature" sortKey="title" sort={sort} onSort={onSort} />
              <SortableHeader label="Count" sortKey="count" sort={sort} onSort={onSort} align="right" />
              <SortableHeader label="Rate" sortKey="rate" sort={sort} onSort={onSort} align="right" />
              <SortableHeader label="Status" sortKey="statusCode" sort={sort} onSort={onSort} align="right" />
              <SortableHeader label="Affected Groups" sortKey="group" sort={sort} onSort={onSort} />
              <SortableHeader label="Actions" sortKey="transaction" sort={sort} onSort={onSort} />
              <SortableHeader label="First Seen" sortKey="firstSeen" sort={sort} onSort={onSort} />
              <SortableHeader label="Latest Seen" sortKey="latestSeen" sort={sort} onSort={onSort} />
              <SortableHeader label="Evidence" sortKey="evidenceCount" sort={sort} onSort={onSort} align="right" />
            </tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr
                key={row.id}
                className={selectedError?.id === row.id ? 'selected' : ''}
                onClick={() => onErrorSelect(row.id)}
              >
                <td><strong>{row.title}</strong><span className="command-table-subtext">{row.sample}</span></td>
                <td className="numeric">{formatNumber(row.count)}</td>
                <td className={`numeric ${metricTone(row.rate, 0.75, 1.5)}`}>{formatPercent(row.rate)}</td>
                <td className="numeric">{row.statusCode || '--'}</td>
                <td>{row.affectedGroups.join(', ') || row.group}</td>
                <td>{row.actions.join(', ') || row.transaction}</td>
                <td>{row.firstSeen}</td>
                <td>{row.latestSeen}</td>
                <td className="numeric">{row.evidenceCount ?? selectedEvidence.length}</td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyTableRow colSpan={9} message="No error patterns match the current filter." />}
          </tbody>
        </table>
      </div>
      <ErrorEvidencePanel
        selectedError={selectedError}
        evidence={selectedEvidence}
        activeEvidence={activeEvidence}
        loading={evidenceLoading}
        error={evidenceError}
        onEvidenceSelect={onEvidenceSelect}
      />
    </div>
  )
}

function ErrorEvidencePanel({
  selectedError,
  evidence,
  activeEvidence,
  loading,
  error,
  onEvidenceSelect,
}: {
  selectedError?: ErrorPattern
  evidence: LiveErrorEvidence[]
  activeEvidence?: LiveErrorEvidence
  loading: boolean
  error: string
  onEvidenceSelect: (evidenceId: string) => void
}) {
  if (!selectedError) {
    return (
      <div className="command-evidence-panel empty">
        <strong>No error selected</strong>
        <span>Error evidence appears after selecting a signature.</span>
      </div>
    )
  }

  return (
    <div className="command-evidence-panel" aria-label="Live error evidence">
      <div className="command-evidence-header">
        <div>
          <span>Selected Signature</span>
          <strong>{selectedError.title}</strong>
        </div>
        <small>{selectedError.evidenceCount ?? evidence.length} captured samples</small>
      </div>
      {loading && <p className="command-table-status">Loading live evidence...</p>}
      {error && <p className="command-table-status danger">{error}</p>}
      {!loading && !error && evidence.length === 0 && (
        <p className="command-table-status">
          Snapshot evidence unavailable for this live signature. Capture requires `snapshot_on_error` and a failed transaction snapshot.
        </p>
      )}
      {evidence.length > 0 && (
        <>
          <div className="command-data-table-wrap command-evidence-table-wrap">
            <table className="command-data-table command-evidence-table">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Action</th>
                  <th>Group</th>
                  <th className="numeric">Duration</th>
                  <th className="numeric">Status</th>
                  <th>Error</th>
                </tr>
              </thead>
              <tbody>
                {evidence.map(item => (
                  <tr
                    key={item.id}
                    className={activeEvidence?.id === item.id ? 'selected' : ''}
                    onClick={() => onEvidenceSelect(item.id)}
                  >
                    <td>{formatTimeValue(item.timestamp)}</td>
                    <td>{item.action}</td>
                    <td>{item.group || '--'}</td>
                    <td className="numeric">{formatOptionalMs(item.duration_ms)}</td>
                    <td className="numeric">{item.status_code || item.status}</td>
                    <td>{item.error_message || item.signature}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {activeEvidence && <EvidenceSnapshotDetail evidence={activeEvidence} />}
        </>
      )}
    </div>
  )
}

function EvidenceSnapshotDetail({ evidence }: { evidence: LiveErrorEvidence }) {
  const snapshot = evidence.snapshot
  return (
    <div className="command-evidence-detail" aria-label="Request and response evidence">
      <div className="command-evidence-detail-title">
        <strong>{evidence.action}</strong>
        <span>{evidence.group || 'Ungrouped'} / {formatOptionalMs(evidence.duration_ms)}</span>
      </div>
      {!snapshot && (
        <p className="command-table-status">Snapshot evidence unavailable for this event.</p>
      )}
      {snapshot && (
        <div className="command-snapshot-grid">
          <section>
            <span className="scenario-script-detail-label">Request headers</span>
            <SnapshotTextPreview
              text={formatSnapshotRequestLineAndHeaders(snapshotSection(snapshot))}
              preClassName="command-snapshot-pre"
            />
            <span className="scenario-script-detail-label">Request body</span>
            <SnapshotBodyPreview
              body={snapshot.request_body}
              headers={snapshot.request_headers}
              emptyMessage="No request body captured"
              preClassName="command-snapshot-pre"
            />
          </section>
          <section>
            <span className="scenario-script-detail-label">Response headers</span>
            <SnapshotTextPreview
              text={formatSnapshotResponseStatusAndHeaders(snapshotSection(snapshot), evidence.status_code)}
              preClassName="command-snapshot-pre"
            />
            <span className="scenario-script-detail-label">Response body</span>
            <SnapshotBodyPreview
              body={snapshot.response_body}
              headers={snapshot.response_headers}
              emptyMessage="No response body captured"
              preClassName="command-snapshot-pre"
            />
          </section>
        </div>
      )}
    </div>
  )
}

function InfrastructureTable({
  rows,
  sort,
  onSort,
}: {
  rows: InfrastructureRow[]
  sort: CommandSortState
  onSort: (key: string) => void
}) {
  return (
    <div className="command-data-table-wrap" role="tabpanel" aria-label="Infrastructure details">
      <table className="command-data-table command-infra-table">
        <thead>
          <tr>
            <SortableHeader label="Type" sortKey="type" sort={sort} onSort={onSort} />
            <SortableHeader label="Generator" sortKey="generatorId" sort={sort} onSort={onSort} />
            <SortableHeader label="Target" sortKey="target" sort={sort} onSort={onSort} />
            <SortableHeader label="Region" sortKey="region" sort={sort} onSort={onSort} />
            <SortableHeader label="Active VU" sortKey="activeVusers" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="TPS" sortKey="tps" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Error %" sortKey="errorPct" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p95" sortKey="p95" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="p99" sortKey="p99" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Saturation" sortKey="saturation" sort={sort} onSort={onSort} />
            <SortableHeader label="CPU" sortKey="cpu" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Memory" sortKey="memory" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Network" sortKey="network" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Disk" sortKey="disk" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="GC" sortKey="gc" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Impact" sortKey="impact" sort={sort} onSort={onSort} />
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.id} className={`command-infra-row ${row.saturation}`}>
              <td>{row.type}</td>
              <td><strong>{row.generatorId}</strong></td>
              <td>{row.target}</td>
              <td>{row.region}</td>
              <td className="numeric">{formatOptionalNumber(row.activeVusers)}</td>
              <td className="numeric">{formatOptionalNumber(row.tps, 1)}</td>
              <td className={`numeric ${metricTone(row.errorPct || 0, 0.75, 1.5)}`}>{formatOptionalPercent(row.errorPct)}</td>
              <td className="numeric">{formatOptionalMs(row.p95)}</td>
              <td className="numeric">{formatOptionalMs(row.p99)}</td>
              <td><span className={`command-state-chip ${row.saturation}`}>{row.saturation}</span></td>
              <td className="numeric">{formatOptionalPercent(row.cpu)}</td>
              <td className="numeric">{formatOptionalPercent(row.memory)}</td>
              <td className="numeric">{formatOptionalNumber(row.network, 1)}</td>
              <td className="numeric">{formatOptionalPercent(row.disk)}</td>
              <td className="numeric">{formatOptionalMs(row.gc)}</td>
              <td>{row.impact}</td>
            </tr>
          ))}
          {rows.length === 0 && <EmptyTableRow colSpan={16} message="No infrastructure rows match the current filter." />}
        </tbody>
      </table>
    </div>
  )
}

function SortableHeader({
  label,
  sortKey,
  sort,
  onSort,
  align,
}: {
  label: string
  sortKey: string
  sort: CommandSortState
  onSort: (key: string) => void
  align?: 'right'
}) {
  const active = sort.key === sortKey
  return (
    <th className={align === 'right' ? 'numeric' : undefined} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" onClick={() => onSort(sortKey)}>
        <span>{label}</span>
        <small>{active ? (sort.direction === 'asc' ? '▲' : '▼') : '·'}</small>
      </button>
    </th>
  )
}

function EmptyTableRow({ colSpan, message }: { colSpan: number; message: string }) {
  return (
    <tr>
      <td colSpan={colSpan} className="command-table-empty">{message}</td>
    </tr>
  )
}

function buildScenarioRunRows(groups: CommandGroup[], metrics?: LiveMetrics): ScenarioRunRow[] {
  const rows: ScenarioRunRow[] = groups.map(group => {
    const groupMetrics = metrics?.groups?.[group.name]
    const transactions = groupMetrics?.transactions ?? group.transactions
    const errors = groupMetrics?.errors ?? group.errors
    const tps = groupMetrics?.tps ?? group.tps
    const targetTps = group.targetMode === 'tps'
      ? group.targetTps ?? group.target
      : groupMetrics?.target_tps ?? group.targetTps ?? group.tps * 1.12
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
      throughputGap: group.targetMode === 'tps' ? calculateThroughputGap(tps, targetTps) : groupMetrics?.throughput_gap_pct ?? calculateThroughputGap(tps, targetTps),
      transactions,
      errors,
      errorPct: errorPctFromCounts(errors, transactions),
      art: groupMetrics?.avg_response_ms ?? group.art,
      p50: groupMetrics?.p50_ms ?? group.art * 0.72,
      p90: groupMetrics?.p90_ms ?? group.art * 1.55,
      p95: groupMetrics?.p95_ms ?? group.art * 1.95,
      p99: groupMetrics?.p99_ms ?? group.p99,
    }
  })

  Object.entries(metrics?.transactions || {}).forEach(([name, tx]) => {
    if (!tx) return
    rows.push({
      id: `transaction-${name}`,
      type: 'Transaction',
      name,
      group: 'All groups',
      protocol: tx.dependency_type || 'transaction',
      tps: tx.count,
      transactions: tx.count,
      errors: tx.errors,
      errorPct: errorPctFromCounts(tx.errors, tx.count),
      art: tx.avg_ms,
      p50: tx.p50_ms,
      p90: tx.p90_ms,
      p95: tx.p95_ms,
      p99: tx.p99_ms,
    })
  })

  return rows
}

function buildInfrastructureRows(groups: CommandGroup[], metrics?: LiveMetrics): InfrastructureRow[] {
  const rows: InfrastructureRow[] = []
  Object.values(metrics?.generator_resources || {}).forEach(generator => {
    const resources = generator.resources
    rows.push({
      id: `generator-${generator.generator_id}`,
      type: 'Generator',
      generatorId: generator.generator_id,
      target: generator.target,
      region: generator.region || 'unassigned',
      activeVusers: generator.active_vusers,
      tps: generator.tps,
      errorPct: generator.error_pct,
      p95: generator.p95_ms,
      p99: generator.p99_ms,
      saturation: generator.saturation_state || 'unknown',
      cpu: resources?.container_cpu_usage_pct ?? resources?.cpu_process_pct,
      memory: resources?.container_memory_usage_pct,
      network: (resources?.net_sent_kbps || 0) + (resources?.net_recv_kbps || 0) || resources?.tcp_retransmits_per_sec,
      disk: resources?.disk_io_time_pct,
      gc: resources?.gc_pause_ms,
      impact: generator.impact_reasons?.join(', ') || 'clear',
    })
  })

  const capacity = metrics?.capacity_status
  Object.entries(capacity?.by_region || {}).forEach(([region, value]) => {
    rows.push({
      id: `capacity-region-${region}`,
      type: 'Capacity',
      generatorId: 'region pool',
      target: 'capacity allocation',
      region,
      activeVusers: value.allocated_vusers,
      saturation: value.rejected_vusers && value.rejected_vusers > 0 ? 'danger' : value.queued_vusers && value.queued_vusers > 0 ? 'warning' : 'clear',
      impact: `queued ${formatNumber(value.queued_vusers || 0)} / rejected ${formatNumber(value.rejected_vusers || 0)}`,
    })
  })
  Object.entries(capacity?.by_protocol || {}).forEach(([protocol, value]) => {
    rows.push({
      id: `capacity-protocol-${protocol}`,
      type: 'Capacity',
      generatorId: protocol,
      target: 'protocol allocation',
      region: 'all regions',
      activeVusers: value.allocated_vusers,
      saturation: value.rejected_vusers && value.rejected_vusers > 0 ? 'danger' : value.queued_vusers && value.queued_vusers > 0 ? 'warning' : 'clear',
      impact: `queued ${formatNumber(value.queued_vusers || 0)} / rejected ${formatNumber(value.rejected_vusers || 0)}`,
    })
  })

  metrics?.latency?.targets?.forEach(target => {
    rows.push({
      id: `route-${target.name}-${target.endpoint}`,
      type: 'Route',
      generatorId: target.name,
      target: target.endpoint,
      region: target.ip || 'route probe',
      activeVusers: groups.reduce((sum, group) => sum + group.current, 0),
      p95: target.probe_total_ms,
      p99: target.latency_ms + (target.jitter_ms || 0),
      saturation: target.last_success ? target.route_status || 'clear' : 'danger',
      network: target.latency_ms,
      impact: `dns ${formatOptionalMs(target.dns_ms)} / connect ${formatOptionalMs(target.connect_ms)} / ttfb ${formatOptionalMs(target.ttfb_ms)}`,
    })
  })

  if (rows.length === 0 && metrics?.resources) {
    rows.push({
      id: 'aggregate-resources',
      type: 'Generator',
      generatorId: 'aggregate',
      target: 'controller snapshot',
      region: 'all',
      activeVusers: metrics.active_vusers,
      tps: metrics.tps,
      errorPct: metrics.error_pct,
      p95: metrics.p95_ms,
      p99: metrics.p99_ms,
      saturation: 'clear',
      cpu: metrics.resources.container_cpu_usage_pct ?? metrics.resources.cpu_process_pct,
      memory: metrics.resources.container_memory_usage_pct,
      network: (metrics.resources.net_sent_kbps || 0) + (metrics.resources.net_recv_kbps || 0),
      disk: metrics.resources.disk_io_time_pct,
      gc: metrics.resources.gc_pause_ms,
      impact: 'aggregate live metrics',
    })
  }

  return rows
}

function filterAndSortRows<T extends object>(rows: T[], filter: string, sort: CommandSortState): T[] {
  const query = filter.trim().toLowerCase()
  const filtered = query
    ? rows.filter(row => rowMatchesFilter(row as Record<string, unknown>, query))
    : rows
  return [...filtered].sort((left, right) => compareSortValues(
    (left as Record<string, unknown>)[sort.key],
    (right as Record<string, unknown>)[sort.key],
    sort.direction,
  ))
}

function rowMatchesFilter(row: Record<string, unknown>, query: string) {
  return Object.values(row).some(value => {
    if (value == null) return false
    if (Array.isArray(value)) return value.join(' ').toLowerCase().includes(query)
    return String(value).toLowerCase().includes(query)
  })
}

function compareSortValues(left: unknown, right: unknown, direction: SortDirection) {
  const leftNumber = typeof left === 'number' && Number.isFinite(left) ? left : Number.NaN
  const rightNumber = typeof right === 'number' && Number.isFinite(right) ? right : Number.NaN
  let result = 0
  if (Number.isFinite(leftNumber) || Number.isFinite(rightNumber)) {
    result = (Number.isFinite(leftNumber) ? leftNumber : -Infinity) - (Number.isFinite(rightNumber) ? rightNumber : -Infinity)
  } else {
    result = String(left ?? '').localeCompare(String(right ?? ''), undefined, { numeric: true, sensitivity: 'base' })
  }
  return direction === 'asc' ? result : -result
}

function errorPctFromCounts(errors: number, successes: number) {
  const attempts = errors + successes
  if (attempts <= 0) return 0
  return errors / attempts * 100
}

function calculateThroughputGap(tps: number, targetTps?: number) {
  if (!targetTps || targetTps <= 0) return 0
  return (targetTps - tps) / targetTps * 100
}

function formatOptionalNumber(value?: number, digits = 0) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return formatNumber(value, digits)
}

function formatOptionalMs(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return `${formatNumber(value)} ms`
}

function formatOptionalPercent(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return formatPercent(value)
}

function snapshotSection(snapshot: BrowserReplaySnapshot) {
  return {
    request_method: snapshot.request_method || 'n/a',
    request_url: snapshot.request_url || '',
    request_headers: snapshot.request_headers,
    request_body: snapshot.request_body,
    response_status: snapshot.response_status || 0,
    response_headers: snapshot.response_headers,
    response_body: snapshot.response_body,
  }
}

export function selectedGroupHistoryHasFields(data: MetricsHistoryPoint[], groupNames: string[]) {
  if (groupNames.length === 0) return false
  return groupNames.every(name => data.some(point => (
    typeof point[`${name}_vusers`] === 'number'
    || typeof point[`${name}_tps`] === 'number'
    || typeof point[`${name}_avgMs`] === 'number'
    || typeof point[`${name}_transactions`] === 'number'
    || typeof point[`${name}_errors`] === 'number'
  )))
}

export function selectedGroupLoadImpactData(data: MetricsHistoryPoint[], groups: CommandGroup[]) {
  if (groups.length === 0) return []
  const selectedNames = groups.map(group => group.name)
  if (!selectedGroupHistoryHasFields(data, selectedNames)) return data
  return data.map(point => {
    let hasGroupData = false
    let selectedVUsers = 0
    let selectedTps = 0
    let selectedTargetTps = 0
    let selectedTransactions = 0
    let selectedErrors = 0
    let tpsSamples = 0
    let weightedArt = 0
    let artWeight = 0
    const weightedPercentiles = { p50: 0, p90: 0, p95: 0, p99: 0 }
    const percentileWeights = { p50: 0, p90: 0, p95: 0, p99: 0 }

    groups.forEach(group => {
      const groupVusers = point[`${group.name}_vusers`]
      const groupTps = point[`${group.name}_tps`]
      const groupArt = point[`${group.name}_avgMs`]
      const groupTargetTps = point[`${group.name}_targetTps`]
      const groupTransactions = point[`${group.name}_transactions`]
      const groupErrors = point[`${group.name}_errors`]
      const groupP50 = point[`${group.name}_p50`]
      const groupP90 = point[`${group.name}_p90`]
      const groupP95 = point[`${group.name}_p95`]
      const groupP99 = point[`${group.name}_p99`]

      if (typeof groupVusers === 'number' && Number.isFinite(groupVusers)) {
        selectedVUsers += groupVusers
        hasGroupData = true
      }
      if (typeof groupTps === 'number' && Number.isFinite(groupTps)) {
        selectedTps += groupTps
        tpsSamples += 1
        hasGroupData = true
      }
      if (typeof groupTargetTps === 'number' && Number.isFinite(groupTargetTps)) {
        selectedTargetTps += groupTargetTps
        hasGroupData = true
      }
      if (typeof groupTransactions === 'number' && Number.isFinite(groupTransactions)) {
        selectedTransactions += groupTransactions
        hasGroupData = true
      }
      if (typeof groupErrors === 'number' && Number.isFinite(groupErrors)) {
        selectedErrors += groupErrors
        hasGroupData = true
      }
      if (typeof groupArt === 'number' && Number.isFinite(groupArt)) {
        const attempts = typeof groupTransactions === 'number' && Number.isFinite(groupTransactions) && typeof groupErrors === 'number' && Number.isFinite(groupErrors)
          ? groupTransactions + groupErrors
          : 0
        const weight = attempts > 0 ? attempts : typeof groupVusers === 'number' && Number.isFinite(groupVusers) && groupVusers > 0 ? groupVusers : 1
        weightedArt += groupArt * weight
        artWeight += weight
        hasGroupData = true
      }
      const attempts = typeof groupTransactions === 'number' && Number.isFinite(groupTransactions) && typeof groupErrors === 'number' && Number.isFinite(groupErrors)
        ? groupTransactions + groupErrors
        : 0
      if (attempts > 0) {
        // Weight each percentile only by the attempts of groups that actually
        // reported it. A group that omits a percentile (e.g. p95) must not
        // inflate that percentile's denominator, which would bias the aggregate
        // toward zero in proportion to the omitting group's traffic.
        if (typeof groupP50 === 'number' && Number.isFinite(groupP50)) {
          weightedPercentiles.p50 += groupP50 * attempts
          percentileWeights.p50 += attempts
          hasGroupData = true
        }
        if (typeof groupP90 === 'number' && Number.isFinite(groupP90)) {
          weightedPercentiles.p90 += groupP90 * attempts
          percentileWeights.p90 += attempts
          hasGroupData = true
        }
        if (typeof groupP95 === 'number' && Number.isFinite(groupP95)) {
          weightedPercentiles.p95 += groupP95 * attempts
          percentileWeights.p95 += attempts
          hasGroupData = true
        }
        if (typeof groupP99 === 'number' && Number.isFinite(groupP99)) {
          weightedPercentiles.p99 += groupP99 * attempts
          percentileWeights.p99 += attempts
          hasGroupData = true
        }
      }
    })

    if (!hasGroupData) {
      return {
        ...point,
        vusers: 0,
        tps: 0,
        targetTps: 0,
        avgMs: 0,
        errorPct: 0,
        p50: 0,
        p90: 0,
        p95: 0,
        p99: 0,
      }
    }
    const attempts = selectedTransactions + selectedErrors

    return {
      ...point,
      vusers: selectedVUsers > 0 ? selectedVUsers : point.vusers,
      tps: tpsSamples > 0 ? Math.round(selectedTps * 10) / 10 : point.tps,
      targetTps: selectedTargetTps > 0 ? Math.round(selectedTargetTps * 10) / 10 : point.targetTps,
      avgMs: artWeight > 0 ? Math.round((weightedArt / artWeight) * 10) / 10 : point.avgMs,
      errorPct: attempts > 0 ? Math.round((selectedErrors / attempts * 100) * 100) / 100 : point.errorPct,
      p50: percentileWeights.p50 > 0 ? Math.round((weightedPercentiles.p50 / percentileWeights.p50) * 10) / 10 : point.p50,
      p90: percentileWeights.p90 > 0 ? Math.round((weightedPercentiles.p90 / percentileWeights.p90) * 10) / 10 : point.p90,
      p95: percentileWeights.p95 > 0 ? Math.round((weightedPercentiles.p95 / percentileWeights.p95) * 10) / 10 : point.p95,
      p99: percentileWeights.p99 > 0 ? Math.round((weightedPercentiles.p99 / percentileWeights.p99) * 10) / 10 : point.p99,
    }
  })
}

interface ArchiveTransactionBucket {
  transactions: number
  errors: number
  totalDurationMs: number
  durations: number[]
  byGroup: Record<string, ArchiveTransactionBucketStats>
}

interface ArchiveTransactionBucketStats {
  transactions: number
  errors: number
  totalDurationMs: number
  durations: number[]
}

function emptyArchiveTransactionBucket(): ArchiveTransactionBucket {
  return {
    transactions: 0,
    errors: 0,
    totalDurationMs: 0,
    durations: [],
    byGroup: {},
  }
}

function emptyArchiveTransactionBucketStats(): ArchiveTransactionBucketStats {
  return {
    transactions: 0,
    errors: 0,
    totalDurationMs: 0,
    durations: [],
  }
}

function commandGroupConfiguredVusers(group: CommandGroup) {
  return Math.max(0, group.targetVusers || group.target || group.current || 0)
}

function estimatedGroupVusers(globalVusers: number, selectedGroups: CommandGroup[], allGroups: CommandGroup[]) {
  const totalConfigured = allGroups.reduce((sum, group) => sum + commandGroupConfiguredVusers(group), 0)
  const selectedConfigured = selectedGroups.reduce((sum, group) => sum + commandGroupConfiguredVusers(group), 0)
  if (globalVusers <= 0 || selectedConfigured <= 0 || totalConfigured <= 0) return 0
  return Math.max(0, Math.min(globalVusers, Math.round(globalVusers * selectedConfigured / totalConfigured)))
}

function archiveBucketSeconds(referenceData: MetricsHistoryPoint[], index: number) {
  const current = referenceData[index]?.timestampMs
  if (typeof current !== 'number') return 1
  if (index > 0 && typeof referenceData[index - 1]?.timestampMs === 'number') {
    return Math.max(1, (current - referenceData[index - 1]!.timestampMs!) / 1000)
  }
  if (referenceData.length > 1 && typeof referenceData[index + 1]?.timestampMs === 'number') {
    return Math.max(1, (referenceData[index + 1]!.timestampMs! - current) / 1000)
  }
  return 1
}

function archiveBucketIndex(referenceData: MetricsHistoryPoint[], timestampMs: number) {
  if (referenceData.length === 0 || !Number.isFinite(timestampMs)) return -1
  const index = referenceData.findIndex(point => typeof point.timestampMs === 'number' && timestampMs <= point.timestampMs)
  return index >= 0 ? index : referenceData.length - 1
}

function percentile(values: number[], ratio: number) {
  if (values.length === 0) return 0
  const sorted = [...values].sort((a, b) => a - b)
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * ratio) - 1))
  return sorted[index]
}

function roundedTenth(value: number) {
  return Math.round(value * 10) / 10
}

function roundedHundredth(value: number) {
  return Math.round(value * 100) / 100
}

function applyBucketStats(point: MetricsHistoryPoint, stats: ArchiveTransactionBucketStats, seconds: number) {
  const transactions = stats.transactions
  const errorPct = transactions > 0 ? roundedHundredth(stats.errors / transactions * 100) : 0
  point.tps = transactions > 0 ? roundedTenth(transactions / seconds) : 0
  point.avgMs = transactions > 0 ? roundedTenth(stats.totalDurationMs / transactions) : 0
  point.errorPct = errorPct
  point.p50 = transactions > 0 ? roundedTenth(percentile(stats.durations, 0.50)) : 0
  point.p90 = transactions > 0 ? roundedTenth(percentile(stats.durations, 0.90)) : 0
  point.p95 = transactions > 0 ? roundedTenth(percentile(stats.durations, 0.95)) : 0
  point.p99 = transactions > 0 ? roundedTenth(percentile(stats.durations, 0.99)) : 0
}

export function buildSelectedGroupChartHistoryFromTransactions(
  referenceData: MetricsHistoryPoint[],
  transactions: TransactionEvent[],
  selectedGroups: CommandGroup[],
  allGroups: CommandGroup[],
) {
  if (selectedGroups.length === 0) return []
  const usableReference = referenceData.filter(point => typeof point.timestampMs === 'number')
  if (usableReference.length === 0 || transactions.length === 0) return []
  const selectedGroupNames = new Set(selectedGroups.map(group => group.name))
  const buckets = usableReference.map(emptyArchiveTransactionBucket)

  transactions.forEach(transaction => {
    const groupName = transaction.group || ''
    if (!selectedGroupNames.has(groupName)) return
    const timestampMs = Date.parse(transaction.timestamp)
    const bucketIndex = archiveBucketIndex(usableReference, timestampMs)
    if (bucketIndex < 0) return
    const durationMs = Number(transaction.duration_ms)
    if (!Number.isFinite(durationMs)) return
    const bucket = buckets[bucketIndex]
    const groupStats = bucket.byGroup[groupName] ?? emptyArchiveTransactionBucketStats()
    bucket.byGroup[groupName] = groupStats
    const success = transaction.success !== false
    bucket.transactions += 1
    groupStats.transactions += 1
    bucket.totalDurationMs += durationMs
    groupStats.totalDurationMs += durationMs
    bucket.durations.push(durationMs)
    groupStats.durations.push(durationMs)
    if (!success) {
      bucket.errors += 1
      groupStats.errors += 1
    }
  })

  let cumulativeTransactions = 0
  let cumulativeErrors = 0
  return usableReference.map((referencePoint, index) => {
    const bucket = buckets[index]
    const seconds = archiveBucketSeconds(usableReference, index)
    cumulativeTransactions += bucket.transactions
    cumulativeErrors += bucket.errors
    const point: MetricsHistoryPoint = {
      ...referencePoint,
      vusers: estimatedGroupVusers(referencePoint.vusers, selectedGroups, allGroups),
    }
    applyBucketStats(point, bucket, seconds)
    point.totalTransactions = cumulativeTransactions
    point.totalErrors = cumulativeErrors
    selectedGroups.forEach(group => {
      const stats = bucket.byGroup[group.name] ?? emptyArchiveTransactionBucketStats()
      const groupPoint: MetricsHistoryPoint = {
        ...point,
        vusers: estimatedGroupVusers(referencePoint.vusers, [group], allGroups),
      }
      applyBucketStats(groupPoint, stats, seconds)
      point[`${group.name}_vusers`] = groupPoint.vusers
      point[`${group.name}_tps`] = groupPoint.tps
      point[`${group.name}_avgMs`] = groupPoint.avgMs
      point[`${group.name}_transactions`] = stats.transactions
      point[`${group.name}_errors`] = stats.errors
      point[`${group.name}_errorPct`] = groupPoint.errorPct
      point[`${group.name}_p50`] = groupPoint.p50
      point[`${group.name}_p90`] = groupPoint.p90
      point[`${group.name}_p95`] = groupPoint.p95
      point[`${group.name}_p99`] = groupPoint.p99
    })
    return point
  })
}

function chartPointIdentity(point: MetricsHistoryPoint) {
  return typeof point.timestampMs === 'number' ? `ts:${point.timestampMs}` : `time:${point.time}`
}

function alignScopedChartDataToReference(scopedData: MetricsHistoryPoint[], referenceData: MetricsHistoryPoint[]) {
  if (scopedData.length === 0 || referenceData.length === 0) return scopedData
  const referenceByIdentity = new Map(referenceData.map(point => [chartPointIdentity(point), point]))
  return scopedData
    .filter(point => referenceByIdentity.has(chartPointIdentity(point)))
    .map(point => {
      const reference = referenceByIdentity.get(chartPointIdentity(point))
      return reference
        ? { ...point, relativeMs: reference.relativeMs, time: reference.time }
        : point
    })
}

interface CommandRuntimeGraphsProps {
  variant: VariantId
  groups: CommandGroup[]
  metrics?: LiveMetrics
  chartData: MetricsHistoryPoint[]
  groupScopedChartData?: MetricsHistoryPoint[]
  groupScopedMetrics?: LiveMetrics
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
  activeGraphGroupTab?: GraphGroupTabId
  onActiveGraphGroupTabChange?: (tab: GraphGroupTabId) => void
  underLoadDisplayMode?: UnderLoadDisplayMode
  externalControlsTarget?: HTMLElement | null
}

function CommandRuntimeGraphs({
  variant,
  groups,
  metrics,
  chartData,
  groupScopedChartData,
  groupScopedMetrics,
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
  activeGraphGroupTab,
  onActiveGraphGroupTabChange,
  underLoadDisplayMode,
  externalControlsTarget = null,
}: CommandRuntimeGraphsProps) {
  const groupNames = groups.map(group => group.name)
  const groupColors = groups.map(group => group.color)
  const performanceChartData = groupScopedChartData ?? chartData
  const activeChartData = variant === 'grafana' && activeGraphGroupTab !== 'infrastructure' ? performanceChartData : chartData
  const activeMetrics = variant === 'grafana' && activeGraphGroupTab !== 'infrastructure' ? groupScopedMetrics ?? metrics : metrics
  const loadImpactSourceData = useMemo(
    () => variant === 'grafana' ? performanceChartData : chartData,
    [chartData, performanceChartData, variant],
  )
  const loadImpactVuserRange = useMemo(() => {
    const loads = loadImpactSourceData
      .map(point => point.vusers)
      .filter((value): value is number => typeof value === 'number' && Number.isFinite(value))
    if (loads.length === 0) return ''
    return `${Math.min(...loads)}-${Math.max(...loads)}`
  }, [loadImpactSourceData])
  return (
    <div className={`command-runtime-graphs command-runtime-graphs-${variant}`} data-visible-groups={groups.map(group => group.key).join(' ')} data-load-impact-vuser-range={loadImpactVuserRange}>
      <TestExecutionCharts
        key={variant}
        filteredData={activeChartData}
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
        loadImpactData={loadImpactSourceData}
        latestLatency={activeMetrics?.latency}
        latestMetrics={activeMetrics}
        surface="command-center"
        storageScope={`apexload.commandCenter.${variant}`}
        initialPresetId={variant === 'ops' ? 'run_operator' : 'performance_engineer'}
        defaultDensity="command_center"
        activeCursor={activeCursor}
        onActiveCursorChange={onActiveCursorChange}
        enableGraphGroupTabs={variant === 'grafana'}
        activeGraphGroupTab={activeGraphGroupTab}
        onActiveGraphGroupTabChange={onActiveGraphGroupTabChange}
        underLoadDisplayMode={underLoadDisplayMode}
        externalControlsTarget={externalControlsTarget}
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
  const targetMode = groups[0]?.targetMode ?? 'vusers'
  const targetValue = groups.reduce((sum, group) => sum + group.target, 0)
  return (
    <div className="command-hero-strip" aria-label="Top line live metrics">
      <HeroMetric label="TPS" value={formatNumber(totalTps, 1)} tone="accent" />
      <HeroMetric label="ART" value={`${formatNumber(avgArt)} ms`} tone="default" />
      <HeroMetric label="Error Rate" value={formatPercent(errorPct)} tone="danger" />
      <HeroMetric label="Active VUsers" value={formatNumber(activeVUsers)} tone="info" />
      <HeroMetric label={targetMode === 'tps' ? 'Target TPS' : 'Target VUsers'} value={formatNumber(targetValue, targetMode === 'tps' ? 1 : 0)} tone="success" />
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

interface LoadModelPoint {
  pct: number
  planned: number
  phase: string
}

function buildLoadModelProfile(totalTarget: number): LoadModelPoint[] {
  const target = Math.max(0, totalTarget)
  return [
    { pct: 0, planned: 0, phase: 'Start' },
    { pct: 12, planned: Math.round(target * 0.28), phase: 'Ramp' },
    { pct: 24, planned: Math.round(target * 0.58), phase: 'Ramp' },
    { pct: 40, planned: target, phase: 'At target' },
    { pct: 78, planned: target, phase: 'Hold' },
    { pct: 90, planned: Math.round(target * 0.72), phase: 'Ramp down' },
    { pct: 100, planned: 0, phase: 'End' },
  ]
}

function plannedLoadAtProgress(profile: LoadModelPoint[], progressPct: number) {
  if (profile.length === 0) return 0
  const progress = clampPercent(progressPct)
  const nextIndex = profile.findIndex(point => point.pct >= progress)
  if (nextIndex <= 0) return profile[0].planned
  const next = profile[nextIndex]
  const previous = profile[nextIndex - 1]
  if (!next) return profile[profile.length - 1].planned
  const span = Math.max(1, next.pct - previous.pct)
  const ratio = (progress - previous.pct) / span
  return Math.round(previous.planned + (next.planned - previous.planned) * ratio)
}

function loadPhaseAtProgress(profile: LoadModelPoint[], progressPct: number) {
  if (profile.length === 0) return 'Model'
  const progress = clampPercent(progressPct)
  const active = [...profile].reverse().find(point => point.pct <= progress)
  return active?.phase || profile[0].phase
}

function LoadModelPanel({
  groups,
  runStatus,
  totalDurationSeconds,
  remainingSeconds,
}: {
  groups: CommandGroup[]
  runStatus: TestStatus
  totalDurationSeconds: number
  remainingSeconds: number
}) {
  const totalTarget = groups.reduce((sum, group) => sum + (group.targetMode === 'vusers' ? group.target : group.suggestedVusers ?? group.targetVusers ?? group.current), 0)
  const totalCurrent = groups.reduce((sum, group) => sum + group.current, 0)
  const progressPct = loadProgressPercent(totalDurationSeconds, remainingSeconds)
  const profile = useMemo(() => buildLoadModelProfile(totalTarget), [totalTarget])
  const plannedNow = plannedLoadAtProgress(profile, progressPct)
  const delta = totalCurrent - plannedNow
  const phase = runStatus === 'PAUSED' ? 'Paused' : loadPhaseAtProgress(profile, progressPct)

  return (
    <section
      className="command-panel command-load-model-panel"
      aria-label="Load model profile"
      data-progress-pct={Math.round(progressPct)}
      data-planned-vusers={plannedNow}
      data-current-vusers={totalCurrent}
      data-target-vusers={totalTarget}
    >
      <div className="command-panel-header command-load-model-header">
        <div>
          <h3>Load Model</h3>
          <p>{phase} / {formatRemaining(remainingSeconds)} remaining</p>
        </div>
        <span className="command-load-model-badge">{formatNumber(progressPct, 0)}% elapsed</span>
      </div>

      <div className="command-load-model-stats">
        <MetricPill label="Planned VU" value={formatNumber(plannedNow)} tone="accent" />
        <MetricPill label="Actual VU" value={formatNumber(totalCurrent)} tone="info" />
        <MetricPill label="Delta VU" value={`${delta >= 0 ? '+' : ''}${formatNumber(delta)}`} tone={Math.abs(delta) > Math.max(60, totalTarget * 0.05) ? 'warning' : 'default'} />
      </div>

      <div className="command-load-model-chart" aria-hidden="true">
        <ResponsiveContainer width="100%" height={132} minWidth={240}>
          <AreaChart data={profile} margin={{ top: 8, right: 8, left: -18, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" />
            <XAxis
              dataKey="pct"
              type="number"
              domain={[0, 100]}
              ticks={[0, 25, 50, 75, 100]}
              tickFormatter={(value) => `${value}%`}
              stroke="var(--text-muted)"
              tick={{ fill: 'var(--text-muted)', fontSize: 10 }}
            />
            <YAxis
              stroke="var(--text-muted)"
              tick={{ fill: 'var(--text-muted)', fontSize: 10 }}
              tickFormatter={(value) => formatNumber(Number(value) / 1000, 1)}
            />
            <Tooltip
              contentStyle={chartTooltipContentStyle}
              labelStyle={chartTooltipLabelStyle}
              formatter={(value) => [`${formatNumber(Number(value))} VU`, 'Planned']}
              labelFormatter={(value) => `${formatNumber(Number(value))}% elapsed`}
            />
            <ReferenceLine x={progressPct} stroke="var(--warning)" strokeDasharray="4 4" ifOverflow="extendDomain" />
            <ReferenceLine y={totalCurrent} stroke="var(--chart-2)" strokeDasharray="3 3" ifOverflow="extendDomain" />
            <Area
              type="monotone"
              dataKey="planned"
              name="Planned VUsers"
              stroke="var(--accent)"
              fill="var(--accent-soft)"
              fillOpacity={0.45}
              strokeWidth={2}
              dot={false}
              activeDot={{ r: 4, stroke: 'var(--accent)', fill: 'var(--bg-card)' }}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>

      <div className="command-load-model-progress" aria-hidden="true">
        <span style={{ width: `${progressPct}%` }} />
      </div>
    </section>
  )
}

function GroupLoadTable({
  groups,
  compact,
  paired = false,
  readOnly = false,
  onTargetChange,
  onApplyTarget,
}: {
  groups: CommandGroup[]
  compact: boolean
  paired?: boolean
  readOnly?: boolean
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
}) {
  const targetMode = groups[0]?.targetMode ?? 'vusers'
  const targetHeader = targetMode === 'tps' ? 'Target TPS' : 'Target'
  return (
    <section className={`command-panel command-group-panel ${compact ? 'compact' : ''} ${paired ? 'paired' : ''}`} aria-label="Group load and health">
      <div className="command-panel-header">
        <div>
          <h3>Group Load</h3>
          <p>{targetMode === 'tps' ? 'Goal targets, current VUsers, suggested load, TPS, ART, and error rate by group.' : 'Current load, staged target, TPS, ART, and error rate by group.'}</p>
        </div>
      </div>
      <div className="command-group-table" role="table">
        <div className="command-group-row header" role="row">
          <span role="columnheader">Group</span>
          <span role="columnheader">Current VU</span>
          <span role="columnheader">{targetHeader}</span>
          <span role="columnheader">TPS</span>
          <span role="columnheader">ART</span>
          <span role="columnheader">Errors</span>
        </div>
        {groups.map(group => {
          const isGoalTarget = group.targetMode === 'tps'
          const sliderMax = isGoalTarget ? Math.max(100, Math.ceil(group.target * 2)) : 1800
          const guardrail = isGoalTarget && (group.minVusers !== undefined || group.maxVusers !== undefined)
            ? `guardrail ${group.minVusers ?? 0}-${group.maxVusers ?? 'unbounded'} VU`
            : ''
          const suggested = isGoalTarget && group.suggestedVusers !== undefined ? `suggested ${formatNumber(group.suggestedVusers)} VU` : ''
          const goalContext = [group.protocol, suggested, guardrail].filter(Boolean).join(' / ')
          const gapText = isGoalTarget && group.throughputGapPct !== undefined ? `gap ${formatPercent(group.throughputGapPct)}` : ''
          return (
            <div key={group.key} className={`command-group-row ${isGoalTarget ? 'goal-target' : ''}`} role="row">
              <span role="cell" className="command-group-name">
                <i style={{ background: group.color }} />
                <span>
                  <strong>{group.name}</strong>
                  <small>{goalContext || group.protocol}</small>
                </span>
              </span>
              <span role="cell">{formatNumber(group.current)}</span>
              <span role="cell" className="command-target-cell">
                {readOnly ? (
                  <span className="command-target-readout">
                    <strong>{formatTargetValue(group.target, group.targetMode)}</strong>
                    <small>read only</small>
                  </span>
                ) : (
                  <>
                    <input
                      type="number"
                      min={isGoalTarget ? 0.1 : 0}
                      max={isGoalTarget ? 100000 : 2400}
                      step={isGoalTarget ? 0.1 : 1}
                      aria-label={`${group.name} target ${isGoalTarget ? 'TPS' : 'VUsers'}`}
                      value={group.target}
                      onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
                    />
                    <input
                      type="range"
                      min={isGoalTarget ? 0.1 : 0}
                      max={sliderMax}
                      step={isGoalTarget ? 0.1 : 1}
                      aria-label={`${group.name} target ${isGoalTarget ? 'TPS' : 'VUser'} slider`}
                      value={group.target}
                      onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
                    />
                    <button type="button" className="btn btn-sm btn-accent" disabled={isGoalTarget && group.target <= 0} onClick={() => onApplyTarget(group.name, group.target)}>
                      Apply
                    </button>
                  </>
                )}
              </span>
              <span role="cell" className={group.throughputTargetMissed ? 'warning' : undefined}>
                <span className="command-tps-stack">
                  <strong>{formatNumber(group.tps, 1)}</strong>
                  {isGoalTarget && <small>target {formatNumber(group.targetTps ?? group.target, 1)} TPS{gapText ? ` / ${gapText}` : ''}</small>}
                </span>
              </span>
              <span role="cell">{formatNumber(group.art)} ms</span>
              <span role="cell" className={metricTone(group.errorPct, 0.75, 1.5)}>{formatPercent(group.errorPct)}</span>
            </div>
          )
        })}
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
