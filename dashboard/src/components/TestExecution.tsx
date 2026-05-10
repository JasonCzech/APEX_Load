import { useState, useEffect, useMemo, useCallback } from 'react'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, Legend, ReferenceArea,
} from 'recharts'
import { ApiError, useApi } from '../hooks/useApi'
import {
  CapacityAdvisory,
  TestSummary,
  MetricsHistoryPoint,
  TxMetrics,
  GoalStatus,
  RunArchiveReport,
  TransactionEvent,
} from '../types'
import CreateTestModal from './CreateTestModal'
import TestExecutionArchive from './TestExecutionArchive'
import TestExecutionCharts from './TestExecutionCharts'
import TestExecutionHeader from './TestExecutionHeader'
import TestExecutionLiveControls from './TestExecutionLiveControls'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import { parseDurationToSeconds, formatTimeLong, formatTimeCompact } from '../utils/timeUtils'
import { downloadBlob } from '../utils/fileUtils'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]
const RESOURCE_BASELINE_SECONDS = 5 * 60


function formatCapacityValue(value: number, decimals = 0): string {
  return new Intl.NumberFormat('en-US', {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(value)
}

function formatCapacityBottleneck(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Unknown'
  switch (advisory.bottleneck) {
    case 'cpu':
      return 'CPU bound'
    case 'memory':
      return 'Memory bound'
    case 'network':
      return 'Network bound'
    case 'vusers':
      return 'VUser ceiling'
    default:
      return 'Unknown'
  }
}

function formatAvailabilitySource(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Configured inventory'
  if (advisory.availability_source === 'configured_plus_live') {
    return advisory.live_available_generators !== undefined ? 'Live snapshot + configured ceiling' : 'Configured ceiling'
  }
  return 'Configured inventory'
}

function formatAvailabilityDetail(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Configured inventory only'
  if (advisory.availability_source === 'configured_plus_live') {
    if (advisory.live_available_generators !== undefined) {
      return `${formatCapacityValue(advisory.live_available_generators)} live / ${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured`
    }
    return `${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured ceiling`
  }
  return `${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured`
}

interface Props {
  testId: string
  history: MetricsHistoryPoint[]
  onBack: () => void
  onEditScript: (scriptId: string) => void
  onSelectTest: (testId: string) => void
}

interface ScenarioScriptDetail {
  id: string
  name: string
  protocol: string
  version?: string
  script?: {
    config?: {
      base_url?: string
      think_time?: {
        type?: string
        mean_ms?: number
        ignore?: boolean
      }
      pacing?: {
        type?: string
        delay_ms?: number
        interval_ms?: number
        tps?: number
      }
      run_logic?: {
        iterations?: number
        auto_transactions?: string
      }
      log?: {
        level?: string
      }
      error_handling?: {
        continue_on_error?: boolean
        snapshot_on_error?: boolean
        max_retries?: number
      }
      http?: {
        version?: string
        max_conns_per_host?: number
      }
    }
    data_sources?: Array<{ name?: string; type?: string; strategy?: string }>
    init?: any[]
    actions?: any[]
    teardown?: any[]
  }
}

function formatThinkTimeLabel(scriptDetail?: ScenarioScriptDetail): string {
  const thinkTime = scriptDetail?.script?.config?.think_time
  if (!thinkTime) return 'Script default'
  if (thinkTime.ignore) return 'Disabled'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatPacingLabel(scriptDetail?: ScenarioScriptDetail): string {
  const pacing = scriptDetail?.script?.config?.pacing
  if (!pacing?.type || pacing.type === 'none') return 'None'
  if (pacing.type === 'fixed_delay') return `Fixed delay · ${pacing.delay_ms || 0}ms`
  if (pacing.type === 'fixed_interval') return `Fixed interval · ${pacing.interval_ms || 0}ms`
  if (pacing.type === 'tps') return `Target TPS · ${pacing.tps || 0}`
  return pacing.type.replace(/_/g, ' ')
}

function formatEffectiveIterations(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary | null): string {
  const override = test?.runtime_settings?.iterations
  if (override !== undefined) return `${override} (scenario override)`
  const iterations = scriptDetail?.script?.config?.run_logic?.iterations
  if (!iterations) return 'Duration driven'
  return `${iterations}`
}

function formatEffectiveAutoTransactions(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary | null): string {
  const override = test?.runtime_settings?.auto_transactions
  if (override) return `${override} (scenario override)`
  return scriptDetail?.script?.config?.run_logic?.auto_transactions || 'none'
}

function formatEffectiveLogLevel(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary | null): string {
  const override = test?.runtime_settings?.log_level
  if (override) return `${override} (scenario override)`
  return scriptDetail?.script?.config?.log?.level || 'standard'
}

function formatSnapshotOnError(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary | null): string {
  const override = test?.runtime_settings?.error_handling?.snapshot_on_error
  if (override !== undefined) return override ? 'Enabled (scenario override)' : 'Disabled (scenario override)'
  return scriptDetail?.script?.config?.error_handling?.snapshot_on_error ? 'Enabled' : 'Disabled'
}

function formatGoalScaleReason(reason?: string): string {
  if (!reason) return 'Tracking target'
  return reason.replace(/_/g, ' ')
}

function describeGoalController(status?: GoalStatus): string {
  if (!status) return 'Waiting for live metrics'
  if (status.stop_triggered) return `Stopping: ${status.stop_reason || 'SLA breached'}`
  if (status.breach_active) return `SLA breach active for ${status.breach_duration_sec || 0}s`
  if (status.last_scale_direction) {
    return `Last scale ${status.last_scale_direction} due to ${formatGoalScaleReason(status.last_scale_reason)}`
  }
  return 'Tracking target'
}

function isTerminalStatus(status?: string): boolean {
  return status === 'COMPLETE' || status === 'FAILED' || status === 'ABORTED'
}

function buildScenarioDraft(test: TestSummary) {
  const groups = test.groups?.length
    ? test.groups.map((group) => ({
        name: group.name,
        scriptId: group.script_id,
        vusers: group.vusers,
        rampUp: group.ramp_up || '',
        rampDown: group.ramp_down || '',
        initialization: group.initialization || 'at_runtime',
      }))
    : [{
        name: '',
        scriptId: test.script_id || '',
        vusers: test.total_vusers || 10,
        rampUp: '',
        rampDown: '',
        initialization: 'at_runtime' as const,
      }]

  return {
    name: test.name,
    duration: test.duration,
    scenarioMode: test.scenario_mode || 'vuser_driven',
    scriptShakeout: Boolean(test.script_shakeout),
    resourceBaseline: Boolean(test.resource_baseline),
    goalConfig: {
      target_average_tps: test.goal_config?.target_average_tps,
      p95_latency_ms: test.goal_config?.p95_latency_ms,
      max_error_pct: test.goal_config?.max_error_pct,
      min_vusers: test.goal_config?.min_vusers,
      max_vusers: test.goal_config?.max_vusers,
      stop_on_sla_breach: test.goal_config?.stop_on_sla_breach,
      sla_breach_window: test.goal_config?.sla_breach_window || '',
    },
    groups,
    runtimeSettings: {
      iterations: test.runtime_settings?.iterations,
      log_level: test.runtime_settings?.log_level || '',
      auto_transactions: test.runtime_settings?.auto_transactions || '',
      snapshot_on_error: Boolean(test.runtime_settings?.error_handling?.snapshot_on_error),
    },
  }
}

export default function TestExecution({ testId, history, onBack, onEditScript, onSelectTest }: Props) {
  const api = useApi()
  const [test, setTest] = useState<TestSummary | null>(null)
  const [error, setError] = useState('')
  const [expandedChart, setExpandedChart] = useState<string | null>(null)
  const [timeRange, setTimeRange] = useState<'3m' | '10m' | 'all' | 'custom'>('all')
  const [zoomRange, setZoomRange] = useState<{ left: string | number; right: string | number } | null>(null)
  const [refAreaLeft, setRefAreaLeft] = useState<string | number>('')
  const [refAreaRight, setRefAreaRight] = useState<string | number>('')
  const [showControls, setShowControls] = useState(false)
  const [vuserTargets, setVuserTargets] = useState<Record<string, number>>({})
  const [durationInput, setDurationInput] = useState('')
  const [controlFeedback, setControlFeedback] = useState('')
  const [scriptDetails, setScriptDetails] = useState<Record<string, ScenarioScriptDetail>>({})
  const [expandedScenarioScript, setExpandedScenarioScript] = useState<string | null>(null)
  const [scriptDetailsLoading, setScriptDetailsLoading] = useState(false)
  const [scriptDetailsError, setScriptDetailsError] = useState('')
  const [showScenarioEditor, setShowScenarioEditor] = useState(false)
  const [availableScripts, setAvailableScripts] = useState<Array<{ id: string; name: string; protocol: string; version: string; created_at: string }>>([])
  const [archiveReport, setArchiveReport] = useState<RunArchiveReport | null>(null)
  const [archiveTransactions, setArchiveTransactions] = useState<TransactionEvent[]>([])
  const [archiveTransactionsTotal, setArchiveTransactionsTotal] = useState(0)
  const [archiveLoading, setArchiveLoading] = useState(false)
  const [archiveTransactionsLoading, setArchiveTransactionsLoading] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const [selectedArchiveAction, setSelectedArchiveAction] = useState('')
  const [selectedArchiveGroup, setSelectedArchiveGroup] = useState('')
  const [archiveErrorsOnly, setArchiveErrorsOnly] = useState(false)
  const [archiveTransactionsOffset, setArchiveTransactionsOffset] = useState(0)
  const [expandedArchiveEvent, setExpandedArchiveEvent] = useState<string | null>(null)
  const [startCapacityConflict, setStartCapacityConflict] = useState<CapacityAdvisory | null>(null)
  const [startFeedback, setStartFeedback] = useState('')

  useEffect(() => {
    const load = async () => {
      try {
        const data = await api.getTest(testId)
        setTest(data)
        if (data.status !== 'QUEUED') {
          setStartCapacityConflict(null)
          setStartFeedback('')
        }
      } catch (e: any) {
        setError(e.message)
      }
    }
    load()
    const interval = setInterval(load, 2000)
    return () => clearInterval(interval)
  }, [api, testId])

  useEffect(() => {
    if (!test?.groups?.length) {
      setScriptDetails({})
      setScriptDetailsError('')
      return
    }

    const uniqueScriptIds = [...new Set(test.groups.map((group) => group.script_id).filter(Boolean))]
    if (uniqueScriptIds.length === 0) return

    let cancelled = false

    const loadScriptDetails = async () => {
      setScriptDetailsLoading(true)
      setScriptDetailsError('')
      try {
        const results = await Promise.all(uniqueScriptIds.map(async (scriptId) => {
          const detail = await api.getScript(scriptId)
          return [scriptId, detail as ScenarioScriptDetail] as const
        }))
        if (cancelled) return
        setScriptDetails(Object.fromEntries(results))
      } catch (e: any) {
        if (cancelled) return
        setScriptDetailsError(e.message || 'Unable to load script details')
      } finally {
        if (!cancelled) setScriptDetailsLoading(false)
      }
    }

    loadScriptDetails()

    return () => {
      cancelled = true
    }
  }, [api, test])

  useEffect(() => {
    if (!showScenarioEditor) return
    let cancelled = false

    const loadScripts = async () => {
      try {
        const response = await api.listScripts()
        if (!cancelled) {
          setAvailableScripts(response.scripts || [])
        }
      } catch {
        if (!cancelled) {
          setAvailableScripts([])
        }
      }
    }

    loadScripts()
    return () => {
      cancelled = true
    }
  }, [api, showScenarioEditor])

  useEffect(() => {
    if (!test?.archive || !isTerminalStatus(test.status)) {
      setArchiveReport(null)
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      setArchiveError('')
      setSelectedArchiveAction('')
      setSelectedArchiveGroup('')
      setArchiveErrorsOnly(false)
      setArchiveTransactionsOffset(0)
      setExpandedArchiveEvent(null)
      return
    }

    let cancelled = false
    const loadArchiveReport = async () => {
      setArchiveLoading(true)
      setArchiveError('')
      try {
        const report = await api.getTestArchiveReport(test.id)
        if (cancelled) return
        setArchiveReport(report)
        const actionNames = Object.keys(report.by_action || {})
        setSelectedArchiveAction((current) => current || actionNames[0] || '')
        setArchiveTransactionsOffset(0)
      } catch (e: any) {
        if (cancelled) return
        setArchiveReport(null)
        setArchiveError(e.message || 'Unable to load archive report')
      } finally {
        if (!cancelled) setArchiveLoading(false)
      }
    }

    loadArchiveReport()

    return () => {
      cancelled = true
    }
  }, [api, test])

  useEffect(() => {
    if (!test?.archive || !isTerminalStatus(test.status) || !selectedArchiveAction) {
      setArchiveTransactions([])
      setArchiveTransactionsTotal(0)
      return
    }

    let cancelled = false
    const loadArchiveTransactions = async () => {
      setArchiveTransactionsLoading(true)
      try {
        const response = await api.getTestArchiveTransactions(test.id, {
          action: selectedArchiveAction,
          group: selectedArchiveGroup || undefined,
          success: archiveErrorsOnly ? false : undefined,
          limit: 25,
          offset: archiveTransactionsOffset,
        })
        if (!cancelled) {
          setArchiveTransactions(response.transactions || [])
          setArchiveTransactionsTotal(response.total || 0)
          setExpandedArchiveEvent(null)
        }
      } catch (e: any) {
        if (!cancelled) {
          setArchiveTransactions([])
          setArchiveTransactionsTotal(0)
          setArchiveError(e.message || 'Unable to load archive transactions')
        }
      } finally {
        if (!cancelled) setArchiveTransactionsLoading(false)
      }
    }

    loadArchiveTransactions()

    return () => {
      cancelled = true
    }
    }, [api, archiveErrorsOnly, archiveTransactionsOffset, selectedArchiveAction, selectedArchiveGroup, test])

  const isRunning = test?.status === 'RUNNING' || test?.status === 'INITIALIZING'
  const hasArchive = Boolean(test?.archive)

  // Initialize vuser targets when test loads
  useEffect(() => {
    if (test?.groups && Object.keys(vuserTargets).length === 0) {
      const targets: Record<string, number> = {}
      test.groups.forEach((g: any) => { targets[g.name] = g.vusers })
      setVuserTargets(targets)
      setDurationInput(test.duration || '')
    }
  }, [test?.groups])

  const handleScaleVUsers = useCallback(async (groupName: string) => {
    const target = vuserTargets[groupName]
    if (target === undefined || target < 0) return
    try {
      await api.scaleVUsers(testId, groupName, target)
      setControlFeedback(`Scaled ${groupName} to ${target} VUsers`)
      setTimeout(() => setControlFeedback(''), 3000)
    } catch (e: any) {
      setControlFeedback(`Error: ${e.message}`)
    }
  }, [api, testId, vuserTargets])

  const handleAdjustDuration = useCallback(async () => {
    if (!durationInput) return
    try {
      await api.adjustDuration(testId, durationInput)
      setControlFeedback(`Duration adjusted to ${durationInput}`)
      setTimeout(() => setControlFeedback(''), 3000)
    } catch (e: any) {
      setControlFeedback(`Error: ${e.message}`)
    }
  }, [api, testId, durationInput])
  
  // If we don't have WebSocket history (completed test), use backend history
  const chartData = useMemo(() => {
    if (history.length > 0) return history
    if (!test?.history) return []
    return (test.history as any[]).map((m: any) => {
      const p: MetricsHistoryPoint = {
        time: new Date(m.timestamp).toLocaleTimeString(),
        vusers: m.active_vusers,
        tps: m.tps,
        avgMs: m.avg_response_ms,
        errorPct: m.error_pct || 0,
        p50: m.p50_ms,
        p90: m.p90_ms,
        p99: m.p99_ms,
        cpuProcess: m.resources?.cpu_process_pct,
        cpuFree: m.resources?.cpu_free_pct,
        memProcess: m.resources?.mem_process_mb,
        memFree: m.resources?.mem_free_mb,
        netSent: m.resources?.net_sent_kbps,
        netRecv: m.resources?.net_recv_kbps,
        diskRead: m.resources?.disk_read_kbps,
        diskWrite: m.resources?.disk_write_kbps,
      }
      if (m.groups) {
        Object.entries(m.groups).forEach(([name, gm]: [string, any]) => {
          p[`${name}_tps`] = gm.tps
          p[`${name}_avgMs`] = gm.avg_response_ms
          p[`${name}_vusers`] = gm.vusers
        })
      }
      return p
    })
  }, [history, test?.history])

  const filteredData = useMemo(() => {
    if (timeRange === 'custom' && zoomRange) {
      const left = chartData.findIndex((d: MetricsHistoryPoint) => d.time === zoomRange.left)
      const right = chartData.findIndex((d: MetricsHistoryPoint) => d.time === zoomRange.right)
      if (left !== -1 && right !== -1) {
        return chartData.slice(Math.min(left, right), Math.max(left, right) + 1)
      }
    }
    if (timeRange === 'all') return chartData
    const seconds = timeRange === '3m' ? 180 : 600
    return chartData.slice(-seconds)
  }, [chartData, timeRange, zoomRange])

  const summary = useMemo(() => {
    if (filteredData.length === 0) return null
    const sum = filteredData.reduce((acc: { tps: number; avgMs: number; errorPct: number; vusers: number }, d: MetricsHistoryPoint) => ({
      tps: acc.tps + d.tps,
      avgMs: acc.avgMs + d.avgMs,
      errorPct: acc.errorPct + d.errorPct,
      vusers: Math.max(acc.vusers, d.vusers)
    }), { tps: 0, avgMs: 0, errorPct: 0, vusers: 0 })
    
    return {
      avgTps: sum.tps / filteredData.length,
      avgLatency: sum.avgMs / filteredData.length,
      avgError: sum.errorPct / filteredData.length,
      maxVusers: sum.vusers
    }
  }, [filteredData])

  const handleZoom = () => {
    if (refAreaLeft === refAreaRight || refAreaRight === '') {
      setRefAreaLeft('')
      setRefAreaRight('')
      return
    }

    setTimeRange('custom')
    setZoomRange({ left: refAreaLeft as number, right: refAreaRight as number })
    setRefAreaLeft('')
    setRefAreaRight('')
  }

  const handleChartMouseDown = (activeLabel?: string | number) => {
    if (activeLabel === undefined) return
    setRefAreaLeft(activeLabel)
    setRefAreaRight(activeLabel)
  }

  const handleChartMouseMove = (activeLabel?: string | number) => {
    if (activeLabel === undefined || refAreaLeft === '') return
    setRefAreaRight(activeLabel)
  }

  const showRefArea = refAreaLeft !== ''
  const refAreaEnd = refAreaRight === '' ? refAreaLeft : refAreaRight

  const liveControlPreview = useMemo(() => {
    if (!test?.groups?.length) {
      return {
        points: [],
        currentTotal: 0,
        projectedTotal: 0,
        remainingSeconds: 0,
        projectedDurationSeconds: 0,
        hasPendingChanges: false,
      }
    }

    const groupMetricsMap = test.live_metrics?.groups ?? {}
    const startedAt = test.started_at || test.start_time
    const elapsedSeconds = startedAt
      ? Math.max(0, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000))
      : 0
    const projectedDurationSeconds = parseDurationToSeconds(durationInput || test.duration)
    const remainingSeconds = Math.max(projectedDurationSeconds - elapsedSeconds, 0)
    const transitionSeconds = remainingSeconds > 0 ? Math.min(Math.max(Math.round(remainingSeconds * 0.06), 1), 30) : 1

    const currentPoint: Record<string, number> = { second: 0, total: 0 }
    const projectedPoint: Record<string, number> = { second: transitionSeconds, total: 0 }
    const steadyPoint: Record<string, number> = { second: Math.max(transitionSeconds, remainingSeconds), total: 0 }
    const endPoint: Record<string, number> = { second: Math.max(transitionSeconds + 1, remainingSeconds + 1), total: 0 }

    let hasPendingChanges = durationInput !== '' && durationInput !== test.duration

    test.groups.forEach((group, index) => {
      const dataKey = `group${index}`
      const liveGroup = groupMetricsMap[group.name]
      const currentValue = liveGroup?.vusers ?? group.vusers
      const projectedValue = vuserTargets[group.name] ?? group.vusers

      currentPoint[dataKey] = currentValue
      projectedPoint[dataKey] = projectedValue
      steadyPoint[dataKey] = projectedValue
      endPoint[dataKey] = 0

      currentPoint.total += currentValue
      projectedPoint.total += projectedValue
      steadyPoint.total += projectedValue

      if (projectedValue !== group.vusers) {
        hasPendingChanges = true
      }
    })

    return {
      points: [currentPoint, projectedPoint, steadyPoint, endPoint],
      currentTotal: currentPoint.total,
      projectedTotal: projectedPoint.total,
      remainingSeconds,
      projectedDurationSeconds,
      hasPendingChanges,
    }
  }, [durationInput, test, vuserTargets])

  const queuedScenarioPreview = useMemo(() => {
    if (!test?.groups?.length) {
      return {
        points: [],
        totalVUsers: 0,
        totalSeconds: 0,
        steadySeconds: 0,
        preBaselineSeconds: 0,
        postBaselineSeconds: 0,
      }
    }

    const steadySeconds = parseDurationToSeconds(test.duration)
    const preBaselineSeconds = test.resource_baseline ? RESOURCE_BASELINE_SECONDS : 0
    const postBaselineSeconds = test.resource_baseline ? RESOURCE_BASELINE_SECONDS : 0
    let maxRampDown = 0
    test.groups.forEach((group) => {
      const rampDown = parseDurationToSeconds(group.ramp_down)
      if (rampDown > maxRampDown) maxRampDown = rampDown
    })

	    const totalSeconds = preBaselineSeconds + steadySeconds + maxRampDown + postBaselineSeconds
    if (totalSeconds <= 0) {
      return {
        points: [],
        totalVUsers: test.total_vusers,
        totalSeconds: 0,
        steadySeconds,
        preBaselineSeconds,
        postBaselineSeconds,
      }
    }

    const step = Math.max(1, Math.floor(totalSeconds / 30))
    const points: Array<Record<string, number>> = []

    for (let second = 0; second <= totalSeconds; second += step) {
      const point: Record<string, number> = { second, total: 0 }
      test.groups.forEach((group, index) => {
        const rampUp = parseDurationToSeconds(group.ramp_up)
        const rampDown = parseDurationToSeconds(group.ramp_down)
        let value = 0

        const loadSecond = Math.max(0, second - preBaselineSeconds)

        if (second < preBaselineSeconds) {
          value = 0
        } else if (loadSecond < rampUp) {
          value = rampUp > 0 ? (loadSecond / rampUp) * group.vusers : group.vusers
        } else if (loadSecond < steadySeconds) {
          value = group.vusers
        } else if (loadSecond < steadySeconds + rampDown) {
          value = rampDown > 0 ? (1 - (loadSecond - steadySeconds) / rampDown) * group.vusers : 0
        }

        const roundedValue = Math.round(Math.max(0, value))
        point[`group${index}`] = roundedValue
        point.total += roundedValue
      })
      points.push(point)
    }

    const lastPoint = points[points.length - 1]
    if (!lastPoint || lastPoint.second < totalSeconds) {
      const finalPoint: Record<string, number> = { second: totalSeconds, total: 0 }
      test.groups.forEach((_, index) => {
        finalPoint[`group${index}`] = 0
      })
      points.push(finalPoint)
    }

    return {
      points,
      totalVUsers: test.total_vusers,
      totalSeconds,
      steadySeconds,
        preBaselineSeconds,
        postBaselineSeconds,
    }
  }, [test])

  const queuedScenarioRows = useMemo(() => {
    if (!test?.groups?.length) return []
    return test.groups.map((group) => {
      const detail = scriptDetails[group.script_id]
      return {
        group,
        detail,
        actionCount: detail?.script?.actions?.length || 0,
        initCount: detail?.script?.init?.length || 0,
        teardownCount: detail?.script?.teardown?.length || 0,
        dataSourceCount: detail?.script?.data_sources?.length || 0,
      }
    })
  }, [scriptDetails, test])

  const metrics = test?.live_metrics
  const goalStatus = metrics?.goal_status
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []
  const isMultiGroup = (test?.groups?.length || 0) > 1
  const groupNames = test?.groups?.map((g) => g.name) || []
  const isGoalOriented = test?.scenario_mode === 'goal_oriented'
  const shakeoutFailed = test?.status === 'FAILED' && Boolean(test.script_shakeout) && test.error?.toLowerCase().includes('shakeout')
  const archiveActionEntries = archiveReport ? Object.entries(archiveReport.by_action || {}).sort((a, b) => b[1].transactions - a[1].transactions) : []
  const archiveGroups = test?.groups?.map((group) => group.name) || []
  const archivePageSize = 25
  const archivePageStart = archiveTransactionsTotal === 0 ? 0 : archiveTransactionsOffset + 1
  const archivePageEnd = Math.min(archiveTransactionsOffset + archiveTransactions.length, archiveTransactionsTotal)

  const handleDownloadArchive = useCallback(async () => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchive(test.id)
      downloadBlob(blob, `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}.zip`)
    } catch (e: any) {
      setArchiveError(e.message || 'Unable to download archive bundle')
    }
  }, [api, test])

  const handleDownloadArchiveArtifact = useCallback(async (artifactName: string) => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchiveArtifact(test.id, artifactName)
      downloadBlob(blob, `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}-${artifactName}`)
    } catch (e: any) {
      setArchiveError(e.message || `Unable to download ${artifactName}`)
    }
  }, [api, test])

  const handleStart = useCallback(async (override = false) => {
    setStartFeedback('')
    try {
      await api.startTest(testId, override ? { override_capacity_shortfall: true } : undefined)
      setStartCapacityConflict(null)
    } catch (e: any) {
      if (e instanceof ApiError && e.code === 'capacity_shortfall') {
        setStartCapacityConflict(e.payload?.capacity_advisory || test?.capacity_advisory || null)
        setStartFeedback(e.message || 'Capacity advisory requires an explicit override before starting.')
        return
      }
      setStartFeedback(e.message || 'Unable to start test')
    }
  }, [api, test?.capacity_advisory, testId])

  if (error) {
    return (
      <div className="execution">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <p className="error-msg">{error}</p>
      </div>
    )
  }

  if (!test) {
    return <div className="execution"><p className="loading">Loading...</p></div>
  }

  return (
    <div className="execution">
      <TestExecutionHeader
        test={test}
        isMultiGroup={isMultiGroup}
        timeRange={timeRange}
        onTimeRangeChange={setTimeRange}
        onBack={onBack}
        onOpenScenarioEditor={() => setShowScenarioEditor(true)}
        onStart={() => handleStart()}
        isRunning={isRunning}
        onStop={() => api.stopTest(testId)}
        onAbort={() => api.abortTest(testId)}
        hasArchive={hasArchive}
        onDownloadArchive={handleDownloadArchive}
      />

      {/* Live Controls Panel */}
      {isRunning && (
        <TestExecutionLiveControls
          showControls={showControls}
          onToggleControls={() => setShowControls(!showControls)}
          test={test}
          vuserTargets={vuserTargets}
          onVuserTargetChange={(groupName, value) => setVuserTargets((prev) => ({ ...prev, [groupName]: value }))}
          onScaleVUsers={handleScaleVUsers}
          durationInput={durationInput}
          onDurationInputChange={setDurationInput}
          onAdjustDuration={handleAdjustDuration}
          liveControlPreview={liveControlPreview}
          controlFeedback={controlFeedback}
          groupColors={GROUP_COLORS}
        />
      )}

      {/* Group config summary */}
      {isMultiGroup && (
        <div className="group-config-bar">
          {test.groups.map((g, i) => (
            <div key={g.name} className="group-chip" style={{ borderLeftColor: GROUP_COLORS[i % GROUP_COLORS.length] }}>
              <span className="group-chip-name">{g.name}</span>
              <span className="group-chip-detail">{g.script_name} · {g.vusers} VUsers</span>
            </div>
          ))}
        </div>
      )}

      {shakeoutFailed && (
        <div className="scenario-alert-banner" role="alert">
          <div>
            <h3 className="scenario-alert-title">Script shakeout failed</h3>
            <p className="scenario-alert-message">{test.error}</p>
          </div>
        </div>
      )}

      {test.status === 'QUEUED' && startCapacityConflict && (
        <div className="scenario-alert-banner scenario-alert-warning" role="alert">
          <div>
            <h3 className="scenario-alert-title">Additional load generators recommended before start</h3>
            <p className="scenario-alert-message">
              This scenario is estimated to need {startCapacityConflict.recommended_generators} load generators, but only {startCapacityConflict.available_generators} are currently available. Bottleneck: {formatCapacityBottleneck(startCapacityConflict)}.
            </p>
            <p className="scenario-alert-message secondary">
              Availability source: {formatAvailabilitySource(startCapacityConflict)}. Snapshot: {formatAvailabilityDetail(startCapacityConflict)}.
            </p>
            {startFeedback && <p className="scenario-alert-message secondary">{startFeedback}</p>}
          </div>
          <div className="scenario-alert-actions">
            <button className="btn btn-secondary" onClick={() => setStartCapacityConflict(null)}>
              Cancel
            </button>
            <button className="btn btn-warning" onClick={() => handleStart(true)}>
              Start Anyway
            </button>
          </div>
        </div>
      )}

      {test.status === 'QUEUED' && queuedScenarioPreview.points.length > 0 && (
        <div className="scenario-preview-card">
          <div className="scenario-preview-header">
            <div>
              <h3 className="scenario-preview-title">Scenario Run Model</h3>
              <p className="scenario-preview-subtitle">This is the saved load shape that will run when you start the scenario.</p>
            </div>
            <div className="scenario-preview-stats">
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Peak</span>
                <span className="scenario-preview-stat-value">{queuedScenarioPreview.totalVUsers} VUsers</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Steady State</span>
                <span className="scenario-preview-stat-value">{formatTimeCompact(queuedScenarioPreview.steadySeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Total Timeline</span>
                <span className="scenario-preview-stat-value">{formatTimeCompact(queuedScenarioPreview.totalSeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Baseline</span>
                <span className="scenario-preview-stat-value">{test.resource_baseline ? '5m pre/post' : 'Disabled'}</span>
              </div>
            </div>
          </div>

          {test.capacity_advisory && (
            <div className={`capacity-advisory-card ${test.capacity_advisory.requires_override ? 'warning' : 'ready'}`}>
              <div className="capacity-advisory-header">
                <div>
                  <h3 className="capacity-advisory-title">Load Generator Advisory</h3>
                  <p className="capacity-advisory-subtitle">
                    Estimated from script complexity, generator baseline resources, {formatCapacityValue(test.capacity_advisory.headroom_pct)}% reserved headroom, and {formatAvailabilitySource(test.capacity_advisory).toLowerCase()}.
                  </p>
                </div>
                <span className={`capacity-advisory-badge ${test.capacity_advisory.requires_override ? 'warning' : 'ready'}`}>
                  {test.capacity_advisory.requires_override ? 'Capacity Shortfall' : 'Capacity Ready'}
                </span>
              </div>

              <div className="capacity-advisory-grid">
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Recommended</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.recommended_generators)}</span>
                  <span className="capacity-advisory-detail">load generators</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Available</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.available_generators)}</span>
                  <span className="capacity-advisory-detail">{formatAvailabilityDetail(test.capacity_advisory)}</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Bottleneck</span>
                  <span className="capacity-advisory-value text">{formatCapacityBottleneck(test.capacity_advisory)}</span>
                  <span className="capacity-advisory-detail">primary sizing driver</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Usable Per Generator</span>
                  <span className="capacity-advisory-value text">{formatCapacityValue(test.capacity_advisory.usable_per_generator.cpu_cores, 1)} CPU / {formatCapacityValue(test.capacity_advisory.usable_per_generator.memory_mb)} MB</span>
                  <span className="capacity-advisory-detail">after reserved headroom</span>
                </div>
              </div>

              <div className="capacity-advisory-grid compact">
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total CPU</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.cpu_cores, 2)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total Memory</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.memory_mb)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total Network</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.network_mbps)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Availability Source</span>
                  <span className="capacity-advisory-value text">{formatAvailabilitySource(test.capacity_advisory)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Complexity</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.complexity_score, 2)}</span>
                </div>
              </div>

              {test.capacity_advisory.warnings && test.capacity_advisory.warnings.length > 0 && (
                <div className="capacity-advisory-warnings">
                  {test.capacity_advisory.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className="scenario-preview-chart">
            <ResponsiveContainer width="100%" height={260}>
              <AreaChart data={queuedScenarioPreview.points} margin={{ top: 8, right: 12, left: -18, bottom: 4 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis
                  dataKey="second"
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  tickFormatter={(value) => formatTimeCompact(Number(value))}
                />
                <YAxis
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  allowDecimals={false}
                />
                <Tooltip
                  labelFormatter={(value) => `T+${formatTimeCompact(Number(value))}`}
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.92)', border: '1px solid var(--border)', borderRadius: 10 }}
                  formatter={(value: number, name: string) => [value, name === 'total' ? 'Total' : name]}
                />
                <Legend verticalAlign="top" height={32} />
                {test.groups.map((group, index) => (
                  <Area
                    key={group.name}
                    type="monotone"
                    dataKey={`group${index}`}
                    name={group.name}
                    stackId="scenario"
                    stroke={GROUP_COLORS[index % GROUP_COLORS.length]}
                    fill={GROUP_COLORS[index % GROUP_COLORS.length]}
                    fillOpacity={0.34}
                    isAnimationActive={false}
                  />
                ))}
                <Line
                  type="monotone"
                  dataKey="total"
                  name="Total"
                  stroke="var(--text-primary)"
                  strokeWidth={2}
                  dot={false}
                  isAnimationActive={false}
                />
                <ReferenceArea
                  x1={queuedScenarioPreview.preBaselineSeconds}
                  x2={queuedScenarioPreview.preBaselineSeconds + queuedScenarioPreview.steadySeconds}
                  fill="var(--accent)"
                  fillOpacity={0.05}
                  strokeOpacity={0}
                />
                {queuedScenarioPreview.preBaselineSeconds > 0 && (
                  <ReferenceArea
                    x1={0}
                    x2={queuedScenarioPreview.preBaselineSeconds}
                    fill="var(--warning)"
                    fillOpacity={0.05}
                    strokeOpacity={0}
                  />
                )}
                {queuedScenarioPreview.postBaselineSeconds > 0 && (
                  <ReferenceArea
                    x1={queuedScenarioPreview.totalSeconds - queuedScenarioPreview.postBaselineSeconds}
                    x2={queuedScenarioPreview.totalSeconds}
                    fill="var(--warning)"
                    fillOpacity={0.05}
                    strokeOpacity={0}
                  />
                )}
              </AreaChart>
            </ResponsiveContainer>
            <div className="scenario-preview-caption">
              Ramp-up and ramp-down are derived from each group’s saved configuration. The blue span marks steady-state load, and the amber spans mark the optional zero-load resource baseline windows.
            </div>
          </div>

          <div className="scenario-settings-grid">
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Duration</span>
              <span className="scenario-setting-value">{test.duration}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Iterations</span>
              <span className="scenario-setting-value">{test.runtime_settings?.iterations ?? 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Log Level</span>
              <span className="scenario-setting-value">{test.runtime_settings?.log_level || 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Auto Transactions</span>
              <span className="scenario-setting-value">{test.runtime_settings?.auto_transactions || 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Snapshot On Error</span>
              <span className="scenario-setting-value">{test.runtime_settings?.error_handling?.snapshot_on_error ? 'Enabled' : 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Script Shakeout</span>
              <span className="scenario-setting-value">{test.script_shakeout ? 'Enabled' : 'Disabled'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Resource Baseline</span>
              <span className="scenario-setting-value">{test.resource_baseline ? '5m before and after' : 'Disabled'}</span>
            </div>
          </div>

          <div className="scenario-script-section">
            <div className="scenario-script-section-header">
              <div>
                <h4 className="scenario-script-section-title">Scenario Scripts</h4>
                <p className="scenario-script-section-subtitle">Each row is a script group in this scenario. Expand a row to inspect the effective script configuration that will run.</p>
              </div>
            </div>

            {scriptDetailsLoading && <p className="scenario-script-status">Loading script settings...</p>}
            {scriptDetailsError && <p className="scenario-script-status error">{scriptDetailsError}</p>}

            <div className="scenario-script-rows">
              {queuedScenarioRows.map(({ group, detail, actionCount, initCount, teardownCount, dataSourceCount }, index) => {
                const isExpanded = expandedScenarioScript === group.name
                return (
                  <div key={`${group.name}-${group.script_id}`} className={`scenario-script-row ${isExpanded ? 'expanded' : ''}`}>
                    <button
                      type="button"
                      className="scenario-script-summary"
                      onClick={() => setExpandedScenarioScript(isExpanded ? null : group.name)}
                    >
                      <span className="scenario-script-color" style={{ background: GROUP_COLORS[index % GROUP_COLORS.length] }}></span>
                      <span className="scenario-script-cell scenario-script-name-cell">
                        <span className="scenario-script-primary">{group.name}</span>
                        <span className="scenario-script-secondary">{group.script_name}</span>
                      </span>
                      <span className="scenario-script-cell">{group.protocol.toUpperCase()}</span>
                      <span className="scenario-script-cell">{group.vusers} VUsers</span>
                      <span className="scenario-script-cell">{group.ramp_up || 'No ramp-up'}</span>
                      <span className="scenario-script-cell">{group.ramp_down || 'No ramp-down'}</span>
                      <span className="scenario-script-cell">{group.initialization || 'at_runtime'}</span>
                      <span className="scenario-script-actions">
                        <button
                          type="button"
                          className="btn btn-sm btn-ghost scenario-script-edit-btn"
                          onClick={(event) => {
                            event.stopPropagation()
                            onEditScript(group.script_id)
                          }}
                        >
                          Edit Script
                        </button>
                        <span className="scenario-script-toggle">{isExpanded ? 'Hide' : 'Inspect'}</span>
                      </span>
                    </button>

                    {isExpanded && (
                      <div className="scenario-script-detail-panel">
                        <div className="scenario-script-detail-grid">
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Base URL</span>
                            <span className="scenario-script-detail-value">{detail?.script?.config?.base_url || 'Not configured'}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Think Time</span>
                            <span className="scenario-script-detail-value">{formatThinkTimeLabel(detail)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Pacing</span>
                            <span className="scenario-script-detail-value">{formatPacingLabel(detail)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Iterations</span>
                            <span className="scenario-script-detail-value">{formatEffectiveIterations(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Auto Transactions</span>
                            <span className="scenario-script-detail-value">{formatEffectiveAutoTransactions(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Log Level</span>
                            <span className="scenario-script-detail-value">{formatEffectiveLogLevel(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Snapshot On Error</span>
                            <span className="scenario-script-detail-value">{formatSnapshotOnError(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">HTTP Runtime</span>
                            <span className="scenario-script-detail-value">{detail?.script?.config?.http?.version || 'auto'} · {detail?.script?.config?.http?.max_conns_per_host || 100} conns/host</span>
                          </div>
                        </div>

                        <div className="scenario-script-asset-grid">
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Actions</span>
                            <span className="scenario-script-detail-value">{actionCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Init</span>
                            <span className="scenario-script-detail-value">{initCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Teardown</span>
                            <span className="scenario-script-detail-value">{teardownCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Data Sources</span>
                            <span className="scenario-script-detail-value">{dataSourceCount}</span>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      )}

      {/* Hero metrics */}
      {showScenarioEditor && test && (
        <CreateTestModal
          scripts={availableScripts}
          initialScenario={buildScenarioDraft(test)}
          onClose={() => setShowScenarioEditor(false)}
          onCreated={(createdTest) => {
            setShowScenarioEditor(false)
            if (createdTest?.id) {
              onSelectTest(createdTest.id)
            }
          }}
        />
      )}

      <div className="hero-metrics">
        <div className="hero-card">
          <span className="hero-value">{archiveReport?.overview.peak_active_vusers ?? summary?.maxVusers ?? metrics?.active_vusers ?? 0}</span>
          <span className="hero-label">Max VUsers</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{(archiveReport?.overview.peak_tps ?? summary?.avgTps ?? metrics?.tps ?? 0).toFixed(1)}</span>
          <span className="hero-label">Avg TPS</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{(archiveReport?.overview.avg_response_ms ?? summary?.avgLatency ?? metrics?.avg_response_ms ?? 0).toFixed(0)}</span>
          <span className="hero-label">Avg Latency (ms)</span>
        </div>
        <div className="hero-card">
          <span className="hero-value error-value">
            {(archiveReport?.overview.error_pct ?? summary?.avgError ?? metrics?.error_pct ?? 0).toFixed(2)}%
          </span>
          <span className="hero-label">Avg Error Rate</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{archiveReport?.overview.transactions ?? metrics?.total_transactions ?? 0}</span>
          <span className="hero-label">Total Tx</span>
        </div>
      </div>

      {!isRunning && (
        <TestExecutionArchive
          hasArchive={hasArchive}
          test={test}
          archiveError={archiveError}
          archiveLoading={archiveLoading}
          archiveReport={archiveReport}
          onDownloadArchiveArtifact={handleDownloadArchiveArtifact}
          archiveActionEntries={archiveActionEntries}
          selectedArchiveAction={selectedArchiveAction}
          onSelectedArchiveActionChange={(value) => {
            setSelectedArchiveAction(value)
            setArchiveTransactionsOffset(0)
          }}
          archiveGroups={archiveGroups}
          selectedArchiveGroup={selectedArchiveGroup}
          onSelectedArchiveGroupChange={(value) => {
            setSelectedArchiveGroup(value)
            setArchiveTransactionsOffset(0)
          }}
          archiveErrorsOnly={archiveErrorsOnly}
          onArchiveErrorsOnlyChange={(value) => {
            setArchiveErrorsOnly(value)
            setArchiveTransactionsOffset(0)
          }}
          archiveTransactionsLoading={archiveTransactionsLoading}
          archiveTransactionsTotal={archiveTransactionsTotal}
          archivePageStart={archivePageStart}
          archivePageEnd={archivePageEnd}
          archiveTransactionsOffset={archiveTransactionsOffset}
          archivePageSize={archivePageSize}
          onPreviousPage={() => setArchiveTransactionsOffset((current) => Math.max(0, current - archivePageSize))}
          onNextPage={() => setArchiveTransactionsOffset((current) => current + archivePageSize)}
          archiveTransactions={archiveTransactions}
          expandedArchiveEvent={expandedArchiveEvent}
          onExpandedArchiveEventChange={setExpandedArchiveEvent}
        />
      )}

      <TestExecutionStatusPanels
        isGoalOriented={isGoalOriented}
        goalDescription={describeGoalController(goalStatus)}
        goalStatus={goalStatus}
        test={test}
        metrics={metrics}
        isMultiGroup={isMultiGroup}
        groupMetrics={groupMetrics}
        groupColors={GROUP_COLORS}
      />

      {/* Charts */}
      <TestExecutionCharts
        filteredData={filteredData}
        expandedChart={expandedChart}
        onExpandedChartChange={setExpandedChart}
        onChartMouseDown={handleChartMouseDown}
        onChartMouseMove={handleChartMouseMove}
        onZoom={handleZoom}
        showRefArea={showRefArea}
        refAreaLeft={refAreaLeft}
        refAreaEnd={refAreaEnd}
        isMultiGroup={isMultiGroup}
        groupNames={groupNames}
        groupColors={GROUP_COLORS}
      />

      <TestExecutionSummary
        transactions={transactions}
        metrics={metrics}
      />
    </div>
  )
}
