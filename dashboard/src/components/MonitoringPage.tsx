import { useState, useEffect, useCallback, useMemo, type DragEvent, type ReactNode } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { useApi } from '../hooks/useApi'
import { HealthResponse, TelemetryDiagnosticEntry, TestSummary } from '../types'
import { formatTelemetryDiagnosticTime, telemetryDiagnosticSeverityClass } from './monitoringTelemetryDiagnostics'

interface Props {
  connectionState: WebSocketConnectionState
  telemetryDiagnostics?: TelemetryDiagnosticEntry[]
}

export default function MonitoringPage({ connectionState, telemetryDiagnostics = [] }: Props) {
  const api = useApi()
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [tests, setTests] = useState<TestSummary[]>([])
  const [customizing, setCustomizing] = useState(false)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
  const [cardOrder, setCardOrder] = useState<string[]>(() => readStringArray('stormrunner.monitoring.cardOrder'))
  const [hiddenCardIds, setHiddenCardIds] = useState<string[]>(() => readStringArray('stormrunner.monitoring.hiddenCards'))
  const websocketValueClass = connectionState === 'connected' ? 'text-success' : connectionState === 'disconnected' ? 'text-danger' : ''
  const websocketLabel = connectionState === 'connected' ? 'Connected' : connectionState === 'connecting' ? 'Connecting' : 'Disconnected'

  const refresh = useCallback(async () => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
      return
    }
    try {
      const [healthRes, testsRes] = await Promise.all([
        api.getHealth(),
        api.listTests().catch(() => ({ tests: [] })),
      ])
      setHealth(healthRes)
      setTests(testsRes.tests || [])
    } catch {
      // API not available
    }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 5000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        refresh()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [refresh])

  const timeline = useMemo(() => buildRunTimeline(tests, health), [tests, health])

  const cards = useMemo<MonitorCard[]>(() => [
    {
      id: 'system-health',
      title: 'System Health',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-stat large">
            <span className={`monitor-indicator ${health ? 'healthy' : 'unknown'}`} />
            <span className="monitor-value">{health?.status || 'Checking...'}</span>
          </div>
          <p className="monitor-hint">API health is polled every 5 seconds while the page is visible.</p>
        </div>
      ),
    },
    {
      id: 'controller',
      title: 'Controller',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-row">
            <span className="monitor-label">Version</span>
            <span className="monitor-value">{health?.version || '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Active Tests</span>
            <span className="monitor-value">{health?.test_count ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">WS Clients</span>
            <span className="monitor-value">{health?.ws_clients ?? '-'}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'websocket',
      title: 'WebSocket',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M5 12.55a11 11 0 0 1 14.08 0" />
          <path d="M1.42 9a16 16 0 0 1 21.16 0" />
          <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
          <line x1="12" y1="20" x2="12.01" y2="20" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-row">
            <span className="monitor-label">Connection</span>
            <span className={`monitor-value ${websocketValueClass}`}>
              {websocketLabel}
            </span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Endpoint</span>
            <span className="monitor-value cell-mono">/api/v1/ws</span>
          </div>
        </div>
      ),
    },
    {
      id: 'telemetry-diagnostics',
      title: 'Telemetry Diagnostics',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {telemetryDiagnostics.length === 0 ? (
            <p className="monitor-hint">No sampled telemetry diagnostics received. Compact metrics updates remain on the hot path until degradation occurs.</p>
          ) : (
            <div className="telemetry-diagnostic-list">
              {telemetryDiagnostics.map((entry) => (
                <div key={`${entry.test_id}-${entry.timestamp}-${entry.diagnostic.summary}`} className={`telemetry-diagnostic-item ${telemetryDiagnosticSeverityClass(entry.diagnostic.severity)}`}>
                  <div className="telemetry-diagnostic-header">
                    <span className={`telemetry-diagnostic-badge ${telemetryDiagnosticSeverityClass(entry.diagnostic.severity)}`}>
                      {entry.diagnostic.severity}
                    </span>
                    <span className="telemetry-diagnostic-time">{formatTelemetryDiagnosticTime(entry.timestamp)}</span>
                  </div>
                  <div className="telemetry-diagnostic-summary">{entry.test_name || entry.test_id}: {entry.diagnostic.summary}</div>
                  {entry.diagnostic.conditions?.length ? (
                    <div className="telemetry-diagnostic-conditions">{entry.diagnostic.conditions.join(' · ')}</div>
                  ) : null}
                  {entry.diagnostic.samples?.length ? (
                    <div className="telemetry-diagnostic-samples">
                      {entry.diagnostic.samples.map((sample) => (
                        <div key={`${entry.test_id}-${entry.timestamp}-${sample.issue}-${sample.transaction}`} className="telemetry-diagnostic-sample">
                          <span className="telemetry-diagnostic-sample-issue">{sample.issue}</span>
                          <span className="telemetry-diagnostic-sample-name">{sample.transaction}</span>
                          <span className="telemetry-diagnostic-sample-stats">
                            {sample.errors ? `${sample.errors}/${sample.count ?? 0} errors` : `p99 ${Math.round(sample.p99_ms ?? 0)} ms`}
                            {sample.avg_ms ? ` · avg ${Math.round(sample.avg_ms)} ms` : ''}
                            {sample.dependency_target ? ` · ${sample.dependency_target}` : ''}
                          </span>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          )}
        </div>
      ),
    },
    {
      id: 'capacity',
      title: 'Capacity',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M3 3v18h18" />
          <path d="m19 9-5 5-4-4-3 3" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-row">
            <span className="monitor-label">Effective Available</span>
            <span className="monitor-value">{health?.capacity_availability?.effective_available_generators ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Configured Ceiling</span>
            <span className="monitor-value">{health?.capacity_availability?.configured_available_generators ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Source</span>
            <span className="monitor-value">{health?.capacity_availability?.source || '-'}</span>
          </div>
          {health?.capacity_availability?.warnings?.length ? (
            <div className="monitor-warning-list">
              {health.capacity_availability.warnings.map(warning => <p key={warning}>{warning}</p>)}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      id: 'generators',
      title: 'Generators',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="4" y="4" width="16" height="16" rx="2" ry="2" />
          <rect x="9" y="9" width="6" height="6" />
          <line x1="9" y1="1" x2="9" y2="4" />
          <line x1="15" y1="1" x2="15" y2="4" />
          <line x1="9" y1="20" x2="9" y2="23" />
          <line x1="15" y1="20" x2="15" y2="23" />
          <line x1="20" y1="9" x2="23" y2="9" />
          <line x1="20" y1="14" x2="23" y2="14" />
          <line x1="1" y1="9" x2="4" y2="9" />
          <line x1="1" y1="14" x2="4" y2="14" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-stat">
            <span className="monitor-value text-muted">Phase 2</span>
          </div>
          <p className="monitor-hint">Distributed generator pods will appear here when live generator inventory is available.</p>
        </div>
      ),
    },
    {
      id: 'run-timeline',
      title: 'Run Timeline',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <line x1="4" y1="19" x2="20" y2="19" />
          <rect x="4" y="5" width="5" height="10" rx="1" />
          <rect x="11" y="8" width="4" height="7" rx="1" />
          <rect x="17" y="3" width="3" height="12" rx="1" />
        </svg>
      ),
      content: (
        <div className="monitor-body timeline-body">
          <div className="timeline-summary-grid">
            <div className="timeline-summary-item">
              <span className="timeline-summary-value">{timeline.runs.length}</span>
              <span className="timeline-summary-label">Planned/Active</span>
            </div>
            <div className="timeline-summary-item">
              <span className="timeline-summary-value">{timeline.peakGenerators}</span>
              <span className="timeline-summary-label">Peak Generators</span>
            </div>
            <div className="timeline-summary-item">
              <span className={`timeline-summary-value ${timeline.capacityPct > 100 ? 'text-danger' : ''}`}>{timeline.capacityPct}%</span>
              <span className="timeline-summary-label">Capacity Used</span>
            </div>
          </div>
          {timeline.runs.length === 0 ? (
            <p className="monitor-hint">No queued or active runs. Queued tests will appear here with estimated resource overlap.</p>
          ) : (
            <>
              <div className="run-timeline-axis">
                <span>{timeline.windowStartLabel}</span>
                <span>{timeline.windowEndLabel}</span>
              </div>
              <div className="run-timeline-list">
                {timeline.runs.map(run => (
                  <div key={run.id} className="run-timeline-row">
                    <div className="run-timeline-meta">
                      <span className="run-timeline-name">{run.name}</span>
                      <span className="run-timeline-detail">{run.status} / {run.vusers.toLocaleString()} VUsers / {run.durationLabel}</span>
                    </div>
                    <div className="run-timeline-track" title={`${run.name}: ${run.startLabel} to ${run.endLabel}`}>
                      <span className={`run-timeline-bar ${run.status.toLowerCase()}`} style={{ left: `${run.leftPct}%`, width: `${run.widthPct}%` }} />
                    </div>
                    <div className="run-timeline-resource">
                      <span>{run.generators} gen</span>
                      <span>{run.startLabel}</span>
                    </div>
                  </div>
                ))}
              </div>
              <div className="timeline-resource-strip">
                <span>Peak overlap</span>
                <div className="timeline-resource-meter">
                  <span style={{ width: `${Math.min(100, timeline.capacityPct)}%` }} className={timeline.capacityPct > 100 ? 'over' : ''} />
                </div>
                <span>{timeline.peakGenerators}/{timeline.availableGenerators || '-'} generators</span>
              </div>
            </>
          )}
        </div>
      ),
    },
  ], [connectionState, health, telemetryDiagnostics, timeline, websocketLabel, websocketValueClass])

  useEffect(() => {
    setCardOrder(prev => mergeOrder(prev, cards.map(card => card.id)))
  }, [cards])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.monitoring.cardOrder', JSON.stringify(cardOrder))
  }, [cardOrder])

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('stormrunner.monitoring.hiddenCards', JSON.stringify(hiddenCardIds))
  }, [hiddenCardIds])

  const orderedCards = useMemo(() => {
    const byId = new Map(cards.map(card => [card.id, card]))
    return mergeOrder(cardOrder, cards.map(card => card.id))
      .map(id => byId.get(id))
      .filter((card): card is MonitorCard => Boolean(card))
  }, [cards, cardOrder])

  const visibleCards = orderedCards.filter(card => !hiddenCardIds.includes(card.id))
  const hiddenCards = orderedCards.filter(card => hiddenCardIds.includes(card.id))

  const toggleCard = (id: string) => {
    setHiddenCardIds(prev => prev.includes(id) ? prev.filter(cardId => cardId !== id) : [...prev, id])
  }

  const resetCards = () => {
    setCardOrder(cards.map(card => card.id))
    setHiddenCardIds([])
  }

  const handleDragStart = (event: DragEvent<HTMLDivElement>, id: string) => {
    if (!customizing) return
    setDraggedCardId(id)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }

  const handleDragOver = (event: DragEvent<HTMLDivElement>, id: string) => {
    if (!customizing || !draggedCardId || draggedCardId === id) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
  }

  const handleDrop = (event: DragEvent<HTMLDivElement>, id: string) => {
    if (!customizing) return
    event.preventDefault()
    const sourceId = event.dataTransfer.getData('text/plain') || draggedCardId
    if (!sourceId) return
    setCardOrder(prev => moveId(mergeOrder(prev, cards.map(card => card.id)), sourceId, id))
    setDraggedCardId(null)
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Monitoring</h2>
          <p className="page-subtitle">System health, generator pods, and connection status</p>
        </div>
        <div className="monitor-actions">
          {customizing && (
            <button className="btn btn-secondary btn-sm" type="button" onClick={resetCards}>Reset Cards</button>
          )}
          <button className={`btn btn-sm ${customizing ? 'btn-primary' : 'btn-secondary'}`} type="button" onClick={() => setCustomizing(value => !value)}>
            {customizing ? 'Done' : 'Customize Cards'}
          </button>
        </div>
      </div>

      {customizing && hiddenCards.length > 0 && (
        <div className="hidden-chart-list">
          {hiddenCards.map(card => (
            <button key={card.id} className="hidden-chart-pill" type="button" onClick={() => toggleCard(card.id)}>
              Show {card.title}
            </button>
          ))}
        </div>
      )}

      <div className={`monitoring-grid ${customizing ? 'is-customizing' : ''}`}>
        {visibleCards.map(card => (
          <div
            key={card.id}
            className={`monitor-card ${customizing ? 'editable' : ''} ${draggedCardId === card.id ? 'dragging' : ''}`}
            draggable={customizing}
            onDragStart={event => handleDragStart(event, card.id)}
            onDragOver={event => handleDragOver(event, card.id)}
            onDrop={event => handleDrop(event, card.id)}
            onDragEnd={() => setDraggedCardId(null)}
          >
            <div className="monitor-header">
              {customizing && <span className="monitor-drag-handle" title="Drag to reorder">::</span>}
              {card.icon}
              <span>{card.title}</span>
              {customizing && (
                <button className="btn-icon monitor-hide-btn" type="button" onClick={() => toggleCard(card.id)}>Hide</button>
              )}
            </div>
            {card.content}
          </div>
        ))}
      </div>
    </div>
  )
}

interface MonitorCard {
  id: string
  title: string
  icon: ReactNode
  content: ReactNode
}

interface TimelineRun {
  id: string
  name: string
  status: string
  vusers: number
  generators: number
  durationLabel: string
  startMs: number
  endMs: number
  startLabel: string
  endLabel: string
  leftPct: number
  widthPct: number
}

interface TimelineModel {
  runs: TimelineRun[]
  peakGenerators: number
  availableGenerators: number
  capacityPct: number
  windowStartLabel: string
  windowEndLabel: string
}

function buildRunTimeline(tests: TestSummary[], health: HealthResponse | null): TimelineModel {
  const now = Date.now()
  const candidates = tests
    .filter(test => ['QUEUED', 'INITIALIZING', 'RUNNING', 'STOPPING'].includes(test.status))
    .sort((a, b) => {
      const aActive = a.status === 'RUNNING' || a.status === 'INITIALIZING' || a.status === 'STOPPING'
      const bActive = b.status === 'RUNNING' || b.status === 'INITIALIZING' || b.status === 'STOPPING'
      if (aActive !== bActive) return aActive ? -1 : 1
      return getTestAnchorMs(a) - getTestAnchorMs(b)
    })
    .slice(0, 8)

  let cursor = now
  const preliminary = candidates.map(test => {
    const isActive = test.status === 'RUNNING' || test.status === 'INITIALIZING' || test.status === 'STOPPING'
    const durationMs = Math.max(parseDurationMs(test.duration), 60_000)
    const startedMs = getTestAnchorMs(test)
    const startMs = isActive ? Math.min(startedMs, now) : cursor
    const endMs = isActive ? Math.max(startMs + durationMs, now + 60_000) : startMs + durationMs
    if (!isActive) cursor = endMs
    return { test, startMs, endMs, durationMs }
  })

  if (preliminary.length === 0) {
    return {
      runs: [],
      peakGenerators: 0,
      availableGenerators: health?.capacity_availability?.effective_available_generators || 0,
      capacityPct: 0,
      windowStartLabel: formatTimelineTime(now),
      windowEndLabel: formatTimelineTime(now + 60 * 60 * 1000),
    }
  }

  const windowStart = Math.min(...preliminary.map(item => item.startMs), now)
  const windowEnd = Math.max(...preliminary.map(item => item.endMs), now + 60 * 60 * 1000)
  const windowMs = Math.max(1, windowEnd - windowStart)
  const availableGenerators = health?.capacity_availability?.effective_available_generators || 0
  const runs = preliminary.map(({ test, startMs, endMs, durationMs }) => {
    const generators = Math.max(1, test.capacity_advisory?.recommended_generators || Math.ceil((test.total_vusers || 0) / 10000))
    const leftPct = ((startMs - windowStart) / windowMs) * 100
    const widthPct = Math.max(3, ((endMs - startMs) / windowMs) * 100)
    return {
      id: test.id,
      name: test.name,
      status: test.status,
      vusers: test.total_vusers || 0,
      generators,
      durationLabel: test.duration || formatDurationShort(durationMs),
      startMs,
      endMs,
      startLabel: formatTimelineTime(startMs),
      endLabel: formatTimelineTime(endMs),
      leftPct: Math.max(0, Math.min(97, leftPct)),
      widthPct: Math.max(3, Math.min(100 - Math.max(0, leftPct), widthPct)),
    }
  })

  const peakGenerators = calculatePeakGenerators(runs)
  const capacityPct = availableGenerators > 0 ? Math.round((peakGenerators / availableGenerators) * 100) : 0
  return {
    runs,
    peakGenerators,
    availableGenerators,
    capacityPct,
    windowStartLabel: formatTimelineTime(windowStart),
    windowEndLabel: formatTimelineTime(windowEnd),
  }
}

function getTestAnchorMs(test: TestSummary) {
  const value = test.start_time || test.started_at || test.created_at
  const parsed = value ? new Date(value).getTime() : Date.now()
  return Number.isFinite(parsed) ? parsed : Date.now()
}

function parseDurationMs(value?: string) {
  if (!value) return 60_000
  const compact = value.trim().toLowerCase()
  const matches = compact.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h)/g)
  let total = 0
  for (const match of matches) {
    const amount = Number(match[1])
    const unit = match[2]
    if (unit === 'ms') total += amount
    if (unit === 's') total += amount * 1000
    if (unit === 'm') total += amount * 60_000
    if (unit === 'h') total += amount * 3_600_000
  }
  return total > 0 ? total : 60_000
}

function calculatePeakGenerators(runs: TimelineRun[]) {
  const events = runs.flatMap(run => [
    { at: run.startMs, delta: run.generators },
    { at: run.endMs, delta: -run.generators },
  ]).sort((a, b) => a.at === b.at ? a.delta - b.delta : a.at - b.at)
  let current = 0
  let peak = 0
  events.forEach(event => {
    current += event.delta
    peak = Math.max(peak, current)
  })
  return peak
}

function formatTimelineTime(value: number) {
  return new Date(value).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
}

function formatDurationShort(ms: number) {
  const minutes = Math.round(ms / 60_000)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  const remaining = minutes % 60
  return remaining ? `${hours}h ${remaining}m` : `${hours}h`
}

function readStringArray(key: string): string[] {
  if (typeof window === 'undefined') return []
  try {
    const parsed = JSON.parse(window.localStorage.getItem(key) || '[]')
    return Array.isArray(parsed) ? parsed.filter(value => typeof value === 'string') : []
  } catch {
    return []
  }
}

function mergeOrder(order: string[], ids: string[]) {
  const retained = order.filter(id => ids.includes(id))
  const added = ids.filter(id => !retained.includes(id))
  return [...retained, ...added]
}

function moveId(ids: string[], draggedId: string, targetId: string) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}
