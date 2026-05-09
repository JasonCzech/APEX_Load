import { useState, useEffect, useMemo, useCallback } from 'react'
import {
  LineChart, Line, AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, Legend, ReferenceArea,
} from 'recharts'
import { useApi } from '../hooks/useApi'
import { TestSummary, MetricsHistoryPoint, TxMetrics, GroupLiveMetrics } from '../types'
import StatusBadge from './StatusBadge'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]

interface Props {
  testId: string
  history: MetricsHistoryPoint[]
  onBack: () => void
}

export default function TestExecution({ testId, history, onBack }: Props) {
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

  const isRunning = test?.status === 'RUNNING' || test?.status === 'INITIALIZING'

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
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []
  const isMultiGroup = (test?.groups?.length || 0) > 1
  const groupNames = test?.groups?.map((g) => g.name) || []

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
            <button className="btn btn-success" onClick={() => api.startTest(testId)}>
              Start
            </button>
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
              <div className="control-group">
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
              </div>
              <div className="control-group">
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

      {/* Hero metrics */}
      <div className="hero-metrics">
        <div className="hero-card">
          <span className="hero-value">{summary?.maxVusers ?? metrics?.active_vusers ?? 0}</span>
          <span className="hero-label">Max VUsers</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{(summary?.avgTps ?? metrics?.tps ?? 0).toFixed(1)}</span>
          <span className="hero-label">Avg TPS</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{(summary?.avgLatency ?? metrics?.avg_response_ms ?? 0).toFixed(0)}</span>
          <span className="hero-label">Avg Latency (ms)</span>
        </div>
        <div className="hero-card">
          <span className="hero-value error-value">
            {(summary?.avgError ?? metrics?.error_pct ?? 0).toFixed(2)}%
          </span>
          <span className="hero-label">Avg Error Rate</span>
        </div>
        <div className="hero-card">
          <span className="hero-value">{metrics?.total_transactions ?? 0}</span>
          <span className="hero-label">Total Tx</span>
        </div>
      </div>

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
                      onMouseDown={e => e && setRefAreaLeft(e.activeLabel ?? '')}
                      onMouseMove={e => e && refAreaLeft && setRefAreaRight(e.activeLabel ?? '')}
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
                      {(refAreaLeft && refAreaRight) && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaRight} strokeOpacity={0.3} fill="var(--accent)" fillOpacity={0.1} />
                      )}
                    </AreaChart>
                  ) : c.type === 'line-multi' ? (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => e && setRefAreaLeft(e.activeLabel ?? '')}
                      onMouseMove={e => e && refAreaLeft && setRefAreaRight(e.activeLabel ?? '')}
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
                      {(refAreaLeft && refAreaRight) && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaRight} strokeOpacity={0.3} fill="var(--accent)" fillOpacity={0.1} />
                      )}
                    </LineChart>
                  ) : c.type === 'line-latency' ? (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => e && setRefAreaLeft(e.activeLabel ?? '')}
                      onMouseMove={e => e && refAreaLeft && setRefAreaRight(e.activeLabel ?? '')}
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
                      {(refAreaLeft && refAreaRight) && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaRight} strokeOpacity={0.3} fill="var(--accent)" fillOpacity={0.1} />
                      )}
                    </LineChart>
                  ) : c.type === 'area-multi' ? (
                    <AreaChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => e && setRefAreaLeft(e.activeLabel ?? '')}
                      onMouseMove={e => e && refAreaLeft && setRefAreaRight(e.activeLabel ?? '')}
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
                      {(refAreaLeft && refAreaRight) && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaRight} strokeOpacity={0.3} fill="var(--accent)" fillOpacity={0.1} />
                      )}
                    </AreaChart>
                  ) : (
                    <LineChart 
                      data={filteredData} 
                      syncId="metrics"
                      onMouseDown={e => e && setRefAreaLeft(e.activeLabel ?? '')}
                      onMouseMove={e => e && refAreaLeft && setRefAreaRight(e.activeLabel ?? '')}
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
                      {(refAreaLeft && refAreaRight) && (
                        <ReferenceArea x1={refAreaLeft} x2={refAreaRight} strokeOpacity={0.3} fill="var(--accent)" fillOpacity={0.1} />
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
