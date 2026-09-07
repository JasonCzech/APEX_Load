import { useState, useEffect, useLayoutEffect, useCallback, useMemo, useRef, type ReactNode } from 'react'
import { formatDatetimeLocalValue } from './newTestScheduleFormat'
import { type WebSocketConnectionState } from '../hooks/useWebSocket'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { useNavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { useHasTopbarContributionProvider, useTopbarContribution, type TopbarMetaTone } from '../hooks/TopbarContributionProvider'
import { APMIntegrationProfilesResponse, DashboardAuditEvent, FleetAlert, FleetCapacityResponse, HealthResponse, InfrastructureMonitorPacksResponse, ProvisionerInfo, RunCalendarCapacityDay, RunCalendarEvent, RunCalendarResponse, ScenarioScheduleHealth, SystemInventoryGenerator, SystemInventoryResponse, SystemReadinessResponse, SystemRestartCapabilities, SystemRestartRequest, SystemServicesResponse, TelemetryDiagnosticEntry } from '../types'
import { readStoredStringArray, writeStoredJSON } from '../utils/browserStorage'
import { formatTelemetryDiagnosticTime, telemetryDiagnosticSeverityClass } from './monitoringTelemetryDiagnostics'
import { formatThresholdAlertSummary, type ThresholdAlertEntry } from '../thresholdAlerts'
import ConfirmDestructiveModal, { type DestructiveConfirmation } from './ConfirmDestructiveModal'
import MonitoringCollectorsCard from './MonitoringCollectorsCard'
import ClustersCard from './ClustersCard'
import ComputeInfrastructureCard from './ComputeInfrastructureCard'
import FleetPoolsCard from './FleetPoolsCard'
import PoolAllocationCard from './PoolAllocationCard'
import FleetGeneratorsCard from './FleetGeneratorsCard'
import TimeslotsPanel from './TimeslotsPanel'
import { dialogBackdropProps } from './dialogBackdropProps'
import { interactiveElementProps } from './interactiveElementProps'
import { frontendBuildMetadata, type FrontendBuildComponentInfo } from '../buildMetadata'
import './CreateTestModal.css'
import './StatusBadge.css'

interface Props {
  connectionState: WebSocketConnectionState
  telemetryDiagnostics?: TelemetryDiagnosticEntry[]
  thresholdAlerts?: ThresholdAlertEntry[]
  onAcknowledgeThresholdAlert?: (key: string) => void
  onSelectTest?: (id: string) => void
  canManageSystem?: boolean
  initialCategoryId?: MonitorCategoryId
  activeCategoryId?: MonitorCategoryId
  onCategoryChange?: (categoryId: MonitorCategoryId) => void
  onScheduleForDate?: (prefillStartLocal: string) => void
  fleetEventSignal?: number
}

const EMPTY_GENERATORS: SystemInventoryResponse['generators'] = []
const EMPTY_SERVICES: SystemServicesResponse['services'] = []
const EMPTY_FLEET_ALERTS: FleetAlert[] = []

const MONITOR_CATEGORIES = [
  { id: 'overview', label: 'Overview', cardIds: ['system-health', 'threshold-alerts', 'production-readiness'] },
  // Cloud connections and execution targets are the normalized path; the
  // cluster registry remains available unchanged for Kubernetes deployments.
  { id: 'fleet', label: 'Fleet', cardIds: ['capacity', 'generators', 'fleet-alerts', 'fleet-audit', 'compute-infrastructure', 'fleet-clusters', 'fleet-pools', 'fleet-allocation', 'generator-metadata'] },
  { id: 'telemetry', label: 'Telemetry', cardIds: ['telemetry-diagnostics', 'apm-integrations', 'monitor-packs', 'collectors'] },
  { id: 'schedules', label: 'Schedules', cardIds: ['run-calendar'] },
] as const

export type MonitorCategoryId = typeof MONITOR_CATEGORIES[number]['id']
type RunCalendarView = 'month' | 'week' | 'day'
type RunCalendarFilter = 'all' | 'cron' | 'ad_hoc' | 'execution_plan' | 'current'
type MonitorSeverity = 'healthy' | 'unknown' | 'warning' | 'danger'
type RefreshWork = () => Promise<void>
type CardFreshnessResult = { cardId: string; ok: boolean }
type ReadinessFetchState = 'loading' | 'loaded' | 'stale' | 'error'

const FLEET_AUDIT_ACTION_PREFIXES = ['fleet.', 'generator.', 'provisioning.']
const FLEET_AUDIT_RESOURCE_TYPES = new Set(['fleet_alert', 'fleet_pool', 'generator', 'provisioning_request'])

function isFleetAuditEvent(event: DashboardAuditEvent): boolean {
  const action = (event.action || '').toLowerCase()
  const resourceType = (event.resource_type || '').toLowerCase()
  return FLEET_AUDIT_ACTION_PREFIXES.some(prefix => action.startsWith(prefix)) || FLEET_AUDIT_RESOURCE_TYPES.has(resourceType)
}

function formatAuditEventTime(value?: string): string {
  if (!value) return 'unknown'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

type RestartAction = {
  targetType: SystemRestartRequest['target_type']
  scope: SystemRestartRequest['scope']
  target: string
  title: string
  description: string
  targetLabel: string
  confirmLabel: string
  warning?: string
  forceDescription?: string
}

function formatReadinessStatus(status?: string): string {
  switch (status) {
    case 'ready':
      return 'Ready'
    case 'degraded':
      return 'Degraded'
    case 'not_ready':
      return 'Not Ready'
    default:
      return status || 'checking'
  }
}

function formatReadinessCheckName(name: string): string {
  const labels: Record<string, string> = {
    kubernetes_api: 'Kubernetes API',
    metadata_store: 'Metadata Store',
    cache_backend: 'Cache Backend',
    archive_writer: 'Archive Writer',
    generator_rpc: 'Generator RPC',
    telemetry_fanout: 'Telemetry Fan-out',
    auth_mode: 'Auth Mode',
    scheduler_state: 'Scheduler State',
  }
  if (labels[name]) return labels[name]
  return name
    .split('_')
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

type InfrastructureReadinessState = 'ready' | 'degraded' | 'not_ready' | 'not_configured' | 'not_reported' | 'checking' | 'unknown'

const REQUIRED_INFRASTRUCTURE_READINESS_CHECKS = ['kubernetes_api', 'metadata_store', 'cache_backend'] as const
const INFRASTRUCTURE_READINESS_CHECK_NAMES = new Set<string>(REQUIRED_INFRASTRUCTURE_READINESS_CHECKS)

function readinessDetailString(check: SystemReadinessResponse['checks'][number] | undefined, key: string): string {
  const value = check?.details?.[key]
  return typeof value === 'string' ? value.trim() : ''
}

function readinessDetailNumber(check: SystemReadinessResponse['checks'][number] | undefined, key: string): number | null {
  const value = check?.details?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function infrastructureReadinessState(check: SystemReadinessResponse['checks'][number] | undefined, fetchState: ReadinessFetchState): InfrastructureReadinessState {
  if (!check) {
    if (fetchState === 'loading') return 'checking'
    if (fetchState === 'error') return 'unknown'
    return 'not_reported'
  }
  if (check.name === 'kubernetes_api' && check.status === 'ready' && check.details?.enabled === false) return 'not_configured'
  if (check.status === 'not_configured' || check.status === 'not_reported' || check.status === 'checking') return check.status
  if (check.status === 'ready' || check.status === 'degraded' || check.status === 'not_ready') return check.status
  return 'unknown'
}

function infrastructureReadinessLabel(kind: 'kubernetes' | 'metadata' | 'cache', check: SystemReadinessResponse['checks'][number] | undefined): string {
  if (kind === 'kubernetes') return 'Kubernetes API'
  if (kind === 'metadata') {
    switch (readinessDetailString(check, 'store_type').toLowerCase()) {
      case 'postgres':
      case 'postgresql':
        return 'PostgreSQL Metadata Store'
      case 'sqlite':
        return 'SQLite Metadata Store'
      default:
        return 'Metadata Store'
    }
  }
  switch (readinessDetailString(check, 'cache_backend').toLowerCase()) {
    case 'redis':
      return 'Redis Cache'
    case 'valkey':
      return 'Valkey Cache'
    case 'memory':
      return 'In-memory Cache'
    default:
      return 'Cache Backend'
  }
}

function infrastructureReadinessStatusLabel(state: InfrastructureReadinessState): string {
  switch (state) {
    case 'ready': return 'Ready'
    case 'degraded': return 'Degraded'
    case 'not_ready': return 'Not Ready'
    case 'not_configured': return 'Not applicable'
    case 'not_reported': return 'Not Reported'
    case 'checking': return 'Checking'
    default: return 'Unknown'
  }
}

function infrastructureReadinessStatusClass(state: InfrastructureReadinessState): string {
  return `monitor-infrastructure-status-${state.replace('_', '-')}`
}

function infrastructureStateMonitorSeverity(state: InfrastructureReadinessState): MonitorSeverity {
  switch (state) {
    case 'not_ready':
      return 'danger'
    case 'degraded':
      return 'warning'
    case 'ready':
    case 'not_configured':
      return 'healthy'
    default:
      return 'unknown'
  }
}

function infrastructureReadinessSummary(
  state: InfrastructureReadinessState,
  check: SystemReadinessResponse['checks'][number] | undefined,
): string {
  switch (state) {
    case 'checking':
      return 'Waiting for readiness data.'
    case 'unknown':
      return check?.message || 'The readiness endpoint is unavailable.'
    case 'not_reported':
      return 'Not reported by this controller.'
    case 'not_configured':
      return 'This runtime does not use this optional integration.'
    default:
      return check?.message || infrastructureReadinessStatusLabel(state)
  }
}

function monitorSeverityRank(severity: MonitorSeverity): number {
  switch (severity) {
    case 'danger':
      return 3
    case 'warning':
      return 2
    case 'unknown':
      return 1
    default:
      return 0
  }
}

function worstMonitorSeverity(...severities: MonitorSeverity[]): MonitorSeverity {
  return severities.reduce((worst, severity) => (
    monitorSeverityRank(severity) > monitorSeverityRank(worst) ? severity : worst
  ), 'healthy' as MonitorSeverity)
}

function statusMonitorSeverity(status?: string): MonitorSeverity {
  const normalized = (status || '').trim().toLowerCase()
  if (!normalized || normalized === 'unknown' || normalized === 'checking') return 'unknown'
  if (['healthy', 'ok', 'ready', 'running', 'active', 'connected'].includes(normalized)) return 'healthy'
  if (['failed', 'unhealthy', 'unreachable', 'unavailable', 'not_ready', 'error'].includes(normalized)) return 'danger'
  if (['degraded', 'stopping', 'draining', 'cordoned', 'paused'].includes(normalized)) return 'warning'
  return 'warning'
}

function websocketMonitorSeverity(connectionState: WebSocketConnectionState): MonitorSeverity {
  if (connectionState === 'connected') return 'healthy'
  if (connectionState === 'disconnected') return 'danger'
  return 'warning'
}

function groupedMonitorHeaderLabel(severity: MonitorSeverity): string {
  switch (severity) {
    case 'danger':
      return 'attention'
    case 'warning':
      return 'degraded'
    case 'unknown':
      return 'checking'
    default:
      return 'healthy'
  }
}

const SCHEDULE_SWEEP_STALE_MS = 2 * 60 * 1000

function scheduleHealthPresentation(health: ScenarioScheduleHealth | null, now = Date.now()): {
  severity: MonitorSeverity
  label: string
  summary: string
} {
  if (!health) {
    return { severity: 'unknown', label: 'checking', summary: 'Schedule health is unavailable.' }
  }

  const sweeps = Object.entries(health.sweeps || {})
  const domainSweeps = sweeps.filter(([name]) => name !== 'scheduler')
  const failedDomainSweeps = domainSweeps.filter(([, sweep]) => Boolean(sweep.last_error?.trim()))
  const schedulerFailure = sweeps.filter(([name, sweep]) => name === 'scheduler' && Boolean(sweep.last_error?.trim()))
  const failedSweeps = failedDomainSweeps.length > 0 ? failedDomainSweeps : schedulerFailure
  const freshnessSweeps = domainSweeps.length > 0 ? domainSweeps : sweeps
  const staleSweeps = freshnessSweeps.filter(([, sweep]) => {
    if (!sweep.last_sweep_at) return true
    const lastSweepAt = Date.parse(sweep.last_sweep_at)
    return Number.isNaN(lastSweepAt) || now - lastSweepAt > SCHEDULE_SWEEP_STALE_MS
  })
  const scenarioConfigured = health.total
  const planConfigured = health.execution_plan_total || 0
  const oneTimeConfigured = health.one_time_scheduled || 0
  const configured = scenarioConfigured + planConfigured + oneTimeConfigured
  const due = health.due + (health.execution_plan_due || 0) + (health.one_time_due || 0)
  const sweepNames = new Set(sweeps.map(([name]) => name))
  const missingSweeps = [
    scenarioConfigured > 0 && !sweepNames.has('scenario') ? 'scenario' : '',
    planConfigured > 0 && !sweepNames.has('execution_plan') ? 'execution_plan' : '',
    oneTimeConfigured > 0 && !sweepNames.has('one_time') ? 'one_time' : '',
  ].filter(Boolean)

  const details = [
    scenarioConfigured > 0 ? `${health.enabled}/${scenarioConfigured} scenario schedules enabled` : '',
    planConfigured > 0 ? `${health.execution_plan_enabled || 0}/${planConfigured} execution-plan schedules enabled` : '',
    oneTimeConfigured > 0 ? `${oneTimeConfigured} one-time runs scheduled` : '',
    due > 0 ? `${due} due across all schedule domains` : '',
    health.failed > 0 ? `${health.failed} scenario occurrences failed` : '',
    health.blocked > 0 ? `${health.blocked} scenario occurrences blocked` : '',
    (health.execution_plan_blocked || 0) > 0 ? `${health.execution_plan_blocked} execution-plan schedules blocked` : '',
    health.pending_approval > 0 ? `${health.pending_approval} pending approval` : '',
    health.storage_error ? `Storage: ${health.storage_error}` : '',
    failedSweeps.length > 0 ? `Failed sweeps: ${failedSweeps.map(([name]) => name).join(', ')}` : '',
    missingSweeps.length > 0 ? `Missing sweep evidence: ${missingSweeps.join(', ')}` : '',
    staleSweeps.length > 0 ? `Stale sweeps: ${staleSweeps.map(([name]) => name).join(', ')}` : '',
  ].filter(Boolean).join('. ')

  if (health.storage_error) return { severity: 'danger', label: 'storage error', summary: details }
  if (health.is_leader === false) return { severity: 'danger', label: 'standby response', summary: `Schedule health was served by a non-leader controller. ${details}`.trim() }
  if (failedSweeps.length > 0) return { severity: 'danger', label: `${failedSweeps.length} sweep failed`, summary: details }
  if (health.failed > 0) return { severity: 'danger', label: `${health.failed} failed`, summary: details }
  if (health.blocked > 0) return { severity: 'danger', label: `${health.blocked} blocked`, summary: details }
  if ((health.execution_plan_blocked || 0) > 0) return { severity: 'danger', label: `${health.execution_plan_blocked} plan blocked`, summary: details }
  if (missingSweeps.length > 0) return { severity: 'warning', label: `${missingSweeps.length} sweep pending`, summary: details }
  if (staleSweeps.length > 0) return { severity: 'warning', label: `${staleSweeps.length} sweep stale`, summary: details }
  if (health.pending_approval > 0) return { severity: 'warning', label: `${health.pending_approval} approval`, summary: details }
  if (due > 0) return { severity: 'warning', label: `${due} due`, summary: details }
  if (configured > 0) return { severity: 'healthy', label: `${configured} configured`, summary: details }
  return { severity: 'unknown', label: 'no schedules', summary: 'No schedules configured.' }
}

type ApiCallResult<T> =
  | { ok: true; value: T }
  | { ok: false }

async function settleApiCall<T>(request: () => Promise<T>): Promise<ApiCallResult<T>> {
  try {
    return { ok: true, value: await request() }
  } catch {
    return { ok: false }
  }
}

function formatBuildVersion(version?: string, displayVersion?: string): string {
  const raw = (displayVersion || version || '').trim()
  if (!raw) return '-'
  if (/^[0-9a-fA-F]{12,64}$/.test(raw)) return `dev+${raw.slice(0, 7)}`
  if (raw.length > 1 && /^[vV][0-9]/.test(raw)) return raw
  return /^[0-9]/.test(raw) ? `v${raw}` : raw
}

function formatBuildDate(buildTime?: string): string {
  const raw = (buildTime || '').trim()
  if (!raw) return '-'
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return raw
  return parsed.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

function fullBuildTimeTitle(buildTime?: string): string | undefined {
  const raw = (buildTime || '').trim()
  if (!raw) return undefined
  const parsed = new Date(raw)
  return Number.isNaN(parsed.getTime()) ? raw : parsed.toLocaleString()
}

interface ComponentBuildRow {
  id: string
  name: string
  version: string
  buildDate: string
  buildTime?: string
}

function frontendBuildRow(id: string, name: string, info: FrontendBuildComponentInfo): ComponentBuildRow {
  return {
    id,
    name,
    version: formatBuildVersion(info.version),
    buildDate: formatBuildDate(info.buildTime),
    buildTime: fullBuildTimeTitle(info.buildTime),
  }
}

function controllerBuildRow(health: HealthResponse | null): ComponentBuildRow {
  return {
    id: 'controller',
    name: 'Controller',
    version: formatBuildVersion(health?.version, health?.display_version),
    buildDate: formatBuildDate(health?.build_time),
    buildTime: fullBuildTimeTitle(health?.build_time),
  }
}

const STATIC_FRONTEND_BUILD_ROWS = [
  frontendBuildRow('vite-server', 'Vite Server', frontendBuildMetadata.viteServer),
  frontendBuildRow('ux', 'UX', frontendBuildMetadata.ux),
]

function restartErrorMessage(error: unknown): string {
  if (typeof error === 'object' && error !== null) {
    const payload = (error as { payload?: unknown }).payload
    if (typeof payload === 'object' && payload !== null) {
      const message = (payload as { message?: unknown; error?: unknown }).message || (payload as { error?: unknown }).error
      if (typeof message === 'string' && message.trim()) return message
    }
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string' && message.trim()) return message
  }
  return 'Restart request failed.'
}

export default function MonitoringPage({ connectionState, telemetryDiagnostics = [], thresholdAlerts = [], onAcknowledgeThresholdAlert, onSelectTest, canManageSystem = false, initialCategoryId = 'overview', activeCategoryId: controlledCategoryId, onCategoryChange, onScheduleForDate, fleetEventSignal = 0 }: Props) {
  const api = useApi()
  const { runGuarded } = useNavigationGuardRunner()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const refreshGenerationRef = useRef(0)
  const refreshScopeRef = useRef(activeProjectId)
  const initializedScopeRef = useRef<string | null>(null)
  const initialRefreshCompleteRef = useRef(false)
  const calendarRefreshRangeRef = useRef('')
  const refreshInFlightRef = useRef<Promise<void> | null>(null)
  const readinessRefreshInFlightRef = useRef<Promise<boolean> | null>(null)
  const queuedRefreshRef = useRef<RefreshWork | null>(null)
  const fleetEventGenerationRef = useRef(0)
  const renderedProjectScopeRef = useRef(activeProjectId)
  const renderedCategoryRef = useRef<MonitorCategoryId>(controlledCategoryId ?? initialCategoryId)
  refreshScopeRef.current = activeProjectId
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [scheduleHealth, setScheduleHealth] = useState<ScenarioScheduleHealth | null>(null)
  const [runCalendar, setRunCalendar] = useState<RunCalendarResponse | null>(null)
  const [calendarView, setCalendarView] = useState<RunCalendarView>('month')
  const [calendarAnchor, setCalendarAnchor] = useState<Date>(() => new Date())
  const [calendarFilter, setCalendarFilter] = useState<RunCalendarFilter>('all')
  const [selectedCalendarEventId, setSelectedCalendarEventId] = useState<string | null>(null)
  const [inventory, setInventory] = useState<SystemInventoryResponse | null>(null)
  const [systemReadiness, setSystemReadiness] = useState<SystemReadinessResponse | null>(null)
  const [readinessFetchState, setReadinessFetchState] = useState<ReadinessFetchState>('loading')
  const [systemServices, setSystemServices] = useState<SystemServicesResponse | null>(null)
  const [apmProfiles, setApmProfiles] = useState<APMIntegrationProfilesResponse | null>(null)
  const [monitorPacks, setMonitorPacks] = useState<InfrastructureMonitorPacksResponse | null>(null)
  // null means "never fetched successfully" so an unavailable fleet alert feed is
  // never reported as "clear"; an empty array means "fetched, no alerts".
  const [fleetAlerts, setFleetAlerts] = useState<FleetAlert[] | null>(null)
  const [fleetAlertActionError, setFleetAlertActionError] = useState<string | null>(null)
  const [fleetAlertActionBusy, setFleetAlertActionBusy] = useState<string | null>(null)
  const [fleetAuditEvents, setFleetAuditEvents] = useState<DashboardAuditEvent[]>([])
  const [fleetCapacity, setFleetCapacity] = useState<FleetCapacityResponse | null>(null)
  const [provisioner, setProvisioner] = useState<ProvisionerInfo | null>(null)
  const [restartCapabilities, setRestartCapabilities] = useState<SystemRestartCapabilities | null>(null)
  const [restartAction, setRestartAction] = useState<RestartAction | null>(null)
  const [restartError, setRestartError] = useState<string | null>(null)
  const [restartMessage, setRestartMessage] = useState<string | null>(null)
  const [restartLoading, setRestartLoading] = useState(false)
  const [isTelemetryModalOpen, setIsTelemetryModalOpen] = useState(false)
  const [isGeneratorsModalOpen, setIsGeneratorsModalOpen] = useState(false)
  const [internalCategoryId, setInternalCategoryId] = useState<MonitorCategoryId>(initialCategoryId)
  const activeCategoryId = controlledCategoryId ?? internalCategoryId
  const [cardOrder, setCardOrder] = useState<string[]>(() => readStoredStringArray('apexload.monitoring.cardOrder'))
  const [lastRefreshed, setLastRefreshed] = useState<Date | null>(null)
  const [staleCardIds, setStaleCardIds] = useState<string[]>([])
  const [readinessLiveAnnouncement, setReadinessLiveAnnouncement] = useState('')
  const readinessLiveAnnouncementRef = useRef('')
  const telemetryDialogRef = useRef<HTMLDivElement>(null)
  const generatorsDialogRef = useRef<HTMLDivElement>(null)

  useModalDialog({
    dialogRef: telemetryDialogRef,
    enabled: isTelemetryModalOpen,
    onClose: () => setIsTelemetryModalOpen(false),
  })
  useModalDialog({
    dialogRef: generatorsDialogRef,
    enabled: isGeneratorsModalOpen,
    onClose: () => setIsGeneratorsModalOpen(false),
  })

  const recordCardFreshness = useCallback((results: CardFreshnessResult[]) => {
    setStaleCardIds(current => {
      const next = new Set(current)
      results.forEach(result => {
        if (result.ok) next.delete(result.cardId)
        else next.add(result.cardId)
      })
      return Array.from(next).sort()
    })
  }, [])

  const runCoalescedRefresh = useCallback((work: RefreshWork): Promise<void> => {
    if (refreshInFlightRef.current) {
      // Keep only the newest requested refresh while one is running. The active
      // request drains this slot before it resolves, preventing poll backlogs.
      queuedRefreshRef.current = work
      return refreshInFlightRef.current
    }

    const request = (async () => {
      let nextWork: RefreshWork | null = work
      while (nextWork) {
        const currentWork = nextWork
        queuedRefreshRef.current = null
        await currentWork()
        nextWork = queuedRefreshRef.current
      }
    })().finally(() => {
      if (refreshInFlightRef.current === request) refreshInFlightRef.current = null
    })
    refreshInFlightRef.current = request
    return request
  }, [])

  useLayoutEffect(() => {
    if (renderedProjectScopeRef.current === activeProjectId) return
    renderedProjectScopeRef.current = activeProjectId
    refreshGenerationRef.current += 1
    fleetEventGenerationRef.current += 1
    setScheduleHealth(null)
    setRunCalendar(null)
    setSelectedCalendarEventId(null)
    setFleetAlerts(null)
    setFleetAuditEvents([])
    setFleetCapacity(null)
    setProvisioner(null)
    setLastRefreshed(null)
    setStaleCardIds([])
    queuedRefreshRef.current = null
  }, [activeProjectId])
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const apiHealthSeverity = statusMonitorSeverity(health?.status)
  const websocketSeverity = websocketMonitorSeverity(connectionState)
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
  const canRestart = canManageSystem && Boolean(restartCapabilities?.enabled)
  const restartDisabledReason = canManageSystem
    ? restartCapabilities?.disabled_reason || (restartCapabilities ? '' : 'Restart capability status is unavailable.')
    : ''
  const generatorConnectedCount = generators.filter(generator => generator.status === 'connected').length
  const generatorIssueCount = generators.length - generatorConnectedCount
  const generatorAvailableCapacity = generators.reduce((sum, generator) => sum + generator.available_capacity, 0)
  const generatorActiveVUsers = generators.reduce((sum, generator) => sum + generator.active_vusers, 0)
  const controllerStatusLabel = inventory?.controller.status || health?.status || 'unknown'
  const controllerHeaderSeverity: MonitorSeverity = statusMonitorSeverity(controllerStatusLabel)
  const controlPlaneHeaderSeverity = worstMonitorSeverity(apiHealthSeverity, websocketSeverity, controllerHeaderSeverity)
  const controlPlaneHeaderLabel = groupedMonitorHeaderLabel(controlPlaneHeaderSeverity)
  const controllerGeneratorInventoryLabel = inventory?.controller.execution_mode === 'distributed'
    ? `${generatorConnectedCount}/${generators.length} connected`
    : inventory?.controller.execution_mode === 'local'
      ? 'Local mode'
      : '-'
  const generatorHeaderSeverity: MonitorSeverity = generatorIssueCount > 0 ? 'warning' : generators.length > 0 ? 'healthy' : 'unknown'
  const generatorHeaderLabel = generators.length > 0
    ? `${generatorConnectedCount}/${generators.length} online`
    : 'no inventory'
  const fleetAlertList = fleetAlerts ?? EMPTY_FLEET_ALERTS
  // Not fetched yet, or the latest fetch failed: the feed cannot be summarised as clear.
  const fleetAlertsUnknown = fleetAlerts === null || staleCardIds.includes('fleet-alerts')
  const fleetAlertDangerCount = fleetAlertList.filter(alert => alert.severity === 'danger').length
  const fleetAlertWarningCount = fleetAlertList.filter(alert => alert.severity === 'warning').length
  const fleetAlertHeaderSeverity: MonitorSeverity = fleetAlertDangerCount > 0
    ? 'danger'
    : fleetAlertWarningCount > 0 ? 'warning' : fleetAlertsUnknown ? 'unknown' : generators.length > 0 ? 'healthy' : 'unknown'
  const fleetAlertHeaderLabel = fleetAlertList.length > 0
    ? `${fleetAlertList.length} alert${fleetAlertList.length === 1 ? '' : 's'}`
    : fleetAlertsUnknown ? 'checking' : generators.length > 0 ? 'clear' : 'no inventory'
  const activeThresholdAlerts = thresholdAlerts.filter(alert => alert.active)
  const unacknowledgedThresholdAlerts = activeThresholdAlerts.filter(alert => !alert.acknowledged)
  const thresholdAlertHeaderSeverity: MonitorSeverity = unacknowledgedThresholdAlerts.some(alert => alert.action === 'stop_requested' || alert.metric === 'error_pct')
    ? 'danger'
    : unacknowledgedThresholdAlerts.length > 0 ? 'warning' : 'healthy'
  const thresholdAlertHeaderLabel = activeThresholdAlerts.length === 0
    ? 'clear'
    : unacknowledgedThresholdAlerts.length > 0
      ? `${unacknowledgedThresholdAlerts.length} alert${unacknowledgedThresholdAlerts.length === 1 ? '' : 's'}`
      : 'all acknowledged'
  const fleetAuditHeaderSeverity: MonitorSeverity = !canManageSystem ? 'unknown' : fleetAuditEvents.length > 0 ? 'healthy' : 'unknown'
  const fleetAuditHeaderLabel = canManageSystem
    ? fleetAuditEvents.length > 0 ? `${fleetAuditEvents.length} event${fleetAuditEvents.length === 1 ? '' : 's'}` : 'quiet'
    : 'admin only'
  const schedulePresentation = scheduleHealthPresentation(scheduleHealth)
  const scheduleHeaderSeverity = schedulePresentation.severity
  const readinessReadyCount = systemReadiness?.checks.filter(check => check.status === 'ready').length ?? 0
  const readinessDegradedCount = systemReadiness?.checks.filter(check => check.status === 'degraded').length ?? 0
  const readinessBlockedCount = systemReadiness?.checks.filter(check => check.status === 'not_ready').length ?? 0
  const readinessChecksByName = useMemo(
    () => new Map((systemReadiness?.checks ?? []).map(check => [check.name, check])),
    [systemReadiness],
  )
  const requiredInfrastructureStates = REQUIRED_INFRASTRUCTURE_READINESS_CHECKS.map(requiredName => (
    infrastructureReadinessState(readinessChecksByName.get(requiredName), readinessFetchState)
  ))
  const missingRequiredInfrastructureCount = REQUIRED_INFRASTRUCTURE_READINESS_CHECKS.filter(
    requiredName => !readinessChecksByName.has(requiredName),
  ).length
  const unknownReportedInfrastructureCount = REQUIRED_INFRASTRUCTURE_READINESS_CHECKS.filter((requiredName, index) => (
    readinessChecksByName.has(requiredName)
    && infrastructureStateMonitorSeverity(requiredInfrastructureStates[index]) === 'unknown'
  )).length
  const readinessDisplayedCheckCount = (systemReadiness?.checks.length ?? 0) + missingRequiredInfrastructureCount
  const hasAllRequiredInfrastructureChecks = systemReadiness !== null && missingRequiredInfrastructureCount === 0
  const allConfiguredReadinessChecksPassing = (systemReadiness?.checks.length ?? 0) > 0
    && systemReadiness?.checks.every(check => check.status === 'ready' || check.status === 'not_configured') === true
  const aggregateReadinessSeverity: MonitorSeverity = systemReadiness?.status === 'not_ready'
    ? 'danger'
    : systemReadiness?.status === 'degraded'
      ? 'warning'
      : systemReadiness?.status === 'ready'
        ? 'healthy'
        : 'unknown'
  const reportedReadinessSeverity = worstMonitorSeverity(
    aggregateReadinessSeverity,
    ...requiredInfrastructureStates.map(infrastructureStateMonitorSeverity),
  )
  const readinessHeaderSeverity = worstMonitorSeverity(
    reportedReadinessSeverity,
    readinessFetchState === 'stale' ? 'warning' : 'healthy',
  )
  const readinessPrimaryLabel = readinessFetchState === 'loading' && !systemReadiness
    ? 'Checking'
    : readinessFetchState === 'error' && !systemReadiness
      ? 'Unavailable'
      : reportedReadinessSeverity === 'danger'
        ? 'Not Ready'
        : reportedReadinessSeverity === 'warning'
          ? 'Degraded'
          : readinessFetchState === 'stale'
            ? 'Stale'
            : reportedReadinessSeverity === 'unknown'
              ? 'Unknown'
              : formatReadinessStatus(systemReadiness?.status)
  const readinessHeaderQualifiers = systemReadiness ? [
    missingRequiredInfrastructureCount > 0 ? `${missingRequiredInfrastructureCount} unreported` : '',
    unknownReportedInfrastructureCount > 0 ? `${unknownReportedInfrastructureCount} unknown` : '',
    readinessFetchState === 'stale' && (reportedReadinessSeverity === 'danger' || reportedReadinessSeverity === 'warning') ? 'stale data' : '',
  ].filter(Boolean) : []
  const readinessHeaderLabel = [readinessPrimaryLabel, ...readinessHeaderQualifiers].join(' · ')
  const readinessFailureChecks = useMemo(() => (
    (systemReadiness?.checks ?? [])
      .filter(check => !INFRASTRUCTURE_READINESS_CHECK_NAMES.has(check.name))
      .filter(check => !['ready', 'not_configured', 'not_reported', 'checking'].includes(check.status))
      .slice(0, 4)
  ), [systemReadiness])
  const apmProfileCount = apmProfiles?.profiles.length ?? 0
  const apmConfiguredCount = apmProfiles?.profiles.filter(profile => profile.status === 'configured' || profile.status === 'supported').length ?? 0
  const apmHeaderSeverity: MonitorSeverity = !apmProfiles ? 'unknown' : apmConfiguredCount > 0 ? 'healthy' : 'warning'
  const apmHeaderLabel = !apmProfiles ? 'checking' : `${apmConfiguredCount}/${apmProfileCount} ready`
  const monitorPackCount = monitorPacks?.packs.length ?? 0
  const monitorPackSupportedCount = monitorPacks?.packs.filter(pack => pack.status === 'supported').length ?? 0
  const monitorPackContractCount = monitorPacks?.packs.filter(pack => pack.status === 'integration_contract').length ?? 0
  const monitorPackHeaderSeverity: MonitorSeverity = !monitorPacks ? 'unknown' : monitorPackSupportedCount === monitorPackCount ? 'healthy' : 'warning'
  const monitorPackHeaderLabel = !monitorPacks ? 'checking' : `${monitorPackSupportedCount}/${monitorPackCount} native`
  const componentBuildRows = useMemo(() => [
    controllerBuildRow(health),
    ...STATIC_FRONTEND_BUILD_ROWS,
  ], [health])
  const dashboardOrigin = getCurrentDashboardOrigin()
  const activeServices = useMemo(() => mergeDashboardService(systemServices?.services ?? EMPTY_SERVICES, dashboardOrigin), [dashboardOrigin, systemServices])
  const backendServices = useMemo(() => activeServices.filter(service => service.kind !== 'frontend'), [activeServices])
  const frontendServices = useMemo(() => activeServices.filter(service => service.kind === 'frontend'), [activeServices])
  const activeServiceCount = activeServices.filter(service => isActiveServiceStatus(service.status)).length
  const serviceIssueCount = activeServices.filter(service => !isActiveServiceStatus(service.status)).length
  const servicesHeaderSeverity: MonitorSeverity = !systemServices && backendServices.length === 0
    ? 'unknown'
    : serviceIssueCount > 0
      ? 'warning'
      : backendServices.length > 0
        ? 'healthy'
        : 'warning'
  const servicesHeaderLabel = !systemServices && backendServices.length === 0
    ? 'checking'
    : backendServices.length === 0
      ? 'frontend only'
      : `${activeServiceCount}/${activeServices.length} active`
  const readinessServicesHeaderSeverity = worstMonitorSeverity(readinessHeaderSeverity, servicesHeaderSeverity)
  const readinessServicesHeaderLabel = readinessServicesHeaderSeverity === servicesHeaderSeverity && servicesHeaderSeverity !== 'healthy'
    ? servicesHeaderLabel
    : readinessServicesHeaderSeverity === readinessHeaderSeverity && readinessHeaderSeverity !== 'healthy'
      ? readinessHeaderLabel
      : groupedMonitorHeaderLabel(readinessServicesHeaderSeverity)
  const readinessLiveSummary = [
    `Readiness ${readinessHeaderLabel}.`,
    ...REQUIRED_INFRASTRUCTURE_READINESS_CHECKS.map((name, index) => (
      `${formatReadinessCheckName(name)} ${infrastructureReadinessStatusLabel(requiredInfrastructureStates[index])}.`
    )),
  ].join(' ')

  useEffect(() => {
    if (readinessLiveAnnouncementRef.current === readinessLiveSummary) return
    readinessLiveAnnouncementRef.current = readinessLiveSummary
    setReadinessLiveAnnouncement(readinessLiveSummary)
  }, [readinessLiveSummary])
  const calendarRange = useMemo(() => getRunCalendarVisibleRange(calendarView, calendarAnchor), [calendarView, calendarAnchor])
  const calendarEvents = useMemo(() => filterRunCalendarEvents(runCalendar?.events ?? [], calendarFilter), [runCalendar, calendarFilter])
  const calendarModel = useMemo(() => buildRunCalendarModel(calendarView, calendarAnchor, calendarEvents), [calendarView, calendarAnchor, calendarEvents])
  const selectedCalendarEvent = useMemo(
    () => (selectedCalendarEventId ? (runCalendar?.events ?? []).find(event => event.id === selectedCalendarEventId) ?? null : null),
    [runCalendar, selectedCalendarEventId],
  )
  const calendarHeaderSeverity: MonitorSeverity = !runCalendar ? 'unknown' : runCalendar.truncated ? 'warning' : calendarEvents.some(event => isRunCalendarProblemStatus(event.status)) ? 'danger' : 'healthy'
  const calendarHeaderLabel = !runCalendar
    ? 'checking'
    : runCalendar.truncated
      ? `${calendarEvents.length.toLocaleString()} shown`
      : `${calendarEvents.length.toLocaleString()} run${calendarEvents.length === 1 ? '' : 's'}`

  const refreshReadiness = useCallback((): Promise<boolean> => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return Promise.resolve(false)
    if (readinessRefreshInFlightRef.current) return readinessRefreshInFlightRef.current

    const generation = refreshGenerationRef.current
    const projectScope = activeProjectId
    const request = (async () => {
      const result = await settleApiCall(() => api.getSystemReadiness())
      if (generation !== refreshGenerationRef.current || projectScope !== refreshScopeRef.current) return false
      if (result.ok) {
        setSystemReadiness(result.value)
        setReadinessFetchState('loaded')
        return true
      }
      // Preserve a previously loaded response as stale data. If no response
      // has ever loaded, stop presenting the settled failure as "Checking".
      setReadinessFetchState(current => current === 'loaded' || current === 'stale' ? 'stale' : 'error')
      return false
    })().finally(() => {
      if (readinessRefreshInFlightRef.current === request) readinessRefreshInFlightRef.current = null
    })
    readinessRefreshInFlightRef.current = request
    return request
  }, [activeProjectId, api])

  const refresh = useCallback((categoryId: MonitorCategoryId = activeCategoryId) => runCoalescedRefresh(async () => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return
    const generation = refreshGenerationRef.current
    const projectScope = activeProjectId
    const isCurrent = () => generation === refreshGenerationRef.current && projectScope === refreshScopeRef.current

    if (categoryId === 'overview') {
      const [healthRes, inventoryRes, restartCapsRes, readinessOk, servicesRes] = await Promise.all([
        settleApiCall(() => api.getHealth()),
        settleApiCall(() => api.getSystemInventory()),
        canManageSystem ? settleApiCall(() => api.getRestartCapabilities()) : Promise.resolve({ ok: false } as const),
        refreshReadiness(),
        settleApiCall(() => api.getSystemServices()),
      ])
      if (!isCurrent()) return
      if (healthRes.ok) setHealth(healthRes.value)
      if (inventoryRes.ok) setInventory(inventoryRes.value)
      if (restartCapsRes.ok) setRestartCapabilities(restartCapsRes.value)
      if (servicesRes.ok) setSystemServices(servicesRes.value)
      recordCardFreshness([
        { cardId: 'system-health', ok: healthRes.ok && inventoryRes.ok && (!canManageSystem || restartCapsRes.ok) },
        { cardId: 'production-readiness', ok: readinessOk && servicesRes.ok },
      ])
      if ([healthRes, inventoryRes, restartCapsRes, servicesRes].some(result => result.ok) || readinessOk) setLastRefreshed(new Date())
      return
    }

    if (categoryId === 'fleet') {
      const [inventoryRes, restartCapsRes, fleetAlertsRes, fleetAuditRes, fleetCapacityRes, provisionerRes] = await Promise.all([
        settleApiCall(() => api.getSystemInventory()),
        canManageSystem ? settleApiCall(() => api.getRestartCapabilities()) : Promise.resolve({ ok: false } as const),
        settleApiCall(() => api.getFleetAlerts()),
        canManageSystem ? settleApiCall(() => api.listAuthAuditEvents({ limit: 20 })) : Promise.resolve({ ok: false } as const),
        settleApiCall(() => api.getFleetCapacity(projectScope ? { project_id: projectScope } : undefined)),
        settleApiCall(() => api.getFleetProvisioner()),
      ])
      if (!isCurrent()) return
      if (inventoryRes.ok) setInventory(inventoryRes.value)
      if (restartCapsRes.ok) setRestartCapabilities(restartCapsRes.value)
      if (fleetAlertsRes.ok) setFleetAlerts(fleetAlertsRes.value.alerts || [])
      if (fleetAuditRes.ok) setFleetAuditEvents((fleetAuditRes.value.events || []).filter(isFleetAuditEvent).slice(0, 8))
      else if (!canManageSystem) setFleetAuditEvents([])
      if (fleetCapacityRes.ok) setFleetCapacity(fleetCapacityRes.value)
      if (provisionerRes.ok) setProvisioner(provisionerRes.value)
      recordCardFreshness([
        { cardId: 'generators', ok: inventoryRes.ok && (!canManageSystem || restartCapsRes.ok) },
        { cardId: 'fleet-alerts', ok: fleetAlertsRes.ok },
        ...(canManageSystem ? [{ cardId: 'fleet-audit', ok: fleetAuditRes.ok }] : []),
        { cardId: 'capacity', ok: fleetCapacityRes.ok },
        { cardId: 'generator-metadata', ok: provisionerRes.ok },
      ])
      if ([inventoryRes, restartCapsRes, fleetAlertsRes, fleetAuditRes, fleetCapacityRes, provisionerRes].some(result => result.ok)) setLastRefreshed(new Date())
      return
    }

    if (categoryId === 'telemetry') {
      const [apmProfilesRes, monitorPacksRes] = await Promise.all([
        settleApiCall(() => api.getAPMIntegrationProfiles()),
        settleApiCall(() => api.getInfrastructureMonitorPacks()),
      ])
      if (!isCurrent()) return
      if (apmProfilesRes.ok) setApmProfiles(apmProfilesRes.value)
      if (monitorPacksRes.ok) setMonitorPacks(monitorPacksRes.value)
      recordCardFreshness([
        { cardId: 'apm-integrations', ok: apmProfilesRes.ok },
        { cardId: 'monitor-packs', ok: monitorPacksRes.ok },
      ])
      if (apmProfilesRes.ok || monitorPacksRes.ok) setLastRefreshed(new Date())
      return
    }

    const [scheduleHealthRes, runCalendarRes] = await Promise.all([
      settleApiCall(() => api.getScheduleHealth()),
      settleApiCall(() => api.getRunCalendar({
        from: calendarRange.from.toISOString(),
        to: calendarRange.to.toISOString(),
        project_id: projectScope,
        capacity: true,
        tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
      })),
    ])
    if (!isCurrent()) return
    if (scheduleHealthRes.ok) setScheduleHealth(scheduleHealthRes.value)
    if (runCalendarRes.ok) setRunCalendar(runCalendarRes.value)
    recordCardFreshness([{ cardId: 'run-calendar', ok: scheduleHealthRes.ok && runCalendarRes.ok }])
    if (scheduleHealthRes.ok || runCalendarRes.ok) setLastRefreshed(new Date())
  }), [activeCategoryId, activeProjectId, api, calendarRange.from, calendarRange.to, canManageSystem, recordCardFreshness, refreshReadiness, runCoalescedRefresh])

  const refreshAllCategories = useCallback(async (): Promise<boolean> => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return false
    for (const category of ['overview', 'fleet', 'telemetry', 'schedules'] as MonitorCategoryId[]) {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return false
      await refresh(category)
    }
    return true
  }, [refresh])

  const toggleFleetAlertAck = useCallback(async (alert: FleetAlert) => {
    if (!alert.id) return
    setFleetAlertActionError(null)
    setFleetAlertActionBusy(alert.id)
    try {
      await api.ackFleetAlert(alert.id, { acknowledged: !alert.acknowledged })
      await refresh('fleet')
    } catch (err) {
      setFleetAlertActionError(err instanceof Error ? err.message : 'Failed to update fleet alert.')
    } finally {
      setFleetAlertActionBusy(null)
    }
  }, [api, refresh])

  useEffect(() => {
    const scope = activeProjectId || ''
    if (initializedScopeRef.current === scope) return
    initializedScopeRef.current = scope
    initialRefreshCompleteRef.current = false
    void refreshAllCategories().then(completed => {
      if (completed && initializedScopeRef.current === scope) initialRefreshCompleteRef.current = true
    })
  }, [activeProjectId, refreshAllCategories])

  useEffect(() => {
    const rangeKey = `${calendarRange.from.toISOString()}|${calendarRange.to.toISOString()}`
    if (!calendarRefreshRangeRef.current) {
      calendarRefreshRangeRef.current = rangeKey
      return
    }
    if (calendarRefreshRangeRef.current === rangeKey) return
    calendarRefreshRangeRef.current = rangeKey
    void refresh('schedules')
  }, [calendarRange.from, calendarRange.to, refresh])

  useEffect(() => {
    if (renderedCategoryRef.current === activeCategoryId) return
    renderedCategoryRef.current = activeCategoryId
    if (activeCategoryId === 'overview') void refresh('overview')
  }, [activeCategoryId, refresh])

  useEffect(() => {
    if (activeCategoryId === 'overview') return

    const retryReadiness = () => { void refreshReadiness() }
    retryReadiness()
    const interval = setInterval(retryReadiness, 5000)
    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') retryReadiness()
    }
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', handleVisibilityChange)

    return () => {
      clearInterval(interval)
      if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [activeCategoryId, refreshReadiness])

  useEffect(() => {
    const interval = setInterval(refresh, 5000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        if (initialRefreshCompleteRef.current) refresh()
        else {
          const scope = initializedScopeRef.current
          void refreshAllCategories().then(completed => {
            if (completed && initializedScopeRef.current === scope) initialRefreshCompleteRef.current = true
          })
        }
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
  }, [refresh, refreshAllCategories])

  useEffect(() => {
    if (fleetEventSignal <= 0) return
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return
    let cancelled = false
    const generation = ++fleetEventGenerationRef.current
    const projectScope = activeProjectId
    void api.getFleetAlerts()
      .then(result => {
        if (cancelled || generation !== fleetEventGenerationRef.current || projectScope !== refreshScopeRef.current) return
        setFleetAlerts(result.alerts || [])
        setLastRefreshed(new Date())
      })
      .catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [activeProjectId, api, fleetEventSignal])

  useEffect(() => {
    if (!selectedCalendarEventId) return
    if (!runCalendar?.events.some(event => event.id === selectedCalendarEventId)) {
      setSelectedCalendarEventId(null)
    }
  }, [runCalendar, selectedCalendarEventId])

  const shiftCalendar = useCallback((amount: number) => {
    setCalendarAnchor(current => shiftRunCalendarAnchor(current, calendarView, amount))
  }, [calendarView])

  const selectCalendarEvent = useCallback((event: RunCalendarEvent) => {
    setSelectedCalendarEventId(event.id)
  }, [])

  const openSelectedTest = useCallback(() => {
    if (selectedCalendarEvent?.test_id) {
      onSelectTest?.(selectedCalendarEvent.test_id)
    }
  }, [onSelectTest, selectedCalendarEvent?.test_id])

  const openGeneratorPodRestart = useCallback((generator: SystemInventoryGenerator) => {
    const target = generator.pod_name || generator.target
    setRestartError(null)
    setRestartMessage(null)
    setRestartAction({
      targetType: 'generator',
      scope: 'pod',
      target,
      title: 'Restart Generator Pod',
      description: 'Delete one generator pod and let Kubernetes recreate it.',
      targetLabel: 'Generator pod',
      confirmLabel: 'Restart Pod',
      warning: generator.active_vusers > 0
        ? `${generator.active_vusers.toLocaleString()} active VUsers are currently assigned to this generator.`
        : generator.status === 'connected' || generator.status === 'healthy' || generator.status === 'ready'
          ? 'Idle generator pod restart requested.'
          : 'This generator is not reachable, so Kubernetes state is the source of truth.',
      forceDescription: 'Required for active VUsers or unreachable generators. Active work can be interrupted.',
    })
  }, [])

  const openGeneratorWorkloadRestart = useCallback(() => {
    const target = restartCapabilities?.generator_stateful_set || 'apexload-generator'
    setRestartError(null)
    setRestartMessage(null)
    setRestartAction({
      targetType: 'generator',
      scope: 'workload',
      target,
      title: 'Restart Generator Fleet',
      description: 'Roll the configured generator StatefulSet.',
      targetLabel: 'StatefulSet',
      confirmLabel: 'Restart Fleet',
      warning: generatorActiveVUsers > 0
        ? `${generatorActiveVUsers.toLocaleString()} active VUsers are running across the generator fleet.`
        : 'All generator pods in the StatefulSet will be replaced by Kubernetes.',
      forceDescription: 'Allows the rollout even when generators have active VUsers or runs are active.',
    })
  }, [generatorActiveVUsers, restartCapabilities?.generator_stateful_set])

  const openControllerRestart = useCallback(() => {
    const target = restartCapabilities?.controller_deployment || 'apexload-controller'
    setRestartError(null)
    setRestartMessage(null)
    setRestartAction({
      targetType: 'controller',
      scope: 'workload',
      target,
      title: 'Restart Controller',
      description: 'Roll the configured controller Deployment.',
      targetLabel: 'Deployment',
      confirmLabel: 'Restart Controller',
      warning: 'Any in-flight runs will be marked FAILED if the controller restarts before they complete.',
      forceDescription: 'Allows the rollout while runs are initializing, running, paused, stopping, or in shakeout.',
    })
  }, [restartCapabilities?.controller_deployment])

  const submitRestart = useCallback(async ({ reason, confirmation, force }: DestructiveConfirmation) => {
    if (!restartAction) return
    setRestartLoading(true)
    setRestartError(null)
    try {
      const result = await api.executeRestart({
        target_type: restartAction.targetType,
        scope: restartAction.scope,
        target: restartAction.target,
        reason,
        confirmation,
        force,
      })
      setRestartMessage(result.message || 'Restart requested.')
      setRestartAction(null)
      await refresh()
    } catch (error) {
      setRestartError(restartErrorMessage(error))
    } finally {
      setRestartLoading(false)
    }
  }, [api, refresh, restartAction])

  const cards = useMemo<MonitorCard[]>(() => [
    {
      id: 'system-health',
      title: 'Control Plane',
      headerBadge: (
        <span className={`monitor-header-badge ${controlPlaneHeaderSeverity}`}>
          {controlPlaneHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-row">
            <span className="monitor-label">API</span>
            <span className="monitor-stat">
              <span className={`monitor-indicator ${apiHealthSeverity === 'healthy' ? 'healthy' : 'unknown'}`} />
              <span className="monitor-value">{health?.status || 'Checking...'}</span>
            </span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">WebSocket</span>
            <span className={`monitor-value ${websocketValueClass}`}>
              {websocketLabel}
            </span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">WS Endpoint</span>
            <span className="monitor-value cell-mono">/api/v1/ws</span>
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
          <div className="monitor-build-grid" aria-label="Component versions and build dates">
            {componentBuildRows.map(component => (
              <div key={component.id} className="monitor-build-row">
                <span className="monitor-build-name">{component.name}</span>
                <span className="monitor-build-version">{component.version}</span>
                <span className="monitor-build-date" title={component.buildTime}>Built {component.buildDate}</span>
              </div>
            ))}
          </div>
          {canRestart && restartCapabilities?.supports.controller_workload && (
            <div className="monitor-action-row">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={openControllerRestart}
              >
                Restart Controller
              </button>
            </div>
          )}
        </div>
      ),
    },
    {
      id: 'threshold-alerts',
      title: 'Recent SLA Threshold Alerts',
      headerBadge: (
        <span className={`monitor-header-badge ${thresholdAlertHeaderSeverity}`}>
          {thresholdAlertHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M22 12h-4l-3 9L9 3l-3 9H2" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {thresholdAlerts.length === 0 ? (
            <p className="monitor-hint">No global SLA threshold breaches on running tests. Thresholds are configured in Settings → Thresholds.</p>
          ) : (
            <div className="monitor-scroll-area">
              <div className="monitor-warning-list">
                {thresholdAlerts.map(alert => (
                  <div key={alert.key} className="monitor-warning-row">
                    <div>
                      <p className={alert.action === 'stop_requested' || alert.metric === 'error_pct' ? 'text-danger' : ''}>
                        {formatThresholdAlertSummary(alert)}
                        {alert.acknowledged ? <span className="monitor-alert-ack">acknowledged</span> : null}
                        {!alert.active ? <span className="monitor-alert-ack">resolved</span> : null}
                      </p>
                      <div className="telemetry-diagnostic-summary">
                        {alert.testName || alert.testId} · last seen {formatTelemetryDiagnosticTime(alert.lastSeenAt)}
                      </div>
                    </div>
                    <div className="settings-inline-actions monitor-warning-actions">
                      {onSelectTest ? (
                        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onSelectTest(alert.testId)}>
                          Open
                        </button>
                      ) : null}
                      {onAcknowledgeThresholdAlert && alert.active && !alert.acknowledged ? (
                        <button type="button" className="btn btn-secondary btn-sm" onClick={() => onAcknowledgeThresholdAlert(alert.key)}>
                          Ack
                        </button>
                      ) : null}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ),
    },
    {
      id: 'production-readiness',
      title: 'Readiness & Services',
      headerBadge: (
        <span className={`monitor-header-badge ${readinessServicesHeaderSeverity}`}>
          {readinessServicesHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M20 13c0 5-3.5 7.5-8 9-4.5-1.5-8-4-8-9V5l8-3 8 3v8Z" />
          <path d="m9 12 2 2 4-5" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          <div className="monitor-row">
            <span className="monitor-label">Ready Checks</span>
            <span className="monitor-value">{systemReadiness ? `${readinessReadyCount}/${readinessDisplayedCheckCount}` : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Degraded</span>
            <span className={`monitor-value ${readinessDegradedCount > 0 ? 'telemetry-text-warning' : ''}`}>{systemReadiness ? readinessDegradedCount : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Blocked</span>
            <span className={`monitor-value ${readinessBlockedCount > 0 ? 'text-danger' : ''}`}>{systemReadiness ? readinessBlockedCount : '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Services</span>
            <span className={`monitor-value ${servicesHeaderSeverity === 'warning' ? 'telemetry-text-warning' : ''}`}>{servicesHeaderLabel}</span>
          </div>
          <InfrastructureReadinessList readiness={systemReadiness} fetchState={readinessFetchState} />
          {systemReadiness ? (
            <div className="monitor-warning-list">
              {readinessFailureChecks.map(check => (
                  <p key={check.name} className={check.status === 'not_ready' ? 'text-danger' : ''}>
                    {formatReadinessCheckName(check.name)}: {check.message}
                  </p>
              ))}
              {readinessFetchState === 'loaded' && allConfiguredReadinessChecksPassing && hasAllRequiredInfrastructureChecks && <p>All configured production readiness checks are passing.</p>}
            </div>
          ) : (
            <p className="monitor-hint">
              {readinessFetchState === 'error'
                ? 'Unable to load /api/v1/system/readiness. Retrying automatically.'
                : 'Waiting for /api/v1/system/readiness.'}
            </p>
          )}
          <ActiveServicesList
            backendServices={backendServices}
            frontendServices={frontendServices}
            endpointAvailable={Boolean(systemServices)}
          />
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
          <button type="button" className="telemetry-summary-card" aria-label="Open telemetry diagnostics" onClick={() => setIsTelemetryModalOpen(true)}>
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
      id: 'apm-integrations',
      title: 'APM Profiles',
      headerBadge: (
        <span className={`monitor-header-badge ${apmHeaderSeverity}`}>
          {apmHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 18V6" />
          <path d="M20 18V6" />
          <path d="M8 6h8" />
          <path d="M8 18h8" />
          <path d="M12 6v12" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {!apmProfiles ? (
            <p className="monitor-hint">Waiting for /api/v1/integrations/apm/profiles.</p>
          ) : (
            <>
              <div className="monitor-row">
                <span className="monitor-label">Correlation</span>
                <span className="monitor-value">{apmProfiles.generic_correlation.propagation}</span>
              </div>
              <div className="monitor-row">
                <span className="monitor-label">Fields</span>
                <span className="monitor-value">{apmProfiles.generic_correlation.correlation_fields.slice(0, 4).join(', ')}</span>
              </div>
              <div className="monitor-scroll-area">
                <div className="monitor-warning-list">
                  {apmProfiles.profiles.map(profile => (
                    <p key={profile.id} className={profile.status === 'available' ? 'telemetry-text-warning' : ''}>
                      {profile.name}: {profile.status}
                    </p>
                  ))}
                </div>
              </div>
            </>
          )}
        </div>
      ),
    },
    {
      id: 'monitor-packs',
      title: 'Monitor Packs',
      headerBadge: (
        <span className={`monitor-header-badge ${monitorPackHeaderSeverity}`}>
          {monitorPackHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="4" width="18" height="14" rx="2" />
          <path d="M7 20h10" />
          <path d="M9 9h.01" />
          <path d="M13 9h4" />
          <path d="M9 13h.01" />
          <path d="M13 13h4" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {!monitorPacks ? (
            <p className="monitor-hint">Waiting for /api/v1/monitoring/packs.</p>
          ) : (
            <>
              <div className="telemetry-summary-metrics">
                <div className="telemetry-summary-metric">
                  <span className="telemetry-summary-value">{monitorPackSupportedCount}</span>
                  <span className="telemetry-summary-label">Native</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className={`telemetry-summary-value ${monitorPackContractCount > 0 ? 'telemetry-text-warning' : ''}`}>{monitorPackContractCount}</span>
                  <span className="telemetry-summary-label">Contracts</span>
                </div>
                <div className="telemetry-summary-metric">
                  <span className="telemetry-summary-value">{monitorPackCount}</span>
                  <span className="telemetry-summary-label">Total</span>
                </div>
              </div>
              <div className="monitor-scroll-area">
                <div className="monitor-warning-list">
                  {monitorPacks.packs.map(pack => (
                    <p key={pack.id} className={pack.status === 'integration_contract' ? 'telemetry-text-warning' : ''}>
                      {pack.name}: {pack.signals.slice(0, 2).join(', ')}
                    </p>
                  ))}
                </div>
              </div>
            </>
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
          {(() => {
            const totalSlots = (fleetCapacity?.active_slots || 0) + (fleetCapacity?.available_slots || 0) + (fleetCapacity?.reserved_slots || 0)
            const usedSlots = (fleetCapacity?.active_slots || 0) + (fleetCapacity?.reserved_slots || 0)
            const usagePct = totalSlots > 0 ? Math.round((usedSlots / totalSlots) * 100) : 0
            return totalSlots > 0 ? (
              <div className="capacity-gauge">
                <div className="capacity-gauge-header">
                  <span className="capacity-gauge-label">Utilization</span>
                  <span className={`capacity-gauge-pct ${usagePct > 85 ? 'text-danger' : usagePct > 60 ? 'telemetry-text-warning' : ''}`}>{usagePct}%</span>
                </div>
                <div className="capacity-gauge-track">
                  <span className={`capacity-gauge-fill ${usagePct > 85 ? 'over' : ''}`} style={{ width: `${Math.min(100, usagePct)}%` }} />
                </div>
                <div className="capacity-gauge-legend">
                  <span>{usedSlots.toLocaleString()} used</span>
                  <span>{(fleetCapacity?.available_slots || 0).toLocaleString()} available</span>
                </div>
              </div>
            ) : null
          })()}
          {provisioner && (
            <div className="monitor-row" data-testid="fleet-provisioner">
              <span className="monitor-label">Generator Provisioning</span>
              <span className="monitor-value" title={provisioner.description}>
                <span className={`status-badge ${provisioner.can_provision ? 'status-running' : 'status-idle'}`}>
                  {provisioner.can_provision ? 'on-demand' : 'static fleet'}
                </span>
                {' '}{provisioner.name}
                {provisioner.supported_regions && provisioner.supported_regions.length > 0 && (
                  <span className="cell-muted"> · {provisioner.supported_regions.join(', ')}</span>
                )}
              </span>
            </div>
          )}
          <div className="monitor-row">
            <span className="monitor-label">Effective Available</span>
            <span className="monitor-value">{fleetCapacity?.available_slots?.toLocaleString() ?? health?.capacity_availability?.effective_available_generators ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Eligible Generators</span>
            <span className="monitor-value">{fleetCapacity ? `${fleetCapacity.eligible_generators}/${fleetCapacity.total_generators}` : health?.capacity_availability?.configured_available_generators ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Active Slots</span>
            <span className="monitor-value">{fleetCapacity?.active_slots?.toLocaleString() ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Reserved Slots</span>
            <span className="monitor-value">{fleetCapacity?.reserved_slots?.toLocaleString() ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Project Active Slots</span>
            <span className="monitor-value">{fleetCapacity?.project_active_slots?.toLocaleString() ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Project Pending Slots</span>
            <span className={`monitor-value ${(fleetCapacity?.project_pending_slots || 0) > 0 ? 'telemetry-text-warning' : ''}`}>{fleetCapacity?.project_pending_slots?.toLocaleString() ?? '-'}</span>
          </div>
          <div className="monitor-row">
            <span className="monitor-label">Source</span>
            <span className="monitor-value">{fleetCapacity ? 'fleet placement API' : health?.capacity_availability?.source || '-'}</span>
          </div>
          {fleetCapacity?.project_slots?.length ? (
            <div className="monitor-warning-list">
              {fleetCapacity.project_slots.slice(0, 3).map(slots => (
                <p key={slots.project_id}>
                  {slots.project_id}: {slots.active_slots.toLocaleString()} active / {slots.pending_slots.toLocaleString()} pending slots ({slots.fair_share_tier})
                </p>
              ))}
            </div>
          ) : null}
          {fleetCapacity?.compatible_features?.length ? (
            <div className="monitor-row">
              <span className="monitor-label">Features</span>
              <span className="monitor-value">{fleetCapacity.compatible_features.slice(0, 4).join(', ')}</span>
            </div>
          ) : null}
          {fleetCapacity?.rejection_reasons?.length ? (
            <div className="monitor-warning-list">
              {fleetCapacity.rejection_reasons.slice(0, 4).map(reason => <p key={reason}>{reason}</p>)}
            </div>
          ) : null}
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
          <button type="button" className="telemetry-summary-card" aria-label="Open generator inventory" onClick={() => setIsGeneratorsModalOpen(true)}>
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
          {fleetAlertList.length === 0 ? (
            <p className="monitor-hint">
              {fleetAlertsUnknown
                ? 'Fleet alert status is unavailable; the alert feed has not been retrieved.'
                : generators.length ? 'No fleet placement alerts detected from live generator inventory.' : 'No generator inventory available for fleet alert evaluation.'}
            </p>
          ) : (
            <div className="monitor-scroll-area">
              {fleetAlertActionError && <p className="monitor-action-message text-danger">{fleetAlertActionError}</p>}
              <div className="monitor-warning-list">
                {fleetAlertList.map(alert => (
                  <div key={alert.id || `${alert.type}:${alert.target}`} className="monitor-warning-row">
                    <p className={alert.severity === 'danger' ? 'text-danger' : ''}>
                      {alert.summary}
                      {alert.acknowledged ? <span className="monitor-alert-ack">acknowledged</span> : null}
                    </p>
                    {canManageSystem && alert.id ? (
                      <button
                        type="button"
                        className="btn btn-secondary btn-sm"
                        disabled={fleetAlertActionBusy === alert.id}
                        onClick={() => void toggleFleetAlertAck(alert)}
                      >
                        {fleetAlertActionBusy === alert.id ? 'Saving...' : alert.acknowledged ? 'Clear' : 'Ack'}
                      </button>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ),
    },
    {
      id: 'fleet-audit',
      title: 'Fleet Audit',
      headerBadge: (
        <span className={`monitor-header-badge ${fleetAuditHeaderSeverity}`}>
          {fleetAuditHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
          <path d="M14 2v6h6" />
          <path d="M8 13h8" />
          <path d="M8 17h5" />
          <path d="M8 9h1" />
        </svg>
      ),
      content: (
        <div className="monitor-body">
          {!canManageSystem ? (
            <p className="monitor-hint">System admin access is required to view fleet audit activity.</p>
          ) : fleetAuditEvents.length === 0 ? (
            <p className="monitor-hint">No recent fleet audit activity was returned.</p>
          ) : (
            <div className="monitor-scroll-area">
              <div className="monitor-warning-list">
                {fleetAuditEvents.map(event => (
                  <div key={event.id} className="monitor-warning-row">
                    <div>
                      <p>
                        {event.action}
                        <span className="monitor-alert-ack">{event.status}</span>
                      </p>
                      <div className="telemetry-diagnostic-summary">
                        {event.resource_type || 'resource'} {event.resource_id || '-'} by {event.actor_display_name || event.actor_subject || event.actor_type || 'unknown'}
                      </div>
                    </div>
                    <span className="telemetry-diagnostic-time">{formatAuditEventTime(event.created_at)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      ),
    },
    {
      id: 'run-calendar',
      title: 'Run Calendar',
      headerBadge: (
        <span className={`monitor-header-badge ${calendarHeaderSeverity}`}>
          {calendarHeaderLabel}
        </span>
      ),
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="4" width="18" height="18" rx="2" />
          <line x1="16" y1="2" x2="16" y2="6" />
          <line x1="8" y1="2" x2="8" y2="6" />
          <line x1="3" y1="10" x2="21" y2="10" />
          <path d="M8 14h.01" />
          <path d="M12 14h.01" />
          <path d="M16 14h.01" />
          <path d="M8 18h.01" />
          <path d="M12 18h.01" />
        </svg>
      ),
      content: (
        <RunCalendarPanel
          calendar={runCalendar}
          events={calendarEvents}
          model={calendarModel}
          view={calendarView}
          anchor={calendarAnchor}
          filter={calendarFilter}
          capacityReservations={<TimeslotsPanel projectId={activeProject?.id} canManage={canManageSystem} />}
          selectedEvent={selectedCalendarEvent}
          canOpenSelectedTest={Boolean(selectedCalendarEvent?.test_id && onSelectTest)}
          onPrevious={() => shiftCalendar(-1)}
          onNext={() => shiftCalendar(1)}
          onToday={() => setCalendarAnchor(new Date())}
          onViewChange={setCalendarView}
          onFilterChange={setCalendarFilter}
          onSelectEvent={selectCalendarEvent}
          onCloseDetails={() => setSelectedCalendarEventId(null)}
          onOpenSelectedTest={openSelectedTest}
          onScheduleForDate={onScheduleForDate}
        />
      ),
    },
    {
      id: 'collectors',
      title: 'Monitoring Collectors',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 11a9 9 0 0 1 9 9" />
          <path d="M4 4a16 16 0 0 1 16 16" />
          <circle cx="5" cy="19" r="1" />
        </svg>
      ),
      content: <MonitoringCollectorsCard projectId={activeProject?.id} canManage={canManageSystem} />,
    },
    {
      id: 'compute-infrastructure',
      title: 'Cloud Connections & Execution Targets',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M7 18h10a4 4 0 0 0 .7-7.9A6 6 0 0 0 6.2 8.3 4.5 4.5 0 0 0 7 18Z" />
          <path d="M9 14h6M12 11v6" />
        </svg>
      ),
      content: <ComputeInfrastructureCard canManage={canManageSystem} />,
    },
    {
      id: 'fleet-clusters',
      title: 'Kubernetes Cluster Connections',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="12" cy="12" r="3" />
          <path d="M12 2v4M12 18v4M2 12h4M18 12h4" />
          <path d="M5 5l2.5 2.5M16.5 16.5L19 19M19 5l-2.5 2.5M7.5 16.5L5 19" />
        </svg>
      ),
      content: <ClustersCard canManage={canManageSystem} />,
    },
    {
      id: 'fleet-pools',
      title: 'Generator Pools',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="3" width="7" height="7" rx="1" />
          <rect x="14" y="3" width="7" height="7" rx="1" />
          <rect x="3" y="14" width="7" height="7" rx="1" />
          <rect x="14" y="14" width="7" height="7" rx="1" />
        </svg>
      ),
      content: <FleetPoolsCard canManage={canManageSystem} />,
    },
    {
      id: 'fleet-allocation',
      title: 'Host Resource Allocation',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <rect x="3" y="4" width="18" height="4" rx="1" />
          <rect x="3" y="10" width="12" height="4" rx="1" />
          <rect x="3" y="16" width="7" height="4" rx="1" />
        </svg>
      ),
      content: <PoolAllocationCard canManage={canManageSystem} />,
    },
    {
      id: 'generator-metadata',
      title: 'Generator Metadata',
      icon: (
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M4 4h16v6H4z" />
          <path d="M4 14h16v6H4z" />
          <path d="M8 7h.01M8 17h.01" />
        </svg>
      ),
      content: <FleetGeneratorsCard canManage={canManageSystem} />,
    },
  ], [activeProject?.id, allConfiguredReadinessChecksPassing, apiHealthSeverity, apmHeaderLabel, apmHeaderSeverity, apmProfiles, backendServices, calendarAnchor, calendarEvents, calendarFilter, calendarHeaderLabel, calendarHeaderSeverity, calendarModel, calendarView, canManageSystem, canRestart, componentBuildRows, controlPlaneHeaderLabel, controlPlaneHeaderSeverity, controllerGeneratorInventoryLabel, fleetAlertActionBusy, fleetAlertActionError, fleetAlertHeaderLabel, fleetAlertHeaderSeverity, fleetAlertList, fleetAlertsUnknown, fleetAuditEvents, fleetAuditHeaderLabel, fleetAuditHeaderSeverity, fleetCapacity, frontendServices, generatorActiveVUsers, generatorAvailableCapacity, generatorConnectedCount, generatorHeaderLabel, generatorHeaderSeverity, generatorIssueCount, generators.length, hasAllRequiredInfrastructureChecks, health, inventory, monitorPackContractCount, monitorPackCount, monitorPackHeaderLabel, monitorPackHeaderSeverity, monitorPackSupportedCount, monitorPacks, onAcknowledgeThresholdAlert, onScheduleForDate, onSelectTest, openControllerRestart, openSelectedTest, provisioner, readinessBlockedCount, readinessDegradedCount, readinessDisplayedCheckCount, readinessFailureChecks, readinessFetchState, readinessReadyCount, readinessServicesHeaderLabel, readinessServicesHeaderSeverity, restartCapabilities?.supports.controller_workload, runCalendar, selectCalendarEvent, selectedCalendarEvent, servicesHeaderLabel, servicesHeaderSeverity, shiftCalendar, systemReadiness, systemServices, telemetryDangerCount, telemetryHeaderLabel, telemetryHeaderSeverity, telemetryLatestEntry, telemetryDiagnostics, telemetryWarningCount, thresholdAlertHeaderLabel, thresholdAlertHeaderSeverity, thresholdAlerts, toggleFleetAlertAck, websocketLabel, websocketValueClass])

  useEffect(() => {
    setCardOrder(prev => {
      const next = mergeOrder(prev, cards.map(card => card.id))
      return next.length === prev.length && next.every((id, index) => id === prev[index]) ? prev : next
    })
  }, [cards])

  useEffect(() => {
    writeStoredJSON('apexload.monitoring.cardOrder', cardOrder)
  }, [cardOrder])

  const orderedCards = useMemo(() => {
    const byId = new Map(cards.map(card => [card.id, card]))
    return mergeOrder(cardOrder, cards.map(card => card.id))
      .map(id => byId.get(id))
      .filter((card): card is MonitorCard => Boolean(card))
  }, [cards, cardOrder])

  const activeCategory = MONITOR_CATEGORIES.find(category => category.id === activeCategoryId) ?? MONITOR_CATEGORIES[0]
  const activeCategoryCardIds = new Set<string>(activeCategory.cardIds)
  const activeVisibleCards = orderedCards.filter(card => activeCategoryCardIds.has(card.id))

  const selectCategory = useCallback((id: MonitorCategoryId) => {
    runGuarded(() => {
      if (controlledCategoryId === undefined) {
        setInternalCategoryId(id)
      }
      onCategoryChange?.(id)
    })
  }, [controlledCategoryId, onCategoryChange, runGuarded])

  const monitorCategoryControls = useMemo(() => (
    <div className="monitor-tab-row topbar-page-section-toggle" role="tablist" aria-label="Monitor categories">
      {MONITOR_CATEGORIES.map(category => (
        <button
          key={category.id}
          type="button"
          role="tab"
          aria-selected={category.id === activeCategoryId}
          className={`monitor-tab ${category.id === activeCategoryId ? 'active' : ''}`}
          onClick={() => selectCategory(category.id)}
        >
          <span>{category.label}</span>
          <span className="monitor-tab-count">{category.cardIds.length}/{category.cardIds.length}</span>
        </button>
      ))}
    </div>
  ), [activeCategoryId, selectCategory])

  const severityEntries = useMemo<{ label: string; severity: string; summary: string }[]>(() => {
    const joinCounts = (parts: string[]) => parts.filter(Boolean).join(', ')
    const readinessSummary = !systemReadiness
      ? 'Readiness status is unavailable.'
      : joinCounts([
          readinessBlockedCount > 0 ? `${readinessBlockedCount} blocked` : '',
          readinessDegradedCount > 0 ? `${readinessDegradedCount} degraded` : '',
          readinessFetchState === 'stale' ? 'stale data' : '',
        ]) || readinessHeaderLabel
    const telemetryCount = telemetryDangerCount + telemetryWarningCount
    const telemetrySummary = telemetryCount > 0
      ? `${joinCounts([
          telemetryDangerCount > 0 ? `${telemetryDangerCount} danger` : '',
          telemetryWarningCount > 0 ? `${telemetryWarningCount} warning` : '',
        ])} telemetry diagnostic${telemetryCount === 1 ? '' : 's'}.`
      : ''
    return [
      { label: 'API', severity: health ? 'healthy' : 'unknown', summary: 'Controller API has not responded yet.' },
      {
        label: 'WebSocket',
        severity: connectionState === 'connected' ? 'healthy' : connectionState === 'disconnected' ? 'danger' : 'warning',
        summary: connectionState === 'disconnected' ? 'Live event stream is disconnected.' : 'Connecting to the live event stream…',
      },
      { label: 'Readiness', severity: readinessHeaderSeverity, summary: readinessSummary },
      {
        label: 'Controller',
        severity: controllerHeaderSeverity,
        summary: controllerHeaderSeverity === 'unknown' ? 'Controller status is unavailable.' : `Controller is reporting "${controllerStatusLabel}".`,
      },
      {
        label: 'Generators',
        severity: generatorHeaderSeverity,
        summary: generators.length === 0 ? 'No generator inventory reported.' : `${generatorIssueCount} of ${generators.length} generator${generators.length === 1 ? '' : 's'} offline.`,
      },
      { label: 'Telemetry', severity: telemetryHeaderSeverity, summary: telemetrySummary },
      {
        label: 'SLA Thresholds',
        severity: thresholdAlertHeaderSeverity,
        summary: unacknowledgedThresholdAlerts.length > 0
          ? `${unacknowledgedThresholdAlerts.length} unacknowledged SLA threshold alert${unacknowledgedThresholdAlerts.length === 1 ? '' : 's'}.`
          : 'No unacknowledged SLA threshold alerts.',
      },
      {
        label: 'Fleet',
        severity: fleetAlertHeaderSeverity,
        summary: fleetAlertsUnknown && fleetAlertList.length === 0
          ? 'Fleet alert status is unavailable.'
          : generators.length === 0 ? 'No generator inventory reported.' : `${fleetAlertList.length} active fleet alert${fleetAlertList.length === 1 ? '' : 's'}.`,
      },
      {
        label: 'Schedules',
        severity: scheduleHeaderSeverity,
        summary: schedulePresentation.summary,
      },
    ]
  }, [health, connectionState, readinessHeaderSeverity, readinessFetchState, systemReadiness, readinessBlockedCount, readinessDegradedCount, readinessHeaderLabel, controllerHeaderSeverity, controllerStatusLabel, generatorHeaderSeverity, generators, generatorIssueCount, telemetryHeaderSeverity, telemetryDangerCount, telemetryWarningCount, thresholdAlertHeaderSeverity, unacknowledgedThresholdAlerts.length, fleetAlertHeaderSeverity, fleetAlertList, fleetAlertsUnknown, scheduleHeaderSeverity, schedulePresentation.summary])

  const overallSeverity = useMemo(() => {
    if (severityEntries.some(e => e.severity === 'danger')) return 'danger'
    if (severityEntries.some(e => e.severity === 'warning')) return 'warning'
    if (readinessHeaderSeverity === 'unknown') return 'unknown'
    if (severityEntries.every(e => e.severity === 'unknown')) return 'unknown'
    return 'healthy'
  }, [readinessHeaderSeverity, severityEntries])

  const issueCount = severityEntries.filter(e => e.severity === 'danger' || e.severity === 'warning').length
  const overallLabel = overallSeverity === 'danger'
    ? `${issueCount} issue${issueCount === 1 ? ' needs' : 's need'} attention`
    : overallSeverity === 'warning'
      ? `${issueCount} advisory warning${issueCount === 1 ? '' : 's'}`
      : overallSeverity === 'unknown'
        ? readinessHeaderSeverity === 'unknown'
          ? readinessFetchState === 'loading' && !systemReadiness
            ? 'Waiting for system data...'
            : readinessFetchState === 'error' && !systemReadiness
              ? 'Readiness unavailable'
              : 'Readiness data incomplete'
          : 'Waiting for system data...'
        : 'All systems operational'
  const advisoryEntries = severityEntries.filter(entry => entry.severity === 'danger' || entry.severity === 'warning')
  const advisoryTone: TopbarMetaTone | null = overallSeverity === 'danger'
    ? 'danger'
    : overallSeverity === 'warning'
      ? 'warning'
      : overallSeverity === 'unknown'
        ? 'default'
        : null
  const statusSummaryEntries = overallSeverity === 'unknown'
    ? severityEntries.filter(entry => entry.severity === 'unknown')
    : advisoryEntries
  const advisorySummary = statusSummaryEntries
    .map(entry => `${entry.label}: ${entry.summary || entry.severity}`)
    .join('\n')
  const monitoringTopbarControls = useMemo(() => (
    <div className="monitoring-topbar-controls">
      {monitorCategoryControls}
      {advisoryTone && (
        <span className={`topbar-meta-chip ${advisoryTone} monitoring-topbar-advisory`} title={advisorySummary}>
          {overallLabel}
        </span>
      )}
    </div>
  ), [advisorySummary, advisoryTone, monitorCategoryControls, overallLabel])

  const monitoringTopbarContribution = useMemo(() => ({
    id: 'monitoring-page',
    priority: 10,
    title: activeCategoryId === 'overview' ? 'Monitoring' : activeCategory.label,
    controls: monitoringTopbarControls,
  }), [activeCategory.label, activeCategoryId, monitoringTopbarControls])

  useTopbarContribution(monitoringTopbarContribution)

  return (
    <div className="page">
      <div
        className="monitor-readiness-live-summary"
        role="status"
        aria-live="polite"
        aria-atomic="true"
        data-readiness-live-summary
      >
        {readinessLiveAnnouncement}
      </div>
      <div className="page-header">
        <div>
          <h2 className="monitor-page-title">
            <span>{activeCategoryId === 'overview' ? 'Monitoring' : activeCategory.label}</span>
            {advisoryTone && (
              <span className={`monitor-title-advisory monitor-header-badge ${advisoryTone}`} title={advisorySummary}>
                {overallLabel}
              </span>
            )}
          </h2>
          <p className="page-subtitle">System health, generator pods, and connection status</p>
        </div>
      </div>

      {!hasTopbarContributionProvider && monitorCategoryControls}

      <div className="monitor-tab-panel" role="tabpanel" aria-label={`${activeCategory.label} monitors`}>
        <div className="monitoring-grid" data-active-category={activeCategory.id}>
        {activeVisibleCards.length === 0 ? (
          <div className="monitor-empty-category">
            No {activeCategory.label} monitors are visible.
          </div>
        ) : activeVisibleCards.map(card => (
          <div
            key={card.id}
            data-card-id={card.id}
            className={`monitor-card${staleCardIds.includes(card.id) ? ' stale' : ''}`}
          >
            <div className="monitor-header">
              {card.icon}
              <span>{card.title}</span>
              {staleCardIds.includes(card.id)
                ? <span className="monitor-header-badge warning" title="The latest refresh for this card failed; previously loaded values may be stale.">Stale</span>
                : card.headerBadge}
            </div>
            {card.content}
          </div>
        ))}
        </div>
      </div>

      {lastRefreshed && (
        <div className="monitor-update-footer" role="status">
          <span className="monitor-last-refreshed">
            Updated available data {lastRefreshed.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', second: '2-digit' })}
            {staleCardIds.length > 0 ? ` · ${staleCardIds.length} stale card${staleCardIds.length === 1 ? '' : 's'}` : ''}
          </span>
        </div>
      )}

      {restartMessage && (
        <div className="monitor-restart-banner" role="status">
          {restartMessage}
        </div>
      )}

      {isTelemetryModalOpen && telemetryDiagnostics.length > 0 && (
        <div className="modal-overlay" {...dialogBackdropProps(() => setIsTelemetryModalOpen(false), 'Close telemetry diagnostics')}>
          <div ref={telemetryDialogRef} className="modal modal-wide telemetry-modal" role="dialog" aria-modal="true" aria-labelledby="telemetry-diagnostics-title">
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
        <div className="modal-overlay" {...dialogBackdropProps(() => setIsGeneratorsModalOpen(false), 'Close generator inventory')}>
          <div ref={generatorsDialogRef} className="modal modal-wide telemetry-modal" role="dialog" aria-modal="true" aria-labelledby="generator-inventory-title">
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
            {canRestart && (
              <div className="monitor-restart-actions">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={openGeneratorWorkloadRestart}
                  disabled={!restartCapabilities?.supports.generator_workload}
                >
                  Restart Generator Fleet
                </button>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  onClick={openControllerRestart}
                  disabled={!restartCapabilities?.supports.controller_workload}
                >
                  Restart Controller
                </button>
                {restartMessage && <span className="monitor-action-message">{restartMessage}</span>}
              </div>
            )}
            {canManageSystem && !canRestart && restartDisabledReason && <p className="monitor-action-message muted">{restartDisabledReason}</p>}
            <div className="monitor-body telemetry-modal-body">
              <GeneratorInventoryList
                generators={generators}
                canRestart={canRestart && Boolean(restartCapabilities?.supports.generator_pod)}
                restartDisabledReason={restartDisabledReason}
                onRestartGenerator={openGeneratorPodRestart}
              />
            </div>
          </div>
        </div>
      )}

      {restartAction && (
        <ConfirmDestructiveModal
          title={restartAction.title}
          description={restartAction.description}
          target={restartAction.target}
          targetLabel={restartAction.targetLabel}
          confirmLabel={restartAction.confirmLabel}
          allowForce={Boolean(restartCapabilities?.allow_force)}
          forceLabel="Force restart"
          forceDescription={restartAction.forceDescription}
          warning={restartAction.warning}
          error={restartError}
          loading={restartLoading}
          onCancel={() => {
            if (!restartLoading) {
              setRestartAction(null)
              setRestartError(null)
            }
          }}
          onConfirm={submitRestart}
        />
      )}
    </div>
  )
}

function GeneratorInventoryList({
  generators,
  canRestart,
  restartDisabledReason,
  onRestartGenerator,
}: {
  generators: SystemInventoryResponse['generators']
  canRestart: boolean
  restartDisabledReason?: string
  onRestartGenerator: (generator: SystemInventoryGenerator) => void
}) {
  return (
    <div className="monitor-inventory-list">
      {generators.map(generator => (
        <div key={generator.target} className={`monitor-inventory-item ${generator.status !== 'connected' ? 'is-warning' : ''}`}>
          <div className="monitor-row">
            <span className="monitor-value">{generator.generator_id || generator.target}</span>
            <span className={`monitor-value ${generator.status === 'connected' ? 'text-success' : 'text-danger'}`}>{generator.status}</span>
          </div>
          {generator.pod_name && (
            <div className="monitor-row">
              <span className="monitor-label">Pod</span>
              <span className="monitor-value cell-mono">{generator.pod_name}</span>
            </div>
          )}
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
          {canRestart && (
          <div className="monitor-action-row">
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              onClick={() => onRestartGenerator(generator)}
              disabled={!canRestart || generator.restartable === false}
              title={generator.restartable === false ? generator.restart_blocked_reason : restartDisabledReason}
            >
              Restart Pod
            </button>
            {generator.restart_blocked_reason && <span className="monitor-action-message muted">{generator.restart_blocked_reason}</span>}
          </div>
          )}
        </div>
      ))}
    </div>
  )
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

interface RunCalendarDay {
  key: string
  date: Date
  isOutsidePeriod: boolean
  isToday: boolean
  events: RunCalendarEvent[]
  startingEventCount: number
  continuingEventCount: number
}

export interface RunCalendarModel {
  label: string
  rangeLabel: string
  days: RunCalendarDay[]
}

function InfrastructureReadinessList({ readiness, fetchState }: { readiness: SystemReadinessResponse | null; fetchState: ReadinessFetchState }) {
  const checks = new Map((readiness?.checks ?? []).map(check => [check.name, check]))
  const rows = [
    { id: 'kubernetes', kind: 'kubernetes' as const, check: checks.get('kubernetes_api') },
    { id: 'metadata', kind: 'metadata' as const, check: checks.get('metadata_store') },
    { id: 'cache', kind: 'cache' as const, check: checks.get('cache_backend') },
  ]

  return (
    <ul className="monitor-infrastructure-readiness" aria-label="Infrastructure readiness">
      {rows.map(row => {
        const state = infrastructureReadinessState(row.check, fetchState)
        const latency = readinessDetailNumber(row.check, 'probe_latency_ms')
        const summary = infrastructureReadinessSummary(state, row.check)
        return (
          <li key={row.id} className="monitor-infrastructure-row">
            <div className="monitor-infrastructure-main">
              <span className="monitor-infrastructure-name">{infrastructureReadinessLabel(row.kind, row.check)}</span>
              <span className="monitor-infrastructure-summary">
                {summary}{latency !== null ? ` · ${latency.toLocaleString(undefined, { maximumFractionDigits: 3 })} ms` : ''}
                {fetchState === 'stale' ? ' · Stale data; latest readiness refresh failed.' : ''}
              </span>
            </div>
            <span className={`status-badge monitor-infrastructure-status ${infrastructureReadinessStatusClass(state)}`}>
              {infrastructureReadinessStatusLabel(state)}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

function ActiveServicesList({
  backendServices,
  frontendServices,
  endpointAvailable,
}: {
  backendServices: SystemServicesResponse['services']
  frontendServices: SystemServicesResponse['services']
  endpointAvailable: boolean
}) {
  return (
    <>
      <ServiceGroup title="Backend" services={backendServices} emptyLabel={endpointAvailable ? 'No backend services reported.' : 'Waiting for /api/v1/system/services.'} />
      <ServiceGroup title="Frontend" services={frontendServices} emptyLabel="No dashboard frontend origin detected." />
    </>
  )
}

function ServiceGroup({ title, services, emptyLabel }: { title: string; services: SystemServicesResponse['services']; emptyLabel: string }) {
  return (
    <div className="monitor-service-group">
      <div className="monitor-service-group-title">{title}</div>
      {services.length ? (
        <div className="monitor-service-list">
          {services.map(service => (
            <div key={service.id} className="monitor-service-row">
              <div className="monitor-service-main">
                <span className="monitor-service-name">{service.name}</span>
                <span className="monitor-service-address cell-mono">{service.url || service.address || '-'}</span>
              </div>
              <span className={`status-badge monitor-service-status ${serviceStatusClass(service.status)}`}>{service.status}</span>
            </div>
          ))}
        </div>
      ) : (
        <p className="monitor-hint">{emptyLabel}</p>
      )}
    </div>
  )
}

function RunCalendarPanel({
  calendar,
  events,
  model,
  view,
  anchor,
  filter,
  capacityReservations,
  selectedEvent,
  canOpenSelectedTest,
  onPrevious,
  onNext,
  onToday,
  onViewChange,
  onFilterChange,
  onSelectEvent,
  onCloseDetails,
  onOpenSelectedTest,
  onScheduleForDate,
}: {
  calendar: RunCalendarResponse | null
  events: RunCalendarEvent[]
  model: RunCalendarModel
  view: RunCalendarView
  anchor: Date
  filter: RunCalendarFilter
  capacityReservations?: ReactNode
  selectedEvent: RunCalendarEvent | null
  canOpenSelectedTest: boolean
  onPrevious: () => void
  onNext: () => void
  onToday: () => void
  onViewChange: (view: RunCalendarView) => void
  onFilterChange: (filter: RunCalendarFilter) => void
  onSelectEvent: (event: RunCalendarEvent) => void
  onCloseDetails: () => void
  onOpenSelectedTest: () => void
  onScheduleForDate?: (prefillStartLocal: string) => void
}) {
  const [detailCollapsed, setDetailCollapsed] = useState(false)
  const cronCount = events.filter(event => event.source === 'cron').length
  const adHocCount = events.filter(event => event.source === 'ad_hoc').length
  const planCount = events.filter(event => event.source === 'execution_plan').length
  const currentCount = events.filter(event => isRunCalendarActiveStatus(event.status)).length
  const projectedCount = events.filter(event => event.projected).length
  const rangeNotice = formatRunCalendarNotice(calendar)
  const selectedRunId = selectedEvent?.id
  const capacityByDate = useMemo(() => {
    const map = new Map<string, RunCalendarCapacityDay>()
    for (const day of calendar?.capacity?.days ?? []) {
      map.set(day.date, day)
    }
    return map
  }, [calendar])

  useEffect(() => {
    if (selectedRunId) setDetailCollapsed(false)
  }, [selectedRunId])

  return (
    <div className="monitor-body run-calendar-panel">
      <div className="run-calendar-top-row">
        <div className="run-calendar-primary">
          <div className="run-calendar-toolbar">
            <div className="run-calendar-controls">
              <div className="run-calendar-nav" role="group" aria-label="Calendar range">
                <button type="button" className="btn-icon run-calendar-nav-btn" aria-label="Previous range" onClick={onPrevious}>
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="m10 12-4-4 4-4" />
                  </svg>
                </button>
                <div className="run-calendar-period-readout" aria-live="polite">
                  <span className="run-calendar-period-label">{model.label}</span>
                  <span className="run-calendar-period-range">{model.rangeLabel}</span>
                </div>
                <button type="button" className="btn-icon run-calendar-nav-btn" aria-label="Next range" onClick={onNext}>
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="m6 4 4 4-4 4" />
                  </svg>
                </button>
              </div>
              <button type="button" className="btn btn-secondary btn-sm run-calendar-today-btn" onClick={onToday}>Today</button>
              <div className="run-calendar-segments" role="group" aria-label="Calendar view">
                {(['month', 'week', 'day'] as const).map(option => (
                  <button key={option} type="button" className={view === option ? 'active' : ''} onClick={() => onViewChange(option)}>
                    {option.charAt(0).toUpperCase() + option.slice(1)}
                  </button>
                ))}
              </div>
              <div className="run-calendar-segments" role="group" aria-label="Run filter">
                {([
                  ['all', 'All'],
                  ['cron', 'Cron'],
                  ['ad_hoc', 'Ad-hoc'],
                  ['execution_plan', 'Plans'],
                  ['current', 'Current'],
                ] as const).map(([value, label]) => (
                  <button key={value} type="button" className={filter === value ? 'active' : ''} onClick={() => onFilterChange(value)}>
                    {label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="run-calendar-summary" aria-label="Run calendar summary">
            <RunCalendarMetric label="Cron" value={cronCount} />
            <RunCalendarMetric label="Ad-hoc" value={adHocCount} />
            <RunCalendarMetric label="Plans" value={planCount} />
            <RunCalendarMetric label="Current" value={currentCount} />
            <RunCalendarMetric label="Projected" value={projectedCount} />
          </div>
        </div>
        {capacityReservations && (
          <aside className="run-calendar-reservations-panel" aria-label="Capacity Reservations">
            <div className="run-calendar-reservations-header">
              <span>Capacity Reservations</span>
            </div>
            {capacityReservations}
          </aside>
        )}
      </div>

      {rangeNotice && <div className="run-calendar-notice">{rangeNotice}</div>}

      <div className={`run-calendar-workspace ${selectedEvent ? 'has-detail' : ''} ${detailCollapsed ? 'detail-collapsed' : ''}`}>
        <div className="run-calendar-main">
          {view === 'day' ? (
            <div className="run-calendar-agenda" aria-label={`Runs for ${formatCalendarDate(anchor)}`}>
              {(model.days[0]?.events.length || 0) === 0 ? (
                <p className="monitor-hint">No runs in this day.</p>
              ) : model.days[0].events.map(event => (
                <RunCalendarEventButton key={event.id} event={event} dayDate={model.days[0].date} selected={selectedEvent?.id === event.id} mode="agenda" onSelect={onSelectEvent} />
              ))}
            </div>
          ) : (
            <div className={`run-calendar-grid ${view}`} aria-label={`${view} run calendar`}>
              {['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].map(day => (
                <div key={day} className="run-calendar-weekday">{day}</div>
              ))}
              {model.days.map(day => {
                const visibleEvents = day.events.slice(0, view === 'month' ? 4 : 7)
                const hiddenCount = day.events.length - visibleEvents.length
                const capacityDay = capacityByDate.get(day.key)
                const scheduleThisDay = onScheduleForDate
                  ? () => {
                      const slot = new Date(day.date.getFullYear(), day.date.getMonth(), day.date.getDate(), 9, 0, 0, 0)
                      onScheduleForDate(formatDatetimeLocalValue(slot))
                    }
                  : undefined
                return (
                  <div
                    key={day.key}
                    data-calendar-date={day.key}
                    className={`run-calendar-day ${day.isOutsidePeriod ? 'outside' : ''} ${day.isToday ? 'today' : ''} ${scheduleThisDay ? 'schedulable' : ''}`}
                    {...(scheduleThisDay ? interactiveElementProps<HTMLDivElement>(event => {
                      // Only the empty cell area schedules; child event buttons
                      // retain their own mouse and keyboard behavior.
                      if ((event.target as HTMLElement).closest('button')) return
                      scheduleThisDay()
                    }, 'Schedule a run on this day') : {})}
                    title={scheduleThisDay ? 'Schedule a run on this day' : undefined}
                  >
                    <div className="run-calendar-day-header">
                      <span>{formatCalendarDayNumber(day.date, view)}</span>
                      <span className="run-calendar-day-header-meta">
                        {scheduleThisDay && (
                          <button type="button" className="btn-icon" aria-label={`Schedule a run on ${day.key}`} onClick={event => { event.stopPropagation(); scheduleThisDay() }}>+</button>
                        )}
                        {capacityDay && capacityDay.reservation_count > 0 && (
                          <span
                            className={`run-calendar-pressure-pip ${capacityDay.pressure}`}
                            title={`Capacity: ${capacityDay.peak_reserved_generators}/${capacityDay.configured_generators} generators reserved (${Math.round(capacityDay.pressure_pct * 100)}%) · ${capacityDay.reservation_count} reservation${capacityDay.reservation_count === 1 ? '' : 's'}`}
                            aria-label={`Capacity pressure ${capacityDay.pressure}`}
                          />
                        )}
                        {day.startingEventCount > 0 && (
                          <span aria-label={`${day.startingEventCount} starting`}>{day.startingEventCount}</span>
                        )}
                        {day.continuingEventCount > 0 && (
                          <span aria-label={`${day.continuingEventCount} continuing`}>↪{day.continuingEventCount}</span>
                        )}
                      </span>
                    </div>
                    <div className="run-calendar-day-events">
                      {visibleEvents.map(event => (
                        <RunCalendarEventButton key={event.id} event={event} dayDate={day.date} selected={selectedEvent?.id === event.id} mode="compact" onSelect={onSelectEvent} />
                      ))}
                      {hiddenCount > 0 && <span className="run-calendar-more">+{hiddenCount} more</span>}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {selectedEvent && (
          <aside className={`run-calendar-detail-panel ${detailCollapsed ? 'collapsed' : ''}`} aria-label="Run details">
            {detailCollapsed ? (
              <button
                type="button"
                className="run-calendar-detail-rail"
                aria-label="Expand run details"
                aria-expanded="false"
                onClick={() => setDetailCollapsed(false)}
              >
                <span className={`run-calendar-source ${selectedEvent.source}`}>{runCalendarSourceLabel(selectedEvent)}</span>
                <span>{selectedEvent.title}</span>
              </button>
            ) : (
              <div className="run-calendar-detail" role="region" aria-label="Run details">
                <div className="run-calendar-detail-header">
                  <div>
                    <span className={`run-calendar-source ${selectedEvent.source}`}>{runCalendarSourceLabel(selectedEvent)}</span>
                    <h3>{selectedEvent.title}</h3>
                  </div>
                  <div className="run-calendar-detail-icon-actions">
                    <button
                      type="button"
                      className="btn-icon"
                      aria-label="Collapse run details"
                      aria-expanded="true"
                      onClick={() => setDetailCollapsed(true)}
                    >
                      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="m10 4-4 4 4 4" />
                      </svg>
                    </button>
                    <button type="button" className="btn-icon" aria-label="Close run details" onClick={onCloseDetails}>
                      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
                        <path d="M4 4l8 8" />
                        <path d="M12 4l-8 8" />
                      </svg>
                    </button>
                  </div>
                </div>
                <div className="run-calendar-detail-grid">
                  <RunCalendarDetailRow label="Status" value={formatRunCalendarStatus(selectedEvent.status)} />
                  <RunCalendarDetailRow label="When" value={`${formatCalendarDateTime(selectedEvent.start_at, selectedEvent.timezone)} - ${selectedEvent.end_at ? formatCalendarTime(selectedEvent.end_at, selectedEvent.timezone) : 'open'}`} />
                  <RunCalendarDetailRow label="User" value={runCalendarUserLabel(selectedEvent)} />
                  <RunCalendarDetailRow label="Workload" value={`${(selectedEvent.workload.total_vusers ?? 0).toLocaleString()} VUsers / ${selectedEvent.workload.duration || 'duration pending'}`} />
                  {(selectedEvent.scenario_name || selectedEvent.scenario_id) && (
                    <RunCalendarDetailRow label="Scenario" value={selectedEvent.scenario_name || selectedEvent.scenario_id || '-'} />
                  )}
                  {selectedEvent.plan_id && <RunCalendarDetailRow label="Plan" value={selectedEvent.plan_id} />}
                  <RunCalendarDetailRow label="Schedule" value={selectedEvent.schedule_name || selectedEvent.cron_expr || (selectedEvent.source === 'ad_hoc' ? 'Ad-hoc' : '-')} />
                  {selectedEvent.cron_expr && <RunCalendarDetailRow label="Cron" value={`${selectedEvent.cron_expr}${selectedEvent.timezone ? ` / ${selectedEvent.timezone}` : ''}`} />}
                  {selectedEvent.test_ref_key || selectedEvent.test_id ? <RunCalendarDetailRow label="Test" value={selectedEvent.test_ref_key || selectedEvent.test_id || '-'} /> : null}
                  {selectedEvent.reason_code && <RunCalendarDetailRow label="Reason" value={formatRunCalendarStatus(selectedEvent.reason_code)} />}
                  {selectedEvent.conflict_summary && <RunCalendarDetailRow label="Conflict" value={selectedEvent.conflict_summary} />}
                </div>
                <div className="run-calendar-topology">
                  <div className="run-calendar-section-heading">
                    <span>Controller & Hosts</span>
                    <span>{selectedEvent.topology.source || 'unknown'}</span>
                  </div>
                  {selectedEvent.topology.controller ? (
                    <div className="run-calendar-detail-grid compact">
                      <RunCalendarDetailRow label="Controller" value={selectedEvent.topology.controller.id || selectedEvent.topology.controller.status} />
                      <RunCalendarDetailRow label="Address" value={selectedEvent.topology.controller.address || '-'} />
                    </div>
                  ) : (
                    <p className="monitor-hint">{selectedEvent.topology.summary || 'Controller topology is not captured for this run.'}</p>
                  )}
                  {selectedEvent.topology.generator_shards?.length ? (
                    <div className="run-calendar-chip-list">
                      {selectedEvent.topology.generator_shards.map((shard, index) => (
                        <span key={`${shard.group}-${shard.target}-${index}`} className="run-calendar-chip">
                          {shard.group || 'group'} / {shard.target || 'auto'} / {shard.vusers.toLocaleString()} VUsers
                        </span>
                      ))}
                    </div>
                  ) : null}
                  {selectedEvent.topology.generator_inventory?.length ? (
                    <div className="run-calendar-host-list">
                      {selectedEvent.topology.generator_inventory.slice(0, 4).map(host => (
                        <span key={host.target} className="run-calendar-host">
                          {host.generator_id || host.target} / {host.status} / {(host.active_vusers ?? 0).toLocaleString()} active
                        </span>
                      ))}
                    </div>
                  ) : null}
                </div>
                <div className="run-calendar-detail-actions">
                  <button type="button" className="btn btn-secondary btn-sm" onClick={onCloseDetails}>Close</button>
                  {canOpenSelectedTest && (
                    <button type="button" className="btn btn-accent-action btn-sm" onClick={onOpenSelectedTest}>Open Test</button>
                  )}
                </div>
              </div>
            )}
          </aside>
        )}
      </div>
    </div>
  )
}

function RunCalendarMetric({ label, value }: { label: string; value: number }) {
  return (
    <div className="run-calendar-metric">
      <span>{value.toLocaleString()}</span>
      <span>{label}</span>
    </div>
  )
}

function RunCalendarEventButton({ event, dayDate, selected, mode, onSelect }: { event: RunCalendarEvent; dayDate: Date; selected: boolean; mode: 'compact' | 'agenda'; onSelect: (event: RunCalendarEvent) => void }) {
  const continuesIntoDay = runCalendarEventContinuesIntoDay(event, dayDate)
  const scheduledZoneTime = event.timezone ? formatCalendarTime(event.start_at, event.timezone) : ''
  return (
    <button type="button" className={`run-calendar-event ${mode} ${runCalendarEventClass(event)} ${continuesIntoDay ? 'continuing' : ''} ${selected ? 'selected' : ''}`} onClick={() => onSelect(event)} title={`${event.title} / ${continuesIntoDay ? 'continues' : formatRunCalendarStatus(event.status)}`}>
      <span className="run-calendar-event-time">{continuesIntoDay ? '↪ Continues' : formatCalendarTime(event.start_at)}</span>
      <span className="run-calendar-event-title">{event.title}</span>
      <span className="run-calendar-event-meta">{runCalendarSourceLabel(event)} / {formatRunCalendarStatus(event.status)}{scheduledZoneTime && !continuesIntoDay ? ` / ${scheduledZoneTime}` : ''}</span>
    </button>
  )
}

function RunCalendarDetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="run-calendar-detail-row">
      <span>{label}</span>
      <span>{value}</span>
    </div>
  )
}

function getRunCalendarVisibleRange(view: RunCalendarView, anchor: Date) {
  if (view === 'month') {
    const monthStart = new Date(anchor.getFullYear(), anchor.getMonth(), 1)
    const from = startOfWeek(monthStart)
    return { from, to: addDays(from, 42) }
  }
  if (view === 'week') {
    const from = startOfWeek(anchor)
    return { from, to: addDays(from, 7) }
  }
  const from = startOfLocalDay(anchor)
  return { from, to: addDays(from, 1) }
}

export function buildRunCalendarModel(view: RunCalendarView, anchor: Date, events: RunCalendarEvent[]): RunCalendarModel {
  const range = getRunCalendarVisibleRange(view, anchor)
  const dayCount = view === 'month' ? 42 : view === 'week' ? 7 : 1
  const todayKey = localDateKey(new Date())
  const days: RunCalendarDay[] = Array.from({ length: dayCount }, (_, index) => {
    const date = addDays(range.from, index)
    const dayStart = startOfLocalDay(date)
    return {
      key: localDateKey(dayStart),
      date: dayStart,
      isOutsidePeriod: view === 'month' && dayStart.getMonth() !== anchor.getMonth(),
      isToday: localDateKey(dayStart) === todayKey,
      events: [],
      startingEventCount: 0,
      continuingEventCount: 0,
    }
  })
  const daysByKey = new Map(days.map(day => [day.key, day]))
  const rangeStartMs = range.from.getTime()
  const rangeEndMs = range.to.getTime()
  const preparedEvents = events.flatMap(event => {
    const startMs = Date.parse(event.start_at)
    if (!Number.isFinite(startMs)) return []
    const explicitEndMs = event.end_at ? Date.parse(event.end_at) : NaN
    const endMs = Number.isFinite(explicitEndMs) && explicitEndMs > startMs
      ? explicitEndMs
      : startMs + Math.max(parseDurationMs(event.workload.duration), 60_000)
    if (startMs >= rangeEndMs || endMs <= rangeStartMs) return []
    return [{ event, startMs, endMs, startDayKey: localDateKey(new Date(startMs)) }]
  }).sort((left, right) => left.startMs === right.startMs
    ? left.event.title.localeCompare(right.event.title)
    : left.startMs - right.startMs)

  for (const prepared of preparedEvents) {
    let cursor = startOfLocalDay(new Date(Math.max(prepared.startMs, rangeStartMs)))
    while (cursor.getTime() < prepared.endMs && cursor.getTime() < rangeEndMs) {
      const day = daysByKey.get(localDateKey(cursor))
      if (day) {
        day.events.push(prepared.event)
        if (day.key === prepared.startDayKey) day.startingEventCount += 1
        else day.continuingEventCount += 1
      }
      cursor = addDays(cursor, 1)
    }
  }
  return {
    label: formatCalendarModelLabel(view, anchor),
    rangeLabel: `${formatCalendarDate(range.from)} - ${formatCalendarDate(addDays(range.to, -1))}`,
    days,
  }
}

function filterRunCalendarEvents(events: RunCalendarEvent[], filter: RunCalendarFilter) {
  if (filter === 'all') return events
  if (filter === 'current') return events.filter(event => isRunCalendarActiveStatus(event.status))
  return events.filter(event => event.source === filter)
}

function shiftRunCalendarAnchor(anchor: Date, view: RunCalendarView, amount: number) {
  if (view === 'month') {
    return new Date(anchor.getFullYear(), anchor.getMonth() + amount, 1)
  }
  return addDays(anchor, amount * (view === 'week' ? 7 : 1))
}

function runCalendarEventContinuesIntoDay(event: RunCalendarEvent, dayStart: Date) {
  const startMs = new Date(event.start_at).getTime()
  if (!Number.isFinite(startMs)) return false
  return startOfLocalDay(new Date(startMs)).getTime() < startOfLocalDay(dayStart).getTime()
}

function startOfLocalDay(date: Date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function startOfWeek(date: Date) {
  const start = startOfLocalDay(date)
  start.setDate(start.getDate() - start.getDay())
  return start
}

function addDays(date: Date, days: number) {
  const next = new Date(date)
  next.setDate(next.getDate() + days)
  return next
}

function localDateKey(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function formatCalendarModelLabel(view: RunCalendarView, anchor: Date) {
  if (view === 'month') {
    return anchor.toLocaleDateString([], { month: 'long', year: 'numeric' })
  }
  if (view === 'week') {
    const range = getRunCalendarVisibleRange(view, anchor)
    return `Week of ${formatCalendarDate(range.from)}`
  }
  return anchor.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })
}

function formatCalendarDate(value: Date | string) {
  const date = typeof value === 'string' ? new Date(value) : value
  if (!Number.isFinite(date.getTime())) return '-'
  return date.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' })
}

// calendarZoneOptions renders a calendar time in the event's declared timezone
// (with a short zone label) so the displayed time matches when the schedule
// actually fires, instead of silently using the browser's local zone. An empty
// or invalid IANA name falls back to browser-local formatting.
function calendarZoneOptions(timezone?: string): Intl.DateTimeFormatOptions {
  const tz = (timezone || '').trim()
  if (!tz) return {}
  try {
    // Throws RangeError on an unknown IANA zone name.
    new Intl.DateTimeFormat([], { timeZone: tz })
    return { timeZone: tz, timeZoneName: 'short' }
  } catch {
    return {}
  }
}

function formatCalendarDateTime(value: string, timezone?: string) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '-'
  return date.toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', ...calendarZoneOptions(timezone) })
}

function formatCalendarTime(value: string, timezone?: string) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '-'
  return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit', ...calendarZoneOptions(timezone) })
}

function formatCalendarDayNumber(date: Date, view: RunCalendarView) {
  if (view === 'month') return String(date.getDate())
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function formatRunCalendarStatus(status: string) {
  return status
    .replace(/_/g, ' ')
    .toLowerCase()
    .replace(/\b\w/g, letter => letter.toUpperCase())
}

function runCalendarSourceLabel(event: RunCalendarEvent) {
  if (event.source === 'cron') return event.projected ? 'Projected Cron' : 'Cron'
  if (event.source === 'ad_hoc') return 'Ad-hoc'
  if (event.source === 'execution_plan') return event.projected ? 'Projected Plan' : 'Execution Plan'
  return event.source || 'Run'
}

function runCalendarUserLabel(event: RunCalendarEvent) {
  return event.user.display_name || event.user.subject || event.user.owner_display_name || event.user.owner_subject || 'Unknown'
}

function runCalendarEventClass(event: RunCalendarEvent) {
  const source = event.source === 'cron' ? 'cron' : event.source === 'ad_hoc' ? 'ad-hoc' : event.source === 'execution_plan' ? 'execution-plan' : 'other'
  const status = event.status.toLowerCase().replace(/[^a-z0-9]+/g, '-')
  return `${source} ${status} ${event.projected ? 'projected' : ''}`
}

function isRunCalendarActiveStatus(status: string) {
  return ['QUEUED', 'PENDING', 'RESERVED', 'INITIALIZING', 'RUNNING', 'STOPPING', 'PAUSED'].includes(status.toUpperCase())
}

function isRunCalendarProblemStatus(status: string) {
  return ['FAILED', 'ABORTED', 'BLOCKED'].includes(status.toUpperCase())
}

function formatRunCalendarNotice(calendar: RunCalendarResponse | null) {
  if (!calendar) return ''
  const notices: string[] = []
  if (calendar.range.history_clamped && calendar.range.history_floor) {
    notices.push(`History starts ${formatCalendarDate(calendar.range.history_floor)}`)
  }
  if (calendar.range.future_clamped) {
    notices.push(`Future view ends ${formatCalendarDate(calendar.range.future_ceiling)}`)
  }
  if (calendar.truncated) {
    notices.push('Result limit reached')
  }
  return notices.join(' / ')
}

function parseDurationMs(value?: string) {
  if (!value) return 60_000
  const compact = value.trim().toLowerCase()
  const matches = compact.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h|d)/g)
  let total = 0
  for (const match of matches) {
    const amount = Number(match[1])
    const unit = match[2]
    if (unit === 'ms') total += amount
    if (unit === 's') total += amount * 1000
    if (unit === 'm') total += amount * 60_000
    if (unit === 'h') total += amount * 3_600_000
    if (unit === 'd') total += amount * 86_400_000
  }
  // Free-text durations reach the calendar verbatim (test.DurationStr, scenario.Duration),
  // so a bare number is common; treat it as minutes rather than flooring to one minute.
  if (total === 0 && /^\d+(?:\.\d+)?$/.test(compact)) total = Number(compact) * 60_000
  return total > 0 ? total : 60_000
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

function getCurrentDashboardOrigin(): string {
  if (typeof window === 'undefined') return ''
  const origin = window.location.origin
  return origin && origin !== 'null' ? trimTrailingSlash(origin) : ''
}

export function mergeDashboardService(services: SystemServicesResponse['services'], dashboardOrigin: string): SystemServicesResponse['services'] {
  if (!dashboardOrigin) return services
  const existingDashboard = services.some(service => {
    if (service.id === 'frontend:dashboard') return true
    return service.kind === 'frontend' && service.url
      ? trimTrailingSlash(service.url) === dashboardOrigin
      : false
  })
  if (existingDashboard) return services
  return [
    ...services,
    {
      id: 'frontend:dashboard',
      name: 'Dashboard',
      kind: 'frontend',
      status: 'running',
      address: dashboardOrigin.replace(/^https?:\/\//, ''),
      url: dashboardOrigin,
      source: 'browser_origin',
      metadata: { current_origin: true },
    },
  ]
}

function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, '')
}

function isActiveServiceStatus(status: string): boolean {
  const normalized = status.trim().toLowerCase()
  return ['active', 'connected', 'healthy', 'idle', 'ok', 'ready', 'running'].includes(normalized)
}

function serviceStatusClass(status: string): string {
  const normalized = status.trim().toLowerCase()
  if (['running', 'healthy', 'ok', 'connected', 'ready', 'active'].includes(normalized)) return 'status-running'
  if (['idle', 'paused'].includes(normalized)) return 'status-idle'
  if (['stopping', 'draining', 'degraded', 'cordoned'].includes(normalized)) return 'status-stopping'
  if (['failed', 'unhealthy', 'unreachable', 'unavailable'].includes(normalized)) return 'status-failed'
  return 'status-queued'
}

function mergeOrder(order: string[], ids: string[]) {
  const validIds = new Set(ids)
  const next = order.filter(id => validIds.has(id))
  const retained = new Set(next)
  ids.forEach((id, desiredIndex) => {
    if (retained.has(id)) return
    const followingId = ids.slice(desiredIndex + 1).find(candidate => next.includes(candidate))
    const insertIndex = followingId ? next.indexOf(followingId) : next.length
    next.splice(insertIndex, 0, id)
    retained.add(id)
  })
  return next
}
