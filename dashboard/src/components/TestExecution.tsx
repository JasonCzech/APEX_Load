import { Fragment, useState, useEffect, useMemo, useCallback } from 'react'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, Legend, ReferenceArea,
} from 'recharts'
import { useApi } from '../hooks/useApi'
import {
  TestSummary,
  MetricsHistoryPoint,
  TxMetrics,
  GroupLiveMetrics,
  GoalStatus,
  RunArchiveReport,
  TransactionEvent,
} from '../types'
import StatusBadge from './StatusBadge'
import CreateTestModal from './CreateTestModal'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]
const RESOURCE_BASELINE_SECONDS = 5 * 60

function parseDurationToSeconds(input?: string): number {
  if (!input) return 0
  let total = 0
  const matches = input.matchAll(/(\d+)(h|m|s)/g)
  for (const match of matches) {
    const value = Number(match[1])
    switch (match[2]) {
      case 'h':
        total += value * 3600
        break
      case 'm':
        total += value * 60
        break
      case 's':
        total += value
        break
    }
  }
  return total
}

function formatPreviewTime(seconds: number): string {
  if (seconds <= 0) return 'now'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = seconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return secs > 0 ? `${minutes}m ${secs}s` : `${minutes}m`
  return `${secs}s`
}

function formatScenarioTimeline(seconds: number): string {
  if (seconds <= 0) return '0s'
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = seconds % 60
  if (hours > 0) return `${hours}h${minutes > 0 ? ` ${minutes}m` : ''}`
  if (minutes > 0) return secs > 0 ? `${minutes}m ${secs}s` : `${minutes}m`
  return `${secs}s`
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

function formatArchiveTimestamp(value?: string): string {
  if (!value) return 'n/a'
  return new Date(value).toLocaleString()
}

function formatHeaderPairs(headers?: Record<string, string>): string {
  if (!headers || Object.keys(headers).length === 0) return 'n/a'
  return Object.entries(headers)
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
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

  useEffect(() => {
    const load = async () => {
      try {
        const data = await api.getTest(testId)
        setTest(data)
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


  const metrics = test?.live_metrics
  const goalStatus = metrics?.goal_status
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []
  const isMultiGroup = (test?.groups?.length || 0) > 1
  const groupNames = test?.groups?.map((g) => g.name) || []
  const isGoalOriented = test.scenario_mode === 'goal_oriented'
  const shakeoutFailed = test.status === 'FAILED' && Boolean(test.script_shakeout) && test.error?.toLowerCase().includes('shakeout')
  const archiveActionEntries = archiveReport ? Object.entries(archiveReport.by_action || {}).sort((a, b) => b[1].transactions - a[1].transactions) : []
  const archiveGroups = test?.groups?.map((group) => group.name) || []
  const archivePageSize = 25
  const archivePageStart = archiveTransactionsTotal === 0 ? 0 : archiveTransactionsOffset + 1
  const archivePageEnd = Math.min(archiveTransactionsOffset + archiveTransactions.length, archiveTransactionsTotal)

  const handleDownloadArchive = useCallback(async () => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchive(test.id)
      saveBlob(blob, `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}.zip`)
    } catch (e: any) {
      setArchiveError(e.message || 'Unable to download archive bundle')
    }
  }, [api, test])

  const handleDownloadArchiveArtifact = useCallback(async (artifactName: string) => {
    if (!test) return
    try {
      const blob = await api.downloadTestArchiveArtifact(test.id, artifactName)
      saveBlob(blob, `${test.name.replace(/\s+/g, '-').toLowerCase()}-${test.id}-${artifactName}`)
    } catch (e: any) {
      setArchiveError(e.message || `Unable to download ${artifactName}`)
    }
  }, [api, test])

  return (
    <div className="execution">
      <div className="exec-header">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <div className="exec-title-area">
          <h2>{test.name}</h2>
          <div className="exec-meta">
            <StatusBadge status={test.status} />
            <span className="meta-item">{test.total_vusers} VUsers</span>
            <span className="meta-item">{test.duration}</span>
            {isMultiGroup && (
              <span className="meta-item group-count">{test.groups.length} groups</span>
            )}
          </div>
        </div>
        <div className="exec-actions">
          <div className="time-range-selector">
            <button className={`btn-chip ${timeRange === '3m' ? 'active' : ''}`} onClick={() => setTimeRange('3m')}>3m</button>
            <button className={`btn-chip ${timeRange === '10m' ? 'active' : ''}`} onClick={() => setTimeRange('10m')}>10m</button>
            <button className={`btn-chip ${timeRange === 'all' ? 'active' : ''}`} onClick={() => setTimeRange('all')}>All</button>
            {timeRange === 'custom' && (
              <button className="btn-chip active zoom-reset" onClick={() => setTimeRange('all')}>Reset Zoom</button>
            )}
          </div>
          {test.status === 'QUEUED' && (
            <>
              <button className="btn btn-secondary" onClick={() => setShowScenarioEditor(true)}>
                Edit Scenario
              </button>
              <button className="btn btn-success" onClick={() => api.startTest(testId)}>
                Start
              </button>
            </>
          )}
          {isRunning && (
            <>
              <button className="btn btn-secondary" onClick={() => api.stopTest(testId)}>
                Stop
              </button>
              <button className="btn btn-danger" onClick={() => api.abortTest(testId)}>
                Abort
              </button>
            </>
          )}
          {!isRunning && hasArchive && (
            <button className="btn btn-secondary" onClick={handleDownloadArchive}>
              Download Archive
            </button>
          )}
        </div>
      </div>

      {/* Live Controls Panel */}
      {isRunning && (
        <div className="live-controls-section">
          <button
            className="btn btn-secondary live-controls-toggle"
            onClick={() => setShowControls(!showControls)}
          >
            {showControls ? 'Hide Controls' : 'Live Controls'}
          </button>
          {showControls && (
            <div className="live-controls-panel">
              <div className="live-controls-layout">
                <div className="control-group control-group-form">
                  <h4 className="control-title">VUser Scaling</h4>
                  {(test?.groups || []).map((g: any) => (
                    <div key={g.name} className="vuser-control-row">
                      <span className="control-label">{g.name}</span>
                      <span className="control-current">Current: {g.vusers}</span>
                      <input
                        type="range"
                        className="vuser-slider"
                        min={0}
                        max={Math.max(g.vusers * 3, 100)}
                        value={vuserTargets[g.name] ?? g.vusers}
                        onChange={e => setVuserTargets(prev => ({ ...prev, [g.name]: parseInt(e.target.value) }))}
                      />
                      <input
                        type="number"
                        className="vuser-input"
                        min={0}
                        value={vuserTargets[g.name] ?? g.vusers}
                        onChange={e => setVuserTargets(prev => ({ ...prev, [g.name]: parseInt(e.target.value) || 0 }))}
                      />
                      <button
                        className="btn btn-sm btn-accent"
                        onClick={() => handleScaleVUsers(g.name)}
                        disabled={(vuserTargets[g.name] ?? g.vusers) === g.vusers}
                      >
                        Apply
                      </button>
                    </div>
                  ))}

                  <div className="control-group control-group-nested">
                    <h4 className="control-title">Duration</h4>
                    <div className="duration-control-row">
                      <span className="control-label">Target</span>
                      <input
                        type="text"
                        className="duration-input"
                        placeholder="e.g. 10m, 1h30m"
                        value={durationInput}
                        onChange={e => setDurationInput(e.target.value)}
                      />
                      <div className="duration-quick-btns">
                        <button className="btn btn-sm btn-secondary" onClick={() => setDurationInput('5m')}>5m</button>
                        <button className="btn btn-sm btn-secondary" onClick={() => setDurationInput('10m')}>10m</button>
                        <button className="btn btn-sm btn-secondary" onClick={() => setDurationInput('30m')}>30m</button>
                        <button className="btn btn-sm btn-secondary" onClick={() => setDurationInput('1h')}>1h</button>
                      </div>
                      <button className="btn btn-sm btn-accent" onClick={handleAdjustDuration}>
                        Apply
                      </button>
                    </div>
                  </div>
                </div>

                <div className="control-group control-group-preview">
                  <div className="preview-header-row">
                    <div>
                      <h4 className="control-title">Projected Model</h4>
                      <p className="control-preview-note">Preview uses the values currently entered on the left and shows the load shape from now until the adjusted stop time.</p>
                    </div>
                    {liveControlPreview.hasPendingChanges && (
                      <span className="preview-pending-badge">Pending edits</span>
                    )}
                  </div>

                  <div className="preview-metrics">
                    <div className="preview-metric-card">
                      <span className="preview-metric-label">Now</span>
                      <span className="preview-metric-value">{liveControlPreview.currentTotal}</span>
                      <span className="preview-metric-detail">active VUsers</span>
                    </div>
                    <div className="preview-metric-card">
                      <span className="preview-metric-label">After Apply</span>
                      <span className="preview-metric-value">{liveControlPreview.projectedTotal}</span>
                      <span className="preview-metric-detail">projected VUsers</span>
                    </div>
                    <div className="preview-metric-card">
                      <span className="preview-metric-label">Remaining</span>
                      <span className="preview-metric-value">{formatPreviewTime(liveControlPreview.remainingSeconds)}</span>
                      <span className="preview-metric-detail">to stop</span>
                    </div>
                  </div>

                  <div className="control-preview-chart">
                    <ResponsiveContainer width="100%" height={220}>
                      <AreaChart data={liveControlPreview.points} margin={{ top: 10, right: 12, left: -18, bottom: 0 }}>
                        <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                        <XAxis
                          dataKey="second"
                          stroke="var(--text-muted)"
                          fontSize={11}
                          tickLine={false}
                          tickFormatter={(value) => formatPreviewTime(Number(value))}
                        />
                        <YAxis
                          stroke="var(--text-muted)"
                          fontSize={11}
                          tickLine={false}
                          allowDecimals={false}
                        />
                        <Tooltip
                          labelFormatter={(value) => `T+${formatPreviewTime(Number(value))}`}
                          contentStyle={{ background: 'rgba(33, 36, 47, 0.92)', border: '1px solid var(--border)', borderRadius: 10 }}
                          formatter={(value: number, name: string) => [value, name === 'total' ? 'Total' : name]}
                        />
                        <Legend verticalAlign="top" height={28} />
                        {(test?.groups || []).map((group, index) => (
                          <Area
                            key={group.name}
                            type="stepAfter"
                            dataKey={`group${index}`}
                            name={group.name}
                            stackId="projected"
                            stroke={GROUP_COLORS[index % GROUP_COLORS.length]}
                            fill={GROUP_COLORS[index % GROUP_COLORS.length]}
                            fillOpacity={0.3}
                            isAnimationActive={false}
                          />
                        ))}
                        <Line
                          type="stepAfter"
                          dataKey="total"
                          name="Total"
                          stroke="var(--text-primary)"
                          strokeWidth={2}
                          dot={false}
                          isAnimationActive={false}
                        />
                        <ReferenceArea
                          x1={0}
                          x2={Math.max(0, Math.min(liveControlPreview.points[1]?.second ?? 0, liveControlPreview.points[2]?.second ?? 0))}
                          fill="var(--accent)"
                          fillOpacity={0.06}
                          strokeOpacity={0}
                        />
                      </AreaChart>
                    </ResponsiveContainer>
                    <div className="control-preview-caption">
                      The profile steps from the current live load to the entered targets, then holds until the projected end of the run.
                    </div>
                  </div>
                </div>
              </div>
              {controlFeedback && (
                <div className={`control-feedback ${controlFeedback.startsWith('Error') ? 'error' : 'success'}`}>
                  {controlFeedback}
                </div>
              )}
            </div>
          )}
        </div>
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
                <span className="scenario-preview-stat-value">{formatScenarioTimeline(queuedScenarioPreview.steadySeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Total Timeline</span>
                <span className="scenario-preview-stat-value">{formatScenarioTimeline(queuedScenarioPreview.totalSeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Baseline</span>
                <span className="scenario-preview-stat-value">{test.resource_baseline ? '5m pre/post' : 'Disabled'}</span>
              </div>
            </div>
          </div>

          <div className="scenario-preview-chart">
            <ResponsiveContainer width="100%" height={260}>
              <AreaChart data={queuedScenarioPreview.points} margin={{ top: 8, right: 12, left: -18, bottom: 4 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis
                  dataKey="second"
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  tickFormatter={(value) => formatScenarioTimeline(Number(value))}
                />
                <YAxis
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  allowDecimals={false}
                />
                <Tooltip
                  labelFormatter={(value) => `T+${formatScenarioTimeline(Number(value))}`}
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

      {!isRunning && hasArchive && (
        <div className="archive-report-card">
          <div className="archive-report-header">
            <div>
              <h3 className="archive-report-title">Archive Report</h3>
              <p className="archive-report-subtitle">
                Post-run summary derived from the saved JSONL bundle. Completed {formatArchiveTimestamp(test.archive?.completed_at)}.
              </p>
            </div>
            <div className="archive-report-badges">
              <span className="archive-chip">{test.archive?.transaction_count || 0} transactions</span>
              <span className="archive-chip">{test.archive?.resource_sample_count || 0} resource samples</span>
              <span className="archive-chip">{test.archive?.interval_summary_count || 0} summaries</span>
            </div>
          </div>

          {archiveError && <p className="scenario-script-status error">{archiveError}</p>}
          {archiveLoading && <p className="scenario-script-status">Loading archive report…</p>}

          {archiveReport && (
            <>
              <div className="archive-overview-grid">
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Peak TPS</span>
                  <span className="scenario-script-detail-value">{archiveReport.overview.peak_tps.toFixed(1)}</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Peak Active VUsers</span>
                  <span className="scenario-script-detail-value">{archiveReport.overview.peak_active_vusers}</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Response Range</span>
                  <span className="scenario-script-detail-value">{archiveReport.overview.min_response_ms.toFixed(1)} - {archiveReport.overview.max_response_ms.toFixed(1)} ms</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">CPU Peak</span>
                  <span className="scenario-script-detail-value">{archiveReport.resource_peaks.cpu_process_pct.toFixed(1)}%</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Memory Peak</span>
                  <span className="scenario-script-detail-value">{archiveReport.resource_peaks.mem_process_mb.toFixed(1)} MB</span>
                </div>
                <div className="scenario-script-detail-card">
                  <span className="scenario-script-detail-label">Network Peak</span>
                  <span className="scenario-script-detail-value">{archiveReport.resource_peaks.net_sent_kbps.toFixed(1)} / {archiveReport.resource_peaks.net_recv_kbps.toFixed(1)} KB/s</span>
                </div>
              </div>

              <div className="archive-artifact-row">
                <div className="archive-artifact-group">
                  <span className="archive-artifact-label">JSONL</span>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.transactions_file || 'transactions.jsonl')}>
                    Download Transactions JSONL
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.resource_samples_file || 'resources.jsonl')}>
                    Download Resources JSONL
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_file || 'interval_summaries.jsonl')}>
                    Download Summaries JSONL
                  </button>
                </div>
                <div className="archive-artifact-group">
                  <span className="archive-artifact-label">Parquet</span>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.transactions_parquet_file || 'transactions.parquet')}>
                    Download Transactions Parquet
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.resource_samples_parquet_file || 'resources.parquet')}>
                    Download Resources Parquet
                  </button>
                  <button className="btn btn-secondary btn-sm" onClick={() => handleDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_parquet_file || 'interval_summaries.parquet')}>
                    Download Summaries Parquet
                  </button>
                </div>
              </div>

              <div className="archive-report-layout">
                <div className="tx-section archive-action-section">
                  <div className="archive-section-header">
                    <div>
                      <h3 className="chart-title">Action Summary</h3>
                      <p className="archive-section-copy">Archived totals by transaction name.</p>
                    </div>
                  </div>
                  <table className="tx-table">
                    <thead>
                      <tr>
                        <th>Action</th>
                        <th>Count</th>
                        <th>Errors</th>
                        <th>Error %</th>
                        <th>Avg (ms)</th>
                        <th>Max (ms)</th>
                      </tr>
                    </thead>
                    <tbody>
                      {archiveActionEntries.slice(0, 8).map(([name, stats]) => (
                        <tr key={name}>
                          <td className="tx-name">{name}</td>
                          <td>{stats.transactions}</td>
                          <td className={stats.errors > 0 ? 'error-cell' : ''}>{stats.errors}</td>
                          <td>{stats.error_pct.toFixed(2)}%</td>
                          <td>{stats.avg_response_ms.toFixed(1)}</td>
                          <td>{stats.max_response_ms.toFixed(1)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <div className="tx-section archive-drilldown-section">
                  <div className="archive-section-header archive-section-header-stack">
                    <div>
                      <h3 className="chart-title">Transaction Drill-Down</h3>
                      <p className="archive-section-copy">Sample archived events for a selected action.</p>
                    </div>
                    <div className="archive-filter-bar">
                      <select
                        className="archive-action-select"
                        value={selectedArchiveAction}
                        onChange={(event) => {
                          setSelectedArchiveAction(event.target.value)
                          setArchiveTransactionsOffset(0)
                        }}
                      >
                        {archiveActionEntries.map(([name]) => (
                          <option key={name} value={name}>{name}</option>
                        ))}
                      </select>
                      <select
                        className="archive-action-select archive-filter-select"
                        value={selectedArchiveGroup}
                        onChange={(event) => {
                          setSelectedArchiveGroup(event.target.value)
                          setArchiveTransactionsOffset(0)
                        }}
                      >
                        <option value="">All groups</option>
                        {archiveGroups.map((group) => (
                          <option key={group} value={group}>{group}</option>
                        ))}
                      </select>
                      <label className="archive-checkbox">
                        <input
                          type="checkbox"
                          checked={archiveErrorsOnly}
                          onChange={(event) => {
                            setArchiveErrorsOnly(event.target.checked)
                            setArchiveTransactionsOffset(0)
                          }}
                        />
                        Errors only
                      </label>
                    </div>
                  </div>
                  <div className="archive-pagination-bar">
                    <span className="archive-pagination-copy">
                      {archiveTransactionsLoading ? 'Loading events…' : archiveTransactionsTotal > 0 ? `${archivePageStart}-${archivePageEnd} of ${archiveTransactionsTotal}` : 'No archived events'}
                    </span>
                    <div className="archive-pagination-actions">
                      <button
                        className="btn btn-sm btn-secondary"
                        disabled={archiveTransactionsOffset === 0 || archiveTransactionsLoading}
                        onClick={() => setArchiveTransactionsOffset((current) => Math.max(0, current - archivePageSize))}
                      >
                        Prev
                      </button>
                      <button
                        className="btn btn-sm btn-secondary"
                        disabled={archiveTransactionsOffset + archivePageSize >= archiveTransactionsTotal || archiveTransactionsLoading}
                        onClick={() => setArchiveTransactionsOffset((current) => current + archivePageSize)}
                      >
                        Next
                      </button>
                    </div>
                  </div>
                  {!archiveTransactionsLoading && archiveTransactions.length === 0 ? (
                    <p className="scenario-script-status">No archived transactions loaded for this action.</p>
                  ) : (
                    <table className="tx-table archive-events-table">
                      <thead>
                        <tr>
                          <th>Time</th>
                          <th>Group</th>
                          <th>Status</th>
                          <th>Duration</th>
                          <th>Bytes</th>
                          <th>Error</th>
                          <th>Details</th>
                        </tr>
                      </thead>
                      <tbody>
                        {archiveTransactions.map((event, index) => (
                          (() => {
                            const rowKey = `${event.timestamp}-${index}`
                            const hasSnapshot = Boolean(event.snapshot)
                            const isExpanded = expandedArchiveEvent === rowKey
                            return (
                              <Fragment key={rowKey}>
                                <tr key={rowKey}>
                                  <td>{formatArchiveTimestamp(event.timestamp)}</td>
                                  <td>{event.group || 'n/a'}</td>
                                  <td className={event.success ? '' : 'error-cell'}>{event.status_code || (event.success ? 'OK' : 'ERR')}</td>
                                  <td>{event.duration_ms.toFixed(1)} ms</td>
                                  <td>{event.bytes_received || 0}</td>
                                  <td className={!event.success ? 'error-cell' : ''}>{event.error_message || 'None'}</td>
                                  <td>
                                    {hasSnapshot ? (
                                      <button
                                        className="btn btn-sm btn-secondary archive-detail-toggle"
                                        onClick={() => setExpandedArchiveEvent((current) => current === rowKey ? null : rowKey)}
                                      >
                                        {isExpanded ? 'Hide' : 'Inspect'}
                                      </button>
                                    ) : (
                                      <span className="cell-muted">n/a</span>
                                    )}
                                  </td>
                                </tr>
                                {isExpanded && hasSnapshot && (
                                  <tr className="archive-event-detail-row">
                                    <td colSpan={7}>
                                      <div className="archive-event-detail-grid">
                                        <div className="archive-event-detail-card">
                                          <span className="scenario-script-detail-label">Request</span>
                                          <span className="scenario-script-detail-value">{event.snapshot?.request_method || 'n/a'} {event.snapshot?.request_url || ''}</span>
                                          <pre className="archive-event-detail-pre">{formatHeaderPairs(event.snapshot?.request_headers)}</pre>
                                          <pre className="archive-event-detail-pre">{event.snapshot?.request_body || 'No request body captured'}</pre>
                                        </div>
                                        <div className="archive-event-detail-card">
                                          <span className="scenario-script-detail-label">Response</span>
                                          <span className="scenario-script-detail-value">{event.snapshot?.response_status || event.status_code || 'n/a'}</span>
                                          <pre className="archive-event-detail-pre">{formatHeaderPairs(event.snapshot?.response_headers)}</pre>
                                          <pre className="archive-event-detail-pre">{event.snapshot?.response_body || 'No response body captured'}</pre>
                                        </div>
                                      </div>
                                    </td>
                                  </tr>
                                )}
                              </Fragment>
                            )
                          })()
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              </div>
            </>
          )}
        </div>
      )}

      {isGoalOriented && (
        <div className="goal-status-card">
          <div className="goal-status-header">
            <div>
              <h3 className="goal-status-title">Goal Controller</h3>
              <p className="goal-status-subtitle">
                {describeGoalController(goalStatus)}
              </p>
            </div>
            <div className={`goal-status-badge ${goalStatus?.stop_triggered ? 'danger' : goalStatus?.breach_active ? 'warning' : 'healthy'}`}>
              {goalStatus?.stop_triggered ? 'Stopping' : goalStatus?.breach_active ? 'SLA Breach' : 'Active'}
            </div>
          </div>

          <div className="goal-status-grid">
            <div className="goal-status-metric">
              <span className="goal-status-label">Target TPS</span>
              <span className="goal-status-value">{(goalStatus?.target_average_tps ?? test.goal_config?.target_average_tps ?? 0).toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Current TPS</span>
              <span className="goal-status-value">{(goalStatus?.current_tps ?? metrics?.tps ?? 0).toFixed(1)}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Current VUsers</span>
              <span className="goal-status-value">{goalStatus?.current_total_vusers ?? metrics?.active_vusers ?? test.total_vusers}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Suggested VUsers</span>
              <span className="goal-status-value">{goalStatus?.suggested_total_vusers ?? goalStatus?.current_total_vusers ?? test.total_vusers}</span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">Guardrails</span>
              <span className="goal-status-value">
                {(test.goal_config?.min_vusers ?? 0)} - {test.goal_config?.max_vusers ?? 'unbounded'}
              </span>
            </div>
            <div className="goal-status-metric">
              <span className="goal-status-label">SLA Window</span>
              <span className="goal-status-value">{test.goal_config?.sla_breach_window || '30s'}</span>
            </div>
          </div>

          <div className="goal-status-flags">
            <span className={`goal-flag ${goalStatus?.latency_breached ? 'alert' : ''}`}>
              P95 {goalStatus?.latency_breached ? 'breached' : 'healthy'}
            </span>
            <span className={`goal-flag ${goalStatus?.error_breached ? 'alert' : ''}`}>
              Error rate {goalStatus?.error_breached ? 'breached' : 'healthy'}
            </span>
            <span className="goal-flag neutral">
              Stop on breach {test.goal_config?.stop_on_sla_breach ? 'enabled' : 'disabled'}
            </span>
          </div>
        </div>
      )}

      {/* Per-group live metrics */}
      {isMultiGroup && groupMetrics.length > 0 && (
        <div className="group-metrics-grid">
          {groupMetrics.map((gm: GroupLiveMetrics, i: number) => (
            <div
              key={gm.name}
              className="group-metric-card"
              style={{ borderTopColor: GROUP_COLORS[i % GROUP_COLORS.length] }}
            >
              <div className="gm-header">
                <span className="gm-name">{gm.name}</span>
                <span className="gm-vusers">{gm.vusers} VUsers</span>
              </div>
              <div className="gm-stats">
                <div className="gm-stat">
                  <span className="gm-value">{gm.tps?.toFixed(1)}</span>
                  <span className="gm-label">TPS</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.avg_response_ms?.toFixed(0)}</span>
                  <span className="gm-label">Avg ms</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.p90_ms?.toFixed(0)}</span>
                  <span className="gm-label">P90</span>
                </div>
                <div className="gm-stat">
                  <span className="gm-value">{gm.errors}</span>
                  <span className="gm-label">Errors</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Charts */}
      {filteredData.length > 1 && (
        <div className="charts-grid">
          {(() => {
            const latest = filteredData[filteredData.length - 1] || {}
            const chartConfigs = [
              { id: 'vusers', title: 'Virtual Users', type: 'area', dataKey: 'vusers', color: 'var(--chart-1)' },
              { id: 'tps', title: 'Transactions Per Second', type: 'line-multi', totalKey: 'tps' },
              { id: 'latency', title: 'Response Time (ms)', type: 'line-latency' },
              { id: 'errors', title: 'Error Rate (%)', type: 'area', dataKey: 'errorPct', color: 'var(--chart-4)', domain: [0, 'auto'] as [number, string] },
              { id: 'cpu', title: 'CPU Usage (%)', type: 'area-multi', keys: [{k:'cpuProcess', n:'VUsers', c:'var(--chart-1)'}, {k:'cpuFree', n:'System', c:'var(--chart-2)'}], domain: [0, 100] as [number, number] },
              { id: 'mem', title: 'Memory Usage (MB)', type: 'area-multi', keys: [{k:'memProcess', n:'VUsers', c:'var(--chart-4)'}, {k:'memFree', n:'System', c:'var(--chart-5)'}] },
              { id: 'io', title: 'Network & Disk I/O (KB/s)', type: 'line-io' }
            ]

            // Sort so expanded is first
            const sorted = [...chartConfigs].sort((a, b) => {
              if (a.id === expandedChart) return -1
              if (b.id === expandedChart) return 1
              return 0
            })

            return sorted.map(c => (
              <div key={c.id} className={`chart-card ${expandedChart === c.id ? 'expanded' : ''}`}>
                <div className="chart-card-header">
                  <h3 className="chart-title">{c.title}</h3>
                  <button className="btn-icon btn-expand" onClick={() => setExpandedChart(expandedChart === c.id ? null : c.id)}>
                    {expandedChart === c.id ? '↙' : '↗'}
                  </button>
                </div>
                <ResponsiveContainer width="100%" height={expandedChart === c.id ? 400 : 200}>
                  {c.type === 'area' ? (
                    <AreaChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => handleChartMouseDown(e?.activeLabel)}
                      onMouseMove={e => handleChartMouseMove(e?.activeLabel)}
                      onMouseUp={handleZoom}
                    >
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                      <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                      <YAxis stroke="var(--text-muted)" fontSize={11} domain={c.domain as any} />
                      <Tooltip
                        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                        isAnimationActive={false}
                      />
                      <Area type="monotone" dataKey={c.dataKey!} name="Total" stroke={c.color!} fill={c.color!} fillOpacity={0.15} isAnimationActive={false} />
                      {showRefArea && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                      )}
                    </AreaChart>
                  ) : c.type === 'line-multi' ? (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => handleChartMouseDown(e?.activeLabel)}
                      onMouseMove={e => handleChartMouseMove(e?.activeLabel)}
                      onMouseUp={handleZoom}
                    >
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                      <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                      <YAxis stroke="var(--text-muted)" fontSize={11} />
                      <Tooltip
                        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                        isAnimationActive={false}
                      />
                      {isMultiGroup && <Legend verticalAlign="bottom" height={36} />}
                      <Line type="monotone" dataKey={c.totalKey!} name="Total" stroke="var(--text-muted)" dot={false} strokeWidth={2} strokeDasharray={isMultiGroup ? '4 2' : undefined} isAnimationActive={false} />
                      {isMultiGroup && groupNames.map((name, i) => (
                        <Line key={name} type="monotone" dataKey={`${name}_tps`} name={name} stroke={GROUP_COLORS[i % GROUP_COLORS.length]} dot={false} isAnimationActive={false} />
                      ))}
                      {showRefArea && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                      )}
                    </LineChart>
                  ) : c.type === 'line-latency' ? (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => handleChartMouseDown(e?.activeLabel)}
                      onMouseMove={e => handleChartMouseMove(e?.activeLabel)}
                      onMouseUp={handleZoom}
                    >
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                      <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                      <YAxis stroke="var(--text-muted)" fontSize={11} />
                      <Tooltip
                        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                        isAnimationActive={false}
                      />
                      <Legend verticalAlign="bottom" height={36} />
                      <Line type="monotone" dataKey="p50" name="P50" stroke="var(--chart-5)" dot={false} strokeWidth={2} isAnimationActive={false} />
                      <Line type="monotone" dataKey="p90" name="P90" stroke="var(--chart-3)" dot={false} strokeWidth={2} isAnimationActive={false} />
                      <Line type="monotone" dataKey="p99" name="P99" stroke="var(--chart-4)" dot={false} strokeWidth={1.5} strokeDasharray="4 2" isAnimationActive={false} />
                      {isMultiGroup && groupNames.map((name, i) => (
                        <Line key={name} type="monotone" dataKey={`${name}_avgMs`} name={`${name} avg`} stroke={GROUP_COLORS[i % GROUP_COLORS.length]} dot={false} strokeWidth={1.5} strokeDasharray="2 2" isAnimationActive={false} />
                      ))}
                      {showRefArea && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                      )}
                    </LineChart>
                  ) : c.type === 'area-multi' ? (
                    <AreaChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => handleChartMouseDown(e?.activeLabel)}
                      onMouseMove={e => handleChartMouseMove(e?.activeLabel)}
                      onMouseUp={handleZoom}
                    >
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                      <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                      <YAxis stroke="var(--text-muted)" fontSize={11} domain={c.domain as any} />
                      <Tooltip
                        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                        isAnimationActive={false}
                      />
                      <Legend verticalAlign="bottom" height={36} />
                      {c.keys!.map(k => (
                        <Area key={k.k} type="monotone" dataKey={k.k} name={k.n} stroke={k.c} fill={k.c} fillOpacity={0.2} isAnimationActive={false} />
                      ))}
                      {showRefArea && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                      )}
                    </AreaChart>
                  ) : (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => handleChartMouseDown(e?.activeLabel)}
                      onMouseMove={e => handleChartMouseMove(e?.activeLabel)}
                      onMouseUp={handleZoom}
                    >
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                      <XAxis dataKey="time" stroke="var(--text-muted)" fontSize={11} />
                      <YAxis stroke="var(--text-muted)" fontSize={11} />
                      <Tooltip
                        contentStyle={{ background: 'rgba(33, 36, 47, 0.2)', backdropFilter: 'blur(12px)', border: '1px solid rgba(255, 255, 255, 0.1)', borderRadius: 8, boxShadow: '0 4px 12px rgba(0,0,0,0.2)' }}
                        labelStyle={{ color: 'var(--text-primary)', fontWeight: 700, marginBottom: 4 }}
                        isAnimationActive={false}
                      />
                      <Legend />
                      <Line type="monotone" dataKey="netSent" name="Net Egress" stroke="var(--chart-1)" dot={false} strokeWidth={2} isAnimationActive={false} />
                      <Line type="monotone" dataKey="netRecv" name="Net Ingress" stroke="var(--chart-2)" dot={false} strokeWidth={2} isAnimationActive={false} />
                      <Line type="monotone" dataKey="diskRead" name="Disk Read" stroke="var(--chart-3)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                      <Line type="monotone" dataKey="diskWrite" name="Disk Write" stroke="var(--chart-6)" dot={false} strokeWidth={1} strokeDasharray="3 3" isAnimationActive={false} />
                      {showRefArea && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaEnd} strokeOpacity={0.25} fill="var(--accent)" fillOpacity={0.12} />
                      )}
                    </LineChart>
                  )}
                </ResponsiveContainer>

                <div className="chart-metrics-footer">
                  {c.id === 'vusers' && (
                    <div className="footer-metric">
                      <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                      <span className="label">Active:</span>
                      <span className="value">{latest.vusers}</span>
                    </div>
                  )}
                  {c.id === 'tps' && (
                    <>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--text-muted)' }}></span>
                        <span className="label">Total:</span>
                        <span className="value">{latest.tps}</span>
                      </div>
                      {isMultiGroup && groupNames.map((name, i) => (
                        <div key={name} className="footer-metric">
                          <span className="dot" style={{ background: GROUP_COLORS[i % GROUP_COLORS.length] }}></span>
                          <span className="label">{name}:</span>
                          <span className="value">{latest[`${name}_tps`]}</span>
                        </div>
                      ))}
                    </>
                  )}
                  {c.id === 'latency' && (
                    <>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--chart-5)' }}></span>
                        <span className="label">P50:</span>
                        <span className="value">{latest.p50}ms</span>
                      </div>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--chart-3)' }}></span>
                        <span className="label">P90:</span>
                        <span className="value">{latest.p90}ms</span>
                      </div>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                        <span className="label">P99:</span>
                        <span className="value">{latest.p99}ms</span>
                      </div>
                    </>
                  )}
                  {c.id === 'errors' && (
                    <div className="footer-metric">
                      <span className="dot" style={{ background: 'var(--chart-4)' }}></span>
                      <span className="label">Current:</span>
                      <span className="value">{latest.errorPct}%</span>
                    </div>
                  )}
                  {c.type === 'area-multi' && c.keys?.map(k => (
                    <div key={k.k} className="footer-metric">
                      <span className="dot" style={{ background: k.c }}></span>
                      <span className="label">{k.n}:</span>
                      <span className="value">{latest[k.k]}{c.id === 'cpu' ? '%' : ' MB'}</span>
                    </div>
                  ))}
                  {c.id === 'io' && (
                    <>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--chart-1)' }}></span>
                        <span className="label">Net Out:</span>
                        <span className="value">{latest.netSent} KB/s</span>
                      </div>
                      <div className="footer-metric">
                        <span className="dot" style={{ background: 'var(--chart-2)' }}></span>
                        <span className="label">Net In:</span>
                        <span className="value">{latest.netRecv} KB/s</span>
                      </div>
                    </>
                  )}
                </div>
              </div>
            ))
          })()}
        </div>
      )}

      {/* Transaction breakdown */}
      {transactions.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Transaction Breakdown</h3>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Transaction</th>
                <th>Count</th>
                <th>Errors</th>
                <th>Avg (ms)</th>
                <th>P50 (ms)</th>
                <th>P90 (ms)</th>
                <th>P99 (ms)</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map((tx: TxMetrics) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.count}</td>
                  <td className={tx.errors > 0 ? 'error-cell' : ''}>{tx.errors}</td>
                  <td>{tx.avg_ms?.toFixed(1)}</td>
                  <td>{tx.p50_ms?.toFixed(1)}</td>
                  <td>{tx.p90_ms?.toFixed(1)}</td>
                  <td>{tx.p99_ms?.toFixed(1)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Percentile summary */}
      {metrics && (
        <div className="percentile-bar">
          <div className="pct-item">
            <span className="pct-label">P50</span>
            <span className="pct-value">{metrics.p50_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P90</span>
            <span className="pct-value">{metrics.p90_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P95</span>
            <span className="pct-value">{metrics.p95_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P99</span>
            <span className="pct-value">{metrics.p99_ms?.toFixed(1)} ms</span>
          </div>
        </div>
      )}
    </div>
  )
}
