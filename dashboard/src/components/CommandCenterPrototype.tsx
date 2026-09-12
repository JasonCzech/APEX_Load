import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { createContext, useContext, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
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
import { useTestApi } from '../hooks/api/useTestApi'
import { isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import { isCompletedTestStatus, isLiveTestStatus, isPendingTestStatus, resolveTestStatus } from '../utils/testStatusUtils'
import { useRunActionConfirmation } from './useRunActionConfirmation'
import type { BrowserReplaySnapshot, GroupLiveMetrics, ImportedDashboardResult, IntervalSummaryRecord, LiveErrorEvidence, LiveMetrics, MetricsHistoryPoint, PrometheusGraph, TestGroup, TestGroupParametersUpdateResult, TestGroupRuntimeSettingsUpdateResult, TestStatus, TestSummary, TransactionEvent, TxMetrics, VUserRampStatus } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import TestExecutionCharts, { type GraphCursorState, type GraphGroupTabId, type UnderLoadDisplayMode } from './TestExecutionCharts'
import type { ExecutionChartTimeRange, ExecutionChartZoomAnchor } from './testExecutionChartZoomModel'
import { mapArchiveSummarySamples, mapExecutionHistory, mapExecutionMetricToHistoryPoint, recentMetricAverages } from './testExecutionMetricsModel'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'
import { useExecutionChartModel } from './useExecutionChartModel'
import { useExecutionMetricsStream } from './useExecutionMetricsStream'
import { usePrometheusGraphs } from './usePrometheusGraphs'
import { useImportedDashboardGraphs } from './useImportedDashboardGraphs'
import { EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS, EXECUTION_POLL_INTERVAL_MS } from './useExecutionTestPolling'
import { ARCHIVE_CHART_MAX_SAMPLES } from './archiveHistorySampling'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'
import { omitTransactionChartSeries } from './transactionChartSeries'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import ConfirmRunActionModal from './ConfirmRunActionModal'
import TestNotesDialog from './TestNotesDialog'
import GroupParametersDialog from './GroupParametersDialog'
import GroupRuntimeSettingsDialog from './GroupRuntimeSettingsDialog'
import RuntimeAdjustmentDialog from './RuntimeAdjustmentDialog'
import { runtimeAdjustmentTargets } from './runtimeAdjustmentModel'
import ThresholdAlertStrip from './ThresholdAlertStrip'
import type { ThresholdAlertEntry } from '../thresholdAlerts'
import { liveEvidenceCacheKey } from './commandCenterEvidenceModel'
import { listAllCommandCenterActiveRuns } from './commandCenterRunDiscovery'
import { clampVUsersForRole, maxVUsersForRole } from './observerGroupModel'
import {
  buildScenarioRunRows,
  type CommandGroup,
  type ScenarioRunRow,
} from './commandCenterScenarioRows'
import { buildLoadModelProfile, type LoadModelPoint } from './commandCenterLoadModel'
import type { CommandCenterPrototypeFixtures } from './commandCenterPrototypeFixtures'
import {
  formatSnapshotRequestLineAndHeaders,
  formatSnapshotResponseStatusAndHeaders,
} from '../utils/snapshotFormatUtils'
import './TestExecution.css'
import './CommandCenterPrototype.css'

type VariantId = 'ops' | 'grafana'
type CommandTableTab = 'scenario' | 'errors' | 'infrastructure'
type SortDirection = 'asc' | 'desc'
type TargetTransitionMode = 'immediate' | 'gradual'
export type GroupSettingsDialogKind = 'runtime-settings' | 'parameters'

export function historicalGroupSummaryQuery(groups: string[]) {
  return {
    sample_limit: ARCHIVE_CHART_MAX_SAMPLES,
    groups,
  }
}

interface CommandCenterPrototypeProps {
  testId?: string
  history?: MetricsHistoryPoint[]
  statusOverride?: string
  initialTest?: TestSummary
  applicationId?: string
  projectId?: string
  // Live global SLA threshold breaches for this run (Settings → Thresholds),
  // rendered as a compact strip above the cockpit views.
  thresholdAlerts?: ThresholdAlertEntry[]
  onAcknowledgeThresholdAlert?: (key: string) => void
  onOpenExecution?: (testId: string) => void
  onOpenArchive?: (testId: string) => void
  onOpenResults?: () => void
  prototypeMode?: boolean
  active?: boolean
}

export interface ErrorPattern {
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
  targetTransitionMode: TargetTransitionMode
  targetRampDuration: string
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

function isCommandCenterDiscoverableStatus(status?: string): boolean {
  return isLiveTestStatus(status) || isPendingTestStatus(status) || status === 'SHAKEOUT' || status === 'SHAKEOUT_REVIEW' || status === 'PAUSED'
}

const VARIANT_OPTIONS: Array<{ value: VariantId; label: string }> = [
  { value: 'ops', label: 'OPS View' },
  { value: 'grafana', label: 'Graph Wall' },
]

const UNDER_LOAD_DISPLAY_OPTIONS: Array<{ value: UnderLoadDisplayMode; label: string }> = [
  { value: 'graphs', label: 'Graphs' },
  { value: 'heatmap', label: 'Heatmap' },
]

const COMMAND_GROUP_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--chart-6)',
] as const

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

function clampTarget(value: number, role?: TestGroup['role']) {
  return clampVUsersForRole(value, role)
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
  if (!group) return 0
  const attempts = group.transactions + group.errors
  return attempts > 0 ? group.errors / attempts * 100 : 0
}

function wholeTestErrorPercent(metrics: LiveMetrics | undefined, groups: CommandGroup[]) {
  const totalTransactions = metrics?.total_transactions ?? groups.reduce((sum, group) => sum + group.transactions, 0)
  const totalErrors = metrics?.total_errors ?? groups.reduce((sum, group) => sum + group.errors, 0)
  const attempts = totalTransactions + totalErrors
  return attempts > 0 ? totalErrors / attempts * 100 : 0
}

function highestGroupErrorRate(groups: CommandGroup[]) {
  if (groups.length <= 1) return undefined
  const highest = groups.reduce((current, group) => {
    if (group.errors <= 0) return current
    const attempts = group.transactions + group.errors
    const errorPct = group.errors / attempts * 100
    if (current && errorPct === current.errorPct) return { ...current, names: [...current.names, group.name] }
    return !current || errorPct > current.errorPct
      ? { names: [group.name], errorPct }
      : current
  }, undefined as { names: string[]; errorPct: number } | undefined)
  return highest ? {
    name: `${highest.names.join(', ')}${highest.names.length > 1 ? ' (tied)' : ''}`,
    errorPct: highest.errorPct,
  } : undefined
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
    const activeVuserRamp = test.vuser_ramps?.[group.name]
    const liveTargetVusers = activeVuserRamp?.target_vusers ?? group.vusers
    const liveTarget = targetMode === 'tps' ? goalTargets[group.name] : liveTargetVusers
    const target = targetDrafts[group.name] ?? liveTarget
    const current = metrics?.vusers ?? aggregateFallback?.active_vusers ?? 0
    const targetTps = targetMode === 'tps' ? target : metrics?.target_tps ?? aggregateFallback?.target_tps
    return {
      key: group.name,
      name: group.name,
      protocol: group.protocol || 'unknown',
      role: group.role,
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
      activeVuserRamp,
      tps: metrics?.tps ?? aggregateFallback?.tps ?? 0,
      art: metrics?.avg_response_ms ?? aggregateFallback?.avg_response_ms ?? 0,
      errorPct: metrics ? groupErrorPercent(metrics) : aggregateFallback?.error_pct ?? 0,
      p99: metrics?.p99_ms ?? aggregateFallback?.p99_ms ?? 0,
      transactions: metrics?.transactions ?? aggregateFallback?.total_transactions ?? 0,
      errors: metrics?.errors ?? aggregateFallback?.total_errors ?? 0,
      color: COMMAND_GROUP_COLORS[index % COMMAND_GROUP_COLORS.length],
    }
  })
}

function buildLiveErrorPatterns(metrics?: LiveMetrics): ErrorPattern[] {
  return (metrics?.error_patterns || []).map((pattern, index) => {
    const rate = pattern.error_pct ?? (metrics?.total_transactions ? pattern.count / metrics.total_transactions * 100 : 0)
    const group = pattern.affected_groups?.join(', ') || 'Multiple groups'
    // Fall back to the pattern's identifying evidence when the backend supplied
    // no signature, and to the index when there is no evidence either. The
    // joined string is never empty, so it can't chain into a `||` fallback.
    const evidenceParts = [
      pattern.snapshot_url || '',
      pattern.status_code == null ? '' : String(pattern.status_code),
      pattern.first_seen || '',
      pattern.actions?.[0] || '',
    ]
    const signatureId = pattern.signature
      || (evidenceParts.some(Boolean) ? evidenceParts.join('\u001f') : `pattern-${index}`)
    return {
      id: `live-${signatureId}`,
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

function buildGroupsWithTargets(prototypeGroups: CommandGroup[], targets: Record<string, number>) {
  return prototypeGroups.map(group => {
    const target = targets[group.name] ?? group.target
    return { ...group, target, targetVusers: target }
  })
}

export default function CommandCenterPrototype({
  testId: testIdProp,
  history = [],
  statusOverride,
  initialTest,
  applicationId,
  projectId,
  thresholdAlerts = [],
  onAcknowledgeThresholdAlert,
  onOpenExecution,
  onOpenArchive,
  onOpenResults,
  prototypeMode = false,
  active = true,
}: CommandCenterPrototypeProps = {}) {
  const [experienceVersion, setExperienceVersion] = useState<'original' | 'v2'>('original')
  const api = useTestApi()
  const pageRef = useRef<HTMLDivElement>(null)
  const wasActive = useRef(active)
  useEffect(() => {
    if (active && !wasActive.current) pageRef.current?.querySelector<HTMLButtonElement>('[data-command-details]')?.focus()
    wasActive.current = active
  }, [active])
  // When the Command Center is opened without an explicit run on the route
  // (e.g. via the sidebar nav rather than from a run chip), we discover the
  // live runs in scope and pick one. `testId` below is the effective run the
  // view renders: the routed run if present, otherwise the auto-selected one.
  const discoveryScopeKey = projectId ? `${projectId}\u001f${applicationId || ''}` : ''
  const [autoSelection, setAutoSelection] = useState<{ scopeKey: string; testId: string } | null>(null)
  const autoSelectedTestId = autoSelection?.scopeKey === discoveryScopeKey ? autoSelection.testId : null
  const setAutoSelectedTestId = useCallback((nextTestID: string | null) => {
    setAutoSelection(nextTestID && discoveryScopeKey ? { scopeKey: discoveryScopeKey, testId: nextTestID } : null)
  }, [discoveryScopeKey])
  const [runDiscoveryState, setRunDiscoveryState] = useState<{
    scopeKey: string
    runs: TestSummary[]
    discovering: boolean
  }>({ scopeKey: '', runs: [], discovering: false })
  const liveRunOptions = runDiscoveryState.scopeKey === discoveryScopeKey ? runDiscoveryState.runs : []
  const discoveringRuns = runDiscoveryState.scopeKey === discoveryScopeKey && runDiscoveryState.discovering
  const discoveryGenerationRef = useRef(0)
  const testId = testIdProp || autoSelectedTestId || undefined
  useEffect(() => {
    const page = pageRef.current
    const scroller = page?.closest<HTMLElement>('.main-content')
    const header = scroller?.querySelector<HTMLElement>('.main-topbar')
    if (!page || !header || typeof ResizeObserver === 'undefined') return
    const shelf = page.querySelector<HTMLElement>('.command-control-shelf')
    const update = () => {
      page.style.setProperty('--command-header-offset', `${header.offsetHeight}px`)
      page.style.setProperty('--command-shelf-height', `${page.querySelector<HTMLElement>('.command-control-shelf')?.offsetHeight || 0}px`)
      page.style.setProperty('--command-scroll-inset', scroller ? getComputedStyle(scroller).paddingTop : '0px')
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(header)
    observer.observe(page)
    if (shelf) observer.observe(shelf)
    return () => observer.disconnect()
  }, [active, testId])
  const viewMemory = useMemo(() => ({ testId, values: new Map<string, unknown>() }), [testId])
  const [variant, setVariant] = useState<VariantId>('ops')
  const [liveTest, setLiveTest] = useState<TestSummary | null>(() => initialTest || null)
  const [loadError, setLoadError] = useState('')
  const [expandedCharts, setExpandedCharts] = useState<Record<VariantId, string | null>>({ ops: null, grafana: null })
  const [activeGraphCursor, setActiveGraphCursor] = useState<GraphCursorState | null>(null)
  const [graphWallGroupVisibility, setGraphWallGroupVisibility] = useState<Record<string, boolean>>({})
  const [activeTableTab, setActiveTableTab] = useState<CommandTableTab>('scenario')
  const [selectedEvidenceId, setSelectedEvidenceId] = useState('')
  const [liveEvidenceByRunSignature, setLiveEvidenceByRunSignature] = useState<Record<string, LiveErrorEvidence[]>>({})
  const [evidenceLoadingKey, setEvidenceLoadingKey] = useState('')
  const [evidenceError, setEvidenceError] = useState('')
  const [archiveFinalizationTimedOut, setArchiveFinalizationTimedOut] = useState(false)
  const [archiveCheckPending, setArchiveCheckPending] = useState(false)
  const [archiveCheckError, setArchiveCheckError] = useState('')
  const [backendScopedChartData, setBackendScopedChartData] = useState<MetricsHistoryPoint[] | null>(null)
  const [backendScopedMetrics, setBackendScopedMetrics] = useState<LiveMetrics | undefined>()
  const [showNotes, setShowNotes] = useState(false)
  const [showRuntimeAdjustment, setShowRuntimeAdjustment] = useState(false)
  const { pendingAction, confirmRunAction, onConfirmRunAction, onCancelRunAction } = useRunActionConfirmation()
  const [groupSettingsDialog, setGroupSettingsDialog] = useState<{ group: string; kind: GroupSettingsDialogKind } | null>(null)
  const archiveCheckGenerationRef = useRef(0)
  const archiveCheckInFlightRef = useRef(false)
  const activeArchiveCheckTestIdRef = useRef(testId)
  activeArchiveCheckTestIdRef.current = testId

  // An open group dialog is bound to the run it was opened for; a run switch
  // (explicit or via auto-discovery) must not silently repoint it, and a stale
  // entry must not reopen against the next discovered run.
  useEffect(() => {
    archiveCheckGenerationRef.current += 1
    archiveCheckInFlightRef.current = false
    setGroupSettingsDialog(null)
    setShowRuntimeAdjustment(false)
    setLiveEvidenceByRunSignature({})
    setSelectedEvidenceId('')
    setEvidenceLoadingKey('')
    setEvidenceError('')
    setArchiveFinalizationTimedOut(false)
    setArchiveCheckPending(false)
    setArchiveCheckError('')
  }, [testId])
  const syncedTargetsRef = useRef<Record<string, number>>({})
  const syncedTargetTestIdRef = useRef<string | null>(null)
  const [state, setState] = useState<CommandCenterState>(() => ({
    runStatus: 'RUNNING',
    // Staged targets start empty for every mode: live runs seed them from the
    // run itself below, and the prototype falls back to each demo group's own
    // target until an operator stages a different one.
    vuserTargets: {},
    targetTransitionMode: 'immediate',
    targetRampDuration: '2m',
    // The first available error pattern is adopted by the selection effect
    // further down once the pattern list for this mode is known.
    selectedErrorId: '',
    feedback: DEFAULT_PROTOTYPE_FEEDBACK,
  }))

  // The demo fixtures are ~540 lines that only `prototypeMode` can ever reach,
  // so they live in their own module and are fetched on demand. A production
  // Command Center session never sets prototypeMode and therefore never
  // downloads that chunk.
  const [prototypeFixtures, setPrototypeFixtures] = useState<CommandCenterPrototypeFixtures | null>(null)
  const [prototypeFixturesError, setPrototypeFixturesError] = useState('')
  useEffect(() => {
    if (!prototypeMode) return undefined
    let cancelled = false
    setPrototypeFixturesError('')
    // A failed chunk fetch (evicted hashed asset after a redeploy, offline
    // reload, a webview that blocks the request) must not reject unhandled and
    // leave the cockpit rendering as an indistinguishable empty run.
    void import('./commandCenterPrototypeFixtures').then(module => {
      if (!cancelled) setPrototypeFixtures(module.commandCenterPrototypeFixtures)
    }).catch((error: unknown) => {
      if (cancelled) return
      setPrototypeFixtures(null)
      setPrototypeFixturesError(
        `Command center demo fixtures failed to load: ${error instanceof Error ? error.message : String(error)}`,
      )
    })
    return () => {
      cancelled = true
    }
  }, [prototypeMode])

  useEffect(() => {
    if (!testId) {
      setLiveTest(null)
      setLoadError('')
      setArchiveFinalizationTimedOut(false)
      return undefined
    }
    if (initialTest && initialTest.id === testId) {
      setLiveTest(initialTest)
      setLoadError('')
      return undefined
    }

    let cancelled = false
    let inFlight = false
    let stopped = false
    let lastSignature = ''
    let archiveFinalizationStartedAt: number | null = null
    setArchiveFinalizationTimedOut(false)
    setArchiveCheckError('')
    const interval = window.setInterval(() => {
      void load()
    }, EXECUTION_POLL_INTERVAL_MS)
    const load = async () => {
      if (stopped || inFlight) return
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      inFlight = true
      try {
        const next = await api.getTest(testId)
        if (!cancelled) {
          const signature = JSON.stringify(next)
          if (signature !== lastSignature) {
            lastSignature = signature
            setLiveTest(next)
          }
          setLoadError('')
          if (isCompletedTestStatus(next.status)) {
            if (!testIdProp && !initialTest) {
              stopped = true
              if (interval !== undefined) window.clearInterval(interval)
              setAutoSelection(current => current?.testId === testId ? null : current)
              setLiveTest(current => current?.id === testId ? null : current)
            } else if (next.archive) {
              setArchiveFinalizationTimedOut(false)
              stopped = true
              if (interval !== undefined) window.clearInterval(interval)
            } else {
              archiveFinalizationStartedAt ??= Date.now()
              if (Date.now() - archiveFinalizationStartedAt >= EXECUTION_ARCHIVE_FINALIZATION_TIMEOUT_MS) {
                setArchiveFinalizationTimedOut(true)
                stopped = true
                if (interval !== undefined) window.clearInterval(interval)
              }
            }
          } else {
            setArchiveFinalizationTimedOut(false)
            archiveFinalizationStartedAt = null
          }
        }
      } catch (error: unknown) {
        if (!cancelled) {
          setLoadError(getErrorMessage(error, 'Unable to load command-center run data'))
        }
      } finally {
        inFlight = false
      }
    }

    void load()
    const handleVisibilityChange = () => {
      if (!stopped && typeof document !== 'undefined' && document.visibilityState === 'visible') {
        void load()
      }
    }
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }
    return () => {
      cancelled = true
      stopped = true
      if (interval !== undefined) window.clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api, initialTest, testId, testIdProp])

  const checkCommandCenterArchiveAgain = useCallback(async () => {
    if (!testId || archiveCheckInFlightRef.current) return

    const requestedTestId = testId
    const requestGeneration = archiveCheckGenerationRef.current
    archiveCheckInFlightRef.current = true
    setArchiveCheckPending(true)
    setArchiveCheckError('')
    try {
      const next = await api.getTest(requestedTestId)
      if (
        archiveCheckGenerationRef.current !== requestGeneration
        || activeArchiveCheckTestIdRef.current !== requestedTestId
      ) return

      setLiveTest(next)
      setLoadError('')
      setArchiveFinalizationTimedOut(isCompletedTestStatus(next.status) && !next.archive)
    } catch (error: unknown) {
      if (
        archiveCheckGenerationRef.current === requestGeneration
        && activeArchiveCheckTestIdRef.current === requestedTestId
      ) {
        setArchiveCheckError(getErrorMessage(error, 'Unable to check archive publication'))
      }
    } finally {
      if (
        archiveCheckGenerationRef.current === requestGeneration
        && activeArchiveCheckTestIdRef.current === requestedTestId
      ) {
        archiveCheckInFlightRef.current = false
        setArchiveCheckPending(false)
      }
    }
  }, [api, testId])

  // Auto-discover live runs when the view is opened without a selected run.
  // A single live run is selected automatically; multiple live runs surface a
  // picker (see CommandCenterEmptyState). Skipped for the local prototype and
  // once a run is routed/selected. Polls so a run that starts while the empty
  // state is open gets picked up.
  useEffect(() => {
    const requestGeneration = ++discoveryGenerationRef.current
    if (prototypeMode || testIdProp || initialTest || autoSelectedTestId || !discoveryScopeKey) {
      setRunDiscoveryState({ scopeKey: discoveryScopeKey, runs: [], discovering: false })
      return undefined
    }
    let cancelled = false
    let inFlight = false
    const discover = async () => {
      if (inFlight) return
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') {
        return
      }
      inFlight = true
      setRunDiscoveryState(current => ({
        scopeKey: discoveryScopeKey,
        runs: current.scopeKey === discoveryScopeKey ? current.runs : [],
        discovering: true,
      }))
      try {
        const discoveredRuns = await listAllCommandCenterActiveRuns(api.listTests, {
          application_id: applicationId,
          project_id: projectId,
        })
        if (cancelled || discoveryGenerationRef.current !== requestGeneration) return
        const liveRuns = discoveredRuns.filter(test => isCommandCenterDiscoverableStatus(test.status))
        if (liveRuns.length === 1) {
          setAutoSelectedTestId(liveRuns[0].id)
          setRunDiscoveryState({ scopeKey: discoveryScopeKey, runs: [], discovering: false })
        } else {
          setRunDiscoveryState({ scopeKey: discoveryScopeKey, runs: liveRuns, discovering: false })
        }
      } catch {
        if (!cancelled && discoveryGenerationRef.current === requestGeneration) {
          setRunDiscoveryState({ scopeKey: discoveryScopeKey, runs: [], discovering: false })
        }
      } finally {
        inFlight = false
        if (!cancelled && discoveryGenerationRef.current === requestGeneration) {
          setRunDiscoveryState(current => current.scopeKey === discoveryScopeKey
            ? { ...current, discovering: false }
            : current)
        }
      }
    }
    void discover()
    const interval = window.setInterval(() => { void discover() }, 5000)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [api, applicationId, autoSelectedTestId, discoveryScopeKey, initialTest, projectId, prototypeMode, setAutoSelectedTestId, testIdProp])

  const activeLiveTest = liveTest?.id === testId ? liveTest : null
  // resolveTestStatus keeps the socket override from rolling the run backward.
  // Stop/Abort on a PAUSED run is known here before its broadcast arrives, and
  // letting a stale PAUSED override mask it left the shelf showing Resume/Stop
  // as though the click had done nothing.
  const streamStatus = activeLiveTest
    ? coerceTestStatus(
      isHistoricalRunStatus(activeLiveTest.status)
        ? activeLiveTest.status
        : resolveTestStatus(activeLiveTest, statusOverride ? { [activeLiveTest.id]: statusOverride } : {}),
      activeLiveTest.status,
    )
    : undefined
  const { localMetricsHistory, latestMetrics } = useExecutionMetricsStream(testId || '', streamStatus)
  const liveMetrics = latestMetrics || activeLiveTest?.live_metrics
  const hasLiveRoute = Boolean(testId)
  const liveMode = Boolean(testId && activeLiveTest)
  const emptyMode = !prototypeMode && !hasLiveRoute && !activeLiveTest
  const loadingTest = useMemo<TestSummary>(() => ({
    ...EMPTY_COMMAND_CENTER_TEST,
    id: testId || '',
    ref_key: testId || '',
    name: 'Loading command-center run...',
    status: 'INITIALIZING',
    groups: [],
    total_vusers: 0,
    live_metrics: undefined,
  }), [testId])
  const displayTest = useMemo<TestSummary>(() => activeLiveTest
    ? {
        ...activeLiveTest,
        status: streamStatus || activeLiveTest.status,
        live_metrics: liveMetrics || activeLiveTest.live_metrics,
      }
    : hasLiveRoute
      ? loadingTest
      : emptyMode
        ? EMPTY_COMMAND_CENTER_TEST
        : prototypeMode
          ? prototypeFixtures?.test ?? EMPTY_COMMAND_CENTER_TEST
          : EMPTY_COMMAND_CENTER_TEST,
  [activeLiveTest, emptyMode, hasLiveRoute, liveMetrics, loadingTest, prototypeFixtures, prototypeMode, streamStatus])
  const scopedThresholdAlerts = useMemo(
    () => {
      const activeTestId = displayTest.id || testId
      if (!activeTestId) return []
      return thresholdAlerts.filter(alert => alert.testId === activeTestId)
    },
    [displayTest.id, testId, thresholdAlerts],
  )

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
  }, [displayTest, liveMode])

  const runStatus = liveMode || hasLiveRoute || emptyMode ? displayTest.status : state.runStatus
  const groups = useMemo(
    () => liveMode ? buildLiveGroups(displayTest, state.vuserTargets) : hasLiveRoute || emptyMode ? [] : buildGroupsWithTargets(prototypeFixtures?.groups ?? [], state.vuserTargets),
    [displayTest, emptyMode, hasLiveRoute, liveMode, prototypeFixtures, state.vuserTargets],
  )
  const graphWallGroups = useMemo(
    () => groups.filter(group => graphWallGroupVisibility[group.key] !== false),
    [graphWallGroupVisibility, groups],
  )
  const graphWallGroupNames = useMemo(() => graphWallGroups.map(group => group.name), [graphWallGroups])
  const graphWallGroupSignature = graphWallGroupNames.join('\u001f')
  const errorPatterns = useMemo(
    () => liveMode ? buildLiveErrorPatterns(displayTest.live_metrics) : hasLiveRoute || emptyMode ? [] : prototypeFixtures?.errorPatterns ?? [],
    [displayTest.live_metrics, emptyMode, hasLiveRoute, liveMode, prototypeFixtures],
  )
  const selectedError = errorPatterns.find(error => error.id === state.selectedErrorId) || errorPatterns[0]
  useEffect(() => {
    if (!selectedError || selectedError.id === state.selectedErrorId) return
    setState(previous => ({ ...previous, selectedErrorId: selectedError.id }))
  }, [selectedError, state.selectedErrorId])
  const selectedEvidenceCacheKey = liveEvidenceCacheKey(testId || '', selectedError?.signature || '')
  const selectedEvidenceFreshnessKey = liveMode
    ? `${selectedError?.evidenceCount ?? ''}\u0000${selectedError?.latestEvidenceId ?? ''}`
    : ''
  const selectedErrorEvidence = useMemo(() => {
    if (!selectedError) return []
    if (liveMode) {
      return liveEvidenceByRunSignature[selectedEvidenceCacheKey] || []
    }
    return prototypeFixtures?.errorEvidence[selectedError.id] || []
  }, [liveEvidenceByRunSignature, liveMode, prototypeFixtures, selectedError, selectedEvidenceCacheKey])

  useEffect(() => {
    if (!liveMode || !testId || activeTableTab !== 'errors' || !selectedError?.signature) {
      setEvidenceLoadingKey('')
      setEvidenceError('')
      return undefined
    }
    const signature = selectedError.signature
    const cacheKey = liveEvidenceCacheKey(testId, signature)
    let cancelled = false
    setEvidenceLoadingKey(cacheKey)
    setEvidenceError('')
    void api.getTestErrorEvidence(testId, { signature, limit: 20 })
      .then(response => {
        if (cancelled) return
        setLiveEvidenceByRunSignature(prev => ({
          ...prev,
          [cacheKey]: response.evidence || [],
        }))
        setEvidenceLoadingKey('')
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setEvidenceError(getErrorMessage(error, 'Unable to load live error evidence'))
        setEvidenceLoadingKey('')
      })
    return () => {
      cancelled = true
    }
  }, [activeTableTab, api, liveMode, selectedError?.signature, selectedEvidenceFreshnessKey, testId])

  const chartFallbackHistory = useMemo(() => {
    if (!liveMode) return hasLiveRoute || emptyMode ? [] : prototypeFixtures?.history ?? []
    if (history.length > 0) return history
    return displayTest.live_metrics ? [mapExecutionMetricToHistoryPoint(displayTest.live_metrics)] : []
  }, [displayTest.live_metrics, emptyMode, hasLiveRoute, history, liveMode, prototypeFixtures])
  const { archiveSummaryHistory } = useExecutionArchiveSummaryHistory(liveMode ? displayTest : null)
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
    archiveSummaryHistory,
  })
  // Only genuine live runs query Prometheus. Passing an empty window (null test +
  // no data) keeps usePrometheusGraphs in its reset branch, so the prototype/mock
  // modes — whose chartData is seeded mock history — never hit the network.
  const { prometheusGraphs, prometheusLoading, prometheusError } = usePrometheusGraphs(
    liveMode ? displayTest : null,
    liveMode ? chartData : [],
  )
  const { importedDashboards, importedDashboardsLoading, importedDashboardsError } = useImportedDashboardGraphs(
    liveMode ? displayTest : null,
    liveMode ? chartData : [],
    liveMode ? (displayTest.project_id || projectId) : undefined,
  )
  const noRequestSamples = liveMode && (!displayTest.live_metrics || displayTest.live_metrics.total_transactions === 0) && archiveSummaryHistory.length === 0 && !localMetricsHistory.some(point => point.tps > 0 || point.avgMs > 0 || point.errorPct > 0)
  const totalCurrent = groups.reduce((sum, group) => sum + group.current, 0)
  const totalTarget = groups.reduce((sum, group) => sum + group.target, 0)
  const targetMode = groups[0]?.targetMode ?? 'vusers'
  const runSummaryAverages = recentMetricAverages(liveMode ? localMetricsHistory : [])
  const runSummaryTps = runSummaryAverages.tps
    ?? displayTest.live_metrics?.tps
    ?? groups.reduce((sum, group) => sum + group.tps, 0)
  const totalDurationSeconds = parseDurationToSeconds(displayTest.duration)
  const remainingSeconds = liveMode
    ? computeRemainingSeconds(displayTest, runStatus)
    : 0
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
  const graphScopedChartData = useMemo(() => {
    if (noGraphWallGroupsVisible) return []
    if (allGraphWallGroupsVisible) return chartData
    return (alignedBackendScopedChartData ?? clientScopedChartData).map(omitTransactionChartSeries)
  }, [alignedBackendScopedChartData, allGraphWallGroupsVisible, chartData, clientScopedChartData, noGraphWallGroupsVisible])
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
          const response = await api.getTestArchiveSummaries(
            testId,
            historicalGroupSummaryQuery(selectedGroupNames),
          )
          const loaded: IntervalSummaryRecord[] = response.summaries || []
          const responseGroups = Array.isArray(response.filters?.groups) ? response.filters.groups : []
          const archiveFilterHonored = selectedGroupNames.every(name => responseGroups.includes(name))
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

        const response = await api.getMetrics(testId, { groups: selectedGroupNames, limit: 300 })
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
    || prototypeFixturesError
    || (hasLiveRoute && !activeLiveTest ? 'Loading live run data for the command center...' : '')
    || (emptyMode ? 'No active run is selected for the command center.' : '')
    || (historicalRun ? DEFAULT_HISTORICAL_FEEDBACK : '')
    || (liveMode && state.feedback === DEFAULT_PROTOTYPE_FEEDBACK ? DEFAULT_LIVE_FEEDBACK : state.feedback)

  const setRunStatus = (runStatus: TestStatus, feedback: string) => {
    setState(prev => ({ ...prev, runStatus, feedback }))
  }

  const openGroupSettings = useCallback((group: string, kind: GroupSettingsDialogKind) => {
    setGroupSettingsDialog({ group, kind })
  }, [])

  const handleGroupRuntimeSettingsSaved = useCallback((result: TestGroupRuntimeSettingsUpdateResult) => {
    setGroupSettingsDialog(null)
    setState(prev => ({
      ...prev,
      feedback: result.no_changes
        ? `Runtime settings for ${result.group} are unchanged.`
        : `Runtime settings saved to this run for ${result.group} (${result.fields.join(', ')}). The change is recorded in the run's change history.`,
    }))
  }, [])

  const handleGroupParametersSaved = useCallback((result: TestGroupParametersUpdateResult) => {
    setGroupSettingsDialog(null)
    setState(prev => ({
      ...prev,
      feedback: result.no_changes
        ? `Parameters for ${result.group} are unchanged.`
        : `Parameters saved to this run for ${result.group} (${result.changed.join(', ')}). The change is recorded in the run's change history.`,
    }))
  }, [])

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
        const nextStatus = runStatus === 'RUNNING' || runStatus === 'PAUSED' || runStatus === 'STOPPING'
          ? 'STOPPING'
          : 'ABORTED'
        updateLiveStatus(nextStatus, nextStatus === 'STOPPING'
          ? 'Abort requested. Waiting for generator teardown and finalization.'
          : 'Abort requested. Pre-start run cancelled.')
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, 'Unable to abort test') }))
      }
      return
    }
    setRunStatus('ABORTED', 'Abort requested. Prototype run state moved to aborted.')
  }

  const changeTarget = (groupName: string, value: number) => {
    const targetMode = isGoalOrientedRun(displayTest) ? 'tps' : 'vusers'
    const role = displayTest.groups.find(group => group.name === groupName)?.role
    const target = targetMode === 'tps' ? clampTpsTarget(value) : clampTarget(value, role)
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: `${groupName} target staged at ${formatTargetValue(target, targetMode)}.`,
    }))
  }

  const changeTargetTransitionMode = (value: TargetTransitionMode) => {
    setState(prev => ({
      ...prev,
      targetTransitionMode: value,
      feedback: value === 'gradual'
        ? `Group load changes will ramp over ${prev.targetRampDuration.trim() || '2m'}.`
        : 'Group load changes will apply immediately.',
    }))
  }

  const changeTargetRampDuration = (value: string) => {
    setState(prev => ({
      ...prev,
      targetRampDuration: value,
      feedback: isValidDurationInput(value)
        ? `Group load ramp duration set to ${value}.`
        : 'Enter a valid ramp duration before applying gradual load changes.',
    }))
  }

  const applyTarget = async (groupName: string, value: number) => {
    const targetMode = isGoalOrientedRun(displayTest) ? 'tps' : 'vusers'
    const role = displayTest.groups.find(group => group.name === groupName)?.role
    const target = targetMode === 'tps' ? clampTpsTarget(value) : clampTarget(value, role)
    if (targetMode === 'tps' && target <= 0) {
      setState(prev => ({ ...prev, feedback: `${groupName} target TPS must be greater than zero.` }))
      return
    }
    const rampDuration = state.targetRampDuration.trim()
    const useGradualVuserChange = targetMode === 'vusers' && state.targetTransitionMode === 'gradual'
    if (useGradualVuserChange && !isValidDurationInput(rampDuration)) {
      setState(prev => ({ ...prev, feedback: 'Enter a valid ramp duration before applying gradual load changes.' }))
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
        const scaleOptions = useGradualVuserChange
          ? { action: 'run' as const, ramp_duration: rampDuration }
          : { action: 'run' as const }
        await api.scaleVUsers(testId, groupName, target, scaleOptions)
        setState(prev => ({
          ...prev,
          vuserTargets: { ...prev.vuserTargets, [groupName]: target },
          feedback: useGradualVuserChange
            ? `Ramping ${groupName} to ${target.toLocaleString()} VUsers over ${rampDuration}.`
            : `${groupName} VUser target applied at ${target.toLocaleString()}.`,
        }))
      } catch (error: unknown) {
        setState(prev => ({ ...prev, feedback: getErrorMessage(error, targetMode === 'tps' ? 'Unable to update goal target' : 'Unable to scale VUsers') }))
      }
      return
    }
    setState(prev => ({
      ...prev,
      vuserTargets: { ...prev.vuserTargets, [groupName]: target },
      feedback: useGradualVuserChange
        ? `${groupName} target staged at ${formatTargetValue(target, targetMode)} over ${rampDuration}.`
        : `${groupName} target staged at ${formatTargetValue(target, targetMode)}.`,
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

  const experienceSwitch = (
    <div className="command-center-experience-switch" role="group" aria-label="Command Center tools">
      {experienceVersion === 'v2' ? <button type="button" onClick={() => { setExperienceVersion('original'); setVariant('ops') }}>Back to operations</button>
        : <button type="button" onClick={() => { setExperienceVersion('v2'); setVariant('ops') }}>Open analysis</button>}
      {experienceVersion === 'v2' && <><button type="button" aria-pressed={variant === 'ops'} onClick={() => setVariant('ops')}>OPS VIEW</button><button type="button" aria-pressed={variant === 'grafana'} onClick={() => setVariant('grafana')}>GRAPH WALL</button></>}
    </div>
  )
  const commandCenterTopbarTarget = typeof document === 'undefined'
    ? null
    : document.querySelector<HTMLElement>('.main-topbar-page')

  return (
    <CommandViewMemoryContext.Provider key={viewMemory.testId} value={viewMemory.values}>
    <div ref={pageRef} className={`page command-center-page ${experienceVersion === 'v2' ? 'command-center-v2' : 'command-center-original'}`}>
      {active && (commandCenterTopbarTarget ? createPortal(experienceSwitch, commandCenterTopbarTarget) : experienceSwitch)}
      {emptyMode ? (
        <CommandCenterEmptyState
          onOpenResults={onOpenResults}
          liveRuns={liveRunOptions}
          discovering={discoveringRuns}
          onSelectRun={setAutoSelectedTestId}
        />
      ) : (
        <>
      <RunControlShelf
        test={displayTest}
        status={runStatus}
        remainingSeconds={remainingSeconds}
        totalCurrent={totalCurrent}
        totalTarget={totalTarget}
        targetMode={targetMode}
        errorPct={noRequestSamples ? undefined : wholeTestErrorPercent(displayTest.live_metrics, groups)}
        noRequestSamples={noRequestSamples}
        highestErrorGroup={noRequestSamples ? undefined : highestGroupErrorRate(groups)}
        tps={runSummaryTps}
        tpsHint={runSummaryAverages.tps !== undefined ? '10s avg' : undefined}
        feedback={feedback}
        historicalRun={historicalRun || (hasLiveRoute && !liveMode)}
        onStart={confirmRunAction('start', startRun)}
        // One button, two directions: confirm whichever one it is about to do.
        onPauseResume={confirmRunAction(runStatus === 'PAUSED' ? 'resume' : 'pause', togglePause)}
        onStop={confirmRunAction('stop', stopRun)}
        onAbort={confirmRunAction('abort', abortRun)}
        onOpenExecution={testId && onOpenExecution ? () => onOpenExecution(testId) : undefined}
        onOpenTestNotes={testId ? () => setShowNotes(true) : undefined}
        onChangeDuration={liveMode && runStatus === 'RUNNING' ? () => setShowRuntimeAdjustment(true) : undefined}
        changeDurationDisabled={showRuntimeAdjustment || pendingAction !== null}
      />
      {pendingAction && (
        <ConfirmRunActionModal
          action={pendingAction}
          runName={displayTest.name}
          runRef={displayTest.ref_key}
          onConfirm={onConfirmRunAction}
          onCancel={onCancelRunAction}
        />
      )}
      {showNotes && testId && (
        <TestNotesDialog
          testId={testId}
          testName={displayTest.name}
          onClose={() => setShowNotes(false)}
        />
      )}
      {showRuntimeAdjustment && testId && liveMode && runStatus === 'RUNNING' && (
        <RuntimeAdjustmentDialog
          test={displayTest}
          onClose={() => setShowRuntimeAdjustment(false)}
          onApplied={(result) => {
            setLiveTest(current => {
              if (!current) return current
              const goalTargets = { ...current.goal_targets }
              for (const adjusted of result.groups) {
                if (adjusted.target_tps !== undefined) {
                  goalTargets[adjusted.name] = { group: adjusted.name, target_tps: adjusted.target_tps }
                }
              }
              const groups = current.groups.map(group => {
                const adjusted = result.groups.find(candidate => candidate.name === group.name)
                return adjusted ? {
                  ...group,
                  target_duration: adjusted.target_duration,
                  effective_end_at: adjusted.effective_end_at,
                  runtime_state: adjusted.runtime_state,
                  vusers: adjusted.target_vusers,
                } : group
              })
              return {
                ...current,
                duration: result.impact.scenario_duration,
                total_vusers: groups.reduce((total, group) => total + group.vusers, 0),
                groups,
                goal_targets: Object.keys(goalTargets).length > 0 ? goalTargets : current.goal_targets,
                goal_config: current.goal_config && Object.keys(goalTargets).length > 0 ? {
                  ...current.goal_config,
                  target_average_tps: Object.values(goalTargets).reduce((total, target) => total + target.target_tps, 0),
                } : current.goal_config,
              }
            })
            setState(previous => ({
              ...previous,
              vuserTargets: {
                ...previous.vuserTargets,
                ...runtimeAdjustmentTargets(result.groups),
              },
              feedback: `Runtime adjusted to ${result.impact.scenario_duration}; peak load ${result.impact.peak_vusers.toLocaleString()} VUsers.`,
            }))
          }}
        />
      )}
      {groupSettingsDialog && testId && liveMode && groupSettingsDialog.kind === 'runtime-settings' && (
        <GroupRuntimeSettingsDialog
          testId={testId}
          groupName={groupSettingsDialog.group}
          onClose={() => setGroupSettingsDialog(null)}
          onSaved={handleGroupRuntimeSettingsSaved}
        />
      )}
      {groupSettingsDialog && testId && liveMode && groupSettingsDialog.kind === 'parameters' && (
        <GroupParametersDialog
          testId={testId}
          groupName={groupSettingsDialog.group}
          onClose={() => setGroupSettingsDialog(null)}
          onSaved={handleGroupParametersSaved}
        />
      )}
      {historicalRun && (
        <CommandCenterHistoricalBanner
          status={runStatus}
          archiveReady={Boolean(displayTest.archive)}
          archiveFinalizationTimedOut={archiveFinalizationTimedOut}
          archiveCheckPending={archiveCheckPending}
          archiveCheckError={archiveCheckError}
          onCheckArchive={() => { void checkCommandCenterArchiveAgain() }}
          onOpenArchive={testId && (onOpenArchive || onOpenExecution) ? () => (onOpenArchive || onOpenExecution)?.(testId) : undefined}
          onOpenResults={onOpenResults}
        />
      )}
      <ThresholdAlertStrip alerts={scopedThresholdAlerts} onAcknowledge={onAcknowledgeThresholdAlert} />

      {noRequestSamples && runStatus !== 'RESERVED' && runStatus !== 'QUEUED' && <section className="command-panel" aria-label="No request samples"><h3>No request samples</h3><p>Response times, throughput and error rates are unavailable until requests are captured. Load values describe VUser allocation; they do not establish workload execution.</p><LoadModelPanel groups={groups} testGroups={displayTest.groups} runStatus={runStatus} totalDurationSeconds={totalDurationSeconds} remainingSeconds={remainingSeconds} /></section>}
      {(!noRequestSamples || runStatus === 'RESERVED' || runStatus === 'QUEUED') && (variant === 'ops' || runStatus === 'RESERVED' || runStatus === 'QUEUED') && (
        <OpsCockpitView
          experienceVersion={experienceVersion}
          variant={variant}
          onVariantChange={changeVariant}
          groups={groups}
          testGroups={displayTest.groups}
          metrics={displayTest.live_metrics}
          metricsHistory={liveMode ? localMetricsHistory : []}
          errorPatterns={errorPatterns}
          selectedError={selectedError}
          selectedErrorEvidence={selectedErrorEvidence}
          selectedEvidenceId={selectedEvidenceId}
          onEvidenceSelect={setSelectedEvidenceId}
          activeTableTab={activeTableTab}
          onTableTabChange={setActiveTableTab}
          evidenceLoading={Boolean(selectedError && evidenceLoadingKey === selectedEvidenceCacheKey)}
          evidenceError={evidenceError}
          runStatus={runStatus}
          totalDurationSeconds={totalDurationSeconds}
          remainingSeconds={remainingSeconds}
          readOnly={historicalRun}
          targetTransitionMode={state.targetTransitionMode}
          targetRampDuration={state.targetRampDuration}
          onTargetTransitionModeChange={changeTargetTransitionMode}
          onTargetRampDurationChange={changeTargetRampDuration}
          onTargetChange={changeTarget}
          onApplyTarget={applyTarget}
          onErrorSelect={selectError}
          onOpenGroupSettings={liveMode && testId ? openGroupSettings : undefined}
          onChangeDuration={liveMode && runStatus === 'RUNNING' ? () => setShowRuntimeAdjustment(true) : undefined}
        />
      )}
      {!noRequestSamples && variant === 'grafana' && runStatus !== 'RESERVED' && runStatus !== 'QUEUED' && (
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
          prometheusGraphs={prometheusGraphs}
          prometheusLoading={prometheusLoading}
          prometheusError={prometheusError}
          importedDashboards={importedDashboards}
          importedDashboardsLoading={importedDashboardsLoading}
          importedDashboardsError={importedDashboardsError}
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
    </CommandViewMemoryContext.Provider>
  )
}

type CommandViewMemory = Map<string, unknown>
const CommandViewMemoryContext = createContext<CommandViewMemory | null>(null)

function useCommandViewState<T,>(key: string, initial: T) {
  const memory = useContext(CommandViewMemoryContext)
  const [value, setValue] = useState<T>(() => memory?.has(key) ? memory.get(key) as T : initial)
  useEffect(() => { memory?.set(key, value) }, [key, memory, value])
  return [value, setValue] as const
}

function CommandCenterEmptyState({
  onOpenResults,
  liveRuns = [],
  discovering = false,
  onSelectRun,
}: {
  onOpenResults?: () => void
  liveRuns?: TestSummary[]
  discovering?: boolean
  onSelectRun?: (testId: string) => void
}) {
  const hasRuns = liveRuns.length > 0
  return (
    <section className="command-empty-state" aria-label="No active command center run">
      <div>
        <h2>{hasRuns ? 'Select an active run' : 'No active run selected'}</h2>
        {hasRuns ? (
          <>
            <p>
              {liveRuns.length} runs are running or waiting. Choose one to inspect its status.
            </p>
            <ul className="command-empty-run-list" aria-label="Active runs">
              {liveRuns.map(run => (
                <li key={run.id}>
                  <button
                    type="button"
                    className="command-empty-run-option"
                    onClick={() => onSelectRun?.(run.id)}
                  >
                    <span className="command-empty-run-name">{run.name || run.ref_key || run.id}<small>{run.ref_key || run.id} · {run.created_at ? new Date(run.created_at).toLocaleString() : "Creation time unavailable"}</small></span>
                    <StatusBadge status={run.status} />
                  </button>
                </li>
              ))}
            </ul>
          </>
        ) : (
          <p>
            {discovering
              ? 'Looking for active runs…'
              : 'Start a test or open the Command Center from an active run to see live controls and metrics. Historical results can be opened from the Results folder.'}
          </p>
        )}
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
  noRequestSamples?: boolean
  test: TestSummary
  status: TestStatus
  remainingSeconds: number
  totalCurrent: number
  totalTarget: number
  targetMode: CommandGroup['targetMode']
  errorPct?: number
  highestErrorGroup?: { name: string; errorPct: number }
  tps?: number
  tpsHint?: string
  feedback: string
  historicalRun?: boolean
  onStart: () => void
  onPauseResume: () => void
  onStop: () => void
  onAbort: () => void
  onOpenExecution?: () => void
  onOpenTestNotes?: () => void
  onChangeDuration?: () => void
  changeDurationDisabled?: boolean
}

function RunControlShelf({
  test,
  status,
  remainingSeconds,
  totalCurrent,
  totalTarget,
  targetMode,
  errorPct,
  highestErrorGroup,
  tps,
  tpsHint,
  feedback,
  historicalRun = false,
  onStart,
  onPauseResume,
  onStop,
  onAbort,
  onOpenExecution,
  onOpenTestNotes,
  onChangeDuration,
  changeDurationDisabled = false,
  noRequestSamples = false,
}: RunControlShelfProps) {
  const terminal = status === 'ABORTED' || status === 'STOPPING' || status === 'COMPLETE' || status === 'FAILED'
  // Abort must stay available during STOPPING so a wedged graceful stop can still be escalated.
  const fullyTerminal = status === 'ABORTED' || status === 'COMPLETE' || status === 'FAILED'
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

      <div className={`command-run-stats${tps !== undefined ? ' with-tps' : ''}${test.groups.length > 1 ? ' with-highest-error' : ''}`} aria-label="Run summary">
        <MetricPill
          label={canStart ? "Planned load" : "Current / Target Load"}
          value={canStart ? `${formatNumber(totalTarget)} ${targetMode === 'tps' ? 'TPS' : 'VU'} · Not started` : noRequestSamples && !test.live_metrics ? `No samples / ${formatNumber(totalTarget)} ${targetMode === 'tps' ? 'TPS' : 'VU'}` : targetMode === 'tps'
            ? `${formatNumber(totalCurrent)} VU / ${formatNumber(totalTarget, 1)} TPS`
            : `${formatNumber(totalCurrent)} / ${formatNumber(totalTarget)} VU`}
          tone="info"
        />
        <MetricPill label="Remaining" value={formatRemaining(remainingSeconds)} tone={remainingSeconds <= 60 ? 'warning' : 'default'} />
        {tps !== undefined && <MetricPill label={tpsHint ? `TPS (${tpsHint})` : 'TPS'} value={canStart ? "Not started" : noRequestSamples ? "No samples" : formatNumber(tps, 1)} tone="accent" />}
        <MetricPill label="Overall Error Rate" value={canStart || noRequestSamples ? "No samples" : formatOptionalPercent(errorPct)} tone={errorPct && errorPct > 0 ? "danger" : "default"} />
        {test.groups.length > 1 && (
          <MetricPill
            label="Highest Error Rate"
            value={canStart || noRequestSamples ? "No samples" : highestErrorGroup ? `${highestErrorGroup.errorPct < 0.005 ? "<0.01%" : formatOptionalPercent(highestErrorGroup.errorPct)} · ${highestErrorGroup.name}` : errorPct && errorPct > 0 ? "No group data" : "No errors"}
            tone={highestErrorGroup ? "danger" : "default"}
          />
        )}
      </div>

      <div className="command-run-actions">
        {onOpenExecution && !historicalRun && (
          <button type="button" data-command-details className="btn btn-sm btn-ghost" onClick={onOpenExecution}>
            Advanced run details
          </button>
        )}
        {onOpenTestNotes && (
          <button type="button" className="btn btn-sm btn-ghost" onClick={onOpenTestNotes}>
            <NotesIcon />
            Test Notes
          </button>
        )}
        {onChangeDuration && (
          <button type="button" className="btn btn-sm btn-accent" onClick={onChangeDuration} disabled={changeDurationDisabled}>
            Change duration
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
            {!fullyTerminal && (
              <button type="button" className="btn btn-sm btn-danger" onClick={onAbort} disabled={fullyTerminal}>
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

function NotesIcon() {
  return (
    <svg className="command-notes-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path d="M7 3.75h8.5L20.25 8.5V20A1.25 1.25 0 0 1 19 21.25H7A1.25 1.25 0 0 1 5.75 20V5A1.25 1.25 0 0 1 7 3.75Z" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      <path d="M15.25 3.75V8.5h5" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
      <path d="M9 12h7M9 15.5h7M9 19h4" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
    </svg>
  )
}

function CommandCenterHistoricalBanner({
  status,
  archiveReady,
  archiveFinalizationTimedOut,
  archiveCheckPending,
  archiveCheckError,
  onCheckArchive,
  onOpenArchive,
  onOpenResults,
}: {
  status: TestStatus
  archiveReady: boolean
  archiveFinalizationTimedOut?: boolean
  archiveCheckPending?: boolean
  archiveCheckError?: string
  onCheckArchive?: () => void
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
            : archiveFinalizationTimedOut
              ? ' No saved archive is available and publication progress is unknown. Check again without leaving this snapshot.'
              : ' Historical results can be opened from the Results folder when no Command Center snapshot is available.'}
        </p>
        {archiveCheckError && <p className="command-history-retry-error" role="alert">{archiveCheckError}</p>}
      </div>
      <div className="command-history-actions">
        {archiveReady && onOpenArchive && (
          <button type="button" className="btn btn-sm btn-primary command-open-archive-action" onClick={onOpenArchive}>
            Open Results Archive
          </button>
        )}
        {!archiveReady && archiveFinalizationTimedOut && onCheckArchive && (
          <button type="button" className="btn btn-sm btn-primary" onClick={onCheckArchive} disabled={archiveCheckPending}>
            {archiveCheckPending ? 'Checking…' : 'Check again'}
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
      <strong title={value}>{value}</strong>
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
  onChartMouseDown: (anchor?: ExecutionChartZoomAnchor) => void
  onChartMouseMove: (anchor?: ExecutionChartZoomAnchor) => void
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
  experienceVersion: 'original' | 'v2'
  variant: VariantId
  onVariantChange: (variant: VariantId) => void
  groups: CommandGroup[]
  testGroups: TestGroup[]
  metrics?: LiveMetrics
  metricsHistory?: MetricsHistoryPoint[]
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
  targetTransitionMode: TargetTransitionMode
  targetRampDuration: string
  onTargetTransitionModeChange: (value: TargetTransitionMode) => void
  onTargetRampDurationChange: (value: string) => void
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onErrorSelect: (errorId: string) => void
  onOpenGroupSettings?: (groupName: string, kind: GroupSettingsDialogKind) => void
  onChangeDuration?: () => void
}

interface GrafanaWallViewProps extends CommonViewProps {
  chartGroups: CommandGroup[]
  groupScopedChartData: MetricsHistoryPoint[]
  groupScopedMetrics?: LiveMetrics
  groupVisibility: Record<string, boolean>
  onGroupVisibilityToggle: (groupKey: string) => void
  prometheusGraphs?: PrometheusGraph[]
  prometheusLoading?: boolean
  prometheusError?: string
  importedDashboards?: ImportedDashboardResult[]
  importedDashboardsLoading?: boolean
  importedDashboardsError?: string
}

function OpsCockpitView({
  experienceVersion,
  variant,
  onVariantChange,
  groups,
  testGroups,
  metrics,
  metricsHistory,
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
  targetTransitionMode,
  targetRampDuration,
  onTargetTransitionModeChange,
  onTargetRampDurationChange,
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
  onOpenGroupSettings,
  onChangeDuration,
}: OpsCockpitViewProps) {
  if (runStatus === 'RESERVED' || runStatus === 'QUEUED') {
    return <section className="command-panel" aria-label="Planned run"><h3>Run not started</h3><p>Load targets describe the plan. Throughput, errors and response times will appear after execution produces samples.</p><LoadModelPanel groups={groups} testGroups={testGroups} runStatus={runStatus} totalDurationSeconds={totalDurationSeconds} remainingSeconds={remainingSeconds} /></section>
  }
  if (experienceVersion === 'v2') {
    return (
      <OpsCockpitV2View
        variant={variant}
        onVariantChange={onVariantChange}
        groups={groups}
        testGroups={testGroups}
        metrics={metrics}
        metricsHistory={metricsHistory}
        errorPatterns={errorPatterns}
        selectedError={selectedError}
        selectedErrorEvidence={selectedErrorEvidence}
        selectedEvidenceId={selectedEvidenceId}
        onEvidenceSelect={onEvidenceSelect}
        activeTableTab={activeTableTab}
        onTableTabChange={onTableTabChange}
        evidenceLoading={evidenceLoading}
        evidenceError={evidenceError}
        runStatus={runStatus}
        totalDurationSeconds={totalDurationSeconds}
        remainingSeconds={remainingSeconds}
        readOnly={readOnly}
        targetTransitionMode={targetTransitionMode}
        targetRampDuration={targetRampDuration}
        onTargetTransitionModeChange={onTargetTransitionModeChange}
        onTargetRampDurationChange={onTargetRampDurationChange}
        onTargetChange={onTargetChange}
        onApplyTarget={onApplyTarget}
        onErrorSelect={onErrorSelect}
        onChangeDuration={onChangeDuration}
      />
    )
  }
  return (
    <section className="command-layout command-layout-ops" aria-label="Run operations">
      <div className="command-primary-column">
        <div className="command-load-model-row">
          <GroupLoadTable
            groups={groups}
            onTargetChange={onTargetChange}
            onApplyTarget={onApplyTarget}
            compact={false}
            paired
            readOnly={readOnly}
            targetTransitionMode={targetTransitionMode}
            targetRampDuration={targetRampDuration}
            onTargetTransitionModeChange={onTargetTransitionModeChange}
            onTargetRampDurationChange={onTargetRampDurationChange}
            onOpenGroupSettings={onOpenGroupSettings}
          />
          <LoadModelPanel
            groups={groups}
            testGroups={testGroups}
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

function numericCommandValue(source: unknown, ...keys: string[]) {
  if (!source || typeof source !== 'object') return 0
  const record = source as Record<string, unknown>
  for (const key of keys) {
    const value = Number(record[key])
    if (Number.isFinite(value)) return value
  }
  return 0
}

type OpsCockpitV2ViewProps = Omit<OpsCockpitViewProps, 'experienceVersion' | 'onOpenGroupSettings'>

function OpsCockpitV2View({
  groups,
  testGroups,
  metrics,
  metricsHistory = [],
  errorPatterns,
  selectedError,
  selectedErrorEvidence,
  selectedEvidenceId,
  onEvidenceSelect,
  activeTableTab,
  onTableTabChange,
  evidenceLoading,
  evidenceError,
  totalDurationSeconds,
  remainingSeconds,
  readOnly = false,
  targetTransitionMode,
  targetRampDuration,
  onTargetTransitionModeChange,
  onTargetRampDurationChange,
  onTargetChange,
  onApplyTarget,
  onErrorSelect,
  onChangeDuration,
}: OpsCockpitV2ViewProps) {
  const progressPct = loadProgressPercent(totalDurationSeconds, remainingSeconds)
  const profile = useMemo(
    () => buildLoadModelProfile(testGroups, groups, totalDurationSeconds),
    [groups, testGroups, totalDurationSeconds],
  )
  const totalCurrent = groups.reduce((sum, group) => sum + group.current, 0)
  const totalTarget = groups.reduce((sum, group) => sum + (group.targetMode === 'vusers' ? group.target : group.suggestedVusers ?? group.targetVusers ?? group.current), 0)
  const plannedNow = plannedLoadAtProgress(profile, progressPct)
  const chartProfile = profile.map(point => {
    if (point.pct > progressPct) return { ...point, actual: null }
    if (metricsHistory.length === 0) {
      return { ...point, actual: point.pct === 0 ? totalCurrent : null }
    }
    const historyIndex = Math.min(
      metricsHistory.length - 1,
      Math.max(0, Math.round((point.pct / Math.max(1, progressPct)) * (metricsHistory.length - 1))),
    )
    return {
      ...point,
      actual: numericCommandValue(metricsHistory[historyIndex], 'vusers', 'activeVusers', 'active_vusers'),
    }
  })
  return (
    <section className="command-v2" aria-label="Command Center analysis Ops view">
      <div className="command-v2-workbench">
        <div className="command-v2-main">
          <section className="command-v2-load-model" aria-label="Command Center analysis load model">
            <div className="command-v2-section-header">
              <div>
                <h3>Load Model</h3>
                <p>{loadPhaseAtProgress(profile, progressPct)} / tracking planned and actual VUsers</p>
              </div>
              <div className="command-v2-model-kpis">
                <span>Planned <strong>{formatNumber(plannedNow)}</strong></span>
                <span>Actual <strong>{formatNumber(totalCurrent)}</strong></span>
                <span>Delta <strong>{totalCurrent - plannedNow >= 0 ? '+' : ''}{formatNumber(totalCurrent - plannedNow)}</strong></span>
                <span>Elapsed <strong>{formatNumber(progressPct, 0)}%</strong></span>
              </div>
            </div>

            <div className="command-v2-chart" aria-label="Planned and actual VUsers over test duration">
              <ResponsiveContainer width="100%" height={230} minWidth={320}>
                <AreaChart data={chartProfile} margin={{ top: 10, right: 18, left: -8, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" />
                  <XAxis dataKey="pct" type="number" domain={[0, 100]} ticks={[0, 25, 50, 75, 100]} tickFormatter={value => `${value}%`} stroke="var(--text-muted)" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} />
                  <YAxis stroke="var(--text-muted)" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} />
                  <Tooltip contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} labelFormatter={value => `${formatNumber(Number(value))}% elapsed`} />
                  <ReferenceLine x={progressPct} stroke="var(--text-primary)" strokeWidth={1.5} ifOverflow="extendDomain" />
                  <Area type="stepAfter" dataKey="planned" name="Target VUsers" stroke="var(--accent)" strokeDasharray="5 4" fill="transparent" strokeWidth={2} dot={false} isAnimationActive={false} />
                  <Area type="stepAfter" dataKey="actual" name="Actual VUsers" stroke="var(--chart-2)" fill="var(--accent-soft)" fillOpacity={0.32} strokeWidth={2} dot={false} isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>

            <div className="command-v2-capacity" aria-label="Capacity pressure">
              <span>Capacity</span>
              <div><i style={{ width: `${Math.min(100, Math.max(3, (totalCurrent / Math.max(1, totalTarget * 1.35)) * 100))}%` }} /></div>
              <strong>{totalCurrent <= totalTarget ? 'Comfort zone' : 'Watch'}</strong>
            </div>

            <div className="command-v2-group-list" role="table" aria-label="Inline group load controls">
              <div className="command-v2-group-row header" role="row">
                <span role="columnheader">Group</span>
                <span role="columnheader">VUsers (actual / target)</span>
                <span role="columnheader">TPS</span>
                <span role="columnheader">ART</span>
                <span role="columnheader">Errors</span>
                <span role="columnheader">Adjust target</span>
              </div>
              {groups.map((group, index) => {
                const art = numericCommandValue(group, 'art', 'artMs', 'avgResponseMs', 'avg_response_ms')
                const errors = numericCommandValue(group, 'errorPct', 'errorRate', 'error_pct')
                const target = Math.max(0, Math.round(group.target))
                return (
                  <div className="command-v2-group-row" role="row" key={group.name}>
                    <span role="cell" className="command-v2-group-name"><i data-group-index={index} /><strong>{group.name}</strong><small>{group.protocol}</small></span>
                    <span role="cell"><strong>{formatNumber(group.current)} / {formatNumber(target)}</strong><em>{target > 0 ? formatNumber((group.current / target) * 100, 0) : '0'}%</em></span>
                    <span role="cell"><strong>{formatNumber(group.tps, 1)}</strong></span>
                    <span role="cell"><strong>{formatNumber(art, 0)} ms</strong></span>
                    <span role="cell" className={errors > 1 ? 'danger' : 'healthy'}><strong>{formatNumber(errors, 2)}%</strong></span>
                    <span role="cell" className="command-v2-target-stepper">
                      <button type="button" disabled={readOnly || target <= 0} aria-label={`Decrease ${group.name} target`} onClick={() => onTargetChange(group.name, Math.max(0, target - 1))}>−</button>
                      <strong>{formatNumber(target)}</strong>
                      <button type="button" disabled={readOnly} aria-label={`Increase ${group.name} target`} onClick={() => onTargetChange(group.name, target + 1)}>+</button>
                      <button type="button" className="apply" disabled={readOnly} onClick={() => onApplyTarget(group.name, target)}>Apply</button>
                    </span>
                  </div>
                )
              })}
            </div>
          </section>

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

        <aside className="command-v2-inspector" aria-label="Change duration and target transition">
          <section>
            <div className="command-v2-inspector-title"><h3>Change Duration</h3><span>{formatRemaining(remainingSeconds)} left</span></div>
            <dl>
              <div><dt>Current duration</dt><dd>{formatRemaining(totalDurationSeconds)}</dd></div>
              <div><dt>Elapsed</dt><dd>{formatNumber(progressPct, 0)}%</dd></div>
            </dl>
            <div className="command-v2-inspector-actions">
              <button type="button" className="primary" disabled={readOnly || !onChangeDuration} onClick={onChangeDuration}>Change Duration</button>
            </div>
          </section>

          <section className="command-v2-transition">
            <div className="command-v2-inspector-title"><h3>Target Transition</h3></div>
            <p>Applies to inline group target adjustments.</p>
            <div className="command-v2-radio-row" role="radiogroup" aria-label="Target transition mode">
              <label><input type="radio" name="command-v2-transition" checked={targetTransitionMode === 'immediate'} disabled={readOnly} onChange={() => onTargetTransitionModeChange('immediate')} /> Immediate</label>
              <label><input type="radio" name="command-v2-transition" checked={targetTransitionMode === 'gradual'} disabled={readOnly} onChange={() => onTargetTransitionModeChange('gradual')} /> Gradual</label>
            </div>
            <label className="command-v2-ramp-duration">
              <span>Ramp duration</span>
              <input type="text" value={targetRampDuration} disabled={readOnly || targetTransitionMode !== 'gradual'} onChange={event => onTargetRampDurationChange(event.target.value)} />
            </label>
          </section>
        </aside>
      </div>
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
  prometheusGraphs,
  prometheusLoading,
  prometheusError,
  importedDashboards,
  importedDashboardsLoading,
  importedDashboardsError,
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
  const [activeGraphGroupTab, setActiveGraphGroupTab] = useCommandViewState<GraphGroupTabId>('graphTab', 'test_results')
  const [underLoadDisplayMode, setUnderLoadDisplayMode] = useCommandViewState<UnderLoadDisplayMode>('underLoadMode', 'graphs')
  const showUnderLoadDisplaySwitch = activeGraphGroupTab === 'under_load'

  return (
    <section className="command-layout command-layout-grafana" aria-label="Runtime graph wall">
      <div className="command-grafana-grid">
        <CommandRuntimeGraphs
          variant="grafana"
          groups={chartGroups}
          metrics={metrics}
          chartData={chartData}
          groupScopedChartData={groupScopedChartData}
          groupScopedMetrics={groupScopedMetrics}
          prometheusGraphs={prometheusGraphs}
          prometheusLoading={prometheusLoading}
          prometheusError={prometheusError}
          importedDashboards={importedDashboards}
          importedDashboardsLoading={importedDashboardsLoading}
          importedDashboardsError={importedDashboardsError}
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

interface InfrastructureRow {
  reserved?: number
  remaining?: number
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
  const [filters, setFilters] = useCommandViewState<Record<CommandTableTab, string>>('tableFilters', { scenario: '', errors: '', infrastructure: '' })
  const filter = filters[activeTab]
  const setFilter = (value: string) => setFilters(previous => ({ ...previous, [activeTab]: value }))
  const [sorts, setSorts] = useCommandViewState<Record<CommandTableTab, CommandSortState>>('tableSorts', {
    scenario: { key: 'type', direction: 'asc' },
    errors: { key: 'count', direction: 'desc' },
    infrastructure: { key: 'type', direction: 'asc' },
  })
  const [transactionSnapshot, setTransactionSnapshot] = useState<Record<string, TxMetrics>>({})
  const errorRowOrderRef = useRef<{ viewKey: string; ids: string[] }>({ viewKey: '', ids: [] })
  useEffect(() => {
    const incoming = metrics?.transactions
    if (!incoming || Object.keys(incoming).length === 0) return
    setTransactionSnapshot(incoming)
  }, [metrics?.transactions])
  const activeSort = sorts[activeTab]
  const scenarioRows = useMemo(() => buildScenarioRunRows(groups, metrics, transactionSnapshot), [groups, metrics, transactionSnapshot])
  const infrastructureRows = useMemo(() => buildInfrastructureRows(groups, metrics), [groups, metrics])
  const activeEvidence = selectedEvidence.find(item => item.id === selectedEvidenceId) || selectedEvidence[0]



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
  const sortedErrorRows = useMemo(
    () => filterAndSortRows(patterns, filter, activeSort),
    [activeSort, filter, patterns],
  )
  const errorViewKey = `${filter.trim().toLowerCase()}\u001f${activeSort.key}\u001f${activeSort.direction}`
  const filteredErrorRows = stabilizeErrorRowOrder(sortedErrorRows, errorViewKey, errorRowOrderRef.current)
  errorRowOrderRef.current = { viewKey: errorViewKey, ids: filteredErrorRows.map(row => row.id) }
  const filteredInfrastructureRows = useMemo(
    () => filterAndSortRows(infrastructureRows, filter, activeSort),
    [activeSort, filter, infrastructureRows],
  )

  return (
    <section className="command-run-tables" aria-label="Command center run tables">
      <div className="command-table-tabs">
        <TabList className="command-table-tab-buttons" role="tablist" aria-label="OPS data tables" onKeyDown={handleTabKey}>
          {COMMAND_TABLE_TABS.map(tab => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              className={activeTab === tab.id ? 'active' : ''}
              aria-selected={activeTab === tab.id}
              onClick={() => onTabChange(tab.id)} tabIndex={(activeTab === tab.id) ? 0 : -1}
            >
              {tab.label}
            </button>
          ))}
        </TabList>
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
            hasErrors={patterns.length > 0}
            recordedErrors={metrics?.total_errors || 0}
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

export function stabilizeErrorRowOrder(
  sortedRows: ErrorPattern[],
  viewKey: string,
  previous: { viewKey: string; ids: string[] },
): ErrorPattern[] {
  if (previous.viewKey !== viewKey || previous.ids.length === 0) return sortedRows
  const byId = new Map(sortedRows.map(row => [row.id, row]))
  const stable = previous.ids.flatMap(id => {
    const row = byId.get(id)
    if (!row) return []
    byId.delete(id)
    return [row]
  })
  for (const row of sortedRows) {
    if (byId.delete(row.id)) stable.push(row)
  }
  return stable
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
  const essentialColumns = [0, 1, 4, 5, 6, 11, 15]
  const labels = ['Name', 'Group', 'Type', 'Protocol', 'Current VU', 'Target VU', 'TPS', 'Target TPS', 'Gap', 'Tx', 'Errors', 'Error %', 'ART', 'p50', 'p90', 'p95', 'p99']
  const [columns, setColumns] = useCommandViewState('scenarioColumns', essentialColumns)
  const fullMetrics = columns.length === labels.length
  return (
    <div className="command-data-table-wrap" role="tabpanel" aria-label="Scenario run metrics">
      <label className="command-column-preset"><input type="checkbox" checked={fullMetrics} onChange={event => setColumns(event.target.checked ? labels.map((_, index) => index) : essentialColumns)} /> Show all 17 metrics</label>
      <details className="command-column-picker"><summary>Choose columns</summary>
        <div>{labels.map((label, index) => <label key={label}><input type="checkbox" checked={columns.includes(index)} disabled={index < 2} onChange={event => setColumns(previous => event.target.checked ? [...previous, index] : previous.filter(column => column !== index))} />{label}</label>)}</div>
      </details>
      <table className={`command-data-table command-scenario-table ${fullMetrics ? 'full-metrics' : 'essential-metrics'}`}>
        <thead>
          <tr>
            <SortableHeader hidden={!columns.includes(0)} label="Name" sortKey="name" sort={sort} onSort={onSort} />
            <SortableHeader hidden={!columns.includes(1)} label="Group" sortKey="group" sort={sort} onSort={onSort} />
            <SortableHeader hidden={!columns.includes(2)} label="Type" sortKey="type" sort={sort} onSort={onSort} />
            <SortableHeader hidden={!columns.includes(3)} label="Protocol" sortKey="protocol" sort={sort} onSort={onSort} />
            <SortableHeader hidden={!columns.includes(4)} label="Current VU" sortKey="currentVusers" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(5)} label="Target VU" sortKey="targetVusers" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(6)} label="TPS" sortKey="tps" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(7)} label="Target TPS" sortKey="targetTps" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(8)} label="Gap" sortKey="throughputGap" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(9)} label="Tx" sortKey="transactions" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(10)} label="Errors" sortKey="errors" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(11)} label="Error %" sortKey="errorPct" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(12)} label="ART" sortKey="art" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(13)} label="p50" sortKey="p50" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(14)} label="p90" sortKey="p90" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(15)} label="p95" sortKey="p95" sort={sort} onSort={onSort} align="right" />
            <SortableHeader hidden={!columns.includes(16)} label="p99" sortKey="p99" sort={sort} onSort={onSort} align="right" />
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.id} className={row.type === 'Group' ? 'command-table-group-row' : ''}>
              <td hidden={!columns.includes(0)}><strong>{row.name}</strong></td>
              <td hidden={!columns.includes(1)}>{row.group}</td>
              <td hidden={!columns.includes(2)}>{row.type}</td>
              <td hidden={!columns.includes(3)}>{row.protocol}</td>
              <td hidden={!columns.includes(4)} className="numeric">{formatOptionalNumber(row.currentVusers)}</td>
              <td hidden={!columns.includes(5)} className="numeric">{formatOptionalNumber(row.targetVusers)}</td>
              <td hidden={!columns.includes(6)} className="numeric">{formatOptionalNumber(row.tps, 1)}</td>
              <td hidden={!columns.includes(7)} className="numeric">{formatOptionalNumber(row.targetTps, 1)}</td>
              <td hidden={!columns.includes(8)} className={`numeric ${typeof row.throughputGap === 'number' ? metricTone(Math.abs(row.throughputGap), 5, 15) : ''}`}>{formatOptionalPercent(row.throughputGap)}</td>
              <td hidden={!columns.includes(9)} className="numeric">{formatOptionalNumber(row.transactions)}</td>
              <td hidden={!columns.includes(10)} className="numeric">{formatOptionalNumber(row.errors)}</td>
              <td hidden={!columns.includes(11)} className={`numeric ${metricTone(row.errorPct, 0.75, 1.5)}`}>{formatPercent(row.errorPct)}</td>
              <td hidden={!columns.includes(12)} className="numeric">{formatOptionalMs(row.art)}</td>
              <td hidden={!columns.includes(13)} className="numeric">{formatOptionalMs(row.p50)}</td>
              <td hidden={!columns.includes(14)} className="numeric">{formatOptionalMs(row.p90)}</td>
              <td hidden={!columns.includes(15)} className="numeric">{formatOptionalMs(row.p95)}</td>
              <td hidden={!columns.includes(16)} className="numeric">{formatOptionalMs(row.p99)}</td>
            </tr>
          ))}
          {rows.length === 0 && <EmptyTableRow colSpan={columns.length} message="No scenario metrics match the current filter." />}
        </tbody>
      </table>
    </div>
  )
}

function ErrorDetailsTable({
  rows,
  hasErrors,
  recordedErrors,
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
  hasErrors: boolean
  recordedErrors: number
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
        <table className="command-data-table command-error-table" role="grid" aria-label="Error patterns">
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
                tabIndex={0}
                aria-selected={selectedError?.id === row.id}
                onClick={() => onErrorSelect(row.id)}
                onKeyDown={event => activateSelectableRow(event, () => onErrorSelect(row.id))}
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
            {rows.length === 0 && <EmptyTableRow colSpan={9} message={hasErrors ? "No matching errors. Try clearing the filter." : recordedErrors > 0 ? "Errors recorded; signatures are not available yet." : "No errors recorded."} />}
          </tbody>
        </table>
      </div>
      {selectedError && rows.some(row => row.id === selectedError.id) && <ErrorEvidencePanel
        selectedError={selectedError}
        evidence={selectedEvidence}
        activeEvidence={activeEvidence}
        loading={evidenceLoading}
        error={evidenceError}
        onEvidenceSelect={onEvidenceSelect}
      />}
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
    <div className="command-evidence-panel" aria-label="Live error evidence" aria-busy={loading}>
      <div className="command-evidence-header">
        <div>
          <span>Selected Signature</span>
          <strong>{selectedError.title}</strong>
        </div>
        <small>{selectedError.evidenceCount ?? evidence.length} captured samples</small>
      </div>
      {error && <p className="command-table-status danger">{error}</p>}
      {!loading && !error && evidence.length === 0 && (
        <p className="command-table-status">
          Snapshot evidence unavailable for this live signature. Capture requires `snapshot_on_error` and a failed transaction snapshot.
        </p>
      )}
      {evidence.length > 0 && (
        <>
          <div className="command-data-table-wrap command-evidence-table-wrap">
            <table className="command-data-table command-evidence-table" role="grid" aria-label="Captured error evidence">
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
                    tabIndex={0}
                    aria-selected={activeEvidence?.id === item.id}
                    onClick={() => onEvidenceSelect(item.id)}
                    onKeyDown={event => activateSelectableRow(event, () => onEvidenceSelect(item.id))}
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

function activateSelectableRow(event: ReactKeyboardEvent<HTMLElement>, activate: () => void) {
  if (event.key !== 'Enter' && event.key !== ' ') return
  event.preventDefault()
  activate()
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
              copyFieldName="conversationId"
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
            <SortableHeader label="Reserved %" sortKey="reserved" sort={sort} onSort={onSort} align="right" />
            <SortableHeader label="Remaining %" sortKey="remaining" sort={sort} onSort={onSort} align="right" />
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
              <td><span className={`command-state-chip ${row.saturation}`}>{row.saturation}</span><span className="command-table-subtext">{row.saturation === 'unknown' ? 'Telemetry unavailable' : row.impact}</span></td>
              <td className="numeric">{formatOptionalPercent(row.cpu)}</td>
              <td className="numeric">{formatOptionalPercent(row.memory)}</td>
              <td className="numeric">{formatOptionalPercent(row.reserved)}</td>
              <td className="numeric">{formatOptionalPercent(row.remaining)}</td>
              <td className="numeric">{formatOptionalNumber(row.network, 1)}</td>
              <td className="numeric">{formatOptionalPercent(row.disk)}</td>
              <td className="numeric">{formatOptionalMs(row.gc)}</td>
              <td>{row.impact}</td>
            </tr>
          ))}
          {rows.length === 0 && <EmptyTableRow colSpan={18} message="No infrastructure rows match the current filter." />}
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
  hidden,
}: {
  label: string
  sortKey: string
  sort: CommandSortState
  onSort: (key: string) => void
  align?: 'right'
  hidden?: boolean
}) {
  const active = sort.key === sortKey
  return (
      <th hidden={hidden} className={align === 'right' ? 'numeric' : undefined} aria-label={`${label} sort`} aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
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
      reserved: generator.pod_capacity?.state === 'ready' ? generator.pod_capacity.reserved_pct : undefined,
      remaining: generator.pod_capacity?.state === 'ready' ? generator.pod_capacity.remaining_pct : undefined,
      network: (resources?.net_sent_kbps || 0) + (resources?.net_recv_kbps || 0) || resources?.tcp_retransmits_per_sec,
      disk: resources?.disk_io_time_pct,
      gc: resources?.gc_pause_ms,
      impact: generator.impact_reasons?.join(', ') || (generator.saturation_state && generator.saturation_state !== 'unknown' ? (generator.saturation_state === 'clear' ? 'No pressure reported' : 'Generator reported pressure; no reason supplied') : 'Telemetry unavailable'),
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
      impact: `${!target.last_success ? 'Probe failed · ' : target.route_status && target.route_status !== 'clear' ? 'Route probe reported ' + target.route_status + ' · ' : ''}dns ${formatOptionalMs(target.dns_ms)} / connect ${formatOptionalMs(target.connect_ms)} / ttfb ${formatOptionalMs(target.ttfb_ms)}`,
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
  const safelyScopedData = data.map(omitTransactionChartSeries)
  if (!selectedGroupHistoryHasFields(safelyScopedData, selectedNames)) return safelyScopedData
  return safelyScopedData.map(point => {
    let hasGroupData = false
    let selectedVUsers = 0
    let vuserSamples = 0
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
        vuserSamples += 1
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
      vusers: vuserSamples > 0 ? selectedVUsers : point.vusers,
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
  prometheusGraphs?: PrometheusGraph[]
  prometheusLoading?: boolean
  prometheusError?: string
  importedDashboards?: ImportedDashboardResult[]
  importedDashboardsLoading?: boolean
  importedDashboardsError?: string
  expandedChart: string | null
  onExpandedChartChange: (value: string | null) => void
  onChartMouseDown: (anchor?: ExecutionChartZoomAnchor) => void
  onChartMouseMove: (anchor?: ExecutionChartZoomAnchor) => void
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
  prometheusGraphs,
  prometheusLoading,
  prometheusError,
  importedDashboards,
  importedDashboardsLoading,
  importedDashboardsError,
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
  const groupColorByName = Object.fromEntries(groups.map(group => [group.name, group.color]))
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
        groupColorByName={groupColorByName}
        showLoadImpact={false}
        loadImpactData={loadImpactSourceData}
        prometheusGraphs={prometheusGraphs}
        prometheusLoading={prometheusLoading}
        prometheusError={prometheusError}
        importedDashboards={importedDashboards}
        importedDashboardsLoading={importedDashboardsLoading}
        importedDashboardsError={importedDashboardsError}
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
  testGroups,
  runStatus,
  totalDurationSeconds,
  remainingSeconds,
}: {
  groups: CommandGroup[]
  testGroups: TestGroup[]
  runStatus: TestStatus
  totalDurationSeconds: number
  remainingSeconds: number
}) {
  const totalTarget = groups.reduce((sum, group) => sum + (group.targetMode === 'vusers' ? group.target : group.suggestedVusers ?? group.targetVusers ?? group.current), 0)
  const totalCurrent = groups.reduce((sum, group) => sum + group.current, 0)
  const progressPct = loadProgressPercent(totalDurationSeconds, remainingSeconds)
  const profile = useMemo(
    () => buildLoadModelProfile(testGroups, groups, totalDurationSeconds),
    [groups, testGroups, totalDurationSeconds],
  )
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
          {runStatus === 'PAUSED' && <p>Allocated users are retained while active load pauses. Rolling throughput decays as its averaging window clears.</p>}
        </div>
        <span className="command-load-model-badge">{formatNumber(progressPct, 0)}% elapsed</span>
      </div>

      <details className="command-load-comparison"><summary>Load comparison</summary>
      <div className="command-load-model-stats">
        <MetricPill label="Planned VU" value={formatNumber(plannedNow)} tone="accent" />
        <MetricPill label="Actual VU" value={runStatus === "RESERVED" || runStatus === "QUEUED" ? "Not started" : formatNumber(totalCurrent)} tone="info" />
        <MetricPill label="Delta VU" value={runStatus === "RESERVED" || runStatus === "QUEUED" ? "No samples" : `${delta >= 0 ? '+' : ''}${formatNumber(delta)}`} tone={Math.abs(delta) > Math.max(60, totalTarget * 0.05) ? 'warning' : 'default'} />
      </div>

      </details>
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
              allowDecimals={false}
              domain={[0, dataMax => Math.max(1, Number(dataMax), totalCurrent)]}
              tickFormatter={(value) => `${formatNumber(Number(value))} VU`}
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
              isAnimationActive={false}
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
  targetTransitionMode,
  targetRampDuration,
  onTargetTransitionModeChange,
  onTargetRampDurationChange,
  onTargetChange,
  onApplyTarget,
  onOpenGroupSettings,
}: {
  groups: CommandGroup[]
  compact: boolean
  paired?: boolean
  readOnly?: boolean
  targetTransitionMode: TargetTransitionMode
  targetRampDuration: string
  onTargetTransitionModeChange: (value: TargetTransitionMode) => void
  onTargetRampDurationChange: (value: string) => void
  onTargetChange: (groupName: string, value: number) => void
  onApplyTarget: (groupName: string, value: number) => void
  onOpenGroupSettings?: (groupName: string, kind: GroupSettingsDialogKind) => void
}) {
  const [groupMenu, setGroupMenu] = useState<{ group: string; x: number; y: number } | null>(null)
  const groupMenuRef = useRef<HTMLDivElement | null>(null)
  const firstMenuItemRef = useRef<HTMLButtonElement | null>(null)
  const groupMenuAnchorRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (!groupMenu) return undefined
    firstMenuItemRef.current?.focus()
    const dismissOnOutsidePress = (event: MouseEvent) => {
      if (groupMenuRef.current && event.target instanceof Node && !groupMenuRef.current.contains(event.target)) {
        setGroupMenu(null)
      }
    }
    const dismissOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setGroupMenu(null)
        // Escape is a keyboard dismissal: return focus to the row that opened
        // the menu (outside clicks intentionally keep the clicked target).
        if (groupMenuAnchorRef.current?.isConnected) groupMenuAnchorRef.current.focus()
      }
    }
    const dismissOnViewportChange = () => setGroupMenu(null)
    window.addEventListener('mousedown', dismissOnOutsidePress)
    window.addEventListener('keydown', dismissOnEscape)
    window.addEventListener('resize', dismissOnViewportChange)
    // Capture-phase so scrolls inside nested containers also dismiss the
    // fixed-position menu before it drifts off its row.
    window.addEventListener('scroll', dismissOnViewportChange, true)
    return () => {
      window.removeEventListener('mousedown', dismissOnOutsidePress)
      window.removeEventListener('keydown', dismissOnEscape)
      window.removeEventListener('resize', dismissOnViewportChange)
      window.removeEventListener('scroll', dismissOnViewportChange, true)
    }
  }, [groupMenu])

  const openGroupMenu = (groupName: string, x: number, y: number, anchor: HTMLElement | null) => {
    if (!onOpenGroupSettings) return
    groupMenuAnchorRef.current = anchor
    const viewportPadding = 8
    const maxX = Math.max(viewportPadding, window.innerWidth - 220)
    const maxY = Math.max(viewportPadding, window.innerHeight - 120)
    setGroupMenu({
      group: groupName,
      x: Math.min(Math.max(viewportPadding, x), maxX),
      y: Math.min(Math.max(viewportPadding, y), maxY),
    })
  }

  const selectGroupMenuItem = (kind: GroupSettingsDialogKind) => {
    const groupName = groupMenu?.group
    setGroupMenu(null)
    if (groupName) onOpenGroupSettings?.(groupName, kind)
  }

  const handleGroupMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const items = Array.from(groupMenuRef.current?.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]') ?? [])
    if (items.length === 0) return
    const currentIndex = items.findIndex(item => item === document.activeElement)
    let nextIndex = -1
    if (event.key === 'ArrowDown') nextIndex = currentIndex < 0 ? 0 : (currentIndex + 1) % items.length
    else if (event.key === 'ArrowUp') nextIndex = currentIndex < 0 ? items.length - 1 : (currentIndex - 1 + items.length) % items.length
    else if (event.key === 'Home') nextIndex = 0
    else if (event.key === 'End') nextIndex = items.length - 1
    else if (event.key === 'Tab') {
      setGroupMenu(null)
      return
    }
    if (nextIndex >= 0) {
      event.preventDefault()
      items[nextIndex].focus()
    }
  }

  const targetMode = groups[0]?.targetMode ?? 'vusers'
  const targetHeader = targetMode === 'tps' ? 'Target TPS' : 'Target'
  const showTransitionControls = !readOnly && targetMode === 'vusers' && groups.length > 0
  const rampDurationValid = isValidDurationInput(targetRampDuration)
  const sharedVuserSliderMax = targetMode === 'vusers'
    ? Math.max(1, Math.ceil(maxVisibleVuserLoad(groups) * 1.5))
    : 1
  return (
    <section className={`command-panel command-group-panel ${compact ? 'compact' : ''} ${paired ? 'paired' : ''}`} aria-label="Group load and health">
      <div className="command-panel-header">
        <div>
          <h3>Group Load</h3>
          <p>{targetMode === 'tps' ? 'Goal targets, current VUsers, suggested load, TPS, ART, and error rate by group.' : 'Current load, staged target, TPS, ART, and error rate by group.'}</p>
        </div>
        {showTransitionControls && (
          <div className="command-group-transition-controls" aria-label="Group load transition controls">
            <div className="command-segmented-control" role="group" aria-label="Load transition mode">
              <button
                type="button"
                className={targetTransitionMode === 'immediate' ? 'active' : ''}
                aria-pressed={targetTransitionMode === 'immediate'}
                onClick={() => onTargetTransitionModeChange('immediate')}
              >
                Immediate
              </button>
              <button
                type="button"
                className={targetTransitionMode === 'gradual' ? 'active' : ''}
                aria-pressed={targetTransitionMode === 'gradual'}
                onClick={() => onTargetTransitionModeChange('gradual')}
              >
                Gradual
              </button>
            </div>
            <label className={`command-ramp-duration-control ${targetTransitionMode === 'gradual' && !rampDurationValid ? 'invalid' : ''}`}>
              <span>Ramp</span>
              <input
                type="text"
                value={targetRampDuration}
                disabled={targetTransitionMode !== 'gradual'}
                aria-invalid={targetTransitionMode === 'gradual' && !rampDurationValid}
                aria-label="Group load ramp duration"
                onChange={(event) => onTargetRampDurationChange(event.target.value)}
              />
            </label>
          </div>
        )}
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
          const indicatorCurrent = isGoalTarget ? group.tps : group.current
          const committedTarget = isGoalTarget ? group.targetTps ?? group.target : group.targetVusers
          const sliderMax = isGoalTarget
            ? Math.max(100, Math.ceil(Math.max(group.target, indicatorCurrent, committedTarget) * 2))
            : Math.min(maxVUsersForRole(group.role), sharedVuserSliderMax)
          const sliderMin = isGoalTarget ? 0.1 : 0
          const applyDisabled = (isGoalTarget && group.target <= 0) || (!isGoalTarget && targetTransitionMode === 'gradual' && !rampDurationValid)
          const guardrail = isGoalTarget && (group.minVusers !== undefined || group.maxVusers !== undefined)
            ? `guardrail ${group.minVusers ?? 0}-${group.maxVusers ?? 'unbounded'} VU`
            : ''
          const suggested = isGoalTarget && group.suggestedVusers !== undefined ? `suggested ${formatNumber(group.suggestedVusers)} VU` : ''
          const goalContext = [group.protocol, suggested, guardrail].filter(Boolean).join(' / ')
          const gapText = isGoalTarget && group.throughputGapPct !== undefined ? `gap ${formatPercent(group.throughputGapPct)}` : ''
          return (
            <div
              key={group.key}
              className={`command-group-row ${isGoalTarget ? 'goal-target' : ''}`}
              role="row"
              tabIndex={onOpenGroupSettings ? 0 : undefined}
              onContextMenu={onOpenGroupSettings ? (event) => {
                event.preventDefault()
                openGroupMenu(group.name, event.clientX, event.clientY, event.currentTarget.querySelector<HTMLElement>('.command-group-name'))
              } : undefined}
              onKeyDown={onOpenGroupSettings ? (event) => {
                if (event.target !== event.currentTarget) return
                if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
                  event.preventDefault()
                  const rect = event.currentTarget.getBoundingClientRect()
                  openGroupMenu(group.name, rect.left + 16, rect.bottom + 4, event.currentTarget)
                }
              } : undefined}
            >
              <span role="cell" className="command-group-name-cell">
                {onOpenGroupSettings ? (
                  <button
                    type="button"
                    className="command-group-name"
                    aria-label={`${group.name} group actions`}
                    aria-haspopup="menu"
                    onKeyDown={(event) => {
                      if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) {
                        event.preventDefault()
                        const rect = event.currentTarget.getBoundingClientRect()
                        openGroupMenu(group.name, rect.left + 16, rect.bottom + 4, event.currentTarget)
                      }
                    }}
                    onClick={(event) => {
                      const rect = event.currentTarget.getBoundingClientRect()
                      openGroupMenu(group.name, rect.left + 16, rect.bottom + 4, event.currentTarget)
                    }}
                  >
                    <i style={{ background: group.color }} />
                    <span>
                      <strong>{group.name} <span aria-hidden="true">⌄</span></strong>
                      <small>{goalContext || group.protocol}</small>
                    </span>
                  </button>
                ) : (
                  <span className="command-group-name">
                    <i style={{ background: group.color }} />
                    <span>
                      <strong>{group.name}</strong>
                      <small>{goalContext || group.protocol}</small>
                    </span>
                  </span>
                )}
              </span>
              <span role="cell" data-label="Current VU">{formatNumber(group.current)}</span>
              <span role="cell" className="command-target-cell" data-label={isGoalTarget ? "Target TPS" : "Target VU"}>
                {readOnly ? (
                  <span className="command-target-readout">
                    <strong>{formatTargetValue(group.target, group.targetMode)}</strong>
                    <small>read only</small>
                  </span>
                ) : (
                  <>
                    <input
                      type="number"
                      min={sliderMin}
                      max={isGoalTarget ? 100000 : maxVUsersForRole(group.role)}
                      step={isGoalTarget ? 0.1 : 1}
                      aria-label={`${group.name} target ${isGoalTarget ? 'TPS' : 'VUsers'}`}
                      value={group.target}
                      onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
                    />
                    <div className="command-target-slider-stack">
                      <input
                        type="range"
                        min={sliderMin}
                        max={sliderMax}
                        step={isGoalTarget ? 0.1 : 1}
                        aria-label={`${group.name} target ${isGoalTarget ? 'TPS' : 'VUser'} slider`}
                        value={group.target}
                        onChange={(event) => onTargetChange(group.name, Number(event.target.value))}
                      />
                      <CommandTargetIndicator
                        mode={group.targetMode}
                        min={sliderMin}
                        max={sliderMax}
                        currentValue={indicatorCurrent}
                        targetValue={group.target}
                        committedValue={committedTarget}
                        activeRamp={group.activeVuserRamp}
                      />
                    </div>
                    <button type="button" className="btn btn-sm btn-accent" disabled={applyDisabled} onClick={() => onApplyTarget(group.name, group.target)}>
                      Apply
                    </button>
                  </>
                )}
              </span>
              <span role="cell" data-label="TPS" className={group.throughputTargetMissed ? 'warning' : undefined}>
                <span className="command-tps-stack">
                  <strong>{formatNumber(group.tps, 1)}</strong>
                  {isGoalTarget && <small>target {formatNumber(group.targetTps ?? group.target, 1)} TPS{gapText ? ` / ${gapText}` : ''}</small>}
                </span>
              </span>
              <span role="cell" data-label="ART">{formatNumber(group.art)} ms</span>
              <span role="cell" data-label="Error %" className={metricTone(group.errorPct, 0.75, 1.5)}>{formatPercent(group.errorPct)}</span>
            </div>
          )
        })}
      </div>
      {groupMenu && onOpenGroupSettings && createPortal(
        <div
          ref={groupMenuRef}
          className="action-parameter-menu command-group-context-menu"
          role="menu"
          tabIndex={-1}
          aria-label={`${groupMenu.group} group actions`}
          style={{ left: groupMenu.x, top: groupMenu.y }}
          onKeyDown={handleGroupMenuKeyDown}
        >
          <header className="panel-title" aria-hidden="true">{groupMenu.group}</header>
          <button
            ref={firstMenuItemRef}
            type="button"
            className="btn btn-ghost"
            role="menuitem"
            onClick={() => selectGroupMenuItem('runtime-settings')}
          >
            Runtime Settings
          </button>
          <button type="button" className="btn btn-ghost" role="menuitem" onClick={() => selectGroupMenuItem('parameters')}>
            Parameters
          </button>
        </div>,
        document.body,
      )}
    </section>
  )
}

function CommandTargetIndicator({
  mode,
  min,
  max,
  currentValue,
  targetValue,
  committedValue,
  activeRamp,
}: {
  mode: CommandGroup['targetMode']
  min: number
  max: number
  currentValue: number
  targetValue: number
  committedValue: number
  activeRamp?: VUserRampStatus
}) {
  const currentPct = loadIndicatorPercent(currentValue, min, max)
  const targetPct = loadIndicatorPercent(targetValue, min, max)
  const leftPct = Math.min(currentPct, targetPct)
  const widthPct = Math.abs(targetPct - currentPct)
  const showCommittedTarget = mode === 'vusers' && committedValue !== targetValue
  return (
    <div
      className="command-target-indicator"
      aria-label={`Current ${formatTargetValue(currentValue, mode)}, target ${formatTargetValue(targetValue, mode)}`}
      data-current-load={currentValue}
      data-target-load={targetValue}
      data-committed-load={committedValue}
      data-scale-min={min}
      data-scale-max={max}
    >
      <div className="command-target-rail" aria-hidden="true">
        <span className="command-target-segment" style={{ left: `${leftPct}%`, width: `${widthPct}%` }} />
        <span className="command-target-marker current" style={{ left: `${currentPct}%` }} />
        <span className="command-target-marker target" style={{ left: `${targetPct}%` }} />
      </div>
      <div className="command-target-legend">
        <span>Current <strong>{formatTargetValue(currentValue, mode)}</strong></span>
        <span>Target <strong>{formatTargetValue(targetValue, mode)}</strong></span>
        {activeRamp && (
          <span>Ramp <strong>{formatTargetValue(activeRamp.target_vusers, 'vusers')}</strong></span>
        )}
        {!activeRamp && showCommittedTarget && (
          <span>Committed <strong>{formatTargetValue(committedValue, mode)}</strong></span>
        )}
      </div>
    </div>
  )
}

function loadIndicatorPercent(value: number, min: number, max: number) {
  if (max <= min) return 0
  return clampPercent((value - min) / (max - min) * 100)
}

function maxVisibleVuserLoad(groups: CommandGroup[]) {
  return groups.reduce((maxValue, group) => Math.max(
    maxValue,
    finiteLoadValue(group.current),
    finiteLoadValue(group.target),
    finiteLoadValue(group.targetVusers),
    finiteLoadValue(group.activeVuserRamp?.target_vusers),
  ), 0)
}

function finiteLoadValue(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
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
  const projected = groups.reduce((sum, group) => sum + (group.targetMode === 'tps' ? group.targetVusers : group.target), 0)
  const availableGenerators = capacity?.available_generators
  const recommendedGenerators = capacity?.recommended_generators
  return (
    <details className="command-panel command-capacity-snapshot" aria-label="Capacity snapshot" open={capacity?.saturated}>
      <summary>Capacity · {capacity?.saturated ? 'Capacity pressure' : !capacity ? 'Telemetry unavailable' : 'Available'}</summary>
      <div className="command-capacity-stats">
        <MetricPill label="Allocated" value={`${formatNumber(allocated)} VU`} tone="info" />
        <MetricPill label="Projected" value={`${formatNumber(projected)} VU`} tone="accent" />
        <MetricPill label="Generators" value={`${formatOptionalNumber(recommendedGenerators)} / ${formatOptionalNumber(availableGenerators)}`} tone={capacity?.saturated ? 'danger' : 'default'} />
      </div>
    </details>
  )
}
