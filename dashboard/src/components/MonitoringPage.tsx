import { useState, useEffect, useCallback, useMemo, type DragEvent, type ReactNode } from 'react'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { HealthResponse, ScenarioScheduleHealth, SystemInventoryResponse, TelemetryDiagnosticEntry, TestSummary } from '../types'
import { formatTelemetryDiagnosticTime, telemetryDiagnosticSeverityClass } from './monitoringTelemetryDiagnostics'

interface Props {
  connectionState: WebSocketConnectionState
  telemetryDiagnostics?: TelemetryDiagnosticEntry[]
}

const EMPTY_GENERATORS: SystemInventoryResponse['generators'] = []

export default function MonitoringPage({ connectionState, telemetryDiagnostics = [] }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [scheduleHealth, setScheduleHealth] = useState<ScenarioScheduleHealth | null>(null)
  const [inventory, setInventory] = useState<SystemInventoryResponse | null>(null)
  const [tests, setTests] = useState<TestSummary[]>([])
  const [isTelemetryModalOpen, setIsTelemetryModalOpen] = useState(false)
  const [isGeneratorsModalOpen, setIsGeneratorsModalOpen] = useState(false)
  const [customizing, setCustomizing] = useState(false)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
  const [cardOrder, setCardOrder] = useState<string[]>(() => readStringArray('stormrunner.monitoring.cardOrder'))
  const [hiddenCardIds, setHiddenCardIds] = useState<string[]>(() => readStringArray('stormrunner.monitoring.hiddenCards'))
  const websocketValueClass = connectionState === 'connected' ? 'text-success' : connectionState === 'disconnected' ? 'text-danger' : ''
  const websocketLabel = connectionState === 'connected' ? 'Connected' : connectionState === 'connecting' ? 'Connecting' : 'Disconnected'
  const telemetryWarningCount = telemetryDiagnostics.filter(entry => entry.diagnostic.severity === 'warning').length
  const telemetryDangerCount = telemetryDiagnostics.filter(entry => entry.diagnostic.severity === 'danger').length
  const telemetryLatestEntry = telemetryDiagnostics[0] ?? null
  const telemetryHeaderSeverity = telemetryDangerCount > 0 ? 'danger' : telemetryWarningCount > 0 ? 'warning' : 'healthy'
  const telemetryHeaderLabel = telemetryDangerCount > 0
    ? `${telemetryDangerCount} danger`
    : telemetryWarningCount > 0
      ? `${telemetryWarningCount} warning`
      : 'healthy'
  const generators = inventory?.generators ?? EMPTY_GENERATORS
  const generatorConnectedCount = generators.filter(generator => generator.status === 'connected').length
  const generatorIssueCount = generators.length - generatorConnectedCount
  const generatorAvailableCapacity = generators.reduce((sum, generator) => sum + generator.available_capacity, 0)
  const generatorActiveVUsers = generators.reduce((sum, generator) => sum + generator.active_vusers, 0)
  const controllerStatusLabel = inventory?.controller.status || health?.status || 'unknown'
  const controllerHeaderSeverity = controllerStatusLabel === 'healthy' || controllerStatusLabel === 'ok'
    ? 'healthy'
    : controllerStatusLabel === 'unknown'
      ? 'unknown'
      : 'warning'
  const controllerGeneratorInventoryLabel = inventory?.controller.execution_mode === 'distributed'
    ? `${generatorConnectedCount}/${generators.length} connected`
    : inventory?.controller.execution_mode === 'local'
      ? 'Local mode'
      : '-'
  const generatorHeaderSeverity = generatorIssueCount > 0 ? 'warning' : generators.length > 0 ? 'healthy' : 'unknown'
  const generatorHeaderLabel = generators.length > 0
    ? `${generatorConnectedCount}/${generators.length} online`
    : 'no inventory'
  const fleetAlerts = useMemo(() => buildFleetAlerts(generators, inventory?.controller.version), [generators, inventory?.controller.version])
  const fleetAlertDangerCount = fleetAlerts.filter(alert => alert.severity === 'danger').length
  const fleetAlertWarningCount = fleetAlerts.filter(alert => alert.severity === 'warning').length
  const fleetAlertHeaderSeverity = fleetAlertDangerCount > 0 ? 'danger' : fleetAlertWarningCount > 0 ? 'warning' : generators.length > 0 ? 'healthy' : 'unknown'
  const fleetAlertHeaderLabel = fleetAlerts.length > 0 ? `${fleetAlerts.length} alert${fleetAlerts.length === 1 ? '' : 's'}` : generators.length > 0 ? 'clear' : 'no inventory'
  const scheduleStatusCounts = scheduleHealth?.status_counts ?? {}
  const scheduleMaterializedCount = scheduleStatusCounts.materialized ?? scheduleStatusCounts.queued ?? 0
  const scheduleSkippedCount = scheduleStatusCounts.skipped ?? 0
  const scheduleCompletedCount = scheduleStatusCounts.completed ?? 0
  const scheduleHeaderSeverity = !scheduleHealth
    ? 'unknown'
    : scheduleHealth.failed > 0 || scheduleHealth.blocked > 0
      ? 'danger'
      : scheduleHealth.pending_approval > 0 || scheduleHealth.due > 0
        ? 'warning'
        : scheduleHealth.total > 0
          ? 'healthy'
          : 'unknown'
  const scheduleHeaderLabel = !scheduleHealth
    ? 'checking'
    : scheduleHealth.failed > 0
      ? `${scheduleHealth.failed} failed`
      : scheduleHealth.blocked > 0
        ? `${scheduleHealth.blocked} blocked`
        : scheduleHealth.pending_approval > 0
          ? `${scheduleHealth.pending_approval} approval`
          : scheduleHealth.due > 0
            ? `${scheduleHealth.due} due`
            : scheduleHealth.total > 0
              ? `${scheduleHealth.enabled}/${scheduleHealth.total} enabled`
              : 'no schedules'

  const refresh = useCallback(async () => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
      return
    }
    try {
      const [healthRes, testsRes, inventoryRes, scheduleHealthRes] = await Promise.all([
        api.getHealth(),
        api.listTests(activeProject?.id ? { project_id: activeProject.id } : undefined).catch(() => ({ tests: [] })),
        api.getSystemInventory().catch(() => null),
        api.getScheduleHealth().catch(() => null),
      ])
      setHealth(healthRes)
      setTests(testsRes.tests || [])
      setInventory(inventoryRes)
      setScheduleHealth(scheduleHealthRes)
    } catch {
      // API not available
    }
  }, [activeProject?.id, api])

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
      headerBadge: (
        <span className={`monitor-header-badge ${controllerHeaderSeverity}`}>
          {controllerStatusLabel}
        </span>
      ),
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
            <span className="monitor-label">Status</span>
            <span className="monitor-value">{controllerStatusLabel}</span>
          </div>
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
          <div className="monitor-row">
            <span className="monitor-label">Generator Inventory</span>
            <span className="monitor-value">{controllerGeneratorInventoryLabel}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Execution</span>
            <span className="monitor-value">{inventory?.controller.execution_mode || '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Instance</span>
            <span className="monitor-value">{inventory?.controller.id || '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Address</span>
            <span className="monitor-value cell-mono">{inventory?.controller.address || '-'}</span>
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
      headerBadge: (
        <span className={`monitor-header-badge ${telemetryHeaderSeverity}`}>
          {telemetryHeaderLabel}
        </span>
      ),
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
            <button type="button" className="telemetry-summary-card" onClick={() => setIsTelemetryModalOpen(true)}>
              <div className="telemetry-summary-metrics">
                <div className="telemetry-summary-metric">
                  <span className={`telemetry-summary-value ${telemetryDangerCount > 0 ? 'text-danger' : ''}`}>{telemetryDangerCount}</span>
                  <span className="telemetry-summary-label">Danger</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className={`telemetry-summary-value ${telemetryWarningCount > 0 ? 'telemetry-text-warning' : ''}`}>{telemetryWarningCount}</span>
                  <span className="telemetry-summary-label">Warning</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className="telemetry-summary-value">{telemetryDiagnostics.length}</span>
                  <span className="telemetry-summary-label">Total</span>
                </div>
              </div>
              <div className="telemetry-summary-details">
                <div className="telemetry-summary-headline">
                  <span className={`telemetry-diagnostic-badge ${telemetryLatestEntry ? telemetryDiagnosticSeverityClass(telemetryLatestEntry.diagnostic.severity) : 'healthy'}`}>
                    {telemetryLatestEntry?.diagnostic.severity || 'healthy'}
                  </span>
                  <span className="telemetry-diagnostic-time">{telemetryLatestEntry ? formatTelemetryDiagnosticTime(telemetryLatestEntry.timestamp) : 'No issues'}</span>
                </div>
                <div className="telemetry-diagnostic-summary">
                  {telemetryLatestEntry ? `${telemetryLatestEntry.test_name || telemetryLatestEntry.test_id}: ${telemetryLatestEntry.diagnostic.summary}` : 'No issues detected.'}
                </div>
                <div className="telemetry-summary-cta">View details</div>
              </div>
            </button>
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
      id: 'schedule-health',
      title: 'Schedule Health',
      headerBadge: (
        <span className={`monitor-header-badge ${scheduleHeaderSeverity}`}>
          {scheduleHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="4" width="18" height="18" rx="2" />
          <line x1="16" y1="2" x2="16" y2="6" />
          <line x1="8" y1="2" x2="8" y2="6" />
          <line x1="3" y1="10" x2="21" y2="10" />
          <path d="m9 16 2 2 4-5" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="telemetry-summary-metrics">
            <div className="telemetry-summary-metric">
              <span className={`telemetry-summary-value ${scheduleHealth?.due ? 'telemetry-text-warning' : ''}`}>{scheduleHealth?.due ?? '-'}</span>
              <span className="telemetry-summary-label">Due</span>
            </div>
            <div className="telemetry-summary-metric">
              <span className={`telemetry-summary-value ${scheduleHealth?.blocked ? 'text-danger' : ''}`}>{scheduleHealth?.blocked ?? '-'}</span>
              <span className="telemetry-summary-label">Blocked</span>
            </div>
            <div className="telemetry-summary-metric">
              <span className={`telemetry-summary-value ${scheduleHealth?.pending_approval ? 'telemetry-text-warning' : ''}`}>{scheduleHealth?.pending_approval ?? '-'}</span>
              <span className="telemetry-summary-label">Approval</span>
            </div>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Enabled Schedules</span>
            <span className="monitor-value">{scheduleHealth ? `${scheduleHealth.enabled}/${scheduleHealth.total}` : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Materialized</span>
            <span className="monitor-value">{scheduleHealth ? scheduleMaterializedCount.toLocaleString() : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Skipped</span>
            <span className="monitor-value">{scheduleHealth ? scheduleSkippedCount.toLocaleString() : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Completed</span>
            <span className="monitor-value">{scheduleHealth ? scheduleCompletedCount.toLocaleString() : '-'}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'generators',
      title: 'Generators',
      headerBadge: (
        <span className={`monitor-header-badge ${generatorHeaderSeverity}`}>
          {generatorHeaderLabel}
        </span>
      ),
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
          {generators.length ? (
            <button type="button" className="telemetry-summary-card" onClick={() => setIsGeneratorsModalOpen(true)}>
              <div className="telemetry-summary-metrics">
                <div className="telemetry-summary-metric">
                  <span className={`telemetry-summary-value ${generatorConnectedCount > 0 ? 'text-success' : ''}`}>{generatorConnectedCount}</span>
                  <span className="telemetry-summary-label">Online</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className={`telemetry-summary-value ${generatorIssueCount > 0 ? 'telemetry-text-warning' : ''}`}>{generatorIssueCount}</span>
                  <span className="telemetry-summary-label">Issues</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className="telemetry-summary-value">{generatorAvailableCapacity.toLocaleString()}</span>
                  <span className="telemetry-summary-label">Capacity</span>
                </div>
              </div>
              <div className="telemetry-summary-details">
                <div className="telemetry-summary-headline">
                  <span className={`telemetry-diagnostic-badge ${generatorIssueCount > 0 ? 'warning' : 'healthy'}`}>
                    {generatorIssueCount > 0 ? `${generatorIssueCount} issue${generatorIssueCount === 1 ? '' : 's'}` : 'all connected'}
                  </span>
                  <span className="telemetry-diagnostic-time">{generators.length} generator{generators.length === 1 ? '' : 's'}</span>
                </div>
                <div className="telemetry-diagnostic-summary">
                  {generatorActiveVUsers.toLocaleString()} active VUsers across {generators.length} generator{generators.length === 1 ? '' : 's'}.
                </div>
                <div className="telemetry-summary-cta">View details</div>
              </div>
            </button>
          ) : (
            <p className="monitor-hint">
              {inventory?.execution_mode === 'distributed'
                ? 'No generator inventory was returned by the controller.'
                : 'Local mode is active. Configure distributed generator targets to list generator pods here.'}
            </p>
          )}
        </div>
      ),
    },
    {
      id: 'fleet-alerts',
      title: 'Fleet Alerts',
      headerBadge: (
        <span className={`monitor-header-badge ${fleetAlertHeaderSeverity}`}>
          {fleetAlertHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" />
          <line x1="12" y1="9" x2="12" y2="13" />
          <line x1="12" y1="17" x2="12.01" y2="17" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {fleetAlerts.length === 0 ? (
            <p className="monitor-hint">
              {generators.length ? 'No fleet placement alerts detected from live generator inventory.' : 'No generator inventory available for fleet alert evaluation.'}
            </p>
          ) : (
            <div className="monitor-warning-list">
              {fleetAlerts.slice(0, 6).map(alert => (
                <p key={`${alert.type}:${alert.target}`} className={alert.severity === 'danger' ? 'text-danger' : ''}>
                  {alert.summary}
                </p>
              ))}
              {fleetAlerts.length > 6 ? <p>{fleetAlerts.length - 6} additional fleet alert{fleetAlerts.length - 6 === 1 ? '' : 's'} hidden.</p> : null}
            </div>
          )}
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
                      <span className="run-timeline-detail">{run.status} / {run.vusers.toLocaleString()} VUsers / {run.durationLabel}{run.status === 'PENDING' ? ` / ${run.queuePosition ? `Queue #${run.queuePosition}` : 'Queued'}` : ''}</span>
                      {run.status === 'PENDING' && (run.pendingReason || run.pendingSinceLabel) && (
                        <span className="run-timeline-detail">{run.pendingReason || 'waiting for capacity'}{run.pendingSinceLabel ? ` · since ${run.pendingSinceLabel}` : ''}</span>
                      )}
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
  ], [controllerGeneratorInventoryLabel, controllerHeaderSeverity, controllerStatusLabel, fleetAlertHeaderLabel, fleetAlertHeaderSeverity, fleetAlerts, generatorActiveVUsers, generatorAvailableCapacity, generatorConnectedCount, generatorHeaderLabel, generatorHeaderSeverity, generatorIssueCount, generators.length, health, inventory, scheduleCompletedCount, scheduleHeaderLabel, scheduleHeaderSeverity, scheduleHealth, scheduleMaterializedCount, scheduleSkippedCount, telemetryDangerCount, telemetryHeaderLabel, telemetryHeaderSeverity, telemetryLatestEntry, telemetryDiagnostics, telemetryWarningCount, timeline, websocketLabel, websocketValueClass])

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
              {card.headerBadge}
              {customizing && (
                <button className="btn-icon monitor-hide-btn" type="button" onClick={() => toggleCard(card.id)}>Hide</button>
              )}
            </div>
            {card.content}
          </div>
        ))}
      </div>

      {isTelemetryModalOpen && telemetryDiagnostics.length > 0 && (
        <div className="modal-overlay" onClick={() => setIsTelemetryModalOpen(false)}>
          <div className="modal modal-wide telemetry-modal" role="dialog" aria-modal="true" aria-labelledby="telemetry-diagnostics-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header telemetry-modal-header">
              <div>
                <h3 id="telemetry-diagnostics-title">Telemetry Diagnostics</h3>
                <p className="page-subtitle">Recent sampled telemetry issues by severity, condition, and affected dependency.</p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setIsTelemetryModalOpen(false)}>Close</button>
            </div>
            <div className="telemetry-modal-summary">
              <div className="telemetry-summary-metric">
                <span className={`telemetry-summary-value ${telemetryDangerCount > 0 ? 'text-danger' : ''}`}>{telemetryDangerCount}</span>
                <span className="telemetry-summary-label">Danger</span>
              </div>
              <div className="telemetry-summary-metric">
                <span className={`telemetry-summary-value ${telemetryWarningCount > 0 ? 'telemetry-text-warning' : ''}`}>{telemetryWarningCount}</span>
                <span className="telemetry-summary-label">Warning</span>
              </div>
              <div className="telemetry-summary-metric">
                <span className="telemetry-summary-value">{telemetryDiagnostics.length}</span>
                <span className="telemetry-summary-label">Total Samples</span>
              </div>
            </div>
            <div className="monitor-body telemetry-modal-body">
              <TelemetryDiagnosticList telemetryDiagnostics={telemetryDiagnostics} />
            </div>
          </div>
        </div>
      )}

      {isGeneratorsModalOpen && generators.length > 0 && (
        <div className="modal-overlay" onClick={() => setIsGeneratorsModalOpen(false)}>
          <div className="modal modal-wide telemetry-modal" role="dialog" aria-modal="true" aria-labelledby="generator-inventory-title" onClick={(event) => event.stopPropagation()}>
            <div className="modal-header telemetry-modal-header">
              <div>
                <h3 id="generator-inventory-title">Generator Inventory</h3>
                <p className="page-subtitle">Live generator health, capacity, resource usage, and runtime workload.</p>
              </div>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setIsGeneratorsModalOpen(false)}>Close</button>
            </div>
            <div className="telemetry-modal-summary">
              <div className="telemetry-summary-metric">
                <span className={`telemetry-summary-value ${generatorConnectedCount > 0 ? 'text-success' : ''}`}>{generatorConnectedCount}</span>
                <span className="telemetry-summary-label">Online</span>
              </div>
              <div className="telemetry-summary-metric">
                <span className={`telemetry-summary-value ${generatorIssueCount > 0 ? 'telemetry-text-warning' : ''}`}>{generatorIssueCount}</span>
                <span className="telemetry-summary-label">Issues</span>
              </div>
              <div className="telemetry-summary-metric">
                <span className="telemetry-summary-value">{generatorActiveVUsers.toLocaleString()}</span>
                <span className="telemetry-summary-label">Active VUsers</span>
              </div>
            </div>
            <div className="monitor-body telemetry-modal-body">
              <GeneratorInventoryList generators={generators} />
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function GeneratorInventoryList({ generators }: { generators: SystemInventoryResponse['generators'] }) {
  return (
    <div className="monitor-inventory-list">
      {generators.map(generator => (
        <div key={generator.target} className={`monitor-inventory-item ${generator.status !== 'connected' ? 'is-warning' : ''}`}>
          <div className="monitor-row">
            <span className="monitor-value">{generator.generator_id || generator.target}</span>
            <span className={`monitor-value ${generator.status === 'connected' ? 'text-success' : 'text-danger'}`}>{generator.status}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Target</span>
            <span className="monitor-value cell-mono">{generator.target}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Available Capacity</span>
            <span className="monitor-value">{generator.available_capacity.toLocaleString()}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Active VUsers</span>
            <span className="monitor-value">{generator.active_vusers.toLocaleString()}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">CPU Usage</span>
            <span className="monitor-value">{generator.cpu_percent.toFixed(1)}%</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">CPU Capacity</span>
            <span className="monitor-value">{formatCPUCores(generator.cpu_limit_cores)}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Memory Usage</span>
            <span className="monitor-value">{formatMemoryBytes(generator.memory_bytes)}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Memory Capacity</span>
            <span className="monitor-value">{formatMemoryBytes(generator.memory_limit_bytes)}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Goroutines</span>
            <span className="monitor-value">{generator.goroutine_count.toLocaleString()}</span>
          </div>
        </div>
      ))}
    </div>
  )
}

type FleetAlert = {
  type: string
  target: string
  severity: 'warning' | 'danger'
  summary: string
}

function buildFleetAlerts(generators: SystemInventoryResponse['generators'], controllerVersion?: string): FleetAlert[] {
  const alerts: FleetAlert[] = []
  const versions = new Map<string, number>()
  generators.forEach(generator => {
    const target = generator.generator_id || generator.target
    const status = (generator.status || '').toLowerCase()
    const drainState = (generator.drain_state || 'active').toLowerCase()
    const capacityState = (generator.capacity_state || '').toLowerCase()
    const compatibilityState = (generator.compatibility_state || 'compatible').toLowerCase()
    const version = generator.version || 'unknown'
    if (version !== 'unknown') {
      versions.set(version, (versions.get(version) || 0) + 1)
    }
    if (status && !['connected', 'healthy', 'ready'].includes(status)) {
      alerts.push({ type: 'unreachable', target, severity: 'danger', summary: `${target} is ${status}; placement cannot use this generator.` })
    }
    if (drainState !== 'active') {
      alerts.push({ type: 'drain', target, severity: 'warning', summary: `${target} is ${drainState}; new placements are excluded.` })
    }
    if (capacityState === 'saturated' || generator.available_capacity <= 0) {
      alerts.push({ type: 'saturation', target, severity: 'danger', summary: `${target} is saturated with ${generator.active_vusers.toLocaleString()} active VUsers.` })
    } else if (capacityState === 'constrained') {
      alerts.push({ type: 'saturation', target, severity: 'warning', summary: `${target} capacity is constrained: ${generator.available_capacity.toLocaleString()} slots remain.` })
    }
    if (compatibilityState && compatibilityState !== 'compatible') {
      const reason = generator.compatibility_issues?.[0] || compatibilityState
      alerts.push({ type: 'compatibility', target, severity: 'danger', summary: `${target} is not placement-compatible: ${reason}.` })
    }
  })
  const controllerMajor = (controllerVersion || '').split('.')[0]
  for (const [version, count] of versions) {
    const generatorMajor = version.split('.')[0]
    if (controllerMajor && generatorMajor && generatorMajor !== controllerMajor) {
      alerts.push({ type: 'version', target: version, severity: 'warning', summary: `${count} generator${count === 1 ? '' : 's'} report version ${version}, which differs from controller ${controllerVersion}.` })
    }
  }
  return alerts
}

function TelemetryDiagnosticList({ telemetryDiagnostics }: { telemetryDiagnostics: TelemetryDiagnosticEntry[] }) {
  return (
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
                    {sample.issue === 'broker'
                      ? `lag ${Math.round(sample.avg_consumer_lag_ms ?? 0)} ms · msg ${Math.round(sample.avg_message_latency_ms ?? 0)} ms · ${Math.round(sample.avg_messages_sent_per_sec ?? 0)}/${Math.round(sample.avg_messages_received_per_sec ?? 0)} msg/s`
                      : sample.issue === 'dead_letter'
                        ? `${sample.count ?? 0} dead-letter msg${sample.count === 1 ? '' : 's'}`
                      : sample.errors
                        ? `${sample.errors}/${sample.count ?? 0} errors`
                        : `p99 ${Math.round(sample.p99_ms ?? 0)} ms`}
                    {sample.issue !== 'broker' && sample.issue !== 'dead_letter' && sample.avg_ms ? ` · avg ${Math.round(sample.avg_ms)} ms` : ''}
                    {sample.dependency_target ? ` · ${sample.dependency_target}` : ''}
                  </span>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ))}
    </div>
  )
}

interface MonitorCard {
  id: string
  title: string
  icon: ReactNode
  headerBadge?: ReactNode
  content: ReactNode
}

interface TimelineRun {
  id: string
  name: string
  status: string
  vusers: number
  generators: number
  durationLabel: string
  queuePosition?: number
  pendingReason?: string
  pendingSinceLabel?: string
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
    .filter(test => ['QUEUED', 'PENDING', 'INITIALIZING', 'RUNNING', 'STOPPING'].includes(test.status))
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
      queuePosition: test.queue_position,
      pendingReason: test.pending_reason,
      pendingSinceLabel: test.pending_since ? formatTimelineTime(new Date(test.pending_since).getTime()) : undefined,
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

function formatCPUCores(value: number) {
  if (!value) return '0 cores'
  const rounded = Number(value.toFixed(value >= 10 ? 0 : 1))
  return `${rounded} ${rounded === 1 ? 'core' : 'cores'}`
}

function formatMemoryBytes(value: number) {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  return `${Number(size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1))} ${units[unitIndex]}`
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
