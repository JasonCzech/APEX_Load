import { useCallback, useEffect, useId, useMemo, useRef, useState, type Dispatch, type ReactNode, type SetStateAction } from 'react'
import { type DashboardThemePreference } from '../hooks/useDashboardTheme'
import { useApplicationApi } from '../hooks/api/useApplicationApi'
import { useProjectApi } from '../hooks/api/useProjectApi'
import { useRepositoryApi } from '../hooks/api/useRepositoryApi'
import { useApplication } from '../hooks/useApplication'
import { useProject } from '../hooks/useProject'
import type { ProjectMutation } from '../hooks/projectContext'
import { useApi } from '../hooks/useApi'
import { Application, ApplicationRepository, ArchiveRetentionPlanResponse, CapacityReservation, DashboardAuditEvent, DashboardAuthAccessResource, DashboardAuthDiagnostics, DashboardAuthDiagnosticsCheck, DashboardAuthEffectiveAccessResource, DashboardAuthRoleDefinition, DashboardAuthStatus, DashboardServiceAccount, DashboardSystemSettings, Domain, GitHubProjectPolicy, HealthResponse, Project, Release, SharedDataPool, SharedDataPoolRow, SystemReadinessResponse, UsageAccountingResponse, UsageAccountingSnapshot } from '../types'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { readStoredString, removeStoredItem, writeStoredJSON } from '../utils/browserStorage'
import { getErrorMessage } from '../utils/errorUtils'
import {
  CAPACITY_SETTINGS_SUBSECTIONS,
  GENERAL_SETTINGS_SUBSECTIONS,
  GOVERNANCE_SETTINGS_SUBSECTIONS,
  INTEGRATION_SETTINGS_SUBSECTIONS,
  isCapacitySettingsSubsectionId,
  isGovernanceSettingsSubsectionId,
  isIntegrationSettingsSubsectionId,
  isProjectSettingsSubsectionId,
  mergeSettingsOrder,
  PROJECT_SETTINGS_SUBSECTIONS,
  readSettingsSectionArray,
  readSettingsSubsectionId,
  SETTINGS_SECTIONS,
  type CapacitySettingsSubsectionId,
  type GeneralSettingsSubsectionId,
  type GovernanceSettingsSubsectionId,
  type IntegrationSettingsSubsectionId,
  type ProjectSettingsSubsectionId,
  type SettingsSectionId,
} from './settingsSectionLayout'
import EntitlementsPanel from './EntitlementsPanel'
import EntitlementHeadroom from './EntitlementHeadroom'
import ProjectPoolQuotasCard, { type ProjectPoolQuotasDraftController } from './ProjectPoolQuotasCard'
import ProjectSecretsPanel from './ProjectSecretsPanel'
import { listAllFleetPools } from './fleetCatalogModel'
import {
  formatAccessOperation,
  formatAuditActor,
  formatAuditTimestamp,
  formatAuthDiagnosticStatus,
  formatAuthDiagnosticsHeadline,
  formatAuthMode,
  formatAuthRoleList,
  formatAuthScopes,
  formatAuthStatusSummary,
  formatBytes,
  formatDateTime,
  formatDurationSeconds,
  formatGrantSource,
  formatHeaderContractValue,
  formatProjectRoleAssignments,
  formatReadinessCheckName,
  formatReadinessDetails,
  formatReadinessStatus,
  formatRoleScope,
  formatSchedulePolicyLabel,
  formatStatusCounts,
  fromDatetimeLocal,
  normalizeProjectRedactionRules,
  parseAuthScopes,
  parseOptionalNumber,
  readinessTone,
  sortProjects,
  toDatetimeLocal,
  toDraftRedactionRules,
  type DraftRedactionRule,
} from './settingsPageFormatters'
import {
  applyLiveGeneratorPresetToSettings,
  buildProjectSettingsDraft,
  capacityDraftIssues,
  DEFAULT_SETTINGS,
  deriveLiveGeneratorPreset,
  describeSettingsSaveError,
  GENERATOR_PRESETS,
  normalizeDashboardSystemSettings,
  OIDC_PROVIDER_PRESETS,
  projectGitHubDraftFromPolicy,
  reservationPoolIssues,
  reservationUsageSummary,
  settingsIssuesBySection,
  settingsIssuesForField,
  splitListDraft,
  TELEMETRY_BUDGET_DEFAULTS,
  usesDefaultGeneratorCapacity,
  validateOptionalPositiveInteger,
  validateProjectDraft,
  validateProjectQuotaField,
  validateSettingsDetailed,
  type TelemetryBudgetKey,
} from './settingsPageModel'
import './CreateTestModal.css'
import { downloadBlob } from '../utils/fileUtils'
import { type DataPoolImportResult } from '../hooks/api/useScriptApi'
import LibraryFederationPanel from './LibraryFederationPanel'
import GitHubIntegrationPanel from './GitHubIntegrationPanel'
import ImportedDashboardsPanel from './ImportedDashboardsPanel'
import PowerBIIntegrationPanel from './PowerBIIntegrationPanel'
import PageViewToggle from './PageViewToggle'
import DesktopNotificationsToggle from './DesktopNotificationsToggle'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { ApiError } from '../hooks/apiClient'
import { useFleetApi } from '../hooks/api/useFleetApi'
import type { FleetPool } from '../types/fleet'
import { consumeProjectSecretsSettingsProjectId } from './projectSecretsNavigation'
import { reconcileSettingsProjectSnapshots } from './settingsProjectCatalog'

const SETTINGS_SECTION_TOPBAR_LABELS: Record<SettingsSectionId, string> = {
  general: 'General',
  thresholds: 'Thresholds',
  capacity: 'Capacity',
  domains: 'Domains',
  applications: 'Apps & Releases',
  projects: 'Projects',
  'data-pools': 'Data Pools',
  integrations: 'Integrations',
}

const SETTINGS_SECTION_TOPBAR_OPTIONS = SETTINGS_SECTIONS.map(section => ({
  value: section.id,
  label: SETTINGS_SECTION_TOPBAR_LABELS[section.id],
  ariaLabel: section.title,
  title: section.title,
}))

const NOTIFICATION_FORMAT_OPTIONS = [
  { value: 'slack', label: 'Slack Block Kit' },
  { value: 'teams', label: 'Microsoft Teams Adaptive Card' },
  { value: 'dynatrace', label: 'Dynatrace Events API v2' },
] as const

const DATA_POOL_ROW_PAGE_SIZE = 100

const TELEMETRY_BUDGET_ROWS: Array<{ key: TelemetryBudgetKey; label: string; desc: string }> = [
  { key: 'max_telemetry_sample_age_ms', label: 'Max Telemetry Sample Age (ms)', desc: 'End-to-end live telemetry budget from collection through controller publish. Always enforced; 0 disables this budget.' },
  { key: 'max_telemetry_generator_lag_ms', label: 'Max Generator Lag (ms)', desc: 'Budget for generator-side capture, aggregation, and serialization. Always enforced; 0 disables this budget.' },
  { key: 'max_telemetry_transport_lag_ms', label: 'Max Transport Lag (ms)', desc: 'Budget for generator-to-controller delivery. Always enforced; 0 disables this budget.' },
  { key: 'max_telemetry_controller_fanout_ms', label: 'Max Controller Fan-out (ms)', desc: 'Budget for controller receipt through websocket publish. Always enforced; 0 disables this budget.' },
  { key: 'max_telemetry_browser_render_lag_ms', label: 'Max Browser Render Lag (ms)', desc: 'Budget reserved for dashboard client receive-to-render delay. Always enforced; 0 disables this budget.' },
]

function settingsDraftFieldValue(settings: DashboardSystemSettings, field: string): string | undefined {
  let value: unknown = settings
  for (const segment of field.split('.')) {
    if (Array.isArray(value)) {
      const index = Number(segment)
      if (!Number.isInteger(index) || index < 0 || index >= value.length) return undefined
      value = value[index]
      continue
    }
    if (value === null || typeof value !== 'object') return undefined
    value = (value as Record<string, unknown>)[segment]
  }
  if (value === undefined || value === null) return undefined
  return String(value)
}

function releaseCatalogRowsMatch(left: Release, right: Release) {
  const keys = new Set([...Object.keys(left), ...Object.keys(right)])
  return Array.from(keys).every(key => Object.is(
    (left as unknown as Record<string, unknown>)[key],
    (right as unknown as Record<string, unknown>)[key],
  ))
}

function releaseCatalogSnapshotCaughtUp(listed: Release, committed: Release) {
  if (releaseCatalogRowsMatch(listed, committed)) return true
  const listedUpdatedAt = Date.parse(listed.updated_at)
  const committedUpdatedAt = Date.parse(committed.updated_at)
  return Number.isFinite(listedUpdatedAt)
    && Number.isFinite(committedUpdatedAt)
    && listedUpdatedAt > committedUpdatedAt
}

function reconcileManagedReleaseSnapshots(
  listed: Release[],
  applicationId: string,
  committedById: Map<string, Release>,
  tombstones: Set<string>,
) {
  const listedIds = new Set<string>()
  const reconciled = listed
    .filter(release => !tombstones.has(release.id))
    .map(release => {
      listedIds.add(release.id)
      const committed = committedById.get(release.id)
      if (!committed) return release
      if (releaseCatalogSnapshotCaughtUp(release, committed)) {
        committedById.delete(release.id)
        return release
      }
      return committed
    })
  committedById.forEach(committed => {
    if (
      committed.application_id === applicationId
      && !tombstones.has(committed.id)
      && !listedIds.has(committed.id)
    ) {
      reconciled.push(committed)
    }
  })
  return reconciled
}

interface SettingsConfirmDialogProps {
  open: boolean
  title: string
  children: ReactNode
  confirmLabel: string
  confirmClassName?: string
  confirmDisabled?: boolean
  onCancel: () => void
  onConfirm: () => void
}

function SettingsConfirmDialog({
  open,
  title,
  children,
  confirmLabel,
  confirmClassName = 'btn btn-danger',
  confirmDisabled = false,
  onCancel,
  onConfirm,
}: SettingsConfirmDialogProps) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  useModalDialog({ dialogRef, enabled: open, onClose: onCancel })

  if (!open) return null

  return (
    <div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onCancel()
      }}
    >
      <div
        ref={dialogRef}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <h3 id={titleId}>{title}</h3>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel}>Cancel</button>
        </div>
        <div className="monitor-body" id={descriptionId}>
          {children}
          <div className="settings-inline-actions">
            <button className="btn btn-secondary" type="button" onClick={onCancel}>Cancel</button>
            <button className={confirmClassName} type="button" disabled={confirmDisabled} onClick={onConfirm}>{confirmLabel}</button>
          </div>
        </div>
      </div>
    </div>
  )
}


interface SettingsPageProps {
  theme: DashboardThemePreference
  onThemeChange: (theme: DashboardThemePreference) => void
  authStatus?: DashboardAuthStatus
  onNavigate?: (page: 'access') => void
  domains?: Domain[]
  activeDomain?: Domain | null
  applicationRepository?: ApplicationRepository | null
  onDomainChange?: (domain: Domain) => void
  onDomainsRefresh?: (
    preferredId?: string,
    reconciliation?: { preserveDomain?: Domain; removeDomainId?: string },
  ) => Promise<Domain[]>
  onProjectUpdate?: (projectID: string, project: ProjectMutation) => Promise<Project>
}

type SettingsSubsectionDefinition<SubsectionId extends string> = {
  id: SubsectionId
  title: string
}

export default function SettingsPage({
  theme,
  onThemeChange,
  authStatus,
  onNavigate,
  domains = [],
  activeDomain,
  applicationRepository,
  onDomainChange,
  onDomainsRefresh,
  onProjectUpdate,
}: SettingsPageProps) {
  const desktopSurface = isDesktopSurface()
  const api = useApi()
  const applicationApi = useApplicationApi()
  const projectApi = useProjectApi()
  const repositoryApi = useRepositoryApi()
  const { runGuarded } = useNavigationGuardRunner()
  const {
    applications,
    applicationsLoading,
    applicationsError,
    releases,
    activeApplication,
    activeRelease,
    setActiveApplication,
    setActiveRelease,
    removeApplication,
    removeRelease,
    refreshApplications,
    refreshReleases,
    createAndSelectApplication,
  } = useApplication()
  const {
    activeProject,
    createAndSelectProject,
    updateProject: updateProjectInContext,
  } = useProject()
  const updateProject = onProjectUpdate || updateProjectInContext
  const [settings, setSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [savedSettings, setSavedSettings] = useState<DashboardSystemSettings>(DEFAULT_SETTINGS)
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [authAccessResources, setAuthAccessResources] = useState<DashboardAuthAccessResource[]>([])
  const [authDiagnostics, setAuthDiagnostics] = useState<DashboardAuthDiagnostics | null>(null)
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [authRoles, setAuthRoles] = useState<DashboardAuthRoleDefinition[]>([])
  const [serviceAccounts, setServiceAccounts] = useState<DashboardServiceAccount[]>([])
  const [auditEvents, setAuditEvents] = useState<DashboardAuditEvent[]>([])
  const [auditActionMessage, setAuditActionMessage] = useState('')
  const [auditActionError, setAuditActionError] = useState('')
  const [auditActionFilter, setAuditActionFilter] = useState('')
  const [auditActorTypeFilter, setAuditActorTypeFilter] = useState('')
  const [auditStatusFilter, setAuditStatusFilter] = useState('')
  const [loading, setLoading] = useState(true)
  const [settingsMutationBusy, setSettingsMutationBusy] = useState(false)
  const [saveMessage, setSaveMessage] = useState('')
  const [requestError, setRequestError] = useState('')
  const [activeSection, setActiveSection] = useState<SettingsSectionId>('general')
  const [activeGeneralSubsection, setActiveGeneralSubsection] = useState<GeneralSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.generalSubsection'))
  const [activeGovernanceSubsection, setActiveGovernanceSubsection] = useState<GovernanceSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.governanceSubsection', 'schedule-defaults', isGovernanceSettingsSubsectionId))
  const [activeCapacitySubsection, setActiveCapacitySubsection] = useState<CapacitySettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.capacitySubsection', 'availability', isCapacitySettingsSubsectionId))
  const [activeProjectSubsection, setActiveProjectSubsection] = useState<ProjectSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.projectSubsection', 'catalog', isProjectSettingsSubsectionId))
  const [activeIntegrationSubsection, setActiveIntegrationSubsection] = useState<IntegrationSettingsSubsectionId>(() => readSettingsSubsectionId('apexload.settings.integrationSubsection', 'library', isIntegrationSettingsSubsectionId))
  const [confirmingResetAll, setConfirmingResetAll] = useState(false)
  // Raw text for capacity number inputs mid-edit: a blank or non-numeric draft
  // must not snap the stored number to 0, it blocks save with a field error.
  const [capacityFieldDrafts, setCapacityFieldDrafts] = useState<Record<string, string>>({})
  const [loadFailed, setLoadFailed] = useState(false)
  const [saveRecovery, setSaveRecovery] = useState<'' | 'reload'>('')
  // null = fleet API unavailable (pool validation is skipped).
  const [fleetPools, setFleetPools] = useState<FleetPool[] | null>(null)
  const [sectionOrder, setSectionOrder] = useState<SettingsSectionId[]>(() => readSettingsSectionArray('apexload.settings.sectionOrder'))
  const [selectedDomainId, setSelectedDomainId] = useState(activeDomain?.id || domains[0]?.id || '')
  const [newDomainName, setNewDomainName] = useState('')
  const [newDomainDescription, setNewDomainDescription] = useState('')
  const [domainDraftName, setDomainDraftName] = useState('')
  const [domainDraftDescription, setDomainDraftDescription] = useState('')
  const [domainActionError, setDomainActionError] = useState('')
  const [domainActionMessage, setDomainActionMessage] = useState('')
  const [domainReconciliationWarning, setDomainReconciliationWarning] = useState<{
    message: string
    preferredId?: string
    preserveDomain?: Domain
    removeDomainId?: string
  } | null>(null)
  const [confirmDeleteDomain, setConfirmDeleteDomain] = useState<Domain | null>(null)
  const [managedReleases, setManagedReleases] = useState(releases)
  const [newApplicationName, setNewApplicationName] = useState('')
  const [newApplicationProjectId, setNewApplicationProjectId] = useState('')
  const [applicationActionError, setApplicationActionError] = useState('')
  const [applicationReconciliationWarning, setApplicationReconciliationWarning] = useState<{
    message: string
    scope: 'applications' | 'releases'
    applicationId?: string
  } | null>(null)
  const [renamingApplicationId, setRenamingApplicationId] = useState<string | null>(null)
  const [renamingApplicationName, setRenamingApplicationName] = useState<string | null>(null)
  const [renamingApplicationOriginalName, setRenamingApplicationOriginalName] = useState<string | null>(null)
  const [renamingReleaseId, setRenamingReleaseId] = useState<string | null>(null)
  const [renamingReleaseName, setRenamingReleaseName] = useState('')
  const [renamingReleaseOriginalName, setRenamingReleaseOriginalName] = useState<string | null>(null)
  const [renamingReleaseApplicationId, setRenamingReleaseApplicationId] = useState<string | null>(null)
  const [confirmDeleteApp, setConfirmDeleteApp] = useState(false)
  const [confirmDeleteRelease, setConfirmDeleteRelease] = useState<{ id: string; name: string } | null>(null)
  const [confirmArchiveRelease, setConfirmArchiveRelease] = useState<{ id: string; name: string } | null>(null)
  const [confirmApplyRetention, setConfirmApplyRetention] = useState(false)
  const [confirmDeletePool, setConfirmDeletePool] = useState<SharedDataPool | null>(null)
  const [confirmDeleteRow, setConfirmDeleteRow] = useState<SharedDataPoolRow | null>(null)
  const [projects, setProjects] = useState<Project[]>([])
  const [selectedProjectId, setSelectedProjectId] = useState('')
  const [newProjectName, setNewProjectName] = useState('')
  const [newProjectDomainId, setNewProjectDomainId] = useState('')
  const [projectDraftName, setProjectDraftName] = useState('')
  const [projectDraftRules, setProjectDraftRules] = useState<DraftRedactionRule[]>([])
  const [projectDraftMaxConcurrentTests, setProjectDraftMaxConcurrentTests] = useState('')
  const [projectDraftMaxVUsers, setProjectDraftMaxVUsers] = useState('')
  const [projectDraftMaxVUsersPerTest, setProjectDraftMaxVUsersPerTest] = useState('')
  const [projectDraftMaxTestDurationMinutes, setProjectDraftMaxTestDurationMinutes] = useState('')
  const [projectDraftGitHubEnabled, setProjectDraftGitHubEnabled] = useState(false)
  const [projectDraftGitHubAllowedRepos, setProjectDraftGitHubAllowedRepos] = useState('')
  const [projectDraftGitHubAllowedBranches, setProjectDraftGitHubAllowedBranches] = useState('')
  const [projectDraftGitHubPathTemplate, setProjectDraftGitHubPathTemplate] = useState('')
  const [projectDraftGitHubRequirePR, setProjectDraftGitHubRequirePR] = useState(true)
  const [projectDraftGitHubAllowDirect, setProjectDraftGitHubAllowDirect] = useState(false)
  const [projectDraftGitHubAllowImport, setProjectDraftGitHubAllowImport] = useState(true)
  const [projectDraftGitHubAllowPush, setProjectDraftGitHubAllowPush] = useState(true)
  const [projectDraftGitHubBulkSync, setProjectDraftGitHubBulkSync] = useState(false)
  const [projectDraftGitHubAutoSync, setProjectDraftGitHubAutoSync] = useState(false)
  const [projectDraftGitHubChecks, setProjectDraftGitHubChecks] = useState(false)
  const [projectDraftGitHubAppInstallation, setProjectDraftGitHubAppInstallation] = useState('')
  const [projectDraftGitHubDefaultRepo, setProjectDraftGitHubDefaultRepo] = useState('')
  const [projectDraftGitHubDefaultBranch, setProjectDraftGitHubDefaultBranch] = useState('')
  const [projectDraftGitHubSyncDirectory, setProjectDraftGitHubSyncDirectory] = useState('')
  const [projectDraftGitHubAutomationIdentity, setProjectDraftGitHubAutomationIdentity] = useState('')
  const [projectActionError, setProjectActionError] = useState('')
  const [projectActionMessage, setProjectActionMessage] = useState('')
  const [missingRequestedProjectId, setMissingRequestedProjectId] = useState('')
  const [projectSecretCount, setProjectSecretCount] = useState(0)
  const [archiveRetentionPlan, setArchiveRetentionPlan] = useState<ArchiveRetentionPlanResponse | null>(null)
  const [archiveRetentionActionError, setArchiveRetentionActionError] = useState('')
  const [archiveRetentionActionMessage, setArchiveRetentionActionMessage] = useState('')
  const [usageAccounting, setUsageAccounting] = useState<UsageAccountingResponse | null>(null)
  const [usageSnapshots, setUsageSnapshots] = useState<UsageAccountingSnapshot[]>([])
  const [usageActionError, setUsageActionError] = useState('')
  const [usageActionMessage, setUsageActionMessage] = useState('')
  const [systemReadiness, setSystemReadiness] = useState<SystemReadinessResponse | null>(null)
  const [readinessActionError, setReadinessActionError] = useState('')
  const [readinessActionMessage, setReadinessActionMessage] = useState('')
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [selectedDataPoolId, setSelectedDataPoolId] = useState('')
  const [dataPoolRows, setDataPoolRows] = useState<SharedDataPoolRow[]>([])
  const [dataPoolName, setDataPoolName] = useState('')
  const [dataPoolDescription, setDataPoolDescription] = useState('')
  const [dataPoolProjectId, setDataPoolProjectId] = useState('')
  const [dataPoolRowJson, setDataPoolRowJson] = useState('{\n  "customer_id": "C-10001"\n}')
  const [editingDataPoolRowId, setEditingDataPoolRowId] = useState<string | null>(null)
  const [dataPoolActionError, setDataPoolActionError] = useState('')
  const [dataPoolActionNotice, setDataPoolActionNotice] = useState('')
  const [dataPoolRowsTotal, setDataPoolRowsTotal] = useState(0)
  const [dataPoolRowsNextCursor, setDataPoolRowsNextCursor] = useState<string | null>(null)
  const [dataPoolRowsHasMore, setDataPoolRowsHasMore] = useState(false)
  const [dataPoolRowsLoadBusy, setDataPoolRowsLoadBusy] = useState(false)
  const [dataPoolCatalogMutationBusy, setDataPoolCatalogMutationBusy] = useState(false)
  // The draft binds the uploaded file to the pool it was previewed against, so
  // a pool switch during the dry-run upload can never commit into another pool.
  const [dataPoolImportDraft, setDataPoolImportDraft] = useState<{ poolId: string; file: File; preview: DataPoolImportResult } | null>(null)
  const [dataPoolImportMode, setDataPoolImportMode] = useState<'append' | 'replace'>('append')
  const [dataPoolImportBusy, setDataPoolImportBusy] = useState(false)
  const [dataPoolExportBusy, setDataPoolExportBusy] = useState(false)
  // Concurrency token captured when the editor opens; looking it up at save
  // time would silently refresh past another writer's change.
  const [editingDataPoolRowVersion, setEditingDataPoolRowVersion] = useState<number | null>(null)
  const [dataPoolRowSaveBusy, setDataPoolRowSaveBusy] = useState(false)
  const [dataPoolRowDeleteBusy, setDataPoolRowDeleteBusy] = useState(false)
  const dataPoolImportInputRef = useRef<HTMLInputElement | null>(null)
  const selectedDataPoolIdRef = useRef('')
  const settingsLoadGenerationRef = useRef(0)
  const settingsMutationBusyRef = useRef(false)
  const dataPoolCatalogMutationBusyRef = useRef(false)
  const dataPoolRowDeleteBusyRef = useRef(false)
  const dataPoolLoadGenerationRef = useRef(0)
  const dataPoolRowsLoadGenerationRef = useRef(0)
  const activeDataPoolProjectIdRef = useRef(activeProject?.id)
  const activeApplicationIdRef = useRef(activeApplication?.id)
  const poolQuotaDraftRef = useRef<ProjectPoolQuotasDraftController | null>(null)
  const committedManagedReleasesRef = useRef(new Map<string, Release>())
  const managedReleaseTombstonesRef = useRef(new Set<string>())
  const committedProjectsRef = useRef(new Map<string, Project>())
  const [poolQuotaDraftDirty, setPoolQuotaDraftDirty] = useState(false)
  activeDataPoolProjectIdRef.current = activeProject?.id
  activeApplicationIdRef.current = activeApplication?.id

  const commitSettingsProject = useCallback((project: Project) => {
    committedProjectsRef.current.set(project.id, project)
    setProjects(previous => sortProjects([
      ...previous.filter(candidate => candidate.id !== project.id),
      project,
    ]))
  }, [])

  const loadProjectGitHubDraft = useCallback((policy?: GitHubProjectPolicy) => {
    setProjectDraftGitHubEnabled(Boolean(policy?.enabled))
    setProjectDraftGitHubAllowedRepos((policy?.allowed_repos || []).join(', '))
    setProjectDraftGitHubAllowedBranches((policy?.allowed_branches || []).join(', '))
    setProjectDraftGitHubPathTemplate(policy?.path_template || '')
    setProjectDraftGitHubRequirePR(policy?.require_pull_request ?? true)
    setProjectDraftGitHubAllowDirect(Boolean(policy?.allow_direct_commit))
    setProjectDraftGitHubAllowImport(policy?.allow_import ?? true)
    setProjectDraftGitHubAllowPush(policy?.allow_push ?? true)
    setProjectDraftGitHubBulkSync(Boolean(policy?.bulk_sync_enabled))
    setProjectDraftGitHubAutoSync(Boolean(policy?.auto_sync_enabled))
    setProjectDraftGitHubChecks(Boolean(policy?.checks_enabled))
    setProjectDraftGitHubAppInstallation(policy?.app_installation_id ? String(policy.app_installation_id) : '')
    setProjectDraftGitHubDefaultRepo(policy?.default_repo || '')
    setProjectDraftGitHubDefaultBranch(policy?.default_branch || '')
    setProjectDraftGitHubSyncDirectory(policy?.default_sync_directory || '')
    setProjectDraftGitHubAutomationIdentity(policy?.automation_identity || '')
  }, [])

  const buildProjectGitHubDraft = useCallback((): GitHubProjectPolicy => ({
    enabled: projectDraftGitHubEnabled,
    allowed_repos: splitListDraft(projectDraftGitHubAllowedRepos),
    allowed_branches: splitListDraft(projectDraftGitHubAllowedBranches),
    path_template: projectDraftGitHubPathTemplate.trim(),
    require_pull_request: projectDraftGitHubRequirePR,
    allow_direct_commit: projectDraftGitHubAllowDirect,
    allow_import: projectDraftGitHubAllowImport,
    allow_push: projectDraftGitHubAllowPush,
    bulk_sync_enabled: projectDraftGitHubBulkSync,
    auto_sync_enabled: projectDraftGitHubAutoSync,
    checks_enabled: projectDraftGitHubChecks,
    app_installation_id: projectDraftGitHubAppInstallation.trim() ? Number(projectDraftGitHubAppInstallation) : undefined,
    default_repo: projectDraftGitHubDefaultRepo.trim(),
    default_branch: projectDraftGitHubDefaultBranch.trim(),
    default_sync_directory: projectDraftGitHubSyncDirectory.trim(),
    automation_identity: projectDraftGitHubAutomationIdentity.trim(),
  }), [
    projectDraftGitHubAllowDirect,
    projectDraftGitHubAllowImport,
    projectDraftGitHubAllowPush,
    projectDraftGitHubAllowedBranches,
    projectDraftGitHubAllowedRepos,
    projectDraftGitHubAppInstallation,
    projectDraftGitHubAutoSync,
    projectDraftGitHubAutomationIdentity,
    projectDraftGitHubBulkSync,
    projectDraftGitHubChecks,
    projectDraftGitHubDefaultBranch,
    projectDraftGitHubDefaultRepo,
    projectDraftGitHubEnabled,
    projectDraftGitHubPathTemplate,
    projectDraftGitHubRequirePR,
    projectDraftGitHubSyncDirectory,
  ])

  const loadSettings = useCallback(async () => {
    const generation = ++settingsLoadGenerationRef.current
    setLoading(true)
    setRequestError('')
    setSaveRecovery('')
    try {
      const response = normalizeDashboardSystemSettings(await api.getSystemSettings())
      if (generation !== settingsLoadGenerationRef.current) return
      let resolvedSettings = response
      if (usesDefaultGeneratorCapacity(response)) {
        try {
          const inventory = await api.getSystemInventory()
          if (generation !== settingsLoadGenerationRef.current) return
          resolvedSettings = applyLiveGeneratorPresetToSettings(response, deriveLiveGeneratorPreset(inventory))
        } catch {
          if (generation !== settingsLoadGenerationRef.current) return
          resolvedSettings = response
        }
      }
      if (generation !== settingsLoadGenerationRef.current) return
      setSettings(resolvedSettings)
      setSavedSettings(resolvedSettings)
      setCapacityFieldDrafts({})
      setLoadFailed(false)
    } catch (error: unknown) {
      if (generation !== settingsLoadGenerationRef.current) return
      setRequestError(getErrorMessage(error, 'Unable to load system settings'))
      // Keep the form shape intact but disabled (loadFailed gates every
      // fieldset), so fabricated defaults are never editable or fake-clean.
      setSettings(DEFAULT_SETTINGS)
      setSavedSettings(DEFAULT_SETTINGS)
      setCapacityFieldDrafts({})
      setLoadFailed(true)
    } finally {
      if (generation === settingsLoadGenerationRef.current) setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void loadSettings()
    return () => {
      settingsLoadGenerationRef.current += 1
    }
  }, [loadSettings])

  const fleetApi = useFleetApi()
  useEffect(() => {
    let cancelled = false
    listAllFleetPools(fleetApi.listFleetPools)
      .then(nextPools => {
        if (!cancelled) setFleetPools(nextPools)
      })
      .catch(() => {
        if (!cancelled) setFleetPools(null)
      })
    return () => {
      cancelled = true
    }
  }, [fleetApi])

  useEffect(() => {
    let cancelled = false
    const loadEffectiveAccess = async () => {
      const selectedAccessProject = projects.find(project => project.id === selectedProjectId)
      const accessProjectId = selectedAccessProject?.id || activeProject?.id
      const accessDomainId = selectedAccessProject?.domain_id || activeProject?.domain_id
      setEffectiveAuthAccessResources([])
      try {
        const response = await api.getAuthEffectiveAccess(accessProjectId, {
          domain_id: accessDomainId,
          application_repository_id: applicationRepository?.id,
        })
        if (!cancelled) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
        }
      }
    }

    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.domain_id, activeProject?.id, api, applicationRepository?.id, projects, selectedProjectId])

  useEffect(() => {
  let cancelled = false
  const loadAuthDiagnostics = async () => {
    try {
      const response = await api.getAuthDiagnostics()
      if (!cancelled) {
        setAuthDiagnostics(response)
      }
    } catch {
      if (!cancelled) {
        setAuthDiagnostics(null)
      }
    }
  }

  loadAuthDiagnostics()
  return () => {
    cancelled = true
  }
  }, [api])

  useEffect(() => {
    let cancelled = false
    // Single fetch for the static RBAC policy (role catalog + access matrix) via /auth/policy.
    const loadAuthPolicy = async () => {
      try {
        const response = await api.getAuthPolicy()
        if (!cancelled) {
          setAuthAccessResources(response.resources || [])
          setAuthRoles(response.roles || [])
        }
      } catch {
        if (!cancelled) {
          setAuthAccessResources([])
          setAuthRoles([])
        }
      }
    }

    loadAuthPolicy()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadServiceAccounts = async () => {
      try {
        const response = await api.listAuthServiceAccounts()
        if (!cancelled) {
          setServiceAccounts(response.service_accounts || [])
        }
      } catch {
        if (!cancelled) {
          setServiceAccounts([])
        }
      }
    }

    loadServiceAccounts()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadAuditEvents = async () => {
      try {
        const response = await api.listAuthAuditEvents({
          limit: 20,
          action: auditActionFilter.trim() || undefined,
          actor_type: auditActorTypeFilter.trim() || undefined,
          status: auditStatusFilter.trim() || undefined,
        })
        if (!cancelled) {
          setAuditEvents(response.events || [])
        }
      } catch {
        if (!cancelled) {
          setAuditEvents([])
        }
      }
    }

    loadAuditEvents()
    return () => {
      cancelled = true
    }
  }, [api, auditActionFilter, auditActorTypeFilter, auditStatusFilter])

  useEffect(() => {
    let cancelled = false
    const loadUsage = async () => {
      try {
        const [usageResponse, snapshotResponse] = await Promise.all([
          api.getUsageAccounting(activeProject?.id),
          api.listUsageAccountingSnapshots(activeProject?.id),
        ])
        if (!cancelled) {
          setUsageAccounting(usageResponse)
          setUsageSnapshots(snapshotResponse.snapshots || [])
        }
      } catch {
        if (!cancelled) {
          setUsageAccounting(null)
          setUsageSnapshots([])
        }
      }
    }

    loadUsage()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api])

  useEffect(() => {
    let cancelled = false
    const loadReadiness = async () => {
      try {
        const response = await api.getSystemReadiness()
        if (!cancelled) {
          setSystemReadiness(response)
        }
      } catch {
        if (!cancelled) {
          setSystemReadiness(null)
        }
      }
    }

    loadReadiness()
    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    let cancelled = false
    const loadHealth = async () => {
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') {
        return
      }
      try {
        const response = await api.getHealth()
        if (!cancelled) setHealth(response)
      } catch {
        if (!cancelled) setHealth(null)
      }
    }

    loadHealth()
    const interval = setInterval(loadHealth, 5000)

    const handleVisibilityChange = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
        loadHealth()
      }
    }

    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', handleVisibilityChange)
    }

    return () => {
      cancelled = true
      clearInterval(interval)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', handleVisibilityChange)
      }
    }
  }, [api])

  const validationIssues = useMemo(() => [
    ...validateSettingsDetailed(settings),
    ...capacityDraftIssues(capacityFieldDrafts),
    ...reservationPoolIssues(settings.capacity_planning.reservations, fleetPools),
  ], [capacityFieldDrafts, fleetPools, settings])
  const validationErrors = useMemo(() => validationIssues.map(issue => issue.message), [validationIssues])
  const issuesBySection = useMemo(() => settingsIssuesBySection(validationIssues), [validationIssues])
  const hasCapacityDraftChanges = useMemo(
    () => Object.entries(capacityFieldDrafts).some(
      ([field, value]) => value !== settingsDraftFieldValue(savedSettings, field),
    ),
    [capacityFieldDrafts, savedSettings],
  )
  const hasUnsavedChanges = useMemo(
    () => JSON.stringify(settings) !== JSON.stringify(savedSettings) || hasCapacityDraftChanges,
    [hasCapacityDraftChanges, settings, savedSettings],
  )
  const authDiagnosticsStale = useMemo(() => JSON.stringify(settings.auth) !== JSON.stringify(savedSettings.auth), [settings.auth, savedSettings.auth])
  const canManageSystem = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_system'), [effectiveAuthAccessResources])
  const canManageDomains = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_domains', false), [effectiveAuthAccessResources])
  const canManageProjects = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects'), [effectiveAuthAccessResources])
  const canManageScripts = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_scripts'), [effectiveAuthAccessResources])
  const canManageServiceAccounts = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_service_accounts'), [effectiveAuthAccessResources])
  const canViewAudit = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'view_audit'), [effectiveAuthAccessResources])
  const canSave = validationErrors.length === 0 && !loading && !loadFailed && !settingsMutationBusy && hasUnsavedChanges && canManageSystem
  const formDisabled = loading || loadFailed || settingsMutationBusy || !canManageSystem
  const renderFieldIssues = (field: string) => settingsIssuesForField(validationIssues, field).map(message => (
    <p key={message} className="settings-error-inline settings-field-error" role="alert">{message}</p>
  ))
  const selectedDomain = useMemo(
    () => domains.find(domain => domain.id === selectedDomainId) || domains.find(domain => domain.id === activeDomain?.id) || domains[0] || null,
    [activeDomain?.id, domains, selectedDomainId],
  )
  const projectCountByDomain = useMemo(() => {
    const counts = new Map<string, number>()
    projects.forEach(project => {
      const domainID = project.domain_id || 'default'
      counts.set(domainID, (counts.get(domainID) || 0) + 1)
    })
    return counts
  }, [projects])
  const selectedDomainProjectCount = selectedDomain ? projectCountByDomain.get(selectedDomain.id) || 0 : 0
  const domainDraftValidationError = selectedDomain && !domainDraftName.trim() ? 'Domain name is required.' : ''
  const domainHasUnsavedChanges = Boolean(
    selectedDomain
    && (
      domainDraftName.trim() !== selectedDomain.name
      || domainDraftDescription.trim() !== (selectedDomain.description || '')
    ),
  )
  const selectedProject = useMemo(
    () => projects.find(project => project.id === selectedProjectId) || null,
    [projects, selectedProjectId],
  )
  const projectDraftValidationError = useMemo(
    () => {
      if (!selectedProject) return ''
      return (
        validateProjectDraft(projectDraftName, projectDraftRules) ||
        validateProjectQuotaField('Project max concurrent tests', projectDraftMaxConcurrentTests) ||
        validateProjectQuotaField('Project max VUsers', projectDraftMaxVUsers) ||
        validateProjectQuotaField('Project max VUsers per test', projectDraftMaxVUsersPerTest) ||
        validateProjectQuotaField('Project max test duration (minutes)', projectDraftMaxTestDurationMinutes) ||
        validateOptionalPositiveInteger('GitHub App installation ID', projectDraftGitHubAppInstallation)
      )
    },
    [projectDraftGitHubAppInstallation, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, projectDraftName, projectDraftRules, selectedProject],
  )
  const projectHasUnsavedChanges = useMemo(() => {
    if (!selectedProject) {
      return false
    }

    const current = {
      name: selectedProject.name,
      settings: buildProjectSettingsDraft(
        toDraftRedactionRules(selectedProject.settings?.redaction_rules),
        selectedProject.settings?.max_concurrent_tests !== undefined ? String(selectedProject.settings.max_concurrent_tests) : '',
        selectedProject.settings?.max_vusers !== undefined ? String(selectedProject.settings.max_vusers) : '',
        selectedProject.settings?.max_vusers_per_test !== undefined ? String(selectedProject.settings.max_vusers_per_test) : '',
        selectedProject.settings?.max_test_duration_minutes !== undefined ? String(selectedProject.settings.max_test_duration_minutes) : '',
        projectGitHubDraftFromPolicy(selectedProject.settings?.github),
        selectedProject.settings?.pool_quotas,
      ),
    }
    const draft = {
      name: projectDraftName.trim(),
      settings: buildProjectSettingsDraft(projectDraftRules, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, buildProjectGitHubDraft(), selectedProject?.settings?.pool_quotas),
    }

    return JSON.stringify(current) !== JSON.stringify(draft)
  }, [
    buildProjectGitHubDraft,
    projectDraftMaxConcurrentTests,
    projectDraftMaxTestDurationMinutes,
    projectDraftMaxVUsers,
    projectDraftMaxVUsersPerTest,
    projectDraftName,
    projectDraftRules,
    selectedProject,
  ])
  const customRuleCount = useMemo(
    () => normalizeProjectRedactionRules(projectDraftRules).length,
    [projectDraftRules],
  )
  const availabilitySnapshot = health?.capacity_availability
  const notificationWebhookCount = settings.notifications.webhooks.length
  const availabilitySourceLabel = availabilitySnapshot?.source === 'configured_plus_live'
    ? availabilitySnapshot.live_available_generators !== undefined
      ? 'Live snapshot + configured ceiling'
      : 'Configured ceiling only'
    : 'Configured inventory only'
  const generalSubsectionSummaries = useMemo<Record<GeneralSettingsSubsectionId, string>>(() => ({
    platform: `${settings.general.max_concurrent_tests} tests`,
    usage: usageAccounting ? `${usageAccounting.totals.run_count} runs` : 'No usage yet',
    entitlements: 'Edition & limits',
    readiness: formatReadinessStatus(systemReadiness?.status),
    authentication: formatAuthMode(settings.auth.mode),
    governance: `${auditEvents.length} audit event${auditEvents.length === 1 ? '' : 's'}`,
  }), [auditEvents.length, settings.auth.mode, settings.general.max_concurrent_tests, systemReadiness?.status, usageAccounting])
  const governanceSubsectionSummaries = useMemo<Record<GovernanceSettingsSubsectionId, string>>(() => ({
    'schedule-defaults': formatSchedulePolicyLabel(settings.schedule_defaults.approval_policy || 'auto'),
    'script-imports': settings.script_imports.allow_source_secret_migration ? 'Migration enabled' : 'Migration disabled',
    'service-account-apis': `${serviceAccounts.length} account${serviceAccounts.length === 1 ? '' : 's'}`,
    'recent-audit-events': `${auditEvents.length} event${auditEvents.length === 1 ? '' : 's'}`,
  }), [auditEvents.length, serviceAccounts.length, settings.schedule_defaults.approval_policy, settings.script_imports.allow_source_secret_migration])
  const capacitySubsectionSummaries = useMemo<Record<CapacitySettingsSubsectionId, string>>(() => ({
    availability: `${availabilitySnapshot?.effective_available_generators ?? settings.capacity_planning.availability.configured_available_generators} available`,
    generator: `${settings.capacity_planning.generator.cpu_cores} CPU / ${settings.capacity_planning.generator.memory_mb} MB`,
    reservations: `${settings.capacity_planning.reservations?.length ?? 0} reserved`,
    retention: settings.archive_retention.enabled ? `${settings.archive_retention.default_retention_days} days` : 'Disabled',
  }), [
    availabilitySnapshot?.effective_available_generators,
    settings.archive_retention.default_retention_days,
    settings.archive_retention.enabled,
    settings.capacity_planning.availability.configured_available_generators,
    settings.capacity_planning.generator.cpu_cores,
    settings.capacity_planning.generator.memory_mb,
    settings.capacity_planning.reservations?.length,
  ])
  const capacityIssueBadges = useMemo(() => {
    const counts: Partial<Record<CapacitySettingsSubsectionId, number>> = {}
    ;(issuesBySection.capacity ?? []).forEach(issue => {
      if (!issue.subsection || !isCapacitySettingsSubsectionId(issue.subsection)) return
      counts[issue.subsection] = (counts[issue.subsection] ?? 0) + 1
    })
    return counts
  }, [issuesBySection])
  const reservationUsage = useMemo(() => reservationUsageSummary(settings), [settings])
  const projectSubsectionSummaries = useMemo<Record<ProjectSettingsSubsectionId, string>>(() => ({
    catalog: `${projects.length} project${projects.length === 1 ? '' : 's'}`,
    limits: selectedProject ? selectedProject.name : 'Select project',
    github: projectDraftGitHubEnabled ? 'Enabled' : 'Disabled',
    redaction: `${customRuleCount} rule${customRuleCount === 1 ? '' : 's'}`,
    secrets: `${projectSecretCount} configured`,
  }), [customRuleCount, projectDraftGitHubEnabled, projectSecretCount, projects.length, selectedProject])
  const integrationSubsectionSummaries = useMemo<Record<IntegrationSettingsSubsectionId, string>>(() => ({
    library: activeProject?.name || 'Project scoped',
    github: settings.github.enabled ? 'Enabled' : 'Disabled',
    prometheus: settings.prometheus.enabled ? 'Enabled' : 'Disabled',
    dashboards: 'Grafana import',
    powerbi: settings.powerbi?.enabled ? 'Enabled' : 'Disabled',
    notifications: `${notificationWebhookCount} webhook${notificationWebhookCount === 1 ? '' : 's'}`,
  }), [activeProject?.name, notificationWebhookCount, settings.github.enabled, settings.prometheus.enabled, settings.powerbi?.enabled])

  useEffect(() => {
    setSectionOrder(prev => mergeSettingsOrder(prev))
  }, [])

  useEffect(() => {
    const targetSection = readStoredString('apexload.settings.focusSection') as SettingsSectionId | ''
    if (!targetSection) return
    removeStoredItem('apexload.settings.focusSection')
    if (!SETTINGS_SECTIONS.some(section => section.id === targetSection)) return
    setActiveSection(targetSection)
    const targetSubsection = readStoredString('apexload.settings.focusSubsection')
    if (targetSubsection) {
      removeStoredItem('apexload.settings.focusSubsection')
      if (targetSection === 'capacity' && isCapacitySettingsSubsectionId(targetSubsection)) {
        setActiveCapacitySubsection(targetSubsection)
      }
    }
    window.setTimeout(() => {
      document.getElementById(`settings-section-${targetSection}`)?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    }, 150)
  }, [])

  useEffect(() => {
    let cancelled = false
    const loadManagedReleases = async () => {
      if (!activeApplication?.id) {
        setManagedReleases([])
        return
      }
      try {
        const response = await applicationApi.listReleases(activeApplication.id, true)
        if (!cancelled) {
          setManagedReleases(reconcileManagedReleaseSnapshots(
            response.releases || [],
            activeApplication.id,
            committedManagedReleasesRef.current,
            managedReleaseTombstonesRef.current,
          ))
        }
      } catch {
        if (!cancelled) {
          setManagedReleases(reconcileManagedReleaseSnapshots(
            releases,
            activeApplication.id,
            committedManagedReleasesRef.current,
            managedReleaseTombstonesRef.current,
          ))
        }
      }
    }
    loadManagedReleases()
    return () => {
      cancelled = true
    }
  }, [activeApplication?.id, applicationApi, releases])

  useEffect(() => {
    let cancelled = false
    const loadProjects = async () => {
      try {
        const response = await projectApi.listProjects()
        if (cancelled) {
          return
        }

        const nextProjects = reconcileSettingsProjectSnapshots(
          response.projects || [],
          committedProjectsRef.current,
        )
        const requestedProjectId = consumeProjectSecretsSettingsProjectId()
        const requestedProject = requestedProjectId
          ? nextProjects.find(project => project.id === requestedProjectId) || null
          : null
        const requestedProjectMissing = Boolean(requestedProjectId && !requestedProject)
        // A Configure secrets deep link is a governed project-scoped action. If
        // its target disappeared (or access was revoked), do not silently switch
        // the write-only form to a different project.
        const nextSelected = requestedProjectMissing
          ? null
          : requestedProject
            || nextProjects.find(project => project.id === activeProject?.id)
            || nextProjects[0]
            || null
        setProjects(nextProjects)
        setSelectedProjectId(nextSelected?.id || '')
        setProjectDraftName(nextSelected?.name || '')
        setProjectDraftRules(toDraftRedactionRules(nextSelected?.settings?.redaction_rules))
        setProjectDraftMaxConcurrentTests(nextSelected?.settings?.max_concurrent_tests !== undefined ? String(nextSelected.settings.max_concurrent_tests) : '')
        setProjectDraftMaxVUsers(nextSelected?.settings?.max_vusers !== undefined ? String(nextSelected.settings.max_vusers) : '')
        setProjectDraftMaxVUsersPerTest(nextSelected?.settings?.max_vusers_per_test !== undefined ? String(nextSelected.settings.max_vusers_per_test) : '')
        setProjectDraftMaxTestDurationMinutes(nextSelected?.settings?.max_test_duration_minutes !== undefined ? String(nextSelected.settings.max_test_duration_minutes) : '')
        loadProjectGitHubDraft(nextSelected?.settings?.github)
        setMissingRequestedProjectId(requestedProjectMissing ? requestedProjectId : '')
        setProjectActionError(requestedProjectMissing
          ? 'The project requested for secret configuration is no longer available or you no longer have access. Choose an available project explicitly before entering a credential.'
          : '')
      } catch (error: unknown) {
        if (!cancelled) {
          setProjectActionError(getErrorMessage(error, 'Unable to load projects'))
        }
      }
    }

    loadProjects()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, loadProjectGitHubDraft, projectApi])

  useEffect(() => {
    if (domains.length === 0) {
      setSelectedDomainId('')
      return
    }
    setSelectedDomainId(prev => {
      if (activeDomain?.id && domains.some(domain => domain.id === activeDomain.id)) {
        return activeDomain.id
      }
      if (prev && domains.some(domain => domain.id === prev)) {
        return prev
      }
      return domains[0].id
    })
  }, [activeDomain?.id, domains])

  useEffect(() => {
    setDomainDraftName(selectedDomain?.name || '')
    setDomainDraftDescription(selectedDomain?.description || '')
  }, [selectedDomain?.description, selectedDomain?.id, selectedDomain?.name])

  useEffect(() => {
    writeStoredJSON('apexload.settings.sectionOrder', sectionOrder)
  }, [sectionOrder])

  useEffect(() => {
    writeStoredJSON('apexload.settings.generalSubsection', activeGeneralSubsection)
  }, [activeGeneralSubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.governanceSubsection', activeGovernanceSubsection)
  }, [activeGovernanceSubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.capacitySubsection', activeCapacitySubsection)
  }, [activeCapacitySubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.projectSubsection', activeProjectSubsection)
  }, [activeProjectSubsection])

  useEffect(() => {
    writeStoredJSON('apexload.settings.integrationSubsection', activeIntegrationSubsection)
  }, [activeIntegrationSubsection])

  const loadDataPools = useCallback(async (preferredId?: string): Promise<boolean> => {
    const generation = ++dataPoolLoadGenerationRef.current
    const projectId = activeProject?.id
    try {
      const response = await api.listDataPools(projectId)
      if (generation !== dataPoolLoadGenerationRef.current || projectId !== activeDataPoolProjectIdRef.current) return false
      const pools: SharedDataPool[] = response.data_pools || []
      setDataPools(pools)
      // Only ids from this response are selectable: a project switch must not
      // carry the previous project's selection into the new project's panel.
      const isMember = (id?: string) => Boolean(id && pools.some(pool => pool.id === id))
      const currentSelection = selectedDataPoolIdRef.current
      const nextSelected = isMember(preferredId) ? (preferredId as string) : isMember(currentSelection) ? currentSelection : (pools[0]?.id || '')
      // Row loading belongs to the selection effect, which carries the
      // cancellation guard; fetching here as well raced it with stale results.
      selectedDataPoolIdRef.current = nextSelected
      setSelectedDataPoolId(nextSelected)
      if (!nextSelected) {
        setDataPoolRows([])
        setDataPoolRowsTotal(0)
        setDataPoolRowsNextCursor(null)
        setDataPoolRowsHasMore(false)
      }
      setDataPoolActionError('')
      return true
    } catch (error: unknown) {
      if (generation !== dataPoolLoadGenerationRef.current || projectId !== activeDataPoolProjectIdRef.current) return false
      setDataPoolActionError(getErrorMessage(error, 'Unable to load data pools'))
      return false
    }
  }, [activeProject?.id, api])

  useEffect(() => {
    // Never show or retain a selection from the previous project while the
    // newly scoped request is in flight.
    dataPoolLoadGenerationRef.current += 1
    selectedDataPoolIdRef.current = ''
    setDataPools([])
    setSelectedDataPoolId('')
    setDataPoolRows([])
    setDataPoolRowsTotal(0)
    setDataPoolRowsNextCursor(null)
    setDataPoolRowsHasMore(false)
    setDataPoolRowsLoadBusy(false)
    setDataPoolActionNotice('')
    void loadDataPools()
    return () => {
      dataPoolLoadGenerationRef.current += 1
    }
  }, [loadDataPools])

  useEffect(() => {
    let cancelled = false
    const generation = ++dataPoolRowsLoadGenerationRef.current
    selectedDataPoolIdRef.current = selectedDataPoolId
    setDataPoolImportDraft(null)
    setDataPoolImportMode('append')
    setEditingDataPoolRowId(null)
    setEditingDataPoolRowVersion(null)
    setDataPoolRows([])
    setDataPoolRowsTotal(0)
    setDataPoolRowsNextCursor(null)
    setDataPoolRowsHasMore(false)
    setDataPoolRowsLoadBusy(Boolean(selectedDataPoolId))
    const loadRows = async () => {
      if (!selectedDataPoolId) {
        setDataPoolRows([])
        setDataPoolRowsTotal(0)
        setDataPoolRowsNextCursor(null)
        setDataPoolRowsHasMore(false)
        return
      }
      try {
        const response = await api.getDataPool(selectedDataPoolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor: '' })
        if (!cancelled && generation === dataPoolRowsLoadGenerationRef.current) {
          const rows = response.rows || []
          setDataPoolRows(rows)
          setDataPoolRowsTotal(response.total ?? rows.length)
          setDataPoolRowsNextCursor(response.next_cursor || null)
          setDataPoolRowsHasMore(Boolean(response.next_cursor) && (response.has_more ?? true))
        }
      } catch (error: unknown) {
        if (
          !cancelled
          && selectedDataPoolIdRef.current === selectedDataPoolId
          && generation === dataPoolRowsLoadGenerationRef.current
        ) {
          setDataPoolActionError(getErrorMessage(error, 'Unable to load data pool rows'))
        }
      } finally {
        if (!cancelled && generation === dataPoolRowsLoadGenerationRef.current) {
          setDataPoolRowsLoadBusy(false)
        }
      }
    }
    loadRows()
    return () => {
      cancelled = true
    }
  }, [api, selectedDataPoolId])

  const updateGeneral = (key: keyof DashboardSystemSettings['general'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      general: {
        ...prev.general,
        [key]: key === 'api_endpoint' ? value : Math.max(0, Number(value) || 0),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateClientApp = (availability: DashboardSystemSettings['client_app']['availability']) => {
    setSettings((prev) => ({
      ...prev,
      client_app: {
        ...prev.client_app,
        availability,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateClientDownloads = (enabled: boolean) => {
    setSettings((prev) => ({
      ...prev,
      client_downloads: {
        ...prev.client_downloads,
        enabled,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateThreshold = (key: keyof DashboardSystemSettings['thresholds'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      thresholds: {
        ...prev.thresholds,
        [key]: parseOptionalNumber(value),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  // Capacity number inputs are draft-backed: the raw text is always recorded so
  // the input shows what the admin typed, and the settings number only changes
  // when the draft parses. Blank drafts surface as required-field errors via
  // capacityDraftIssues instead of writing 0. Out-of-range numbers are written
  // unclamped so validation reports them rather than silently mutating input.
  const updateCapacityDraftNumber = (field: string, value: string, apply: (parsed: number) => void) => {
    setCapacityFieldDrafts(prev => ({ ...prev, [field]: value }))
    const parsed = parseOptionalNumber(value)
    if (parsed !== undefined) {
      apply(parsed)
    }
    setSaveMessage('')
    setRequestError('')
  }

  const updateCapacityPlanning = (
    key: keyof DashboardSystemSettings['capacity_planning'],
    value: string,
  ) => {
    if (key !== 'headroom_pct') return
    updateCapacityDraftNumber('capacity_planning.headroom_pct', value, parsed => {
      setSettings(prev => ({
        ...prev,
        capacity_planning: { ...prev.capacity_planning, headroom_pct: parsed },
      }))
    })
  }

  const updateGenerator = (key: keyof DashboardSystemSettings['capacity_planning']['generator'], value: string) => {
    updateCapacityDraftNumber(`capacity_planning.generator.${key}`, value, parsed => {
      setSettings(prev => ({
        ...prev,
        capacity_planning: {
          ...prev.capacity_planning,
          generator: { ...prev.capacity_planning.generator, [key]: parsed },
        },
      }))
    })
  }

  const updateAvailability = (key: keyof DashboardSystemSettings['capacity_planning']['availability'], value: string) => {
    if (key === 'source') {
      setSettings(prev => ({
        ...prev,
        capacity_planning: {
          ...prev.capacity_planning,
          availability: { ...prev.capacity_planning.availability, source: value as DashboardSystemSettings['capacity_planning']['availability']['source'] },
        },
      }))
      setSaveMessage('')
      setRequestError('')
      return
    }
    updateCapacityDraftNumber(`capacity_planning.availability.${key}`, value, parsed => {
      setSettings(prev => ({
        ...prev,
        capacity_planning: {
          ...prev.capacity_planning,
          availability: { ...prev.capacity_planning.availability, [key]: parsed },
        },
      }))
    })
  }

  const updateScheduleDefaults = (key: keyof DashboardSystemSettings['schedule_defaults'], value: string) => {
    setSettings((prev) => ({
      ...prev,
      schedule_defaults: {
        ...prev.schedule_defaults,
        [key]: value,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateScriptImports = (allowSourceSecretMigration: boolean) => {
    setSettings(prev => ({
      ...prev,
      script_imports: {
        ...prev.script_imports,
        allow_source_secret_migration: allowSourceSecretMigration,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const addReservation = () => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        reservations: [...(prev.capacity_planning.reservations ?? []), { project_id: '', reserved_generators: 1 }],
      },
    }))
    // Row indexes shift, so per-row draft keys would point at the wrong rows.
    setCapacityFieldDrafts({})
    setSaveMessage('')
    setRequestError('')
  }

  const updateReservation = (index: number, patch: Partial<CapacityReservation>) => {
    setSettings((prev) => {
      const reservations = [...(prev.capacity_planning.reservations ?? [])]
      const existing = reservations[index]
      if (!existing) return prev
      const next: CapacityReservation = { ...existing, ...patch }
      // Empty datetime inputs clear the window bound entirely.
      if (patch.starts_at === '') delete next.starts_at
      if (patch.ends_at === '') delete next.ends_at
      // The "Global envelope" pool option clears the pool scope entirely.
      if (patch.pool_id === '') delete next.pool_id
      reservations[index] = next
      return { ...prev, capacity_planning: { ...prev.capacity_planning, reservations } }
    })
    setSaveMessage('')
    setRequestError('')
  }

  const removeReservation = (index: number) => {
    setSettings((prev) => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        reservations: (prev.capacity_planning.reservations ?? []).filter((_, i) => i !== index),
      },
    }))
    setCapacityFieldDrafts({})
    setSaveMessage('')
    setRequestError('')
  }

  const updateArchiveRetention = (key: keyof DashboardSystemSettings['archive_retention'], value: boolean | string) => {
    if (key === 'enabled') {
      setSettings((prev) => ({
        ...prev,
        archive_retention: { ...prev.archive_retention, enabled: Boolean(value) },
      }))
    } else {
      updateCapacityDraftNumber('archive_retention.default_retention_days', String(value), parsed => {
        setSettings(prev => ({
          ...prev,
          archive_retention: { ...prev.archive_retention, default_retention_days: parsed },
        }))
      })
    }
    setArchiveRetentionActionMessage('')
    setArchiveRetentionActionError('')
    setSaveMessage('')
    setRequestError('')
  }

  const planArchiveRetention = async () => {
    if (!canManageSystem) {
      setArchiveRetentionActionError('Current session cannot manage archive retention.')
      setArchiveRetentionActionMessage('')
      return
    }
    try {
      const plan = await api.planArchiveRetention()
      setArchiveRetentionPlan(plan)
      setArchiveRetentionActionMessage(`Retention plan found ${plan.summary.candidate_count} prune candidate${plan.summary.candidate_count === 1 ? '' : 's'}.`)
      setArchiveRetentionActionError('')
    } catch (error: unknown) {
      setArchiveRetentionActionError(getErrorMessage(error, 'Unable to plan archive retention'))
      setArchiveRetentionActionMessage('')
    }
  }

  const applyArchiveRetention = async () => {
    if (!canManageSystem) {
      setArchiveRetentionActionError('Current session cannot manage archive retention.')
      setArchiveRetentionActionMessage('')
      return
    }
    setConfirmApplyRetention(false)
    try {
      const plan = await api.applyArchiveRetention()
      setArchiveRetentionPlan(plan)
      setArchiveRetentionActionMessage(`Retention apply pruned ${plan.summary.pruned_count} archive${plan.summary.pruned_count === 1 ? '' : 's'}${plan.summary.failed_count ? ` with ${plan.summary.failed_count} failure${plan.summary.failed_count === 1 ? '' : 's'}` : ''}.`)
      setArchiveRetentionActionError('')
    } catch (error: unknown) {
      setArchiveRetentionActionError(getErrorMessage(error, 'Unable to apply archive retention'))
      setArchiveRetentionActionMessage('')
    }
  }

  const refreshUsageAccounting = async () => {
    try {
      const [usageResponse, snapshotResponse] = await Promise.all([
        api.getUsageAccounting(activeProject?.id),
        api.listUsageAccountingSnapshots(activeProject?.id),
      ])
      setUsageAccounting(usageResponse)
      setUsageSnapshots(snapshotResponse.snapshots || [])
      setUsageActionMessage('Usage accounting refreshed.')
      setUsageActionError('')
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to refresh usage accounting'))
      setUsageActionMessage('')
    }
  }

  const captureUsageSnapshot = async () => {
    if (!canManageSystem) {
      setUsageActionError('Current session cannot capture usage snapshots.')
      setUsageActionMessage('')
      return
    }
    let snapshot: UsageAccountingSnapshot
    try {
      snapshot = await api.captureUsageAccountingSnapshot(activeProject?.id)
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to capture usage snapshot'))
      setUsageActionMessage('')
      return
    }

    // The POST response is authoritative. Publish it before reconciliation so
    // a failed list request cannot make a committed capture look retryable.
    setUsageSnapshots(previous => [snapshot, ...previous.filter(candidate => candidate.id !== snapshot.id)])
    setUsageAccounting(snapshot.usage)
    setUsageActionMessage(`Usage snapshot captured for ${snapshot.usage.totals.run_count} run${snapshot.usage.totals.run_count === 1 ? '' : 's'}.`)
    setUsageActionError('')
    try {
      const response = await api.listUsageAccountingSnapshots(activeProject?.id)
      const listed = response.snapshots || []
      setUsageSnapshots(listed.some(candidate => candidate.id === snapshot.id)
        ? listed
        : [snapshot, ...listed])
    } catch {
      setUsageActionError('The usage snapshot was captured, but the snapshot list could not be refreshed. The capture succeeded—do not capture again. Use Refresh for GET-only synchronization.')
    }
  }

  const exportUsageAccounting = async () => {
    setUsageActionMessage('')
    setUsageActionError('')
    try {
      const blob = await api.downloadUsageAccounting(activeProject?.id)
      const date = new Date().toISOString().slice(0, 10)
      const outcome = await downloadBlob(blob, `apexload-usage-accounting-${date}.csv`)
      if (outcome === 'cancelled') return
      setUsageActionMessage('Usage accounting export downloaded.')
      setUsageActionError('')
    } catch (error: unknown) {
      setUsageActionError(getErrorMessage(error, 'Unable to export usage accounting'))
      setUsageActionMessage('')
    }
  }

  const refreshSystemReadiness = async () => {
    try {
      const response = await api.getSystemReadiness()
      setSystemReadiness(response)
      setReadinessActionMessage('Production readiness refreshed.')
      setReadinessActionError('')
    } catch (error: unknown) {
      setReadinessActionError(getErrorMessage(error, 'Unable to refresh production readiness'))
      setReadinessActionMessage('')
    }
  }

  const updateAuth = (key: keyof DashboardSystemSettings['auth'], value: string | string[]) => {
    setSettings((prev) => ({
      ...prev,
      auth: {
        ...prev.auth,
        [key]: value,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const exportAuditEvents = async () => {
    if (!canViewAudit) {
      setAuditActionError('Current session cannot export audit events.')
      setAuditActionMessage('')
      return
    }
    setAuditActionMessage('')
    setAuditActionError('')
    try {
      const blob = await api.downloadAuthAuditEvents({
        limit: 1000,
        action: auditActionFilter.trim() || undefined,
        actor_type: auditActorTypeFilter.trim() || undefined,
        status: auditStatusFilter.trim() || undefined,
      })
      const date = new Date().toISOString().slice(0, 10)
      const outcome = await downloadBlob(blob, `apexload-audit-events-${date}.csv`)
      if (outcome === 'cancelled') return
      setAuditActionMessage('Audit export downloaded.')
      setAuditActionError('')
    } catch (error: unknown) {
      setAuditActionError(getErrorMessage(error, 'Unable to export audit events'))
      setAuditActionMessage('')
    }
  }

  const refreshAuthDiagnostics = useCallback(async () => {
    try {
      const response = await api.getAuthDiagnostics()
      setAuthDiagnostics(response)
    } catch {
      setAuthDiagnostics(null)
    }
  }, [api])

  const saveSettings = useCallback(async (): Promise<boolean> => {
    if (!canSave || settingsMutationBusyRef.current) return false
    settingsMutationBusyRef.current = true
    setSettingsMutationBusy(true)
    try {
      const response = normalizeDashboardSystemSettings(await api.updateSystemSettings(settings))
      setSettings(response)
      setSavedSettings(response)
      setCapacityFieldDrafts({})
      setSaveRecovery('')
      await refreshAuthDiagnostics()
      setSaveMessage('Settings saved.')
      setRequestError('')
      return true
    } catch (error: unknown) {
      const guidance = describeSettingsSaveError(
        error instanceof ApiError ? error.status : undefined,
        getErrorMessage(error, 'Unable to save settings'),
      )
      setRequestError(guidance.message)
      setSaveRecovery(guidance.offerReload ? 'reload' : '')
      if (guidance.focusSection) {
        setActiveSection(guidance.focusSection)
        if (guidance.focusCapacitySubsection) {
          setActiveCapacitySubsection(guidance.focusCapacitySubsection)
        }
      }
      setSaveMessage('')
      return false
    } finally {
      settingsMutationBusyRef.current = false
      setSettingsMutationBusy(false)
    }
  }, [api, canSave, refreshAuthDiagnostics, settings])

  const resetSettings = useCallback(async () => {
    if (!canManageSystem || settingsMutationBusyRef.current) return
    settingsMutationBusyRef.current = true
    setSettingsMutationBusy(true)
    try {
      const response = normalizeDashboardSystemSettings(await api.updateSystemSettings({
        ...DEFAULT_SETTINGS,
        revision: savedSettings.revision,
      }))
      setSettings(response)
      setSavedSettings(response)
      setCapacityFieldDrafts({})
      setSaveRecovery('')
      await refreshAuthDiagnostics()
      setSaveMessage('Settings reset to defaults.')
      setRequestError('')
      setConfirmingResetAll(false)
    } catch (error: unknown) {
      const guidance = describeSettingsSaveError(
        error instanceof ApiError ? error.status : undefined,
        getErrorMessage(error, 'Unable to reset settings'),
      )
      setRequestError(guidance.message)
      setSaveRecovery(guidance.offerReload ? 'reload' : '')
      setSaveMessage('')
      setConfirmingResetAll(false)
    } finally {
      settingsMutationBusyRef.current = false
      setSettingsMutationBusy(false)
    }
  }, [api, canManageSystem, refreshAuthDiagnostics, savedSettings.revision])

  const discardChanges = useCallback(() => {
    if (settingsMutationBusyRef.current) return
    setSettings(savedSettings)
    setCapacityFieldDrafts({})
    setSaveMessage('')
    setRequestError('')
    setSaveRecovery('')
  }, [savedSettings])

  const applyGeneratorPreset = (preset: typeof GENERATOR_PRESETS[number]) => {
    setSettings(prev => ({
      ...prev,
      capacity_planning: {
        ...prev.capacity_planning,
        generator: {
          cpu_cores: preset.cpu_cores,
          memory_mb: preset.memory_mb,
          network_mbps: preset.network_mbps,
          max_vusers: preset.max_vusers,
        },
      },
    }))
    setCapacityFieldDrafts({})
    setSaveMessage('')
    setRequestError('')
  }

  const applyOIDCProviderPreset = (preset: typeof OIDC_PROVIDER_PRESETS[number]) => {
    setSettings(prev => ({
      ...prev,
      auth: {
        ...prev.auth,
        mode: 'oidc',
        provider_name: preset.provider_name,
        scopes: [...preset.scopes],
        roles_claim: preset.roles_claim,
        project_roles_claim: preset.project_roles_claim,
        client_secret_env_var: preset.client_secret_env_var,
        session_secret_env_var: preset.session_secret_env_var,
        logout_url: preset.logout_url,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const applyLiveGeneratorPreset = async () => {
    try {
      const inventory = await api.getSystemInventory()
      const preset = deriveLiveGeneratorPreset(inventory)
      setSettings(prev => applyLiveGeneratorPresetToSettings(prev, preset))
      setCapacityFieldDrafts({})
      setSaveMessage(`Estimated from ${preset.generatorCount} connected load generator pod${preset.generatorCount === 1 ? '' : 's'}: CPU set to ${(preset.cpuCores ?? settings.capacity_planning.generator.cpu_cores).toLocaleString()}, memory set to ${(preset.memoryMB ?? settings.capacity_planning.generator.memory_mb).toLocaleString()} MB, max VUsers set to ${preset.maxVUsers.toLocaleString()}, and configured available generators set to ${preset.configuredAvailableGenerators}. Network remains manual until live pod bandwidth limits are reported.`)
      setRequestError('')
    } catch (error: unknown) {
      setRequestError(getErrorMessage(error, 'Unable to estimate generator capacity from live pods'))
      setSaveMessage('')
    }
  }

  const orderedSectionIds = mergeSettingsOrder(sectionOrder)
  const getSectionOrder = (id: SettingsSectionId) => orderedSectionIds.indexOf(id)
  const sectionClass = 'settings-card'

  const createApplication = async (): Promise<boolean> => {
    const name = newApplicationName.trim()
    if (!name) return true
    const projectId = newApplicationProjectId || activeProject?.id
    try {
      await createAndSelectApplication(name, projectId)
      setNewApplicationName('')
      setNewApplicationProjectId('')
      setApplicationActionError('')
      return true
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to create application'))
      return false
    }
  }

  const applyDomainSelection = (domain: Domain) => {
    setSelectedDomainId(domain.id)
    setDomainDraftName(domain.name)
    setDomainDraftDescription(domain.description || '')
    setDomainActionError('')
    setDomainActionMessage('')
    onDomainChange?.(domain)
  }

  const selectDomain = (domain: Domain) => {
    if (selectedDomain?.id === domain.id) return
    runGuarded(() => applyDomainSelection(domain))
  }

  const createDomain = async () => {
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      return
    }
    const name = newDomainName.trim()
    if (!name) return
    let created: Awaited<ReturnType<typeof repositoryApi.createDomain>>
    try {
      created = await repositoryApi.createDomain({
        name,
        description: newDomainDescription.trim(),
      })
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to create domain'))
      setDomainActionMessage('')
      return
    }

    setNewDomainName('')
    setNewDomainDescription('')
    setSelectedDomainId(created.domain.id)
    setDomainDraftName(created.domain.name)
    setDomainDraftDescription(created.domain.description || '')
    onDomainChange?.(created.domain)
    setDomainActionMessage(`Domain "${created.domain.name}" created.`)
    setDomainActionError('')
    try {
      await onDomainsRefresh?.(created.domain.id, { preserveDomain: created.domain })
      setDomainReconciliationWarning(null)
    } catch {
      setDomainReconciliationWarning({
        message: `Domain "${created.domain.name}" was created, but the domain list could not be refreshed. The create succeeded—do not create it again. Retry synchronization below.`,
        preferredId: created.domain.id,
        preserveDomain: created.domain,
      })
    }
  }

  const saveDomain = async (): Promise<boolean> => {
    if (!selectedDomain) return true
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      return false
    }
    if (domainDraftValidationError) {
      setDomainActionError(domainDraftValidationError)
      setDomainActionMessage('')
      return false
    }
    let updated: Domain
    try {
      updated = await repositoryApi.updateDomain(selectedDomain.id, {
        name: domainDraftName.trim(),
        description: domainDraftDescription.trim(),
      })
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to save domain'))
      setDomainActionMessage('')
      return false
    }

    setSelectedDomainId(updated.id)
    setDomainDraftName(updated.name)
    setDomainDraftDescription(updated.description || '')
    onDomainChange?.(updated)
    setDomainActionMessage(`Domain "${updated.name}" saved.`)
    setDomainActionError('')
    try {
      await onDomainsRefresh?.(updated.id, { preserveDomain: updated })
      setDomainReconciliationWarning(null)
    } catch {
      setDomainReconciliationWarning({
        message: `Domain "${updated.name}" was saved, but the domain list could not be refreshed. The save succeeded—do not save it again. Retry synchronization below.`,
        preferredId: updated.id,
        preserveDomain: updated,
      })
    }
    return true
  }

  const deleteDomain = async (domain: Domain) => {
    if (!canManageDomains) {
      setDomainActionError('Current session cannot manage domains.')
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    if (domain.id === 'default') {
      setDomainActionError('Default domain cannot be deleted.')
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    const projectCount = projectCountByDomain.get(domain.id) || 0
    if (projectCount > 0) {
      setDomainActionError(`Domain "${domain.name}" still has ${projectCount} project${projectCount === 1 ? '' : 's'}. Move or delete those projects before deleting the domain.`)
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }
    try {
      await repositoryApi.deleteDomain(domain.id)
    } catch (error: unknown) {
      setDomainActionError(getErrorMessage(error, 'Unable to delete domain'))
      setDomainActionMessage('')
      setConfirmDeleteDomain(null)
      return
    }

    const fallbackDomain = domains.find(candidate => candidate.id !== domain.id) || null
    if (fallbackDomain) {
      setSelectedDomainId(fallbackDomain.id)
      onDomainChange?.(fallbackDomain)
    } else {
      setSelectedDomainId('')
    }
    setDomainActionMessage(`Domain "${domain.name}" deleted.`)
    setDomainActionError('')
    setConfirmDeleteDomain(null)
    try {
      await onDomainsRefresh?.(fallbackDomain?.id, { removeDomainId: domain.id })
      setDomainReconciliationWarning(null)
    } catch {
      setDomainReconciliationWarning({
        message: `Domain "${domain.name}" was deleted, but the domain list could not be refreshed. The delete succeeded—do not delete it again. Retry synchronization below.`,
        preferredId: fallbackDomain?.id,
        removeDomainId: domain.id,
      })
    }
  }

  const retryDomainReconciliation = async () => {
    if (!domainReconciliationWarning) return
    try {
      await onDomainsRefresh?.(domainReconciliationWarning.preferredId, {
        preserveDomain: domainReconciliationWarning.preserveDomain,
        removeDomainId: domainReconciliationWarning.removeDomainId,
      })
      setDomainReconciliationWarning(null)
    } catch {
      // Keep the warning visible: this retry reconciles the catalog and must
      // never replay the already-committed mutation.
    }
  }

  const applyProjectSelection = (project: Project | null) => {
    setMissingRequestedProjectId('')
    setProjectSecretCount(0)
    setSelectedProjectId(project?.id || '')
    setProjectDraftName(project?.name || '')
    setProjectDraftRules(toDraftRedactionRules(project?.settings?.redaction_rules))
    setProjectDraftMaxConcurrentTests(project?.settings?.max_concurrent_tests !== undefined ? String(project.settings.max_concurrent_tests) : '')
    setProjectDraftMaxVUsers(project?.settings?.max_vusers !== undefined ? String(project.settings.max_vusers) : '')
    setProjectDraftMaxVUsersPerTest(project?.settings?.max_vusers_per_test !== undefined ? String(project.settings.max_vusers_per_test) : '')
    setProjectDraftMaxTestDurationMinutes(project?.settings?.max_test_duration_minutes !== undefined ? String(project.settings.max_test_duration_minutes) : '')
    loadProjectGitHubDraft(project?.settings?.github)
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const selectProject = (project: Project | null) => {
    if (selectedProject?.id === project?.id) return
    runGuarded(() => applyProjectSelection(project))
  }

  const createProject = async (): Promise<boolean> => {
    if (!canManageProjects) {
      setProjectActionError('Current session cannot manage projects.')
      setProjectActionMessage('')
      return false
    }
    const name = newProjectName.trim()
    if (!name) return true
    const domainId = newProjectDomainId || selectedDomain?.id
    try {
      const created = await createAndSelectProject({ name, domain_id: domainId })
      commitSettingsProject(created)
      setNewProjectName('')
      setNewProjectDomainId('')
      applyProjectSelection(created)
      setProjectActionMessage(`Project "${created.name}" created.`)
      return true
    } catch (error: unknown) {
      setProjectActionError(getErrorMessage(error, 'Unable to create project'))
      setProjectActionMessage('')
      return false
    }
  }

  const updateProjectRule = (index: number, key: keyof DraftRedactionRule, value: string) => {
    setProjectDraftRules(prev => prev.map((rule, ruleIndex) => (ruleIndex === index ? { ...rule, [key]: value } : rule)))
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const addProjectRule = () => {
    setProjectDraftRules(prev => [...prev, { name: '', pattern: '', replacement: '' }])
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const removeProjectRule = (index: number) => {
    setProjectDraftRules(prev => prev.filter((_, ruleIndex) => ruleIndex !== index))
    setProjectActionError('')
    setProjectActionMessage('')
  }

	const saveProjectCompliance = async (): Promise<Project | null> => {
		if (!selectedProject) return null
    if (!canManageProjects) {
      setProjectActionError('Current session cannot manage this project.')
      setProjectActionMessage('')
			return null
    }
    if (projectDraftValidationError) {
      setProjectActionError(projectDraftValidationError)
      setProjectActionMessage('')
			return null
    }

    try {
      const updated = await updateProject(selectedProject.id, {
        name: projectDraftName.trim(),
        settings: buildProjectSettingsDraft(projectDraftRules, projectDraftMaxConcurrentTests, projectDraftMaxVUsers, projectDraftMaxVUsersPerTest, projectDraftMaxTestDurationMinutes, buildProjectGitHubDraft(), selectedProject?.settings?.pool_quotas),
      })
      commitSettingsProject(updated)
      applyProjectSelection(updated)
      setProjectActionMessage(`Saved project governance defaults for ${updated.name}.`)
			return updated
    } catch (error: unknown) {
      setProjectActionError(getErrorMessage(error, 'Unable to save project governance defaults'))
      setProjectActionMessage('')
			return null
    }
  }

  const discardDomainChanges = () => {
    setDomainDraftName(selectedDomain?.name || '')
    setDomainDraftDescription(selectedDomain?.description || '')
    setDomainActionError('')
    setDomainActionMessage('')
  }

  const discardProjectChanges = () => {
    setProjectDraftName(selectedProject?.name || '')
    setProjectDraftRules(toDraftRedactionRules(selectedProject?.settings?.redaction_rules))
    setProjectDraftMaxConcurrentTests(selectedProject?.settings?.max_concurrent_tests !== undefined ? String(selectedProject.settings.max_concurrent_tests) : '')
    setProjectDraftMaxVUsers(selectedProject?.settings?.max_vusers !== undefined ? String(selectedProject.settings.max_vusers) : '')
    setProjectDraftMaxVUsersPerTest(selectedProject?.settings?.max_vusers_per_test !== undefined ? String(selectedProject.settings.max_vusers_per_test) : '')
    setProjectDraftMaxTestDurationMinutes(selectedProject?.settings?.max_test_duration_minutes !== undefined ? String(selectedProject.settings.max_test_duration_minutes) : '')
    loadProjectGitHubDraft(selectedProject?.settings?.github)
    setProjectActionError('')
    setProjectActionMessage('')
  }

  const applicationCreateDraftDirty = Boolean(newApplicationName.trim())
  const applicationRenameDraftDirty = renamingApplicationId !== null
    && renamingApplicationName !== null
    && renamingApplicationOriginalName !== null
    && renamingApplicationName.trim() !== renamingApplicationOriginalName
  const releaseRenameDraftDirty = renamingReleaseId !== null
    && renamingReleaseApplicationId !== null
    && renamingReleaseOriginalName !== null
    && renamingReleaseName.trim() !== renamingReleaseOriginalName
  const projectCreateDraftDirty = Boolean(newProjectName.trim())
  const dataPoolCreateDraftDirty = Boolean(dataPoolName.trim() || dataPoolDescription.trim())
  const hasAnyUnsavedChanges = hasUnsavedChanges
    || domainHasUnsavedChanges
    || projectHasUnsavedChanges
    || applicationCreateDraftDirty
    || applicationRenameDraftDirty
    || releaseRenameDraftDirty
    || projectCreateDraftDirty
    || dataPoolCreateDraftDirty
    || poolQuotaDraftDirty
  const canSaveAllSettingsChanges = hasAnyUnsavedChanges
    && (!hasUnsavedChanges || canSave)
    && (!domainHasUnsavedChanges || (canManageDomains && !domainDraftValidationError))
    && (!projectHasUnsavedChanges || (canManageProjects && !projectDraftValidationError))
    && (!applicationRenameDraftDirty || Boolean(renamingApplicationName?.trim()))
    && (!releaseRenameDraftDirty || Boolean(renamingReleaseName.trim()))
    && (!projectCreateDraftDirty || canManageProjects)
    && (!dataPoolCreateDraftDirty || Boolean(dataPoolName.trim()))
    && (!poolQuotaDraftDirty || canManageProjects)
  const saveAllSettingsChanges = async (): Promise<boolean> => {
    if (hasUnsavedChanges && !(await saveSettings())) return false
    if (domainHasUnsavedChanges && !(await saveDomain())) return false
		let authoritativeProject: Project | undefined
		if (projectHasUnsavedChanges) {
			const savedProject = await saveProjectCompliance()
			if (!savedProject) return false
			authoritativeProject = savedProject
		}
		if (poolQuotaDraftDirty && !(await poolQuotaDraftRef.current?.save(authoritativeProject))) return false
    if (applicationRenameDraftDirty && !(await confirmRenameApplication())) return false
    if (releaseRenameDraftDirty && !(await confirmRenameRelease())) return false
    if (applicationCreateDraftDirty && !(await createApplication())) return false
    if (dataPoolCreateDraftDirty && !(await createDataPool())) return false
    // Creating a project changes the global active-project scope, so commit it
    // after drafts bound to the previous project have been saved.
    if (projectCreateDraftDirty && !(await createProject())) return false
    return true
  }
  const discardAllSettingsChanges = () => {
    discardChanges()
    discardDomainChanges()
    discardProjectChanges()
    poolQuotaDraftRef.current?.discard()
    setNewApplicationName('')
    setNewApplicationProjectId('')
    setRenamingApplicationId(null)
    setRenamingApplicationName(null)
    setRenamingApplicationOriginalName(null)
    setRenamingReleaseId(null)
    setRenamingReleaseName('')
    setRenamingReleaseOriginalName(null)
    setRenamingReleaseApplicationId(null)
    setNewProjectName('')
    setNewProjectDomainId('')
    setDataPoolName('')
    setDataPoolDescription('')
    setDataPoolProjectId('')
  }
  const hasAnyUnsavedChangesRef = useRef(false)
  hasAnyUnsavedChangesRef.current = hasAnyUnsavedChanges
  const saveAllSettingsChangesRef = useRef<() => Promise<boolean>>(async () => false)
  saveAllSettingsChangesRef.current = saveAllSettingsChanges
  const discardAllSettingsChangesRef = useRef<() => void>(() => {})
  discardAllSettingsChangesRef.current = discardAllSettingsChanges
  const settingsNavigationGuard = useMemo<NavigationGuard>(() => ({
    id: 'system-settings',
    label: 'Settings',
    isDirty: () => hasAnyUnsavedChangesRef.current,
    save: () => saveAllSettingsChangesRef.current(),
    discard: () => discardAllSettingsChangesRef.current(),
  }), [])
  useNavigationGuard(settingsNavigationGuard, hasAnyUnsavedChanges)

  const selectApplication = (application: Application) => {
    if (activeApplication?.id === application.id) return
    runGuarded(() => setActiveApplication(application))
  }

  const selectRelease = (release: Release) => {
    if (release.archived_at || activeRelease?.id === release.id) return
    runGuarded(() => setActiveRelease(release))
  }

  const startRenamingApplication = () => {
    if (!activeApplication) return
    setRenamingApplicationId(activeApplication.id)
    setRenamingApplicationName(activeApplication.name)
    setRenamingApplicationOriginalName(activeApplication.name)
  }

  async function confirmRenameApplication(): Promise<boolean> {
    if (!renamingApplicationId || renamingApplicationName === null) return false
    const name = renamingApplicationName.trim()
    if (!name) return false
    let updated: Application
    try {
      updated = await applicationApi.updateApplication(renamingApplicationId, { name })
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to rename application'))
      return false
    }

    // The mutation has committed. Reflect that authoritative response and exit
    // edit mode before the best-effort list reconciliation so a failed GET is
    // never presented as a failed (and therefore tempting to repeat) rename.
    setActiveApplication(updated)
    setApplicationActionError('')
    setRenamingApplicationId(null)
    setRenamingApplicationName(null)
    setRenamingApplicationOriginalName(null)
    try {
      await refreshApplications({ preserveApplication: updated })
      setApplicationReconciliationWarning(null)
    } catch {
      setApplicationReconciliationWarning({
        message: `Application "${updated.name}" was renamed, but the application list could not be refreshed. The rename succeeded—do not rename again. Retry synchronization below.`,
        scope: 'applications',
      })
    }
    return true
  }

  const reconcileManagedReleaseCatalog = async (applicationId: string, preserveRelease?: Release) => {
    await refreshReleases(applicationId, preserveRelease ? { preserveRelease } : undefined)
    const response = await applicationApi.listReleases(applicationId, true)
    if (activeApplicationIdRef.current !== applicationId) return
    setManagedReleases(reconcileManagedReleaseSnapshots(
      response.releases || [],
      applicationId,
      committedManagedReleasesRef.current,
      managedReleaseTombstonesRef.current,
    ))
  }

  const retryApplicationReconciliation = async () => {
    try {
      if (applicationReconciliationWarning?.scope === 'releases' && applicationReconciliationWarning.applicationId) {
        await reconcileManagedReleaseCatalog(applicationReconciliationWarning.applicationId)
      } else {
        await refreshApplications()
      }
      setApplicationReconciliationWarning(null)
    } catch {
      // ApplicationProvider retains the last valid state and exposes the load
      // error; keep the committed-mutation warning until a retry succeeds.
    }
  }

  const deleteApplication = async () => {
    if (!activeApplication) return
    const deleted = activeApplication
    try {
      await applicationApi.deleteApplication(deleted.id)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to delete application'))
      setConfirmDeleteApp(false)
      return
    }
    removeApplication(deleted.id)
    setApplicationActionError('')
    setConfirmDeleteApp(false)
    try {
      await refreshApplications()
      setApplicationReconciliationWarning(null)
    } catch {
      setApplicationReconciliationWarning({
        message: `Application "${deleted.name}" was deleted, but the application list could not be refreshed. The delete succeeded—do not delete again. Retry synchronization below.`,
        scope: 'applications',
      })
    }
  }

  const startRenamingRelease = (releaseId: string, currentName: string, applicationId: string) => {
    setRenamingReleaseId(releaseId)
    setRenamingReleaseName(currentName)
    setRenamingReleaseOriginalName(currentName)
    setRenamingReleaseApplicationId(applicationId)
  }

  async function confirmRenameRelease(): Promise<boolean> {
    if (!renamingReleaseId || !renamingReleaseApplicationId) return false
    const name = renamingReleaseName.trim()
    if (!name) return false
    const applicationId = renamingReleaseApplicationId
    let updated: Release
    try {
      updated = await applicationApi.updateRelease(renamingReleaseId, { name })
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to rename release'))
      return false
    }
    const releaseApplicationId = updated.application_id || applicationId
    committedManagedReleasesRef.current.set(updated.id, updated)
    if (activeApplicationIdRef.current === releaseApplicationId) {
      setManagedReleases(prev => prev.some(release => release.id === updated.id)
        ? prev.map(release => release.id === updated.id ? updated : release)
        : [updated, ...prev])
    }
    if (!updated.archived_at && activeRelease?.id === updated.id) setActiveRelease(updated)
    setApplicationActionError('')
    setRenamingReleaseId(null)
    setRenamingReleaseName('')
    setRenamingReleaseOriginalName(null)
    setRenamingReleaseApplicationId(null)
    try {
      await reconcileManagedReleaseCatalog(releaseApplicationId, updated)
      setApplicationReconciliationWarning(null)
    } catch {
      setApplicationReconciliationWarning({
        message: `Release "${updated.name}" was renamed, but the release list could not be refreshed. The rename succeeded—do not rename again. Retry synchronization below.`,
        scope: 'releases',
        applicationId: releaseApplicationId,
      })
    }
    return true
  }

  const archiveRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before archiving the release.`)
      setConfirmArchiveRelease(null)
      return
    }
    const applicationId = activeApplication?.id || ''
    let archived: Release
    try {
      archived = await applicationApi.archiveRelease(releaseId)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to archive release'))
      setConfirmArchiveRelease(null)
      return
    }
    const releaseApplicationId = archived.application_id || applicationId
    committedManagedReleasesRef.current.set(archived.id, archived)
    setManagedReleases(prev => prev.some(release => release.id === archived.id)
      ? prev.map(release => release.id === archived.id ? archived : release)
      : [archived, ...prev])
    // Shared release scope excludes archived rows. Apply that committed
    // removal before the list GET so a failed reconciliation cannot leave the
    // archived release selected elsewhere in the dashboard.
    removeRelease(archived.id)
    setApplicationActionError('')
    setConfirmArchiveRelease(null)
    try {
      await reconcileManagedReleaseCatalog(releaseApplicationId)
      setApplicationReconciliationWarning(null)
    } catch {
      setApplicationReconciliationWarning({
        message: `Release "${releaseName}" was archived, but the release list could not be refreshed. The archive succeeded—do not archive again. Retry synchronization below.`,
        scope: 'releases',
        applicationId: releaseApplicationId,
      })
    }
  }

  const deleteRelease = async (releaseId: string, releaseName: string) => {
    const results = await api.listTests({ application_id: activeApplication?.id, release_id: releaseId })
    if ((results.tests || []).length > 0) {
      setApplicationActionError(`Release "${releaseName}" still has associated results. Archive or delete those results before deleting the release.`)
      setConfirmDeleteRelease(null)
      return
    }
    const applicationId = activeApplication?.id || ''
    try {
      await applicationApi.deleteRelease(releaseId)
    } catch (error: unknown) {
      setApplicationActionError(getErrorMessage(error, 'Unable to delete release'))
      setConfirmDeleteRelease(null)
      return
    }
    managedReleaseTombstonesRef.current.add(releaseId)
    committedManagedReleasesRef.current.delete(releaseId)
    setManagedReleases(prev => prev.filter(release => release.id !== releaseId))
    removeRelease(releaseId)
    setApplicationActionError('')
    setConfirmDeleteRelease(null)
    try {
      await reconcileManagedReleaseCatalog(applicationId)
      setApplicationReconciliationWarning(null)
    } catch {
      setApplicationReconciliationWarning({
        message: `Release "${releaseName}" was deleted, but the release list could not be refreshed. The delete succeeded—do not delete again. Retry synchronization below.`,
        scope: 'releases',
        applicationId,
      })
    }
  }

  const createDataPool = async (): Promise<boolean> => {
    const name = dataPoolName.trim()
    if (!name) return false
    if (dataPoolCatalogMutationBusyRef.current) return false
    dataPoolCatalogMutationBusyRef.current = true
    setDataPoolCatalogMutationBusy(true)
    const projectId = dataPoolProjectId || activeProject?.id
    setDataPoolActionNotice('')
    try {
      const pool = await api.createDataPool({ name, description: dataPoolDescription.trim(), project_id: projectId })
      // Commit is complete. Clear the bound draft and publish the authoritative
      // row before attempting the best-effort list reconciliation.
      setDataPoolName('')
      setDataPoolDescription('')
      setDataPoolProjectId('')
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPools(previous => [pool, ...previous.filter(candidate => candidate.id !== pool.id)])
        selectedDataPoolIdRef.current = pool.id
        setSelectedDataPoolId(pool.id)
      }
      const reloaded = await loadDataPools(pool.id)
      if (activeDataPoolProjectIdRef.current !== projectId) return true
      setDataPoolActionError('')
      setDataPoolActionNotice(reloaded
        ? `Created data pool "${pool.name}".`
        : `Data pool "${pool.name}" was created, but the list could not be reloaded. The create succeeded—do not retry. Refresh the list to verify it.`)
      return true
    } catch (error: unknown) {
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPoolActionError(getErrorMessage(error, 'Unable to create data pool'))
      }
      return false
    } finally {
      dataPoolCatalogMutationBusyRef.current = false
      setDataPoolCatalogMutationBusy(false)
    }
  }

  const deleteDataPool = async (pool: SharedDataPool) => {
    if (dataPoolCatalogMutationBusyRef.current) return
    dataPoolCatalogMutationBusyRef.current = true
    setDataPoolCatalogMutationBusy(true)
    const projectId = activeProject?.id
    setConfirmDeletePool(null)
    setDataPoolActionNotice('')
    try {
      await api.deleteDataPool(pool.id)
      const reloaded = await loadDataPools('')
      if (activeDataPoolProjectIdRef.current !== projectId) return
      setDataPoolActionError('')
      setDataPoolActionNotice(reloaded
        ? `Deleted data pool "${pool.name}".`
        : `Data pool "${pool.name}" was deleted, but the list could not be reloaded. The delete succeeded—do not retry. Refresh the list to verify it.`)
    } catch (error: unknown) {
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPoolActionError(getErrorMessage(error, 'Unable to delete data pool'))
      }
    } finally {
      dataPoolCatalogMutationBusyRef.current = false
      setDataPoolCatalogMutationBusy(false)
    }
  }

  const saveDataPoolRow = async () => {
    const poolId = selectedDataPoolId
    const rowId = editingDataPoolRowId
    const rowVersion = editingDataPoolRowVersion
    const updating = Boolean(rowId)
    if (!poolId) return
    if (rowId && (!rowVersion || rowVersion <= 0)) {
      setDataPoolActionError('This row is missing its edit version. Refresh the list and reopen the row before saving.')
      return
    }
    let parsed: Record<string, string>
    try {
      parsed = JSON.parse(dataPoolRowJson)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        throw new Error('Row data must be a JSON object.')
      }
    } catch (error: unknown) {
      setDataPoolActionError(getErrorMessage(error, 'Invalid row JSON'))
      return
    }
    setDataPoolRowSaveBusy(true)
    setDataPoolActionNotice('')
    try {
      try {
        if (rowId) {
          await api.updateDataPoolRow(poolId, rowId, { data: parsed, version: rowVersion as number })
        } else {
          await api.createDataPoolRow(poolId, { data: parsed })
        }
      } catch (error: unknown) {
        const conflict = error instanceof ApiError && error.status === 409
        const deleted = error instanceof ApiError && error.status === 404 && Boolean(rowId)
        if (conflict || deleted) {
          try {
            await refreshDataPoolRows(poolId)
          } catch {
            // Keep the mutation conflict as the primary outcome when its
            // best-effort reconciliation read also fails.
          }
          // Close the editor: a blind retry against the refreshed list would
          // otherwise pick up the new version and clobber the other writer
          // (409), or keep 404ing against a row that no longer exists.
          if (selectedDataPoolIdRef.current === poolId) {
            setEditingDataPoolRowId(null)
            setEditingDataPoolRowVersion(null)
            setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}')
            setDataPoolActionError(conflict
              ? 'This row changed while you were editing. Reload the row list, then reopen the row to apply your change to the latest version.'
              : 'This row was deleted while you were editing. Reload the row list before continuing.')
          }
          return
        }
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError(getErrorMessage(error, 'Unable to save data row'))
        }
        return
      }

      // The mutation is authoritative at this point. A follow-up read is only
      // reconciliation and must never turn a successful create/update into an
      // error that invites a duplicate retry.
      if (selectedDataPoolIdRef.current === poolId) {
        setEditingDataPoolRowId(null)
        setEditingDataPoolRowVersion(null)
        setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}')
      }
      try {
        const refreshed = await refreshDataPoolRows(poolId)
        if (!refreshed) return
      } catch {
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError('')
          setDataPoolActionNotice(updating
            ? 'Row updated, but the row list could not be reloaded. The save succeeded—do not retry the update. Refresh the list to verify it.'
            : 'Row added, but the row list could not be reloaded. The save succeeded—do not add it again. Refresh the list to verify it.')
        }
        return
      }
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice(updating ? 'Row updated.' : 'Row added.')
      }
    } finally {
      setDataPoolRowSaveBusy(false)
    }
  }

  const editDataPoolRow = (row: SharedDataPoolRow) => {
    setEditingDataPoolRowId(row.id)
    setEditingDataPoolRowVersion(row.version)
    setDataPoolRowJson(JSON.stringify(row.data || {}, null, 2))
  }

  const refreshDataPoolRows = async (poolId: string): Promise<boolean> => {
    // A late mutation from a pool the user has left must not invalidate the
    // active pool's generation or take ownership of its busy state.
    if (selectedDataPoolIdRef.current !== poolId) return false
    const generation = ++dataPoolRowsLoadGenerationRef.current
    setDataPoolRowsLoadBusy(true)
    try {
      const details = await api.getDataPool(poolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor: '' })
      if (selectedDataPoolIdRef.current !== poolId || generation !== dataPoolRowsLoadGenerationRef.current) return false
      const rows = details.rows || []
      setDataPoolRows(rows)
      setDataPoolRowsTotal(details.total ?? rows.length)
      setDataPoolRowsNextCursor(details.next_cursor || null)
      setDataPoolRowsHasMore(Boolean(details.next_cursor) && (details.has_more ?? true))
      return true
    } finally {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        setDataPoolRowsLoadBusy(false)
      }
    }
  }

  const loadMoreDataPoolRows = async () => {
    const poolId = selectedDataPoolId
    if (!poolId || dataPoolRowsLoadBusy || !dataPoolRowsHasMore || !dataPoolRowsNextCursor) return
    const generation = dataPoolRowsLoadGenerationRef.current
    const cursor = dataPoolRowsNextCursor
    setDataPoolRowsLoadBusy(true)
    try {
      const details = await api.getDataPool(poolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor })
      if (selectedDataPoolIdRef.current !== poolId || generation !== dataPoolRowsLoadGenerationRef.current) return
      const rows = details.rows || []
      setDataPoolRows(current => {
        const seen = new Set(current.map(row => row.id))
        return [...current, ...rows.filter(row => !seen.has(row.id))]
      })
      setDataPoolRowsTotal(details.total ?? dataPoolRowsTotal)
      setDataPoolRowsNextCursor(details.next_cursor || null)
      setDataPoolRowsHasMore(Boolean(details.next_cursor) && (details.has_more ?? true))
      setDataPoolActionError('')
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        if (error instanceof ApiError && error.status === 409) {
          try {
            await refreshDataPoolRows(poolId)
            if (selectedDataPoolIdRef.current === poolId) {
              setDataPoolActionError('')
              setDataPoolActionNotice('Data pool rows changed while the next page was loading. The list was refreshed from the beginning.')
            }
          } catch {
            if (selectedDataPoolIdRef.current === poolId) {
              setDataPoolActionError('Data pool rows changed while the next page was loading, and the automatic refresh failed. Refresh the list before continuing.')
            }
          }
          return
        }
        setDataPoolActionError(getErrorMessage(error, 'Unable to load more data pool rows'))
      }
    } finally {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        setDataPoolRowsLoadBusy(false)
      }
    }
  }

  const beginDataPoolImport = async (file: File) => {
    const poolId = selectedDataPoolId
    if (!poolId) return
    setDataPoolImportBusy(true)
    setDataPoolActionNotice('')
    try {
      const preview = await api.importDataPoolRows(poolId, file, { dryRun: true })
      if (selectedDataPoolIdRef.current !== poolId) return
      setDataPoolImportDraft({ poolId, file, preview })
      // append_warning means an append commit is known to be rejected. Start
      // on the safe replacement path while keeping both choices visible.
      setDataPoolImportMode(preview.mode === 'replace' || preview.append_warning ? 'replace' : 'append')
      setDataPoolActionError('')
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolImportDraft(null)
        setDataPoolActionError(getErrorMessage(error, 'Unable to read the uploaded file'))
      }
    } finally {
      setDataPoolImportBusy(false)
    }
  }

  const cancelDataPoolImport = () => {
    setDataPoolImportDraft(null)
    setDataPoolImportMode('append')
  }

  const confirmDataPoolImport = async () => {
    const draft = dataPoolImportDraft
    if (!draft) return
    const mode = dataPoolImportMode
    const poolName = dataPools.find(candidate => candidate.id === draft.poolId)?.name || draft.poolId
    setDataPoolImportBusy(true)
    try {
      let result: DataPoolImportResult
      try {
        result = await api.importDataPoolRows(draft.poolId, draft.file, { mode })
      } catch (error: unknown) {
        if (selectedDataPoolIdRef.current === draft.poolId) {
          setDataPoolActionError(getErrorMessage(error, `Unable to import rows into "${poolName}"`))
        }
        return
      }
      if (selectedDataPoolIdRef.current === draft.poolId) {
        setDataPoolImportDraft(null)
        setDataPoolImportMode('append')
        setEditingDataPoolRowId(null)
        setEditingDataPoolRowVersion(null)
        setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}')
      }
      try {
        const refreshed = await refreshDataPoolRows(draft.poolId)
        if (!refreshed) return
      } catch {
        if (selectedDataPoolIdRef.current === draft.poolId) {
          setDataPoolActionError('')
          setDataPoolActionNotice(`Imported ${result.imported ?? 0} row(s) in ${mode} mode, but the row list could not be reloaded. The import succeeded—do not import the file again. Refresh the list to verify it.`)
        }
        return
      }
      // Only speak for the pool the user is still looking at; the import
      // itself is done either way.
      if (selectedDataPoolIdRef.current === draft.poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice(`Imported ${result.imported ?? 0} row(s) in ${mode} mode; the pool now holds ${result.total ?? 0} row(s).`)
      }
    } finally {
      setDataPoolImportBusy(false)
    }
  }

  const exportDataPool = async () => {
    const poolId = selectedDataPoolId
    if (!poolId) return
    const pool = dataPools.find(candidate => candidate.id === poolId)
    setDataPoolExportBusy(true)
    try {
      const { blob, filename } = await api.exportDataPoolRows(poolId)
      const safeName = (pool?.name || 'data-pool').trim().replace(/[^A-Za-z0-9_-]+/g, '-') || 'data-pool'
      const downloadName = filename || `${safeName}.apexload.csv`
      const outcome = await downloadBlob(blob, downloadName)
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        if (outcome !== 'cancelled') {
          setDataPoolActionNotice(`Exported ${downloadName}.`)
        }
      }
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError(getErrorMessage(error, `Unable to export data pool "${pool?.name || poolId}"`))
      }
    } finally {
      setDataPoolExportBusy(false)
    }
  }

  const addNotificationWebhook = () => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: [
          ...current.notifications.webhooks,
          {
            name: '',
            enabled: true,
            url: '',
            format: 'slack',
            events: [],
            api_token: '',
            entity_selector: '',
          },
        ],
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateNotificationWebhook = (index: number, updates: Partial<DashboardSystemSettings['notifications']['webhooks'][number]>) => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: current.notifications.webhooks.map((webhook, webhookIndex) => (
          webhookIndex === index ? { ...webhook, ...updates } : webhook
        )),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const removeNotificationWebhook = (index: number) => {
    setSettings(current => ({
      ...current,
      notifications: {
        ...current.notifications,
        webhooks: current.notifications.webhooks.filter((_, webhookIndex) => webhookIndex !== index),
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updatePrometheus = (updates: Partial<DashboardSystemSettings['prometheus']>) => {
    setSettings(current => ({
      ...current,
      prometheus: {
        ...current.prometheus,
        ...updates,
      },
    }))
    setSaveMessage('')
    setRequestError('')
  }

  const updateGrafana = (updates: Partial<NonNullable<DashboardSystemSettings['grafana']>>) => {
    setSettings(current => ({ ...current, grafana: { ...DEFAULT_SETTINGS.grafana!, ...current.grafana, ...updates } }))
    setSaveMessage('')
    setRequestError('')
  }

  const deleteDataPoolRow = async (row: SharedDataPoolRow) => {
    const poolId = row.pool_id
    if (!poolId || dataPoolRowDeleteBusyRef.current) return
    dataPoolRowDeleteBusyRef.current = true
    setDataPoolRowDeleteBusy(true)
    setConfirmDeleteRow(null)
    setDataPoolActionNotice('')
    try {
      try {
        await api.deleteDataPoolRow(poolId, row.id)
      } catch (error: unknown) {
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError(getErrorMessage(error, 'Unable to delete data row'))
        }
        return
      }
      try {
        const refreshed = await refreshDataPoolRows(poolId)
        if (!refreshed) return
      } catch {
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError('')
          setDataPoolActionNotice('Row deleted, but the row list could not be reloaded. The delete succeeded—do not retry. Refresh the list to verify it.')
        }
        return
      }
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice('Row deleted.')
      }
    } finally {
      dataPoolRowDeleteBusyRef.current = false
      setDataPoolRowDeleteBusy(false)
    }
  }


  const renderSettingsSubnav = <SubsectionId extends string,>(
    ariaLabel: string,
    idPrefix: string,
    subsections: readonly SettingsSubsectionDefinition<SubsectionId>[],
    activeSubsection: SubsectionId,
    setActiveSubsection: (id: SubsectionId) => void,
    summaries: Record<SubsectionId, string>,
    badges?: Partial<Record<SubsectionId, number>>,
  ) => (
    <div className="settings-subnav" role="tablist" aria-label={ariaLabel}>
      {subsections.map(subsection => (
        <button
          key={subsection.id}
          id={`${idPrefix}-tab-${subsection.id}`}
          type="button"
          role="tab"
          aria-selected={activeSubsection === subsection.id}
          aria-controls={`${idPrefix}-panel-${subsection.id}`}
          className={`settings-subnav-item ${activeSubsection === subsection.id ? 'active' : ''}`}
          onClick={() => setActiveSubsection(subsection.id)}
        >
          <span className="settings-subnav-label">{subsection.title}</span>
          {badges?.[subsection.id] ? (
            <span className="repository-status-pill danger settings-subnav-badge" aria-label={`${badges[subsection.id]} validation issue${badges[subsection.id] === 1 ? '' : 's'}`}>
              {badges[subsection.id]}
            </span>
          ) : null}
          <span className="settings-subnav-meta">{summaries[subsection.id]}</span>
        </button>
      ))}
    </div>
  )

  const sectionMessages = useMemo(() => {
    const msgs: Record<SettingsSectionId, { notices: string[]; errors: string[] }> = {
      general: { notices: [], errors: [] },
      thresholds: { notices: [], errors: [] },
      capacity: { notices: [], errors: [] },
      domains: { notices: [], errors: [] },
      applications: { notices: [], errors: [] },
      projects: { notices: [], errors: [] },
      'data-pools': { notices: [], errors: [] },
      integrations: { notices: [], errors: [] },
    }
    if (saveMessage) msgs[activeSection].notices.push(saveMessage)
    if (requestError) msgs[activeSection].errors.push(requestError)
    if (domainActionMessage) msgs.domains.notices.push(domainActionMessage)
    if (domainActionError) msgs.domains.errors.push(domainActionError)
    if (applicationActionError) msgs.applications.errors.push(applicationActionError)
    if (projectActionMessage) msgs.projects.notices.push(projectActionMessage)
    if (projectActionError) msgs.projects.errors.push(projectActionError)
    if (dataPoolActionError) msgs['data-pools'].errors.push(dataPoolActionError)
    return msgs
  }, [saveMessage, requestError, domainActionMessage, domainActionError, applicationActionError, projectActionMessage, projectActionError, dataPoolActionError, activeSection])

  const activeSectionMessages = sectionMessages[activeSection]
  const settingsTopbarContribution = useMemo(() => ({
    id: 'settings-page',
    title: 'Settings',
    controls: (
      <PageViewToggle
        value={activeSection}
        ariaLabel="Settings sections"
        className="settings-section-toggle settings-topbar-section-toggle new-test-view-toggle"
        options={SETTINGS_SECTION_TOPBAR_OPTIONS.map(option => ({
          ...option,
          badge: issuesBySection[option.value]?.length || undefined,
        }))}
        onChange={setActiveSection}
      />
    ),
    primaryActions: [
      {
        id: 'save-settings',
        label: hasAnyUnsavedChanges ? 'Save Changes' : 'Saved',
        compactLabel: hasAnyUnsavedChanges ? 'Save' : 'Saved',
        onClick: () => { void saveAllSettingsChangesRef.current() },
        disabled: !canSaveAllSettingsChanges,
        tone: 'primary' as const,
        title: canSaveAllSettingsChanges ? 'Save settings changes' : 'Settings are already saved or cannot be saved',
      },
    ],
    secondaryActions: [
      {
        id: 'discard-settings',
        label: 'Discard Changes',
        compactLabel: 'Discard',
        onClick: () => discardAllSettingsChangesRef.current(),
        disabled: settingsMutationBusy,
        hidden: !hasAnyUnsavedChanges,
        tone: 'secondary' as const,
      },
    ],
    priority: 20,
  }), [activeSection, canSaveAllSettingsChanges, hasAnyUnsavedChanges, issuesBySection, settingsMutationBusy])

  useTopbarContribution(settingsTopbarContribution)

  return (
    <div className="page settings-page">
      <div className="page-header">
        <div>
          <h2>Settings</h2>
          <p className="page-subtitle">Configuration, thresholds, and integrations</p>
        </div>
        {!canManageSystem && (
          <p className="settings-error">Current session can view settings but cannot save global configuration changes.</p>
        )}
      </div>

      <div className="settings-tabbed-layout">
        <div className="settings-main">
          {loading && <p className="settings-notice" role="status" aria-live="polite">Loading settings...</p>}
          {activeSectionMessages.notices.map(msg => (
            <p key={msg} className="settings-notice" role="status" aria-live="polite">{msg}</p>
          ))}
          {activeSectionMessages.errors.map(msg => (
            <p key={msg} className="settings-error-inline" role="alert">{msg}</p>
          ))}
          {loadFailed && !loading && (
            <div className="settings-inline-actions">
              <button className="btn btn-secondary" type="button" disabled={settingsMutationBusy} onClick={() => { void loadSettings() }}>
                Retry Loading Settings
              </button>
            </div>
          )}
          {saveRecovery === 'reload' && (
            <div className="settings-inline-actions">
              <button className="btn btn-secondary" type="button" disabled={settingsMutationBusy} onClick={() => { void loadSettings() }}>
                Reload Settings
              </button>
            </div>
          )}
          {validationIssues.length > 0 && (
            <div className="settings-errors" role="alert">
              {(issuesBySection[activeSection] ?? []).map(issue => (
                <p key={`${issue.field ?? ''}${issue.message}`}>{issue.message}</p>
              ))}
              {SETTINGS_SECTIONS.filter(section => section.id !== activeSection && (issuesBySection[section.id]?.length ?? 0) > 0).map(section => (
                <p key={section.id} className="settings-inline-actions settings-errors-elsewhere">
                  {issuesBySection[section.id]?.length} issue{issuesBySection[section.id]?.length === 1 ? '' : 's'} in {section.title}
                  <button className="btn btn-ghost btn-sm" type="button" onClick={() => setActiveSection(section.id)}>
                    Review
                  </button>
                </p>
              ))}
            </div>
          )}

          <div className="settings-grid">
        <div
          id="settings-section-general"
          className={sectionClass}
          hidden={activeSection !== 'general'}
          style={{ order: getSectionOrder('general') }}
        >
          <fieldset className="settings-fieldset" disabled={formDisabled}>
          <div className="settings-subsection-layout">
            <div className="settings-subnav" role="tablist" aria-label="General settings categories">
              {GENERAL_SETTINGS_SUBSECTIONS.map(subsection => (
                <button
                  key={subsection.id}
                  id={`settings-general-tab-${subsection.id}`}
                  type="button"
                  role="tab"
                  aria-selected={activeGeneralSubsection === subsection.id}
                  aria-controls={`settings-general-panel-${subsection.id}`}
                  className={`settings-subnav-item ${activeGeneralSubsection === subsection.id ? 'active' : ''}`}
                  onClick={() => setActiveGeneralSubsection(subsection.id)}
                >
                  <span className="settings-subnav-label">{subsection.title}</span>
                  <span className="settings-subnav-meta">{generalSubsectionSummaries[subsection.id]}</span>
                </button>
              ))}
            </div>

            <section
              id="settings-general-panel-platform"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-platform"
              hidden={activeGeneralSubsection !== 'platform'}
            >
              <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">API Endpoint <span className="settings-restart-badge">Restart required</span></span>
                <span className="setting-desc">Controller API server address. Changes take effect after restarting the controller.</span>
              </div>
              <input
                className="settings-input cell-mono"
                value={settings.general.api_endpoint}
                onChange={(e) => updateGeneral('api_endpoint', e.target.value)}
                placeholder=":8080"
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Concurrent Tests</span>
                <span className="setting-desc">Maximum tests running simultaneously</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.max_concurrent_tests}
                onChange={(e) => updateGeneral('max_concurrent_tests', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Metrics Interval (seconds)</span>
                <span className="setting-desc">How often metrics are broadcast via WebSocket</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.general.metrics_interval_seconds}
                onChange={(e) => updateGeneral('metrics_interval_seconds', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Usage Snapshot Cadence (seconds)</span>
                <span className="setting-desc">Optional wall-clock cadence for durable usage accounting snapshots. Use 0 to disable scheduled capture.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                step={60}
                value={settings.general.usage_snapshot_interval_seconds || 0}
                onChange={(e) => updateGeneral('usage_snapshot_interval_seconds', e.target.value)}
              />
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Dashboard Theme</span>
                <span className="setting-desc">Stored locally in this browser and applied immediately</span>
              </div>
              <select
                className="settings-input"
                value={theme}
                onChange={(e) => onThemeChange(e.target.value as DashboardThemePreference)}
              >
                <option value="system">System (Auto)</option>
                <option value="dark">Dark</option>
                <option value="light">Light</option>
                <option value="solarized-dark">Solarized Dark</option>
                <option value="solarized-light">Solarized Light</option>
                <option value="monokai-dimmed">Monokai Dimmed</option>
              </select>
            </div>
            {desktopSurface && <DesktopNotificationsToggle />}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Desktop Client Promotion</span>
                <span className="setting-desc">Controls whether the hosted browser UI promotes the optional desktop workspace. Authorized API access is still governed by RBAC.</span>
              </div>
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={settings.client_app.availability === 'enabled'}
                  onChange={event => updateClientApp(event.target.checked ? 'enabled' : 'disabled')}
                  disabled={!canManageSystem}
                />
                <span>{settings.client_app.availability === 'enabled' ? 'Enabled' : 'Disabled'}</span>
              </label>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Client Software Downloads</span>
                <span className="setting-desc">Controls whether the dashboard offers the “Download client” menu (Recording Agent and desktop app). When disabled, the menu is hidden and the download endpoints are refused.</span>
              </div>
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={settings.client_downloads.enabled}
                  onChange={event => updateClientDownloads(event.target.checked)}
                  disabled={!canManageSystem}
                />
                <span>{settings.client_downloads.enabled ? 'Enabled' : 'Disabled'}</span>
              </label>
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-usage"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-usage"
              hidden={activeGeneralSubsection !== 'usage'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Usage Accounting</span>
                  <span className="setting-desc">Current project chargeback rollup plus durable snapshots for governance review.</span>
                </div>
                <strong>{usageAccounting ? usageAccounting.totals.run_count : '-'}</strong>
              </div>
              <div className="new-test-summary-grid">
                <div><span>VUser Seconds</span><strong>{usageAccounting ? usageAccounting.totals.vuser_seconds.toLocaleString() : '-'}</strong></div>
                <div><span>Duration</span><strong>{usageAccounting ? formatDurationSeconds(usageAccounting.totals.duration_seconds) : '-'}</strong></div>
                <div><span>Archive Bytes</span><strong>{usageAccounting ? formatBytes(usageAccounting.totals.archive_bytes) : '-'}</strong></div>
                <div><span>Status Counts</span><strong>{usageAccounting ? formatStatusCounts(usageAccounting.totals.status_counts) : '-'}</strong></div>
              </div>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={refreshUsageAccounting}>
                  Refresh Usage
                </button>
                <button className="btn btn-secondary" type="button" onClick={captureUsageSnapshot} disabled={!canManageSystem}>
                  Capture Snapshot
                </button>
                <button className="btn btn-secondary" type="button" onClick={exportUsageAccounting}>
                  Export Usage CSV
                </button>
              </div>
              {usageActionMessage && <p className="settings-notice" role="status" aria-live="polite">{usageActionMessage}</p>}
              {usageActionError && <p className="settings-error-inline" role="alert">{usageActionError}</p>}
              <div className="settings-release-list">
                {usageSnapshots.slice(0, 5).map(snapshot => (
                  <div key={snapshot.id} className="settings-release-row">
                    <div className="settings-release-main">
                      <span>{snapshot.id}</span>
                      <small>Captured {formatDateTime(snapshot.created_at)}</small>
                      <small>{snapshot.project_id || 'all projects'} / {snapshot.usage.totals.run_count} runs / {snapshot.usage.totals.vuser_seconds.toLocaleString()} VUser seconds</small>
                    </div>
                    <span className="setting-value">{formatBytes(snapshot.usage.totals.archive_bytes)}</span>
                  </div>
                ))}
                {usageSnapshots.length === 0 && <p className="new-test-inline-note">No usage snapshots captured yet.</p>}
              </div>
            </div>
            <EntitlementHeadroom />
              </div>
            </section>

            <section
              id="settings-general-panel-entitlements"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-entitlements"
              hidden={activeGeneralSubsection !== 'entitlements'}
            >
              <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Entitlements</span>
                      <span className="setting-desc">Edition, enabled protocol bundles, operational limits, and current consumption.</span>
                    </div>
                  </div>
                  <EntitlementsPanel />
                </div>
              </div>
            </section>

            <section
              id="settings-general-panel-readiness"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-readiness"
              hidden={activeGeneralSubsection !== 'readiness'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Production Readiness</span>
                  <span className="setting-desc">Controller-owned readiness checks for storage, archive writes, generator RPC, telemetry, auth, and scheduler state.</span>
                </div>
                <strong>{formatReadinessStatus(systemReadiness?.status)}</strong>
              </div>
              <div className="new-test-summary-grid">
                <div><span>Generated</span><strong>{systemReadiness ? formatDateTime(systemReadiness.generated_at) : '-'}</strong></div>
                <div><span>Ready</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'ready').length : '-'}</strong></div>
                <div><span>Degraded</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'degraded').length : '-'}</strong></div>
                <div><span>Blocked</span><strong>{systemReadiness ? systemReadiness.checks.filter(check => check.status === 'not_ready').length : '-'}</strong></div>
              </div>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={refreshSystemReadiness}>
                  Refresh Readiness
                </button>
              </div>
              {readinessActionMessage && <p className="settings-notice" role="status" aria-live="polite">{readinessActionMessage}</p>}
              {readinessActionError && <p className="settings-error-inline" role="alert">{readinessActionError}</p>}
              <div className="settings-release-list">
                {(systemReadiness?.checks || []).map(check => {
                  const details = formatReadinessDetails(check.details)
                  return (
                    <div key={check.name} className="settings-release-row">
                      <div className="settings-release-main">
                        <span>{formatReadinessCheckName(check.name)}</span>
                        <small>{check.message}</small>
                        {details && <small>{details}</small>}
                      </div>
                      <span className={`script-health-token ${readinessTone(check.status)}`}>{formatReadinessStatus(check.status)}</span>
                    </div>
                  )
                })}
                {!systemReadiness && <p className="new-test-inline-note">Production readiness is unavailable.</p>}
              </div>
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-authentication"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-authentication"
              hidden={activeGeneralSubsection !== 'authentication'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Access Control</span>
                  <span className="setting-desc">Backend 3.1 scaffold for local development bypass, Microsoft Entra ID-backed trusted headers, service-account API keys, and governance audit history.</span>
                </div>
                <strong>{formatAuthMode(settings.auth.mode)}</strong>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Access Control Mode <span className="settings-restart-badge">Restart required</span></span>
                  <span className="setting-desc">Dev Bypass keeps local development open by default. Trusted Headers expects an authenticating reverse proxy to inject user and role headers before requests reach APEX Load. Changes to auth mode take effect after restarting the controller.</span>
                </div>
                <select
                  className="settings-input"
                  value={settings.auth.mode}
                  onChange={(e) => updateAuth('mode', e.target.value)}
                >
                  <option value="dev">Dev Bypass</option>
                  <option value="oidc" disabled={desktopSurface}>Native OIDC</option>
                  <option value="trusted_headers" disabled={desktopSurface}>Trusted Headers</option>
                </select>
              </div>
              {desktopSurface && (
                <div className="settings-inline-note" role="note">
                  Native OIDC and Trusted Headers are configured and used from the hosted browser dashboard. The embedded desktop workspace requires Dev Bypass because browser sessions and proxy-injected headers are not available on its loopback connection.
                </div>
              )}
              {settings.auth.mode === 'dev' && (
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Development Bypass</span>
                      <span className="setting-desc">Local development bypass is active. APEX Load allows dashboard and API access without upstream identity, while still recording audit events and supporting service-account API keys for automation flows.</span>
                    </div>
                    <strong>{formatAuthStatusSummary(authStatus)}</strong>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Current Access</span>
                      <span className="setting-desc">All operations are currently permitted through the explicit dev-mode bypass. Switch to Trusted Headers before production use.</span>
                    </div>
                    <span className="settings-value-display">Open access for local development</span>
                  </div>
                </div>
              )}
              {settings.auth.mode === 'trusted_headers' && (
                <>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Provider Label</span>
                      <span className="setting-desc">Optional identity provider or gateway name shown in the dashboard sign-in screen.</span>
                    </div>
                    <input
                      className="settings-input"
                      value={settings.auth.provider_name || ''}
                      onChange={(e) => updateAuth('provider_name', e.target.value)}
                      placeholder="Microsoft Entra ID"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Login URL</span>
                      <span className="setting-desc">Optional upstream sign-in URL or absolute path used by the dashboard access gate.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
					  aria-label="Prometheus bearer token environment variable"
                      value={settings.auth.login_url || ''}
                      onChange={(e) => updateAuth('login_url', e.target.value)}
                      placeholder="https://login.example.com/apexload/start"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Logout URL</span>
                      <span className="setting-desc">Optional upstream sign-out URL or absolute path surfaced for future dashboard navigation.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.logout_url || ''}
                      onChange={(e) => updateAuth('logout_url', e.target.value)}
                      placeholder="/logout"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">User Header</span>
                      <span className="setting-desc">Required identity header. Missing values return 401 before the request reaches protected handlers.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.user_header}
                      onChange={(e) => updateAuth('user_header', e.target.value)}
                      placeholder="X-Auth-Request-User"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Display Name Header</span>
                      <span className="setting-desc">Optional friendly name forwarded into the request context for future UI and audit use.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.name_header}
                      onChange={(e) => updateAuth('name_header', e.target.value)}
                      placeholder="X-Auth-Request-Name"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Email Header</span>
                      <span className="setting-desc">Optional email address from the upstream identity provider.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.email_header}
                      onChange={(e) => updateAuth('email_header', e.target.value)}
                      placeholder="X-Auth-Request-Email"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Global Roles Header</span>
                      <span className="setting-desc">Comma or pipe-delimited roles such as viewer, editor, project_admin, or system_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.roles_header}
                      onChange={(e) => updateAuth('roles_header', e.target.value)}
                      placeholder="X-APEXLoad-Roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Roles Header</span>
                      <span className="setting-desc">Project-scoped assignments in the form project-id=viewer|editor;other-project=project_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.project_roles_header}
                      onChange={(e) => updateAuth('project_roles_header', e.target.value)}
                      placeholder="X-APEXLoad-Project-Roles"
                    />
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Microsoft Entra ID Gateway Contract</span>
                        <span className="setting-desc">Use your Entra-backed ingress or reverse proxy to translate identity claims into the headers APEX Load evaluates for RBAC.</span>
                      </div>
                      <strong>{settings.auth.provider_name?.trim() || 'Microsoft Entra ID'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Required Subject Header</span>
                        <span className="setting-desc">Stable user identifier forwarded on every authenticated request.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.user_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Optional Identity Headers</span>
                        <span className="setting-desc">Friendly display data forwarded from Entra claims when available.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.name_header)} | {formatHeaderContractValue(settings.auth.email_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Global Role Mapping</span>
                        <span className="setting-desc">Map Entra groups or app roles into APEX Load roles such as viewer, editor, project_admin, and system_admin.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.roles_header, 'No global role header')}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Project Role Mapping</span>
                        <span className="setting-desc">Emit assignments in the form project-id=viewer|editor;other-project=project_admin.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.project_roles_header, 'No project role header')}</span>
                    </div>
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Current Session Access</span>
                        <span className="setting-desc">Shows the identity and role assignments from the trusted headers reaching APEX Load right now. User lifecycle remains managed by Microsoft Entra ID or another upstream OIDC or SAML gateway.</span>
                      </div>
                      <strong>{formatAuthStatusSummary(authStatus)}</strong>
                    </div>
                    {authStatus?.authenticated && authStatus.user ? (
                      <>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Subject</span>
                            <span className="setting-desc">Stable upstream identity key used for access checks.</span>
                          </div>
                          <span className="settings-value-display cell-mono">{authStatus.user.subject}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Display Name</span>
                            <span className="setting-desc">Friendly identity label if the proxy forwards one.</span>
                          </div>
                          <span className="settings-value-display">{authStatus.user.display_name || 'Not provided'}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Email</span>
                            <span className="setting-desc">Optional email forwarded from the upstream identity provider.</span>
                          </div>
                          <span className="settings-value-display">{authStatus.user.email || 'Not provided'}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Global Roles</span>
                            <span className="setting-desc">Effective instance-wide roles on this request.</span>
                          </div>
                          <span className="settings-value-display">{formatAuthRoleList(authStatus.user.global_roles)}</span>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Project Roles</span>
                            <span className="setting-desc">Project-scoped assignments resolved from the forwarded headers.</span>
                          </div>
                          <span className="settings-value-display">{formatProjectRoleAssignments(authStatus.user.project_roles)}</span>
                        </div>
                      </>
                    ) : (
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Session Status</span>
                          <span className="setting-desc">Open the dashboard through the authenticating proxy so APEX Load receives identity and role headers.</span>
                        </div>
                        <span className="settings-value-display">No authenticated user detected</span>
                      </div>
                    )}
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Microsoft Entra ID Troubleshooting</span>
                        <span className="setting-desc">Use this checklist when APEX Load is configured for trusted headers but the current request is missing an authenticated identity.</span>
                      </div>
                      <strong>{authStatus?.authenticated ? 'Headers detected' : 'Check gateway mapping'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Open Through Entra Gateway</span>
                        <span className="setting-desc">Reach the dashboard through the Entra-protected ingress or reverse proxy instead of calling the controller origin directly.</span>
                      </div>
                      <span className="settings-value-display">{authStatus?.authenticated ? 'Current request is authenticated' : 'Required for sign-in'}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Subject Header Check</span>
                        <span className="setting-desc">Verify the gateway forwards the configured subject header on every authenticated request.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.user_header)}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Role Header Check</span>
                        <span className="setting-desc">Forward either the global roles header or the project roles header so APEX Load can evaluate RBAC.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{formatHeaderContractValue(settings.auth.roles_header, 'No global role header')} | {formatHeaderContractValue(settings.auth.project_roles_header, 'No project role header')}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Gateway UX</span>
                        <span className="setting-desc">Provider label and login URL should point operators back to the correct Microsoft Entra ID entry point when APEX Load blocks unauthenticated access.</span>
                      </div>
                      <span className="settings-value-display">{settings.auth.provider_name?.trim() || 'Microsoft Entra ID'}{settings.auth.login_url?.trim() ? ` | ${settings.auth.login_url.trim()}` : ''}</span>
                    </div>
                  </div>
                </>
              )}
              {settings.auth.mode === 'oidc' && (
                <>
                  <div className="settings-subsection">
                    <div>
                      <span className="setting-label">OIDC Provider Presets</span>
                      <span className="setting-desc">Apply safe provider defaults for label, scopes, claim names, and secret environment variables. Issuer URL, client ID, and redirect URL stay deployment-specific.</span>
                    </div>
                    <div className="settings-preset-row">
                      {OIDC_PROVIDER_PRESETS.map(preset => (
                        <button key={preset.id} type="button" className="btn btn-secondary" onClick={() => applyOIDCProviderPreset(preset)}>
                          {preset.label}
                        </button>
                      ))}
                    </div>
                    <p className="setting-desc">{OIDC_PROVIDER_PRESETS.find(preset => preset.provider_name === settings.auth.provider_name)?.description || 'Choose a preset, then fill the issuer, client, and callback values registered with your identity provider.'}</p>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Provider Label</span>
                      <span className="setting-desc">Friendly identity provider name shown in the dashboard sign-in experience.</span>
                    </div>
                    <input
                      className="settings-input"
                      value={settings.auth.provider_name || ''}
                      onChange={(e) => updateAuth('provider_name', e.target.value)}
                      placeholder="Microsoft Entra ID"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Issuer URL</span>
                      <span className="setting-desc">Absolute OIDC issuer URL used for discovery and ID-token verification.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.issuer_url || ''}
                      onChange={(e) => updateAuth('issuer_url', e.target.value)}
                      placeholder="https://login.example.com/tenant/v2.0"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Client ID</span>
                      <span className="setting-desc">OIDC client/application identifier registered for the dashboard.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.client_id || ''}
                      onChange={(e) => updateAuth('client_id', e.target.value)}
                      placeholder="apexload-dashboard"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Client Secret Env Var</span>
                      <span className="setting-desc">Environment variable name read by the controller at runtime for the OIDC client secret.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.client_secret_env_var || ''}
                      onChange={(e) => updateAuth('client_secret_env_var', e.target.value)}
                      placeholder="APEXLOAD_OIDC_CLIENT_SECRET"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Redirect URL</span>
                      <span className="setting-desc">Absolute callback URL registered with the identity provider and served by the controller.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.redirect_url || ''}
                      onChange={(e) => updateAuth('redirect_url', e.target.value)}
                      placeholder="https://apexload.example.com/api/v1/auth/callback"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Scopes</span>
                      <span className="setting-desc">Comma or space-delimited OIDC scopes requested during sign-in.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={formatAuthScopes(settings.auth.scopes)}
                      onChange={(e) => updateAuth('scopes', parseAuthScopes(e.target.value))}
                      placeholder="openid, profile, email"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Roles Claim</span>
                      <span className="setting-desc">Token claim used to map global RBAC roles such as viewer, editor, project_admin, or system_admin.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.roles_claim || ''}
                      onChange={(e) => updateAuth('roles_claim', e.target.value)}
                      placeholder="roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Roles Claim</span>
                      <span className="setting-desc">Token claim that carries project-scoped role assignments.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.project_roles_claim || ''}
                      onChange={(e) => updateAuth('project_roles_claim', e.target.value)}
                      placeholder="project_roles"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Session Secret Env Var</span>
                      <span className="setting-desc">Environment variable name read by the controller to sign the APEX Load auth session cookie.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.session_secret_env_var || ''}
                      onChange={(e) => updateAuth('session_secret_env_var', e.target.value)}
                      placeholder="APEXLOAD_AUTH_SESSION_SECRET"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Post-Logout Redirect URL</span>
                      <span className="setting-desc">Optional absolute URL or absolute path used after APEX Load clears its local OIDC session.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.auth.logout_url || ''}
                      onChange={(e) => updateAuth('logout_url', e.target.value)}
                      placeholder="/"
                    />
                  </div>
                  <div className="settings-subsection">
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Native OIDC Session Flow</span>
                        <span className="setting-desc">APEX Load redirects the dashboard through the identity provider, exchanges the authorization code, and then serves its own signed session cookie for API access.</span>
                      </div>
                      <strong>{settings.auth.provider_name?.trim() || 'OIDC Provider'}</strong>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Sign-In Route</span>
                        <span className="setting-desc">Dashboard entry point exposed by the controller when native OIDC mode is enabled.</span>
                      </div>
                      <span className="settings-value-display cell-mono">/api/v1/auth/login</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Callback Route</span>
                        <span className="setting-desc">Must match the registered redirect URI at the identity provider.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{settings.auth.redirect_url?.trim() || 'Not configured'}</span>
                    </div>
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Claim Mapping</span>
                        <span className="setting-desc">APEX Load maps token claims back onto the same RBAC role catalog used by service accounts and project governance.</span>
                      </div>
                      <span className="settings-value-display cell-mono">{settings.auth.roles_claim?.trim() || 'roles'} | {settings.auth.project_roles_claim?.trim() || 'project_roles'}</span>
                    </div>
                  </div>
                </>
              )}
              {(settings.auth.mode === 'trusted_headers' || settings.auth.mode === 'oidc') && (
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Auth Diagnostics</span>
                      <span className="setting-desc">Backend-owned readiness checks for the active auth mode. These checks surface missing env vars, invalid URLs, discovery failures, and missing gateway guidance before sign-in failures turn into guesswork.</span>
                    </div>
                    <strong>{formatAuthDiagnosticsHeadline(authDiagnostics)}</strong>
                  </div>
                  {authDiagnostics ? (
                    <>
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Readiness Summary</span>
                          <span className="setting-desc">Overall backend assessment for the current saved auth mode.</span>
                        </div>
                        <span className="settings-value-display">{authDiagnostics.summary}</span>
                      </div>
                      {authDiagnosticsStale && (
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Draft Changes Pending</span>
                            <span className="setting-desc">These diagnostics still reflect the last saved auth configuration.</span>
                          </div>
                          <span className="settings-value-display">Save changes to refresh diagnostics</span>
                        </div>
                      )}
                      {authDiagnostics.checks.map((check: DashboardAuthDiagnosticsCheck) => (
                        <div className="setting-row" key={check.id}>
                          <div className="setting-info">
                            <span className="setting-label">{check.label}</span>
                            <span className="setting-desc">{check.summary}</span>
                          </div>
                          <span className="settings-value-display">
                            {formatAuthDiagnosticStatus(check.status)}
                            {check.detail ? ` | ${check.detail}` : ''}
                          </span>
                        </div>
                      ))}
                    </>
                  ) : (
                    <div className="setting-row">
                      <div className="setting-info">
                        <span className="setting-label">Diagnostics Status</span>
                        <span className="setting-desc">APEX Load could not load the auth diagnostics payload for this session.</span>
                      </div>
                      <span className="settings-value-display">Unavailable</span>
                    </div>
                  )}
                </div>
              )}
            </div>
              </div>
            </section>

            <section
              id="settings-general-panel-governance"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-general-tab-governance"
              hidden={activeGeneralSubsection !== 'governance'}
            >
              <div className="settings-subsection-layout">
                {renderSettingsSubnav(
                  'Governance settings categories',
                  'settings-governance',
                  GOVERNANCE_SETTINGS_SUBSECTIONS,
                  activeGovernanceSubsection,
                  setActiveGovernanceSubsection,
                  governanceSubsectionSummaries,
                )}
                <section
                  id="settings-governance-panel-schedule-defaults"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-governance-tab-schedule-defaults"
                  hidden={activeGovernanceSubsection !== 'schedule-defaults'}
                >
                  <div className="settings-group">
                    {canManageSystem ? (
                      <div className="settings-subsection">
                        <div className="settings-manager-summary">
                          <div className="setting-info">
                            <span className="setting-label">Scenario Schedule Defaults</span>
                            <span className="setting-desc">Administrator-owned governance and calendar-window defaults applied when new recurring schedules are created.</span>
                          </div>
                          <strong>{formatSchedulePolicyLabel(settings.schedule_defaults.approval_policy || 'auto')}</strong>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Approval Policy</span>
                            <span className="setting-desc">Default review policy used by New Test schedules before each recurring run materializes.</span>
                          </div>
                          <select
                            className="settings-input"
                            value={settings.schedule_defaults.approval_policy || 'auto'}
                            onChange={(e) => updateScheduleDefaults('approval_policy', e.target.value)}
                          >
                            <option value="auto">Auto approve</option>
                            <option value="manual_review">Manual review</option>
                            <option value="high_cost">High cost</option>
                            <option value="off_hours">Off hours</option>
                            <option value="production">Production</option>
                            <option value="quota_burst">Quota burst</option>
                          </select>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Missed Run Policy</span>
                            <span className="setting-desc">Default reconciliation behavior when the scheduler starts after one or more cron windows were missed.</span>
                          </div>
                          <select
                            className="settings-input"
                            value={settings.schedule_defaults.missed_run_policy || 'skip'}
                            onChange={(e) => updateScheduleDefaults('missed_run_policy', e.target.value)}
                          >
                            <option value="skip">Skip missed runs</option>
                            <option value="catch_up_once">Catch up once</option>
                            <option value="catch_up_all">Catch up all</option>
                            <option value="require_operator_decision">Require operator decision</option>
                          </select>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Blackout Window</span>
                            <span className="setting-desc">Optional default daily no-run range. Schedules apply it in their selected timezone.</span>
                          </div>
                          <div className="inline-create-row">
                            <input
                              className="settings-input"
                              value={settings.schedule_defaults.blackout_start_time || ''}
                              onChange={(e) => updateScheduleDefaults('blackout_start_time', e.target.value)}
                              placeholder="00:00"
                            />
                            <input
                              className="settings-input"
                              value={settings.schedule_defaults.blackout_end_time || ''}
                              onChange={(e) => updateScheduleDefaults('blackout_end_time', e.target.value)}
                              placeholder="06:00"
                            />
                          </div>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Availability Window</span>
                            <span className="setting-desc">Optional default weekday allowed-run range. Leave blank to allow all weekdays.</span>
                          </div>
                          <div className="inline-create-row">
                            <input
                              className="settings-input"
                              value={settings.schedule_defaults.availability_start_time || ''}
                              onChange={(e) => updateScheduleDefaults('availability_start_time', e.target.value)}
                              placeholder="09:00"
                            />
                            <input
                              className="settings-input"
                              value={settings.schedule_defaults.availability_end_time || ''}
                              onChange={(e) => updateScheduleDefaults('availability_end_time', e.target.value)}
                              placeholder="17:00"
                            />
                          </div>
                        </div>
                      </div>
                    ) : (
                      <div className="settings-subsection">
                        <div className="settings-manager-summary">
                          <div className="setting-info">
                            <span className="setting-label">Scenario Schedule Defaults</span>
                            <span className="setting-desc">Administrator-owned governance and calendar-window defaults applied when new recurring schedules are created.</span>
                          </div>
                          <strong>View only</strong>
                        </div>
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">Global Settings Permission Required</span>
                            <span className="setting-desc">Current session can view settings but cannot save schedule governance defaults.</span>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                </section>

                <section
                  id="settings-governance-panel-script-imports"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-governance-tab-script-imports"
                  hidden={activeGovernanceSubsection !== 'script-imports'}
                >
                  <div className="settings-group">
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div className="setting-info">
                          <span className="setting-label">Source Credential Migration</span>
                          <span className="setting-desc">
                            Controls whether project and system administrators may opt in to copying recoverable credential values from a protected import session into the project secret provider.
                          </span>
                        </div>
                        <span className={`script-health-token ${settings.script_imports.allow_source_secret_migration ? 'warning' : ''}`}>
                          {settings.script_imports.allow_source_secret_migration ? 'enabled' : 'disabled'}
                        </span>
                      </div>
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Allow source secret migration</span>
                          <span className="setting-desc">
                            Default off. Enabling exposes an unchecked import option to administrators only. Imports remain create-only and never overwrite an existing project secret.
                          </span>
                        </div>
                        <label className="settings-checkbox-label">
                          <input
                            type="checkbox"
                            checked={settings.script_imports.allow_source_secret_migration}
                            disabled={!canManageSystem}
                            onChange={event => updateScriptImports(event.target.checked)}
                          />
                          <span>{settings.script_imports.allow_source_secret_migration ? 'Allowed' : 'Blocked'}</span>
                        </label>
                      </div>
                      {!canManageSystem && (
                        <p className="settings-notice">Only a system administrator can change source-secret migration policy.</p>
                      )}
                    </div>
                  </div>
                </section>

                <section
                  id="settings-governance-panel-service-account-apis"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-governance-tab-service-account-apis"
                  hidden={activeGovernanceSubsection !== 'service-account-apis'}
                >
                  <div className="settings-group">
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div className="setting-info">
                          <span className="setting-label">Service Account API Keys</span>
                          <span className="setting-desc">Generate, rotate, disable, and delete scoped API keys from Access & API Keys. Settings keeps the auth mode, role contract, and audit evidence here.</span>
                        </div>
                        <strong>{serviceAccounts.length}</strong>
                      </div>
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Provisioning Workspace</span>
                          <span className="setting-desc">Use the dedicated Access page for CI, MCP, reporting, script import, and system automation key generation.</span>
                        </div>
                        <div className="settings-inline-actions">
                          <button className="btn btn-secondary btn-accent-action" type="button" onClick={() => onNavigate?.('access')} disabled={!canManageServiceAccounts || !onNavigate}>
                            Open Access & API Keys
                          </button>
                        </div>
                      </div>
                      {!canManageServiceAccounts && (
                        <p className="settings-notice">Current session can view auth settings but cannot manage service-account API keys.</p>
                      )}
                    </div>
                    {settings.auth.mode !== 'dev' && (
                      <>
                        {authRoles.length > 0 && (
                          <div className="settings-subsection">
                            <div className="settings-manager-summary">
                              <div className="setting-info">
                                <span className="setting-label">Built-In Role Reference</span>
                                <span className="setting-desc">APEX Load applies these roles through the request RBAC matrix regardless of whether identity arrives from trusted headers or a native OIDC session.</span>
                              </div>
                              <strong>{authRoles.length} roles</strong>
                            </div>
                            {authRoles.map((role) => (
                              <div className="setting-row" key={role.id}>
                                <div className="setting-info">
                                  <span className="setting-label">{role.name}</span>
                                  <span className="setting-desc">{role.description}</span>
                                </div>
                                <span className="settings-value-display">{formatRoleScope(role.scope)}{role.capabilities && role.capabilities.length > 0 ? ` | ${role.capabilities.join(', ')}` : ''}</span>
                              </div>
                            ))}
                          </div>
                        )}
                        {authAccessResources.length > 0 && (
                          <div className="settings-subsection">
                            <div className="settings-manager-summary">
                              <div className="setting-info">
                                <span className="setting-label">Route Access Matrix</span>
                                <span className="setting-desc">Backend-owned summary of which roles can read or mutate each major platform surface.</span>
                              </div>
                              <strong>{authAccessResources.length} resources</strong>
                            </div>
                            {authAccessResources.map((resource) => (
                              <div className="setting-row" key={resource.id}>
                                <div className="setting-info">
                                  <span className="setting-label">{resource.name}</span>
                                  <span className="setting-desc">{resource.description}</span>
                                  {resource.operations.map((operation) => (
                                    <span className="setting-desc" key={operation.id}>{operation.label}: {formatAccessOperation(operation)}</span>
                                  ))}
                                </div>
                              </div>
                            ))}
                          </div>
                        )}
                        {effectiveAuthAccessResources.length > 0 && (
                          <div className="settings-subsection">
                            <div className="settings-manager-summary">
                              <div className="setting-info">
                                <span className="setting-label">Effective Access For Current Session</span>
                                <span className="setting-desc">Evaluates the current interactive identity against the active project selection and the backend RBAC policy.</span>
                              </div>
                              <strong>{activeProject?.name || 'Global'}</strong>
                            </div>
                            {effectiveAuthAccessResources.map((resource) => (
                              <div className="setting-row" key={resource.id}>
                                <div className="setting-info">
                                  <span className="setting-label">{resource.name}</span>
                                  <span className="setting-desc">{resource.description}</span>
                                  {resource.operations.map((operation) => (
                                    <span className="setting-desc" key={operation.id}>{operation.label}: {formatGrantSource(operation.grant_source)}</span>
                                  ))}
                                </div>
                              </div>
                            ))}
                          </div>
                        )}
                      </>
                    )}
                  </div>
                </section>

                <section
                  id="settings-governance-panel-recent-audit-events"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-governance-tab-recent-audit-events"
                  hidden={activeGovernanceSubsection !== 'recent-audit-events'}
                >
                  <div className="settings-group">
                    <div className="settings-subsection">
                      <div className="settings-manager-summary">
                        <div className="setting-info">
                          <span className="setting-label">Recent Audit Events</span>
                          <span className="setting-desc">Backend-recorded governance history for settings changes, project administration, service-account provisioning, and core test control actions.</span>
                        </div>
                        <strong>{auditEvents.length}</strong>
                      </div>
                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Audit Filters</span>
                          <span className="setting-desc">Narrow the recent audit feed and CSV export by action, actor type, or status.</span>
                        </div>
                        <div className="settings-inline-actions">
                          <input className="settings-input cell-mono" value={auditActionFilter} onChange={(e) => setAuditActionFilter(e.target.value)} placeholder="action" />
                          <input className="settings-input cell-mono" value={auditActorTypeFilter} onChange={(e) => setAuditActorTypeFilter(e.target.value)} placeholder="actor_type" />
                          <input className="settings-input cell-mono" value={auditStatusFilter} onChange={(e) => setAuditStatusFilter(e.target.value)} placeholder="status" />
                          <button className="btn btn-secondary" type="button" onClick={exportAuditEvents} disabled={!canViewAudit}>
                            Export CSV
                          </button>
                        </div>
                      </div>
                      {(auditActionMessage || auditActionError) && (
                        <div className={`settings-alert ${auditActionError ? 'error' : 'success'}`}>
                          {auditActionError || auditActionMessage}
                        </div>
                      )}
                      {auditEvents.length > 0 ? auditEvents.map((event) => (
                        <div className="setting-row" key={event.id}>
                          <div className="setting-info">
                            <span className="setting-label">{event.action}</span>
                            <span className="setting-desc">{formatAuditActor(event)} · {event.actor_type} · {event.status}</span>
                            <span className="setting-desc">Resource: {event.resource_type}{event.resource_id ? `/${event.resource_id}` : ''}{event.project_id ? ` | Project: ${event.project_id}` : ''}</span>
                            <span className="setting-desc">Recorded: {formatAuditTimestamp(event.created_at)}</span>
                          </div>
                        </div>
                      )) : (
                        <div className="setting-row">
                          <div className="setting-info">
                            <span className="setting-label">No audit events yet</span>
                            <span className="setting-desc">Audit history appears here after configuration, project, or execution lifecycle changes.</span>
                          </div>
                        </div>
                      )}
                    </div>
                  </div>
                </section>
              </div>
            </section>
          </div>
          </fieldset>
        </div>

        <div
          id="settings-section-thresholds"
          className={sectionClass}
          hidden={activeSection !== 'thresholds'}
          style={{ order: getSectionOrder('thresholds') }}
        >
          <div className="settings-group">
            <fieldset className="settings-fieldset" disabled={formDisabled}>
            <div className="settings-subgroup-header">
              <span className="settings-subgroup-title">Performance SLAs</span>
              <span className="settings-subgroup-desc">Alert and auto-stop thresholds for transaction-level quality gates</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Error Rate (%)</span>
                <span className="setting-desc">Optional: a breach auto-stops the run and raises a live alert plus configured notification webhooks</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={100}
                step={0.1}
                value={settings.thresholds.max_error_rate_pct ?? ''}
                onChange={(e) => updateThreshold('max_error_rate_pct', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            {renderFieldIssues('thresholds.max_error_rate_pct')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Response Time (P99 ms)</span>
                <span className="setting-desc">Optional: a breach raises a live alert and notification webhooks. Alert-only unless a stop sample count is set below.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_p99_latency_ms ?? ''}
                onChange={(e) => updateThreshold('max_p99_latency_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            {renderFieldIssues('thresholds.max_p99_latency_ms')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">P99 Stop After Breached Samples</span>
                <span className="setting-desc">Optional: auto-stop the run after this many consecutive metrics samples above Max Response Time (P99). Samples arrive once per metrics interval on local runs and about once per second on distributed runs. Blank keeps P99 alert-only.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                step={1}
                value={settings.thresholds.p99_latency_stop_consecutive_samples ?? ''}
                onChange={(e) => updateThreshold('p99_latency_stop_consecutive_samples', e.target.value)}
                placeholder="Alert only"
              />
            </div>
            {renderFieldIssues('thresholds.p99_latency_stop_consecutive_samples')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Avg Server Processing (ms)</span>
                <span className="setting-desc">Optional: a breach raises a live alert for the worst transaction — it does not stop the run</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_avg_server_processing_ms ?? ''}
                onChange={(e) => updateThreshold('max_avg_server_processing_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            {renderFieldIssues('thresholds.max_avg_server_processing_ms')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Max Avg Transfer Time (ms)</span>
                <span className="setting-desc">Optional: a breach raises a live alert for the worst transaction — it does not stop the run</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={settings.thresholds.max_avg_transfer_ms ?? ''}
                onChange={(e) => updateThreshold('max_avg_transfer_ms', e.target.value)}
                placeholder="Not configured"
              />
            </div>
            {renderFieldIssues('thresholds.max_avg_transfer_ms')}
            <div className="settings-subgroup-header">
              <span className="settings-subgroup-title">Fleet Alerts</span>
              <span className="settings-subgroup-desc">Free-slot floors for routed fleet alerts and their webhooks. The per-generator capacity badge stays a relative heuristic (20% of that generator's own capacity), so these floors change alerts only.</span>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Fleet Constrained Free Slots</span>
                <span className="setting-desc">Optional: warn when a generator has this many or fewer free VUser slots</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                step={1}
                value={settings.thresholds.fleet_constrained_available_slots ?? ''}
                onChange={(e) => updateThreshold('fleet_constrained_available_slots', e.target.value)}
                placeholder="Inventory default"
              />
            </div>
            {renderFieldIssues('thresholds.fleet_constrained_available_slots')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Fleet Saturated Free Slots</span>
                <span className="setting-desc">Optional: mark danger when a generator has this many or fewer free VUser slots. Must be less than the constrained threshold.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                step={1}
                value={settings.thresholds.fleet_saturated_available_slots ?? ''}
                onChange={(e) => updateThreshold('fleet_saturated_available_slots', e.target.value)}
                placeholder="0"
              />
            </div>
            {renderFieldIssues('thresholds.fleet_saturated_available_slots')}
            <div className="settings-subgroup-header">
              <span className="settings-subgroup-title">Telemetry Pipeline</span>
              <span className="settings-subgroup-desc">Time budgets for each stage of the live metrics pipeline: generator capture, transport, controller fan-out, and browser render. Breaches surface as telemetry diagnostics warnings.</span>
            </div>
            {TELEMETRY_BUDGET_ROWS.map(row => (
              <div key={row.key}>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">{row.label}</span>
                    <span className="setting-desc">{row.desc}</span>
                  </div>
                  <input
                    className="settings-input settings-input-number"
                    type="number"
                    min={0}
                    step={1}
                    value={settings.thresholds[row.key] ?? ''}
                    onChange={(e) => updateThreshold(row.key, e.target.value)}
                    placeholder={String(TELEMETRY_BUDGET_DEFAULTS[row.key])}
                  />
                  {settings.thresholds[row.key] !== TELEMETRY_BUDGET_DEFAULTS[row.key] && (
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      onClick={() => updateThreshold(row.key, String(TELEMETRY_BUDGET_DEFAULTS[row.key]))}
                    >
                      Reset to default ({TELEMETRY_BUDGET_DEFAULTS[row.key]} ms)
                    </button>
                  )}
                </div>
                {renderFieldIssues(`thresholds.${row.key}`)}
              </div>
            ))}
            </fieldset>
          </div>
        </div>

        <div
          id="settings-section-capacity"
          className={sectionClass}
          hidden={activeSection !== 'capacity'}
          style={{ order: getSectionOrder('capacity') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Capacity settings categories',
              'settings-capacity',
              CAPACITY_SETTINGS_SUBSECTIONS,
              activeCapacitySubsection,
              setActiveCapacitySubsection,
              capacitySubsectionSummaries,
              capacityIssueBadges,
            )}
            <section
              id="settings-capacity-panel-availability"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-availability"
              hidden={activeCapacitySubsection !== 'availability'}
            >
              <div className="settings-group">
            <div className="settings-status-block">
              <div className="settings-status-header">
                <span className="settings-status-title">Current Availability Snapshot</span>
                <span className={`settings-status-badge ${availabilitySnapshot ? 'ready' : 'unknown'}`}>
                  {availabilitySnapshot ? 'Live status' : 'Unavailable'}
                </span>
              </div>
              <div className="settings-status-grid">
                <div className="settings-status-item">
                  <span className="settings-status-label">Source</span>
                  <strong>{availabilitySourceLabel}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Effective Available</span>
                  <strong>{availabilitySnapshot?.effective_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Configured Ceiling</span>
                  <strong>{availabilitySnapshot?.configured_available_generators ?? settings.capacity_planning.availability.configured_available_generators}</strong>
                </div>
                <div className="settings-status-item">
                  <span className="settings-status-label">Live Snapshot</span>
                  <strong>{availabilitySnapshot?.live_available_generators ?? 'Not provided'}</strong>
                </div>
              </div>
              <p className="settings-status-copy">
                Advisory calculations and pre-start checks use this snapshot when availability source is set to configured plus live.
              </p>
              {availabilitySnapshot?.warnings && availabilitySnapshot.warnings.length > 0 && (
                <div className="settings-status-warnings" role="status">
                  {availabilitySnapshot.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </div>
            <fieldset className="settings-fieldset" disabled={formDisabled}>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Availability Source</span>
                <span className="setting-desc">Choose whether capacity advice should stop at configured inventory or also honor the latest live generator snapshot</span>
              </div>
              <select
                className="settings-input"
                value={settings.capacity_planning.availability.source}
                onChange={(e) => updateAvailability('source', e.target.value)}
              >
                <option value="configured">Configured generators only</option>
                <option value="configured_plus_live">Configured plus live availability</option>
              </select>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Configured Available Generators</span>
                <span className="setting-desc">Configured inventory ceiling. When live snapshots are enabled, the effective available count is the lower of this value and the latest runtime snapshot.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={1}
                value={capacityFieldDrafts['capacity_planning.availability.configured_available_generators'] ?? String(settings.capacity_planning.availability.configured_available_generators)}
                onChange={(e) => updateAvailability('configured_available_generators', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.availability.configured_available_generators')}
            </fieldset>
              </div>
            </section>

            <section
              id="settings-capacity-panel-generator"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-generator"
              hidden={activeCapacitySubsection !== 'generator'}
            >
              <div className="settings-group">
            <fieldset className="settings-fieldset" disabled={formDisabled}>
            <div className="settings-subsection">
              <div>
                <span className="setting-label">Generator Presets</span>
                <span className="setting-desc">Apply common sizing defaults, then tune individual fields below.</span>
              </div>
              <div className="settings-preset-row">
                {GENERATOR_PRESETS.map(preset => (
                  <button key={preset.id} type="button" className="btn btn-secondary" onClick={() => applyGeneratorPreset(preset)}>
                    {preset.label}
                  </button>
                ))}
                <button type="button" className="btn btn-secondary" onClick={applyLiveGeneratorPreset}>
                  Live Pod Capacity
                </button>
              </div>
              <p className="setting-desc">Live Pod Capacity uses connected generators' reported pod CPU, pod memory, VUser capacity, and count from the controller inventory. Network remains manual for now.</p>
            </div>
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Headroom (%)</span>
                <span className="setting-desc">Reserve spare generator capacity when estimating pod requirements. 0 uses full generator capacity.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0}
                max={99}
                step={1}
                value={capacityFieldDrafts['capacity_planning.headroom_pct'] ?? String(settings.capacity_planning.headroom_pct)}
                onChange={(e) => updateCapacityPlanning('headroom_pct', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.headroom_pct')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator CPU (cores)</span>
                <span className="setting-desc">Baseline CPU available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={0.1}
                step={0.1}
                value={capacityFieldDrafts['capacity_planning.generator.cpu_cores'] ?? String(settings.capacity_planning.generator.cpu_cores)}
                onChange={(e) => updateGenerator('cpu_cores', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.generator.cpu_cores')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Memory (MB)</span>
                <span className="setting-desc">Baseline memory available per load generator pod or machine</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={256}
                value={capacityFieldDrafts['capacity_planning.generator.memory_mb'] ?? String(settings.capacity_planning.generator.memory_mb)}
                onChange={(e) => updateGenerator('memory_mb', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.generator.memory_mb')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Network (Mbps)</span>
                <span className="setting-desc">Estimated network throughput budget per load generator</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={50}
                value={capacityFieldDrafts['capacity_planning.generator.network_mbps'] ?? String(settings.capacity_planning.generator.network_mbps)}
                onChange={(e) => updateGenerator('network_mbps', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.generator.network_mbps')}
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Generator Max VUsers</span>
                <span className="setting-desc">Planning ceiling for VUsers one generator should carry before headroom. Used for sizing and admission math; the hard runtime cap is each generator's own APEXLOAD_GENERATOR_MAX_VUSERS.</span>
              </div>
              <input
                className="settings-input settings-input-number"
                type="number"
                min={1}
                step={100}
                value={capacityFieldDrafts['capacity_planning.generator.max_vusers'] ?? String(settings.capacity_planning.generator.max_vusers)}
                onChange={(e) => updateGenerator('max_vusers', e.target.value)}
              />
            </div>
            {renderFieldIssues('capacity_planning.generator.max_vusers')}
            </fieldset>
              </div>
            </section>

            <section
              id="settings-capacity-panel-reservations"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-reservations"
              hidden={activeCapacitySubsection !== 'reservations'}
            >
              <div className="settings-group">
            <fieldset className="settings-fieldset" disabled={formDisabled}>
            <div className="settings-subsection" data-testid="capacity-reservations">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Reserved Capacity Pools</span>
                  <span className="setting-desc">Reserve generators for a project so other tenants are bounded to the shared pool. Add a time window to make it a timeslot; leave blank for an always-active reservation. Scope a reservation to a fleet pool to draw from that pool's envelope instead of the global inventory.</span>
                </div>
                <strong>{(settings.capacity_planning.reservations ?? []).length}</strong>
              </div>
              <p className={`settings-status-copy ${reservationUsage.overbooked ? 'text-danger' : ''}`} data-testid="reservation-usage-summary">
                {reservationUsage.reserved} of {reservationUsage.configured} generator{reservationUsage.configured === 1 ? '' : 's'} reserved by always-active global reservations — {reservationUsage.overbooked ? `over by ${reservationUsage.reserved - reservationUsage.configured}` : `${reservationUsage.free} free`}.
              </p>
              {(settings.capacity_planning.reservations ?? []).map((reservation, index) => (
                <div key={`reservation-${index}`}>
                  <div className="capacity-reservation-row setting-row">
                    <input
                      className="settings-input"
                      placeholder="Project ID"
                      aria-label="Reservation project"
                      value={reservation.project_id}
                      onChange={event => updateReservation(index, { project_id: event.target.value })}
                    />
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      placeholder="Generators"
                      aria-label="Reserved generators"
                      value={capacityFieldDrafts[`capacity_planning.reservations.${index}.reserved_generators`] ?? String(reservation.reserved_generators)}
                      onChange={event => updateCapacityDraftNumber(`capacity_planning.reservations.${index}.reserved_generators`, event.target.value, parsed => updateReservation(index, { reserved_generators: parsed }))}
                    />
                    <select
                      className="settings-input"
                      aria-label="Reservation pool"
                      value={reservation.pool_id ?? ''}
                      onChange={event => updateReservation(index, { pool_id: event.target.value })}
                    >
                      <option value="">Global envelope</option>
                      {(fleetPools ?? []).map(pool => (
                        <option key={pool.id} value={pool.id}>{pool.name}</option>
                      ))}
                      {reservation.pool_id && !(fleetPools ?? []).some(pool => pool.id === reservation.pool_id) && (
                        <option value={reservation.pool_id}>
                          {fleetPools === null ? `${reservation.pool_id} (pool list unavailable)` : `${reservation.pool_id} (unknown pool)`}
                        </option>
                      )}
                    </select>
                    <input
                      className="settings-input"
                      type="datetime-local"
                      aria-label="Reservation start"
                      value={toDatetimeLocal(reservation.starts_at)}
                      onChange={event => updateReservation(index, { starts_at: fromDatetimeLocal(event.target.value) })}
                    />
                    <input
                      className="settings-input"
                      type="datetime-local"
                      aria-label="Reservation end"
                      value={toDatetimeLocal(reservation.ends_at)}
                      onChange={event => updateReservation(index, { ends_at: fromDatetimeLocal(event.target.value) })}
                    />
                    <button className="btn btn-ghost" type="button" onClick={() => removeReservation(index)}>Remove</button>
                  </div>
                  {renderFieldIssues(`capacity_planning.reservations.${index}.project_id`)}
                  {renderFieldIssues(`capacity_planning.reservations.${index}.reserved_generators`)}
                  {renderFieldIssues(`capacity_planning.reservations.${index}.pool_id`)}
                  {renderFieldIssues(`capacity_planning.reservations.${index}.window`)}
                </div>
              ))}
              {renderFieldIssues('capacity_planning.reservations')}
              <button className="btn btn-secondary" type="button" onClick={addReservation}>Add Reservation</button>
            </div>
            </fieldset>
              </div>
            </section>

            <section
              id="settings-capacity-panel-retention"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-capacity-tab-retention"
              hidden={activeCapacitySubsection !== 'retention'}
            >
              <div className="settings-group">
            <fieldset className="settings-fieldset" disabled={formDisabled}>
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Archive Retention Executor</span>
                  <span className="setting-desc">Plan and apply audited archive pruning through the same retention executor used by the API.</span>
                </div>
                <strong>{archiveRetentionPlan ? archiveRetentionPlan.summary.candidate_count : '-'}</strong>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Retention Enabled</span>
                  <span className="setting-desc">When enabled, dry-run and apply operations evaluate archived runs against the saved retention policy.</span>
                </div>
                <label className="settings-checkbox-label">
                  <input
                    type="checkbox"
                    checked={settings.archive_retention.enabled}
                    onChange={event => updateArchiveRetention('enabled', event.target.checked)}
                  />
                  Enabled
                </label>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Default Retention Days</span>
                  <span className="setting-desc">Archives older than this policy window become prune candidates unless a scoped policy matches first.</span>
                </div>
                <input
                  className="settings-input settings-input-number"
                  type="number"
                  min={1}
                  step={1}
                  value={capacityFieldDrafts['archive_retention.default_retention_days'] ?? String(settings.archive_retention.default_retention_days)}
                  onChange={event => updateArchiveRetention('default_retention_days', event.target.value)}
                />
              </div>
              {renderFieldIssues('archive_retention.default_retention_days')}
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={planArchiveRetention} disabled={!canManageSystem}>
                  Plan Retention
                </button>
                <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmApplyRetention(true)} disabled={!canManageSystem || !archiveRetentionPlan?.summary.candidate_count}>
                  Apply Retention
                </button>
              </div>
              {archiveRetentionActionMessage && <p className="settings-notice" role="status" aria-live="polite">{archiveRetentionActionMessage}</p>}
              {archiveRetentionActionError && <p className="settings-error-inline" role="alert">{archiveRetentionActionError}</p>}
              {archiveRetentionPlan && (
                <div className="settings-release-list">
                  {archiveRetentionPlan.candidates.slice(0, 5).map(candidate => (
                    <div key={`${candidate.test_id}-${candidate.archive_path}`} className="settings-release-row">
                      <div className="settings-release-main">
                        <span>{candidate.test_id}</span>
                        <small>{candidate.policy_name || 'default'} / {candidate.artifact_type} / {candidate.compliance_class || 'standard'} / {candidate.reason || 'expired_by_policy'} / expired {new Date(candidate.expired_at).toLocaleDateString()}</small>
                      </div>
                      <span className="setting-value">{candidate.retention_days} days</span>
                    </div>
                  ))}
                  {archiveRetentionPlan.candidates.length > 5 && (
                    <p className="new-test-inline-note">{archiveRetentionPlan.candidates.length - 5} additional candidate{archiveRetentionPlan.candidates.length - 5 === 1 ? '' : 's'} hidden.</p>
                  )}
                  {archiveRetentionPlan.results?.length ? (
                    <p className="new-test-inline-note">{archiveRetentionPlan.summary.pruned_count} pruned / {archiveRetentionPlan.summary.failed_count} failed.</p>
                  ) : null}
                </div>
              )}
            </div>
            </fieldset>
              </div>
            </section>
          </div>
        </div>

        <div
          id="settings-section-domains"
          className={sectionClass}
          hidden={activeSection !== 'domains'}
          style={{ order: getSectionOrder('domains') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Domains</span>
                  <span className="setting-desc">Select the active tenancy boundary for repository projects and access scopes.</span>
                </div>
                <strong>{domains.length}</strong>
              </div>
              {domainReconciliationWarning && (
                <div className="settings-inline-actions" role="alert">
                  <span className="settings-error-inline">{domainReconciliationWarning.message}</span>
                  <button
                    className="btn btn-secondary btn-sm"
                    type="button"
                    onClick={() => void retryDomainReconciliation()}
                  >
                    Retry
                  </button>
                </div>
              )}
              {domains.length > 0 ? (
                <div className="settings-app-grid">
                  {domains.map(domain => {
                    const projectCount = projectCountByDomain.get(domain.id) || 0
                    return (
                      <button
                        key={domain.id}
                        type="button"
                        className={`settings-app-card ${selectedDomain?.id === domain.id ? 'active' : ''}`}
                        onClick={() => selectDomain(domain)}
                      >
                        <span>{domain.name}</span>
                        <small>{projectCount} project{projectCount === 1 ? '' : 's'}{activeDomain?.id === domain.id ? ' | Active domain' : ''}</small>
                      </button>
                    )
                  })}
                </div>
              ) : (
                <p className="new-test-inline-note">No domains are available.</p>
              )}
              <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto' }}>
                <input
                  className="settings-input"
                  value={newDomainName}
                  onChange={event => {
                    setNewDomainName(event.target.value)
                    setDomainActionError('')
                    setDomainActionMessage('')
                  }}
                  placeholder="Create domain, e.g. Retail"
                  disabled={!canManageDomains}
                />
                <input
                  className="settings-input"
                  value={newDomainDescription}
                  onChange={event => {
                    setNewDomainDescription(event.target.value)
                    setDomainActionError('')
                    setDomainActionMessage('')
                  }}
                  placeholder="Optional description"
                  disabled={!canManageDomains}
                />
                <button className="btn btn-secondary" type="button" onClick={createDomain} disabled={!newDomainName.trim() || !canManageDomains}>
                  Add Domain
                </button>
              </div>
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Selected Domain</span>
                <span className="setting-desc">Rename the selected domain and maintain its administrative description.</span>
              </div>
              {selectedDomain ? (
                <>
                  <input
                    className="settings-input"
                    value={domainDraftName}
                    onChange={event => {
                      setDomainDraftName(event.target.value)
                      setDomainActionError('')
                      setDomainActionMessage('')
                    }}
                    placeholder="Domain name"
                    disabled={!canManageDomains}
                  />
                  <textarea
                    className="settings-input"
                    value={domainDraftDescription}
                    onChange={event => {
                      setDomainDraftDescription(event.target.value)
                      setDomainActionError('')
                      setDomainActionMessage('')
                    }}
                    placeholder="Optional description"
                    rows={3}
                    disabled={!canManageDomains}
                  />
                  {domainDraftValidationError && <p className="settings-error-inline">{domainDraftValidationError}</p>}
                  {!canManageDomains && <p className="settings-error-inline">Current session can view domains but cannot change them.</p>}
                  <div className="settings-inline-actions">
                    <button
                      className="btn btn-primary"
                      type="button"
                      onClick={saveDomain}
                      disabled={!domainHasUnsavedChanges || Boolean(domainDraftValidationError) || !canManageDomains}
                    >
                      Save Domain
                    </button>
                  </div>
                </>
              ) : (
                <p className="new-test-inline-note">Select a domain to edit it.</p>
              )}
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Delete Domain</span>
                <span className="setting-desc">Only non-default domains with no projects can be deleted.</span>
              </div>
              <div className="settings-inline-actions">
                <span className="setting-value">{selectedDomain?.name || 'No domain selected'}</span>
                <button
                  className="btn btn-ghost"
                  style={{ color: 'var(--danger)' }}
                  type="button"
                  onClick={() => selectedDomain && setConfirmDeleteDomain(selectedDomain)}
                  disabled={!selectedDomain || selectedDomain.id === 'default' || selectedDomainProjectCount > 0 || !canManageDomains}
                >
                  Delete
                </button>
              </div>
              {selectedDomain?.id === 'default' && <p className="new-test-inline-note">Default domain cannot be deleted.</p>}
              {selectedDomain && selectedDomain.id !== 'default' && selectedDomainProjectCount > 0 && (
                <p className="new-test-inline-note">
                  {selectedDomainProjectCount} project{selectedDomainProjectCount === 1 ? '' : 's'} must be moved or deleted first.
                </p>
              )}
              {confirmDeleteDomain && (
                <div className="settings-inline-actions">
                  <span className="settings-error-inline">Delete {confirmDeleteDomain.name}?</span>
                  <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => deleteDomain(confirmDeleteDomain)}>
                    Confirm Delete
                  </button>
                  <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteDomain(null)}>
                    Cancel
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

        <div
          id="settings-section-applications"
          className={sectionClass}
          hidden={activeSection !== 'applications'}
          style={{ order: getSectionOrder('applications') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Applications</span>
                  <span className="setting-desc">Select one to manage its releases and rename or remove it.</span>
                </div>
                <strong>{applications.length}</strong>
              </div>
              <div className="settings-app-grid">
                {applications.map(application => (
                  <button
                    key={application.id}
                    type="button"
                    className={`settings-app-card ${activeApplication?.id === application.id ? 'active' : ''}`}
                    onClick={() => selectApplication(application)}
                    disabled={Boolean(renamingApplicationId && renamingApplicationId !== application.id)}
                  >
                  <span>{application.name}</span>
                  <small>{application.id === activeApplication?.id ? 'Current application' : 'Switch context'}</small>
                </button>
              ))}
              </div>
              {applicationsLoading && <p className="new-test-inline-note" role="status">Loading applications...</p>}
              {(applicationReconciliationWarning || applicationsError) && (
                <div className="settings-inline-actions" role="alert">
                  <span className="settings-error-inline">{applicationReconciliationWarning?.message || applicationsError}</span>
                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => void retryApplicationReconciliation()}>Retry</button>
                </div>
              )}
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={newApplicationName}
                  onChange={event => {
                    const next = event.target.value
                    if (!newApplicationName.trim() && next.trim()) setNewApplicationProjectId(activeProject?.id || '')
                    if (!next.trim()) setNewApplicationProjectId('')
                    setNewApplicationName(next)
                  }}
                  placeholder="Create application, e.g. ACC"
                />
                <button className="btn btn-secondary" type="button" onClick={createApplication} disabled={!newApplicationName.trim()}>
                  Add Application
                </button>
              </div>
            </div>

            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Current Application</span>
                <span className="setting-desc">Rename or delete the selected application.</span>
              </div>
              <div className="settings-inline-actions">
                {renamingApplicationName !== null ? (
                  <>
                    <input
                      type="text"
                      className="input"
                      value={renamingApplicationName}
                      onChange={e => setRenamingApplicationName(e.target.value)}
                      onKeyDown={e => {
                        if (e.key === 'Enter') confirmRenameApplication()
                        if (e.key === 'Escape') {
                          setRenamingApplicationId(null)
                          setRenamingApplicationName(null)
                          setRenamingApplicationOriginalName(null)
                        }
                      }}
                    />
                    <button className="btn btn-primary" type="button" onClick={confirmRenameApplication}>Save</button>
                    <button className="btn btn-secondary" type="button" onClick={() => { setRenamingApplicationId(null); setRenamingApplicationName(null); setRenamingApplicationOriginalName(null) }}>Cancel</button>
                  </>
                ) : (
                  <>
                    <span className="setting-value">{activeApplication?.name || 'No application selected'}</span>
                    <button className="btn btn-secondary" type="button" onClick={startRenamingApplication} disabled={!activeApplication}>
                      Rename
                    </button>
                    <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeleteApp(true)} disabled={!activeApplication}>
                      Delete
                    </button>
                  </>
                )}
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Releases</span>
                  <span className="setting-desc">These belong to {activeApplication?.name || 'the selected application'}. Create new releases from the New Test page.</span>
                </div>
                <strong>{managedReleases.length}</strong>
              </div>
              {managedReleases.length > 0 ? (
                <div className="settings-release-list">
                  {managedReleases.map(release => (
                    <div key={release.id} className={`settings-release-row ${!release.archived_at && activeRelease?.id === release.id ? 'active' : ''}`}>
                      <button
                        type="button"
                        className="settings-release-main"
                        onClick={() => selectRelease(release)}
                        disabled={Boolean(release.archived_at)}
                      >
                        <span>{release.name}</span>
                        <small>
                          {release.archived_at
                            ? `Archived ${new Date(release.archived_at).toLocaleDateString()}`
                            : activeRelease?.id === release.id
                              ? 'Current release'
                              : 'Set active release'}
                        </small>
                      </button>
                      <div className="settings-inline-actions">
                        {renamingReleaseId === release.id ? (
                          <>
                            <input
                              type="text"
                              className="input"
                              value={renamingReleaseName}
                              onChange={e => setRenamingReleaseName(e.target.value)}
                              onKeyDown={e => { if (e.key === 'Enter') confirmRenameRelease(); if (e.key === 'Escape') { setRenamingReleaseId(null); setRenamingReleaseName(''); setRenamingReleaseOriginalName(null); setRenamingReleaseApplicationId(null); } }}
                            />
                            <button className="btn btn-primary" type="button" onClick={confirmRenameRelease}>Save</button>
                            <button className="btn btn-secondary" type="button" onClick={() => { setRenamingReleaseId(null); setRenamingReleaseName(''); setRenamingReleaseOriginalName(null); setRenamingReleaseApplicationId(null); }}>Cancel</button>
                          </>
                        ) : (
                          <>
                            <button className="btn btn-secondary" type="button" onClick={() => runGuarded(() => startRenamingRelease(release.id, release.name, release.application_id))}>
                              Rename
                            </button>
                            {!release.archived_at && (
                              <button className="btn btn-secondary" type="button" onClick={() => setConfirmArchiveRelease({ id: release.id, name: release.name })}>
                                Archive
                              </button>
                            )}
                            <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => setConfirmDeleteRelease({ id: release.id, name: release.name })}>
                              Delete
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No releases yet for this application.</p>
              )}
            </div>
          </div>
        </div>

        <div
          id="settings-section-projects"
          className={sectionClass}
          hidden={activeSection !== 'projects'}
          style={{ order: getSectionOrder('projects') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Project settings categories',
              'settings-projects',
              PROJECT_SETTINGS_SUBSECTIONS,
              activeProjectSubsection,
              setActiveProjectSubsection,
              projectSubsectionSummaries,
            )}
            <section
              id="settings-projects-panel-catalog"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-projects-tab-catalog"
              hidden={activeProjectSubsection !== 'catalog'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Projects</span>
                  <span className="setting-desc">Compliance defaults apply automatically to tests and replay sessions that resolve this project by `project_id`.</span>
                </div>
                <strong>{projects.length}</strong>
              </div>
              {projects.length > 0 ? (
                <div className="settings-app-grid">
                  {projects.map(project => (
                    <button
                      key={project.id}
                      type="button"
                      className={`settings-app-card ${selectedProject?.id === project.id ? 'active' : ''}`}
                      onClick={() => selectProject(project)}
                    >
                      <span>{project.name}</span>
                      <small>
                        {selectedProject?.id === project.id
                          ? 'Editing compliance defaults'
                          : `${project.settings?.redaction_rules?.length || 0} custom rule${project.settings?.redaction_rules?.length === 1 ? '' : 's'}`}
                      </small>
                    </button>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No projects yet. Create one to define project-specific redaction defaults.</p>
              )}
              <div className="inline-create-row">
                <input
                  className="settings-input"
                  value={newProjectName}
                  onChange={event => {
                    const next = event.target.value
                    if (!newProjectName.trim() && next.trim()) setNewProjectDomainId(selectedDomain?.id || '')
                    if (!next.trim()) setNewProjectDomainId('')
                    setNewProjectName(next)
                  }}
                  placeholder="Create project, e.g. Customer Portal"
                />
                <button className="btn btn-secondary" type="button" onClick={createProject} disabled={!newProjectName.trim() || !canManageProjects}>
                  Add Project
                </button>
              </div>
            </div>
              </div>
            </section>

            {selectedProject ? (
              <>
                <section
                  id="settings-projects-panel-limits"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-limits"
                  hidden={activeProjectSubsection !== 'limits'}
                >
                  <div className="settings-group">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Selected Project</span>
                    <span className="setting-desc">Built-in token and PII scrubbing still runs globally. Add regex rules here only for project-specific payload shapes.</span>
                  </div>
                  <input
                    className="settings-input"
                    value={projectDraftName}
                    onChange={event => {
                      setProjectDraftName(event.target.value)
                      setProjectActionError('')
                      setProjectActionMessage('')
                    }}
                    placeholder="Project name"
                  />
                </div>

                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Project Limits</span>
                      <span className="setting-desc">Optional project-level caps that reject over-sized runs before execution starts.</span>
                    </div>
                    <strong>{selectedProject.name}</strong>
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max Concurrent Tests</span>
                      <span className="setting-desc">Leave blank or set to 0 for no project-level concurrency cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxConcurrentTests}
                      onChange={event => {
                        setProjectDraftMaxConcurrentTests(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max VUsers</span>
                      <span className="setting-desc">Leave blank or set to 0 for no project-level VUser cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxVUsers}
                      onChange={event => {
                        setProjectDraftMaxVUsers(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max VUsers Per Test</span>
                      <span className="setting-desc">Reject any single test requesting more than this many VUsers. Blank or 0 = no per-test cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxVUsersPerTest}
                      onChange={event => {
                        setProjectDraftMaxVUsersPerTest(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Project Max Test Duration (minutes)</span>
                      <span className="setting-desc">Reject any single test longer than this many minutes. Blank or 0 = no duration cap.</span>
                    </div>
                    <input
                      className="settings-input"
                      inputMode="numeric"
                      value={projectDraftMaxTestDurationMinutes}
                      onChange={event => {
                        setProjectDraftMaxTestDurationMinutes(event.target.value)
                        setProjectActionError('')
                        setProjectActionMessage('')
                      }}
                      placeholder="Unlimited"
                    />
                  </div>

                  {selectedProject && (
                    <ProjectPoolQuotasCard
                      ref={poolQuotaDraftRef}
                      project={selectedProject}
                      canManage={canManageProjects}
                      onUpdate={updateProject}
                      onDirtyChange={setPoolQuotaDraftDirty}
                      onUpdated={updated => {
                        commitSettingsProject(updated)
                      }}
                    />
                  )}
                </div>
                  </div>
                </section>

                <section
                  id="settings-projects-panel-github"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-github"
                  hidden={activeProjectSubsection !== 'github'}
                >
                  <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary" style={{ marginTop: 16 }}>
                    <div>
                      <span className="setting-label">GitHub Project Policy</span>
                      <span className="setting-desc">Repository, branch, path, and automation rules enforced by the controller for GitHub sync actions in this project.</span>
                    </div>
                    <span className={`script-health-token ${projectDraftGitHubEnabled ? 'healthy' : ''}`}>
                      {projectDraftGitHubEnabled ? 'enabled' : 'disabled'}
                    </span>
                  </div>
                  <div className="project-redaction-rule-grid">
                    <label className="project-redaction-rule-field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>
                      <input
                        type="checkbox"
                        checked={projectDraftGitHubEnabled}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubEnabled(event.target.checked)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                      />
                      <span>Enable policy</span>
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Allowed Repositories</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubAllowedRepos}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAllowedRepos(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="org/perf-scripts, org/team-*"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Allowed Branches</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubAllowedBranches}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAllowedBranches(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="main, release/*"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Path Template</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubPathTemplate}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubPathTemplate(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="apexload/scripts/{project}/{ref_key}.json"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Repository</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubDefaultRepo}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubDefaultRepo(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="org/perf-scripts"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Branch</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubDefaultBranch}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubDefaultBranch(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="main"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Default Sync Folder</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubSyncDirectory}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubSyncDirectory(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="apexload/scripts"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>Automation Identity</span>
                      <input
                        className="settings-input"
                        value={projectDraftGitHubAutomationIdentity}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAutomationIdentity(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="apexload-bot"
                      />
                    </label>
                    <label className="project-redaction-rule-field">
                      <span>App Installation ID</span>
                      <input
                        className="settings-input"
                        inputMode="numeric"
                        value={projectDraftGitHubAppInstallation}
                        disabled={!canManageProjects}
                        onChange={event => {
                          setProjectDraftGitHubAppInstallation(event.target.value)
                          setProjectActionError('')
                          setProjectActionMessage('')
                        }}
                        placeholder="12345678"
                      />
                    </label>
                  </div>
                  <div className="settings-inline-actions" style={{ flexWrap: 'wrap', marginTop: 8 }}>
                    {[
                      ['Require PR', projectDraftGitHubRequirePR, setProjectDraftGitHubRequirePR],
                      ['Allow direct commit', projectDraftGitHubAllowDirect, setProjectDraftGitHubAllowDirect],
                      ['Allow import', projectDraftGitHubAllowImport, setProjectDraftGitHubAllowImport],
                      ['Allow push', projectDraftGitHubAllowPush, setProjectDraftGitHubAllowPush],
                      ['Bulk sync', projectDraftGitHubBulkSync, setProjectDraftGitHubBulkSync],
                      ['Webhook auto-sync', projectDraftGitHubAutoSync, setProjectDraftGitHubAutoSync],
                      ['Publish checks', projectDraftGitHubChecks, setProjectDraftGitHubChecks],
                    ].map(([label, checked, setter]) => (
                      <label key={label as string} className="project-redaction-rule-field" style={{ flexDirection: 'row', alignItems: 'center', gap: 8, minWidth: 150 }}>
                        <input
                          type="checkbox"
                          checked={checked as boolean}
                          disabled={!canManageProjects}
                          onChange={event => {
                            const applyChecked = setter as Dispatch<SetStateAction<boolean>>
                            applyChecked(event.target.checked)
                            setProjectActionError('')
                            setProjectActionMessage('')
                          }}
                        />
                        <span>{label as string}</span>
                      </label>
                    ))}
                  </div>
                </div>
                  </div>
                </section>

                <section
                  id="settings-projects-panel-redaction"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-redaction"
                  hidden={activeProjectSubsection !== 'redaction'}
                >
                  <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Custom Redaction Rules</span>
                      <span className="setting-desc">Each regex runs after the built-in scrubbing pass. Leave replacement empty to fall back to the engine default `[REDACTED]` token.</span>
                    </div>
                    <strong>{customRuleCount}</strong>
                  </div>
                  {projectDraftRules.length > 0 ? (
                    <div className="project-redaction-rule-list">
                      {projectDraftRules.map((rule, index) => (
                        <div key={`${selectedProject.id}-rule-${index}`} className="project-redaction-rule-card">
                          <div className="project-redaction-rule-header">
                            <strong>Rule {index + 1}</strong>
                            <button className="btn btn-ghost" type="button" onClick={() => removeProjectRule(index)}>
                              Remove
                            </button>
                          </div>
                          <div className="project-redaction-rule-grid">
                            <label className="project-redaction-rule-field">
                              <span>Name</span>
                              <input
                                className="settings-input"
                                value={rule.name}
                                onChange={event => updateProjectRule(index, 'name', event.target.value)}
                                placeholder="Optional label"
                              />
                            </label>
                            <label className="project-redaction-rule-field">
                              <span>Replacement</span>
                              <input
                                className="settings-input"
                                value={rule.replacement}
                                onChange={event => updateProjectRule(index, 'replacement', event.target.value)}
                                placeholder="Defaults to [REDACTED]"
                              />
                            </label>
                          </div>
                          <label className="project-redaction-rule-field">
                            <span>Regex Pattern</span>
                            <textarea
                              className="settings-input project-redaction-rule-pattern"
                              rows={3}
                              value={rule.pattern}
                              onChange={event => updateProjectRule(index, 'pattern', event.target.value)}
                              placeholder="customer-[0-9]+"
                            />
                          </label>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="new-test-inline-note">No custom rules yet. Built-in header, token, SSN, MRN, and high-entropy redaction still applies.</p>
                  )}

                  <div className="settings-inline-actions">
                    <button className="btn btn-secondary" type="button" onClick={addProjectRule} disabled={!canManageProjects}>
                      Add Rule
                    </button>
                  </div>
                </div>
                  </div>
                </section>

                <section
                  id="settings-projects-panel-secrets"
                  className="settings-subpanel"
                  role="tabpanel"
                  aria-labelledby="settings-projects-tab-secrets"
                  hidden={activeProjectSubsection !== 'secrets'}
                >
                  <ProjectSecretsPanel
                    projectId={selectedProject.id}
                    projectName={selectedProject.name}
                    canCreate={canManageScripts || canManageProjects || canManageSystem}
                    canRotate={canManageProjects || canManageSystem}
                    onCountChange={setProjectSecretCount}
                  />
                </section>

                {activeProjectSubsection !== 'secrets' && activeProjectSubsection !== 'catalog' && (
                  <div className="settings-subsection settings-subsection-actions">
                    {projectDraftValidationError && <p className="settings-error-inline">{projectDraftValidationError}</p>}
                    {!canManageProjects && <p className="settings-error-inline">Current session can view this project but cannot change project governance.</p>}
                    <div className="settings-inline-actions">
                      <button
                        className="btn btn-primary"
                        type="button"
                        onClick={saveProjectCompliance}
                        disabled={!projectHasUnsavedChanges || Boolean(projectDraftValidationError) || !canManageProjects}
                      >
                        Save Project Governance
                      </button>
                    </div>
                  </div>
                )}
              </>
            ) : (
              <div className="settings-subsection settings-empty-subpanel">
                <p className="new-test-inline-note">
                  {missingRequestedProjectId
                    ? 'The requested Project Secrets target is unavailable. No fallback project was selected.'
                    : 'Select a project to edit its compliance defaults.'}
                </p>
                {missingRequestedProjectId && projects.length > 0 && (
                  <button className="btn btn-secondary" type="button" onClick={() => setActiveProjectSubsection('catalog')}>
                    Choose an available project
                  </button>
                )}
              </div>
            )}
          </div>
        </div>

        <div
          id="settings-section-data-pools"
          className={sectionClass}
          hidden={activeSection !== 'data-pools'}
          style={{ order: getSectionOrder('data-pools') }}
        >
          <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Create Data Pool</span>
                  <span className="setting-desc">Create CRUD data pools that scenarios can bind by alias and share across scripts.</span>
                </div>
                <strong>{dataPools.length}</strong>
              </div>
              <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto' }}>
                <input
                  className="settings-input"
                  value={dataPoolName}
                  disabled={dataPoolCatalogMutationBusy}
                  onChange={event => {
                    const next = event.target.value
                    if (!dataPoolName.trim() && !dataPoolDescription.trim() && next.trim()) setDataPoolProjectId(activeProject?.id || '')
                    if (!next.trim() && !dataPoolDescription.trim()) setDataPoolProjectId('')
                    setDataPoolName(next)
                  }}
                  placeholder="Pool name, e.g. Customer Accounts"
                />
                <input
                  className="settings-input"
                  value={dataPoolDescription}
                  disabled={dataPoolCatalogMutationBusy}
                  onChange={event => {
                    const next = event.target.value
                    if (!dataPoolName.trim() && !dataPoolDescription.trim() && next.trim()) setDataPoolProjectId(activeProject?.id || '')
                    if (!next.trim() && !dataPoolName.trim()) setDataPoolProjectId('')
                    setDataPoolDescription(next)
                  }}
                  placeholder="Optional description"
                />
                <button className="btn btn-secondary btn-sm" type="button" onClick={createDataPool} disabled={!dataPoolName.trim() || dataPoolCatalogMutationBusy}>
                  Add Pool
                </button>
              </div>
            </div>

            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Data Pools</span>
                  <span className="setting-desc">Select a pool to inspect and edit its rows.</span>
                </div>
                <strong>
                  {selectedDataPoolId
                    ? (dataPoolRowsTotal > dataPoolRows.length
                      ? `showing ${dataPoolRows.length} of ${dataPoolRowsTotal} rows`
                      : `${dataPoolRowsTotal} rows`)
                    : 'No pool selected'}
                </strong>
              </div>
              {dataPools.length > 0 ? (
                <div className="settings-release-list">
                  {dataPools.map(pool => (
                    <div key={pool.id} className={`settings-release-row ${selectedDataPoolId === pool.id ? 'active' : ''}`}>
                      <button
                        type="button"
                        className="settings-release-main"
                        onClick={() => {
                          setDataPoolActionNotice('')
                          selectedDataPoolIdRef.current = pool.id
                          setSelectedDataPoolId(pool.id)
                        }}
                      >
                        <span>{pool.name}</span>
                        <small>{pool.description || `Updated ${new Date(pool.updated_at).toLocaleDateString()}`}</small>
                      </button>
                      <div className="settings-inline-actions">
                        <button className="btn btn-ghost" style={{ color: 'var(--danger)' }} type="button" disabled={dataPoolCatalogMutationBusy} onClick={() => setConfirmDeletePool(pool)}>
                          Delete
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No data pools have been created yet.</p>
              )}
            </div>

            {dataPoolActionNotice && <p className="settings-notice" role="status" aria-live="polite">{dataPoolActionNotice}</p>}

            {selectedDataPoolId && (
              <div className="settings-subsection">
                <div className="settings-manager-summary">
                  <div>
                    <span className="setting-label">{editingDataPoolRowId ? 'Edit Row' : 'Add Row'}</span>
                    <span className="setting-desc">Use JSON object keys as parameter names, for example customer_id, account_id, or zip.</span>
                  </div>
                  <strong>{editingDataPoolRowId ? 'Editing' : 'New'}</strong>
                </div>
                <textarea
                  className="settings-input cell-mono"
                  style={{ minHeight: 120, resize: 'vertical' }}
                  value={dataPoolRowJson}
                  onChange={event => setDataPoolRowJson(event.target.value)}
                />
                <div className="settings-inline-actions">
                  <button className="btn btn-primary" type="button" disabled={dataPoolRowSaveBusy || dataPoolRowDeleteBusy} onClick={saveDataPoolRow}>
                    {dataPoolRowSaveBusy ? 'Saving…' : editingDataPoolRowId ? 'Update Row' : 'Add Row'}
                  </button>
                  {editingDataPoolRowId && (
                    <button className="btn btn-secondary" type="button" disabled={dataPoolRowDeleteBusy} onClick={() => { setEditingDataPoolRowId(null); setEditingDataPoolRowVersion(null); setDataPoolRowJson('{\n  "customer_id": "C-10001"\n}') }}>
                      Cancel Edit
                    </button>
                  )}
                  <input
                    ref={dataPoolImportInputRef}
                    type="file"
                    accept=".csv,.dat,.txt,.tsv"
                    style={{ display: 'none' }}
                    aria-label="Data pool CSV file"
                    onChange={event => {
                      const file = event.target.files?.[0]
                      if (file) void beginDataPoolImport(file)
                      event.target.value = ''
                    }}
                  />
                  <button className="btn btn-secondary" type="button" disabled={dataPoolImportBusy} onClick={() => dataPoolImportInputRef.current?.click()}>
                    {dataPoolImportBusy && !dataPoolImportDraft ? 'Reading…' : 'Import CSV…'}
                  </button>
                  <button className="btn btn-secondary" type="button" disabled={dataPoolExportBusy || dataPoolRowsTotal === 0} onClick={() => { void exportDataPool() }}>
                    {dataPoolExportBusy ? 'Exporting…' : 'Export CSV'}
                  </button>
                </div>
                {dataPoolImportDraft && dataPoolImportDraft.poolId === selectedDataPoolId && (
                  <div className="settings-subsection" style={{ marginTop: 12 }}>
                    <div className="settings-manager-summary">
                      <div>
                        <span className="setting-label">Import Preview</span>
                        <span className="setting-desc" role="status">
                          {dataPoolImportDraft.file.name} — {dataPoolImportDraft.preview.row_count ?? 0} row(s), columns: {(dataPoolImportDraft.preview.columns || []).join(', ') || 'none'}
                        </span>
                      </div>
                      <strong>Dry run</strong>
                    </div>
                    {(dataPoolImportDraft.preview.warnings || []).map(warning => (
                      <p key={warning} className="new-test-inline-note">{warning}</p>
                    ))}
                    {dataPoolImportMode === 'replace' && dataPoolRowsTotal > 0 && (
                      <p className="new-test-inline-note" role="alert">
                        Replace will first delete the {dataPoolRowsTotal} existing row(s) in this pool.
                      </p>
                    )}
                    {dataPoolImportDraft.preview.append_warning && (
                      <p className="new-test-inline-note" role="alert">{dataPoolImportDraft.preview.append_warning}</p>
                    )}
                    {(dataPoolImportDraft.preview.sample_rows || []).length > 0 && (
                      <div className="script-parameter-table-wrap">
                        <table className="script-parameter-table">
                          <thead>
                            <tr>
                              {(dataPoolImportDraft.preview.columns || []).map(column => <th key={column}>{column}</th>)}
                            </tr>
                          </thead>
                          <tbody>
                            {(dataPoolImportDraft.preview.sample_rows || []).map((sample, index) => (
                              <tr key={index}>
                                {(dataPoolImportDraft.preview.columns || []).map(column => (
                                  <td key={column} className="cell-mono">{sample[column]}</td>
                                ))}
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                    <div className="settings-inline-actions" style={{ marginTop: 8 }}>
                      <select
                        className="settings-input"
                        value={dataPoolImportMode}
                        disabled={dataPoolImportBusy}
                        onChange={event => setDataPoolImportMode(event.target.value === 'replace' ? 'replace' : 'append')}
                        aria-label="Import mode"
                      >
                        <option value="append">Append to existing rows</option>
                        <option value="replace">Replace all rows</option>
                      </select>
                      <button className="btn btn-primary" type="button" disabled={dataPoolImportBusy} onClick={() => { void confirmDataPoolImport() }}>
                        {dataPoolImportBusy
                          ? 'Importing…'
                          : dataPoolImportMode === 'replace'
                            ? `Replace With ${dataPoolImportDraft.preview.row_count ?? 0} Row(s)`
                            : `Append ${dataPoolImportDraft.preview.row_count ?? 0} Row(s)`}
                      </button>
                      <button className="btn btn-secondary" type="button" disabled={dataPoolImportBusy} onClick={cancelDataPoolImport}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}

                {dataPoolRows.length > 0 ? (
                  <div className="script-parameter-table-wrap" style={{ marginTop: 16 }}>
                    <table className="script-parameter-table">
                      <thead>
                        <tr>
                          <th>Data</th>
                          <th>Status</th>
                          <th>Version</th>
                          <th>Updated</th>
                          <th>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {dataPoolRows.map(row => (
                          <tr key={row.id}>
                            <td className="cell-mono">{JSON.stringify(row.data)}</td>
                            <td>{row.status || 'active'}</td>
                            <td>{row.version}</td>
                            <td>{new Date(row.updated_at).toLocaleString()}</td>
                        <td aria-label="Data pool actions">
                              <div className="settings-inline-actions">
                                <button className="btn btn-sm btn-secondary" type="button" disabled={dataPoolRowSaveBusy || dataPoolRowDeleteBusy} onClick={() => editDataPoolRow(row)}>
                                  Edit
                                </button>
                                <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} type="button" disabled={dataPoolRowSaveBusy || dataPoolRowDeleteBusy} onClick={() => setConfirmDeleteRow(row)}>
                                  Delete
                                </button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {dataPoolRowsHasMore && dataPoolRowsNextCursor && (
                      <div className="settings-inline-actions" style={{ marginTop: 12 }}>
                        <button
                          className="btn btn-secondary"
                          type="button"
                          disabled={dataPoolRowsLoadBusy}
                          onClick={() => { void loadMoreDataPoolRows() }}
                        >
                          {dataPoolRowsLoadBusy
                            ? 'Loading rows…'
                            : `Load more rows (${Math.max(dataPoolRowsTotal - dataPoolRows.length, 0)} remaining)`}
                        </button>
                      </div>
                    )}
                  </div>
                ) : (
                  <p className="new-test-inline-note">No rows in this data pool yet.</p>
                )}
              </div>
            )}
          </div>
        </div>

        <div
          id="settings-section-integrations"
          className={sectionClass}
          hidden={activeSection !== 'integrations'}
          style={{ order: getSectionOrder('integrations') }}
        >
          <div className="settings-subsection-layout">
            {renderSettingsSubnav(
              'Integration settings categories',
              'settings-integrations',
              INTEGRATION_SETTINGS_SUBSECTIONS,
              activeIntegrationSubsection,
              setActiveIntegrationSubsection,
              integrationSubsectionSummaries,
            )}
            <section
              id="settings-integrations-panel-library"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-library"
              hidden={activeIntegrationSubsection !== 'library'}
            >
              <div className="settings-group">
            <LibraryFederationPanel
              projectId={activeProject?.id}
              applicationId={activeApplication?.id}
              applicationName={activeApplication?.name}
              releaseId={activeRelease?.id}
              releaseName={activeRelease?.name}
            />
              </div>
            </section>

            <section
              id="settings-integrations-panel-github"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-github"
              hidden={activeIntegrationSubsection !== 'github'}
            >
              <div className="settings-group">
            <GitHubIntegrationPanel
              value={settings.github}
              onChange={(next) => {
                setSettings((prev) => ({ ...prev, github: next }))
                setSaveMessage('')
                setRequestError('')
              }}
            />
              </div>
            </section>

            <section
              id="settings-integrations-panel-prometheus"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-prometheus"
              hidden={activeIntegrationSubsection !== 'prometheus'}
            >
              <div className="settings-group">
                <div className="settings-subsection">
                  <div className="settings-manager-summary">
                    <div>
                      <span className="setting-label">Prometheus Graphs</span>
                      <span className="setting-desc">Proxy the configured Prometheus server through the backend for the curated APEX runtime graph pack.</span>
                    </div>
                    <strong>{settings.prometheus.enabled ? 'Enabled' : 'Disabled'}</strong>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Enable Prometheus Graphs</span>
                      <span className="setting-desc">Shows the Prometheus graph category when the server URL is configured.</span>
                    </div>
                    <label className="settings-checkbox-label">
                      <input
                        type="checkbox"
                        checked={settings.prometheus.enabled}
                        onChange={event => updatePrometheus({ enabled: event.target.checked })}
                      />
                      <span>Enabled</span>
                    </label>
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Server URL</span>
                      <span className="setting-desc">Backend-only Prometheus HTTP API base URL.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.prometheus.base_url}
                      onChange={event => updatePrometheus({ base_url: event.target.value })}
                      placeholder="http://prometheus:9090"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Timeout (seconds)</span>
                      <span className="setting-desc">Maximum backend wait for each Prometheus query.</span>
                    </div>
                    <input
                      className="settings-input settings-input-number"
                      type="number"
                      min={1}
                      max={60}
                      step={1}
                      value={settings.prometheus.timeout_seconds}
                      onChange={event => updatePrometheus({ timeout_seconds: Number(event.target.value) })}
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Bearer token environment variable</span>
                      <span className="setting-desc">Name of the controller environment variable containing the Prometheus bearer token. The token itself is never stored.</span>
                    </div>
                    <input
                      className="settings-input cell-mono"
                      value={settings.prometheus.bearer_token_env_var || ''}
                      onChange={event => updatePrometheus({ bearer_token_env_var: event.target.value })}
                      placeholder="APEXLOAD_PROMETHEUS_TOKEN"
                    />
                  </div>
                  <div className="setting-row">
                    <div className="setting-info">
                      <span className="setting-label">Resolve label_values() options</span>
                      <span className="setting-desc">Opt in to bounded, run-scoped datasource queries that populate variable pick-lists during import.</span>
                    </div>
                    <label className="settings-checkbox-label">
                      <input type="checkbox" checked={settings.prometheus.allow_label_values} onChange={event => updatePrometheus({ allow_label_values: event.target.checked })} />
                      <span>Allowed</span>
                    </label>
                  </div>
                  <div className="settings-manager-summary">
                    <div><span className="setting-label">Grafana datasource proxy</span><span className="setting-desc">Route imported panels through Grafana using their datasource UIDs.</span></div>
                    <strong>{settings.grafana?.enabled ? 'Enabled' : 'Disabled'}</strong>
                  </div>
                  <div className="setting-row"><div className="setting-info"><span className="setting-label">Enable Grafana proxy</span></div><label className="settings-checkbox-label"><input type="checkbox" checked={Boolean(settings.grafana?.enabled)} onChange={event => updateGrafana({ enabled: event.target.checked })} /><span>Enabled</span></label></div>
                  <div className="setting-row"><div className="setting-info"><span className="setting-label">Grafana server URL</span></div><input className="settings-input cell-mono" aria-label="Grafana server URL" value={settings.grafana?.base_url || ''} onChange={event => updateGrafana({ base_url: event.target.value })} placeholder="https://grafana.example.com" /></div>
                  <div className="setting-row"><div className="setting-info"><span className="setting-label">Service-account token environment variable</span></div><input className="settings-input cell-mono" aria-label="Grafana service-account token environment variable" value={settings.grafana?.service_account_token_env_var || ''} onChange={event => updateGrafana({ service_account_token_env_var: event.target.value })} placeholder="APEXLOAD_GRAFANA_TOKEN" /></div>
                  <div className="setting-row"><div className="setting-info"><span className="setting-label">Grafana timeout (seconds)</span></div><input className="settings-input settings-input-number" aria-label="Grafana timeout seconds" type="number" min={1} max={60} step={1} value={settings.grafana?.timeout_seconds || 10} onChange={event => updateGrafana({ timeout_seconds: Number(event.target.value) })} /></div>
                </div>
              </div>
            </section>

            <section
              id="settings-integrations-panel-dashboards"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-dashboards"
              hidden={activeIntegrationSubsection !== 'dashboards'}
            >
              <ImportedDashboardsPanel
                projects={projects}
                defaultProjectId={selectedProjectId}
                prometheusConfigured={(settings.grafana?.enabled && Boolean(settings.grafana.base_url.trim()) && Boolean(settings.grafana.service_account_token_env_var.trim())) || (settings.prometheus.enabled && settings.prometheus.base_url.trim() !== '')}
              />
            </section>

            <section
              id="settings-integrations-panel-powerbi"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-powerbi"
              hidden={activeIntegrationSubsection !== 'powerbi'}
            >
              <div className="settings-group">
                <PowerBIIntegrationPanel
                  value={settings.powerbi || { enabled: false }}
                  onChange={(next) => {
                    setSettings((prev) => ({ ...prev, powerbi: next }))
                    setSaveMessage('')
                    setRequestError('')
                  }}
                />
              </div>
            </section>

            <section
              id="settings-integrations-panel-notifications"
              className="settings-subpanel"
              role="tabpanel"
              aria-labelledby="settings-integrations-tab-notifications"
              hidden={activeIntegrationSubsection !== 'notifications'}
            >
              <div className="settings-group">
            <div className="settings-subsection">
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Notification Webhooks</span>
                  <span className="setting-desc">Send Slack, Microsoft Teams, or Dynatrace Events API notifications when tests or telemetry health change state.</span>
                </div>
                <strong>{notificationWebhookCount}</strong>
              </div>

              {settings.notifications.webhooks.length > 0 ? (
                <div className="settings-notification-webhook-list">
                  {settings.notifications.webhooks.map((webhook, index) => (
                    <div className="settings-notification-webhook-card" key={`${webhook.url}-${index}`}>
                      <div className="project-redaction-rule-header">
                        <div>
                          <strong>{webhook.name?.trim() || `Webhook ${index + 1}`}</strong>
                          <p className="setting-desc">All test lifecycle and telemetry health events are sent. Event filtering is supported by the backend and can be added to the UI later.</p>
                        </div>
                        <button className="btn btn-sm btn-ghost" type="button" onClick={() => removeNotificationWebhook(index)}>
                          Remove
                        </button>
                      </div>

                      <div className="project-redaction-rule-grid">
                        <label className="project-redaction-rule-field">
                          <span>Webhook Name</span>
                          <input
                            className="settings-input"
                            value={webhook.name || ''}
                            onChange={event => updateNotificationWebhook(index, { name: event.target.value })}
                            placeholder="Perf Alerts"
                          />
                        </label>
                        <label className="project-redaction-rule-field">
                          <span>Format</span>
                          <select
                            className="settings-select"
                            value={webhook.format}
                            onChange={event => updateNotificationWebhook(index, { format: event.target.value as DashboardSystemSettings['notifications']['webhooks'][number]['format'] })}
                          >
                            {NOTIFICATION_FORMAT_OPTIONS.map(option => (
                              <option key={option.value} value={option.value}>{option.label}</option>
                            ))}
                          </select>
                        </label>
                      </div>

                      <label className="project-redaction-rule-field">
                        <span>Destination URL</span>
                        <input
                          className="settings-input"
                          value={webhook.url}
                          onChange={event => updateNotificationWebhook(index, { url: event.target.value })}
                          placeholder={webhook.format === 'dynatrace' ? 'https://{environment}.live.dynatrace.com/api/v2/events/ingest' : 'https://hooks.slack.com/services/...'}
                        />
                      </label>

                      {webhook.format === 'dynatrace' && (
                        <div className="project-redaction-rule-grid">
                          <label className="project-redaction-rule-field">
                            <span>Dynatrace API Token</span>
	                            <input
	                              className="settings-input"
	                              type="password"
	                              value={webhook.api_token || ''}
	                              onChange={event => updateNotificationWebhook(index, { api_token: event.target.value, has_api_token: webhook.has_api_token })}
	                              placeholder={webhook.has_api_token ? 'Configured; enter a new token to rotate' : 'dt0c01...'}
	                            />
	                            <span>{webhook.has_api_token ? 'A token is configured and hidden. Enter a new value only when rotating it.' : 'Token is write-only and will not be shown after saving.'}</span>
	                            {webhook.api_token && (
	                              <button
	                                className="btn btn-sm btn-ghost"
	                                type="button"
	                                onClick={() => updateNotificationWebhook(index, { api_token: '' })}
	                              >
	                                Clear Entry
	                              </button>
	                            )}
	                          </label>
                          <label className="project-redaction-rule-field">
                            <span>Event Timeout (minutes)</span>
                            <input
                              className="settings-input"
                              type="number"
                              min="0"
                              max="360"
                              value={webhook.timeout_minutes ?? ''}
                              onChange={event => updateNotificationWebhook(index, {
                                timeout_minutes: event.target.value.trim() === '' ? undefined : Number(event.target.value),
                              })}
                              placeholder="15"
                            />
                          </label>
                          <label className="project-redaction-rule-field project-redaction-rule-field-wide">
                            <span>Entity Selector</span>
                            <input
                              className="settings-input"
                              value={webhook.entity_selector || ''}
                              onChange={event => updateNotificationWebhook(index, { entity_selector: event.target.value })}
                              placeholder="type(SERVICE),entityName.equals(CheckoutService)"
                            />
                          </label>
                        </div>
                      )}

                      <div className="setting-row">
                        <div className="setting-info">
                          <span className="setting-label">Enabled</span>
                          <span className="setting-desc">Disable the target without removing the stored destination.</span>
                        </div>
                        <label className="settings-toggle">
                          <input
                            type="checkbox"
                            checked={webhook.enabled}
                            onChange={event => updateNotificationWebhook(index, { enabled: event.target.checked })}
                          />
                          <span>{webhook.enabled ? 'Enabled' : 'Disabled'}</span>
                        </label>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="new-test-inline-note">No webhook destinations configured yet.</p>
              )}

              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={addNotificationWebhook}>
                  Add Webhook
                </button>
              </div>
            </div>
              </div>
            </section>
          </div>
        </div>
          </div>

          <div className="settings-section-footer">
            <button className="btn btn-danger-outline btn-sm" type="button" onClick={() => setConfirmingResetAll(true)} disabled={!canManageSystem || settingsMutationBusy}>
              Reset All Settings to Defaults
            </button>
          </div>
        </div>
      </div>

      <SettingsConfirmDialog
        open={confirmDeleteApp}
        title="Delete Application"
        confirmLabel="Delete Application"
        onCancel={() => setConfirmDeleteApp(false)}
        onConfirm={deleteApplication}
      >
        <p>Permanently delete <strong>{activeApplication?.name}</strong>? All associated releases and test results will also be removed.</p>
        <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={Boolean(confirmDeleteRelease)}
        title="Delete Release"
        confirmLabel="Delete Release"
        onCancel={() => setConfirmDeleteRelease(null)}
        onConfirm={() => {
          if (confirmDeleteRelease) void deleteRelease(confirmDeleteRelease.id, confirmDeleteRelease.name)
        }}
      >
        <p>Permanently delete release <strong>{confirmDeleteRelease?.name}</strong>?</p>
        <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={Boolean(confirmArchiveRelease)}
        title="Archive Release"
        confirmLabel="Archive Release"
        confirmClassName="btn btn-primary"
        onCancel={() => setConfirmArchiveRelease(null)}
        onConfirm={() => {
          if (confirmArchiveRelease) void archiveRelease(confirmArchiveRelease.id, confirmArchiveRelease.name)
        }}
      >
        <p>Archive release <strong>{confirmArchiveRelease?.name}</strong>? Archived releases are read-only and cannot be used for new tests.</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={confirmApplyRetention}
        title="Apply Archive Retention"
        confirmLabel="Apply Retention"
        onCancel={() => setConfirmApplyRetention(false)}
        onConfirm={applyArchiveRetention}
      >
        <p>Apply archive retention pruning for the current plan? This will permanently remove {archiveRetentionPlan?.summary.candidate_count || 0} archive{(archiveRetentionPlan?.summary.candidate_count || 0) === 1 ? '' : 's'}.</p>
        <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={Boolean(confirmDeletePool)}
        title="Delete Data Pool"
        confirmLabel="Delete Data Pool"
        confirmDisabled={dataPoolCatalogMutationBusy}
        onCancel={() => setConfirmDeletePool(null)}
        onConfirm={() => {
          if (confirmDeletePool) void deleteDataPool(confirmDeletePool)
        }}
      >
        <p>Delete data pool <strong>{confirmDeletePool?.name}</strong>? Data pools bound to scenarios must be unbound first.</p>
        <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={Boolean(confirmDeleteRow)}
        title="Delete Data Row"
        confirmLabel="Delete Row"
        confirmDisabled={dataPoolRowDeleteBusy}
        onCancel={() => setConfirmDeleteRow(null)}
        onConfirm={() => {
          if (confirmDeleteRow) void deleteDataPoolRow(confirmDeleteRow)
        }}
      >
        <p>Permanently delete this data row?</p>
      </SettingsConfirmDialog>

      <SettingsConfirmDialog
        open={confirmingResetAll}
        title="Reset All Settings"
        confirmLabel="Reset All Settings"
        confirmDisabled={settingsMutationBusy}
        onCancel={() => setConfirmingResetAll(false)}
        onConfirm={resetSettings}
      >
        <p>This will reset every system setting to its factory default, including thresholds, capacity planning, notifications, and auth configuration.</p>
        <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
      </SettingsConfirmDialog>
    </div>
  )
}
