import { useState, useEffect, useMemo, useCallback } from 'react'
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
import TestExecutionArchive from './TestExecutionArchive'
import TestExecutionCharts from './TestExecutionCharts'
import TestExecutionHeader from './TestExecutionHeader'
import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import TestExecutionLiveControls from './TestExecutionLiveControls'
import TestExecutionScenarioEditor from './TestExecutionScenarioEditor'
import TestExecutionScenarioPreview, { ScenarioScriptDetail } from './TestExecutionScenarioPreview'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import { parseDurationToSeconds, formatTimeLong } from '../utils/timeUtils'
import { downloadBlob } from '../utils/fileUtils'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]
const RESOURCE_BASELINE_SECONDS = 5 * 60

interface Props {
  testId: string
  history: MetricsHistoryPoint[]
  onBack: () => void
  onEditScript: (scriptId: string) => void
  onSelectTest: (testId: string) => void
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
      if (m.latency?.targets) {
        m.latency.targets.forEach((t: any) => {
          p[`latency_${t.name}`] = t.latency_ms
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
  const handlePause = useCallback(async () => {
    try {
      await api.pauseTest(testId)
      setControlFeedback('Test paused')
      setTest(prev => prev ? { ...prev, status: 'PAUSED' } : null)
      setTimeout(() => setControlFeedback(''), 3000)
    } catch (e: any) {
      setControlFeedback(`Error pausing: ${e.message}`)
    }
  }, [api, testId])

  const handleResume = useCallback(async () => {
    try {
      await api.resumeTest(testId)
      setControlFeedback('Test resumed')
      setTest(prev => prev ? { ...prev, status: 'RUNNING' } : null)
      setTimeout(() => setControlFeedback(''), 3000)
    } catch (e: any) {
      setControlFeedback(`Error resuming: ${e.message}`)
    }
  }, [api, testId])

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
        onPause={handlePause}
        onResume={handleResume}
        hasArchive={hasArchive}
        onDownloadArchive={handleDownloadArchive}
      />

      {/* Live Controls Panel */}
      {(isRunning || test.status === 'PAUSED') && (
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

      <TestExecutionScenarioPreview
        test={test}
        startCapacityConflict={startCapacityConflict}
        startFeedback={startFeedback}
        onDismissStartConflict={() => setStartCapacityConflict(null)}
        onConfirmStartAnyway={() => handleStart(true)}
        queuedScenarioPreview={queuedScenarioPreview}
        queuedScenarioRows={queuedScenarioRows}
        scriptDetailsLoading={scriptDetailsLoading}
        scriptDetailsError={scriptDetailsError}
        expandedScenarioScript={expandedScenarioScript}
        onExpandedScenarioScriptChange={setExpandedScenarioScript}
        onEditScript={onEditScript}
        groupColors={GROUP_COLORS}
      />

      {/* Hero metrics */}
      <TestExecutionScenarioEditor
        open={showScenarioEditor}
        test={test}
        onClose={() => setShowScenarioEditor(false)}
        onSelectTest={onSelectTest}
      />

      <TestExecutionHeroMetrics
        archiveReport={archiveReport}
        summary={summary}
        metrics={metrics}
      />

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
