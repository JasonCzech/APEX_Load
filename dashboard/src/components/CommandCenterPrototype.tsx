import { useCallback, useEffect, useMemo, useState } from 'react'
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
import type { BrowserReplaySnapshot, GroupLiveMetrics, LiveErrorEvidence, LiveMetrics, MetricsHistoryPoint, TestGroup, TestStatus, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import TestExecutionCharts, { type GraphCursorState } from './TestExecutionCharts'
import type { ExecutionChartTimeRange } from './testExecutionChartZoomModel'
import { mapExecutionMetricToHistoryPoint } from './testExecutionMetricsModel'
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
  { value: 'ops', label: 'OPS View' },
  { value: 'grafana', label: 'Graph Wall' },
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
  const [graphWallGroupVisibility, setGraphWallGroupVisibility] = useState<Record<string, boolean>>({})
  const [activeTableTab, setActiveTableTab] = useState<CommandTableTab>('scenario')
  const [selectedEvidenceId, setSelectedEvidenceId] = useState('')
  const [liveEvidenceBySignature, setLiveEvidenceBySignature] = useState<Record<string, LiveErrorEvidence[]>>({})
  const [evidenceLoadingSignature, setEvidenceLoadingSignature] = useState('')
  const [evidenceError, setEvidenceError] = useState('')
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
  const graphWallGroups = useMemo(
    () => groups.filter(group => graphWallGroupVisibility[group.key] !== false),
    [graphWallGroupVisibility, groups],
  )
  const errorPatterns = useMemo(
    () => liveMode ? buildLiveErrorPatterns(displayTest.live_metrics) : hasLiveRoute ? [] : ERROR_PATTERNS,
    [displayTest.live_metrics, hasLiveRoute, liveMode],
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
  const totalDurationSeconds = parseDurationToSeconds(displayTest.duration)
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
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onErrorSelect: (errorId: string) => void
}

interface GrafanaWallViewProps extends CommonViewProps {
  chartGroups: CommandGroup[]
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
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
}: OpsCockpitViewProps) {
  return (
    <section className="command-layout command-layout-ops" aria-label="Ops cockpit mockup">
      <div className="command-primary-column">
        <HeroMetricStrip groups={groups} metrics={metrics} />
        <div className="command-load-model-row">
          <GroupLoadTable groups={groups} onTargetChange={onTargetChange} onApplyTarget={onApplyTarget} compact={false} paired />
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
  const bindGraphOptionsTarget = useCallback((node: HTMLDivElement | null) => {
    setGraphOptionsTarget(node)
  }, [])

  return (
    <section className="command-layout command-layout-grafana" aria-label="Grafana wall mockup">
      <div className="command-grafana-grid">
        <CommandRuntimeGraphs
          variant="grafana"
          groups={chartGroups}
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
          externalControlsTarget={graphOptionsTarget}
        />
        <div className="command-grafana-side">
          <CommandViewSwitchPanel variant={variant} onVariantChange={onVariantChange} />
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
              aria-pressed={visible}
              aria-label={`${group.name} graph visibility`}
              onClick={() => onToggle(group.key)}
            >
              <span className="command-group-select-swatch" style={{ background: group.color }} />
              <strong>{group.name}</strong>
              <small>{visible ? 'Visible' : 'Hidden'}</small>
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
    const targetTps = groupMetrics?.target_tps ?? group.tps * 1.12
    return {
      id: `group-${group.key}`,
      type: 'Group',
      name: group.name,
      group: group.name,
      protocol: group.protocol,
      currentVusers: groupMetrics?.vusers ?? group.current,
      targetVusers: group.target,
      tps: groupMetrics?.tps ?? group.tps,
      targetTps,
      throughputGap: groupMetrics?.throughput_gap_pct ?? calculateThroughputGap(groupMetrics?.tps ?? group.tps, targetTps),
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

function selectedGroupLoadImpactData(data: MetricsHistoryPoint[], groups: CommandGroup[]) {
  if (groups.length === 0) return data
  return data.map(point => {
    let hasGroupData = false
    let selectedVUsers = 0
    let selectedTps = 0
    let tpsSamples = 0
    let weightedArt = 0
    let artWeight = 0

    groups.forEach(group => {
      const groupVusers = point[`${group.name}_vusers`]
      const groupTps = point[`${group.name}_tps`]
      const groupArt = point[`${group.name}_avgMs`]

      if (typeof groupVusers === 'number' && Number.isFinite(groupVusers)) {
        selectedVUsers += groupVusers
        hasGroupData = true
      }
      if (typeof groupTps === 'number' && Number.isFinite(groupTps)) {
        selectedTps += groupTps
        tpsSamples += 1
        hasGroupData = true
      }
      if (typeof groupArt === 'number' && Number.isFinite(groupArt)) {
        const weight = typeof groupVusers === 'number' && Number.isFinite(groupVusers) && groupVusers > 0 ? groupVusers : 1
        weightedArt += groupArt * weight
        artWeight += weight
        hasGroupData = true
      }
    })

    if (!hasGroupData) return point

    return {
      ...point,
      vusers: selectedVUsers > 0 ? selectedVUsers : point.vusers,
      tps: tpsSamples > 0 ? Math.round(selectedTps * 10) / 10 : point.tps,
      avgMs: artWeight > 0 ? Math.round((weightedArt / artWeight) * 10) / 10 : point.avgMs,
    }
  })
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
  externalControlsTarget?: HTMLElement | null
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
  externalControlsTarget = null,
}: CommandRuntimeGraphsProps) {
  const groupNames = groups.map(group => group.name)
  const groupColors = groups.map(group => group.color)
  const loadImpactSourceData = useMemo(
    () => variant === 'grafana' ? selectedGroupLoadImpactData(chartData, groups) : chartData,
    [chartData, groups, variant],
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
        loadImpactData={loadImpactSourceData}
        latestLatency={metrics?.latency}
        latestMetrics={metrics}
        surface="command-center"
        storageScope={`apexload.commandCenter.${variant}`}
        initialPresetId={variant === 'ops' ? 'run_operator' : 'performance_engineer'}
        defaultDensity="command_center"
        activeCursor={activeCursor}
        onActiveCursorChange={onActiveCursorChange}
        enableGraphGroupTabs={variant === 'grafana'}
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
  const totalTarget = groups.reduce((sum, group) => sum + group.target, 0)
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
  onTargetChange,
  onApplyTarget,
}: {
  groups: CommandGroup[]
  compact: boolean
  paired?: boolean
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
}) {
  return (
    <section className={`command-panel command-group-panel ${compact ? 'compact' : ''} ${paired ? 'paired' : ''}`} aria-label="Group load and health">
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
